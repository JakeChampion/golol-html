# golol-html - Go bindings for lol-html

Modern, idiomatic Go bindings for [cloudflare/lol-html](https://github.com/cloudflare/lol-html),
a streaming HTML rewriter with CSS-selector-based content handlers.

- Module path: `github.com/JakeChampion/golol-html`
- Package name: `lolhtml`
- Upstream pinned at: **lol_html v3.0.1** (`608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c`, 2026-07-29),
  C API crate `lol_html_c_api` **v1.4.0**

## Goals

1. `go get` works with no Rust toolchain and no C dependencies beyond a C compiler.
2. Idiomatic Go: Go closures as handlers, `io.Writer` sinks, `error` returns, range-over-func
   iterators. Never expose `unsafe.Pointer` or C types in the public API.
3. Full parity with the C API surface in v1 - no gaps to fill via breaking changes later.
4. Memory-safe by construction: no way for user code to hold a pointer past its valid lifetime.

## Non-goals

- Pure-Go HTML parsing. Use `golang.org/x/net/html` for that.
- A wasm/`CGO_ENABLED=0` backend in v1. Left as a future option; see "Deferred".

## Decisions (locked)

### D1. Linking: cgo + vendored prebuilt static archives

Prebuilt `liblolhtml.a` per platform, committed under `internal/lib/<goos>_<goarch>/`, selected
by `//go:build` constrained files each carrying a `#cgo LDFLAGS` line. Consumers need only a C
compiler (cgo requirement), never Rust.

Rejected alternatives:
- *Build from source at install time*: Go cannot run cargo during `go build`; breaks `go get`.
- *wasm + wazero*: no cgo and trivial cross-compilation, but costs a host/guest boundary
  crossing plus copies on every handler call. Deferred, not discarded.

Measured costs (darwin/arm64, `--release`, `panic = "abort"`, `lto = true`):

| Artifact                        | Size     |
|---------------------------------|----------|
| `liblolhtml.a` unstripped       | 21.0 MB  |
| `liblolhtml.a` after `strip -S -x` | 14.8 MB |
| gzipped (approximates git/proxy cost) | 5.97 MB |
| Added to a linked Go binary     | ~2.0 MB  |

The archive is large but the linker discards unused Rust std objects, so consumer binaries grow
by only ~2 MB (4.27 MB vs a 2.26 MB pure-Go baseline). Three platforms cost roughly 18 MB
compressed in the repo. If that becomes a complaint, split each platform into its own Go module
under `lib/` - build constraints prune un-imported modules from a `go build`, though not from
`go mod download`. Git LFS is NOT an option: the module proxy would serve LFS pointer files.

Trust mitigation: archives are built in CI from the pinned upstream commit, `SHA256SUMS` is
committed alongside, and `make verify-native` reproduces and diffs them locally.

### D2. Platforms for v1

`darwin/arm64`, `linux/amd64`, `linux/arm64` (glibc).

Deferred: linux/musl (Alpine needs a separate static build), windows, darwin/amd64.

### D3. API shape: idiomatic Go, thin over C

```go
w, err := lolhtml.NewWriter(os.Stdout,
    lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
        href, _ := e.Attribute("href")
        return e.SetAttribute("href", rewrite(href))
    }),
    lolhtml.OnComment("*", func(c *lolhtml.Comment) error {
        c.Remove()
        return nil
    }),
)
io.Copy(w, resp.Body)
err = w.Close()

for name, val := range e.Attributes() { ... }   // iter.Seq2[string, string]

out, err := lolhtml.Rewrite(html, handlers...)  // convenience over the above
```

- Handlers return `error`. `nil` maps to `LOL_HTML_CONTINUE`; non-nil is stashed and maps to
  `LOL_HTML_STOP`, then surfaces from `Write`/`Close` - the caller's own error, not a generic
  C one.
- Panics in a handler are recovered at the C boundary (they must not unwind through Rust),
  converted to an error, and re-panicked on the calling goroutine from `Write`/`Close`.
- Options are a single variadic `Option` list: handler registrations plus `WithEncoding`,
  `WithMemorySettings`, `WithStrict`, `WithGracefulBailOut`.

### D4. Scope: full C API parity in v1

All 8 rewritable unit types (Element, EndTag, Comment, TextChunk, Doctype, DocumentEnd,
Attribute, AttributesIterator), streaming handlers, source locations, user data, memory
settings, graceful bail-out, non-UTF8 encodings, strict mode.

`unstable_lol_html_rewriter_build_with_esi_tags` is exposed as `WithESITags` and explicitly
documented as unstable.

Per-unit user data is implemented (`SetUserData`/`UserData` on Element, Comment, TextChunk,
Doctype), storing a `cgo.Handle` in lol-html's `void *`. It is close to redundant in Go - the C
API needs it because C has no closures, whereas an end-tag handler registered inside an element
handler simply closes over what it needs - but it is part of the surface, so it is bound. Handles
are tracked in a map on `native` rather than the handler slice, because user data can be replaced
mid-rewrite and a `cgo.Handle` must be deleted exactly once.

Content insertion takes an explicit `ContentType` argument (`Text` or `HTML`) rather than
splitting each method in two (`BeforeText`/`BeforeHTML`). This mirrors lol-html's own
`ContentType` and keeps the surface at 15 insertion methods instead of 30. The escaping
distinction is too important to leave implicit in a bool.

## Critical implementation constraints

These are verified against upstream source, not assumed.

### C1. `take_last_error` is a Rust `thread_local!`

`c-api/src/errors.rs` stores the last error in `thread_local! { static LAST_ERROR }`. A cgo call
is pinned to one OS thread only for its own duration, so calling `lol_html_rewriter_write` and
`lol_html_take_last_error` as two separate cgo calls can read the error on the wrong thread and
silently get nothing.

**Therefore every fallible C function is wrapped by a C shim that performs the call and, on
failure, `take_last_error()` within the same single cgo call**, returning the message via an
out-parameter:

```c
int golol_write(lol_html_rewriter_t *rw, const char *chunk, size_t len, lol_html_str_t *err) {
    int rc = lol_html_rewriter_write(rw, chunk, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}
```

### C2. Unit pointers are valid only during handler execution

The header warns pointers "should never be leaked outside of handlers". Go wrapper values
(`*Element`, `*Comment`, ...) are therefore invalidated when the callback returns: every method
checks a validity flag and returns `ErrDetached` if the value escaped. Wrappers are pooled and
reused to keep allocation off the hot path.

### C3. cgo callback signatures must come from `_cgo_export.h`

Hand-declaring `//export`ed Go callbacks in our own header conflicts with cgo's generated
prototypes (cgo emits `char *` where lol-html declares `const char *`). `shim.c` includes
`_cgo_export.h` and defines static C trampolines that cast, keeping the C compiler quiet:

```c
static void sink_trampoline(const char *chunk, size_t len, void *ud) {
    golol_sink_cb((char *)chunk, len, ud);
}
```

### C4. Go closures cross the boundary as `cgo.Handle`

`runtime/cgo.Handle` (a `uintptr`) is passed to shims typed `uintptr_t`, and the shim casts to
`void *` for lol-html. This avoids `unsafe.Pointer(uintptr(...))` round-trips that violate the
pointer-passing rules and trip `go vet`.

### C5. Lifetimes

A `Selector` must outlive every builder that accepted it; a builder may be freed as soon as its
rewriters are built. `NewWriter` therefore parses selectors, builds, then frees the builder and
selectors in that order. `runtime.AddCleanup` (Go 1.24+) is a backstop for a `Writer` the caller
forgets to `Close`; it is a leak guard, not the documented path.

### C6. Generics must be parameterised over the pointer type

lol-html's structs are opaque (`typedef struct lol_html_Element lol_html_element_t;`), so cgo
sees an *incomplete* type, and Go rejects an incomplete type as a type argument:

    cannot use incomplete (or unallocatable) type as a type argument

A pointer to an incomplete type is complete and comparable, so every internal generic is
parameterised over the pointer: `unit[P comparable]` instantiated as `unit[*C.lol_html_element_t]`,
not `unit[C.lol_html_element_t]`. Detachment compares against the zero value of `P`, which is why
the constraint is `comparable` rather than `any`.

### C7. cgo functions are not first-class values

Referring to `C.golol_element_before` without calling it yields an `unsafe.Pointer`, not a func
value, so the generic helpers cannot accept a shim directly. Each shim is wrapped once in a Go
closure in `cfuncs.go`, which keeps the boilerplate in one labelled file and every method on the
unit types a single line.

### C8. The output sink borrows rather than copies

`io.Writer` requires that implementations not retain `p`, so the sink hands the destination a
`unsafe.Slice` over lol-html's own buffer instead of copying it. This was measurably the dominant
allocation cost: copying every output chunk cost 2224 allocations per rewrite on the
`SetAttribute` benchmark against 423 after the change. Anything that must outlive the callback
still copies (`TextChunk.Text`, every `lol_html_str_t` getter).

### C9. Rust toolchain floor

`lol_html_c_api` declares `rust-version = 1.89` and `edition = 2024`. The build script pins an
explicit toolchain; this machine's `stable` is 1.87, so `cargo +1.95.0` was used to validate.

## Layout

```
go.mod
SPEC.md  README.md  LICENSE  LICENSE-lol-html
lolhtml.go          package docs, Rewrite convenience
rewriter.go         Writer (io.WriteCloser), Option, settings
handler.go          OnElement / OnComment / OnText / OnDoctype / OnDocumentEnd
callbacks.go        //export'ed Go callbacks (no C definitions in preamble)
element.go endtag.go comment.go text.go doctype.go docend.go attribute.go
selector.go streaming.go errors.go
shim.h shim.c       C trampolines + single-call error retrieval
link_darwin_arm64.go link_linux_amd64.go link_linux_arm64.go
internal/include/lol_html.h
internal/lib/<goos>_<goarch>/liblolhtml.a
scripts/build-native.sh
.github/workflows/{ci.yml,native.yml}
```

## Verification

Implemented:

- `rewrite_test.go` - table-driven behaviour tests across all handler kinds, attributes,
  introspection, source locations, streaming sinks and end tags.
- `errors_test.go` - selector errors, handler error propagation and unwrapping, panic
  re-raising, detachment, poisoning, destination-writer failure, memory limits, graceful
  bail-out, encodings, and a 32-goroutine concurrency test that specifically checks native
  error *messages* arrive (an empty message would mean C1 regressed).
- `fuzz_test.go` - `FuzzRewrite` asserts chunk-invariance of the output, plus a cleanup test
  that drops 200 unclosed Writers and forces GC.
- `bench_test.go` - six benchmarks over a generated 16 KB page.
- `go test -race` clean.

Still worth adding:

- Port the remaining upstream C tests (`c-api/c-tests/src/*.c`) that have no Go analogue.
- Differential test against `golang.org/x/net/html` for parse-equivalence on a corpus.

### Measured findings

Two assumptions written into the first draft of this spec turned out to be wrong, and the tests
now encode the real behaviour:

- **Attribute escaping.** lol-html escapes `&` and `"` in attribute values but not `>`, which is
  correct: a bare `>` cannot terminate a quoted value.
- **Text chunk counts are not chunk-invariant.** lol-html splits text at input chunk boundaries,
  so writing byte-at-a-time produces more text chunks than one big write. Output is invariant;
  handler invocation counts are not. `FuzzRewrite` compares output always, and invocation counts
  only for structural handlers.

**Writes are quadratic at byte granularity.** While the rewriter is buffering an unclosed tag,
each write rescans the pending buffer: 4 KB byte-at-a-time takes 4.4 ms against 43.7 ms for
16 KB. This is upstream behaviour, not a binding artefact, but it shaped the fuzz harness, which
caps input at 8 KB and bounds the write count above 1 KB - without that the fuzzer stalled to
0 execs/sec within seconds as it grew inputs. Documented in the README as a user-facing caveat.

Graceful bail-out, measured on v3.0.1 with a 64-byte cap and 4112 bytes of input:

| `GracefulBailOut` | Bytes reaching the sink |
|---|---|
| `false` | 0 - response broken |
| `true`  | 4112 - every input byte preserved |

Benchmarks, Apple M3 Pro, darwin/arm64, 16 KB page with 200 links:

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|---|---|---|---|---|
| Passthrough | 21986 | 752.5 | 440 | 14 |
| SetAttribute | 197037 | 84.0 | 7042 | 423 |
| ReadAttributes | 186676 | 88.6 | 17179 | 1023 |
| TextHandler | 138216 | 119.7 | 11843 | 623 |
| StreamingAppend | 216391 | 76.5 | 28335 | 1426 |
| ChunkedWrite | 245348 | 67.4 | 7250 | 435 |

Passthrough is the floor. Everything else is dominated by crossing into Go once per match, so
throughput tracks handler invocation count rather than document size.

## Deferred

- wasm/wazero backend for `CGO_ENABLED=0`.
- linux/musl, windows, darwin/amd64.
- Bail-out handlers (`Settings::append_bail_out_handler`) - Rust-only upstream, no C API yet.
- `graceful_bail_out_on_content_handler_error` - Rust-only upstream.

## Licensing

lol-html is BSD-3-Clause. Because we distribute its compiled object code, `LICENSE-lol-html`
reproduces upstream's notice. The binding code is BSD-3-Clause to match.

## Notes

- A one-off `ld: warning: ... malformed LC_DYSYMTAB` was observed once during development and
  traced to a stale build cache after swapping archives underneath it, not to `strip`. Three
  clean-cache race links of each variant produced zero warnings. Stripping is kept.
- `make verify` rebuilds the host archive and diffs it. Rust builds are not bit-identical across
  toolchain patch versions, so a mismatch needs the same `RUST_TOOLCHAIN` before it means
  anything.

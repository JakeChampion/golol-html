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

Archives are built with `cargo rustc --crate-type staticlib`, not `cargo build`. The c-api crate
declares staticlib, cdylib and rlib; building only the one we need lets LTO prune far harder and
removes the need for a linker for the target, which is also what makes cross-building work with
nothing but `rustup target add`. Measured on darwin/arm64:

| Artifact                              | `cargo build` | `cargo rustc --crate-type staticlib` |
|---------------------------------------|--------------|--------------------------------------|
| unstripped                            | 21.0 MB      | 6.51 MB                              |
| after `strip -S -x`                   | 15.57 MB     | 2.73 MB                              |
| gzipped (approximates git/proxy cost)  | 5.97 MB      | 0.83 MB                              |
| added to a linked Go binary           | ~2.0 MB      | ~1.06 MB                             |

Final CI-built sizes, stripped, with this recipe: darwin/arm64 2.73 MB, linux/amd64 4.22 MB,
linux/arm64 4.24 MB - about 11.2 MB of vendored archives in total, against roughly 52 MB before
the crate-type change. On linux/amd64 the change alone is 18.31 MB against 8.98 MB unstripped. Restricting the crate type
is strictly better on every axis measured, and the smaller archive links a smaller binary
(3.44 MB against a 2.37 MB pure-Go baseline) because the pruning happens before the Go linker
sees it. If that becomes a complaint, split each platform into its own Go module
under `lib/` - build constraints prune un-imported modules from a `go build`, though not from
`go mod download`. Git LFS is NOT an option: the module proxy would serve LFS pointer files.

Trust mitigation: archives are built in CI from the pinned upstream commit, `SHA256SUMS` is
committed alongside, and `make verify-native` reproduces and diffs them locally.

### D2. Platforms

`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64` (glibc), `linux/amd64` and
`linux/arm64` (musl, via `-tags musl`), `windows/amd64`.

Deferred: linux/arm, 32-bit anything, windows/arm64.

**Linker flags come from `rustc --print native-static-libs`**, not from guesswork. That is how the
original glibc set was found to be incomplete: it named `-lm -ldl -lpthread` where rustc asks for
`-lgcc_s -lutil -lrt -lpthread -lm -ldl -lc`. It linked anyway because modern glibc folds `rt`
into libc and the missing libraries happened to be unreferenced - fragile rather than correct.

| Target | `native-static-libs` | Shipped |
|---|---|---|
| darwin (both) | `-liconv -lSystem -lc -lm` | `-liconv` only |
| linux gnu (both) | `-lgcc_s -lutil -lrt -lpthread -lm -ldl -lc` | as given |
| linux musl (both) | `-lunwind -lc` | `-lc` only |
| windows-gnu | `-lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp` | as given |

Two deliberate deviations, both measured rather than assumed:

- **darwin** drops `-lSystem -lc -lm`. macOS links libSystem implicitly, and naming it again makes
  `ld` warn about a duplicate library on every consumer build. Verified that `-liconv` alone links
  cleanly, as does no flag at all.
- **musl** drops `-lunwind`. The c-api crate builds with `panic = "abort"`, so nothing references
  the unwinder, and Alpine does not ship libunwind - requiring it would burden every Alpine user
  for no benefit. This is an assumption the Alpine job in CI exists to falsify; if it ever fails
  on an undefined `_Unwind_*`, add the flag and document the extra package.

**musl cannot be detected by build constraint.** `linux/amd64` is `linux/amd64` whether the C
library is glibc or musl, so the choice is explicit via the `musl` build tag, with the glibc files
constrained `!musl`. Verified with `go list -tags musl` that the tag selects the musl archive and
its flags, and that its absence selects glibc.

**All seven archives are cross-built on one Linux runner.** Restricting the crate type to
staticlib means no linker for the target is involved, so cross-compiling needs nothing but
`rustup target add` - verified locally by building all seven from macOS/arm64 and confirming the
object formats (Mach-O x86_64, ELF musl on both arches, COFF amd64). One toolchain for every
archive beats seven differently-provisioned runners. Each archive is then linked and tested on the
platform it targets before it can reach a pull request.

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

- `parity_test.go` - the corners of the upstream C suite that the behaviour tests missed: every
  streaming insertion, user data on all four units that carry it, source locations checked by
  slicing the input, doctype PUBLIC/SYSTEM identifiers, attribute-present-but-empty, end-tag
  handler ordering and clearing, and handle-release tests (see below).
- `differential/` - a separate module comparing against `golang.org/x/net/html` over a 20-document
  corpus: passthrough preserves meaning, passthrough is byte-identical, script removal and
  attribute setting match the same edit done by tree surgery, and the reported text chunks
  reconstruct the document text.

`differential/` is its own module so the root stays dependency-free: a test-only requirement on
`golang.org/x/net` would otherwise appear in the module graph of every consumer. The cost is that
`go test ./...` at the root does not run it, so CI and `make test` invoke it explicitly.

### Handle-release tests

Upstream asserts its drop-callback contract by counting calls. The Go equivalent is stronger: a
handler payload is reachable only through the cgo handle table, so attaching `runtime.AddCleanup`
to a captured value and watching it become collectable proves the handle was actually deleted.
Three cases are covered - handler handles released by `Close`, streaming handles released by
lol-html's drop callback, and streaming handles released when a rewrite *aborts* before the
streamed content was ever emitted (verified: lol-html honours drop even then).

One path is deliberately untested because it is unreachable: `withStream` deletes the handle
itself if lol-html rejects a streaming handler, but the C API only rejects a NULL handler struct
and the shim never passes one. Probed on v3.0.1, every `Stream*` method succeeds even on a void
`<br>` or a self-closing `<circle/>`.

### Measured findings

Three assumptions written into the first draft of this spec turned out to be wrong, and the tests
now encode the real behaviour:

- **Attribute escaping.** lol-html escapes `&` and `"` in attribute values but not `>`, which is
  correct: a bare `>` cannot terminate a quoted value.
- **Character references are never decoded.** Found by the differential test, which is exactly
  what it was for. `TextChunk.Text`, `Comment.Text` and attribute values all return raw source:
  the href of `<a href="?a=1&amp;b=2">` is `?a=1&amp;b=2`. The binding's documentation claimed
  the opposite ("with character references already decoded", "the attribute value, unescaped"),
  which would have quietly broken anyone comparing an href against a decoded Go string. The
  behaviour is right - a rewriter must be able to re-emit what it read, and writing a value back
  unchanged round-trips correctly because `SetAttribute` escapes - so the docs were corrected and
  `TestCharacterReferencesAreNotDecoded` pins it down.
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

## CI findings

First CI run (32459538116) went red in five jobs. Two distinct causes:

**1. The gofmt step failed on macOS regardless of formatting.** The idiom
`gofmt -l . | tee /dev/stderr | (! read)` is not portable. GitHub runs steps with `bash -e`, and
macOS runners ship `/bin/bash` 3.2, where `set -e` aborts the script on a *non-final* pipeline
element even though the pipeline's own status is 0. Verified locally:

| Shell | `bash -e -c 'true \| tee /dev/stderr \| (! read)'` |
|---|---|
| `/bin/bash` 3.2 (macOS) | exits **1** - false failure |
| bash 5.3 | exits 0 - correct |

Worse, `tee /dev/stderr` did not reach the runner log, so the step failed while printing nothing
at all. gofmt itself was innocent: eight toolchains from go1.25.8 through go1.26.5, including
the runner's exact go1.25.13, all report the tree clean.

Replaced everywhere (ci.yml and the Makefile) with a form that is portable and actually prints
the diff:

    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then echo "$unformatted"; gofmt -d .; exit 1; fi

**2. The three Linux jobs and `consume` failed because no Linux archive exists.** This is the
known gap, not a regression: only `internal/lib/darwin_arm64/liblolhtml.a` is committed, so
`ld` cannot find `internal/lib/linux_amd64/liblolhtml.a`. The `native` workflow exists to
produce them; until it runs, those jobs are expected to be red. The archive-presence check now
emits a `::error::` naming the workflow to run instead of a bare `test -f` failure.

Second run (32460780995) reduced this to one failure, and the native run (32460792086) to one:

**3. `-fuzz` cannot take multiple packages.** The step ran
`go test -fuzz FuzzRewrite ./...`, and `./...` matches both the root package and
`examples/rewrite-url`, so Go refused with `cannot use -fuzz flag with multiple packages`. It was
never caught locally because the local runs used `.`. Lesson applied: run CI commands verbatim,
not a close paraphrase.

**4. `upload-artifact` strips the least common ancestor of its paths.** Uploading
`internal/lib/<target>/liblolhtml.a` together with `internal/include/lol_html.h` produces an
artifact containing `lib/<target>/...` and `include/...`, with `internal/` removed. The collect
job assumed the original paths and failed on `cp`. It now locates each archive with
`find -name liblolhtml.a`, which holds regardless of how the ancestor is computed, and reports
the directory listing if it finds nothing.

What the runs did confirm:

- The gofmt fix works: macOS is green.
- **The Linux `LDFLAGS` (`-lm -ldl -lpthread`) are correct.** Both Linux jobs build, test and
  pass `-race` against the vendored archives. This was the last unverified guess in D1.
- The cross-built staticlibs (`cargo rustc --crate-type staticlib`) link and pass on real Linux,
  so the cross recipe in `scripts/build-native.sh` is sound.
- `consume` passes: a module can depend on this one and build with cargo stripped from `PATH`.
- All three `native` build jobs pass, including `go test -race` against freshly built archives.

Also cleaned up: actions bumped to `checkout@v5` / `setup-go@v6` (v4/v5 are Node 20 and now
warn), `cache: false` on setup-go since a module with no dependencies has no `go.sum` to key a
cache on, checksum verification made portable across `sha256sum` and `shasum`, and a misordered
`setup-go` step in native.yml that was labelled as the smoke test.

**5. The `native` workflow could not open its own pull request** - resolved. This was a
repository setting rather than code: `GitHub Actions is not permitted to create or approve pull
requests`. The branch is pushed regardless, so the archives were available on `native/rebuild`
and PR #1 was opened by hand and merged. The setting is now enabled, so subsequent upstream
bumps are self-service. A fork without it gets the branch and a failed PR step.

Note that pushing to `native/rebuild` does not itself run `ci.yml`, which triggers on pushes to
`main` and on pull requests. Opening the PR is therefore what validates CI-built archives before
they land, and is worth doing rather than pushing archives straight to `main`.

The action reuses and force-updates `native/rebuild`, so the branch is expected to linger between
runs; it is not litter to clean up.

Rust builds are not bit-reproducible here: two `native` runs of the same commit produced darwin
archives differing by about 1 KB. So re-running the workflow to test something will generally
produce a real diff and therefore a real PR, rather than a no-op.

## Notes

- Stripping is verified rather than trusted: `strip_archive` counts exported entry points before
  and after and fails if the number changes. This caught Apple's `strip -S -x` dropping
  `__.SYMDEF`, the archive symbol index - benign, since all 97 entry points survived and linkers
  cope, but the archive is now put back in conventional shape with `ranlib`. The check tolerates a
  zero count, because that means no available `nm` could read the archive rather than that the
  archive is empty.
- A one-off `ld: warning: ... malformed LC_DYSYMTAB` was observed once during development and
  traced to a stale build cache after swapping archives underneath it, not to `strip`. Three
  clean-cache race links of each variant produced zero warnings. Stripping is kept.
- `make verify` rebuilds the host archive and diffs it. Rust builds are not bit-identical across
  toolchain patch versions, so a mismatch needs the same `RUST_TOOLCHAIN` before it means
  anything.

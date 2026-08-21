# golol-html

Go bindings for [lol-html](https://github.com/cloudflare/lol-html), Cloudflare's
streaming HTML rewriter.

lol-html parses and rewrites HTML in a single pass without building a document
tree, so memory use is bounded by the largest element it has to buffer rather
than by document size. That makes it a good fit for rewriting HTTP responses of
unknown length as they stream past.

```go
w, err := lolhtml.NewWriter(os.Stdout,
	lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		href, _ := e.Attribute("href")
		return e.SetAttribute("href", absolutise(href))
	}),
	lolhtml.OnComment("*", func(c *lolhtml.Comment) error {
		c.Remove()
		return nil
	}),
)
if err != nil {
	return err
}
if _, err := io.Copy(w, resp.Body); err != nil {
	return err
}
return w.Close()
```

## Install

```
go get github.com/JakeChampion/golol-html
```

No Rust toolchain is required. Prebuilt static archives are vendored in the
module, so a C compiler (which cgo needs anyway) is enough.

**Supported platforms:**

| Platform | Notes |
|---|---|
| `darwin/arm64`, `darwin/amd64` | |
| `linux/amd64`, `linux/arm64` | glibc |
| `linux/amd64`, `linux/arm64` (musl) | build with `-tags musl`, see below |
| `windows/amd64` | cgo needs the mingw gcc that ships with Go on Windows |

Anything else fails at compile time with a message naming the gap rather than an
opaque linker error. `CGO_ENABLED=0` is not supported.

### Alpine and musl

Go build constraints cannot tell musl from glibc - both are `linux/amd64` - so
which C library you are on has to be stated:

```
go build -tags musl ./...
```

Without the tag an Alpine build picks the glibc archive and fails at link time
with missing glibc symbols. Passing it on a glibc system fails the mirror-image
way. Both are loud rather than subtle.

Pinned to **lol-html v3.0.1** (C API crate 1.4.0).

## Usage

### Streaming

[`NewWriter`](https://pkg.go.dev/github.com/JakeChampion/golol-html#NewWriter)
returns an `io.WriteCloser`. Chunk boundaries never change what handlers see, so
input can arrive however the network delivers it. `Close` finishes the document
and flushes the tail: skip it and the output is truncated.

### In memory

```go
out, err := lolhtml.RewriteString(`<a href="/x">link</a>`,
	lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	}))
// <a href="/x" rel="noopener">link</a>
```

### Handlers

| Option | Fires for |
|---|---|
| `OnElement(selector, fn)` | start tags matching the selector |
| `OnComment(selector, fn)` | comments inside matching elements |
| `OnText(selector, fn)` | text chunks inside matching elements |
| `OnDoctype(fn)` | the doctype declaration |
| `OnDocumentComment(fn)` | every comment in the document |
| `OnDocumentText(fn)` | every text chunk in the document |
| `OnDocumentEnd(fn)` | once, after all content |

`Element.OnEndTag` registers a handler that runs when the element closes, which
is how you act on an element after seeing its content.

Text arrives in chunks with no guaranteed boundaries: one text node can be
reported as several chunks, and the last one is often empty. Accumulate across
chunks when you need whole nodes, and check `IsLastInTextNode`.

### Inserting content

Every insertion takes a `ContentType`. `lolhtml.Text` escapes the content, which
is what you want for untrusted values; `lolhtml.HTML` inserts raw markup.

```go
e.Before("<b>x</b>", lolhtml.Text)  // &lt;b&gt;x&lt;/b&gt;
e.Before("<b>x</b>", lolhtml.HTML)  // <b>x</b>
```

For content that is large or produced incrementally, the `Stream*` methods take
a callback invoked at the point the content is needed, so nothing has to be
assembled in memory first:

```go
e.StreamAppend(func(s *lolhtml.Sink) error {
	_, err := io.Copy(s.AsWriter(lolhtml.HTML), bigTemplate)
	return err
})
```

## Two things to know

**Units do not outlive their handler.** lol-html only guarantees an `*Element`,
`*Comment`, `*TextChunk`, `*Doctype`, `*EndTag` or `*DocumentEnd` for the
duration of the call. The wrapper is detached on return, so a retained value
returns `ErrDetached` rather than reading freed memory. Copy out what you need:

```go
lolhtml.OnElement("img", func(e *lolhtml.Element) error {
	src, _ := e.Attribute("src")   // fine: a Go string
	found = append(found, e)       // useless: detached once this returns
	return nil
})
```

**A failed rewriter cannot be reused.** A handler returning an error stops the
rewrite; the error surfaces from the `Write` or `Close` that was running, wrapped
in a `*HandlerError` you can unwrap. lol-html cannot resume afterwards, so the
`Writer` is poisoned and later writes return `ErrPoisoned`. A handler that panics
does not unwind through Rust: it is caught at the boundary and re-raised on the
goroutine that called `Write` or `Close`.

A `Writer` is not safe for concurrent use, but independent `Writer`s on separate
goroutines are fine.

## Memory limits

```go
lolhtml.WithMemorySettings(lolhtml.MemorySettings{
	MaxMemory:       64 << 10,
	GracefulBailOut: true,
})
```

Exceeding `MaxMemory` fails the rewrite with a `*NativeError` whose
`MemoryLimitExceeded()` reports true. What happens to the response depends on
`GracefulBailOut`; measured on v3.0.1 with a 64-byte cap and a 4112-byte input:

| `GracefulBailOut` | Bytes reaching the sink |
|---|---|
| `false` (default) | 0 - the response is broken |
| `true` | all 4112, rewritten up to the bail-out boundary and verbatim after it |

With it on you can keep serving by writing subsequent bytes straight to your own
sink, bypassing the now-unusable rewriter.

## Performance

`go test -bench .` on an Apple M3 Pro, darwin/arm64, rewriting a 16 KB generated
page with 200 links. Run it on your own hardware before relying on these.

```
BenchmarkPassthrough-12        21986 ns/op   752.47 MB/s     440 B/op    14 allocs/op
BenchmarkSetAttribute-12      197037 ns/op    83.96 MB/s    7042 B/op   423 allocs/op
BenchmarkReadAttributes-12    186676 ns/op    88.62 MB/s   17179 B/op  1023 allocs/op
BenchmarkTextHandler-12       138216 ns/op   119.70 MB/s   11843 B/op   623 allocs/op
BenchmarkChunkedWrite-12      245348 ns/op    67.43 MB/s    7250 B/op   435 allocs/op
```

Passthrough is the floor - lol-html plus cgo and sink overhead, with no handlers.
The rest is dominated by crossing into Go once per match, so throughput tracks
how many handler invocations a document produces, not its size.

### Write in reasonable chunks

Feeding the rewriter a byte at a time is quadratic while it is buffering an
unclosed tag, because each write rescans the pending buffer. Measured on the same
machine with a pathological unclosed-tag input:

| Input | Byte-at-a-time |
|---|---|
| 4 KB | 4.4 ms |
| 16 KB | 43.7 ms |

`io.Copy` and normal network-sized reads are all well clear of this. It only bites
if you deliberately write tiny chunks.

Linking adds about 1 MB to a binary: 3.44 MB against a 2.37 MB pure-Go baseline
on darwin/arm64.

## The vendored archives

`internal/lib/<goos>_<goarch>/liblolhtml.a` is built by CI from the pinned
upstream commit, stripped, and committed with a `SHA256SUMS`. Archives are built
with `cargo rustc --crate-type staticlib` rather than `cargo build`: restricting
the crate type lets LTO prune much harder, which on darwin/arm64 is 2.73 MB
against 15.57 MB and also yields a smaller linked binary. To rebuild and
compare:

```
make verify        # host platform: rebuild, diff against what is committed
make native-all    # every supported platform (needs the cross toolchains)
```

Rust builds are not bit-identical across toolchain patch versions, so a mismatch
is worth investigating with the same `RUST_TOOLCHAIN` before assuming the worst.

### Provenance

Each archive carries a signed provenance attestation naming the workflow run and
commit that produced it:

```
make attest-verify

# or one file
gh attestation verify internal/lib/darwin_arm64/liblolhtml.a \
  --repo JakeChampion/golol-html
```

This is a stronger claim than `SHA256SUMS`, which only shows a file has not
rotted - anyone who can push could update an archive and its checksum together.
The attestation is issued to the workflow run, so it cannot be reissued by
someone with push access alone.

GitHub does not support attestations for user-owned private repositories, so
while this repository is private the workflow skips that step with a notice and
`make attest-verify` will find nothing to verify. It starts working on its own
if the repository is made public.

Adding a platform means adding a target to `scripts/build-native.sh`, a
`link_<goos>_<goarch>.go` file, and matrix entries in both workflows. Linker
flags come from `rustc --print native-static-libs` for the target rather than
guesswork - that is how the glibc set was found to be missing `-lgcc_s -lutil
-lrt`, which only worked by accident because modern glibc folds `rt` into
libc.

## Not supported yet

- `CGO_ENABLED=0`. A wasm backend on wazero would allow it, at the cost of a
  host/guest crossing per handler call.
- linux/arm, 32-bit platforms, windows/arm64.
- Bail-out handlers and `graceful_bail_out_on_content_handler_error`, which
  upstream exposes only through its Rust API.

## Licence

BSD-3-Clause, matching lol-html. This module distributes compiled lol-html code;
its notice is reproduced in `LICENSE-lol-html`.

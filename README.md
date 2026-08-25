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

**Requirements: Go 1.24 or later, cgo enabled, and a C compiler.** The floor is
Go 1.24 because the library uses `runtime.AddCleanup` to release native
resources if a `Writer` is dropped without being closed; attribute iteration
also returns an `iter.Seq2`, which needs Go 1.23. A CI job builds and tests
against the oldest supported version so this stays true.

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

Handlers of the same kind run in the order you registered them, and each sees
what the previous one did. The exception is between kinds: a selector handler
always runs before a document handler on the same unit, so `OnComment` beats
`OnDocumentComment` and `OnText` beats `OnDocumentText` even when the document
one was registered first.

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

`Text` escapes exactly `<`, `>` and `&`. That is right for element content, for
a `textarea` or `title`, and inside a comment - but **not inside a `<script>` or
a `<style>`**. Those are raw text: a parser does not decode references in them,
so `Text` gives you `if (a &lt; b)` in the script source, which is valid HTML
that throws in the browser.

`HTML` is verbatim, and there it is refused rather than trusted: inserting into
the content of a `script`, `style`, `textarea` or `title` returns
`ErrRawTextBreakout` when the content would close that element, so a `</script>`
in a string literal is an error rather than a script injection. The refusal
cannot cover everything - inserting a *whole* `<script>` element as markup is
allowed, because its payload legitimately contains its own closing tag - so build
script and style bodies from values you control, and pass untrusted data through
a data attribute or a `<script type="application/json">` block instead. JSON's
own `\/` escape exists for exactly this.

Ten element names hold content a parser does not read as markup, and the other
two ways into that content are not checked at all: renaming one of them with
`SetTagName`, or unwrapping one with `RemoveAndKeepContent`, turns its text into
markup without inserting anything. `lolhtml.IsRawText(tag)` is the list, so a
sanitiser that unwraps everything not on its allowlist can ask instead of
copying ten names out of a doc comment:

```go
if lolhtml.IsRawText(e.TagName()) {
	e.Remove() // not RemoveAndKeepContent: the content is not markup yet
	return nil
}
```

When you have to assemble markup yourself, `EscapeText` and `EscapeAttribute` are
the escaping the library would have done for you:

```go
e.SetInnerContent(
	`<a href="`+lolhtml.EscapeAttribute(url)+`">`+lolhtml.EscapeText(label)+`</a>`,
	lolhtml.HTML)
```

Both take a literal value, not markup. Everything the library reports is raw
source with character references still encoded, so escaping a value read from the
document turns `&amp;` into `&amp;amp;` - decode it first, or leave it raw. Leaving
it raw is only safe back into the context it came from: an attribute value may hold
a bare `<`, so a `title` written into an element's text unescaped is markup, and a
text node may hold a bare `"`, so text written into an attribute ends it. Escape the
one character the destination ends on, or decode and escape properly.

Rewriting text that is already there is the other way round again. `TextChunk.Replace`
with `Text` escapes `<`, `>` and `&`, which raw text does not decode - so a
stylesheet's `.a > .b` comes back as `.a &gt; .b`. Use `HTML` to edit a stylesheet or
a script body, and call `CheckRawText(tag, content)` first: the `Element` methods
refuse a breakout for you and the `TextChunk` methods cannot, because a chunk does
not know what element it is in.

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
	src, _ := e.Attribute("src")    // fine: a Go string
	sources = append(sources, src)  // fine: copied out
	elements = append(elements, e)  // useless: detached once this returns
	return nil
})
```

**A failed rewriter cannot be reused, and failing is not atomic.** A handler
returning an error stops the rewrite; the error surfaces from the `Write` or `Close`
that was running, wrapped in a `*HandlerError` you can unwrap. Everything before the
token whose handler failed has already reached the destination - at every write size,
including a single `Write` - and it is a whole number of tokens, so a client reading
it sees a short page rather than a failure. Refusing a document means buffering the
output and forwarding it only on success. lol-html cannot resume afterwards, so the
`Writer` is poisoned and every later `Write` and the `Close` return `ErrPoisoned`
wrapped around that first error, so `errors.Is` still reaches it. A handler that panics
does not unwind through Rust: it is caught at the boundary and re-raised on the
goroutine that called `Write` or `Close`.

A `Writer` is not safe for concurrent use, but independent `Writer`s on separate
goroutines are fine - as long as their handlers are independent too. An `Option`
holds no state and can be reused, and the function inside it is shared with every
`Writer` it is given to, so anything it closes over is shared. Building the option
set once at startup and reusing it per request is the natural thing to do and is
where this bites: measured, two concurrent rewrites over a shared counter reported
655 of 800 matches, and the race detector flagged it. Build the options where the
state lives, once per rewrite.

## Encodings

The default is UTF-8. For anything else, name it with a WHATWG encoding label:

```go
lolhtml.WithEncoding("windows-1252")
```

The encoding is the document's, not your handlers'. A handler always sees UTF-8,
whatever the document is, and content you insert is taken as UTF-8 and encoded on
the way out - so the output stays in the document's encoding, and a character the
target cannot represent becomes a numeric character reference rather than being
dropped.

Two things follow from the standard rather than from this library, and both have
caught people out:

- **The labels are aliases.** `iso-8859-1`, `latin1`, `ascii` and `us-ascii` all
  select windows-1252, which is what the standard requires and what browsers do.
  Bytes 0x80 to 0x9F therefore decode as printable characters, not controls.
- **UTF-16 is refused.** The rewriter has to find ASCII markup in the byte
  stream. Decode to UTF-8 first.

An unusable label fails from `NewWriter`, with an `*EncodingError` naming it.

## Strict mode

Strict mode is on by default and should stay on. A handful of non-conforming
shapes - a `<title>`, `<style>`, `<iframe>`, `<xmp>`, `<plaintext>`, `<noembed>`,
`<noframes>` or `<noscript>` opening inside a `<select>`, or any of those but
`<noframes>` inside a `<frameset>` - leave a streaming parser unable to tell
whether what follows is markup or text. Nothing else triggers it.

Neither setting is simply the safe one:

- **strict on**: the rewrite fails from `Write` or `Close`, and whatever was
  already emitted has reached the sink. Truncated response; discard it.
- **strict off**: the rewrite succeeds and the content after the ambiguous tag
  is treated as text, so no handler runs for it. For a sanitiser that is a
  bypass - `<select><xmp><script>alert(1)</script>` comes out verbatim, with no
  error and no handler invocation. The unseen region runs to the closing tag, or
  to the end of the document if there is not one.

## Memory limits

```go
lolhtml.WithMemorySettings(lolhtml.MemorySettings{
	MaxMemory:       64 << 10,
	GracefulBailOut: true,
})
```

**Size the limit with the writes you will actually make.** How much memory a
rewrite needs depends on how the input is fed, and not by a little: the 5170-byte
document below completes with `MaxMemory: 1024` when written in one call, and
needs `8192` when written in 256-byte chunks. A limit chosen by testing with a
single `Write` is far too low for the `io.Copy` this README recommends, and the
first sign of it is a bail-out in production.

Exceeding `MaxMemory` fails the rewrite with a `*NativeError` whose
`errors.Is(err, lolhtml.ErrMemoryLimitExceeded)` is true. What reaches the sink depends on
`GracefulBailOut` **and on how the input was fed**, which is the part worth
knowing before choosing a default.

Measured on v3.0.1 with a 5170-byte document containing 41 links and one
pathological tag in the middle, at a 1 KiB cap:

| fed as | `GracefulBailOut` | reaches the sink |
|---|---|---|
| one `Write` | `false` (default) | nothing |
| one `Write` | `true` | every byte received, none of it rewritten |
| 256-byte `Write`s | `false` (default) | **670 bytes: a rewritten prefix, then it stops** |
| 256-byte `Write`s | `true` | every byte received: rewritten prefix, then verbatim |

The third row is the one to design for, because it is what `io.Copy` does. On
the default setting a bail-out does not empty the response, it **truncates**
it - and the truncation lands on an element boundary, so the result is
well-formed HTML that a parser accepts without complaint. A client that gets it
sees a plausible page missing most of its content. Check the error from `Write`
and `Close` and discard the response; do not rely on the client noticing.

With `GracefulBailOut` on, everything the rewriter received still reaches the
sink, so you can keep serving by writing subsequent bytes straight to your own
sink, bypassing the now-unusable rewriter. The handover point is the last byte
you wrote, and the flushed tail can end mid-tag, so append to it rather than
inserting anything of your own.

## Performance

`go test -bench .` on an Apple M3 Pro, darwin/arm64, rewriting a 16 KB generated
page with 200 links. Run it on your own hardware before relying on these.

```
BenchmarkPassthrough-12         23437 ns/op   705.88 MB/s     440 B/op    14 allocs/op
BenchmarkSetAttribute-12       211903 ns/op    78.07 MB/s    7042 B/op   423 allocs/op
BenchmarkReadAttributes-12     189778 ns/op    87.18 MB/s   17179 B/op  1023 allocs/op
BenchmarkTextHandler-12        139737 ns/op   118.39 MB/s   11842 B/op   623 allocs/op
BenchmarkStreamingAppend-12    229763 ns/op    72.00 MB/s   28336 B/op  1426 allocs/op
BenchmarkChunkedWrite-12       199385 ns/op    82.98 MB/s    7249 B/op   435 allocs/op
```

Passthrough is the floor - lol-html plus cgo and sink overhead, with no handlers.
The rest is dominated by crossing into Go once per match, so throughput tracks
how many handler invocations a document produces, not its size.

Allocations follow a simple rule, and `alloc_test.go` gates it: a unit wrapper
costs one allocation, every string read or written costs one more, a
`SourceLocation` costs nothing, and `AttributeList` or `Attributes` costs four
per attribute. Nothing is cached, so reading the same attribute twice costs
twice. A handler that lists every attribute to find one is the usual accidental
cost.

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

GitHub does not support attestations for user-owned private repositories, so a
private fork of this repository will see the workflow skip that step with a
notice, and `make attest-verify` will find nothing to verify.

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

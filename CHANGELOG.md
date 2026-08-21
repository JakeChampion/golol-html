# Changelog

## v0.1.0

First release. Go bindings for [lol-html](https://github.com/cloudflare/lol-html)
v3.0.1 (C API crate 1.4.0), pinned at
`608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c`.

### API

Complete coverage of the lol-html C API:

- `NewWriter` returns an `io.WriteCloser`; `Rewrite` and `RewriteString` for
  documents already in memory.
- Handlers for elements, comments, text, the doctype and the document end, plus
  `Element.OnEndTag` for acting on an element once its content has been seen.
- All eight rewritable unit types: `Element`, `EndTag`, `Comment`, `TextChunk`,
  `Doctype`, `DocumentEnd`, `Attribute` and attribute iteration.
- Streaming insertions (`StreamBefore`, `StreamAppend`, ...) for content that is
  large or produced incrementally, with a `Sink` that adapts to `io.Writer`.
- Source locations, per-unit user data, memory limits with graceful bail-out,
  non-UTF-8 input encodings, strict mode, and ESI tags (unstable upstream).

Handlers are Go closures returning `error`; attributes iterate as an
`iter.Seq2[string, string]`. No C types or `unsafe.Pointer` appear in the public
API.

### Platforms

Prebuilt static archives are vendored, so `go get` needs no Rust toolchain -
only the C compiler cgo already requires.

| Platform | Notes |
|---|---|
| `darwin/arm64`, `darwin/amd64` | |
| `linux/amd64`, `linux/arm64` | glibc |
| `linux/amd64`, `linux/arm64` | musl, via `-tags musl` |
| `windows/amd64` | |

`CGO_ENABLED=0` is not supported. Unsupported platforms fail during
type-checking with a name that explains the gap.

### Behaviour worth knowing

- **A unit is valid only inside its handler.** Retaining an `*Element` past the
  handler that received it yields `ErrDetached` rather than reading freed
  memory.
- **Character references are not decoded.** `TextChunk.Text`, `Comment.Text` and
  attribute values return raw source: the href of `<a href="?a=1&amp;b=2">` is
  `?a=1&amp;b=2`. Writing a value back unchanged round-trips correctly, because
  `SetAttribute` escapes on the way out.
- **A failed rewriter cannot be reused.** lol-html cannot resume after an error,
  so the `Writer` is poisoned and later writes return `ErrPoisoned`.
- **Handler panics do not unwind through Rust.** They are caught at the boundary
  and re-raised on the goroutine that called `Write` or `Close`.
- **Text arrives in chunks with no guaranteed boundaries**, and chunk counts
  depend on how the input was split. Output does not.
- **Writing a byte at a time is quadratic** while the rewriter is buffering an
  unclosed tag. Normal read sizes are far from this.

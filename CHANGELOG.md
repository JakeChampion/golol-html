# Changelog

## Unreleased

### Fixed

- **Several `OnDocumentEnd` handlers ran in reverse.** lol-html dispatches its
  document-end handlers in the opposite order to the one they were registered
  in, which is deliberate upstream but the opposite of `Element.OnEndTag`, the
  sibling API. Two handlers appending content emitted it backwards, and because
  a failing handler stops the ones after it, an error in a handler written
  second could stop one written first from running at all. Every
  `OnDocumentEnd` now shares a single native registration and they run in the
  order they were written.

### Documentation

- **Handler order is now stated.** Handlers of one kind run in registration
  order and each sees the previous one's edits. Between kinds, a
  selector-associated handler always runs before a document-level one on the
  same unit - `OnComment` before `OnDocumentComment`, `OnText` before
  `OnDocumentText` - whatever order the options were written in, because
  lol-html keeps the two in separate lists. Neither rule was documented, and
  the second cannot be changed from here.

## v0.1.1

### Fixed

- **A handler that panicked leaked the rewriter and its cgo handles.** `Write`
  re-raises a handler panic, so `Rewrite`'s `Close` never ran and the native
  resources were released only if a garbage collection eventually got round to
  the cleanup: three handles per rewrite. `Rewrite` now defers `Close`, and
  `Write` and `Close` release on the way out of a panic, so a caller driving a
  `Writer` directly does not leak either.

### Documentation

No behaviour changed here, but two things were described wrongly, and both are
easy to get wrong in calling code:

- **`SetAttribute` takes raw source text, not a literal value.** Only the double
  quote is escaped, because only it would break the attribute syntax; `&` and
  `<` pass through. Writing the five characters `&amp;` therefore means the
  single character `&` to whoever parses the result. Content insertion with
  `lolhtml.Text` is the opposite and escapes fully, which is what makes it safe
  for untrusted values.
- **A leading U+FEFF is dropped when reading an attribute.** lol-html decodes on
  the way out and its decoder removes a byte-order mark, so a value starting
  with U+FEFF reads back without it. The value is serialised faithfully, and a
  U+FEFF anywhere but the first position survives.

### Changed

- The declared minimum Go version drops from 1.25 to **1.24**, which is what the
  code actually needs: `runtime.AddCleanup` is the binding constraint. Nothing
  in the library required 1.25. A CI job now builds and tests against 1.24 so
  the documented floor stays true.

### Testing

The leak above was found by new machinery rather than by inspection, and none of
it changes the public API:

- every cgo handle is counted, and the count is asserted on each fuzz iteration
- `FuzzOperations` fuzzes the handler program rather than the input document
- an AddressSanitizer job on both Linux architectures
- deterministic seed-driven fault injection for sink failures, memory limits,
  handler errors and panics
- property tests over generated documents, in a separate module

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

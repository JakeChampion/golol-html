# Changelog

## Unreleased

### Fixed

- **`WithESITags` said what it enabled but not what it does.** The option was
  documented as enabling "parsing of ESI tags such as `<esi:include>`". What it
  does is treat them as **void elements**. Without it an `esi:` element is an
  ordinary container whose content runs to the next matching end tag - which,
  since ESI is conventionally written unclosed, is the enclosing element's. So
  replacing or removing an include takes that end tag with it:
  `<span><esi:include src=a></span>` with a handler replacing the include gives
  `<span>?` rather than `<span>?</span>`, with no error. A trailing slash does
  not help, because HTML ignores it on an element that is neither void nor
  foreign. `Element.CanHaveContent` is what reports the treatment. The existing
  `TestESITags` said outright that it asserted no ESI-specific parsing and used
  the self-closing form; it now pins the void treatment, the swallowed end tag,
  the trailing slash, and that `<esi:remove>` keeps its content either way.

- **`WithStrict(false)` is a sanitiser bypass, and its documentation called it
  "tolerance".** Strict mode is on by default, and the option said only that
  turning it off "trades that safety for tolerance of markup the rewriter cannot
  fully reason about". What actually happens is that content after an ambiguous
  tag is treated as raw text, so no handler runs for it: a rewriter that removes
  every `<script>` does not remove the one in
  `<select><xmp><script>alert(1)</script>`, and emits it verbatim with no error
  and no handler invocation. The unseen region runs to the closing tag, or to
  the end of the document if there is not one - and a document malformed enough
  to trip the guard often has not got one. The other direction is not free
  either: with strict on the rewrite fails mid-stream, leaving a truncated
  response that the caller must discard. `WithStrict` now says all of this, with
  the exact trigger set (eight tags inside `<select>`; the same minus
  `<noframes>` inside `<frameset>`; nothing else), and `strict_test.go` pins it,
  the bypass included.

- **The README's graceful bail-out table was wrong about the default, and it is
  the dangerous direction.** It said that exceeding `MaxMemory` with
  `GracefulBailOut` off delivers 0 bytes and "the response is broken". That
  holds only when the whole document arrives in one `Write`. Fed in chunks -
  which is what `io.Copy` does - the same document and cap deliver a **rewritten
  prefix and then stop**: 670 bytes of 5170 in the measured case, cut on an
  element boundary, so the result is well-formed HTML that a parser accepts
  without complaint. A client sees a plausible page missing most of its content.
  The `MemorySettings.GracefulBailOut` doc comment always said this correctly;
  the README contradicted it, and the README is read first. Both tables are now
  the measured four-row matrix, and `memory_test.go` pins every row.

- **How much memory a rewrite needs depends on how the input is fed.** The same
  5170-byte document completes with `MaxMemory: 1024` in a single `Write` and
  needs `8192` in 256-byte writes - eight times as much. A limit sized by
  testing with one big write will bail out under `io.Copy`, and nothing said so.
  Now documented on `MaxMemory` and in the README, and pinned by test.

- **An unusable encoding label was reported without naming it.** A bad
  `WithEncoding` value failed with `rewriter_build: Unknown character encoding
  has been provided`, which leaves a caller whose encoding comes from
  configuration to go and find which one. It is now an `*EncodingError`
  carrying the label, matching `*SelectorError`, which has always named the
  selector. Every way `lol_html_rewriter_build` can fail is about the encoding
  - upstream returns `UnknownEncoding` or `NonAsciiCompatibleEncoding` and
  nothing else - so the attribution is exact rather than a guess, and the
  native message is kept verbatim in `Message` in case that ever changes.

- **A destination that reported a short write silently truncated the output.**
  The sink discarded the count `io.Writer` returns. A destination accepting five
  bytes of every chunk delivered 14 bytes of a 213-byte document, and both
  `Write` and `Close` reported success; one accepting nothing delivered nothing,
  still with no error. `io.Writer`'s contract says an implementation returning
  `n < len(p)` must also return an error, and not every implementation obeys it -
  which is why `io.Copy` checks. The count is now checked and `io.ErrShortWrite`
  reported, the same error `io.Copy` reports. A destination that returns its own
  error alongside a short count keeps that error rather than having it replaced.
  The seeded fault scenarios in `faults_test.go` now include short writes, and
  their assertion is stronger: the error reported has to be one of the faults
  actually injected rather than merely non-nil.

- **A panic inside a `StreamFunc` leaked a cgo handle per rewrite.** The
  streaming callback was the one `//export`ed callback that did not go through
  the shared panic-recovery path, so a panic in a streaming insertion unwound
  through Rust instead of being converted to an error at the boundary. lol-html
  never ran the drop callback that releases the streaming handle, so each such
  rewrite leaked one handle for the life of the process, growing with traffic
  and invisible in the output. Every other handler kind was already covered:
  this was the gap left by the v0.1.1 panic-leak fix. `FuzzOperations` asserts
  the handle count on every iteration but only ever panicked from an element
  handler, so it could not reach the path; it now has a streaming-panic opcode,
  and fails within a tenth of a second without the fix.

- **Several `OnDocumentEnd` handlers ran in reverse.** lol-html dispatches its
  document-end handlers in the opposite order to the one they were registered
  in, which is deliberate upstream but the opposite of `Element.OnEndTag`, the
  sibling API. Two handlers appending content emitted it backwards, and because
  a failing handler stops the ones after it, an error in a handler written
  second could stop one written first from running at all. Every
  `OnDocumentEnd` now shares a single native registration and they run in the
  order they were written.

### Testing

- **Allocation complexity is gated.** The benchmarks measure six fixed shapes and
  nothing compared them across document sizes, so nothing would have noticed a
  path going from a constant number of allocations to one proportional to the
  input, or a per-match cost quietly doubling. Both leave every output identical.
  `alloc_test.go` pins the shape instead of the numbers: passthrough and
  non-matching handlers must not allocate per byte, and the cost of one more
  match is asserted exactly while the fixed overhead is allowed to drift with the
  toolchain. Verified to have teeth by injecting one escaping allocation per
  callback (7 subtests fail) and by making the output sink copy each chunk
  instead of borrowing it - the regression the borrow exists to prevent, worth
  2224 allocations against 423 when it was first measured.

### Documentation

- **Inserted content is not re-parsed, and that cuts both ways.** Nothing a
  handler inserts is dispatched to any handler, including the one that inserted
  it. Two conveniences follow - there is no loop hazard, so a handler inserting
  an element matching its own selector fires once, and an accumulator is safe, so
  a text handler collecting a heading's text does not also collect a label an
  element handler prepended. One hazard follows too: a rewrite that removes every
  `<script>` does not remove one another of its own handlers inserted, in either
  registration order. Anything inserted has to be safe before it goes in. Now
  documented and pinned by `insertion_test.go`.

- **When a `StreamFunc` runs is now stated.** "On demand" reads as lazy, and if
  it were, a streaming insertion could compute its content from the whole
  document. It is not: the closure runs when its content is emitted, which is
  while the element it belongs to is being written out. Two consequences, both
  silent. It cannot see anything not yet parsed - building a table of contents at
  a marker near the top of a page is impossible in one pass, and you get the
  empty result your closure computed rather than an error. And it may never run
  at all: if the content is discarded, because a later handler removed the
  element or an ancestor was removed, the closure is skipped, so a side effect
  placed in a sink is not a side effect that happens. Pinned by
  `streamtiming_test.go`.

- **The allocation cost model is written down.** A unit wrapper costs one
  allocation, every string read or written costs one more, a `SourceLocation`
  costs nothing, and `AttributeList` or `Attributes` costs four per attribute.
  Nothing is cached, so reading the same attribute twice costs twice. Previously
  the README gave six benchmark numbers and no way to reason about a seventh
  case.

- **The encoding surface is documented.** `WithEncoding` had one sentence. It
  now says that handlers always see UTF-8 whatever the document is, that
  inserted content is taken as UTF-8 and encoded on the way out, and that a
  character the target cannot represent becomes a numeric character reference.
  It also names the two behaviours that come from the WHATWG standard and
  surprise people: the labels are aliases, so `iso-8859-1`, `latin1`, `ascii`
  and `us-ascii` all select windows-1252 and bytes 0x80 to 0x9F decode as
  printable characters; and UTF-16 is refused outright, because the rewriter
  has to find ASCII markup in the byte stream. Both are now pinned by test.

- **Neither `ContentType` is right inside a `<script>` or a `<style>`.** Those
  are raw text elements, where an HTML parser does not decode character
  references, and the escaping choice does not consult the element it is
  landing in. `Text` therefore produces content that is inert but corrupted:
  `if (a < b)` becomes `if (a &lt; b)`, which is valid HTML, returns no error,
  and throws a syntax error in the browser. `HTML` inserts the text verbatim,
  so a `</script>` inside a JavaScript string literal ends the element and
  whatever follows it becomes document markup. There is no combination that
  makes arbitrary text safe there, because escaping it correctly is a
  JavaScript transformation rather than an HTML one. Now documented, with what
  to do instead, and pinned by `contenttype_test.go`. `Comment.SetText`, which
  refuses a comment-closing sequence, is the model for what the script context
  is missing.

- **`Text` escapes exactly `<`, `>` and `&`.** A quote, an apostrophe, a
  backtick and a NUL pass through. The NUL is emitted as a literal zero byte,
  so a value containing one does not survive a round trip: any parser reading
  the output replaces it with U+FFFD.

- **Removal suppresses output, not handler calls, and one corner of it is
  wrong.** A text handler still sees the text of a removed element and an
  element handler still runs for its descendants; their edits are discarded,
  but a handler that accumulates has to notice for itself. Separately, removal
  decides the fate of the inner content when it is called, so `e.Remove()`
  followed by `e.Append(...)` emits the appended content with the element's
  tags gone from around it, while appending first and removing second discards
  it. The two orders disagree, and when two handlers share a selector the order
  is decided by which option was written first: one removing a `<script>` and
  one appending inside it will, in one of the two orders, emit the appended
  content as document markup. `Element.IsRemoved` cannot be used to guard
  against it, because it is also true after `RemoveAndKeepContent`, where
  appending is well defined. Now documented and pinned by `removal_test.go`;
  the fix belongs upstream, where the flag that distinguishes the two removals
  is not exposed through the C API.

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

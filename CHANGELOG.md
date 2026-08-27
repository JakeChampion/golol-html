# Changelog

## Unreleased

- Added `DecodesCharacterReferences`, the predicate for the reading question.
  `IsRawText` answers the writing one - can content written into this element end
  it - and the same ten names come up in a second question with a different
  answer: whether a parser decodes character references in the content. That set
  is `IsRawText` minus `textarea` and `title`, the escapable raw-text pair.

  Getting it backwards is silent in both directions: unescaping a `<style>`'s
  content makes it say something it does not say, and not unescaping a
  `<title>`'s loses the decoding a parser performs. Until now a caller reading
  text had to copy those two names out of a doc comment - which `IsRawText`'s own
  documentation argues against, on the grounds that a copied list falls behind
  the parser silently. `examples/gip/texttruth` and the differential suite both
  carried that literal; they ask the library now, and the differential tests
  still ask the parser about every element name in the HTML index, so the
  library's answer is measured rather than trusted.

- CI: one run per pull request at a time, superseding rather than queueing. This
  matrix is sixteen jobs, so a branch pushed twice in a minute costs thirty-two,
  and the superseded ones sit in the queue ahead of the runs that matter. Pushes
  to `main` are exempt, because each is a distinct commit that has to be gated on
  its own.

  The sanitizer's fuzz step also gets `ASAN_OPTIONS=allocator_may_return_null=1`,
  as an attempt at the intermittent failure described in #327 - ASan's own
  allocator failing an internal check rather than detecting anything. It is
  unverified and says so in the workflow: `-asan` is unsupported on darwin/arm64,
  so it cannot be reproduced locally, and the evidence will be whether the flake
  recurs.

- Doc figures: correct the one that was wrong, gate it, and stamp the one that had
  drifted with the toolchain it came from.

  The Content-Encoding table in the package documentation said "256 arbitrary
  bytes ... 482 bytes", and `lossytext_test.go`'s table said the same. The case
  they describe feeds every byte value in order, and the answer is 512 - measured
  through the helper that test uses. It reads like a number left behind when the
  input stopped being random. Nothing caught it because nothing gated it: the
  assertion is that a lossy body grows, which 482 and 512 both satisfy. It is
  gated now, with a message naming both comments to update if the decoder ever
  legitimately changes the answer.

  `Element.OnEndTag` said "about 30 MB against about 6 MB ... roughly 300 bytes
  per element". Measured on Go 1.25.8 it is 27.0 MB, 4.2 MB and about 240 bytes.
  The figures were hedged and directionally right, so this is a re-measurement
  rather than a correction - but the section quoted no toolchain, and this
  project's own rules say to name it, because that is the axis allocation figures
  move on.

  Two other figures were checked and stand: `NamespaceURI` returning the package
  constants costs zero allocations rather than one per element (measured 1025
  either way over 1000 elements), and the write-amplification claim is gated
  one-sidedly in `writecount_test.go`, which is the right way to pin an
  approximate count. Two remain ungated and unverified here because measuring
  them needs a 64 MB document: the user-data heap figures on
  `Element.SetUserData` and the pipeline peak-heap figures in the package
  documentation.

- Added `CheckComment` and `ErrCommentBreakout`, for text a caller is about to
  put inside a comment it assembled itself. `Comment.SetText` refuses text that
  would end the comment early and says there is no escaping that would work; a
  comment built by hand out of `HTML` content had no equivalent guard, which the
  documentation named without offering one. `CheckRawText` is exported for
  exactly that hand-built case, so this is the missing half of the pair.

  The rule is the tokenizer's: refuse text containing `-->` or `--!>`, or
  beginning with `>` or `->`. Agreement with `SetText` is pinned over 2813
  strings rather than assumed, 474 of them refused by both, and what it refuses
  is checked against what actually leaks by parsing the output.

- `examples/gip/tailcomment`: emit a summary of what changed as a trailing
  comment, which is the path that has no guard - `DocumentEnd.Append` takes
  markup. Unguarded, a summary holding a URL with `-->` in it truncates the
  comment and puts the rest in the page. Since there is no escape, the program
  makes the choice explicit: rewrite the sequence ("- ->"), or refuse to emit a
  comment and say why.

  Its test caught that the danger needs a literal `-->` in the source.
  `Attribute` returns raw source, so `&gt;` stays encoded and is harmless; a
  literal `>` is legal inside a quoted attribute value and is not.
- Package documentation, Cost: quantify the rule. "A rewrite's cost tracks how
  many times your handlers run, not how long the document is" was already there;
  what was missing is how much that varies. At a fixed 200 KB the spread across
  document shapes is about 1900x, from 103.6 ns/byte for a list of `<li>` with no
  closing tags down to 0.055 for one element with a 200 KB attribute value. The
  worst shape costs three handler calls per item - the element, its text, and the
  empty chunk that ends the text node - and it is a navigation menu rather than a
  pathological document.

- `examples/gip/worstshape`: the harness, pointed at your own handler set. It
  runs sixteen shapes at a fixed byte count and ranks them. The gates are the
  numbers that do not depend on the machine - handler calls and allocations per
  byte, and that the ordering by one tracks the ordering by the other - with the
  times logged and asserted nowhere.

  "Asserted nowhere" took a second attempt. It checked that each shape took some
  nonzero time, which is the timing rule broken in a third form: the Windows
  runner reports the cheapest shapes as exactly 0 ns, because they take a few
  microseconds and its clock ticks every 340µs. The tool now measures the tick,
  says when a figure is below it, and compares allocations instead.

- `Attribute`: say that it has no source location, and that its bytes cannot be
  recovered from the ones that do. `Element.SourceLocation` is the whole start
  tag, and searching that for one attribute's range fails on markup the library
  preserves - a duplicate name, two entries with the same name *and* value, a
  name that is a substring of another (`a=` inside `data-a=`), and a bare
  attribute with no `=` at all. The first two have no answer, since a repeated
  attribute is kept rather than dropped. So an offset-keyed tool can act on an
  element or a start tag and not on one attribute of it.

- `examples/gip/shrink`: reduce a failing document to its essence, with the
  rewriter proposing the cuts - element extents, start tags, comments, doctypes
  and text nodes, with byte halving as the fallback for what structure cannot
  reach (attributes among them, per the above).

  Two measurements it produced, both against what I expected. Proposing every
  structural cut before any byte cut - the obvious design - cost 595 oracle calls
  on a document wrapped in thirty divs against 34 for plain halving, because the
  cuts structure proposes are mostly the ones that remove the failure. Ordering
  all candidates by size brings that to 55, and then structure wins 6 of 9
  documents, 308 calls against 331: a real but modest gain, which is what the
  test now asserts rather than the sweeping one I started with.

  It also demonstrates the classic reduction mistake concretely. On a document
  holding two failures, an oracle that asks only "does it fail" reduces
  `<div><script>a</script></div><div><style>b</style></div>` to `<style>` where
  the original failure was the script's - a different error with different advice
  in it.

- `SourceLocation`: say that a text chunk is the exception to the
  write-invariance the section promises. When a multi-byte character straddles a
  write boundary the chunk's range covers only the part of it that arrived last,
  or the held-over bytes are charged to the chunk already emitted. `<p>a€b</p>`
  fed in one call reports one chunk, 3..8, whose text is its own slice; fed
  three bytes at a time it reports 3..6 for the text "a", three bytes of range
  for one byte of text; fed one byte at a time it leaves bytes 4 and 5 named by
  no chunk at all. The text is right in every case, which is why ASCII never
  shows it and a proxy reading from an `io.Reader` with a fixed buffer does.
  Everything else reports the same range at every write size, so the section now
  gives the recipe that does not depend on the write pattern: take the ranges of
  the units around the text and read the text itself from your own copy of the
  input.

- `examples/gip/textmap`: report the location of every text chunk and rebuild
  the document from what was reported. Rebuilding from the chunk text is wrong
  at some write sizes; the tag-derived text map is identical at all of them,
  measured over four documents at every size from one byte up. It is also the
  lossless way to read the text of a body that is not text: for a 259-byte body
  holding every byte value the map yields its 255 text bytes, where the
  handler's own text reports 511, because the text path turns each undecodable
  byte into U+FFFD.

  One trap, found by a test that passed when it should not have: filling the gaps
  between chunks from the input and taking each chunk by slicing the input is
  `doc[pos:start]` followed by `doc[start:end]`, a contiguous copy whatever the
  ranges say. It reproduces the document even when every range is nonsense, so
  it cannot be used as a check on the ranges - which is what the first draft of
  this program's tests did.
- `differential`: remove `zz_scratch_test.go`, a scratch draft committed by
  accident in #238. It is the working version of what became
  `preserving_test.go`: the same fourteen documents, the same six rewrites, the
  same tree comparison, and `!!` still in its failure message. It duplicates
  those assertions rather than adding any, so a change that broke them broke two
  files, and the surviving one is the one with the hazards half and the prose.
- `SourceLocation`: say that the units do not tile the document. The section is
  exhaustive about what each unit's range covers and silent about which tokens
  never become a unit, which matters most for the tool the section is written
  for - one keying on offsets across two passes. A stray end tag, one with no
  start tag to pair with, reaches no handler at all: an end tag is observable
  only through `Element.OnEndTag` and there is no element to register it on.
  Its bytes are still written out, so rebuilding a document from the ranges
  reported drops them: `<p>a</p></p><b>c</b>` comes back as
  `<p>a</p><b>c</b>`. Measured for `</p>`, `</span>`, `</br>`, `</img>`,
  `</p class=x>`, `</>`, `</circle>` and a trailing `</li>`; with every handler
  the library has registered on `*` and the document written in one call, those
  are the only unnamed bytes in the documents measured. One space decides it - `</ x>` is a bogus comment, not a
  tag, and a comment handler sees it.

- `examples/gip/locate`: grep for HTML - report every match's source location
  and prove each report by slicing the caller's own copy of the input, then
  report the bytes no handler named. Also pins the exactness the library's own
  test stopped short of: `sourceloc_test.go` asserts a start-tag slice *begins*
  `<name`, which leaves where it ends untested, and it is exact for the forms a
  scanner hunting the next `>` gets wrong - `<a title="a<b">`, `<p attr=>`,
  `<p/ >`, `<p a="1"b="2">`. The slice is also the only way back to the
  author's spelling, since `<P>` reports `TagName` `p`.

  Two of its own bugs. It registered an end-tag handler without checking
  `CanHaveContent`, which is an error rather than a no-op on a void element, so
  `<img src=a/>` failed the rewrite instead of reporting one span. And its
  chunk-invariance test compared a four-handler baseline against a two-handler
  run, so the span counts it was checking were never meant to match.
- `DocumentEnd.Append`: say that there is no streaming form of it, and what to do
  when the append is large. `Element` and `EndTag` each have six `Stream`
  methods; `DocumentEnd` has none, so a trailing report exists in memory in full
  - 65.5 MB of allocation for a 12 MB report of a million rows, against 16.0 MB
  written to the caller's own sink after `Close` for byte-identical output. The
  routes differ in two things: the caller escapes its own content, for which
  `EscapeText` is already documented as exactly what `ContentType` `Text`
  applies, and the caller has to check `Close` first.

- `examples/gip/tailreport`: append a generated report to every document,
  streaming it rather than building it. Two corrections it forced on me. I had
  written that a handler error discards the output; it does not - the sink keeps
  the early-stop prefix, so `before<a href="/">l</a>after` with a failing anchor
  handler leaves `before` there, which is a better reason not to write a report
  than the one I had. And its allocation comparison used twelve distinct keys, so
  it was measuring the fixed cost rather than the report; with 200,000 it is 19.2
  MB streamed against 40.1 MB built.
- `IsRawText`: say what it is not the predicate for. It answers the insertion
  question - can content written into this element end it - and the other
  question those ten names come up in is whether character references decode,
  where the answer is this list minus `textarea` and `title`. Getting that
  backwards is silent both ways: unescaping a `<style>`'s content makes it say
  something it does not say, and not unescaping a `<title>`'s loses the decoding
  a parser performs. The NUL rule does key on the list exactly.

- `differential`: sweep the four source-versus-parsed-text rules across all 144
  element names in the HTML index, rather than the hand-written tables in
  `preprocess_test.go` and `rawtext_test.go`. Those tables were right; what was
  missing is that they are complete - the same gap that let the raw-text guard
  ship covering four elements out of ten. CR and CRLF become LF in every
  element with no exception; a NUL becomes U+FFFD in exactly the ten raw-text
  elements and is dropped in the other 134; references decode in all but eight;
  and one leading LF is dropped by `pre`, `listing` and `textarea` only, not by
  `xmp`.

- `examples/gip/texttruth`: compose those four rules into the conversion, so a
  word counter or a search index can get what the page says from what the
  rewriter reports. Checked end to end against `x/net/html`. Its own bug, caught
  by its own test: an empty `<pre>` emits no text chunk, so the flag for "the
  next text loses a newline" outlived the element and ate the newline after it -
  `<pre></pre>\nx` said "x". It is cleared on the end tag now.
- `SourceLocation`: say how much input a streaming caller has to retain to slice
  at these offsets. The section recommends the offsets as identity across two
  passes and says nothing about the buffer, and the obvious rule - keep the
  current write, drop it when no handler is pending - is wrong. A start tag spans
  writes and its handler runs after its first bytes were handed over: fed three
  bytes at a time, the handler for `<div id=a>` at offset 0 fires while such a
  caller holds input from offset 9. The floor is the end of the last unit any
  handler reported, since tokens do not overlap.

- `examples/gip/dupsection`: duplicate a section, renaming the ids in the copy,
  without holding the document. The copy is the section's own bytes, sliced from
  the caller's buffer at the element's extent, so what a reconstruction from
  reported units would lose survives - a stray end tag inside it, a comment, an
  entity, the original quoting. With 512-byte reads and a 1016-byte section,
  documents of 5 KB, 41 KB and 801 KB retained 1072, 1504 and 1408 bytes at
  peak.

  Two things its tests caught. The retention rule above, which it had wrong and
  which failed at every read size below the section. And that the copy is not
  byte-identical after all where the second pass sets an id: that start tag is
  re-serialised per B171, so `id=a class = 'x'  data-k` comes back as
  `id="a2" class = 'x' data-k`. The test now says which start tags keep their
  bytes and which do not.
- `TextChunk.IsLastInTextNode`: the final chunk of a text node is not always
  empty, and the documentation said it was - "that chunk is a call of its own and
  it carries no bytes... measured empty in every shape tried". Every shape tried
  decoded cleanly. When a node ends with bytes that could not be decoded, the
  flagged chunk is the replacement character produced for them: three bytes of
  text, at a zero-width source range because those bytes are not in the input.
  Measured in each raw-text element, after 100 KB of text, unterminated, and at
  every write size; the same bytes declared `windows-1252` decode and the chunk
  is empty again.

  This mattered because the wording invited the handler that drops it: act on
  the flag, return, and never read its text. Accumulating that way gives "ab"
  for `<p>ab\xe9</p>` where reading the final chunk gives "ab\ufffd" - and
  undecodable input is exactly the case a proxy meets.

  The adjacent claim was wrong for the same reason, though narrower than I first
  wrote it: a text handler does not always run twice per text node. Where the
  node is nothing but a truncated multi-byte sequence - `<p>\xe9</p>`,
  `<p>\xc3</p>` - it runs once, and that call is both the node's first chunk and
  its last. A standalone invalid byte like `<p>\x80</p>` is still two, because it
  is replaced inside the content chunk.

### Added
- **`CheckRawText`, because the text paths cannot apply the breakout guard.**
  Inserting content into a `<script>` or a `<style>` through an `Element` method is
  refused when the content would close the element - `ErrRawTextBreakout`, with
  advice per element. The same content through `TextChunk.Before`,
  `TextChunk.After` or `TextChunk.Replace` was written straight out:

      OnElement("script", … SetInnerContent(payload, HTML))   ErrRawTextBreakout
      OnText("script",    … Replace(payload, HTML))           the payload, verbatim

  where the payload is `var a="</script><img src=x onerror=alert(1)>"` and the
  output of the second is a document with an `img` element in it. Measured for
  `script`, `style`, `title`, `textarea`, `iframe`, `noembed`, `noframes`,
  `noscript` and `xmp`: the element paths refuse, the text paths do not.

  The binding cannot close that gap by itself, and the reason is worth writing
  down. A text chunk does not know what element it is in: lol-html's C API for one
  has its content, source location, removal, user data and the streaming variants,
  and nothing that names the enclosing element. There is no tag to check against.

  A handler does know, because it registered the selector. So the check is exported
  now: `CheckRawText(tag, content)` is what the element paths apply, tokenizer rule
  and per-element advice included - raw text ends at `</` plus the name followed by
  `>`, `/`, ASCII whitespace, or the end of the content, since what follows an
  insertion is the rest of the document. The test asserts the exported function's
  error against the element path's for the same content, so the two cannot drift.

  It matters because the text path is the one a rewrite editing CSS or JavaScript
  has to use - see the Documentation entry below.

- **`ErrNilOption`: a nil option was a panic.** `NewWriter(dst, opts...)` with a
  nil entry in the list dereferenced it, so the stack trace pointed at
  `rewriter.go` rather than at the call that made the mistake - while a nil
  destination has always been a returned error. The asymmetry was the bug.

  It is an easy mistake to make: a conditional that leaves an `Option` unset, a
  slice built with a gap, a helper returning a zero value on a path nobody tested.

      var opt lolhtml.Option
      if enabled {
          opt = lolhtml.OnElement("a", rewriteLink)
      }
      w, err := lolhtml.NewWriter(dst, opt)   // panicked; now an error

  Refused rather than skipped, for the same reason an unsupported selector is
  refused rather than ignored: a rewrite that quietly did less than it was told to
  is worse than one that did not start. The error names the position, because a
  caller building options in a loop needs to know which iteration it was.
  `niloption_test.go` covers every position in the list, both entry points, that
  no handle is left behind, and that no options at all is still fine.

- **`ErrInvalidUTF8`, because a value from outside the program can fail the whole
  rewrite.** Every path that takes content or a name refuses bytes that are not
  valid UTF-8: the `ContentType` insertions, `SetAttribute` for name and value,
  `SetTagName`, `Comment.SetText`, and both sink writes. That is a rewrite that
  fails, not an insertion that fails, so a page personalised from a request header
  is one Latin-1 name away from not being served - `X-Name: caf\xe9` is an
  ordinary thing for an old client to send.

  The document path refuses nothing. The same bytes pass through untouched with no
  text handler, and come back as U+FFFD with one, which is why this is easy to
  miss while testing: a rewrite can carry bytes it cannot write.

  Until now the only sign was lol-html's own wording ("invalid utf-8 sequence of 1
  bytes from index 1", or "Invalid UTF-8" from the chunk writer), so a caller who
  wanted to repair the value rather than fail the page had to match on message
  text. `errors.Is(err, lolhtml.ErrInvalidUTF8)` now answers, and the
  classification is made on this side: when a write fails, the argument it was
  given is checked, so a reword upstream cannot turn the guard off. A trailing
  partial sequence in `Sink.WriteChunk` is deliberately not this error - that is
  the case `WriteChunk` exists for - and one still open when the `StreamFunc`
  returns is still `ErrIncompleteRune`.

  `strings.ToValidUTF8` and `utf8.ValidString` are the two answers, and which one
  is right is the caller's decision, which is why this is an error rather than a
  silent replacement. `invalidutf8_test.go` measures every path, both directions,
  and the negative cases: a raw-text breakout and a forbidden attribute name are
  not reported as encoding failures.

- **`IsRawText`.** Ten element names hold content an HTML parser does not read as
  markup, and the package has always known which: it checks insertions into them
  and returns `ErrRawTextBreakout`. It also warns, in three places, that the two
  other ways into that content are the caller's problem - renaming one of them
  with `SetTagName`, or unwrapping one with `RemoveAndKeepContent`, turns its text
  into elements without inserting anything - and gave the caller no way to ask
  which names they are.

  The only thing left was to copy ten names out of a doc comment, which then
  falls behind the parser silently. `IsRawText(tag)` answers instead, and is
  measured against the parser by the same test that holds the guard to it: for
  every element name in the HTML index, whether a `<b>` inside is reported as an
  element. It covers all ten including `plaintext`, which the breakout check skips
  because nothing can close one.

  `SetTagName`, `RemoveAndKeepContent`, `ErrRawTextBreakout`, the package
  documentation and the README now point at it.

- **`Sink.Err`, so a `StreamFunc` can find out that the rewrite has already
  failed.** A sink's methods write into lol-html's buffer rather than to the
  destination, so a nil from `WriteString`, `WriteChunk` or a writer from
  `AsWriter` means the content was accepted and not that it arrived. Measured:
  after a destination that fails on its second write, fifty further sink writes
  were all accepted and none reported anything - the error surfaced only from the
  outer `Write`. For short content that costs nothing; for what a `StreamFunc` is
  for, which the documentation describes as large or incrementally produced
  content copied with `io.Copy`, it means copying the whole thing after there is
  nowhere to put it. Checking `Err` between chunks stops it: the same document
  stops after one write instead of fifty.

  It reports a handler error too, so one `StreamFunc` can see that another has
  already failed. Nil means nothing has failed yet, not that anything has
  arrived - there is no point at which delivery is known, since the rewriter may
  still be holding the content.

- **`ErrIncompleteRune`: a UTF-8 sequence a `StreamFunc` never finishes is no
  longer silent.** lol-html holds an incomplete sequence waiting for the rest of
  it, which is what makes `io.Copy` into `Sink.AsWriter` safe across arbitrary
  chunk boundaries. If nothing finishes it, the bytes go nowhere:
  `WriteChunk([]byte("ab\xc3"))` produced `ab` with no error, so an insertion
  from a truncated source was silently shorter than the content. A `WriteString`
  while a sequence is open does not finish it either - the held bytes become
  U+FFFD and the string is written after them, which `Sink.WriteChunk` had
  documented as something not to do without there being any way to notice having
  done it.

  Both now return `ErrIncompleteRune`, checked when the `StreamFunc` returns
  rather than inside each write, so splitting a rune stays free. The document
  path never lost these bytes - a truncated sequence in the input becomes U+FFFD
  - so this was the one place in the library where content vanished without a
  word.

- **`NamespaceHTML`, `NamespaceSVG` and `NamespaceMathML`, and
  `Element.NamespaceURI` no longer copies.** There are three possible values,
  they are static in lol-html, and every call was returning a fresh 28- or
  32-byte string: measured at 1000 extra allocations and 32 KB for a document of
  1000 elements, which is what anything namespace-aware pays. It now compares the
  C string against the constants without copying it and returns one of them, so
  reading it costs the same as not reading it. `TestNamespaceURIDoesNotAllocate`
  fails by exactly one allocation per element if that regresses.

  The constants are exported because a caller has to compare against something,
  and three URIs retyped by hand is three chances to get one wrong.

- **`ErrMemoryLimitExceeded` and `ErrAmbiguousTag`, so `errors.Is` reaches the
  two failures a streaming caller has to act on.** Both arrive as a
  `*NativeError` carrying lol-html's own message. The memory one had a
  `MemoryLimitExceeded()` method, so identifying it took `errors.As`, a type name
  and a method call; the strict-mode ambiguity had nothing, and this package's own
  test identified it with `strings.Contains(ne.Message, "ambiguous")`. Both now
  match through `errors.Is`, including through the wrapping a poisoned `Close`
  adds. `MemoryLimitExceeded()` stays, because it is exported.

  The matching is still against lol-html's prose, which is the only thing there
  is; what changed is that it is in one place and provoked by tests rather than
  repeated in every caller. The ambiguity message names the offending tag, so the
  match is on the fixed part, checked against all six shapes that produce it and
  against five that look like they should and do not.

- **`ErrRawTextBreakout`: an insertion that would close the element it is going
  inside is now refused.** `Comment.SetText` has always refused text containing a
  comment-closing sequence; the script and style equivalent was recorded in a test
  comment as "the model for what the script context is missing" and is now
  implemented. `Element.Prepend`, `Element.Append`, `Element.SetInnerContent` and
  `EndTag.Before` on any of the nine elements that hold raw text and can be
  closed from inside - `script`, `style`, `textarea`, `title`, `iframe`,
  `noembed`, `noframes`, `noscript`, `xmp` - with `ContentType` `HTML`, return it
  when the content would end that element. `plaintext` is the tenth raw-text
  element and is deliberately not covered: nothing closes it. The
  rule is the tokenizer's, measured against x/net/html rather than read off the
  specification, so `</scriptx` is accepted and `</script foo>` is not, and the
  end of the content counts as a terminator because what follows an insertion is
  the rest of the document.

  Not checked either: `TextChunk.Before`, `TextChunk.After`, `TextChunk.Replace`
  and every streaming insertion. A text chunk has no way to name the element it
  is inside and a streaming write can split a closing tag across two calls, so
  neither has anything to look up; `TestWhatIsStillNotChecked` pins where the
  check stops.

  Not checked: `Before`, `After` and `Replace`, which write outside the element,
  where a closing tag is ordinary markup; `ContentType` `Text`, which escapes the
  `<` and so cannot close anything; and the insertion of a whole `<script>`
  element as markup, whose payload legitimately contains its own closing tag.
  That last one is the case a caller is most likely to be in, and its answer is
  still to escape for the language inside the element.

  This changes behaviour: what previously produced a working script injection now
  returns an error. `contenttype_test.go` had a test asserting the old behaviour;
  its assertion is inverted rather than deleted, with the reason recorded there.


- **`EscapeText` and `EscapeAttribute`.** The package documentation has told
  callers for some time that building markup yourself makes you the serialiser,
  and then handed them no serialiser. These are it: `EscapeText` is byte for byte
  what the library applies for `ContentType` `Text` - asserted against the
  library over a corpus rather than assumed - and `EscapeAttribute` adds both
  quote characters so a value is safe inside quotes you chose yourself. Both
  return their argument unchanged when there is nothing to escape, and allocate
  exactly once when there is. Four properties in `properties/` state the
  relationship over generated values rather than a table: that EscapeText is
  ContentType Text, that an escaped value makes exactly one attribute between
  either quote, that it reads back as what went in, and that EscapeAttribute is
  EscapeText plus the two quotes.

  They take literal values, not markup. Everything the library reports is raw
  source with character references still encoded, so a value read from the
  document must either be left raw and not escaped, or decoded first; escaping it
  twice turns "Configure &amp; run" into "Configure &amp;amp; run". The
  documentation says which of those applies where.

### Fixed
- **`examples/gip/upgrade` was overwritten by another app and restored.** GIP 148
  wrote its widget-modernising app to `examples/gip/upgrade`, a name taken since
  PR #13 by the mixed-content HTTPS upgrader. `cp` replaced both files, including
  the tests, so nothing failed and the PR merged green.

  The HTTPS upgrader is back, unchanged, and the widget app is
  `examples/gip/widgets`. The trap is in `docs/gip/GIP.md`: a name has to be
  checked before it is written to, because 143 example directories is more than
  anyone remembers, and a name should say what the app does rather than what it
  does it to - "upgrade" was already ambiguous between a URL scheme and markup.

- **`examples/gip/queue` reported a build share of 1.33, which cannot be.** The
  two-queue method runs the work pass and the overhead pass separately, and
  whichever goes second pays for the first one's rubbish - the work pass allocates
  far more, so it left the overhead pass holding the collection. On the arm64
  runner that put the overhead pass at 2.01ms against 1.51ms for the whole queue.

  The passes alternate which goes first now, and each starts from a collected
  heap. And the invariant the test asserted was not one: two separate runs can come
  out either way round on a loaded machine, so a share at or above 1 is now
  reported as two passes that could not be separated - naming both figures - rather
  than printed as a number. The counted share is unaffected, having no clock in it,
  so the report still gives a reader something to use.

  The new case is gated by an `Outcome` built by hand with the runner's own
  figures, because a fast machine will not produce it: 120 test runs against forty
  spinning processes did not.

- **`examples/gip/idmerge` was the pattern B177 warns about, and it corrupted
  silently.** It rewrites each input with its own rewriter and concatenates the
  outputs, which is exactly the join that is not the same as rewriting the whole.
  A part that ends inside an unfinished construct swallows whatever comes after it,
  so the wrapper and the next part disappear into it. Measured, on a first part cut
  short after `class="`:

      <section data-source="a"><p id="p1">a</p><div class="c</section>…

      elements a parser then finds:
        section[data-source=a]  p[id=p1]
        div[class=c</section><section data-source= b"=]
        p[id=p2]

  Four elements where six were meant, the second part's `<section>` gone, and its
  `data-source` now an attribute of the div. Nothing errored: the merge was a
  well-formed document that said something else.

  A part that does not end cleanly is refused now, by name, before anything is
  written - `Merge` writes nothing at all on a bad part, since a partial merge on
  standard output is worse than none. The check is the one the package
  documentation gives: append a sentinel element and see whether its handler runs.
  It costs one selector rather than a pass, because the collect pass is already
  reading the whole document.

  Three tests: every absorbing construct is refused and every clean ending
  accepted - including the two that look unfinished and are not, an element left
  open and a stray end tag - `Merge` writes nothing before refusing, and
  `TestWhatTheCheckPrevents` measures the corruption itself, so the check is known
  to be worth its selector rather than assumed to be.

- **A mean over microsecond timings is not a measurement, and neither is a
  median on a coarse clock.** `examples/gip/queue` reported the share of a
  queue's time that went on building rewriters rather than rewriting, and drove
  its advice off that share. It timed each item with `time.Since` and averaged.
  Two separate things were wrong with that, and CI found both.

  The first is that an item is a few microseconds and the scheduler is not. One
  item that loses its core outweighs the other fifty-nine, and where the pause
  lands decides the answer. Sixty 128-byte documents, one selector, on a loaded
  machine - the same build, sixty times:

      run   median build   95th   slowest   slowest over median
        1        1.708µs   21.5µs   41.5µs                  24x
        2        1.458µs    4.0µs   11.9µs                   8x
        3        4.125µs   28.4µs   92.1µs                  22x
        4        4.041µs   17.4µs   28.2µs                   7x
        5        1.875µs    9.4µs   29.8µs                  16x

  Over twenty runs the mean build share ranged from 0.16 to 0.45 where the median
  of the same samples held between 0.16 and 0.18. The macOS runner reported 0.64
  for one selector against 0.60 for fifty, which is the ordering backwards.

  The second is that the median does not save it. On the Windows runner every
  per-item figure came out as exactly zero: the clock ticks more coarsely than an
  item takes, so sixty readings of a hundred microseconds each read zero. The
  mean had been hiding that - a sum of mostly-zero readings with the occasional
  tick in it is not zero, so it looked like a measurement.

  So there is no per-item stopwatch left. The queue runs twice, once doing the
  work and once building a rewriter per item and closing it without writing, and
  the timed share is the ratio of two whole-queue intervals - milliseconds,
  thousands of ticks even on a coarse clock, fastest of three passes each.
  Checked against the method it replaced, one worker, quiet machine, both
  counting Close as overhead:

      documents          selectors   two queues   per-item medians
      400 x 1050 bytes          50        0.458              0.461
      400 x 150 bytes           50        0.839              0.852
      400 x 150 bytes            1        0.253              0.272
      200 x 32 KB               50        0.027              0.028

  And the report carries a second share that needs no clock at all: the mallocs
  an item cannot avoid over the mallocs it makes. Eight runs of the test against
  forty spinning processes moved the allocation shares not at all - 0.963, 0.636
  and 0.158 for the three configurations - while the time share for a 32 KB
  document moved sevenfold, 0.005 to 0.034. The counted share is not the time
  share and does not pretend to be, since allocation misses the per-byte
  parsing, but over six combinations of size and rule set it ranks them in
  exactly the same order. So the gate is on the counted share, the timed share is
  checked only where the clock can resolve it, and the test logs which one it
  skipped. The report prints the measured clock tick and refuses to advise when
  the queue is too short for it.

  Counting has its own tolerance, measured rather than assumed: with the
  measurement pinned to one processor and the answer rounded to whole
  allocations, the same input still moves a little, because the malloc counter
  includes the runtime's own and those depend on the state of the heap. An M3 Pro
  moves by one allocation in four hundred and the macOS and Linux arm64 runners
  by two in four hundred and fifty - the same wobble `alloc_test.go` already
  documents for the fixed part of a count, and the tolerance here is the eight
  that gate uses. The share itself is asserted thirty times tighter than the
  signal it has to separate.

  `examples/gip/backpressure` had the same shape in a test rather than in a
  report - a rewrite that takes microseconds compared against the same rewrite
  with 2ms of enforced destination latency, one run each. It takes the fastest of
  five runs at each latency now and asserts the gap is at least half the sleeping
  the destination was asked to do, since preemption only ever adds time.

- **`Write` allocated once per call, whatever its size.** The out-parameter the
  write passes to C - `var cerr C.lol_html_str_t` - has its address taken, which
  forces it to the heap: one 16-byte allocation per write, and a byte-at-a-time
  rewrite paid it once per byte. Measured on a 64 KB page, with the difference
  being exactly the number of writes:

      64 KB of ordinary markup     before   after   written whole
        one byte at a time         68,684   3,143   3,143
      64 KB of unclosed tag        before   after   written whole
        one byte at a time         65,563       22      22

  It now lives with the rewriter, allocated once in `NewWriter`. It is its own
  allocation rather than a field of the `native` struct because cgo refuses a
  pointer into memory that holds Go pointers, and that struct holds several -
  the attempt panics with "cgo argument has Go pointer to unpinned Go pointer",
  which is the runtime being right. Reuse across writes is safe because a Writer
  is not safe for concurrent use; `Close` keeps its own local, since once per
  document is not worth sharing state with the hot path for.

  What this buys is not mainly speed - the time is dominated by the crossing
  itself, and the measured difference is a few per cent, at the edge of what one
  machine can separate. It is that an allocation count no longer depends on how
  the document is fed. `BenchmarkChunkedWrite` and `BenchmarkSetAttribute`
  rewrite the same page in twelve writes and in one, and now report the same
  figure; every allocation number in `alloc_test.go` and in the README describes
  a streaming caller as well as a batch one. `bytecost_test.go` gates it across
  seven document shapes and four write sizes, and fails by three orders of
  magnitude if the allocation comes back.

- **`examples/gip/mixed` missed an insecure `<image>`.** The mixed-content checker
  matched `img` and not `image`, so a page whose insecure request was spelled the old
  way passed. It now matches both, and reports SVG's own `image` element as
  `svg:image` rather than as a spelling of img, which the namespace tells it apart by.
  Found by the measurement above; the other example programs keyed on `img` have the
  same hole and are listed in the issue.

- **Two of the example programs lower-cased SVG attribute names**, found by the
  `SetAttribute` measurement under Documentation below. `examples/gip/widows` rebuilt a heading's markup through the
  `Attributes` iterator, so an `<svg viewBox>` inside a heading came out as
  `viewbox`; `examples/gip/minifydiff` projected attribute names the same way, so
  its checker would have approved a minifier that lower-cased one. Both now use
  `AttributeList` and `NamePreserveCase`, with a test each.
- **The first byte written to a `Sink` is a commitment, and `StreamFunc` did not
  say so.** It said "returning an error aborts the rewrite, and the error surfaces
  from Write or Close", which reads like the insertion is abandoned the way a
  failing handler's is. It is not, and the difference is the whole point of the
  feature: a sink write reaches the destination as it is made.

      e.Before("<div>partial", lolhtml.HTML); return err
      // destination: "<p>a</p>"                     -- the insertion went with the rewrite

      e.StreamBefore(func(s *lolhtml.Sink) error {
          s.WriteString("<div>partial", lolhtml.HTML)
          return err
      })
      // destination: "<p>a</p><div>partial"         -- and an unclosed <div> with it

  Measured, along with the streaming itself: from inside a `StreamFunc`, the
  destination's write count goes up after each sink write, so nothing is held.
  That is what makes a large insertion cheap and what makes failing halfway
  unrecoverable - the response has left, and the committed prefix need not be well
  formed.

  So whatever has to be true before committing - the file opened, the request
  returned 200, the template parsed - has to be established in the handler, where
  returning an error still costs nothing, and the sink given only content already
  known to exist. `StreamFunc` now says that with both shapes; `Sink.Err`, which
  is the same problem in the other direction, points at it.
  `streamcommit_test.go` measures all of it, including that the error is a
  `HandlerError` of kind "streaming" so a log can tell the unrecoverable case from
  a handler that failed before writing.
- **A stream can tell a comment from a bogus comment, and the documentation said
  it could not.** "What counts as a comment" has always said that four pieces of
  syntax arrive as comment tokens - a comment, a bogus comment, a processing
  instruction, a CDATA section - and that `<!x>` and `<!--x-->` both have the text
  `x`, so the text cannot say which. It then concluded: "A stripper that has the
  input to hand can check; one working from a stream cannot, and should match the
  comments it wants to keep rather than the ones it wants to remove."

  It can. The token's source range less its text is exactly the delimiters:

      End - Start - len(Text) == 7   the document spelled it <!--...-->
      8   a comment closed by --!>        3   a bogus comment or a CDATA section
      4   a comment closed by the input   2   a processing instruction

  and that is exact rather than approximate because a comment's text is raw source
  bytes: measured, a CR, a CRLF and a NUL inside a comment are all passed through
  unrewritten, so nothing changes the text's length but the text.

  Two of the values collide - a truncated bogus comment and a processing
  instruction are both 2 - so what this tells you reliably is the ordinary form
  from everything else, which is the question a comment stripper actually has.

  `Comment` now carries the measured table, `Comment.Text` says the delimiters it
  removed were not necessarily `<!--` and `-->`, and `Comment.SetText` says it
  writes them back as `<!--` and `-->` whatever the document used - so a
  processing instruction that gets its text set becomes a comment, and the
  template engine downstream stops seeing it. `commentshapes_test.go` measures all
  of it, including the one-byte-at-a-time stripper that keeps the PHP block.
- **Removing or renaming an element acts on the token that closed it, which is
  not always its own end tag.** `Remove` and `Replace` already said so - they
  take the content up to that token with them, which is why removing the first
  item of `<ul><li>a<li>b<li>c</ul>` removes all three. Three other methods act on
  the same token and said nothing:

      <h1>a <em>b</h1><p>after</p>   em.RemoveAndKeepContent()
      <h1>a b<p>after</p>            // the </h1> is gone

      <h1>a <em>b</h1>               em.SetTagName("i")
      <h1>a <i>b</i>                 // written over the </h1>

      <ul><li><em>a<li>b</ul>        em.SetTagName("i")
      <ul><li><i>a<li>b</i>          // "b" is emphasised now

  `RemoveAndKeepContent` is the quiet one: every character of content survives and
  the shape of the document does not, so the paragraph ends up inside the heading.
  The element that loses its tag need not be one the selector named -
  `<h1><span>a <em>b</span> c</h1>` unwrapping the `em` loses the `</span>`.

  All three now document it, with the repair the name guard makes possible: register
  `OnEndTag` before removing, and write the token back when `EndTag.Name` is not
  this element's name. `EndTag.Remove` had a one-line doc and now says whose tag it
  may be removing. The package documentation's end-tag section says the general
  rule once, since this is the fourth thing that follows from it.

  `removeimplied_test.go` measures the whole matrix - four documents by six
  operations - so the examples in those comments cannot drift, and includes the
  well-formed document where none of this happens, which is why it is easy to ship.
- **"Independent `Writer`s on separate goroutines are fine" is a statement about
  the `Writer`.** An `Option` holds no state and can be passed to as many
  `Writer`s as you like - and the function inside it is shared with all of them,
  so anything it closes over is shared too. Building the option set once at
  startup and reusing it per request is the obvious thing for a server to do, and
  it is where this goes wrong: measured, four goroutines sharing one counting
  handler over 200 links each reported **655 of 800** matches, and the race
  detector flagged two races.

  `Option`, `Writer` and the README now say so, with the two shapes that work:
  build the options where the state lives, once per rewrite, or synchronise what
  they share. The cost of building them again is about seven allocations per
  distinct selector, which the cost section already measures.

  `sharedhandler_test.go` tests both correct shapes rather than the broken one -
  a test that races would fail the `-race` build it exists to inform - and records
  the measured numbers in a comment.
- **The fallback for an unrepresentable character has three answers, and
  `WithEncoding` named one.** It said "A character the target encoding cannot
  represent is emitted as a numeric character reference rather than dropped or
  replaced". True for content, attribute values, streamed content and a
  document-end insertion. `SetTagName` and `Comment.SetText` refuse it instead:

      content, any ContentType   &#128512;
      an attribute value         &#128512;
      streamed content           &#128512;
      appended at document end   &#128512;
      SetTagName                 refused
      Comment.SetText            refused

  The refusals are right rather than inconsistent - there is no such thing as a
  reference in a tag name, and a comment holds characters rather than references,
  so emitting one would put eight characters where the caller asked for one. The
  section says so now, and adds what the reference is worth: it is a character
  only to something that decodes references, which a script and a style do not.
  Measured for windows-1252 and iso-8859-2 in `encoding_test.go`.
- **`RemoveAndKeepContent` is a third way into the raw-text hazard.** Unwrapping
  one of the ten elements whose content is not markup turns that content into
  markup:

      <script>var x = "<img src=x onerror=alert(1)>"</script>
      e.RemoveAndKeepContent()
      var x = "<img src=x onerror=alert(1)>"

  and the image is an element. Measured for all ten. Nothing is inserted and
  nothing is renamed, so neither `ErrRawTextBreakout` nor the note on
  `SetTagName` covered it.

  The shape that invites it is worth naming: a sanitiser with an allowlist that
  unwraps everything not on the list. Few allowlists include `noembed` or `xmp`,
  so a payload placed inside one is inert until the sanitiser unwraps it. The
  method now says so, and says to remove the element instead or check the tag name
  first. `ErrRawTextBreakout` points at both doors.
- **`EscapeAttribute` is not "the escaping `SetAttribute` would have done for
  you", and the section on building markup said it was.** The claim is exact for
  `EscapeText` and `Text` - byte for byte, and asserted as a property. It is not
  for the attribute pair:

      value      SetAttribute   EscapeAttribute
      a"b        a&quot;b       a&quot;b
      a'b        a'b            a&#39;b
      a<b        a<b            a&lt;b
      a&b        a&b            a&amp;b
      a&amp;b    a&amp;b        a&amp;amp;b

  Both are right for their job - `SetAttribute` writes the quotes so it only has
  to escape those, while hand-built markup might use either quote and a bare `&`
  in it could begin a reference the caller did not write - and the difference means
  a value read from a document round-trips through one and is escaped again by the
  other. The section carries the table now, and `escape_test.go` asserts the
  difference rather than assuming it.
- **`SetAttribute` writes one copy of a duplicated attribute; `RemoveAttribute`
  takes them all, and only one of them said so.**

      <a href="first" href="second">
      e.SetAttribute("href", "safe")
      <a href="safe" href="second">

  A browser reads the first, so the rewrite took effect there - and
  `RemoveAttribute`'s own reasoning applies here too: what a browser drops on
  parse is not necessarily what the next parser in the chain drops. A rewrite that
  sanitises by changing a value rather than removing it leaves the value it was
  sanitising. `SetAttribute` now says so, gives the remove-then-set recipe, and
  says why it is not done for you: finding out whether a name is duplicated means
  listing every attribute on every call to the most-used method in the package, to
  change the answer for the documents that have a duplicate and move the attribute
  in all the rest.

- **Mutating an element while iterating its attributes is safe, and nothing said
  so.** `Attributes` documented only that the iterator must be consumed inside the
  handler. Setting or removing inside the loop takes effect and does not disturb
  the walk, and an attribute added inside the loop is not visited - measured by
  adding one per iteration, which terminates at the original count rather than
  growing without end.
- **What `ContentType.Text` guarantees, stated at the level it holds.** Nothing it
  writes can become a tag - now a property over every document and value the
  generator produces, for the plain path, the streaming path and `EscapeText` used
  by hand, comparing the sequence of tags in and out.

  The guarantee is about the markup and not about the tree, and the difference is
  measurable. Tree construction responds to the presence of text, so one character
  can change the tree while adding no tag:

      <p><a><div></div></a></p>    tree: <p><a></a></p><div></div><p></p>
      <p><a><div>x</div></a></p>   tree: <p><a></a></p><div><a></a></div><p></p>

  The second has an `<a>` the markup does not contain, because inserting a
  character makes the parser reconstruct the active formatting elements there.
  Appending "x" as `Text` through this library does the same. So "this rewrite
  cannot change the structure" is true of the bytes and false of the tree, and a
  program promising the stronger version is promising something the format does not
  allow. Pinned in `differential/textstructure_test.go`, with the well-nested
  shapes where it does not happen.
- **`Comment.SetText` refuses; its documentation said it escapes.** The sentence
  was "The value is escaped so that it cannot terminate the comment early, so
  untrusted input is safe". The value is rejected:

      c.SetText("--><img src=x>")
      lolhtml: comment_text_set: Comment text shouldn't contain a
      comment-closing sequence.

  Nothing breaks out either way, and the difference is what a caller has to
  handle: passing arbitrary text fails the rewrite instead of producing a
  sanitised comment. The doc now says so, and says why refusing is the only honest
  option - a comment ends at `-->` or `--!>` and holds characters rather than
  references, so there is no escaped spelling of those that a comment can hold and
  still mean.

- **Building a comment by hand has no guard, and the section about building markup
  by hand did not mention comments.** `Append("<!-- "+title+" -->")` with a title
  containing `-->` or `--!>` lets an element out; `EscapeText` stops that and
  changes what the comment says. So the section now covers the third context, and
  says the answer there is different in kind: remove the sequence from the value,
  or use `SetText` on a comment that already exists.
- **The `# Cost` section explained the two-pass ratio with a reason that points
  the wrong way.** It said a second pass roughly doubles the allocation count,
  "a ratio that does not grow with document size, since almost all of it is
  building the rewriter". The ratio does hold - measured 24/52 at 2 elements,
  45/98 at 40, 425/866 at 800 - and the reason is not that. At 800 elements one
  pass costs 425 allocations, so the per-element work dominates by a long way; the
  ratio holds because the second pass re-parses the document and runs every
  handler again.

  That matters because the old reason invites the conclusion that two passes are
  cheap on a large document, which is the opposite of the truth. The section now
  carries the table and says so, and `TestASecondPassCostsTwice` gates the ratio -
  the counts move with the toolchain and the doubling does not.
- **`ErrDetached` claimed more than it does.** It said it is returned by "any
  method on a rewritable unit ... called after its handler has returned". Only
  mutators return it. A getter has nowhere to put an error without a second
  return value, so a detached one answers with a zero value and says nothing:
  `TagName` `""`, `CanHaveContent` false, `SourceLocation` `{0, 0}`, `Attribute`
  `("", false)`, `Attributes` no iterations. `Remove` and `ClearEndTagHandlers`
  return nothing at all, having no error to give.

  So a detached unit gives plausible answers, and `Element.Attribute` cannot tell
  "the attribute is absent" from "the element is gone". `HasAttribute` can, purely
  because its signature has room for an error - an accident rather than a design,
  and worth knowing when choosing between them. `Detached()` answers directly and
  costs nothing.

  `detached_test.go` enumerates the surface: 38 mutators and every getter on all
  six unit types, because a rule stated in prose and checked nowhere is how this
  one drifted.
- **A rename is the way round `ErrRawTextBreakout`.** Whether content is markup is
  decided by the element it is in, so `SetTagName` across the raw-text boundary
  reinterprets everything inside:

      <script>var x = "<img src=x onerror=alert(1)>"</script>
      SetTagName("div")
      <div>var x = "<img src=x onerror=alert(1)>"</div>

  and the image is now an element. Measured for `script`, `style`, `textarea` and
  `title`. Nothing is inserted, so the breakout check has nothing to look at, and
  the call happens at the start tag before any content is reported, so the library
  cannot see it coming. The other direction is quieter and still a change of
  meaning: renaming an ordinary element to a raw-text one turns its markup into
  text.

  `SetTagName` now says so and says what to do instead - replace the element, or
  read the content and decide at the end tag - and `ErrRawTextBreakout` points at
  it, since that is where a reader looks for this hazard. Pinned in
  `settagname_test.go`, including that a rename which does not cross the boundary
  leaves the content alone.
- **A table can contain things that a parser says are not in it.** Content that
  cannot be inside a table is moved to just before it by a parser - foster
  parenting - and there is no tree here to move anything in, so it is reported
  inside the table and emitted there. The output is byte-identical, because a
  browser reading it fosters the content out again, so nothing looks wrong.

  Two consequences, both measured against `x/net/html`: a text handler on the
  table is given text that is not in the table, and `Element.Remove` on the table
  removes that content where a tree-based edit keeps it -
  `<p>before</p><table>stray<tr><td>a</table><p>after</p>` becomes
  `<p>before</p><p>after</p>` here and `<p>before</p>stray<p>after</p>` in a
  tree. There is a package-doc section and a note on `Element.Remove`, and
  `differential/table_test.go` pins five shapes.
- **`Element.SetUserData` named the one thing it cannot do.** It said the value
  is "readable by any later handler that sees the same element - most usefully an
  end-tag handler". An end-tag handler cannot read it: `EndTag` has no user data,
  because lol-html provides it for elements, comments, text chunks and the
  doctype and not for end tags, and the element is detached by then, so reading
  through the captured `Element` returns nil. Measured.

  What it is for is a second handler given the same unit - two selectors matching
  one element, or an `OnComment` and an `OnDocumentComment` on one comment - which
  the documentation did not mention. And it is per unit rather than per position:
  a value set on one text chunk is not readable from the next chunk of the same
  node, so it is not somewhere to accumulate across chunks. All of it is pinned in
  `userdata_test.go`, including that closing over a Go variable does work, which
  is what an end-tag handler is written inside a start-tag handler for.
- **Nothing said how to select an element whose name contains a colon, and the
  error blames something else.** `esi:include` is rejected with "Unsupported
  pseudo-class or pseudo-element in selector", which names something the caller
  did not write; the answer is `esi\:include`. Same for `[xlink\:href]`.
  `WithESITags` described a worked example with a handler on the include and
  never showed a selector that could match one.

  There is now a package-doc section with the measured table, `WithESITags`
  shows the escaped form, and `SelectorError` adds the answer when the selector
  it rejected contains an unescaped colon - suppressed for `::`, where a colon
  cannot be part of a name.

  The dot is worse and cannot be helped the same way: `.a.b` parses as "class a
  and class b", matches nothing, and reports no error at all, so the handler
  simply never runs. `.a\.b` is the escaped form. Both are pinned.
- **An end-tag handler has three timings, and the documentation described two.**
  The guard added for insertions - compare `EndTag.Name` against the element's
  tag name, and do nothing when they differ - is right for writing at a position
  and too coarse for observing that an element is over:

      <p><em>a</em> b</p>       at </em>, its own tag, exactly where it ends
      <p><em>a</p>b             at </p>, an ancestor's, exactly where it ends
      <ul><li><em>a<li>b</ul>   at </ul>, an ancestor's, and "b" was already
                                reported: the <em> ended at the second <li>

  A foreign end tag is where the element ended when an ancestor's end tag closed
  it, and later than where it ended when a sibling's start tag did. Nothing in
  the callback separates those, so anything accumulating - a converter closing an
  emphasis, a counter measuring an extent - has to keep the stack of open
  elements itself. `TestActingOnAForeignEndTagCanWrapTooMuch` shows the failure as
  a rewrite: the naive version turns `<ul><li><em>a<li>b</ul>` into `*ab*`.
- **The `# Cost` section gave a number for building a rewriter that nothing
  measured, and it was wrong.** It said "about five allocations per distinct
  selector, one fewer for a repeat". Measured with the options built beforehand,
  so the count is registration rather than the caller's slice: 13 allocations
  with no handlers, and a marginal cost around seven per distinct selector that
  falls as more are registered, because the slices behind them grow in steps. A
  repeat saves about one and a half, not one.

  The section now carries the table and says the numbers are gated as a range
  rather than a value, which is what `TestSelectorRegistrationCost` does: the
  marginal cost has to be single-digit and a repeat has to be cheaper than a
  distinct one. The per-invocation figures next to it were already gated; these
  were the ones nothing looked at.
- **What survives a write boundary, and what a boundary can fall inside.** The
  chunking of text was documented as having "no guaranteed boundaries", which is
  true and reads as weaker than it is: a chunk never contains part of a
  character. Measured at one byte per write over two-, three- and four-byte
  runes, in text, in a comment and in an attribute value. Content going the other
  way has the opposite rule - `Sink.WriteChunk` takes a partial sequence and
  joins it to the next write - so the two are worth stating together.

  The consequence is the useful part: a transform applied per chunk is safe per
  character and wrong per pattern. `strings.ToUpper` on a chunk is correct
  however the document arrived; a regular expression looking for a word is not,
  because the word can straddle two chunks.

  And the rest of it is invariant. Element, comment and doctype calls and their
  order, tag names, attributes, source locations, end tags, and the text of each
  node are identical however the input arrived - 22 documents against seven write
  patterns, in `examples/gip/chunkinvariance`, where the text handler's call
  count moves by up to 500x on the same document and nothing else moves at all.
- **The worked example for reading an element's whole text corrupted character
  references.** It accumulated `TextChunk.Text`, which is source, and wrote the
  result back as `Text`, which escapes it again. On the `<a href="/x">click
  <b>here</b></a>` the example was written against, the two are the same; on
  `<a href="/x">caf&eacute; <b>&amp; more</b></a>` with `strings.ToUpper` it
  produced `CAF&amp;EACUTE; &amp;AMP; MORE`, which renders as those characters.
  The example now decodes with `html.UnescapeString` first, and
  `textroundtrip_test.go` transcribes both versions so the corrected one cannot
  drift back.

  `TextChunk.Text` gains the three-way comparison behind that: as `Text` without
  decoding escapes twice, as `HTML` without decoding is stable and mangles the
  reference into something that is not one, and decode-then-`Text` is the only
  spelling that is right on the first pass and unchanged by the second.
- **Nothing said how many times the destination is written to, and it is not what
  a reader would guess.** It is decided by what the rewrite does rather than by
  the document: a start tag a handler mutated is re-serialised piece by piece, so
  one `SetAttribute` turns one write into twelve on a single element, and 2000 of
  them turn one 132 KB write into 22,001 writes with a median size of one byte -
  22,001 system calls on an unbuffered socket, for 162 KB. Passthrough of the same
  document in one `Write` is one write. `NewWriter` now says so and says to wrap
  an unbuffered destination in a `bufio.Writer`, and `writecount_test.go` pins
  every number in that table.
- **`OnText` said less than it needed to, in two directions.** It did not point
  at `OnDocumentText` the way `OnComment` points at `OnDocumentComment`, and
  nothing said that no selector reaches text outside every element - so
  `OnText("*", redact)` leaves a fragment untouched and reports nothing.
  Measured and now in the doc: `hello` gives 0 selector calls and 2 document
  ones, `before<p>a</p>after` gives 2 and 6, and `<html><body>a</body></html>`
  gives 2 and 2, which is why a test written against a full document passes.

  And `IsLastInTextNode` said the final chunk is "frequently empty". It is a call
  of its own and carries no bytes in every shape measured - a short node, a
  100 KB one, character references, all four raw-text elements, and one-, two-,
  three- and five-byte writes. The cost follows: a text handler runs at least
  twice per text node and about half its calls on prose are handed nothing.
- **`IsSelfClosing` says it is about the source text, and that it is not a test
  for emptiness.** Its comment said a trailing slash "is ignored" in HTML, which
  is true of the parser and not of this method: `<div/>` reports self-closing and
  then has content and an end tag like any other div. So a rewrite using it to
  decide whether an element can hold content is wrong wherever an author wrote a
  slash out of habit, and `CanHaveContent` is the method for that - right in all
  eighteen cases measured. In foreign content the two agree, because there the
  slash is what closes the element and `<svg><rect/>` and `<svg><rect>` are two
  different trees.

- **`TagNamePreserveCase` says what it preserves, which is the spelling and not
  the meaning.** Its comment pointed at foreign content - "which matters for
  foreign content such as SVG's `<linearGradient>`" - as though it produced the
  useful form there. It produces whatever was typed. A parser applies the SVG
  tag-name adjustment, so a browser holds `linearGradient` for all three of
  `<linearGradient>`, `<LINEARGRADIENT>` and `<lineargradient>`; this library
  reports the lower-cased name from `TagName` and the source spelling from
  `TagNamePreserveCase`, and neither is canonical unless the page happened to
  write it that way. Comparing either with a canonical SVG name is therefore wrong
  for two spellings out of three. Nothing is wrong on the way out - the source
  spelling is emitted and a browser adjusts it - and `SetTagName` writes what it
  is given, so a rewrite can normalise if it wants to.

- **`PreallocatedParsingBuffer` says what it costs.** It read like a performance
  knob - "at the cost of reallocations later" - and behaves like a charge against
  `MaxMemory`. Measured, the smallest limit that completes one document rises by
  about whatever is preallocated:

      prealloc      0     16   1024   4096   8192
      floor       832    848   1856   4928   9024

  and no document tried was cheaper with a buffer than without one, including four
  chosen to reallocate a lot. The Go allocation count is identical at 0, 1024 and
  8192, so what it buys is invisible from here. Setting it equal to `MaxMemory` is
  accepted by validation and fails as soon as a selector has to match: 1024 and
  1024 with one `OnElement` bails out on `<p>x</p>`, while the same pair with no
  handlers or document-level handlers only is fine, because nothing needs the
  buffer.

- **`WithEncoding` says that nothing is sniffed, and corrects what a wrong label
  costs.** That the rewriter ignores a document's own `<meta charset>` was written
  only in an internal comment inside `defaultConfig`, where no caller reads it.
  It is now on `WithEncoding`, along with the fact that inserting a charset meta
  does not change the bytes - so one naming a different encoding produces a
  document that lies about itself, which every reader believes.

  The claim about a misdeclared encoding was also too general. It said passthrough
  bytes stay identical; measured, that depends on whether a **text handler** is
  registered. Text is decoded and re-encoded only then, and a byte invalid in the
  declared encoding becomes U+FFFD on the way out even if the handler does
  nothing. On `<p>caf\xe9</p>` declared as utf-8: no handlers, an element handler,
  and an element handler that writes all leave the byte intact; any text handler
  replaces it. The earlier wording was right about the case it was measured on and
  wrong in general.

- **The README no longer describes behaviour the library stopped having.** It
  said that with `ContentType` `HTML` "a `</script>` in a string literal ends the
  element", which stopped being true three changes ago when that became
  `ErrRawTextBreakout`. It also never mentioned `EscapeText`, `EscapeAttribute` or
  the error itself, so a reader of the README alone would hand-roll escaping the
  library now provides and be surprised by a refusal it does not document. The
  test suite already contradicted the README and nothing compared the two.
  Corrected, with the limits of the refusal stated: inserting a whole `<script>`
  element as markup is still allowed, because its payload legitimately contains
  its own closing tag.

- **The package documentation says what happens when an attribute appears
  twice.** The HTML parsing specification calls a repeat a parse error and
  requires a parser to keep the first and drop the rest; lol-html keeps them all,
  and the API is split over which copy counts. Selectors, `Attribute`,
  `HasAttribute` and `SetAttribute` act on the first - the copy a browser would
  have kept. `Attributes` and `AttributeList` yield every copy. `RemoveAttribute`
  removes every copy. Two of those halves were recorded in test comments in the
  `properties` module; the selector half was not recorded anywhere, and it is the
  one a rewrite is most likely to depend on unknowingly, since `[a="v"]` does not
  match `<p a="x" a="v">`. The whole rule is now in one section, referenced from
  each of the five methods, and pinned in `duplicate_test.go`.

- **The package documentation names the ordering constraint that shapes every
  rewrite.** Output is produced as input is consumed, so an insertion can only go
  at a position the rewriter has not passed - which means a rewrite whose content
  depends on evidence appearing after the position it writes to is not a one-pass
  rewrite at all. Head content derived from the body is the common case: a
  rel=next from a pagination nav, a canonical URL from the page's own content, a
  table of contents from the headings. Nothing reports it, and three of the
  programs in `examples/gip/` walked into a milder version of it - deciding at the
  first element what to insert, then finding a second candidate later and both
  inserting and rewriting. The new section says where to put the decision when
  both the evidence and the position exist, and what two passes cost when they do
  not: about double the fixed allocation count, flat in document size, plus a
  buffer that is not.

- **The numeric-reference fallback is not safe inside a script or a style, and
  both sections that describe it now say so.** `WithEncoding` documents that a
  character the target encoding cannot represent is emitted as a numeric
  character reference, and the script section documents that neither
  `ContentType` is right inside raw text. Each is correct; together they hide a
  case where both rules are followed and the output is still wrong. Inserting
  `日` into a `<script>` in a windows-1252 document produces
  `<script>var s = '&#26085;'</script>` - eight literal characters in the script
  instead of the one that was meant - with no error from the insertion, from
  `Write` or from `Close`, and no difference between `Text` and `HTML`, because
  the substitution happens after escaping. There is nothing to fix in the call:
  either keep the body inside the document's encoding, using an escape the target
  language understands, or serve the document as UTF-8.

- **The selector section says how attribute values are matched, not just which
  selectors exist.** It said names are matched case-insensitively, which is true
  and reads as though it covered values. Values follow a different and non-uniform
  rule: HTML matches them case-insensitively for a fixed list of 46 attributes and
  exactly for everything else, and the rewriter implements that list precisely.
  So `[rel="canonical"]` matches `rel="CANONICAL"` while `[name="foo"]` does not
  match `name="Foo"`, and `.Foo` does not match `class="foo"`. The list is now
  written out, along with the `i` and `s` flags as the way to stop depending on
  it. Verified against all 46 plus a control group of 17 that are not on it.

- **`OnDoctype` says that it fires for doctype tokens, not for the document's
  doctype.** An HTML parser honours a DOCTYPE only before anything else has been
  seen and discards the rest; the handler is told about all of them. Checked
  against x/net/html: a doctype after an element, after text, inside `<html>`, or
  a second one is reported by the handler and kept by nobody. So a rewrite that
  leaves a page alone because it already has a doctype can be wrong, and a page
  whose source begins with a meta and then a DOCTYPE renders in quirks mode. The
  comment also records that a doctype cannot be added or replaced - `Doctype` has
  no insertion methods because the C API has none - and why prefixing the output
  is not a substitute.


- **Selectors are namespace-blind, and `NamespaceURI` does not rescue you.** A
  tag name in a selector matches that name in any namespace, so `title` matches
  an SVG tooltip as well as the document title and `a[href]` matches HTML, SVG and
  MathML anchors. `NamespaceURI`'s comment said it "returns the element's
  namespace URI"; measured, it returns the namespace the element's *children* are
  parsed in, so every integration point - SVG's `title`, `desc` and
  `foreignObject`, MathML's `mi`, `mo`, `mn`, `ms`, `mtext`, and `annotation-xml`
  with an HTML encoding - reports the HTML namespace despite being a foreign
  element. Both titles therefore look identical to a handler. The package
  documentation gains a section on it with the two things that do work, including
  why `head title` is not one of them: `<head>` is optional, so it matches nothing
  in a document that omits it.
- **`DocumentEnd.Append` says what it appends to.** It said "adds content at the
  very end of the document", and it adds content at the end of the output - which
  is wherever the input stopped. An input cut off inside a script, a comment, a
  raw-text element or a doctype swallows the appended markup as text, and one cut
  off inside a start tag absorbs it into an unterminated attribute and turns the
  remainder into attributes of that element. Measured: seven of twelve
  mid-construct documents produce no element from the append, `Write` and `Close`
  both succeed, and `WithStrict` changes nothing. This is what a rewriter sees
  when an origin dies mid-stream, which is when injected instrumentation matters
  most, so the doc now says it and points at the end tag as the alternative -
  along with the reason that is not a complete answer either: `</body>` is
  optional in HTML, and `Element.OnEndTag` on a body without one never fires.

- **The package documentation is ordered how it is read.** It had grown to
  fifteen sections in the order they were written, which is not the order anyone
  needs them: ":not() is wrong for anything but a single simple selector" came
  fourth, ahead of handler order, and the two sections a reader most needs before
  inserting anything - neither content type is right inside a script, and
  hand-built markup escapes nothing - were ninth and twelfth. It now runs
  streaming, handler lifetime, handler order, selectors, character references,
  then the four insertion sections under one "Inserting content" heading that
  says what they cover, then reading an element's text, removal, comments, cost,
  errors. No prose changed; the only addition is the grouping section.

- **Selectors match the document as it arrived; handlers see each other's
  edits.** The handler-order section said the second half. The first was
  unstated, and it is what makes a rewrite predictable: matching is decided
  before any handler runs, so renaming a class does not trigger a rule keyed on
  the new name, renaming a tag does not trigger a rule keyed on the new tag, and
  removing the attribute a selector matched on does not un-fire a handler that
  was going to run. No cascade, no order-dependence in which handlers fire, no
  way for a rewrite to trigger itself - and no way to act on what another handler
  produced without a second pass. Pinned by `order_test.go`.

- **The supported selector subset is written down.** `SelectorError` used to say
  "lol-html implements a subset of CSS selectors; see its README for which",
  which sends a caller to another project's documentation for something they need
  before their code compiles. The subset is now listed, with the rule behind
  almost all of it: a selector works if the rewriter can decide it when it sees
  the start tag, so `:first-child` and `:nth-child(2n+1)` are in and
  `:last-child`, `:only-child` and `:empty` are out. Every attribute operator
  works, including the `i` and `s` case flags; `[style]` matches `style=""`; tag
  and attribute names match case-insensitively. `selector_test.go` exercises
  every row of the list in both directions, so the documentation cannot drift
  from the implementation.

- **Building markup yourself makes you the serialiser.** Every path that writes a
  value escapes it: `SetAttribute` escapes the quote and the ampersand,
  `ContentType` `Text` escapes the three characters that would be markup. The one
  path that escapes nothing is markup you construct and pass as `HTML` - and that
  is the tempting route for turning one element into another. A single-quoted
  attribute in the source may hold a bare double quote, so
  `<iframe title='" onload=alert(1) x="'>` put through
  `e.Replace(`+"`"+`<div data-x="`+"`"+`+title+...)` produces a div with a working
  event handler taken from the document. The same value through `SetAttribute` is
  inert. Now documented, with the recommendation that follows: change the element
  with `SetTagName`, `SetAttribute` and `RemoveAttribute` rather than replacing
  it, which does the same job with every value escaped and in less code. Pinned by
  `contenttype_test.go`, which also records exactly what `SetAttribute` escapes
  and what it correctly leaves.

- **"Decide on the decoded form, rewrite the raw one."** The raw-source
  behaviour was documented as a fact, with `html.UnescapeString` mentioned for
  "when you need the decoded form". The security shape of it was not: a browser
  decodes an attribute value before acting on it, so `javascript:x()`,
  `java&#9;script:x()` and `&#106;avascript:x()` all execute, while a filter
  comparing the raw string catches only the first. The rule is now stated with
  those three examples, and its other half too - having decoded a value to decide
  about it, write the original back, since `SetAttribute` takes raw source and
  writing the decoded form turns `&amp;` into a bare `&`. Pinned by
  `contenttype_test.go`, which fails if the naive check ever stops missing the
  encoded forms.

- **`CanHaveContent` governs four methods that fail differently.** It said they
  could not "do anything" when it was false. In fact `Append`, `Prepend` and
  `SetInnerContent` silently do nothing on a void element, while `OnEndTag`
  returns an error that fails the rewrite - so a handler on a selector that can
  match a `<br>` must check before calling `OnEndTag` and can call the others
  blind. `Before`, `After` and `Replace` are unaffected.

- **`Element.SourceLocation` is the start tag, not the element.** It said so, but
  not what to do about it: the element's extent is that Start with the End taken
  from the end tag's own location, and an element whose end tag never arrives has
  no measurable extent because the handler never runs. Now spelled out with the
  recipe.

- **A comment handler fires for things that are not comments.** "Comment" is the
  HTML parser's word, and the spec turns several malformed constructs into bogus
  comments. `<?php echo $x; ?>` is a comment, with the text `?php echo $x; ?`.
  So is `<?xml version="1.0"?>`, and so is `<!bogus>`. A rewrite that removes
  every comment therefore removes PHP blocks, XML declarations and processing
  instructions, silently, because each of them is well formed as far as the
  parser is concerned. The `<?` forms can be told apart by their text, which
  keeps the `?`; `<!x>` cannot - it has the same text as `<!--x-->`, and
  `Comment.SourceLocation` against the input is the only discriminator.
  Conditional comments are not one comment either: the downlevel-revealed form
  is two, with real markup between them, and only the first contains `[if`, so a
  filter keyed on that keeps the opening half and drops the closing one. All of
  it documented and pinned by `comment_test.go`.

- **Two insertions of the same kind do not always come out in call order.**
  Every insertion goes immediately adjacent to the unit, so the newest is always
  the closest to it. For `Before` and `Append` that reads as in order; for
  `After` and `Prepend` it reads as reversed. Three calls inserting "1", "2",
  "3": `Before` gives `123<p>`, `After` gives `<p>321`, `Prepend` gives
  `<p>321t`, `Append` gives `<p>t123`. One rule, two apparent behaviours, and no
  way to guess which method does which. It matters when several calls assemble
  one thing - building a comment out of a delimiter, some text and a closing
  delimiter with three `After` calls emits `-->text<!--`, which is
  valid-looking output containing broken markup. Now documented on the four
  methods and in the package docs, with the whole table pinned by
  `insertorder_test.go`.

- **A text node is not an element's text.** `OnText` fires for text inside
  descendants too, and `IsLastInTextNode` marks the end of a text node rather
  than of the element's content - the same thing only when the element contains
  no markup. So the documented recipe, accumulating to `IsLastInTextNode` and
  replacing there, replaces each text node separately:
  `<a>click <b>here</b></a>` becomes `REPLACED<b>REPLACED</b>`. A test document
  without nested markup looks perfect and hides it. The three recipes that work
  are now written down, with what each does to the descendant markup, and pinned
  by `textnode_test.go`.

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

- **`HandlerError.Selector` is now filled in for end-tag and streaming
  handlers.** Both are registered from inside a handler that has a selector, and
  both reported an empty one - so an error from a program with twenty handlers
  said `end-tag handler` and left twenty candidates. They inherit the selector of
  the handler that registered them, and the message says it:
  `end-tag handler for "a[href]"`.

  `Kind` also has a seventh value the documentation did not list, `streaming`,
  which is a `StreamFunc`'s own failure - reported separately because it runs
  later than the handler that registered it. `TestEveryDocumentedKindIsReachable`
  now requires every documented kind to be produced by something and nothing to
  produce a kind that is not documented, which is what noticed it.
- **A poisoned `Writer` now says why it is poisoned.** lol-html cannot resume
  after an error, so the first failure is reported from whichever call was
  running and every later `Write` and the `Close` refuse. Those refusals returned
  the bare `ErrPoisoned` sentinel, which names a state and not a cause - so a
  caller writing the ordinary Go shape, write and then check `Close`, learned that
  something had failed and never what. `ErrPoisoned` is now wrapped around the
  first error, so `errors.Is` and `errors.As` reach the handler error or the
  destination-writer error underneath it however late they are asked. A handler
  panic is the exception: it poisons the `Writer` on its way to the caller without
  leaving an error, and the sentinel then stands alone.

- **`WithGracefulBailOut` before `WithMemorySettings` no longer does nothing.**
  Every other option sets one field, so order cannot matter;
  `WithMemorySettings` takes a whole struct and replaces every field in it,
  including the graceful flag an earlier `WithGracefulBailOut()` had set. The
  difference is not subtle once it bites: on a bail-out, the graceful path flushed
  2021 bytes of already-rewritten output in one measured case and the strict path
  flushed none. The flag is now kept separately and combined by union, so the two
  compose in either order, and `MemorySettings{GracefulBailOut: false}` does not
  turn off an explicit `WithGracefulBailOut` - there is no reason to ask for both.


- **`.gitignore` actually ignores rapid's failure files now.** The pattern was
  `testdata/rapid/`, and a pattern containing a slash is anchored to the
  directory holding the `.gitignore` - so it matched only a top-level
  `testdata/rapid/`, and the only module that uses rapid is `properties/`. It has
  therefore never ignored anything. Three `.fail` files reached a commit before
  it was noticed, from deliberately breaking an escaper to check the new
  properties fail.

- **`SetAttribute` no longer claims to make untrusted input safe.** Its doc
  comment said "the value is escaped as needed, so it is safe to pass untrusted
  input", and the package documentation said it escaped the quote and the
  ampersand. Measured, it rewrites the double quote and nothing else - which is
  correct, because its argument is raw attribute-value source, the mirror of what
  `Attribute` reports. `Attribute`'s own comment said so all along. The two now
  agree, and `SetAttribute` says what the difference between source and a literal
  value costs: passing the five characters "&amp;" sets an attribute a browser
  reads as one "&".

- **The table of contents in `examples/gip/toc` no longer carries an
  injection.** It interpolated a heading's id straight into an href it quoted
  itself, so a heading with `id='a" onmouseover="alert(1)'` produced a working
  event handler in the contents. This is what the missing escaper cost in
  practice, in a program written after the documentation warning existed.
- **The handle-leak assertions no longer measure other tests' garbage.**
  `LiveHandles` counts the whole process, and a `Writer` dropped without being
  closed releases its handles from a `runtime.AddCleanup` callback that runs one
  GC cycle later. `TestUnclosedWriterIsReclaimed` abandons 200 writers on
  purpose, so a later test sampling the count before and after its own work could
  see it fall: five subtests reported between -1 and -4 "leaked" handles on one
  CI run. Every equality assertion now samples through `settledHandles`, which
  drains the queue first, and the manual-writer test keeps its writer alive
  across the measurement so it cannot collect what it is counting. Demonstrated:
  the same window measures -20 unsettled and 0 settled, and the assertions still
  report 30 leaked handles when the streaming panic fix is reverted.

- **The allocation gate no longer demands an exactness the measurement cannot
  give.** It compared the per-match slope for equality with an integer and
  required the extrapolated fixed cost to be identical at both document sizes.
  Neither holds in general: on darwin/amd64 under Rosetta the same code measured
  222 allocations at 100 matches and 823 at 400, a slope of 2.003, because one
  allocation of setup appeared somewhere between the two sizes. The gate failed
  on a difference of one allocation and passed on the same commit elsewhere,
  which makes it noise rather than a signal. The slope is now compared within
  0.05 and the base within 8 allocations - fifteen times the observed noise, and
  more than an order of magnitude below the regression it is there to catch,
  which is verified by injecting one extra allocation per match and watching
  both checks fire.

- **The allocation-complexity gate no longer fails the AddressSanitizer build.**
  `alloc_test.go` asserts how many times a rewrite allocates, and `-asan`
  replaces the allocator with one that allocates on its own account: a path that
  allocates once per match allocates four times per match under the sanitizer,
  and setting an attribute goes from two to 19.84. The gate was therefore
  measuring the sanitizer rather than the binding, and the `sanitize` job had
  failed on every commit since the gate landed. The six allocation tests now
  skip under `-asan`, behind a build constraint, and say why where they skip.
  The sanitizer still runs the rest of the suite, which is what it is for.

- **`:not()` is wrong for anything but a single simple selector.** Upstream's,
  and not fixable here, so it is documented and pinned instead. `:not()` is
  correct with one simple selector - `:not(div)`, `:not(.a)`, `:not([href])`,
  `:not(:first-child)`. Given a compound selector it negates each part separately
  and requires all of them, which is the wrong half of De Morgan's law:
  **`:not(div.a)` is evaluated as `:not(div):not(.a)`**. On a document of
  `div.a div.b span.a span.b` it matches one element where CSS says three, and
  `:not(div.a, span.b)` matches nothing at all. There is no error. A filter
  written `OnElement(":not(a.trusted)")` therefore skips every anchor and
  everything carrying that class, which for a filter is a hole rather than a
  nuisance. The cause is `add_selector_components` in `selectors_vm/ast.rs`,
  which flips the negation flag per component and adds each to a predicate whose
  expressions are ANDed; upstream's own tests only ever put a single simple
  selector inside `:not()`. `selector_test.go` asserts the wrong behaviour
  deliberately, so a fix upstream fails the test and the documentation gets
  corrected rather than rotting.

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
- **`examples/gip/selectorcheck`: report every selector a rewrite cannot use, before
  it starts.** B193, in two halves.

  `NewWriter` returns on the first rejection, so a list with five bad selectors names
  one - fix it and the next appears, five round trips for five mistakes. Checking
  each selector on its own names them all, and it is the only way to.

  It is also cheaper at scale, because registering selectors together is superlinear
  in time. Fastest of ten on an M3 Pro:

      selectors      build   µs per selector   allocations   per selector
             10        8µs             0.700            73           7.30
            100       81µs             0.810           571           5.71
            500      615µs             1.228          2734           5.47
           1000    1.956ms             1.956          5408           5.41
           2000    5.896ms             2.948         10718           5.36
           4000   23.702ms             5.926         21524           5.38

  Four times the selectors for twelve times the time, while allocations stay at about
  5.4 each, flat from five hundred up. B172 recorded the allocation figure; this is
  the other half, and it matters for the programs that have thousands of selectors: a
  stylesheet-coverage tool, a sanitiser with a per-element allowlist, a rule engine
  fed from configuration. Validating a thousand selectors one at a time took 1.55ms
  against 1.944ms together.

  The program also adds a line where the library's message misleads - `:not(div p)`
  is rejected for the combinator inside the parentheses and reported as an
  unsupported pseudo-class, per B175 - while keeping the library's own words, which
  are what a search will match. It adds nothing where the library is clearer:
  `li + li` says "Unsupported combinator `+`" and that needs no help.

  Three of its own bugs. The comment marker was `#`, which ate the `#id` selector and
  reported a shorter list than it was given. The timing assertions were unguarded,
  which on the Windows runner's 340µs tick would have compared two figures reading
  zero. And then, guarded on the tick and still gating a *ratio*, they failed on the
  musl runner at 1.46x against a threshold of 1.5x - because there the fixed
  per-selector cost is nine times larger and the superlinear part is proportionally
  smaller. A threshold on that ratio is a threshold on the machine, which is what
  `docs/gip/GIP.md` exists to stop, so the gate is the allocation count and the
  durations are logged and compared nowhere.

- **`examples/gip/doctypepick`: choose what a rewrite does from the document's
  doctype, and only from a doctype the document's own parser will honour.** B192 is
  why the second half of that sentence is the turn.

  `OnDoctype` fires for every doctype token in the source. A parser honours the first
  one with nothing but whitespace and comments before it and drops the rest as parse
  errors. Measured against x/net/html using B174's behaviour:

      document begins with          OnDoctype reports   the parser is in
      <!doctype html>                          "html"   standards mode
      whitespace, then a doctype               "html"   standards mode
      a comment, then a doctype                "html"   standards mode
      text, then a doctype                     "html"   *quirks* mode
      an element, then a doctype               "html"   *quirks* mode
      a non-breaking space, then one           "html"   *quirks* mode
      nothing                                      ""   quirks mode

  Three rows where the handler fires and the mode is not what it says, so a rewrite
  that trusts the handler alone is wrong on those three and silent about it. The
  qualifying condition is the same one `examples/gip/deployid` measures for a meta
  reaching the head: only the five ASCII whitespace characters count, and a
  non-breaking space is text.

  The other half is that no peek is needed. The handler set is fixed when the
  `Writer` is built, so choosing by doctype looks like it needs a first look - and a
  peek is slower rather than faster (3.002ms against 2.959ms over an 87 KB document)
  because it parses its prefix twice, *and* it can miss the doctype altogether: after
  a 5000-byte comment the doctype ends at byte 5022, after two hundred comments at
  2015. Registering both sets and gating them is one pass and cannot miss.

- **`examples/gip/regions`: apply a different set of handlers to each region of one
  document.** B191, and it took four wrong premises of mine to get to it.

  Most of what a region sees is context-free, because a rewriter is a tokenizer
  rather than a tree builder: a `td` alone is the same token as a `td` in a table,
  an `option` alone the same as one in a select. A tree builder would disagree about
  both. That is what makes per-region handlers possible at all, and it is not the
  same as a boundary being safe.

  End tags are the exception and they set the rule. An end tag pairs with a start
  tag, so an element spanning a boundary is split: the region before never meets the
  end tag and the region after meets one with nothing open to match. Measured,
  `<div><p>A</p><p>B</p></div>` cut at 13 runs the div's `OnEndTag` handler once as a
  whole document and **zero** times across the two halves. So the rule is: cut where
  nothing is open.

  The sentinel test from B177 does not answer this. It answers whether the prefix
  *swallows* what follows, which is necessary and not sufficient, and it misses two
  things: a prefix ending in a bare `<` or `</`, which swallows nothing and still
  orphans the tail's tag name, and an open element, which it cannot see at all.
  Measured over one document, 10 of 79 offsets pass the cheap test and are unsafe.
  The exact check is to rewrite both halves with a probe that touches every kind of
  unit - elements, end tags, text, comments, the doctype - and compare against the
  whole; a weaker probe answers a narrower question and approves boundaries that a
  caller's own handlers would notice.

  `TestASafeBoundaryIsSafeForAnyHandlers` is the gate that matters: every offset the
  check approves is then split with five different handler sets, each touching a
  different unit kind, and all five have to agree with the whole. Four of eighty
  offsets are approved, which is what a conservative check looks like.

  Also fixes the same `fmt.Sscanf("%d")` bug I fixed in `examples/gip/gunzip` two
  turns ago and then wrote again here: it stops at the first character it does not
  understand and reports success, so `-at 1.5` would have split at byte 1. There are
  no other uses of it in the tree.

- **`examples/gip/stopafter`: rewrite until a marker and copy everything after it.**
  The mechanism is B189's, from `examples/gip/headonly`; what is new is that the
  marker can be any kind of unit, and B190 - where the rewrite resumes is not the
  same for all of them.

      marker            the prefix ends            resume at
      a comment         before the comment         the comment's Start
      an element        before its start tag       the element's Start
      an end tag        before the end tag         the end tag's Start
      text              before that chunk          the last chunk's End

  The first three read alike: the stopping unit was not emitted, so resume where it
  begins. Text is the exception, because a node arrives in several chunks and the
  earlier ones are written before the marker is recognised - so the prefix holds the
  whole node and the resume point is its end. Measured, on
  `<p>before STOP here</p><p>after</p>`: resuming at 3 emits the text twice,
  resuming at 19 is exact. The test asserts the duplication as well as the fix, so
  the choice is measured rather than asserted.

  And a marker inside a raw-text element is not a marker - for a comment handler.
  Measured for script, style, textarea and title: the bytes of a comment inside one
  are that element's text, so nothing stops. The text path does not get that for
  free, which was a bug in my first version: a document-level text handler is handed
  raw text like any other text, so a bare marker inside a script stopped the
  rewrite. The fix is the handler order - a selector-associated text handler runs
  before the document-level one for the same chunk - so a handler on those four
  marks the chunk before the general one decides about it, and the report counts the
  sightings that do not count.

- **`examples/gip/headonly`: rewrite the head of a document and pass the body
  through without parsing it.** A rewriter runs to Close and cannot be switched off
  part way, so "only the head" looks like it has to mean handlers that check a flag
  and do nothing. B189 says otherwise, and by two orders of magnitude.

  A handler can stop the rewrite by returning an error, and what has reached the
  destination is documented to be byte for byte what a fresh rewriter produces from
  that much input rather than a truncation. The stopping element's
  `SourceLocation().Start` is therefore exactly where the untouched input resumes,
  so the rest can be copied. Measured on a 114-byte head, fastest of twenty runs:

      body size   stop and copy   handlers gated off   a plain no-handler pass
      100 bytes            6µs                  7µs                       1µs
      10 KB                8µs                 99µs                      20µs
      200 KB              16µs              1.842ms                     375µs

  115x faster than gating at 200 KB and 23x faster than passing the body through a
  rewriter with no handlers, because it does not pass the body through anything. The
  outputs are byte-identical, which the tests assert against the gated version over
  eleven document shapes rather than assuming.

  Where the head ends cannot be `<body>`: a document need not spell one, and a
  rewriter reports the source. The rule is the specification's - the first element
  outside `base`, `link`, `meta`, `noscript`, `script`, `style`, `template` and
  `title` - so `<head><title>T</title></head><p>x` ends at the `<p>`.

  Two things it gives up, both stated rather than discovered later: the bytes have to
  be available from the offset, so this holds the document (a stream would keep what
  it read past the stop point, bounded by one write); and nothing in the body is
  parsed, so nothing in it is checked - a body that would have failed a sanitiser
  passes through untouched.

- **`examples/gip/multipart`: rewrite the HTML parts of a multipart body and pass
  everything else through byte for byte.** B188 is why one rewriter per part is
  correct rather than merely convenient.

  Multipart is the join B177 warns about, made safe, and the reason is that the
  delimiter is out of band. A boundary is written by the multipart writer rather than
  by the rewriter, so a part ending in `<div attr="` keeps its truncated tail and the
  next part is untouched - measured for five truncation shapes, against the same two
  fragments concatenated without boundaries, which lose the second one's paragraph.

  A rewriter per part is free until the parts are small. Over 100000 bytes total,
  one rewriter per part against one rewriter for all of it:

      parts   bytes each   a rewriter per part   one rewriter   ratio
          1        99990              5.338ms        5.473ms    0.98x
         10         9990              5.336ms        5.368ms    0.99x
        100          990              5.668ms         5.58ms    1.02x
       1000           90              7.661ms        4.984ms    1.54x

  Free above a kilobyte a part, half again as expensive at ninety bytes, where
  B172's fixed registration cost exceeds the content. There is no reusing a
  `Writer`, so the answer for a body of many tiny parts is fewer selectors rather
  than fewer rewriters.

  And closing a part's rewriter after the next part has begun gives `multipart:
  can't write to finished part` - B186 in a second place, loud, and lost to a
  deferred Close the same way. A part with no `Content-Type` is not treated as HTML,
  because guessing would rewrite whatever happened to look like markup: the test
  includes a `text/plain` part whose content is an anchor.

- **`examples/gip/gunzip`: rewrite a document that arrives gzipped, decompressing as
  it goes.** B187 is the pair of things that go wrong.

  A gzipped body is a size claim the sender controls twice, so the limit has to go
  after the decompressor. Measured, 50991 compressed bytes expanding to 52428800:

      no limit                        52428800 bytes reached the rewriter
      io.LimitReader after gunzip      1048576 bytes, as asked
      io.LimitReader before gunzip    52428800 bytes - the limit was never near

  The last row is the natural mistake, because the thing being limited is called the
  input and the input that arrived was the compressed one.

  And a limit is silent unless asked to speak. `io.LimitReader` ends the stream at
  the limit, which to everything downstream looks exactly like the document ending:
  the copy succeeds, `Close` succeeds, and the output is half a page that nothing
  complains about. Reading one byte past the limit is what separates a short
  document from a truncated one.

  A truncated stream can also deliver every byte. Cut at 90 per cent, the deflate
  data is complete and only the trailer is missing - so the whole document arrived
  and the checksum that would have vouched for it did not. The error is about
  integrity rather than completeness, and it arrives after the bytes have been
  written, which is why the program reports what it wrote alongside it.

  One bug of my own, found by its own test: `parseSize` used `fmt.Sscanf("%d")`,
  which stops at the first character it does not understand and reports success - so
  `-limit 1x` meant one byte and `-limit 1.5m` meant one. It uses `strconv.ParseInt`
  now, and the test rejects nine spellings rather than five.

- **`examples/gip/gzipout`: rewrite into a gzip writer and check the round trip.**
  Two Closers in a chain, and B186 is the reason it is worth an example.

  A rewriter writes during its own `Close` - `OnDocumentEnd` always, and a text
  handler for the last chunk of a text node the document left open - so the order of
  the two `Close` calls decides whether those bytes arrive:

      document and handlers                       rewriter first   gzip first
      a complete document, no append                     34 bytes     34 bytes
      an append at the document end                      71 bytes     34 bytes
      an append, document ends inside a tag              58 bytes     21 bytes
      text held, document ends inside the text            7 bytes      3 bytes

  The first row is why the mistake survives testing: on an ordinary document with
  ordinary handlers there is nothing to lose. When there is, the rewriter's `Close`
  returns `flate: closed writer` - so it is detectable, and `defer` is where the
  report dies. Deferred calls run last in, first out, so the defer written *second*
  runs first, and a defer discards the error that would have said so.

  Two measurements that came out the reassuring way. The write pattern costs nothing
  in compressed size: the same document at chunk sizes 1, 16, 512 and 1 MiB gzipped
  to 1188 bytes each, identical to compressing the finished output in one write - so
  wrapping the compressor in a `bufio.Writer` buys nothing here. And what a rewrite
  costs on the wire is not what it costs in bytes: adding `rel="noopener"` to two
  hundred links added 3000 bytes to the document and **51** to the stream, a 22 per
  cent increase becoming 4.5. A rewrite that adds distinct content per element -
  an id, a nonce - does not compress away, and a test asserts the difference rather
  than the claim.

- **`examples/gip/tee`: stream a document to two destinations at once, one rewritten
  and one exactly as it arrived, from a single read.** The verbatim copy is the
  input, so teeing the reader is the whole implementation. What is worth measuring is
  the gap between the two, because it is the only view from outside of what the
  rewriter is holding.

  B185: it holds one start tag, and which tags depends on the selectors rather than
  on whether a handler ran. Widest gap over a 5513-byte `<div>` with five hundred
  attributes, fed a byte at a time:

      selector                                  widest gap
      none                                               5
      a[href]  span[data-x]  p                           5
      div[data-x]  div[data-absent]  div.absent       5505
      div#absent  [data-absent]  div  *              5505

  A tag name rules a selector out at the name and the rest of the tag streams. An
  attribute, class or id cannot be ruled out until the tag ends, so the tag is held
  whether it matches or not - `div[data-absent]` costs exactly what `div[data-x]`
  does - and a selector with no tag-name component holds every tag. This refines
  B78, which distinguished "a handler registered" from "none": the line is the tag
  name.

  Text is never held - a 10 KB text node ran a gap of three bytes with a text
  handler and without one - and the one way to hold the document is to do it
  yourself, which accumulating a text node to `IsLastInTextNode` does. Telling the
  rewriter's buffer from the caller's is the point of measuring.

  The two destinations also fail differently, and there is no ordering that avoids
  it: whichever is written first is ahead when the other breaks. The program reports
  both counts so a partial pair is recognisable rather than surprising.

- **`examples/gip/etag`: compute an entity tag for a rewritten page without waiting
  for the page.** An etag is a header and the output only exists as it is written, so
  hashing the output means either sending the header late or holding the body.
  Neither is needed: a rewrite is a function, so hashing the input with the
  rewriter's identity names the output without producing it.

  Fastest of twenty runs over a 233 KB page, normalised to a plain rewriting pass:

      approach                              time   relative   etag known
      rewrite only (the baseline)         5.161ms      1.00x   -
      hash the input, sha256, up front    5.296ms      1.02x   before the body
      one pass, buffered, then hash       5.389ms      1.04x   before the body, holding it
      hash the output, fnv64a, streaming  5.623ms      1.09x   after the body
      hash the output, sha256, streaming  5.881ms      1.14x   after the body
      two rewriting passes               10.473ms      2.03x   before the body

  Hashing the input is the cheapest row and the only one that is both up front and
  O(1) memory. The last row is why this is not `examples/gip/cachetags`: there the
  second pass could register no handlers and cost 0.08x, so two passes came to less
  than one. Here the second pass has to do the rewriting, because the rewriting is
  what the etag is about.

  What the design trusts is that the rewrite is deterministic, which is a property
  of the handlers rather than of the library - a map iterated inside a handler or a
  clock read in one loses it. So `-verify` rewrites twice and reports whether the
  outputs agree, the tests do the same at six chunk sizes, and one test builds a
  deliberately non-deterministic handler and asserts the check notices. The etag is
  a cache key rather than a signature, so the hash is fnv64a: it has to change when
  the bytes change and it does not have to resist an adversary choosing them.

- **`examples/gip/cachetags`: collect cache tags from a document into a header
  value, which is a thing a streaming rewriter cannot quite do.** The tags are in
  the body and the header goes in front of it, so no single streaming pass can do
  both. Of the four answers, three are real: buffer the body, parse it twice, or
  send a trailer that few clients read.

  It parses twice, and the reason is measured. Fastest of twenty runs over a 347 KB
  page, normalised to one pass that rewrites the body:

      pass                                  time    relative
      copy only, no handlers               406µs       0.08x
      collect only, no output             3.351ms      0.66x
      rewrite only (the baseline)         5.085ms      1.00x
      two passes: collect then copy       3.773ms      0.75x
      two passes: collect then rewrite    8.463ms      1.67x
      one pass, buffered                  7.285ms      1.44x

  The first row decides it: a pass with no handlers costs eight per cent of a pass
  with them, because with nothing registered the sink hands the destination a slice
  over lol-html's own buffer instead of copying. So where the body passes through
  unchanged - the common case for a tag extractor - collecting and then copying
  costs **less** than a single pass that rewrites, and holds nothing.

  Where the body is rewritten too, it is 1.67x against buffering's 1.44x, and the
  memory column is the one that matters. Live heap after a collection, at the moment
  the header would be set: buffering holds +72 KB, +351 KB and +1.76 MB for pages of
  68 KB, 348 KB and 1.4 MB, and two passes hold nothing at any size. Sixteen per
  cent is what that costs.

  A tag from a document is not a header value: it arrives as source, so it is
  decoded before it is judged, and a carriage return or line feed in it is refused
  rather than stripped. My first version checked *after* splitting the attribute
  into tags, which is a hole with no error in it - `strings.Fields` treats a newline
  as a separator, so `a\nb` became the two clean tags `a` and `b`, the newline never
  reached the check, and the cache would have been keyed on two tags the page never
  asked for. The check comes before the split now, and a value holding a line break
  is refused whole rather than quietly divided. Eleven spellings are tested,
  including `&#10;`, `&#xa;`, `&NewLine;` and `&#010;`.

- **`examples/gip/deployid`: echo a deploy id from the environment into a meta tag,
  and say when it could not put it somewhere a browser will read.** A comment can go
  anywhere; a meta has to be in the head, and a rewriter cannot see the head a
  parser will build.

  It turns out not to need to. B183: inserting a bare meta before the first element
  lands it in the head, because a parser in its "before head" mode meets the meta,
  creates the head, and puts it there. Wrapping it in a `<head>` of your own gives an
  identical tree and is dropped as a duplicate where the source has one, so the
  wrap is wasted work. Measured against x/net/html over eight shapes, including a
  page with no head spelled, `<html>` with no head, `<body>` first and `<title>`
  first.

  Where it fails is text: text ends the head, so a meta inserted after any lands in
  the body and is ignored. The trap is what counts as text. Only tab, line feed,
  form feed, carriage return and space keep the head open; U+00A0, U+2007, U+202F,
  U+3000 and the vertical tab do not - so a template that indents its output with a
  non-breaking space moves every meta tag it adds into the body, invisibly.

  And that is detectable in one pass, because text arrives before the element that
  follows it. The program counts non-whitespace text before the first element,
  inserts the meta anyway - a meta in the body is inert rather than harmful - and
  reports that a browser will ignore it, naming the text that closed the head.
  `-strict` makes it an exit status.

- **`examples/gip/buildinfo`: stamp a page with the commit that produced it, and
  say where the stamp went.** The value is known before the rewrite starts, which
  is the opposite of `examples/gip/servertiming` and makes the top of the document
  available. Available is not guaranteed, and the difference is the program.

  A rewriter reports the elements the source contains, not the ones a tree builder
  adds. The tree always holds `html`, `head` and `body`; the source usually does
  not, so `<!doctype html><p>x</p>` reports one element and a rewrite anchored on
  `head` does nothing at all. `Doctype` cannot help either - it has `Remove` and no
  insertions. So the anchor is the first element of any kind, and the document end
  when there are none: empty, only text, only a comment, or only a doctype.

  The other half is a trade worth stating. At the first element, "is this page
  already stamped" is not knowable - an existing stamp could be anywhere later in
  the document - so `-at=top` is not idempotent, and running it twice gives two
  stamps and a report that says it noticed, too late. At the document end every
  comment has gone past, so `-at=end` is idempotent and puts the stamp where a human
  reading source and a byte-range fetch of the first kilobyte will both miss it.
  Six passes of `-at=end` leave one stamp; two passes of `-at=top` leave two.

  B182 settles the question the top placement raises: a comment between the doctype
  and the first element does not stop the doctype counting. Measured against
  x/net/html, one comment before it, two, a comment after it and whitespace before
  it are all standards mode; no doctype and a legacy doctype are quirks.

- **`examples/gip/servertiming`: time a rewrite and write what it measured into the
  document it rewrote.** A Server-Timing *header* would be better and is not
  available: a header is sent before the body and the duration is known after it, so
  the value goes in a comment at the document end - which is also the only position
  left, since an insertion can only go where the rewriter has not been.

  Two things it measures rather than assumes.

  What timing costs, because the obvious worry is that instrumenting every handler
  call swamps the work. It does not. Two clock reads against a handler call that is
  a crossing into C, fastest of fifty runs on an M3 Pro:

      page              plain      one clock read   a read per handler call
      486 bytes         12.9µs     12.8µs           13.1µs
      4806 bytes         105µs     105.9µs          110.6µs
      49806 bytes      1.030ms     1.017ms          1.032ms

  About six per cent at the middle size and inside the noise at the other two, with
  no extra allocations per call. So per-handler timing is affordable, which matters
  because the alternative - timing the whole rewrite and dividing - cannot tell a
  slow selector from a slow document.

  Affordable where it is possible at all, and CI supplied the counter-example. A
  handler call is under a microsecond and the Windows runner's clock ticks every
  **340µs to 363µs** across runs, so two hundred calls there summed to zero in one
  run and to 1.1 ticks in another. That is not a small number, and the program says
  so: the comment carries the call count and the tick instead of a duration.

  The same tick puts a floor under the whole-rewrite figure too. Twenty ticks of a
  350µs clock is 7ms, a rewrite of a few hundred kilobytes, and below that there is
  nothing to report on that machine either. So there are three states, all
  reachable and each with its own output - neither figure, the rewrite only, or
  both - and a deterministic table asserts all three so that the timed test is
  checking expectations that have themselves been checked. Getting that table wrong
  twice is what this took: the first fix asserted a positive per-call figure, and
  the second conflated "the calls are unresolvable" with "nothing is".

  The tick is worth recording on its own: the project's earlier guess for that
  runner was 15ms, from a different measurement.

  And whether the figure is a figure at all. The clock tick is measured and
  reported, and a rewrite that did not last twenty of them gets a comment saying so
  instead of a number - the lesson `examples/gip/queue` paid for, applied before it
  bites rather than after.

  B181 is the third thing: the comment does not always survive. It is swallowed by
  the same truncated inputs as an element, and a document ending inside a comment
  *merges* with it, so one comment comes out carrying both the page's text and the
  marker. Counting comments says it arrived and so does searching for its text.

- **`examples/gip/esi`: expand Edge Side Includes both ways and report where the
  two disagree.** `examples/gip/include` is the one to copy - it uses
  `WithESITags`, fetches in the handler rather than in the sink, and implements the
  spec's error handling. This one exists to measure what the option is buying,
  because "use the option" is easier to follow when the alternative has a number
  against it.

  Without it, an `esi:` element is an ordinary unclosed container, so its end is
  the enclosing element's end tag. On
  `<div><p>before</p><esi:include src="/f"/><p>after</p></div>`:

      operation                     without the option                     with it
      Replace                       loses <p>after</p> and </div>          correct
      Before then Remove            loses <p>after</p> and </div>          correct
      SetInnerContent               keeps the marker, loses <p>after</p>   correct
      RemoveAndKeepContent          loses </div>                           correct
      Before then RemoveAndKeep     loses </div>                           correct
      Before alone                  correct, and the marker stays          the marker stays

  So there is one lossless way to do it without the option and it cannot remove the
  marker. That matters less than it sounds and in a way worth measuring: the source
  tree already nests what follows the include *inside* it, so `div > p` matches one
  paragraph without the option and two with it - and `Before` alone preserves that
  tree exactly, fragment added, nothing moved.

  The trap is the operation that looks like the fix. `Before` plus
  `RemoveAndKeepContent` produces exactly the option's tree on a document that ends
  soon after the include, and on one that does not it consumes the `</div>` and
  makes a following `<section>` a child of the div instead of its sibling. A rewrite
  tested on the first shape passes and then moves half the page on the second.
  B180, with the trees in `differential/esi_test.go`.

- **`examples/gip/placeholders`: resolve handlebars-style `{{ name }}` placeholders,
  choosing the escape by position and refusing the positions where no escape is
  enough.** "Escaping properly" is five different jobs:

      position               what it needs                       what this does
      text content           HTML text escaping                  ContentType Text
      attribute value        attribute-value escaping            EscapeAttribute
      title, textarea        HTML text escaping, because          ContentType Text
                             references are decoded there
      comment                no "-->" in the value               refused, and reported
      script, style          escaping for JavaScript or CSS      refused
      iframe, noembed,       nothing works: no references         refused
      noframes, noscript,    decoded and no inner language
      xmp, plaintext

  The script row is the one that looks wrong and is not, per B16: inserting as
  `Text` is safe - the value cannot become an element - and corrupt, because
  references are not decoded there, so the JavaScript ends up holding `&lt;` where
  a `<` was meant. Safe and corrupt is not resolved.

  Two positions are gone before the program sees them, and both are reported
  because a rewrite that says nothing has processed a different document from the
  one it was given. `<div {{ attr }}="1">` is three attributes - `{{`, `attr`, and
  `}}="1"` - so the name is not recoverable; written without spaces it is one
  attribute and still not rewritable. And `<{{ tag }}>x</{{ tag }}>` is not an
  element at all: the opening half is text, the closing half is a bogus comment,
  and nothing inside nests. Measured: zero elements, one comment.

  Ten tests. Four are security properties and each was checked by removing the
  protection and watching the test fail - which caught one that was vacuous: the
  attribute escape is not an injection guard, since `SetAttribute` rewrites the
  double quote on the way out, so an unescaped value cannot end the attribute
  either. What it prevents is corruption, and the test asserts the round trip
  instead. The URL scheme is checked on the decoded value against twelve payloads,
  including `&#106;avascript:`, `jav&#x09;ascript:` and `&Tab;javascript:`.

- **`examples/gip/bindings`: turn framework attribute syntax into plain HTML
  attributes where it can, and say why it cannot everywhere else.** Only a literal
  can become a plain attribute - a quoted string, a number, `true` or `false` -
  because an expression needs a runtime and a program that guessed would produce a
  page that looks right and says something else. Everything else is reported with
  a reason: an event handler has no plain form, a structural directive decides
  whether the element exists, two-way binding has no plain form, and `v-html`
  writes markup rather than an attribute.

  It reads `Attribute.NamePreserveCase` for everything it prints, per B179, and
  only ever writes names that are lower-case already - which is the only reason it
  is safe to write them at all.

  Two things it demonstrates rather than merely uses. A binding's value is
  attribute-value source, so `:title="'a &amp; b'"` becomes `title="a &amp; b"`
  and not `title="a &amp;amp; b"`: the test asserts the decoded value is unchanged
  across five escapes. And every framework name needs escaping to appear in a
  selector - `[\:href]`, `[\@click]`, `[\*ngIf]`, `[\(click\)]`,
  `[\[\(ngModel\)\]]` - with the unescaped form rejected rather than silently
  matching nothing, which is why the program reads the attribute list instead of
  registering one selector per prefix.

- **`examples/gip/widgets`: turn legacy widget markup into web component markup.**
  A container becomes a custom element, the state it kept in classes and data
  attributes becomes properties, and the parts it kept in nested divs become slots.

  Both of its refusals are measurements. A target that cannot hold content is
  rejected, because B178 - renaming a container to `br` gives two elements, to
  `col` gives none, to `meta` moves the element to the head, and the output markup
  looks reasonable in every case. A hyphenated name is always an ordinary
  container, which is what makes a custom-element target safe by construction, and
  the program says so rather than relying on it.

  And a widget whose end tag the source omitted is skipped rather than renamed,
  because a rename writes over the token that closed the element and that token
  belongs to something enclosing: renaming the items of `<ul><li>a<li>b<li>c</ul>`
  yields `<ul><my-item>a<my-item>b<my-item>c</my-item>` with the `</ul>` gone, and
  the outermost rename is the one that wins. Whether an element closed itself is
  knowable from `EndTag.Name`, and knowable too late, so this is two passes: the
  first records which candidates closed themselves and the second renames only
  those. A mixed list gets the two that spelled their end tags and reports the one
  that did not.

  Nine tests, two of which measure what the refusals prevent rather than asserting
  that they are worth having. Idempotence comes for free: a renamed element no
  longer matches the selector that found it.

- **`examples/gip/inventory`: list the custom elements a page uses and say which
  nothing defines.** Definitions come from `customElements.define("name"` in the
  document's own scripts - accumulated to `IsLastInTextNode`, because script text
  arrives in chunks and a define call can straddle two of them - or from
  `-defined` for what a bundle registers. It exits non-zero when anything is
  undefined.

  Getting the classification right is most of the work, and the specification's
  rule is narrower than "has a hyphen" in both directions. Eight hyphenated names
  are reserved and are not custom elements: `annotation-xml`, `color-profile`,
  `font-face`, `font-face-src`, `font-face-uri`, `font-face-format`,
  `font-face-name` and `missing-glyph`. And the other direction is the one that
  costs an afternoon, because HTML lower-cases a tag name:

      source            TagName      a custom element?
      <my-card>         my-card      yes
      <MY-CARD>         my-card      yes, the same one
      <myCard>          mycard       no - no hyphen once lower-cased
      <my_card>         my_card      no - an underscore is not a hyphen

  So the inventory keeps both names per element: `TagName` is what a definition
  has to match, and `TagNamePreserveCase` is what tells the reader why their
  component never upgraded. A name with neither a hyphen nor capitals is not
  reported at all, since it cannot be told from a typo of a built-in.
  `<div is="my-div">` counts as a use, and `is=` on an element that is already
  custom is ignored, as the specification says.

  Ten tests, including the eight reserved names, the three spellings that are one
  component, definitions found at every chunk size from one byte up, and the
  fragment trap in B177.

- **`examples/gip/shadow`: give every custom element a declarative shadow root,
  and give it exactly once.** A page can go through twice without gaining two,
  which is the property the whole design is for.

  Both design decisions are measurements rather than preferences. The insertion
  goes at the host's end tag, not its start tag, because a host that already has a
  shadow root must be left alone and whether it has one is only known once its
  children have gone past - by which time the start tag is behind the rewriter.
  The detecting selector is `my-card > template[shadowrootmode]` rather than
  `template[shadowrootmode]`, because a declarative shadow root is a child of its
  host and a template deeper inside is an ordinary template: on
  `<my-card><div><template shadowrootmode="open">…</template></div></my-card>` the
  first matches 0 and the second matches 1.

  The second reason for the end tag is B176. And what the program cannot do is a
  host whose end tag never arrives: `<my-card/>` is the case that costs someone an
  afternoon, since HTML ignores the slash on an element that is neither void nor
  foreign, so the host opens and runs to the end of the document while
  `IsSelfClosing` reports true and `CanHaveContent` also reports true. Neither is
  a test for it, so the report counts hosts that never closed rather than
  pretending they were done.

  Nine tests. `TestTwiceIsTheSameAsOnce` is the property, over documents one to
  four hosts deep and one to three wide - inserting at the start tag instead fails
  it. `TestAppendAndEndTagBeforeDoNotAgreeWhenTheEndTagIsOmitted` is the
  measurement behind B176, with the closed-end-tag case asserted alongside so the
  difference is shown to be about the omission and nothing else.

- **`examples/gip/islands`: annotate the interactive regions of a page for
  partial hydration - which ones there are, which are inside which, and what each
  needs.** Three attributes per island and a manifest a bundler can read.

  The question it most needs to ask has no selector. In CSS "an island not inside
  another island" is `[data-island]:not([data-island] [data-island])`, and that is
  rejected. Measured, the whole boundary:

      :not(div)  :not(.a)  :not(#i)  :not([a])  :not([a=v])  :not(*)   accepted
      :not(div.a)  :not(div, span)                                     accepted
      :not(:first-child)  :not(:nth-child(2))  :not(:not(div))         accepted
      :not(div p)  :not(div > p)  :not(div + p)  :not(div ~ p)         rejected

  A combinator is rejected inside `:not()` and nowhere else. The documented rule -
  a selector works if the rewriter can decide it at the start tag - predicts
  `:not(div p)` works, because the plain descendant selector `div p` decides
  exactly that question, at the start tag, and it does work. The error compounds
  it by naming the pseudo-class rather than the combinator inside it, and
  following that with advice about escaping colons in tag names. The selector
  documentation now says all of this, and `TestACombinatorInsideNotIsRejected`
  pins both the rejection and the message so upstream changing either fails here.

  So nesting comes from a stack, which has two traps of its own that this program
  exists to demonstrate. A void element marked as an island - `<img
  data-island="Hero">`, a reasonable thing for a template to emit - would fail the
  whole rewrite, because `OnEndTag` on an element with no end tag returns an error
  that fails the run rather than the handler; every push checks `CanHaveContent`
  first. And an island whose own end tag is omitted swallows what follows it: in
  `<ul><li data-island="A">x<li data-island="B">y</ul>` the handlers run start A,
  start B, end B, end A, so both the stack and the descendant selector make B a
  child of A where the HTML tree has them as siblings. No answer is available to a
  streaming rewriter, so the program says when the question arose - it compares
  `EndTag.Name` against the element's own tag and marks every island closed by
  someone else's end tag.

  Twelve tests. The nesting is cross-checked against the descendant selector over
  generated documents from one to five deep and one to three wide - and the test
  is explicit that the two methods share the omitted-end-tag behaviour rather than
  being independent of it. Props are decoded for the manifest and left exactly as
  written in the document, over six escapes including `caf&eacute;`. The
  annotation is identical at read sizes from one byte to four kilobytes, with a
  reader that fails the test if the size did nothing.

- **`examples/gip/transitions`: give view-transition names to the elements that
  appear on both of two pages.** A transition needs the same name on the same thing
  in both documents, and "the same thing" is the problem: two pages are two
  documents, and nothing in either says which element corresponds to which.

  A source offset cannot be the identity, which is worth stating because the
  library's offsets are so nearly right for it. They are absolute and stable, which
  is what makes them an identity *within* a document - `examples/gip/article` leans
  on exactly that - and meaningless between two, where the same header is at byte 210
  in one page and 1804 in the other.

  So the identity comes from the content: the chain of open elements, each with its
  tag, its id or first class, and its position among siblings of the same shape.
  Computable while streaming, because it only ever needs what is already open. Only
  the *first* class is part of the shape, so a page that adds `is-active` between
  versions has not changed the element.

  The path is a fact about the source rather than about the tree - a rewriter reports
  the elements the document contains, not the ones a tree builder would add, so a
  fragment beginning with `<body>` has a path starting at `body` and a full page has
  one starting at `html`. That is what makes the comparison work between pages from
  the same templates, and it is the limit too: a page that gains a wrapping `<div>`
  has different paths for everything inside it, and this cannot tell that from a page
  that replaced its content. The report says how many paths matched rather than
  claiming they are the right ones.

  Two things it has to be careful about, both lessons from earlier turns. The
  declaration is *prepended* to any existing style attribute, because the cascade
  takes the last declaration for a property and an element's own style should keep
  winning. And a page that already named an element is left alone, since it knows
  something this program does not.

  The name has to be a CSS custom-ident: it cannot begin with a digit and cannot be
  `none` or another CSS-wide keyword, so a class of `3-col` becomes `vt-3-col` and
  the keywords are prefixed - and every name is made unique within the document,
  because two elements sharing one animate as a single thing.

- **`examples/gip/split`: cut a document into parts at a heading level, and make
  each part stand on its own.** A rewriter writes to one destination and a split
  needs several, so the destination is a writer that forwards to whichever part is
  current and the handler that meets a heading tells it to move on. From the
  rewriter's side this is one document, which is what keeps the parse right.

  What makes a part standalone is the tags that were open when the cut happened. A
  heading three levels inside `<html><body><article>` starts a part whose text is
  inside nothing at all unless those three are written again - so each part begins
  with the ancestors' start tags, attributes included, and ends with their end tags
  in reverse. An element handler is all that takes: the tag and its attributes at the
  start tag, the pop at the end tag, no tree and no second pass.

  Three things it got wrong first, all of them lessons the library documents.

  The tag-balance test read `e.TagName()` inside the end-tag callback and got an
  empty string every time, so it reported every part as unbalanced. The element is
  detached by then - which the documentation says, under user data - and the name has
  to be captured before the handler is registered. The library was right and the
  test was wrong, which took a debugging pass to establish.

  And "does this part have anything in it" cannot be answered by counting bytes. The
  wrapper's own start tags are written to a part before the first heading arrives, so
  a byte count says a part holding `<body>` and nothing else has content, and a
  document beginning with a heading gets an empty first part. It is answered by
  whether text or a completed element arrived instead.

  And the reopened tag was double-escaped. An attribute value from `AttributeList` is
  raw source, so `class="a&amp;b"` is seven characters and writing them back
  unchanged round-trips - while escaping them properly, which is what the first
  version did, produces `class="a&amp;amp;b"` and a part whose class is not the class
  it had. The rule is `SetAttribute`'s: escape only the double quote, because only it
  would end the attribute. That leaves one thing a reopened tag cannot preserve - a
  value written in single quotes can hold a literal double quote, and there is no way
  to write that inside double quotes - so the tag preserves the attribute *values*
  rather than their spelling, which is what a parser and a stylesheet see.

  The byte budget moves the cut to the next heading rather than cutting mid-element,
  because a part that ends inside a tag is not a part - and it is counted at the
  destination rather than at the input, since the output is what a caller is sizing
  and the two differ whenever a handler edits anything.

  Its property is that no text is lost or duplicated: every character of the
  document's text is in exactly one part, in order, over four documents including one
  with no headings and one that begins with a heading nested in a div.

- **`examples/gip/idmerge`: concatenate documents and keep every id unique,
  rewriting the references as well as the ids.** It is two passes per document and
  not by choice: a table of contents at the top of a page points at headings further
  down, so a rename has to be known before the first reference arrives and cannot
  be. That is the ordering constraint in the case where no better position exists -
  the evidence is genuinely later than the first place it is needed.

  The part a program usually gets wrong is what counts as a reference. Nine
  attributes name an id, five of them as space-separated lists, and a document whose
  `aria-labelledby="intro summary"` is left behind by a rename is a document whose
  labels point at nothing. So the list values are rebuilt entry by entry rather than
  replaced, and each entry is looked up on its own.

  Three smaller decisions, each the library's rather than the domain's. An id is
  compared *decoded*, because `id="a&amp;b"` and `id="a&b"` are the same id to a
  parser and a document can spell one either way - and the value is written back raw,
  which is the same rule as everywhere else. A `<map>` is matched by its name rather
  than by an id, so names share the namespace for this purpose. And a reference to an
  id that no document defines is reported rather than rewritten, because inventing a
  target is worse than saying so.

  It also declines the thing the line asked for most directly: it does not feed
  several documents into one rewriter. Two documents written into one Writer are one
  document to the parser - the second one's `<html>` lands inside the first one's
  body - so each is rewritten alone and the outputs are joined, wrapped in a section
  that says where each came from.

  Its read-size test was vacuous first time round: the helper it called ignored the
  size and re-ran the whole-document path, so it compared a thing with itself. The
  rewrite pass takes an `io.Reader` now, the test feeds it a reader that hands out a
  few bytes at a time, and it counts the reads - so a rewrite that stopped streaming
  would fail rather than pass.

- **`examples/gip/formschema`: read every form on a page and print what it would
  take to submit it.** The point is replay - the hidden fields, the pre-selected
  options, the checkbox that submits nothing until it is checked - which is what
  makes it different from listing the inputs.

  Four of its decisions are the library's rather than the domain's, and they pull in
  different directions inside one program:

  A `<textarea>`'s value is its *text*, and a textarea is a raw-text element, so the
  value arrives in chunks with no markup in them and has to be accumulated to
  `IsLastInTextNode`. A per-chunk read gets a prefix and looks like it worked; the
  test feeds a 520-byte value at five read sizes.

  A `<select>`'s value is a nested element's attribute, so the field is not complete
  until the select's end tag - while an `<input>` is void, has no end tag, and is
  complete at its start tag. The same program records one at each.

  A duplicate attribute is read through `Attribute`, which acts on the first copy -
  the one a parser keeps and so the one a browser submits - rather than through the
  iterator, which yields both.

  And values are *decoded* while the action is not, which is the difference between
  a thing that gets submitted and a thing that gets requested: `value="a&amp;b"`
  submits `a&b`, and `action="/s?a=1&amp;b=2"` requests the URL as written. The
  library's caveat applies to the first - `html.UnescapeString` decodes more of an
  attribute value than a parser does, so `?a=1&copy=2` gains a copyright sign here
  and keeps its parameter in a browser - and the report says when a value could have
  been affected rather than pretending it could not.

  Two things it declines to do. A field that names its form with a `form` attribute
  is reported separately, because resolving it means knowing about a form that may
  not have arrived - the ordering constraint, stated as a note rather than guessed
  at. And strict parsing is off, because a raw-text element inside a `<select>` makes
  it refuse the document; the report says when that shape was seen, since the content
  inside such an element is text to a parser and any fields in it are invisible.

- **`examples/gip/article`: find a page's article body by scoring elements as it
  streams past them, then emit that element and nothing else.** Two passes, and not
  by choice: the winner is not known until the document ends, and a rewrite cannot
  write to a position it has already passed.

  What makes the second pass cheap is the thing the library documents about source
  locations - the offsets are absolute and unaffected by how the document was
  written in, so the byte range the first pass measured names the element in the
  second whatever its read sizes are. Nothing is buffered but the scores, and the
  test that pins it asserts the output equals the input sliced at those offsets.

  Its own property found the bug worth reporting. The text count was taken per
  chunk, and a chunk boundary falls wherever the writes and the tokenizer put it:
  the same page scored 128 characters read one byte at a time and 155 read whole,
  because every chunk lost its own leading and trailing space to the trim. The count
  is taken over the accumulated node now, at `IsLastInTextNode`, which is the
  discipline the documentation gives for anything that measures text - and the
  property that failed is the one that says the scores do not depend on the read
  size.

  Three smaller things the library decided rather than the heuristic. A text chunk
  does not know what element it is in, so the count is kept per open container and
  added to all of them, which is what makes a container outscore its children. A
  text chunk cannot tell that it is inside a link either, so the depth of open
  anchors is counted - there is no selector for "not inside an `<a>`". And an
  element's score is complete only at its end tag, so a container whose end tag
  never arrives is reported as skipped rather than scored on partial evidence.

  One test states a limitation rather than a feature: the outermost container wins
  on text it merely contains, which is why a real extractor excludes `<body>`, and
  the test says so instead of asserting a preference the heuristic does not have.

- **`examples/gip/comments`: a comment renderer, and three bugs its own tests
  found.** It sanitises untrusted comment HTML to a short allow-list and turns bare
  URLs into links, which is the one job in the collection that has to build markup
  out of text an attacker chose.

  The bugs, in the order the tests found them:

  *Double escaping.* A text node's contents arrive as source, so `a &amp; b` is
  those nine characters. Writing them back with `lolhtml.Text` escapes them again
  and produces `a &amp;amp; b`. The rule the library gives is the same one it gives
  for attributes - decide on the decoded form, write back the raw one - so the text
  goes back with `HTML`, and nothing in the linkifier escapes anything. What makes
  that safe is what a text node is: bytes the tokenizer decided are not markup, so a
  `<` inside one could not begin a tag and re-emitting it in place leaves it text.

  *A link with no policy.* The `rel` and `target` attributes were set by the element
  handler, which never saw the anchors the linkifier produced - a selector does not
  match markup the same pass inserted, deliberately, because that is what stops a
  rewrite triggering itself. So the first pass produced bare links and a second pass
  added the policy: the idempotence test caught the disagreement. The linkifier
  writes them itself now, and the handler still applies them to the commenter's own
  anchors, which makes the policy the renderer's rather than the commenter's.

  *A test measuring the wrong thing.* A comment saying `" onmouseover="alert(1)`
  comes out as those characters as *text*, which is correct, and a substring check
  called it a failure. The check re-reads the output with a rewriter and asks whether
  any attribute begins with "on" - which is the difference between looking at bytes
  and asking a parser.

  The rest is the discipline the docs give, applied: text accumulated to
  `IsLastInTextNode` because a URL can straddle chunks and a per-chunk linkifier
  finds nothing in `https://exa` or `mple.com/x`; a depth counter for open anchors,
  since there is no selector for "not inside a link"; `RemoveAndKeepContent` for a
  disallowed element that holds prose and `Remove` for one that holds code, which
  `IsRawText` is how to tell apart; and an href kept only when its *decoded* scheme
  is http, https or mailto.

- **The text-insertion property was stated over documents that excluded every
  hazardous context.** `properties/text_structure_test.go` says inserting with
  `lolhtml.Text` never changes a document's tags, "for any value, at any position,
  in any document", and the documents it drew were built from nine ordinary
  elements: div, p, span, a, b, i, ul, li, section. Raw text, escapable raw text,
  foreign content, templates, tables and selects - every place a parser's rules
  change, which is the whole of what anybody would doubt - were outside the space
  the property was checked on.

  `properties/hostileinsert_test.go` states the same property over fourteen of
  those contexts: `<script>`, `<style>`, `<title>`, `<textarea>`, `<xmp>`,
  `<plaintext>`, `<svg>`, a `<foreignObject>`, `<math>`, `<template>`, a table, a
  table cell, a `<select>` and a paragraph, with the value inserted at all four
  positions that take a content type and drawn from the terminators and
  markup-shaped strings most likely to escape. It holds: Text never changed the
  tags and was never refused.

  Two tests keep that honest. The converse - the same insertions as `HTML` - changed
  the tags 172 times and was refused twice, so the escaping is doing the work rather
  than the positions being harmless. And each context is checked to have exactly one
  target the selector matches, with its raw-text flag compared against
  `lolhtml.IsRawText`, so a typo cannot quietly drop a context from the property.

  Writing it also corrected two things I had wrong. The breakout payload has to end
  *that* element - `</script>` does nothing inside a `<style>`, so the guard rightly
  accepted it and the test was wrong, not the library. And `<plaintext>` is raw text
  by `IsRawText` and has no end tag at all, so nothing can break out of it and the
  breakout test skips it rather than expecting a refusal.

- **`properties/sanitiser_test.go`: an allow-list sanitiser as a property, over
  documents built to break it.** Every other property in that package states
  something about one call. This one states something about a composition -
  selectors, removal, attribute iteration and decoding used together, which is what
  a sanitiser is - and the generator produces what a sanitiser meets: `script`,
  `iframe`, `svg`, `math`, `image`, `base`, event handlers, raw-text elements,
  unclosed tags, duplicate attributes, and every spelling of a `javascript:` URL
  that a browser decodes.

  Three properties: nothing outside the allow-list survives, sanitising is
  idempotent, and the result does not depend on the write size. The first one's
  checker deliberately does not call the code under test - a checker that shares a
  decoder with the sanitiser agrees with it by construction, including when both are
  wrong - so it decodes the value itself and looks for the schemes that execute.

  The generator was wrong first, and wrong in the way that makes a security property
  decorative rather than false: it wrote attribute values through
  `html.EscapeString`, which turns `&#106;avascript:` into `&amp;#106;avascript:` -
  literal characters that no browser executes. The vectors never reached the
  sanitiser in their dangerous form, and the property passed against a sanitiser
  with the hole deliberately put back. Only the quote that would end the attribute is
  escaped now, and with the hole put back the property fails in eight cases and
  shrinks to `<a href="/one" href="&#106;avascript:alert(1)">x</a>` - which is also a
  reminder that `AttributeList` sees a duplicate attribute that a selector does not
  (B57).

  A fourth test keeps the other side honest: it asserts that a quarter of the
  generated documents need sanitising at all, so a generator that drifted into
  producing harmless input would fail rather than pass quietly.

- **`examples/gip/emailstrip`** removes what a mail client would reject and says
  what it removed and why, from an allow-list rather than a block-list - a
  block-list is a list of the things somebody thought of, and mail clients reject
  more than anybody has thought of.

  Its first version had a hole, and the hole is the most useful thing in the file.
  It checked a URL's scheme against the raw attribute value, so every encoded
  spelling of `javascript:` walked past it:

      javascript:alert(1)                rejected
      &#106;avascript:alert(1)           accepted
      &#x6a;avascript:alert(1)           accepted
      &#0000106;avascript:alert(1)       accepted
      jav&#x09;ascript:alert(1)          accepted
      &Tab;javascript:alert(1)           accepted

  A browser decodes before it acts; a check on the raw string sees a scheme called
  `&#106;avascript` and lets it through. The library documents exactly this, with
  three of those vectors and the rule to follow - decide on the decoded form,
  rewrite the raw one - which is what the program does now: `html.UnescapeString`
  before the scheme check, and the value written back untouched, so
  `https://example.com/?a=1&amp;b=2` keeps its entity. All eleven vectors are a
  test.

  The documented caveat cuts the right way here: `html.UnescapeString` decodes more
  of an attribute value than a browser does, which for a filter can only reject a
  URL a browser would have accepted. For a rewrite it would be the wrong direction,
  and nothing in the program writes a decoded value back.

  The other thing worth reading is the last line of its report. Removing an element
  does not stop handlers running for what was inside it, so a report that counts
  those says it removed eleven attributes when it removed one `<script>`.
  `Element.IsRemoved` answers for an ancestor, so on `<form action="/x"><p
  class="inside" id="y">text</p><b>bold</b></form>` the report says one removal and
  two things inside it, not four. Text is the exception and the library says so:
  `TextChunk.IsRemoved` answers for the chunk and not its ancestors, so the
  visible-text collection is driven by a removed-depth counter an element handler
  maintains, and the test that would fail without it is in the file.

  Its properties: everything that survives is on the allow-list, checked by
  re-reading the output with a rewriter of its own; stripping twice strips nothing;
  the output does not depend on the read size; and a note appended at the document
  end can never become markup, over seven notes including one that closes the
  document and opens a script. Conditional comments survive and ordinary ones do
  not, and the doctype is left alone, because replacing it would change the
  document's mode and that decides where a table wrapper lands - B174.

- **`differential/tablewrap_test.go`** has the matrix: five wrappers against two
  document modes, with x/net/html saying where each landed, plus a legacy doctype
  behaving like none, plus the five contexts where a wrapper lands exactly where it
  was put, plus the half that holds either way - the content is inside the wrapper
  even when the wrapper is not where it was meant to be. One assertion is about the
  table itself: that exactly one of the five wrappers is mode-dependent, so the
  test fails if another one starts to be.

- **`examples/gip/tablelayout`** converts a div-based page into the table markup an
  email client renders, and refuses the conversions whose result would depend on the
  doctype: a row inside a paragraph is left as it is and counted, and the report says
  the document's mode so a reader can tell whether the refusal mattered. Empty cells
  get a non-breaking space, as the character via `lolhtml.Text` rather than as
  `&nbsp;`, since the escaping is the library's job. Its properties are that the
  output does not depend on the read size, over six sizes from one byte, and that
  converting twice converts nothing the second time.

- **`examples/gip/email`** prepares a page for a mail client: it inlines the
  stylesheet, absolutises the URLs, and removes what a client would refuse to run.
  It is the clearest case in the collection of a rewriter being most of a tool and
  not all of it - selector matching is most of what inlining CSS needs, and
  specificity, cascade and inheritance are the rest, of which it implements the
  cascade and says plainly that it does not implement specificity.

  Which rules it can inline is decided by the library rather than by the program: it
  builds a rewriter per rule and keeps the ones the selector engine accepts, so its
  list cannot drift from `selector_test.go`'s. A refused rule is reported with the
  library's own reason - `a:hover` for a pseudo-class, `.row + .row` for a
  combinator - because a newsletter whose hover styles quietly vanished is
  somebody's afternoon.

  Three things about the shape are worth reading if you are writing something like
  it. The stylesheet arrives as the text of a `<style>` element, so nothing before
  it can be styled - which is why this works on templates, where the sheet is in the
  head, and the program counts and reports the elements that came first rather than
  losing them silently. Whether to strip the `<style>` blocks afterwards is a real
  choice rather than a default: keeping them means the rules that could not be
  inlined still work in the clients that honour a style block. And the declarations
  are *prepended* to an element's style attribute, in reverse registration order,
  because that is the cascade - appending, which is the obvious thing, makes an
  earlier rule beat a later one and the sheet beat the element's own style.

  Two of its tests are properties rather than cases, and both earned their place.
  Inlining twice is inlining once - which failed first time round, because appending
  declarations without checking turned `style="color:red;"` into
  `style="color:red; color:red;"` on a second pass. And a footer can never become
  markup, over eight footers including `</body></html><script>alert(1)</script>`,
  which is what `lolhtml.Text` is for: the document's element list is identical
  whatever the footer says. The third property is that the output does not depend on
  how the input was chunked, over six read sizes from one byte up.

  Its own CSS parser had the bug that shallow CSS parsers have: it took the closing
  brace of the rule inside `@media` as the end of the at-rule and produced a rule
  whose selector was `}`. Braces are counted now, and the test that would have
  caught it is in the file.

- **`examples/gip/middleware`** rewrites a handler's HTML on the way out without
  giving up streaming, and measures what the alternative costs. The same handler,
  writing five chunks forty milliseconds apart:

      streaming middleware   first byte after 411µs, last after 209ms
      buffering middleware   first byte after 210ms, last after 210ms

  Both produce the same bytes. Three things break the first column, and the example
  is mostly about them: buffering the response and rewriting at the end, which is
  the shape everyone writes first; a wrapped `ResponseWriter` that does not
  implement `Flush`, which leaves the handler's own flushes stranded and lets Go's
  chunked writer hold up to 4 KB; and deleting `Content-Length` too late, since
  after `WriteHeader` the header map has already gone.

  It also keeps the buffering middleware, rather than only naming it as the mistake,
  because it is the right answer for a rewrite that has to know something about the
  whole document before it can write anything - and it has one advantage worth
  stating: with nothing sent yet, a failed rewrite can still become a 502.

  Its tests assert the ordering rather than the timings: a signalling
  `ResponseWriter` holds the handler mid-page and checks that bytes have already
  reached the client, and the same test against the buffering middleware checks that
  they have not. Also gated: `Content-Length` deleted, a non-HTML response untouched
  headers and all, the handler's own `Flush` arriving through the wrapper, the status
  code surviving so a 404's body is still rewritten, a handler that writes nothing
  being fine, and the tail of a document that ends inside a tag arriving - which is
  what closing the rewriter after the handler returns is for.

- **`lossytext_test.go`** gates the table above - six bodies against both handler
  sets, with the gzip case checked for still being decodable rather than merely for
  its length - and the encoding labels: `utf-8`, `windows-1252`, `iso-8859-1` and
  `shift_jis` accepted, `utf-16le`, `utf-16be`, `utf-7` and nonsense refused with an
  `EncodingError`.

- **`examples/gip/proxy`** is a reverse proxy that rewrites HTML bodies and skips
  what it must not touch: it asks upstream for `identity`, deletes
  `Content-Length`, takes the encoding from the charset parameter and falls back to
  passing the body through when the library refuses the label, flushes so the
  rewrite streams, forwards `Unwrap` so a websocket upgrade still finds `Hijack`,
  and logs which of those decided each response. Its tests run a real `httptest`
  origin through it: HTML rewritten, gzip passed through and still decodable, JSON
  and CSS and a PNG untouched, a UTF-16 page untouched, and a windows-1252 page
  rewritten with its accented byte intact.

- **`fixedcost_test.go`** gates the shape of the table: the cost grows with the
  rule set and not with a document that matches nothing, fifty selectors is
  hundreds of allocations before a byte is written, and a Writer refuses a second
  document once closed - which is why the cost is per item.

- **`examples/gip/queue`** runs a rewriter per goroutine over a queue and reports
  three things: that no worker's output is another's - every document carries its
  own number and every output is checked against that document rewritten alone -
  how much of the per-item work went on construction rather than rewriting, and
  with `-scan`, the throughput at several worker counts so a caller can find the
  knee on their own machine rather than assume it is the core count.

- **`reserialise_test.go`** gates both tables: fifteen operations against whether
  they reformat the tag, twelve source spellings against what survives, the tag
  name's case, the self-closing slash, and a document that comes back smaller than
  it went in.

- **`examples/gip/twoways`** runs two rewriters over the same input at once - one
  transforming and streaming, one auditing and reporting - and checks that the
  concurrent answer is the sequential answer. It is also how the re-serialisation
  above turned up: the transform reported fewer bytes out than in, which is not
  something an attribute set should do, so the program now says why.

- **`concurrentisolation_test.go`** pins three claims that were made and only
  measured sequentially. The same input bytes can be read by two rewriters at once,
  because `Write` reads the slice and does not keep it. User data and end-tag
  registrations are per-Writer: eight goroutines, 400 reads, no cross-talk and no
  handles left over, which matters because the handle table they use is
  process-wide. And a panic in one Writer's handler leaves a Writer that is
  *actually* mid-document in another goroutine untouched - the survivors hold at
  half a document until the panic has happened, so the two overlap rather than
  merely both occurring.

- **`pipeline_test.go`** gates the composition: two stages annotate what the first
  produced where one pass does not, the peak heap does not grow with the input,
  piping costs what buffering costs and produces identical bytes, closing upstream
  first is the order and the wrong one reports `ErrClosed`, an error in any of three
  stages reaches the caller, and the downstream stage sees elements after the first
  write rather than at `Close`.

- **`examples/gip/pipeline`** runs a document through two stages and prints the
  one-pass result beside the piped one, so the difference is visible rather than
  described; `-wrong-order` closes the stages backwards and shows what that costs.

- **`sinkwrites_test.go`** counts how the output was divided rather than what it
  said: a passthrough is one write, a selector matching nothing is one, a handler
  that does nothing is two per match, reading an attribute is the same, editing
  multiplies it and most of those writes are four bytes or fewer, the count is per
  match rather than per byte, a buffer collapses it by two orders of magnitude
  without changing a byte, and a document whose every element is removed costs no
  writes at all.

- **`examples/gip/backpressure`** measures the same thing against a destination
  with a latency, since that is where the write count becomes a number a caller
  feels: it prints writes, writes per match, median write size, and elapsed time
  with and without a `bufio.Writer` in front, for eight rewrites from a passthrough
  to an attribute set.

- **`sinkfailure_test.go`** gates the five facts above: every handler stops, the
  document-end handler does not run, the destination's own error is findable from
  `Write` and from `Close` under `ErrPoisoned`, the stopping point does not move
  with the write sizes, a destination with no budget still sees one handler run,
  and `Close` fails exactly for the documents where `Close` writes.

- **`examples/gip/clientgone`** rewrites a page to a destination that stops
  accepting partway, which is what a browser closing a connection looks like from
  inside a handler. It prints what the client got, what the counters had reached,
  and that the summary handler never ran; `-closes` prints the Close table above
  from a live measurement rather than from a comment.

- **`earlystop_test.go` gates the prefix guarantee** across the four handler kinds
  that can stop and four write sizes, together with the error identity through
  `Write`, through the refused writes after it, and through `Close`; that the
  stopping position is write-invariant for element, end-tag and comment handlers
  and is not for text chunks; that counting text *nodes* restores it; that
  stopping by not writing is not an error; and that no handles survive any of it.

- **`examples/gip/stopwhen` rewrites a stream that never ends** and stops on a
  condition, either way, reporting what the sink holds and checking - rather than
  asserting - that it is a rewrite of a prefix. It exits non-zero if that fails.

  Its own generator was wrong first: it wrote the same first-n-bytes chunk over and
  over, so at a write size that is not a multiple of the repeating unit the
  "stream" was a sequence of fragments rather than a document. The prefix check
  caught it, which is the argument for having the program check something instead
  of printing a table.

- **`userdatacost_test.go` counts handles rather than bytes**, since a retained
  value is exactly one handle on every machine and a peak-memory figure is a sample
  of a moving number. Seven patterns over 2000 elements: reading holds the two
  handles a registration costs, editing holds those two, user data holds one per
  element, replacing it holds one not two, clearing it to nil holds none, an
  end-tag registration holds one per element, and clearing that one still holds it.
  Everything is released by `Close` in all seven.

- **A timing assertion in `examples/gip/bytewise` was flaky, and is gone.** It
  asserted that byte-at-a-time writes take longer than one whole write - true, by
  about eight times per byte - with a two-fold threshold for headroom, and it still
  failed once under the load of `go test ./...`, where the rest of the module is
  building and testing beside it. The neighbouring gate's own comment says a timing
  assertion in CI is a flake generator; this was one. The allocation assertions stay,
  the timing stays in the program's output where a reader can see what machine it
  came from.

- **`examples/gip/unbounded` rewrites a document larger than any buffer worth
  holding**, and says which handler patterns keep the peak flat. It generates the
  document as it writes it and discards the output, so what the peak measures is the
  rewriter and the handler rather than the caller's buffers; it measures each pattern
  at two sizes and reports the growth; and it exits non-zero if a pattern's claim and
  its measurement disagree, which is what makes it a check rather than a table.

  Two things it had to get right to be honest. The peak is a delta from a
  post-collection baseline, because `HeapAlloc` counts the whole process. And a peak
  under 8 MB is not treated as evidence either way: a bounded rewrite's working set
  is a couple of megabytes whatever the document, so the ratio between two small
  numbers is the sampler's noise - which is what reported "no handlers" as unbounded
  in the first draft.

- **The fuzzer compares the text of every node now, and its comment stops
  promising something it never did.** `FuzzRewrite` rewrites each input twice, as
  one write and in pieces, and compares the output, the structural invocation
  counts and a digest of what every handler was told. Text was left out of the
  digest for a good reason - chunk boundaries move, so a per-chunk digest differs
  legitimately - and the comment said the text handler "contributes its
  concatenation at the end instead, via textSeen". There was no `textSeen`, and
  nothing in the harness recorded any text. The one part of the library whose
  chunking is documented as write-dependent was the part with no comparison at
  all.

  The node is the unit that does not move, so the harness accumulates to
  `IsLastInTextNode` and notes each node's text into the same digest as
  everything else. 1.7 million executions at 40,000/sec found no disagreement,
  which is the first time that claim has been checked against random input rather
  than a fixed corpus.

- **`nodeinvariance_test.go`: every write size from 1 to 40, over documents that
  end inside a construct.** `examples/gip/chunkinvariance` already compares a
  larger corpus and records more per call, at seven chosen write sizes -
  1, 2, 3, 5, 7, 64, 1024. The gaps matter for a boundary test: a construct eight
  bytes long is never cut at offset four by any of those. This walks the sizes
  consecutively over eighteen documents - 720 comparisons - and adds the shapes
  that end mid-construct, where the last chunk of a text node arrives during
  `Close` rather than during `Write`:

      <p>trailing text with no end        <script>var a = 1;
      <div><p>text                        <!--
      <p>text</p                          bare text

  Nothing moved except the chunk count. The last chunk always arrives.

- **`bytecost_test.go` gates the shape of the write cost**, in allocations rather
  than in time, since allocation counts are identical on every machine and timings
  are not. Four tests: the allocation count does not change with the write size,
  over seven document shapes and four write sizes; the per-byte cost is flat
  across four-fold steps in document size; a pending tag costs less per byte than
  ordinary markup, which is the claim that inverts the old story; and a hundred
  empty writes cost nothing.

  The measurement slices a `[]byte` rather than converting `string` slices,
  deliberately and with a comment saying why: the first version of it wrote
  `[]byte(in[i:end])` and so paid one allocation per write of its own, which is
  the same figure it was measuring the library for. Half the reported cost was the
  harness.

- **`examples/gip/bytewise` measures the curve for a caller's own document.**
  Reads a document on stdin, or generates one of the pathological shapes with
  `-shape`, and prints allocations and time per byte at several write sizes. With
  `-check` it re-measures a document four times the size and says whether the
  per-byte cost held, which is the same assertion the gate makes, available to
  anyone who would rather run it against their own markup on their own hardware
  than take a table on trust.

- **`examples/gip/corpus`, which says which of the documented hazards a document actually
  has.** Twelve constructs, each one a place where a streaming rewrite and a browser see
  different documents, counted with the first offset and what each costs. Run over the three
  real pages vendored in lol-html's own benchmarks:

      cloudflare.com.html      119237 bytes
        implied end tag                   1     at 98980
      ecma402-spec.html        391568 bytes
        none of the documented constructs
      html-parsing-spec.html   713903 bytes
        implied end tag                3247     at 7829
        element nothing closes            5     at 16
        p containing a block element    194     at 245839

  None of these is a defect in a page - they are ordinary HTML. The point is that the answer
  is per corpus, and the three documents say so: one page has a single implied end tag near
  its end, one has none of the twelve, and one has thousands. A rewrite that positions
  content at an element's end is safe on the second and wrong on the third, and which of
  those a site produces is a fact about its templates rather than about HTML.

  The element nothing closes at offset 16 of the parsing specification is its `<html>`; the
  other four are `<body>` and three paragraphs. Reporting them where they open rather than
  at the document end is the difference between a position a reader can look at and the
  moment the scan found out.

  Every detector uses the rewriter alone, with no parser to compare against: an implied end
  tag is an end-tag callback whose name is not the element's, fostered content is text
  arriving while a table is open and no cell is, a self-closing HTML tag is `IsSelfClosing`
  on an element that can have content. The scan runs with strict mode off so it survives the
  documents it reports on, and says that a document containing a raw-text tag inside a
  select is one it has not fully seen - the count is a floor rather than a total.

  Its tests find each of the twelve in a document that has it, check that a tidy document
  reports none, and pin the distinctions that make the counts meaningful: a base before the
  URLs is not the construct, a closed list has no implied end tags, text in a cell is not
  fostered, and a self-closing *void* element is not the self-closing hazard.

- **`differential/surgery_test.go`, the same edit done two ways.** A streaming rewrite and
  tree surgery are different machines, and most people think about editing HTML as the
  second one. This compares them directly: four edits - adding a class to every div,
  inserting a comment before every image, renaming `b` to `strong`, removing every image -
  applied by the rewriter and by walking a `golang.org/x/net/html` tree, over six documents,
  three of which close every element and three of which do not. The comparison is the
  document a parser builds from each result, written back out, because comparing node
  structure would report a difference that is not one: removing an element between two runs
  of text leaves one text node in the stream and two in the tree, and both mean the same
  document.

  All four agree everywhere. The fifth edit is the documented exception: replacing an
  element's content agrees with surgery on a list whose items are closed and cannot on one
  whose items are not, where the streaming edit takes the later items with it - and the
  test asserts both halves, including that the streaming version really does end up with
  fewer items. A third case shows the recommended way out: the same edit positioned at the
  end tag only when the name matches agrees with surgery on the closed lists and declines
  to do anything on the open one, which is doing less rather than doing something else.

- **`differential/preserving_test.go`, the two halves of "does this rewrite preserve
  meaning".** Six rewrites that should leave the tree exactly as it was - setting an
  attribute, adding a class, renaming `b` to `strong`, inserting a comment before an
  element, inserting one after it with the end-tag name check, and reading everything and
  changing nothing - are run over fourteen documents chosen because each is a place a
  streaming rewrite can go wrong: implied end tags, foster parenting, a template's own
  parse rules, foreign content, a form in a table. The comparison is the x/net/html tree
  with the intended change subtracted, node for node. All six preserve all fourteen.

  The other half asserts that the documented hazards still are ones: a div wrapper inside
  a paragraph, prepending an element into a template holding rows, renaming a div to a
  table, appending to a list item whose end tag was omitted, prepending into a table. If
  any of those stopped changing the tree, the documentation about it would be wrong, and
  the test says so. One pair makes the difference explicit: the same insertion at an
  element's end changes nothing with the end-tag name guard and changes the tree without
  it.

  `examples/gip/preserve` is the part that can run against a live document without a
  parser dependency: it compares the token stream the rewriter itself reports, and its
  own documentation is clear about what that cannot see - foster parenting, a content
  model that deletes nodes, and any wrapper, since "put this around that" has no
  expression in a token sequence. Its hazard is one the token stream *can* see: replacing
  a list item's content, which on a list with implied end tags deletes the items after it,
  and on a closed list does exactly what it says.

- **`examples/gip/streamvsmemory`, which runs a rewrite both ways and says what differs.**
  The in-memory shape is the one people test with - `RewriteString` is the shortest way to
  try a rewrite - and three of the differences it hides are worth having a tool for:

      output            identical (1671 bytes)
      element handlers  42 both ways
      text handlers     80 in memory, 87 streamed
      text nodes        40 both ways
      comment handlers  40 both ways
      memory floor      1024 both ways

  The output and the element, comment, doctype and text-*node* counts are guarantees; the
  text-*chunk* count is not, because a text node's chunk boundaries follow the writes. The
  memory floor is where the two shapes really part company, and `-floor` measures it for
  both, since a limit chosen in memory can be far too small when streamed.

  Nothing in the library changed. The tests pin which lines are guarantees, that smaller
  writes give more chunks and the same node count, that a differing chunk count is not a
  failure, and that the in-memory floor is not enough for the streamed shape on a document
  big enough to show it.

- **`examples/gip/observe`, a rewriter that changes nothing and proves it.** The
  properties module already asserts that a read-only rewrite is the identity over
  generated documents; this is the same claim as a tool, over a caller's own document,
  with the profile of what it saw: elements by name, attributes by name, text nodes and
  bytes, comments, the doctype.

  Nothing in the library changed. What the program adds is the honest handling of the one
  exception, which is not the handler's: a text handler decodes and re-encodes, so a
  document holding bytes its declared encoding cannot decode comes out with U+FFFD where
  those bytes were. The tool reports that difference as the document's - naming the byte
  offset - and exits zero, while any other difference is the observer's and exits
  non-zero. `-no-text` skips the text handler, which is the only way to observe such a
  document without changing it.

  Its tests run twenty awkward documents - unquoted attributes, single quotes, stray
  whitespace in tags, references the parser does not decode, a NUL, a U+FFFD, raw text,
  a template holding table rows, conditional comments, a PHP block, CDATA, an unclosed
  paragraph, the empty document - through one Write and through 1-, 3- and 64-byte
  writes, and check that the counts do not depend on the chunking.

- **`differential/tagname_test.go`.** `TagNamePreserveCase` had exactly one
  mention in the whole suite - `_ = e.TagNamePreserveCase()` inside the operations
  fuzzer, with the result discarded - so nothing checked what it returned. It now
  has eight SVG cases and four HTML ones against `x/net/html`, which is where a
  claim about what a parser does belongs.

- **The README's code is compiled now, and one block of it did not compile.**
  Eight Go blocks, none of them built by anything. `readme_snippets_test.go` holds
  each one verbatim inside something that typechecks, and `readme_test.go` asserts
  the README and that file have not drifted - comparing with whitespace collapsed,
  since the snippet file carries an extra tab and gofmt aligns trailing comments
  its own way. A changed identifier or argument still fails; indentation does not.

  The block illustrating detached units declared `src` and never used it, so it
  could not have compiled. It now copies the value out, which is the contrast the
  surrounding paragraph is about anyway - a copied string survives its handler and
  a retained `*Element` does not.

  Four of the blocks make a claim in a comment about what they produce, and those
  claims are checked too, which a compiler cannot do. Verified the gates can fail:
  changing a block's code, adding a block nothing compiles, and both are caught.

  These tests read the README from disk, and the first version of them assumed
  LF: the Windows runner checks out CRLF, so the fence never matched, no blocks
  were found, and one of the three checks passed *vacuously* rather than failing -
  its pattern contained a newline and simply never matched anything. Line endings
  are normalised on read now, and the block count is asserted exactly rather than
  as "more than none", so an extraction that stops working cannot leave the checks
  below passing on an empty list.

- **`readme_test.go` checks the README against the package.** Three tests: every
  `lolhtml.X` it names exists, a short explicit list of names a caller cannot
  safely do without is mentioned at all, and the sentence that went stale has not
  returned. The middle one is a judgement call encoded as a list, and says so - a
  name belongs on it only if a caller who does not know about it writes something
  unsafe rather than something clumsy. All three verified to fail: by renaming a
  README identifier, by removing a required name, and by restoring the stale
  sentence.

- **`properties/` is vetted now, and a script fails if a module is added without
  being.** `go vet ./...` stops at a module boundary, so the root's invocation
  covers neither `differential/` nor `properties/`. CI vetted the first two and
  `make lint` vetted only the root, which means `properties/` - a whole module,
  including the generator - had never been vetted since the day it landed.
  Confirmed by putting a `fmt.Printf` with the wrong argument type in it: `make
  lint` passed, and so would CI.

  `scripts/check-modules.sh` finds every `go.mod` in the tree and fails unless the
  workflow both vets and tests each one, so the next module cannot be forgotten
  the same way. Verified in both directions by removing a vet step and by
  pointing a test job at a directory that does not exist.

  `properties/` also runs under `-race` now, like the root and `differential`.
  Measured at 1.5 seconds without and 7.9 with, for 2000 checks, which is worth
  paying for code that drives the library harder than any fixed corpus.

- **The documentation's code is compiled and run now, at least in part.** The
  package carried about 140 lines of indented code inside doc comments and zero
  example functions, so none of it was compiled, let alone executed.
  `example_test.go` holds sixteen runnable transcriptions of the load-bearing
  claims - the streaming shape, the insertion-order table, that matching is
  decided before any handler runs, the `:not()` defect, the escaper equivalence,
  the repeated-attribute split, the raw-text refusal, raw source in and out, the
  document-end truncation, bogus comments, comment refusal, removal semantics, the
  encoding fallback, strict mode, and text chunking. `go test` compiles them, runs
  them and checks their output, so those claims cannot rot silently.

  Two of the sixteen failed on the first run, and both times the transcription
  was wrong rather than the documentation: an example of `:not()` dropped the
  element the documented output depended on, and a strict-mode example used a
  construct that does not trigger the guard. The documentation was right in both
  cases, which is worth recording alongside the five claims that have been found
  wrong by hand.

- **`apisurface_test.go` fails if a test file does not so much as mention an
  exported name.** The crudest possible coverage check - a name appearing in the
  text of a test, not a claim that anything about it is asserted - and it found
  two gaps immediately. `WithGracefulBailOut` was never used in a test, only the
  `MemorySettings` field it sets, which is why the bug above could exist;
  `HandlerError.Unwrap` was never mentioned either, though it is the only way a
  caller recovers the error their own handler returned. Both now have tests. The
  same file counts the exported names, so adding one is a deliberate act visible
  in a diff.

- **`FuzzRewrite` compares what the handlers were told, not only what came out.**
  It checked output bytes, failure parity, handle counts and handler invocation
  counts - all four of which are identical whether a source location is absolute
  or relative to the current `Write`, whether a tag name is reported in the wrong
  case, or whether an attribute is read from the wrong element. What a handler
  sees is the library's other interface and nothing compared it across chunkings.
  Each structural handler now records what it was given and the two runs' records
  are compared. Text is deliberately excluded: chunk boundaries do split text
  nodes, so its record would differ legitimately.

- **`sourceloc_test.go` pins the promise that made that gap matter.**
  `SourceLocation` is documented as "counted from the first byte fed to the
  rewriter", which anything extracting by slicing its own copy of the input
  depends on. Ten documents at five chunk sizes, checking that element, comment
  and doctype locations are identical however the input arrives, that slicing the
  document at them returns the unit, and that text chunk locations stay absolute,
  contiguous and in order even though the chunks themselves move.


- **Two properties for attribute rewriting, and two for duplicates.**
  `properties/attributes_test.go` states four claims over generated documents:
  `RemoveAttribute` removes every copy of a repeated attribute, `SetAttribute`
  replaces the first and leaves the rest, an attribute-only rewrite leaves the
  tree an independent parser sees exactly as it was - error recovery included,
  which the generator produces on purpose - and removing an absent attribute is
  a no-op. The shared generator deliberately avoids duplicate attributes, so the
  first two bring their own document builder.

- **Error message quality is gated.** Nothing checked that this package's errors
  say anything useful, and they are the surface a caller meets when something
  goes wrong. `errquality_test.go` collects every reachable error - 23 of them,
  covering all four typed errors and all three sentinels - and checks the
  properties they should share: non-empty, attributable to the package, no
  formatting fault, no dangling colon from a wrap whose inner error was lost, and
  crucially that an error about a caller's input contains that input. A second
  test fails if an exported error type has no case, so the list cannot rot as the
  package grows. Verified to have teeth by removing the selector from
  `SelectorError` and by dropping the wrapped error from `HandlerError`; both
  fail with a message that names the problem.

- **The differential oracle now covers what a rewriter reads, not only what it
  copies.** Passthrough byte-identity says the rewriter can copy a document.
  `differential/links_test.go` says something harder: that every anchor's target
  and text, extracted by a rewrite, matches what `golang.org/x/net/html` reads
  out of the same document - across 47 documents at four chunk sizes each. That
  exercises attribute reading, text accumulation across nested markup, and chunk
  boundaries together, which is the part a rewriter is responsible for rather
  than the part lol-html has its own fuzzing for.

  It also widens an existing claim: `TestTextHandlerSeesAllText` compared the
  concatenated text chunks against the parser's text, but only for a document
  written in one call. Chunk boundaries are the one thing lol-html explicitly
  does not promise to reproduce, so the chunked version of that claim is the
  interesting one, and it is the only way a document arrives in production.

  No disagreement was found. That is the result, not a lack of one: the claim is
  now checked rather than assumed.

- **The fuzzers now vary the rewriter's configuration.** Neither passed a `With*`
  option, so every one of roughly 32 million nightly executions ran with the
  defaults: utf-8, strict on, no memory limit, no ESI. Whole categories were
  therefore unexplored - an insertion transcoded into a legacy encoding, a
  streaming sink interrupted by a memory bail-out, malformed markup with strict
  mode off - and both fuzzers assert the live handle count on every iteration, so
  a leak reachable only under one of those would never have been found. That is
  the same shape of gap that hid the `StreamFunc` panic leak.

  `FuzzOperations` now derives an encoding, a strict-mode choice, ESI and an
  occasional generous memory limit from the program bytes. `FuzzRewrite` derives
  an encoding and strict mode, given to both of its writers - but deliberately
  **not** a memory limit: the memory a rewrite needs depends on how the input is
  fed, by a factor of eight in one measured case, so a limit one writer stayed
  under and the other did not would make them differ legitimately and the
  chunk-invariance test would report it as a bug. Both run clean, and both found
  new interesting inputs immediately - 35 and 26 - so the variation is reaching
  states neither had before.

- **The cost of registering selectors is gated, and the parse cache now has a
  test.** `config.register` parses each distinct selector once and reuses it,
  which is deliberate and had no test - removing it would have broken nobody's
  build. `alloc_test.go` now asserts that build allocations are linear in the
  number of selectors rather than quadratic, that a repeated selector costs less
  than a distinct one, and that the saving grows with the number of duplicates.
  Verified by defeating the cache: both assertions fail, reporting "the parse
  cache is not saving anything". The cost model is documented too - about five
  allocations per distinct selector at build, and matching cost that grows with
  the number registered on every element, since there is no index by tag or
  class.

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
- **Two stray `// //` lines in the package documentation.** They rendered as a
  literal `//` in the middle of a paragraph break, in the sections on inserting
  into a script and on rewriting text that is already there.

- **Lower-cased attribute names break more than SVG.** `SetAttribute`'s
  documentation explained that adding an attribute lower-cases its name, that
  updating one keeps the document's spelling, and that this is a silent breakage in
  SVG and MathML where `viewbox` is not `viewBox`. It also said that "in HTML that
  is nothing", which is true of an HTML parser and not of whoever reads the
  document next.

  A framework template is the other case, and it is HTML-shaped text rather than a
  document: parsed as HTML by anything using this library, with attribute names
  that are identifiers to a compiler.

      source              Attribute.Name      Attribute.NamePreserveCase
      *ngIf="ok"          *ngif               *ngIf
      [ngClass]="c"       [ngclass]           [ngClass]
      [(ngModel)]="v"     [(ngmodel)]         [(ngModel)]
      v-bind:someProp     v-bind:someprop     v-bind:someProp
      @myEvent            @myevent            @myEvent

  `*ngIf` is a directive and `*ngif` is not. So a rewrite that reads `Name` and
  writes it back turns the directive off, a report built from `Name` names
  something the author cannot search for, and an attribute added as `*ngIf` arrives
  as `*ngif`. Updating one already in the document keeps the spelling, and
  `Replace` with built markup is the other way round - the same two escape hatches
  SVG has.

  The section says that now, with the table, and ends on the rule rather than the
  example: the question is not whether the document is HTML but whether whoever
  reads it next cares about case. B179.

- **Renaming a container to a name that cannot hold content has four answers.**
  `SetTagName`'s documentation covered the content-model cases - a `table` that
  fosters its content out, a `select` that deletes it - and not the void direction,
  which is worse because the answer depends on which void name. Measured against
  x/net/html on `<div class="w">x</div>`:

      renamed to        the tree a parser builds
      br                two br elements, with x between them
      img hr input      one element, and x is now its sibling
      wbr area
      col               no element at all, only x
      meta              the element in <head>, and x left in <body>

  `</br>` is the one end tag HTML treats as a start tag, so the stray one becomes a
  second element and the rename duplicated the widget. A `col` outside a table is
  dropped, so the rename deleted it. A `meta` belongs in the head, so the rename
  moved it there and left its content behind. The output markup has the same shape
  in all eight cases and nothing errors.

  A hyphenated name is always an ordinary container, which is what makes a
  custom-element target safe. B178, gated by
  `differential/rename_test.go` `TestARenameIntoAVoidNameHasFourAnswers`.

- **One rewriter per document, not one per fragment.** The Streaming section
  promises that chunk boundaries never affect handler behaviour, which is true and
  is about chunks of one stream. It does not extend to splitting the work between
  two rewriters, and nothing said so.

  Measured on `<p>a</p><script>alert(1)</script><p>b</p>` with every script
  removed, over all forty places the document can be cut in two:

      one rewriter, whole document        saw p script p    <p>a</p><p>b</p>
      one rewriter, two writes, any cut   saw p script p    <p>a</p><p>b</p>
      two rewriters, cuts 9 to 15         saw p and p       the script, whole
      two rewriters, cut 16               saw p script, p   alert(1) as text
      two rewriters, any other cut        the script is removed

  The seven are the cuts strictly inside the eight bytes of `<script>`. The first
  fragment ends mid-tag, so no handler of any kind runs for it and the bytes pass
  through; the second begins mid-name, so its remainder is text and passes through
  too; and the join reassembles an element neither pass inspected. Cut 16 fails
  differently - the first pass does remove the script, but its content was in the
  other fragment, so the payload survives as text beside a stray end tag. Nothing
  errors in any of it.

  Two things are going on and the section now keeps them apart. A tag is the only
  construct a document can end inside and have nothing report it: `<!-`, `<!--`,
  `<!`, `<?php` and `<![CDATA[x` each produce a comment with the text so far,
  `<!DOCTYPE` a doctype, `<script>var a` the element and its text, while `<p`,
  `<p attr="v`, `</p` and `<script` produce nothing at all and are still emitted. A
  stray end tag is unreported too, and harmless.

  Which unfinished constructs *swallow* what follows is a wider set, and the same
  one `DocumentEnd.Append` already documents: a tag, a comment, a doctype, or any
  open raw-text element. An element merely left open is not one of them, because
  more content is simply more content. That set is what decides whether a join is
  safe, and the handler blindness is what makes the tag case silent as well as
  wrong.

  A caller who has to accept fragments from elsewhere can test one instead of
  trusting it, without reimplementing the tokenizer: append a sentinel element,
  rewrite, and see whether the sentinel's handler runs. Asking the same question by
  scanning for a `<` after the last `>` fails in the direction that matters - over
  a fixed set of 4000 generated fragments it calls 1007 of them safe when they are
  not, and never over-reports, because it cannot know that `<!DOCTYPE` does not
  begin with a letter, that an open `<script>` has its last `>` behind it, or that
  a bare `</` at the end is an unfinished end tag.

  So the section now says to feed a document assembled from pieces to one rewriter
  as successive writes, and that a rewrite which must work on fragments has to be
  able to say where a fragment may be cut: element boundaries are safe and byte
  offsets are not. B177 records it, gated by `fragment_test.go` - which pins which
  cuts fail and in which of the two ways, so a change upstream that made the tail
  of a truncated document visible would fail the test rather than leave the
  documentation wrong.

- **`EndTag.Before` does not lose insertions where `Append` does.** The end-tag
  section documents what happens to every end-of-element operation when the source
  omits an end tag - `Append` and `After` keep one insertion of three,
  `SetInnerContent` and `Replace` delete content - and it did not mention the one
  operation that keeps all of them. Applied to every item of
  `<ul><li>a<li>b<li>c</ul>`:

      Append         <ul><li>a<li>b<li>c[1]</ul>
      EndTag.Before  <ul><li>a<li>b<li>c[3][2][1]</ul>

  All three handlers run at the single `</ul>`, innermost first, so all three
  insertions survive. The position is no more correct than `Append`'s - the content
  belongs at each item's own end and the source has no such position - but one of
  them drops content silently and the other does not. With the end tags spelled
  out the two are identical and per-item, so the difference is about the omission
  and nothing else.

  The package section now carries the row, and `Element.Append` and
  `EndTag.Before` each point at the other, because the choice between them is
  usually made without knowing there is one. B176 records it.

- **The rule for which selectors are supported does not predict `:not(div p)`.**
  The documentation gives one rule - a selector works if the rewriter can decide
  it when it sees the start tag - and it covers almost everything. It does not
  cover a combinator inside `:not()`, which is rejected even though the plain
  descendant selector decides exactly the same question, at the start tag, and
  works. The consequence is that there is no way to write "not inside an X" as a
  selector, and a handler that needs the answer has to keep its own stack.

  The `:not()` section now carries the measured boundary - which arguments are
  accepted, which are rejected, and that the sibling combinators are unsupported
  everywhere while the descendant and child ones are unsupported only here.

  It also says what the error looks like, because the message points somewhere
  else: all four rejections report "Unsupported pseudo-class or pseudo-element in
  selector", which names `:not()` rather than what is inside it, and then advises
  escaping a colon in a tag name. Neither half points at the combinator. B175 and
  `TestACombinatorInsideNotIsRejected` pin the rejection and the message together,
  so upstream accepting the selector, or improving the message, fails the test
  rather than leaving the documentation wrong.

- **Whether a table wrapper escapes a paragraph depends on the doctype.** The
  package documentation says a wrapper is two insertions and the parser decides
  what they wrap, and B146 says a block wrapper inside a `<p>` takes the content
  out of it. Both are right, and `<table>` is an exception whose answer is not a
  property of the markup at all: a table start tag closes an open paragraph in a
  standards-mode document and not in a quirks-mode one. Measured against
  x/net/html:

      wrapper     no doctype (quirks)   <!doctype html>
      <table>     stays in the <p>      leaves the <p>
      <div>       leaves                leaves
      <section>   leaves                leaves
      <ul>        leaves                leaves
      <span>      stays                 stays

  Every other wrapper is mode-independent: a block one leaves in both modes, an
  inline one stays in both. A legacy doctype - `HTML 4.01 Transitional` - is quirks
  too, so it behaves like having none.

  It matters where table wrappers are the technique rather than an accident, which
  is converting a page to the markup an email client renders - on documents that
  frequently have no doctype at all. The same input gives two different trees and
  nothing reports it.

  Documented as its own section before the wrapper one, and as B174.

- **What a rewrite does to a body that is not text, and why it depends on the
  handler set.** The text path decodes and re-encodes, so a byte that is not valid
  in the declared encoding becomes U+FFFD. That was documented as a one-character
  cost on a Latin-1 page. In a proxy it is not one character:

      body                     element handlers only   with a text handler
      gzip of a small page                 identical   longer, and not gzip any more
      a PNG header                         identical   two bytes longer
      256 arbitrary bytes                  identical   482 bytes
      JSON, valid UTF-8                    identical   identical
      a windows-1252 page                  identical   two bytes longer

  Neither column reports an error. So a proxy that forgets `Content-Encoding`
  either destroys every compressed response or silently rewrites nothing, and which
  one it does depends on whether it happens to have registered a text handler. That
  is a bad thing to learn from a user.

  The other headers are written down with it now, in a "Rewriting an HTTP response"
  section: `Content-Type` decides whether this is a document at all, the charset
  parameter is the authority on the encoding and belongs in `WithEncoding`,
  `Content-Length` has to be deleted because the rewrite changes the length, and a
  failure partway has already sent a prefix of the page - so a broken rewrite
  cannot become a 502.

  One thing worth knowing for the charset: `NewWriter` refuses `utf-16le` and
  `utf-16be` with an `EncodingError` for not being ASCII-compatible, alongside the
  labels it does not know. So a proxy can decide whether it can rewrite a response
  by trying to build the rewriter, rather than by keeping a list of labels that
  would drift from the library's. Recorded as B173.

- **What a rewriter costs to build, for a workload that builds one per document.**
  The registration cost was documented in allocations, "paid once per
  `NewWriter`". For a queue of small documents that phrase is the whole story
  rather than a footnote: a `Writer` cannot be reused - `Close` ends it, there is
  no reset, and a parsed selector belongs to the Writer that parsed it - so every
  item pays the whole rule set again. Measured as the allocations for a complete
  rewrite:

      selectors   empty document   1 KB   16 KB
              1               23      26      26
             10              105     106     106
             50              406     408     408

  Read down a column and the cost is the rule set; read across and a document that
  matches nothing adds almost nothing. In time, fifty selectors is about 38µs of
  construction on an M3 Pro, which is more than rewriting a one-kilobyte document
  with them costs - so below about sixteen kilobytes a document, such a rewrite
  spends more time being built than running. Fewer selectors or bigger documents
  are the two ways out, and there is no third.

  How many goroutines to run a queue on is a property of the machine rather than of
  the library, and it is lower than the core count. 400 one-kilobyte documents with
  fifty selectors, on twelve threads:

      workers   wall clock   items/sec   speedup
            1         39ms       10268      1.00x
            2       20.6ms       19447      1.89x
            4       12.1ms       33074      3.22x
            8       20.2ms       19826      1.93x
           12       18.5ms       21630      2.11x

  The peak is at four and it is worse above it, because a rewrite that is mostly
  allocation contends. A large-document workload saturates instead: 3.4x at four
  workers and the same at twelve. Written up in the cost section and as B172.

- **A mutated start tag comes back re-serialised, and the output is not the input
  plus the edit.** Setting an attribute on every anchor of the vendored
  cloudflare.com.html takes the document from 119,237 bytes to 114,542 - four per
  cent smaller, having been asked to make it bigger by fifteen bytes an element.
  183 of its 233 anchors have their attributes on separate lines, and those
  newlines do not survive the edit.

  What survives is more than "re-serialised" suggests, and what does not is
  precise: each attribute's own source text comes back exactly as it arrived, and
  the separators between attributes are regenerated.

      kept                           changed
      single quotes                  newlines between attributes -> one space
      unquoted values                tabs and runs of spaces     -> one space
      spaces around the equals       a trailing space before >   -> dropped
      the case of a name             a missing space between two -> added
      the case of the tag            <circle r="1"/>             -> <circle r="1" />
      duplicate attributes
      bare booleans
      entities in values
      newlines inside a value

  Which operations trigger it is worth knowing too, because most do not:

      re-serialises              leaves the bytes alone
      SetAttribute               reading anything
      SetAttribute to the        AttributeList
        value already there      Before, After, Prepend, Append
      RemoveAttribute, when      SetInnerContent
        the attribute is there   OnEndTag, SetUserData
      SetTagName                 RemoveAttribute of an absent attribute

  Setting a value to the one already present still re-serialises, so a rewrite
  that means to leave unchanged elements untouched has to compare first and set
  only when it differs.

  The consequence is for anything comparing bytes: a diff, a checksum, an ETag or
  a "did this change" check over the output sees changes the caller did not ask
  for. Documented on `Element.SetAttribute` with the tables, referenced from
  `ClearEndTagHandlers`, and recorded as B171.

- **A second pass does not have to hold the document.** The two-pass section
  explained the cost of running a rewrite twice - about double the allocations -
  and then said "the document has to be held while the first pass reads it, which
  is the part that stops it being a streaming rewrite at all". That is true of a
  second pass that needs what the first pass learned: a table of contents, a
  canonical URL derived from the body. It is not true of a second pass that is
  just another rewrite, and that is the common case, because acting on markup an
  earlier handler produced is exactly what a second pass is for.

  A `Writer` is an `io.Writer`. So a rewriter can be another rewriter's
  destination, and then both stages run at once:

      second, _ := lolhtml.NewWriter(dst, annotate...)
      first, _  := lolhtml.NewWriter(second, insert...)
      io.Copy(first, src)
      first.Close()
      second.Close()

  Measured against the buffered form over 400 anchors:

      one pass, both handlers            831 allocations   document held: no
      piped, one handler each           1645               no
      buffered, one handler each        1655               yes, all of it

  Same doubling, same output byte for byte, and the peak heap above the baseline
  stays flat as the input grows - 2.8 MB piping 1 MB, 3.5 MB piping 4 MB, 3.5 MB
  piping 16 MB. The downstream stage sees bytes before the upstream stage has been
  given the whole document, which is what a buffered second pass cannot do.

  Two things about the shape were nowhere. **Close upstream first**, because each
  stage's `Close` flushes into the next: measured on `<p>a</p`, closing the
  downstream stage first loses `</p` and the upstream `Close` reports `ErrClosed`.
  A document that ends cleanly has nothing to flush, so the wrong order looks fine
  on the documents most tests use. And **an error in any stage reaches the caller
  through every stage above it with its identity intact** - `errors.Is` finds a
  third stage's sentinel in what the first stage's `Write` returned - so a pipeline
  needs no error plumbing of its own.

  Written up in the package documentation's two-pass section, on `Writer.Write`,
  and as B170.

- **What splits the output is matching, not editing.** The cost section said the
  number of writes a destination receives "is decided by what the rewrite does",
  and illustrated it with a mutation: one attribute set turning one write into
  twelve. Editing is not where it starts. A handler that does nothing at all splits
  the output around every element it matched. Measured on 200 anchors handed over
  as one 6200-byte `Write`:

      no handlers                        1 write
      a selector matching nothing        1 write
      a handler that does nothing      400 writes
      the same, reading an attribute   400 writes
      an end-tag handler               600 writes
      RemoveAttribute                 1200 writes
      SetAttribute                    2600 writes, mostly of one byte

  Two writes per matched element before the handler has done anything. The
  registration is not what costs - a selector matching nothing costs one write for
  the document, and so does a comment handler on a document without comments - so
  it is the matches that count.

  The case this matters for is the one that looks free. A read-only instrumentation
  pass - a counter, an audit, a linter - over a rewrite streaming to an unbuffered
  destination turns one write per document into two per element. The output is
  identical, which is exactly what makes it easy to miss, and "adding a read-only
  handler cannot change the output" is documented and true. The write pattern is
  not the output.

  At 50 microseconds a write, which is a plausible figure for a socket, the
  passthrough above takes 96 microseconds and the attribute set 192 milliseconds.
  A `bufio.Writer` of 4096 bytes collapses every row to two or three writes. The
  library still declines to buffer on the caller's behalf, for the reason it always
  did: a buffer is a promise not to write yet, and only the caller knows whether
  the far end is a browser waiting for a page.

  Rewritten in the package documentation's cost section, given its own subsection
  in the README's performance section, and recorded as B169.

- **A destination failure stops every handler, including the one a summary is
  written in.** The docs said a destination error surfaces from `Write` or `Close`
  and poisons the Writer. They did not say that the rewrite stops entirely: no
  further element, comment or text handler runs, and `OnDocumentEnd` never runs at
  all.

  The last one is the trap, because the document end is where a rewrite naturally
  writes its accounting. A program that counts what it rewrote and logs the total
  from `OnDocumentEnd` logs nothing on exactly the runs worth knowing about - the
  ones where the client went away. Measured on a 760-byte page against a
  destination that accepted 99 bytes: 2 links rewritten, 2 comments, 6 text chunks,
  document-end handler not run. The counters have to live where the caller can read
  them after the error, and they say what the rewrite reached rather than what the
  page contains.

  Two smaller facts, both now pinned. Where a destination failure stops does not
  depend on the write sizes - the same handler counts and the same byte count at
  every write size from one byte to the whole document - because the budget is a
  fact about the destination and the page is a fact about the document. And a
  destination that accepts *nothing* still sees one handler run, since handlers run
  as tokens are parsed and the destination is written to afterwards.

- **`Close` discovers a broken destination only when `Close` is the call that
  writes.** Which is rarer than it sounds. Measured, with the destination made
  unable to accept anything after the last `Write`:

      document                     written during Close   Close reports
      <p>text</p>                  nothing                nil
      <p>unclosed text             nothing                nil
      <div><p>a                    nothing                nil
      <script>var a =              nothing                nil
      <p>a</p                      the unfinished end tag   the failure
      <div a="x                    the unfinished attribute the failure
      <!--unclosed                 the unfinished comment   the failure
      <p>text</p><                 the bare less-than       the failure
      any document, appending at the document end           the failure

  So a rewrite of an ordinary document has handed everything over by the time
  `Close` is called, and a destination that broke in between is invisible to it.
  Documented on `Writer.Write` and `Writer.Close`, and as B168.

- **How to stop a rewrite early, and what it leaves behind.** Nothing said how to
  stop, though a rewrite over a stream that does not end has to. There are two
  ways and the docs described neither.

  Returning an error from a handler stops it where the handler said, and what has
  already reached the sink turns out to be a stronger thing than a truncation:
  byte for byte what a fresh rewriter produces from that many bytes of the input.
  No half-serialised element, no tag cut in the middle, and the unit whose handler
  stopped is not emitted at all - so the partial output can be kept or served.
  Measured for every handler kind that can stop, at write sizes from one byte to
  the whole document. Where the prefix ends:

      an element handler   the bytes before that element's start tag
      an end-tag handler   the bytes before that end tag
      a comment handler    the bytes before that comment
      a text handler       the bytes before that chunk

  The last row is the odd one out, and it matters. A chunk is not a position in
  the document - how many chunks a text node arrives in depends on the write sizes
  - so "stop at the fifth chunk" stops in a different place for a different reader
  upstream: measured at 83 bytes written whole, 64 in 64-byte writes and 23 a byte
  at a time. Counting to `IsLastInTextNode` instead lands in the same place every
  time.

  The caller's own sentinel survives all of it. Wrap it, and `errors.Is` finds it
  in what `Write` returned, in every later `Write` that is refused, and in what
  `Close` returns - where it sits under `ErrPoisoned`. So the Go idiom of checking
  only `Close` still tells a caller why its own rewrite stopped.

  The other way to stop is to stop writing and call `Close`: no error, no
  poisoning, and the output is a rewrite of what was fed. The cost is granularity,
  since the condition is checked between writes - asking to stop at the third
  heading of a generated stream stops after the 114th with 4 KB writes, the 4th
  with 64-byte writes and the 3rd with 7-byte writes. That is the one to reach for
  when the condition is the caller's; the sentinel is for when it is the
  document's.

  Written up in the package documentation under "Stopping early" and as B167.

- **`SetUserData` costs a handle per unit, and only `OnEndTag` said so.** Both
  calls ask the library to remember something until the rewrite ends, and both
  allocate outside `MaxMemory` - that limit is lol-html's parsing buffer and this
  is the binding's handle table. `OnEndTag` has a paragraph about it, with figures.
  `SetUserData` had five words: "The attached value is released with the Writer",
  which reads as a note about lifetime rather than about memory. Measured over
  documents fed in 4 KB writes, peak Go heap above the baseline:

      pattern                      1 MB doc   4 MB doc   16 MB doc
      reading elements              3.5 MB     3.5 MB      3.5 MB
      editing attributes            3.5 MB     3.5 MB      3.5 MB
      reading text per chunk        3.6 MB     3.6 MB      3.6 MB
      SetUserData per element       8.4 MB    32.1 MB    128.8 MB
      OnEndTag per element         10.5 MB    40.3 MB    153.4 MB
      SetUserData per text chunk   29.8 MB   117.2 MB    422.2 MB

  About 150 bytes per element, and 520 MB by the time the document is 64 MB.

  Three things were not written down anywhere. **Setting the value to nil releases
  the handle immediately**, which is what makes a bounded rewrite possible when a
  value has to reach a later handler - clear it in the handler that reads it and
  the peak stays flat. **Replacing a value releases the one it replaced**, so
  setting it twice holds one handle rather than two. And **`ClearEndTagHandlers`
  does not release anything**: it stops the callbacks running and keeps the
  handle, so it is not a way to bound the cost that `OnEndTag` describes.

  The text case is the sharpest of them, because the unit is the chunk. How many
  chunks a text node arrives in is decided by the caller's write sizes, so this is
  the one cost in the library that depends on how a document was fed rather than on
  what it says:

      one 2000-byte text node    written whole          2 chunks     4 handles
                                 1024-byte writes       3 chunks     5 handles
                                 64-byte writes        33 chunks    35 handles
                                 one byte at a time  2001 chunks  2003 handles

  A rewrite reading from a socket does not choose those sizes. Documented on
  `Element.SetUserData` with the mitigation, on `TextChunk.SetUserData` with the
  table, on `Comment.SetUserData` by reference, on `ClearEndTagHandlers`, in the
  README's memory section, and as B166.

- **B4 and `SPEC.md` understated what survives chunking.** Both said "output is
  invariant; handler invocation counts are not", which reads as though any handler
  might fire a different number of times. Only a text handler does. The number of
  text nodes, the text of each one, every structural count, and the output are all
  the same however the input arrived - which is what the package documentation and
  the README already said, and what the two gates above now assert. Corrected to
  match.

- **Byte-at-a-time writing is not quadratic.** The README, the package doc,
  `SPEC.md`, the fuzz harness, two lessons in `GIP.md` and B5 in the
  known-behaviours table all said that writes are quadratic at byte granularity
  while the rewriter buffers an unclosed tag, because each write rescans the
  pending buffer. They are not, and the documented figures (4.4 ms for 4 KB
  against 43.7 ms for 16 KB) do not reproduce:

      one byte at a time        4 KB      16 KB     64 KB
      ordinary markup       157 ns/B  146 ns/B  123 ns/B
      one unclosed tag      130 ns/B  134 ns/B  144 ns/B

  Flat, at four-fold steps in size, and flat for every shape that makes the
  rewriter hold something across writes: an unclosed tag, an unclosed tag with
  many attributes, an unclosed comment, an unclosed quoted value, a raw-text
  element that never ends, one enormous text node. Also flat with no handlers,
  with a handler on the pending element, with every handler kind registered, with
  a memory limit, with a legacy encoding, and with strict mode off.

  The buffered tag turns out to be the cheap case rather than the pathological
  one: 64 KB of it costs 22 allocations and 0.9 ns per byte written whole, against
  3143 and 15.9 for the same weight of ordinary markup, because a pending tag
  produces no tokens to hand back and ordinary markup produces one per element.

  What survives is the advice, for a different reason. Each write costs about
  100 ns of crossing into C whatever its size, so writing in reasonable chunks
  matters and the fuzz harness still needs its caps - a byte-at-a-time rewrite of
  a 64 KB page spends about 7 ms crossing the boundary and about 1 ms rewriting.
  A constant factor is a thing a caller pays knowingly; a quadratic is a thing
  they have to design around, and saying the first is the second sends them
  somewhere they did not need to go.

  The benchmark table in the README was re-run on the same machine while this was
  measured, since one of its rows moves with the fix above.

- **Two handlers can run inside `Close`, and a panic from there leaves the Writer closed
  rather than poisoned.** `Close` said the error from the final flush is reported there,
  "including any handler error raised while processing the document end". The document end
  is not the only handler that runs inside Close:

      <p>a</p>   every text chunk, including the boundary, arrives during Write
      <p>a       the boundary chunk arrives during Close

  So a text handler's error - or its panic - can surface from `Close`, and a caller that
  recovers around `Write` alone has a gap. An end-tag handler for an element nothing
  closes never runs at all, so it is not a third case.

  And the two kinds of panic leave different states behind. A panic from `Write` poisons
  the Writer with the bare sentinel, because the panic went to the caller instead of
  becoming an error. A panic from a handler inside `Close` leaves it *closed*: Close marks
  it closed before it does anything, so a later `Write` reports `ErrClosed` and a later
  `Close` reports nil. Either way the native resources are released on the way out of the
  boundary and the library is unaffected - a new Writer works, and a Writer already
  mid-document is untouched.

  `Close` carries both now, and `examples/gip/panics` prints the table across all eight
  handler kinds. B165.

- **A retained `Sink` is the seventh unit, and the only one whose getter reports the
  detachment.** `ErrDetached` listed the six rewritable units and left out the `Sink`
  handed to a `StreamFunc`, which has the same handler-bounded lifetime. Measured, it is
  the best-behaved of the seven: `WriteString`, `WriteChunk`, `AsWriter().Write` and
  `Err` all report `ErrDetached`, so a retained sink cannot be mistaken for a working
  one.

  Every other unit's getters answer with a zero value and say nothing - which is the
  documented rule and the surprising half, because a retained element describes an empty
  document rather than reporting a problem, and `Attribute` returning `("", false)` is
  indistinguishable from an absent attribute. `Element.HasAttribute` is the only other
  getter that can tell them apart, an accident of its signature.

  `ErrDetached` now names the Sink and says why it is the exception, and
  `detached_test.go` covers it. `examples/gip/detached` prints the whole table - forty
  calls across seven units - so the rule can be read rather than inferred. B164.

- **A second `Close` is quiet, including after a failure.** `Close` says it is safe to
  call more than once, and that if an earlier `Write` failed it reports `ErrPoisoned`
  wrapped around the cause because "checking only Close is the ordinary Go shape, and it
  should not lose the reason". Both are true of the *first* Close. A later one does
  nothing and returns nil, which is deliberate - `faults_test.go` asserts it over
  hundreds of injected fault combinations - and worth stating next to the promise,
  because the two sentences together read as more than they are.

  The shape that gets caught is an explicit `Close` in an error path together with a
  deferred one that assigns to the returned error: the deferred call runs second, sees a
  closed writer, and returns nil for a rewrite that failed. Keep one Close, and let it be
  the one whose error is checked.

  This turn confirmed the rest of the error state machine rather than changing it. What
  is new is that it is now in one place: `examples/gip/poisoned` prints the whole table -
  what the first Write, a later Write and Close return after a handler error, a
  destination error, a memory bail-out, an ambiguous tag, a handler panic and a clean
  close, and what each of those left at the destination - and its tests pin every row.

- **`MaxMemory` does not bound the document, and its floor is not a function of the
  write size.** The option said to size the limit with the writes that will actually be
  made, and gave one example. Three measurements sharpen that into something a caller
  can act on.

  A megabyte of small paragraphs completes under a **1 KiB** limit when it arrives in
  one `Write`, and 10 KiB of the same paragraphs in 4 KiB writes does not. So the
  option is not a defence against a large body - only bounding the input is that - and
  a limit chosen with `RewriteString` can be wrong under `io.Copy`.

  The floor is not derivable from the write size, because what decides it is where the
  boundaries fall relative to the tokens. Measured on two 14 KB documents of paragraphs
  with a handler on each: one needed 4930 bytes at both 4095-byte and 4096-byte writes,
  and the other needed 4928 at 4095 and **832 at 4096**. A larger write can want a
  smaller limit.

  And the fixed part of the floor is "an element handler exists", not "the rewrite
  changes something": in one Write over 400 paragraphs, no handler and a text handler
  both complete at 5 bytes while an element handler needs 832, whether it reads an
  attribute or sets one.

  `MemorySettings.MaxMemory` carries all three, `memoryinput_test.go` gates them, and
  `examples/gip/bailout` measures the floor for a caller's own document and write size.
  B162.

- **Permissive mode is not silence, and both the README and `WithStrict` said it was.**
  Turning strict mode off was described as leaving the content after an ambiguous tag
  "treated as text, so no handler runs for it", with the document coming out "with no
  error and no handler invocation". The first half is right and the second is not.

  Measured on `<select><xmp><script>alert(1)</script></xmp></select><p>after</p>` with
  strict off:

      element handlers    select, xmp and p all fire; script does not
      text handlers       the script's source arrives as text, in chunks
      the output          identical to the input

  So the ambiguous element itself is an element; the document after a *closed*
  ambiguous tag is markup as usual, and an `<img src=x onerror=…>` after a `<title>`
  in a `<select>` still fires an element handler; and what is missed is markup that
  arrives as text.

  That last part is the useful one, because it gives a rewrite that cannot use strict
  mode something to do: a run of text holding `<script` is the signal, and returning an
  error from a text handler stops the document. The chunking matters - the tokenizer
  splits a text node around a `<` that does not begin a tag, so the check has to
  accumulate to `IsLastInTextNode` rather than look at one chunk.

  `WithStrict`, the README and `strict_test.go` all say this now, and
  `examples/gip/strictmode` prints the difference between the two modes for a given
  document. B161.

- **The declared encoding changes what a document says and never what counts as
  markup.** `WithEncoding` described what each label does to the characters and said
  nothing about the property a caller accepting a label from a `Content-Type` header
  actually needs.

  In a browser, a legacy multi-byte encoding can hide a markup character: a lead byte
  takes the byte after it, so a quote or a `>` can vanish into a character and a filter
  reading bytes disagrees with a browser reading characters about where the tag ended.
  That is a whole class of cross-site scripting.

  Measured over all 36 accepted encodings, against a corpus that puts every markup
  character after nine different lead bytes: the byte spans of the elements, their
  names, their attribute names, and the spans of the text and comments are identical in
  every one of them - and identical to `x-user-defined`, which is single-byte and maps
  every high byte to a character of its own, so it cannot combine bytes even in
  principle. The same three bytes read as at least ten different strings across those
  encodings, so the readings differ everywhere and the structure differs nowhere.

  `WithEncoding` says this now, `encodingstructure_test.go` gates it - along with the
  fact that makes the comparison possible, that source locations are byte offsets
  whatever the encoding - and `examples/gip/encodingmatrix` runs the same comparison
  over a caller's own corpus. B160.

- **A rewrite cannot convert a document's encoding, and a lost byte has two shapes.**
  `WithEncoding` said the output is in the document's encoding throughout, and left the
  consequence for a caller to work out. It is worth stating: there is no
  output-encoding option, and replacing every text chunk and every attribute with
  itself leaves the bytes exactly as they were - measured over windows-1252,
  iso-8859-2, shift_jis, euc-jp and gbk, each of which also round-trips byte for byte
  through a text handler. So a program that has to convert transcodes the bytes itself,
  and the rewriter is what proves the result: read both versions, compare what the
  handlers were given.

  The other half is the failure. A byte the decoder cannot use reaches the output as
  the three bytes of U+FFFD when the document is declared UTF-8, and as `&#65533;` -
  seven bytes where the document had one - in a legacy encoding, because U+FFFD is
  outside a legacy repertoire and a numeric reference is the documented fallback for
  that. Both only with a text handler registered; with none, the byte passes through
  either way.

  `WithEncoding` carries both now, and `legacyencoding_test.go` gates them along with
  the reference-in-source reporting and the insert-beyond-the-repertoire case. B159.

- **Invalid bytes are lossy to read everywhere and lossy to write only in text.**
  `ErrInvalidUTF8` said that the document path does not refuse bytes that are not valid
  UTF-8, and that they pass through unless a text handler is registered. Two things it
  did not say, both of which decide how a diagnostic tool has to be built.

  Reading is always lossy: every unit kind hands a handler U+FFFD rather than the byte,
  so no rewrite can see what the document actually held. Writing is lossy for text
  alone - measured over the five places a byte can sit:

      text, with a text handler          the output has U+FFFD
      raw text, with a text handler      the output has U+FFFD
      an attribute value, read           the bytes are kept
      a comment, read                    the bytes are kept
      a tag name, read                   the bytes are kept

  because those three are re-emitted from the source and text is re-emitted through the
  handler's path. And reading something else on the same element does not cost the
  text: an element handler that reads an attribute leaves a mis-declared text node
  alone.

  So a tool that reads text to diagnose a document's encoding is the pass that damages
  it, which is why `examples/gip/mojibake` writes to `io.Discard` unless told to fix,
  and why "this document is not UTF-8" can only be reported as "the text contains
  U+FFFD". `ErrInvalidUTF8` says this now and `invalidutf8_test.go` gates it. B158.

- **`html.UnescapeString` is not the parser's decoder for an attribute value.** Three
  places in the documentation say to decide on the decoded form and to get that form
  from the standard library. For text that is right. For an attribute value it is not,
  and the difference lands on URLs:

      <a href="?a=1&copy=2">

      a browser has          ?a=1&copy=2
      html.UnescapeString    ?a=1(c)=2

  A named reference without its semicolon is not a reference in an attribute when the
  character after it is `=` or ASCII alphanumeric - the rule the specification keeps
  for the URLs the web already had. Measured against x/net/html over `&copy=`,
  `&notit=`, `&amp=`, `&lt=` and `&noti`, all of which reach a browser unchanged and
  all of which the standard library decodes; with the semicolon, or at the end of a
  value, or before a character that is neither, the two agree. In text they agree
  everywhere.

  So a filter deciding on that decoded form is deciding about a URL nobody will
  request, and a rewrite that decodes, edits and re-encodes produces a different one.
  `Element.Attribute`, `EscapeText` and the package documentation now say so, and
  `differential/attrrefs_test.go` pins the table and the next-character rule. B157.

- **A rename changes how the element's existing content is parsed.** The
  documentation said that inserted content is not re-parsed, and that renaming a
  raw-text element turns its text into markup. The general case was missing:
  `SetTagName` writes over the tag and leaves the content alone, and whoever reads the
  output applies the new name's content model to it. Measured against x/net/html:

      <div><p>x</p></div>                 renamed to table   the p is fostered out
      <div><p>x</p><span>y</span></div>   renamed to select  both are gone, text merged

  No error, and the output is exactly the markup that was asked for - the div's tag
  became a table's tag and nothing else moved in the bytes. So a rename is safe when
  the new element accepts what the old one held, which is a question about the two
  content models rather than about the method.

  `SetTagName` and the "inserted content is not re-parsed" section say this now, and
  `differential/rename_test.go` gates it - including the eleven modernising renames
  that are safe, and the older fact that a rename writes over an implied end tag
  belonging to something else. B156.

- **`<image>` is a spelling of `<img>`, and a selector for `img` does not match it.**
  The parser renames one element, carrying the attributes over:

      <image src="x.png" onerror="alert(1)">

      in the tree   img src="x.png" onerror="alert(1)"
      here          TagName() == "image", and "img" matches nothing

  So a browser fetches the file and runs the handler, and every rewrite keyed on `img`
  has a hole in it - a sanitiser stripping event handlers, a URL rewriter, a
  mixed-content checker. Measured against x/net/html over five spellings, including a
  self-closing one and one with an explicit end tag, and confirmed the other way:
  center, font, marquee, blink, nobr, acronym, big, strike, tt, applet, keygen,
  isindex, spacer, menuitem, dir and basefont all reach the tree under their own
  names, so this is one alias rather than a habit.

  Matching both names needs a namespace check rather than a rename, because SVG has an
  image element of its own that keeps its name and is not an img at all.
  `Element.NamespaceURI` answers that, and `SetTagName("img")` is the tidiest fix for a
  rewrite that is editing the document anyway.

  New package documentation section, a note on `Element.TagName`,
  `differential/imagealias_test.go`, and B155.

- **Failing a rewrite from a handler is not atomic, and nothing said what the
  destination already holds.** `Write` and `ErrPoisoned` documented that a handler
  error stops the rewrite and poisons the Writer. What has gone out by then is the
  part a caller refusing a document needs to know, and it is not nothing:

      <p>a</p><p>b</p><p>c</p><img src="http://insecure/x.png"><p>d</p><p>e</p>

      the destination holds   <p>a</p><p>b</p><p>c</p>

  Identical at one Write of the whole document and at 64-, 16-, 4- and 1-byte
  writes. The prefix is a whole number of tokens - measured over an open tag, text,
  a list, a comment and a doctype - so it is well-formed markup, and a client
  reading it sees a short page rather than a failure.

  The two ends of the range are worth knowing too: a handler that fails on the
  document's first element delivers nothing at all, and one that fails in
  `OnDocumentEnd` has already delivered every byte, with the error surfacing only
  from `Close`. So a rewrite that must refuse a document decides as early as it can
  *and* holds its own output, forwarding only on success.

  `Writer.Write`, `ErrPoisoned` and the README's "two things to know" say this now,
  and `handlerfailure_test.go` gates it. B154.

- **Registration cost is per handler, not per selector clause.** The Cost section had
  the per-handler figure - about seven allocations per distinct selector - and said
  nothing about what a selector *list* costs, which is the shape a tool with a list of
  elements to look at wants. Measured over 500 elements that match nothing, so the
  numbers are registration and matching with no handler ever running:

      no handlers                              16 allocations
      one selector                             24
      a twelve-clause list                     24
      twelve separate registrations            96

  So naming twelve elements in one `OnElement` costs what naming one costs, and twelve
  `OnElement` calls cost eight times as much. The list is not free at match time - it
  measured slower per element than a single clause and faster than twelve
  registrations - but it allocates nothing extra.

  The section already advised the narrowest selector that says what the rule is, and
  compared a list against a `"*"` handler on a matching document. This is the other
  half: the list against separate registrations, on a document where nothing matches.
  `reportshape_test.go` gates it, along with the two facts a reporting tool relies on -
  a wide selector pays per element of the document and the gap grows with the page,
  and a named lookup that misses allocates nothing. B153.

- **Rewriting raw text inverts both rules for inserting into it.** The package
  documentation covered inserting into a `<script>` or a `<style>`: `Text` escapes
  three characters that raw text does not decode, so it corrupts the content, and
  `HTML` is refused when it would close the element. Rewriting text that is already
  there turns both of those around, and nothing said so.

      <style>.a > .b{color:red}</style>          Replace(s, Text)
      <style>.a &gt; .b{color:red}</style>       a selector that matches nothing

      <script>if (a < b && c > d) f()</script>   Replace(s, Text)
      <script>if (a &lt; b &amp;&amp; c &gt; d) f()</script>

  A `<title>`'s `&amp;` is escaped twice the same way. So `HTML` is the content type
  for editing a stylesheet or a script body - which reads backwards, and is the
  opposite of the advice for inserting new content into the same element - and the
  breakout guard does not cover that path, which is what `CheckRawText` is for.

  New paragraph in the package documentation's script-and-style section, notes on
  `TextChunk.Replace`, `TextChunk.Before` and `TextChunk.After`, and
  `rawtextrewrite_test.go` measuring all of it. B152.

- **Structural selectors count tokens, not children.** The engine pushes on a start
  tag and pops on an end tag, so it matches against the nesting the tokens describe.
  HTML lets a document leave most end tags out, and then that nesting is not the
  page's - the second list item is inside the first rather than beside it:

      <ul><li>a<li>b<li>c</ul>          with </li> spelled

      ul > li            1 of 3          3
      li > li            2               0
      li:first-child     all 3           1
      li:nth-child(2)    none            1
      li:nth-of-type(2)  none            1

  The tree has three items in one list either way, checked against x/net/html. So a
  rewrite keyed on position is right on the pages written one way and wrong on the
  pages written the other, and a document that closes some items and not others is
  partly right, which is harder to notice.

  It is not an off-by-one that could be corrected for: the count is of whatever the
  tokens nested. In `<ul><li><img><li><img></ul>`, `li:nth-child(2)` matches the
  second item as the second child of the first item, after the image - the same
  selector matching the same element for a different reason than it would on the
  closed form. Paragraphs, table cells, table rows and definition lists all behave
  this way, and paragraphs are the most common markup there is.

  New package documentation section with what to do instead - a counter in a handler,
  or a buffered first pass - a note in the supported-selectors list, and a
  correction to last turn's claim that the child combinator cannot be fooled: it
  cannot over-match, and it under-matches here. `differential/structural_test.go`
  gates every row. B151.

- **Handlers on one element share the element, and the documentation said the
  opposite.** The selectors section explained that matching is settled before any
  handler runs - an edit never changes which handlers fire - and then drew the wrong
  conclusion from it: "a rewrite cannot act on what another handler produced: that
  needs a second pass."

  It can, and this is how an asset URL gets hashed twice:

      OnElement("img[src]", ... SetAttribute("src", v+"?v=1")),
      OnElement("[src]",    ... SetAttribute("src", v+"?v=2")),

      <img src="/a.js">  ->  <img src="/a.js?v=1?v=2">

  Both selectors matched, both handlers ran in registration order, and each did its
  job on the other's output. Swapping the registrations swaps the result, so there is
  order-dependence in what comes out even though there is none in what fires. A
  renamed tag reads the same way: a later handler asking `TagName` gets the new name.

  What a later handler cannot do is *match* on the edit - a class an earlier handler
  added does not fire a `.new` selector, and a renamed tag does not fire a handler on
  the new name - so acting on produced markup still needs a second pass. That was the
  true half of the old sentence.

  Two smaller measurements came with it: removing an attribute and setting it again
  moves it to the end of the tag, and setting one and removing it again leaves the
  tag byte-identical.

  The package documentation and `Element.Attribute` now say this, and
  `handlerstate_test.go` gates it. B150.

- **`MaxMemory` is bounded by the biggest token a handler is given, not by the
  document.** The option already said that how much a document needs depends on how
  it is written, with one page needing 1024 in a single Write and 8192 in 256-byte
  writes. The rule underneath that was not written down, and it is simple: a token
  has to be copied when it straddles two writes, and only a token a handler is given
  is copied.

      one Write   256 B writes   64 B writes
      one 2012-byte tag, matched          5           2012          2012
      one 2012-byte tag, unmatched        5            260            68
      200 short tags (2800 B), matched    5            268            76

  So a document delivered in one Write needs almost nothing however long it is; an
  unmatched token costs nothing beyond the write size; a matched tag costs its whole
  length, exactly, at every write size small enough to split it. Text costs nothing
  at all, because it arrives in chunks - while a comment, which arrives whole, costs
  its length like a matched tag. And what the rewrite writes does not count: growing
  an attribute 64 times does not move the floor by a byte.

  The consequence is worth stating plainly, because it turns up as a bail-out in
  production rather than in a test: adding a handler can raise the limit the same
  pipeline needed before, with no change to the document. A rewrite matching the
  elements that carry `srcset` - the longest tags on most pages - is the usual way to
  meet it.

  `MemorySettings.MaxMemory` carries the table, `memoryfloor_test.go` gates every
  row, and it is B149.

- **An HTML tag name inside an `<svg>` ends the svg, and the library's two views of
  that disagree.** Foreign content is not a container the way an element is: the
  parser breaks out of SVG when it meets an HTML tag name, and everything after that
  tag is document content rather than image content.

      <svg><rect/><p>x</p><circle/></svg>

      in the tree   svg > rect, then p and circle beside the svg, both HTML elements

  Measured against x/net/html, 44 names do it - b, big, blockquote, body, br,
  center, code, dd, div, dl, dt, em, embed, h1 to h6, head, hr, i, img, li, listing,
  menu, meta, nobr, ol, p, pre, ruby, s, small, span, strong, strike, sub, sup,
  table, tt, u, ul, var - plus `font`, which breaks out only when it carries a
  color, face or size attribute. `title`, `desc`, `a`, `script`, `style`, `g` and
  `text` do not, so the list is a list rather than "HTML tag names".

  Then the disagreement, from the same document at the same moment:
  `NamespaceURI` follows the break-out and reports HTML for what comes after it,
  while the selector engine does not - `svg circle` and even `svg > circle` match a
  circle the tree puts outside the svg, because the engine pops its stack on end tags
  and this was a start tag. So neither answers "is this still inside the image", and
  a rewrite that needs to know has to look for the names itself.

  New package documentation section, a note on `Element.NamespaceURI`,
  `differential/foreign_test.go`, and B148.

- **A value is only source for the context it came from.** The escaping section said
  that a value taken from the document is already raw source, so building markup with
  it means not escaping it again, and `EscapeText` added that "text can usually be
  written through raw". Measured, that is the unsafe half of the advice.

  Each context lets through the character the other one ends on:

      <span title="<img src=x onerror=alert(1)>">   an attribute may hold a raw "<"
      <h2>a" onload=alert(1) x="b</h2>             text may hold a raw quote

  Both are inert where they sit - the tree has no img in the first and no attributes
  on the h2 in the second. Move the title into an element's text unescaped, which is
  the obvious way to turn an alt into a `<title>`, and the img is an element with a
  working onerror. Move the heading's text into an attribute unescaped and the new
  element gets an onload. Measured both directions against x/net/html by counting
  what the tree has, in `differential/context_test.go`.

  A move needs the destination's terminator escaped and nothing else: the `<` for
  text, the quote for a double-quoted attribute. That is what `Text` and
  `SetAttribute` apply, which is why an attribute value going back into an attribute
  can pass through unchanged, and it is why `EscapeText` and `EscapeAttribute` are
  for values that are not already source - on one that is, they escape its `&` a
  second time. The other answer, usually the better one, is not to move it: keep a
  name in an attribute rather than in a `<title>` child.

  The package documentation's build-markup section and `EscapeText` both say this
  now, and it is B147.

- **A wrapper is two insertions and the parser decides whether they wrap.** Putting
  a container around an element is `Before` with an opening tag and a closing tag at
  the element's end. Both insertions succeed, the output reads exactly as intended,
  and whether the result is a container is decided afterwards by whoever parses it.

      <p>text <iframe src="a"></iframe> more</p>

      wrapped in a div     p > "text", div > iframe, "more", p
      wrapped in a span    p > "text", span > iframe, "more"

  A div closes an open paragraph, so it takes the element out of the paragraph,
  leaves the text that followed it outside as well, and turns the source's `</p>`
  into a second, empty paragraph: three changes to the tree from an edit that only
  meant to add a container. A span does not - and cannot hold an element that closes
  a paragraph by starting, where it comes out empty with the element outside it:

      <p>text<pre>code</pre></p>

      wrapped in a span    p > "text", span (empty), then pre outside it
      wrapped in a div     p > "text", div > pre, p

  So the question is not whether the wrapper is a block or an inline element, it is
  whether the wrapped element closes a paragraph by starting. For a `<table>` that
  depends on the doctype, because a table closes a paragraph only outside quirks
  mode - the same document wraps one way with `<!DOCTYPE html>` and the other way
  without it. The doctype arrives before any element, so `OnDoctype` can decide it.

  And a wrapper around a table-internal element wraps nothing at all: it is fostered
  out to just before the table while the cell stays where it was.

  New package documentation section, a note on `Element.Before`,
  `differential/wrap_test.go`, and B146.

- **A descendant selector keeps matching after the ancestor has ended.** The
  documentation covered the end-tag rule for insertion positions - Append and After
  on an element whose end tag was omitted write somewhere else - and said nothing
  about the same rule deciding which handlers fire.

  The selector engine pops its stack on end tag tokens, and a start tag never pops
  anything. So for every element whose end tag HTML lets a document leave out, a
  descendant selector goes on matching:

      <ul><li><video><li><track></ul>

      "video track"  matches the track
      in the tree    the track is in the second item, with no video above it

  Measured against x/net/html over a second list item, paragraph, table cell, row,
  definition and option, in `differential/impliedclose_test.go`. The over-match runs
  until something explicit closes the ancestor - the enclosing `</ul>` ends it - and
  catches everything after it at any depth, so `li p` on a page written without
  `</li>` is a selector for the rest of the list.

  This is the worse half of the two: a position taken from a missing end tag is at
  least silent, and this one runs the handler on an element that is not there. The
  child combinator cannot be fooled the same way, because the start tag that ended
  the element is also the parent of whatever comes next - so where the thing being
  looked for can only be a child, `a > b` is both the more precise question and the
  safe one.

  Recorded on the end-tag section, in the supported-selectors list, and as B145.

- **A `<template>` is markup that is not on the page, and prepending into one can
  delete its content.** The package documentation said nothing about templates,
  which left two things to find out the hard way.

  The first is what fires. Handlers fire inside a template exactly as anywhere
  else, at any depth of nesting, and a descendant selector crosses the boundary -
  `template video` matches and so does a bare `video`. But the content is inert
  until a script clones it, so a match there is a rewrite of a blueprint: a report
  saying "6 videos" for a page with two and a carousel template is wrong twice
  over. The selector cannot tell you which side of the boundary you are on, so a
  program that cares needs a depth counter.

  The second is that the content parses by the template's own rules. Measured
  against x/net/html:

      <template><tr><td>x</td></tr></template>   template > tr > td > "x"
      <div><tr><td>x</td></tr></div>             div > "x": both tags dropped

  A `td` handler fires in both, so a handler call is not evidence that a cell
  exists. And a template inside a table is not fostered out, which sounds like the
  safe place to insert until you measure it:

      <table><template><tr><td>x</td></tr></template></table>

      Prepend("<input>", HTML)   table > template > input > "x"   the rows are gone
      Append("<input>", HTML)    table > template > tr > td > "x" > input
      Prepend("<!--c-->", HTML)  table > template > tr > td > "x"
      Prepend("hello", Text)     table > template > "hello" > tr > td > "x"

  The rows are parsed in a mode that the first inserted element ends, so prepending
  one throws them away - the parser's rule, not the insertion's fault, since the
  same bytes written by hand lose them too. It is the exact mirror of foster
  parenting: in a table the insertion moves and the content survives, in a template
  the insertion stays and the content can go.

  New package documentation section, a warning on `Element.Prepend`, and
  `differential/template_test.go` measuring all of it.

- **A graceful bail-out serves unrewritten input, and its rewritten prefix can be
  empty.** `MemorySettings.GracefulBailOut` said that the rewriter flushes what it
  has instead of discarding it, which sounds like a strictly better failure. It is
  not, and the reason is in the numbers. On a 2.6 KB document of 201 paragraphs fed
  in 64-byte writes, with a handler setting an attribute on each:

      MaxMemory 560     0 bytes    0 rewritten   default: nothing served
      MaxMemory 560    64 bytes    0 rewritten   graceful: one write, verbatim
      MaxMemory 900   5425 bytes 201 rewritten   no bail-out

  So at the limit that bails out on the first write, "flush what it has" is the
  input, unchanged - not a partly-rewritten document but a document the rewrite
  never touched, and the error that says so is returned either way.

  That decides the option by what the rewrite is for. A rewrite that adds
  something - a class, an analytics tag, a lazy-loading attribute - loses a feature
  when it bails out, and serving the input is the better failure. A rewrite that
  removes or neutralises something - a sanitiser, a CSRF token check, an `autoplay`
  attribute - is serving exactly the thing it existed to stop, to a client that has
  no way to know, and the truncated response is the safer failure.
  `GracefulBailOut` now carries the table and that decision; `gracefulbailout_test.go`
  gates it, including that a removing rewrite's flushed bytes still contain the
  `onclick` it was stripping.

- **An end-tag registration is held until the rewrite ends, and `MaxMemory` does
  not bound it.** `Element.OnEndTag` said what it does and nothing about what it
  costs. Measured: the live handle count climbs through five *closed* siblings -
  2, 3, 4, 5, 6 - and falls only when the Writer is closed, so the cost is one
  handle per matched element rather than per open element. On 100,000 sibling
  divs with a handler on `*` registering one each, that is 100,001 handles and
  about 30 MB of Go allocation, against about 6 MB for the same rewrite without
  the registration: roughly 300 bytes an element, and the same for a wide document
  as for a deep one.

  And the option that looks like a memory budget does not cover it. The same
  document completes under a 64 KiB `MaxMemory` while allocating those 30 MB,
  because that limit is lol-html's parsing buffer and this is the binding's handle
  table. `MemorySettings.MaxMemory` now says so - it caps lol-html's memory, not
  the rewrite's - and `OnEndTag` carries the measurement and the two mitigations:
  bound the input, and register only where an element needs it, deciding before
  registering rather than inside the callback.

  `endtagcost_test.go` pins the lifetime, that the cost is per matched element in
  both shapes of document, that `MaxMemory` does not refuse it, and that deciding
  first is an order of magnitude cheaper.
- **Foster parenting cuts both ways: an insertion can land outside the element it
  was put in.** The table section covered reading and removal - content a parser
  moves out of a table is reported inside it here, and removing the table removes
  it. The insertion direction was unsaid, and it is the one that bites a rewrite.
  Measured against golang.org/x/net/html, prepending `<input name="csrf">` to a
  form:

      <form method=post><p>x</p>            form > input        where it was put
      <table><tr><td><form method=post>      td > form > input   the same
      <table><form method=post><tr>          table > input       outside the form
      <table><tbody><form method=post><tr>   tbody > input       outside the form
      <select><form method=post>             body > input        outside everything

  The bytes say the field is inside the form and the tree says it is beside it. For
  a hidden field carrying a token, a nonce on a script, or anything whose position
  is the whole point, "the markup looks right" is not the test - and a rewrite that
  cannot tell those shapes apart should refuse the ones it cannot.

  The section now says so, and `differential/table_test.go` pins all five shapes
  through the oracle.
- **A doctype can be removed and never written, and `Remove` did not say what
  removing one costs.** `Doctype` has `Name`, `PublicID`, `SystemID`,
  `SourceLocation` and `Remove` - no `Before`, `After` or `Replace`, because
  lol-html offers none. So a rewrite that wants to turn a legacy declaration into
  `<!DOCTYPE html>` has to remove the old one and insert the new one before the
  first element, and that has three silent failure modes. Measured against
  golang.org/x/net/html:

      <!DOCTYPE …><html>…          upgraded
      <!DOCTYPE …><!--c--><html>…  upgraded: a comment before a doctype is allowed
      <!DOCTYPE …>   <html>…       upgraded: so is whitespace
      <!DOCTYPE …>text<html>…      the new one lands after text, where a parser
                                   ignores it: quirks mode
      <!DOCTYPE …>                 nothing to insert before: quirks mode
      <!DOCTYPE …>just text        the same

  And adding one without removing the old is not an alternative: a second DOCTYPE
  is a parse error and dropped, so the legacy declaration still applies. Which
  leaves the decision to remove being made before the place for the replacement is
  known, and nothing in the doctype handler can know it - a rewrite that must be
  right has to read the document twice or leave the declaration alone.

  `Doctype.Remove` now says what a missing doctype means, which is the largest
  rendering change a single token removal can make: quirks mode, where the box
  model, table cell heights and line heights all differ. `Doctype` carries the
  workaround and its failure table. `differential/doctype_test.go` measures all six
  shapes through the oracle, and that the second declaration is the one dropped.
- **Strict mode's trigger list was wrong for `<frameset>`, in the direction that
  matters.** `WithStrict` said eight names are ambiguous inside a `<select>` and
  that inside a `<frameset>` it is "any of them except `<noframes>`", that
  `<script>` is allowed, and that `<select>`, `<textarea>`, `<input>` and
  `<keygen>` end the ambiguous context. Measured by trying every element name in
  the HTML index inside each context:

      inside <select>    8 names: title style iframe xmp plaintext noembed
                         noframes noscript
      inside <frameset>  9 names: those minus noframes, plus script and textarea

  So two of the names the documentation calls safe are ambiguous in a frameset -
  `script`, which it says is allowed, and `textarea`, which it says ends the
  context. And the four context-ending tags end nothing there:
  `<frameset><select><title>` is ambiguous, while `<select><textarea><title>` is
  fine. Only `<noframes>` ends it in a frameset.

  It matters because of what the other mode does: with strict off, the region after
  an ambiguous tag is handed through as text with no handler invoked, which the
  same documentation calls a sanitiser bypass. A caller reading the list would have
  believed a `<frameset><script>` was seen and rewritten.

  `strict_test.go` now sweeps every name in the index against both contexts and
  four contexts that are not, so the two lists are measured rather than trusted -
  which is how this was found: the old test asserted the frameset list by removing
  one name from the select list, so the two extra names were never tried.
- **A selector list handles an element once; separate handlers each run.** The two
  spellings look interchangeable and are not, and nothing said so. Measured on
  `<a href="/x" class="t">` with a handler that appends to an attribute:

      OnElement(`a[href], a.t`, set)                     one call    data-n="x"
      OnElement(`a[href]`, set), OnElement(`a.t`, set)   two calls   data-n="xx"
      OnElement(`a`, set), OnElement(`a`, set)           two calls   data-n="xx"
      OnElement(`a[href], a.t, a`, set)                  one call

  A list is one selector, so an element matching several of its parts is handled
  once - two hundred copies of `a` in a list still give one call. That makes a list
  the way to say "at most once per element", which is usually what a rewrite wants,
  and it is also the cheapest form, which the cost section already measured.
  Separate handlers are for rules that really do each need their own call.

  The "Handler order" section now carries it, with the measured table.
  `selectorlist_test.go` pins the three shapes, the visible difference in the
  output, and the list-level errors: an invalid part fails the whole registration
  and the error names the whole list rather than the part, while an empty part -
  `a,,p`, `a, p,` - is refused with a message that for once means what it says.
- **A class or id that starts with a digit is a third escaping case, with the least
  helpful error.** The escaping section covered the colon and the dot. A digit is a
  different rule - a CSS identifier cannot begin with one - and lol-html reports it
  as "The selector is empty", which describes what its parser had left rather than
  what the caller wrote:

      #1a                     The selector is empty
      #\31 a                  matches id="1a"
      #\31a                   parses, matches nothing: \31a is U+031A, one character
      #\000031a               matches: six hex digits need no terminator
      [id="1a"]               matches, and needs no escaping at all
      .2xl\:hidden            The selector is empty: a digit and a colon at once
      [class~="2xl:hidden"]   matches that one

  Generated ids and utility class names land here regularly, and the missing space
  after `\31` is the quiet version of the mistake rather than a syntax error.

  `SelectorError` now carries both answers for a rejected selector whose class or
  id starts with a digit - the hex escape and the attribute-selector form - written
  plainly so they can be copied, next to the hint it already carried for an
  unescaped colon. `digitident_test.go` checks that every suggestion the error
  makes actually matches the element it is suggested for, which is the only test
  that makes a hint worth having.
- **An attribute selector with an empty operand matches most of the page, where
  CSS says it matches nothing.** The specification is explicit: a substring
  operator whose value is the empty string "does not represent anything". Measured,
  three of the six operators disagree:

      [a=""]     an empty value                        as specified
      [a|=""]    an empty value, or one starting "-"   as specified
      [a*=""]    nothing                               as specified
      [a^=""]    every non-empty value                 the specification says nothing
      [a$=""]    every non-empty value                 the specification says nothing
      [a~=""]    every value with no words in it       the specification says nothing

  Which matters because a selector is a string, and strings get interpolated: a
  rewrite that builds `a[href^="<prefix>"]` from configuration and is handed an
  empty prefix rewrites every link on the page rather than none. The `i` and `s`
  flags make no difference. An operand omitted altogether - `a[href^=]` - is a
  `SelectorError`, which is the one shape that fails loudly, and it is one keystroke
  from the shape that does not.

  The library cannot refuse it, because it does not parse selectors: lol-html does,
  and this is what it decides. What it can do is say so, and say the only thing
  that helps - check the value before building the selector.
  `emptyoperand_test.go` measures all six operators, both quotings, both flags and
  the omitted-operand refusal.
- **`WithEncoding` refuses four of the standard's encodings, and named one.** The
  documentation said "a non-ASCII-compatible encoding is refused" and listed the
  UTF-16 family. Measured across every canonical name in the WHATWG Encoding
  Standard's index, 36 of the 40 work and four do not:

      utf-16le  utf-16be   not ASCII-compatible, as documented
      iso-2022-jp          not ASCII-compatible, and not documented
      replacement          refused as unknown

  `iso-2022-jp` is the one that matters, because it is the one a real page
  declares. Its bytes look ASCII until an escape sequence switches the charset,
  after which they are not - so it cannot be rewritten in a stream either, and a
  caller pointing this at a Japanese page finds out from an error rather than from
  the documentation. `replacement` is a real label whose purpose is to decode
  nothing safely, and is reported as unknown rather than as incompatible.

  Two notes on label matching went in with it, for a caller passing a value straight
  from a Content-Type header: leading and trailing whitespace is stripped, so
  " utf-8 " works, and nothing else is normalised, so "utf_8" and "utf 8" are
  unknown labels while "iso8859-1" and "iso88591" are windows-1252.

  `encoding_test.go` walks the whole index, so the documented list cannot fall
  behind the library, and checks that each refusal gives the reason it should.
- **Registering a text handler is not free even if it does nothing, and the other
  handlers are.** That a text handler re-encodes undecodable bytes was documented,
  inside `ErrInvalidUTF8`, as a consequence of talking about invalid UTF-8. What
  was not said is that this makes "adding a read-only handler cannot change the
  output" false for exactly one kind of handler, and true for the rest:

      <p>caf\xe9</p>   no text handler             <p>caf\xe9</p>
                       a text handler, reading      <p>caf\uFFFD</p>
                       a text handler, ignoring     <p>caf\uFFFD</p>

      <p title="caf\xe9">text</p><!--caf\xe9-->   with all three kinds of
                       read-only handler registered: unchanged

  Measured both ways: the same undecodable byte survives in an attribute value and
  in a comment while a text handler is registered, and does not survive in text.

  It matters for instrumentation - a counter, an audit, a linter added to a rewrite
  that has to be byte-exact - and the answer where the output is a report rather
  than a document is to write the rewrite's output to `io.Discard`, where the
  question does not arise. `OnText` now says all of it.

  `readonlytext_test.go` measures five kinds of read-only handler over eight
  documents, and `properties/properties_test.go` gains the property over generated
  documents: a rewrite whose handlers only observe gives back the document it was
  given. That is stronger than the passthrough property next to it, which
  registers nothing at all.

  Also corrected in passing: `OnText` still said chunk boundaries "follow the
  writes", which the tokenizer half of that was fixed for in `TextChunk.Text`.
- **"Registering a few handlers with broad selectors and deciding inside them is
  cheaper than registering many narrow ones" is backwards.** The Cost section
  advised it, and the measurement says the opposite by an order of magnitude.
  Over a 2000-element page where about a tenth of the elements match:

      three narrow selectors, all matching     439 allocations
      one selector list "code,kbd,samp"        424
      one "*" handler with a switch          4,228

  and where nothing matches at all, fifty narrow selectors still win:

      fifty narrow selectors, none matching    351 allocations
      one "*" handler with a fifty-name set  4,031
      no handlers                               16

  The reason is that the two costs are not the same size. A selector that does not
  match costs matching, which is real and small; a handler that runs costs a unit
  wrapper and whatever it reads, and a broad selector makes it run for every
  element of the document rather than for every element the rule is about.

  The section now says so, with the numbers and the rule that follows: prefer the
  narrowest selector that says what the rule is, and where one handler has to
  cover several names, a selector list is both the cheapest and the clearest way to
  write it. A `*` handler is for when the rule really is about every element.

  `alloc_test.go` gates it as a ratio rather than as figures - what should not
  change is which side is cheaper - and `bench_test.go` has the three shapes so
  the numbers can be reproduced.
- **`SourceLocation` had one sentence, and it is the identity a two-pass rewrite
  runs on.** The documentation recommends reading a document twice wherever a
  decision needs what comes later, and a byte range is what joins the passes - so
  the four things a caller needs from it are worth stating:

      the offsets index the bytes fed to the rewriter, before decoding or
      transcoding: under WithEncoding a chunk reporting "café" has a four-byte
      range, so slicing the input works and measuring the reported string does not

      an element's range is its start tag and nothing of its content; an end tag's
      is the token that closed the element, which every element that token closed
      reports identically; a comment's and a doctype's cover the whole token

      a range can be empty: the final chunk of a text node stands at the point
      where the node ended, which is how to find a text node's extent - first
      chunk's Start to last chunk's End

      a replacement character produced for a byte that could not be decoded can
      stand at a point too, so the length of the reported text and the length of
      the range are unrelated numbers

  `sourcelocation_test.go` measures all of it, plus the guarded extent recipe and
  a two-pass rewrite finding its own elements again. That the offsets are absolute
  and chunk-invariant was already measured in `sourceloc_test.go`.
- **Source is not only undecoded, it is unpreprocessed.** "Character references
  are not decoded" covers the well-known half of what "raw source" means. HTML also
  normalises bytes before the tokenizer sees them, and a rewriter that re-emits
  what it read cannot do that and still be a rewriter - so four more things differ
  between what a handler is handed and what a parser has. Measured against
  golang.org/x/net/html:

      a CR or a CRLF                    is a LF to a parser, in text, in a comment
                                        and in an attribute value
      a NUL in element content          is dropped by a parser
      a NUL in raw text or a comment    is U+FFFD
      a NUL in an attribute value       is kept
      one leading LF in <pre>,
      <listing> or <textarea>           is dropped - one of them, not all

  Every one of them is reported here as written. Two consequences: a comparison
  against a value that came from a browser, a DOM or another parser can fail on
  bytes neither side chose, and a value written into an attribute is read back
  changed if it holds a CR - `&#13;` is how to write one that survives.

  This also corrects a claim on `ContentType` `Text`, which said a NUL in inserted
  content "any parser reading the result replaces it with U+FFFD". That is true in
  raw text and in a comment, and wrong in the two commonest places: element content
  drops it and an attribute value keeps it. It still does not survive a round trip,
  which was the point of the sentence.

  `differential/preprocess_test.go` measures all of it, including the reassuring
  half: a rewrite that only copies values around is exact, because both sides are
  source.
- **"Case-insensitively" means ASCII, and three places said it without the
  qualifier.** The selector section, the case-flag paragraph and
  `Element.TagName` all described folding as though it applied to a name; HTML
  folds the ASCII letters of a name and leaves everything else, which is only the
  same thing for an ASCII name.

      <DÉTAIL>   matched by "DÉTAIL", "dÉtail", "DÉtail"
                 not matched by "détail" or "DéTAIL"

  Both sides are folded, so the É has to match in case. `TagName` reports
  `dÉtail` - a spelling nobody wrote - and the selector a caller would reach for,
  all lower case, matches nothing, with no warning: a selector that matches no
  element is a valid selector. Custom element names may contain non-ASCII letters,
  so a template written in a language with accents reaches this without trying.

  The `i` flag is the sharpest case, because it is what a caller reaches for when
  case matters: `[data-x="é" i]` does not match `data-x="É"`, while
  `[data-x="café" i]` does match `CAFé` - most of the value folds the way it
  looks like it should, which is what makes the failure quiet.

  Measured with it: a tag name has to *begin* with an ASCII letter to be a tag at
  all, so `<ÉTAT>` is text and no selector reaches it; and the HTML
  case-insensitive attribute list is no exception - `rel` folds `CANONICAL` and
  not `CANONICÁL`. `asciicase_test.go` holds all of it, including the way round:
  a wide selector and `strings.EqualFold`, which matches both spellings where no
  single selector can.
- **Whether a handler can tell it is inside a removed element depends on which
  handler it is.** The removal section said that handlers keep running over
  removed content and that "a handler that accumulates ... has to notice for itself
  that the content it is looking at is on its way to being dropped.
  `Element.IsRemoved` is how an element handler checks" - and stopped there,
  leaving the two things a caller needs unsaid.

  Measured, both of them:

      an element handler   IsRemoved is TRUE for a descendant of an element
                           removed with Remove or Replace - no depth counter needed
      a text handler       TextChunk.IsRemoved is FALSE: it reports the chunk's
                           own removal and nothing else
      a comment handler    the same

  So the good news is better than the documentation implied - an element does not
  have to track anything, and `RemoveAndKeepContent` correctly reports false
  because the content is being kept - and the gap is real: the handler that most
  often accumulates is the one that cannot ask. The package documentation now
  carries both, with the depth-counter recipe a text handler needs, which works
  because an end-tag handler still runs for an element inside a removed one.

  Also measured and now stated: an insertion made anywhere inside a removed
  subtree is discarded - every position, from a grandchild, and from a text
  handler - so the documented "insert first, remove last" hazard is about one
  element and its own handlers rather than about a subtree.

  `Element.IsRemoved`, `TextChunk.IsRemoved` and `Comment.IsRemoved` each say what
  they answer for. `removedsubtree_test.go` measures all of it, including the
  recipe on a document whose removed element has no end tag of its own.
- **Chunk boundaries do not only follow the writes.** `TextChunk.Text` said "where
  the chunk boundaries fall is not a caller's choice - they follow the writes,
  which follow whatever reader is upstream", which reads as a promise that
  controlling the writes controls the chunking. The tokenizer splits a text node
  as well, at a `<` that turns out not to begin a tag, and hands that character
  over as a chunk by itself:

      <p>3 < 4 and 5 < 6</p>    "3 "  "<"  " 4 and 5 "  "<"  " 6"  ""

  Six chunks for one text node, from one write. So prose with a bare `<` in it -
  arithmetic, a code sample outside a `<code>` element - arrives in more pieces
  than a caller sizing the work by writes would expect, and the Cost section's
  "a text handler sees two chunks per text node" is a floor rather than a figure.

  Both now say so. Measured with it: a `&lt;`, a `&`, a NUL and a CRLF split
  nothing; `<!`, `</` and `<?` end the text node instead, each of them beginning a
  comment token; and length alone never splits a node, so 1 MB of text in one
  write is still two chunks. The advice does not change - accumulate to
  `IsLastInTextNode` - but the reason it is not optional does.
  `textchunks_test.go` holds the measurements, and `chunkboundary_test.go`'s
  header no longer says the writes are the whole story.
- **`SetAttribute` lower-cases a name it is adding, and keeps the document's
  spelling when it is updating.** Which means an SVG attribute can be changed and
  cannot be introduced:

      <svg viewBox="0 0 1 1">   SetAttribute("viewBox", "0 0 9 9")  ->  viewBox="0 0 9 9"
      <svg>                     SetAttribute("viewBox", "0 0 9 9")  ->  viewbox="0 0 9 9"

  In HTML that is nothing - names are matched case-insensitively, which is why
  `Attributes` lower-cases them. In SVG and MathML the case is part of the name, so
  the second line is an attribute a browser ignores, silently, on the path that is
  hardest to test: a rewrite that reads a `viewBox`, computes a new value and writes
  it back works, and the same code on an element that did not have one does not.

  The library already takes care over this in the other direction -
  `Attribute.NamePreserveCase` exists and its comment names `viewBox` - so the
  write side quietly undoing it is worth a paragraph. `SetAttribute` now has one,
  with the workaround (rebuild the tag, or carry the attribute in the source), and
  the measured name rules: a space, a tab, a newline, `/`, `=`, `>` and the empty
  name are refused, so a name taken from a document cannot break the tag it is
  written into, while the merely odd - a quote, a `<`, a leading digit - is
  accepted and reads back as itself. `SetTagName` is stricter and different again:
  it keeps the case it is given and wants an ASCII letter first.

  `attrnamecase_test.go` measures all of it.

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

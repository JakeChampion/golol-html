# Changelog

## Unreleased

### Added
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

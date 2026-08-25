# Changelog

## Unreleased

### Added
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

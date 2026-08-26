// Package lolhtml provides Go bindings for lol-html, Cloudflare's streaming
// HTML rewriter.
//
// The rewriter walks HTML in a single pass, without building a document tree,
// and invokes your handlers as it encounters content matching a CSS selector.
// Memory use is bounded by the largest element it has to buffer rather than by
// document size, which makes it suitable for rewriting responses of unknown
// length on the fly.
//
// # Streaming
//
// [NewWriter] returns an [io.WriteCloser], so a rewrite composes with the rest
// of the io package:
//
//	w, err := lolhtml.NewWriter(os.Stdout,
//		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
//			href, _ := e.Attribute("href")
//			return e.SetAttribute("href", absolutise(href))
//		}),
//	)
//	if err != nil {
//		return err
//	}
//	if _, err := io.Copy(w, resp.Body); err != nil {
//		return err
//	}
//	return w.Close()
//
// Close finishes the document and flushes the tail of the output; skipping it
// truncates the result. Chunk boundaries never affect handler behaviour, so
// input may arrive however the network delivers it.
//
// For a document already in memory, [Rewrite] and [RewriteString] wrap the same
// machinery.
//
// # One rewriter per document, not one per fragment
//
// The guarantee above is about chunks of one stream, and it does not extend to
// splitting the work. Rewriting two fragments with two rewriters and joining the
// outputs is not the same as rewriting the whole, because a fragment that ends
// inside a tag is invisible to every handler and is emitted verbatim.
//
// Measured on <p>a</p><script>alert(1)</script><p>b</p>, removing every script,
// over all forty places the document can be cut in two:
//
//	one rewriter, whole document        saw p script p    <p>a</p><p>b</p>
//	one rewriter, two writes, any cut   saw p script p    <p>a</p><p>b</p>
//	two rewriters, cuts 9 to 15         saw p and p       the script, whole
//	two rewriters, cut 16               saw p script, p   alert(1) as text
//	two rewriters, any other cut        the script is removed
//
// The seven cuts are the ones strictly inside the eight bytes of <script>. The
// first fragment ends mid-tag, so no element handler and no text handler runs for
// it and the bytes pass through; the second begins mid-name, so its remainder is
// text and passes through too; and the join reassembles an element that neither
// pass inspected. Cut 16 is the boundary immediately after the complete start tag
// and fails differently: the first pass does remove the script, but its content
// was in the other fragment, so the payload survives as text beside a stray end
// tag.
//
// Two different things are going on, and it is worth keeping them apart.
//
// The first is that a tag is the only thing a document can end inside and have
// nothing report it. Measured, on inputs that end where they say:
//
//	<p    <p attr    <p attr="v    <p/    </p    <script
//	                                     no handler of any kind
//	<!-  <!--  <!-- x  <!  <?php  <![CDATA[x
//	                                     a comment, with the text so far
//	<!DOCTYPE                            a doctype
//	<script>  <script>var a  <style>p{   the element, and its text if any
//
// So the blind spot is exactly the tag, and its bytes still reach the output. A
// stray end tag - </div> with nothing open - is unreported too, and harmless: it
// swallows nothing.
//
// The second is which unfinished constructs swallow what follows them, which is a
// wider set and the one that decides whether a join is safe. Appending
// <x-sentinel> to each of these and asking whether the element is still reported:
//
//	<p   <p attr   <p attr="v   </p         swallowed
//	<!-- c   <!   <?php   <!DOCTYPE         swallowed
//	<script>   <script>var a   <style>p{    swallowed
//	<textarea>x   <title>t                  swallowed
//	<ul><li>a   <p>a   </div>   a < b       reported: nothing is open that absorbs
//
// which is the same set the documentation for [DocumentEnd.Append] describes for
// the same reason: an unfinished construct has no end until one arrives, and the
// next thing written becomes part of it. An *element* left open is not one of
// them - more content is simply more content.
//
// So a fragment is safe to join if nothing is unfinished at its end. An open
// element is fine; an open tag, comment, doctype or raw-text element is not.
//
// No call errors in any of this, and the output looks like a document. For a
// sanitiser that is a hole, for an inventory an undercount, and for a rewrite that
// only adds things a corruption.
//
// So a document assembled from pieces should be fed to one rewriter as successive
// writes - which the second row shows is correct at every boundary - and a rewrite
// that must work on fragments has to be able to say where a fragment may be cut.
// Element boundaries are safe; byte offsets are not. examples/gip/inventory
// measures this and examples/gip/split cuts only at boundaries it chose.
//
// A caller who has to accept fragments from elsewhere can test one rather than
// trust it, and without reimplementing the tokenizer: append a sentinel element to
// the fragment, rewrite it, and see whether a handler for the sentinel runs. If it
// does not, something at the end of that fragment swallowed it.
//
// Asking the same question by scanning for a "<" after the last ">" does not work,
// and it fails in the direction that matters: over a fixed set of 4000 generated
// fragments it says "safe" for 1007 that are not, and never the other way. It
// cannot know that <!DOCTYPE does not begin with a letter, that an open <script>
// has its last ">" behind it, or that a bare "</" at the end is an unfinished end
// tag. Pinned in fragment_test.go.
//
// # An insertion can only go where the rewriter has not been yet
//
// The output is produced as the input is consumed, so a handler can insert
// content only at a position the rewriter has not passed. That is obvious said
// plainly and easy to walk into, because it constrains the shape of a rewrite
// rather than any single call: whatever a rewrite decides has to be decided
// before the position it wants to write to.
//
// Some things are therefore not one-pass rewrites at all. Head content derived
// from the body is the common one - a rel=next link from a pagination nav, a
// canonical URL from the page's own content, a table of contents built from the
// headings - because the head has closed before the evidence arrives. Nothing
// reports this: the handler that would insert simply never runs, or runs before
// it knows what to say.
//
// Where the evidence and the position both exist, pick the position that is
// after every place the evidence could be. That is usually an end tag rather
// than a start tag, and the choice is not cosmetic: a program that decides at
// the first element what to insert cannot know about a second candidate further
// on, and the usual result is inserting and then also rewriting - two of the
// thing it was meant to leave one of.
//
// Where they do not both exist, the answer is two passes, and the cost is worth
// knowing before choosing it. A second pass roughly doubles the allocation count,
// at every size:
//
//	elements   one pass   two passes
//	       2         24           52
//	      40         45           98
//	     800        425          866
//
// The ratio holds at about two everywhere, and the reason is not that the fixed
// cost of building a rewriter dominates - at 800 elements it plainly does not.
// It is that the second pass re-parses the whole document and runs every handler
// again, so the per-element work doubles along with the fixed part. Two passes
// over a large document are not cheap because the overhead is amortised; they
// cost twice.
//
// What grows with it is memory - but only for the kind of second pass that needs
// what the first pass learned. A table of contents or a canonical URL derived
// from the body has to be complete before the second pass starts, so the document
// is held, and that is what stops it being a streaming rewrite.
// examples/gip/pagenav and examples/gip/glossary both do this, with the second
// pass behind a flag or skipped entirely when the first pass found nothing to do.
//
// A second pass that is just another rewrite needs none of that. A [Writer] is an
// io.Writer, so it can be another Writer's destination:
//
//	second, _ := lolhtml.NewWriter(dst, annotate...)
//	first, _  := lolhtml.NewWriter(second, insert...)
//	io.Copy(first, src)
//	first.Close()   // upstream first: its tail flushes into second
//	second.Close()
//
// Both stages run at once, the downstream one seeing bytes before the upstream one
// has been given the whole document, and neither holds it: measured, peak heap
// above the baseline was 2.8 MB piping 1 MB and 3.5 MB piping 4 MB and 16 MB. The
// allocation cost is the same doubling as the buffered form - 831 for one pass
// over 400 anchors, 1645 piped, 1655 buffered - and the output is identical, so
// the pipeline is the buffered form without the document.
//
// Two things to know about the shape. Close upstream first, because each stage's
// Close flushes into the next: the wrong order truncates the tail and reports
// [ErrClosed] from the upstream Close, on a document that has a tail. And an error
// in any stage reaches the caller through the stages above it with its identity
// intact, so a pipeline needs no error plumbing of its own. examples/gip/pipeline
// is the whole thing in one file; pipeline_test.go gates it.
//
// Gated as a ratio in alloc_test.go, since the absolute numbers move with the
// toolchain and the doubling does not.
//
// # An end tag is a token, not a fact about the element
//
// HTML lets a document leave many end tags out. A list item is closed by the
// next list item, a table cell by the next row, a paragraph by anything that
// cannot be inside one. In a browser's tree those elements are closed exactly
// like any other. Here there is no tree, and an element ends where the next end
// tag token is - which, for an element whose own end tag was omitted, is the
// enclosing element's.
//
// So the element the library hands a handler is bigger than the element the page
// describes, and everything positioned at its end goes somewhere else. Measured
// on <ul><li>a<li>b<li>c</ul>, one operation applied to every item:
//
//	Prepend  <ul><li>[1]a<li>[2]b<li>[3]c</ul>   correct
//	Before   <ul>[1]<li>a[2]<li>b[3]<li>c</ul>  correct
//	Append   <ul><li>a<li>b<li>c[1]</ul>        one survives, at the end of the list
//	After    <ul><li>a<li>b<li>c</ul>[1]        one survives, outside the list
//	SetInner <ul><li>[1]</ul>                   items b and c are gone
//	Replace  <ul>[1]                            every item is gone
//
// The one exception is inserting through the end tag rather than through the
// element. All three handlers run at the single `</ul>`, innermost first, and
// every insertion survives:
//
//	EndTag.Before  <ul><li>a<li>b<li>c[3][2][1]</ul>   all three, at the end of the list
//
// The position is no more correct than Append's - the content belongs at each
// item's own end, and the source has no such position - but nothing is silently
// dropped. For a rewrite whose whole job is to add something, that is the
// difference between a misplaced insertion and a missing one, and it is the
// reason to prefer [EndTag.Before] over [Element.Append] where either would do.
// examples/gip/shadow is built on it.
//
// And [Element.Remove] on the *first* item alone empties the whole list:
//
//	<ul><li>a<li>b<li>c</ul>  ->  <ul>
//
// No call returns an error and nothing in the output looks damaged, which is
// what makes this the worst trap in the library. The same program on
// <ul><li>a</li><li>b</li><li>c</li></ul> is correct in every row, so it works
// on the pages written one way and destroys content on the pages written the
// other.
//
// Positions taken from the start tag are safe: Prepend, Before, SetAttribute and
// anything read from the element. Positions taken from the end are not.
//
// [Element.OnEndTag] has the same shape, and it is the one place the mismatch
// can be detected. The handler still runs - against the tag that closed the
// element, which has a different name:
//
//	tag := e.TagName()
//	e.OnEndTag(func(t *lolhtml.EndTag) error {
//		if t.Name() != tag {
//			return nil // closed implicitly; this position is not this element's
//		}
//		return t.Before("<span class=\"marker\"></span>", lolhtml.HTML)
//	})
//
// An end tag closes the nearest open element of its name, which is the element
// itself, so a name that matches is this element's end tag and a name that
// differs is not. Measured against what the source spells at that position, over
// every shape in endtagposition_test.go.
//
// If nothing closes the element - <p>a<p>b at the top level - the handler does
// not run at all, and Append and After produce nothing. That is at least a
// silence rather than a wrong answer.
//
// What to do about the other branch is the rewrite's decision. Doing nothing is
// honest. Where the rewrite must be right on both kinds of page, the answer is
// the same as everywhere else evidence arrives too late: read the document
// twice, and let the first pass find out which elements have their own end tags.
//
// Writing is not the only thing that acts on that token. Removing an element
// removes it - [Element.Remove] and [Element.Replace] take the content up to it
// as well, and [Element.RemoveAndKeepContent] takes the token alone, so the
// element it belonged to never closes. Renaming an element writes over it, so
// <h1>a <em>b</h1> renamed to i becomes <h1>a <i>b</i>, with the heading left
// open. Each of those methods says so; the general rule is this one, and the name
// guard above is what detects it in all of them.
//
// The same token rule decides which handlers fire, not only where content goes. The
// selector engine pops its stack on end tags, and a start tag never pops anything,
// so a descendant selector goes on matching after the element has ended:
//
//	<ul><li><video><li><track></ul>
//
//	"video track"  matches the track
//	in the tree    the track is in the second item, with no video above it
//
// Measured over a second list item, paragraph, table cell, row, definition and
// option in differential/impliedclose_test.go. The over-match runs until something
// explicit closes the ancestor - the enclosing </ul> ends it - and catches
// everything after it at any depth, so "li p" on a page written without </li> is a
// selector for the rest of the list. This is the worse half of the two: a position
// taken from a missing end tag is at least silent, and this one runs the handler on
// an element that is not there.
//
// The child combinator cannot be fooled into over-matching this way, because the
// start tag that ended the element is also the parent of whatever comes next. Where
// the thing being looked for can only be a child, "a > b" is the more precise
// question - examples/gip/captions asks "video > track" for that reason. It fails the
// other way instead, under-matching when the omitted end tag is the one being
// selected through: see the next section. Where neither combinator will do, the
// answer is the caller's own stack of open elements with the implied end tags
// applied.
//
// A handler that only wants to know the element is over, rather than to write at
// its position, needs a finer distinction than the name gives. A foreign end tag
// is where the element ended when an ancestor's end tag closed it, and later than
// where it ended when a sibling's start tag did - in <ul><li><em>a<li>b</ul> the
// em's callback arrives after "b" has been reported. Nothing in the callback
// separates those two, so anything accumulating has to keep the stack of open
// elements itself and apply the implied end tags. See [Element.OnEndTag], and
// examples/gip/markdown for what that costs.
//
// # Structural selectors count tokens, not children
//
// The selector engine pushes on a start tag and pops on an end tag, so the tree it
// matches against is the nesting the tokens describe. HTML lets a document leave most
// end tags out, and then the nesting is not the page's: the second list item is inside
// the first rather than beside it. Every selector that depends on position or
// parentage answers a different question from the one it looks like:
//
//	<ul><li>a<li>b<li>c</ul>          with </li> spelled
//
//	ul > li            1 of 3          3
//	li > li            2               0
//	li:first-child     all 3           1
//	li:nth-child(2)    none            1
//	li:nth-of-type(2)  none            1
//
// So a rewrite keyed on position is right on the pages written one way and wrong on
// the pages written the other, and a document that mixes the two - some items closed,
// some not - is partly right, which is harder to notice. Measured over that list, over
// a list whose items hold an element, and over paragraphs, table cells, table rows and
// definition lists, in differential/structural_test.go.
//
// It is not an off-by-one that could be corrected for. The count is of whatever the
// tokens nested, so in <ul><li><img><li><img></ul> the selector "li:nth-child(2)"
// matches the second item - as the second child of the first item, after the image.
// The same selector on the same list with its end tags matches the same element for
// the right reason. Nothing in the callback distinguishes those two.
//
// What to do about it depends on what the position was for. Numbering, striping and
// "every third" want a counter in a handler, incremented per match, which counts the
// elements the rewrite actually sees; that is what examples/gip/shard uses a hash for
// instead, needing stability rather than position. Selecting the *first* of something
// is the one position that survives, since ":first-child" over-matching still includes
// the element a page would call first. And a rewrite that must be exact about position
// on documents it did not write has the same answer as everything else here: buffer
// the input, and let a first pass find out what the tree is.
//
// # Handler lifetime
//
// The value passed to a handler is valid only until that handler returns.
// lol-html reuses the underlying storage, so golol-html detaches the wrapper on
// the way out and every later method call returns [ErrDetached]. Copy out what
// you need rather than retaining the unit:
//
//	lolhtml.OnElement("img", func(e *lolhtml.Element) error {
//		src, _ := e.Attribute("src")   // fine: a Go string
//		seen = append(seen, e)         // useless: detached once this returns
//		return nil
//	})
//
// # Handler order
//
// More than one handler can see the same unit, and the order they run in
// follows two rules.
//
// Within one kind of registration, handlers run in the order they were
// registered: two [OnElement] handlers whose selectors both match, three
// [OnDocumentEnd] handlers, several [Element.OnEndTag] handlers on one element.
// Each sees what the previous one did, so a handler reading an attribute gets
// the value an earlier handler wrote to it.
//
// How many times a handler runs on one element is decided by how the rules were
// spelled, and the two spellings differ. A selector list is one selector: the
// handler runs once for an element, however many parts of the list match it.
// Separate handlers are separate: each runs. Measured on
// <a href="/x" class="t">, with a handler that appends to an attribute:
//
//	OnElement(`a[href], a.t`, set)              one call    data-n="x"
//	OnElement(`a[href]`, set), OnElement(`a.t`, set)   two calls   data-n="xx"
//	OnElement(`a`, set), OnElement(`a`, set)    two calls   data-n="xx"
//	OnElement(`a[href], a.t, a`, set)           one call
//
// So merging rules into one list is the way to say "at most once per element",
// which is usually what a rewrite wants and is also the cheapest form - see the
// section on cost. Keep them separate when each rule really does need its own
// call. Pinned in selectorlist_test.go.
//
// Selectors do not. Matching is decided against the document as it arrived,
// before any handler runs, so an edit never changes which handlers fire:
//
//	OnElement(".a", func(e *Element) error { return e.SetAttribute("class", "b") }),
//	OnElement(".b", ...)   // does not fire
//
// and neither does renaming a tag, in either registration order. The reverse
// holds too: removing the class an already-matched selector needed does not
// un-fire it, so a handler on ".a" still runs even if an earlier handler took the
// attribute away.
//
// That is worth relying on: there is no cascade and no way for a rewrite to trigger
// itself, so the set of handlers that will run is fixed by the document before any
// of them does anything.
//
// What they read is a different matter. Handlers on one element share the element,
// and a later one sees an earlier one's edits - its attribute values and its tag
// name:
//
//	OnElement("img[src]", ... SetAttribute("src", v+"?v=1")),
//	OnElement("[src]",    ... SetAttribute("src", v+"?v=2")),
//
//	<img src="/a.js">  ->  <img src="/a.js?v=1?v=2">
//
// Both selectors matched, both handlers ran in registration order, and each did its
// job on the other's output. Swapping the two registrations swaps the result, so
// there is order-dependence in what comes out even though there is none in what
// fires. Two selectors that can match the same element and write the same attribute
// are the shape to watch for: examples/gip/bust uses one handler and one selector
// list for exactly that reason.
//
// What a later handler cannot do is match on the edit - a class an earlier handler
// added does not make a ".new" selector fire, and a renamed tag does not make a
// handler on the new name fire, though the later handler sees the new name when it
// asks. Acting on produced *markup* still needs a second pass. Measured in
// handlerstate_test.go.
//
// Between kinds, every selector-associated handler runs before every
// document-level handler for the same unit, whatever order the options were
// written in. [OnComment] runs before [OnDocumentComment] and [OnText] before
// [OnDocumentText] even when the document-level one was registered first,
// because lol-html keeps the two in separate lists. A rewrite that needs to see
// a unit before anything else does has to register a selector-associated
// handler, not a document-level one.
//
// # Which selectors are supported
//
// One rule covers almost all of it: a selector can be used if the rewriter can
// decide it when it sees the start tag. It has no tree to look at and it cannot
// wait, so anything that depends on what comes after the element is out.
//
// Supported:
//
//	div  *  .cls  #id                  type, universal, class, id
//	a, b                               a selector list
//	div p     div > p                  descendant and child combinators, though a
//	                                   descendant one keeps matching after an
//	                                   omitted end tag: see end tags above
//	[a]  [a=v]  [a~=v]  [a|=v]         attribute presence and matching
//	[a^=v]  [a$=v]  [a*=v]
//	[a=v i]   [a=v s]                  case-sensitivity flags
//	:not(x)                            one simple selector only, and no combinator
//	                                   inside it at all, see below
//	:first-child  :nth-child(2n+1)     odd, even and an+b all work, over the
//	                                   nesting the tokens describe: see structural
//	                                   selectors above
//	:first-of-type  :nth-of-type(n)
//	*|name                             any namespace
//
// Not supported, because deciding them needs what follows the start tag:
//
//	:last-child  :only-child  :empty
//	:last-of-type  :nth-last-child(n)  :nth-last-of-type(n)
//
// Not supported for other reasons - state a stream does not have, or simply
// unimplemented:
//
//	x + y   x ~ y                      sibling combinators
//	:root  :scope  :host
//	:checked  :disabled  :hover
//	:is(...)  :where(...)  :has(...)
//	::before  ::first-line  ::marker    any pseudo-element
//	ns|name                            an explicit namespace other than *|
//
// An empty operand does not mean what CSS says it means, and this is worth
// knowing before a selector is built from a string that might be empty. The
// specification says a substring operator with an empty value "does not represent
// anything" - matches nothing at all. Measured here, three of the six do something
// else:
//
//	[a=""]     an empty value                    as the specification says
//	[a|=""]    an empty value, or one starting "-"   as the specification says
//	[a*=""]    nothing                           as the specification says
//	[a^=""]    every non-empty value             the specification says nothing
//	[a$=""]    every non-empty value             the specification says nothing
//	[a~=""]    every value with no words in it   the specification says nothing
//
// So a rewrite that interpolates a prefix into a[href^="..."] and is handed an
// empty prefix does not match nothing, it matches every link on the page. The i
// and s flags make no difference, and an operand omitted altogether -
// a[href^=] - is a [SelectorError], which is the one shape that fails loudly.
//
// The library does not refuse the empty operand because it does not parse
// selectors - lol-html does, and this is what it decides. Check the value before
// building the selector, which is a line of code and the only thing that helps.
// Measured in emptyoperand_test.go.
//
// Tag and attribute names are matched case-insensitively, so "LI" and "li" are
// the same selector and [CLASS=a] matches class="a". An attribute selector
// matches a present-but-empty attribute: [style] matches style="".
//
// Case-insensitively means ASCII, which is what HTML means by it. Both the name
// and the selector have their ASCII letters folded and everything else left alone,
// so a non-ASCII letter has to match in case:
//
//	<DÉTAIL>   matched by "DÉTAIL", "dÉtail", "DÉtail"
//	           not matched by "détail" or "DéTAIL"
//
// The É is the same character on both sides or it is not the same selector. So
// <DÉTAIL> is the element "dÉtail" - see [Element.TagName] - and the spelling a
// caller would reach for, all lower case, matches nothing. Nothing warns: a
// selector that matches no element is a valid selector. Custom element names may
// contain non-ASCII letters, so this is reachable from a template written in a
// language that has accents rather than only from a deliberately odd document.
//
// A tag name still has to *begin* with an ASCII letter to be a tag at all:
// "<ÉTAT>" is text, not an element, so no selector reaches it.
//
// Attribute values are a different rule, and it is not uniform. HTML matches the
// value case-insensitively for a fixed list of attributes and case-sensitively
// for everything else, and the rewriter follows that list exactly:
//
//	[rel="canonical"]  matches rel="CANONICAL"
//	[name="foo"]       does not match name="Foo"
//
// The list is the one in the HTML specification's section on selector
// case-sensitivity, all 46 of them:
//
//	accept accept-charset align alink axis bgcolor charset checked clear
//	codetype color compact declare defer dir direction disabled enctype face
//	frame hreflang http-equiv lang language link media method multiple nohref
//	noresize noshade nowrap readonly rel rev rules scope scrolling selected
//	shape target text type valign valuetype vlink
//
// Everything else is matched exactly, including id, class, href, src, alt,
// title, name, value, style, content, role, srcset, integrity and every data-*
// attribute. So are the .cls and #id shorthands: ".Foo" does not match
// class="foo".
//
// Where that matters, say which you want rather than relying on the default:
// [a=v i] is case-insensitive and [a=v s] is exact, and both work for any
// attribute. The i flag folds ASCII too, and only ASCII: [data-x="é" i] does not
// match data-x="É", so the flag a caller reaches for when case matters is the one
// that will not help with the case that is hardest to spot. Measured in
// asciicase_test.go.
//
// An unsupported selector is rejected by [NewWriter], not silently ignored, with
// a [SelectorError] naming it and saying which part it could not use.
//
// # A colon, a dot or a leading digit in a name has to be escaped
//
// A selector is CSS, so a punctuation character in a tag or attribute name is
// read as CSS punctuation unless it is escaped with a backslash. The two that
// come up are the colon, in the namespace-prefixed names that Edge Side Includes
// and SVG's xlink attributes use, and the dot, in a class or id that contains
// one. Measured:
//
//	esi:include        Unsupported pseudo-class or pseudo-element in selector
//	esi\:include       matches <esi:include>
//	[xlink:href]       Unexpected token in the attribute selector
//	[xlink\:href]      matches <a xlink:href="x">
//	.a.b               parses, matches nothing: two classes, "a" and "b"
//	.a\.b              matches class="a.b"
//	my-element         matches; a hyphen needs no escape
//
// The first two rows are the ones worth knowing, because the message names a
// pseudo-class the caller did not write. [SelectorError] adds the answer when the
// selector it rejected contains an unescaped colon. The dot has no such help: it
// parses, so nothing fails - the handler simply never runs, which is the quietest
// failure in this list.
//
// A digit is a third case, with a rule of its own: a CSS identifier cannot begin
// with one, so a class or id that does cannot be written after "#" or "." at all.
// Generated ids and utility class names land here regularly, and the message is no
// help either - lol-html reports "The selector is empty", which describes what its
// parser had left rather than what the caller wrote:
//
//	#1a                     The selector is empty
//	#\31 a                  matches id="1a"
//	#\31a                   parses, matches nothing: \31a is U+031A, one character
//	#\000031a               matches: six hex digits need no terminator
//	[id="1a"]               matches, and needs no escaping at all
//	.2xl\:hidden            The selector is empty: a digit and a colon at once
//	[class~="2xl:hidden"]   matches that one
//
// The space after "\31" is what ends the escape, and leaving it out is the quiet
// version of this mistake rather than a syntax error. [SelectorError] carries both
// answers for a rejected selector whose class or id starts with a digit, written so
// they can be copied; the attribute-selector form is the one to reach for, since it
// needs no escaping at all. Measured in digitident_test.go, including that both
// suggestions match the element they are suggested for.
//
// Everything else about matching is case-insensitive as usual, so
// `ESI\:INCLUDE` matches too.
//
// # Selectors do not consider namespaces
//
// A tag name in a selector matches that name in any namespace, so "a[href]"
// matches an HTML anchor, an SVG <a> and a MathML <a> alike, and "title" matches
// both a document title and an SVG tooltip:
//
//	<html><head><title>page</title></head>
//	<body><svg><title>tooltip</title></svg></body></html>
//
//	OnText("title", ...)   // fires for "page" and for "tooltip"
//
// [Element.NamespaceURI] does not settle it, because it reports the namespace an
// element's children are parsed in rather than the element's own, and SVG's
// title, desc and foreignObject are HTML integration points - so they report the
// HTML namespace, exactly like the document title. Same for MathML's mi, mo, mn,
// ms and mtext.
//
// Two things do work. A selector that names the context is exact:
//
//	OnText("svg title", ...)   // only the tooltip
//
// and its complement is not, because a selector cannot say "not inside svg":
// "head title" and "head > title" find the document title only when the input
// actually contains <head>, and <head> is optional in HTML - given
// "<title>page</title><p>x</p>" they match nothing at all.
//
// So a handler that must act on the document title and not on tooltips has to
// match "title" and track the context itself, which is one more handler and a
// stack. examples/gip/envbadge counts <svg> and <math> depth, which is enough
// when the only question is "am I in foreign content".
//
// When the question is "which namespace is this element in", a depth counter is
// not enough, because an integration point switches back: the <p> in
// <svg><foreignObject><p> is an ordinary HTML paragraph and a counter says SVG.
// What works is a stack of [Element.NamespaceURI] values - an element is parsed
// in the namespace its parent's children are parsed in, which is what the parent
// reported. The method's own documentation has the shape of it, and
// examples/gip/histogram uses it to keep an HTML <a> and an SVG <a> in separate
// rows.
//
// # :not() is wrong for anything but a single simple selector
//
// This one is not a limitation but a defect, and it is silent, so it is worth
// knowing exactly.
//
// :not() is correct when its argument is one simple selector - :not(div),
// :not(.a), :not([href]), :not(:first-child). Give it a compound selector and it
// negates each part separately and requires all of them, which is the wrong half
// of De Morgan's law: :not(div.a) is evaluated as :not(div):not(.a).
//
// On the document
//
//	<div class="a">1</div><div class="b">2</div><span class="a">3</span><span class="b">4</span>
//
// :not(div.a) should match everything except the first, three elements. It
// matches one, span.b - the same as :not(div):not(.a). A selector list inside is
// affected too: :not(div.a, span.b) matches nothing at all.
//
// So a rewrite meant to process everything except trusted anchors, written
// OnElement(":not(a.trusted)"), skips every anchor and everything carrying that
// class. For a filter that is a hole rather than a nuisance.
//
// Until it is fixed upstream, use :not() with a single simple selector, or match
// positively and decide inside the handler:
//
//	lolhtml.OnElement("a", func(e *lolhtml.Element) error {
//		if cls, _ := e.Attribute("class"); strings.Contains(cls, "trusted") {
//			return nil
//		}
//		...
//	})
//
// A combinator inside :not() is a separate matter: it is not wrong, it is
// rejected, and the rule above does not predict it. "Supported if the rewriter
// can decide it at the start tag" is satisfied by :not(div p) - whether an
// element is inside a div is exactly what the plain descendant selector div p
// decides, at the start tag, and that one works. Measured, the whole boundary:
//
//	:not(div)  :not(.a)  :not(#i)         accepted
//	:not([a])  :not([a=v])  :not(*)       accepted
//	:not(div.a)  :not(div, span)          accepted, and wrong as described above
//	:not(:first-child)                    accepted
//	:not(:nth-child(2))                   accepted
//	:not(:not(div))                       accepted
//	:not(div p)                           rejected
//	:not(div > p)                         rejected
//	:not(div + p)                         rejected
//	:not(div ~ p)                         rejected
//
// The sibling combinators are unsupported anywhere, so those two are no
// surprise. The descendant and child ones are supported everywhere except here.
//
// The error message does not say so. All four report
//
//	Unsupported pseudo-class or pseudo-element in selector.
//
// which names :not() rather than what is inside it, and follows it with the
// advice about escaping a colon in a tag name. Neither part points at the
// combinator. If a selector with :not() in it is rejected and the colon is not
// the problem, look for a space or a > inside the parentheses.
//
// There is no selector for "not inside an X", then. Keep a stack instead: push
// at the start tag, pop in the end-tag handler, and read the stack in the
// handler - examples/gip/islands does exactly that, and has the two further
// traps that come with it.
//
// # An attribute can appear twice
//
// The HTML parsing specification calls a repeated attribute a parse error and
// requires the parser to keep the first and drop the rest, so a browser's DOM
// never has two. lol-html keeps them all, and the API is split over what to do
// about that:
//
//	<p a="x" a="v">
//
//	[a="x"] matches, [a="v"] does not     the first only
//	Attribute("a")   is "x"               the first only
//	SetAttribute("a", "z")                replaces the first, leaves a="v"
//	RemoveAttribute("a")                  removes both
//	Attributes(), AttributeList()         yield a="x" and a="v"
//
// Three of those act on the first, which is the copy a browser would have kept.
// Two do not, and both for a reason. Removal takes every copy, because a filter
// that left one behind would be a filter that does not filter. Iteration yields
// every copy, because a program reporting on a document should be able to see
// what is actually in it.
//
// The consequence for a reader is worth stating plainly: iterating attributes
// shows you attributes that nothing downstream will act on. A tool extracting
// microdata, or Open Graph tags, or anything else keyed on attribute names, has
// to decide which copy counts - and the answer that matches what a browser does
// is the first. examples/gip/microdata does that.
//
// The consequence for a rewrite is smaller but sharper: reading a value,
// deciding from it, and writing it back is consistent, because all three use the
// first. Reading through the iterator and writing back is not.
//
// # Character references are not decoded
//
// Text, comment text and attribute values are reported as raw source: the href
// of <a href="?a=1&amp;b=2"> is "?a=1&amp;b=2". lol-html has to be able to
// re-emit what it read, so it does not decode on the way in, and correspondingly
// escapes what you write. Reading a value and writing it back unchanged is
// therefore correct; comparing one against a decoded Go string is not.
//
// The rule: decide on the decoded form, rewrite the raw one. Use
// html.UnescapeString for the first and leave the value alone for the second - with
// the caveat below, because for an attribute value that decoder is not the parser's.
//
// One more difference, and it runs the other way: html.UnescapeString decodes more of
// an attribute value than a browser does. A named reference without its semicolon is
// not a reference in an attribute when the next character is "=" or ASCII
// alphanumeric, so "?a=1&copy=2" keeps its copy parameter in a browser and grows a
// copyright sign in the standard library. A filter deciding on that decoded form is
// deciding about a URL nobody will request, and a rewrite that decodes, edits and
// re-encodes produces a different one. In text the two agree; the rule is an attribute
// rule. Measured in differential/attrrefs_test.go, and implemented in
// examples/gip/references.
//
// Getting that the wrong way round is how a filter acquires a hole, because a
// browser decodes before it acts. These three hrefs all execute:
//
//	javascript:x()
//	java&#9;script:x()
//	&#106;avascript:x()
//
// A check on the raw string catches only the first: the others read as schemes
// called "java&#9;script" and "&#106;avascript". Decode first and all three are
// the same URL. The same applies to any decision taken on a value - an
// allow-list of protocols, a comparison against an expected filename, a test for
// a marker in text.
//
// It cuts the other way too. Having decoded a value to decide about it, do not
// write the decoded form back unless you mean to: SetAttribute takes raw source,
// so writing "a&b" produces an attribute whose value is "a&b" to a parser, and
// writing back the "a&amp;b" you were given round-trips exactly.
//
// # Source is not only undecoded, it is unpreprocessed
//
// References are the well-known half. The other half is that HTML normalises some
// bytes before the tokenizer ever sees them, and a rewriter that re-emits what it
// read cannot do that and still be a rewriter. So four more things differ between
// what a handler is handed and what a parser has, measured against
// golang.org/x/net/html in differential/preprocess_test.go:
//
//	a CR or a CRLF          is a LF to a parser, in text, in a comment and in an
//	                        attribute value; reported here as written
//	a NUL in element content is dropped by a parser; reported here as written
//	a NUL in raw text or a comment  is U+FFFD to a parser
//	a NUL in an attribute value     is kept as a NUL
//	a leading LF in <pre>, <listing> or <textarea>  is dropped by a parser - one
//	                        of them, not all; reported here as written
//
// Two consequences. A comparison against a value that came from a browser, a DOM
// or another parser can fail on bytes neither side chose: "a\r\nb" here is "a\nb"
// there. And a value written into an attribute is read back changed if it contains
// a CR - to write one, write "&#13;", which is a reference and survives.
//
// A rewrite that only copies values around is unaffected, because both sides are
// source. One that compares, hashes, or reports what a page says has to decide
// which form it means, the same way it does for references.
//
// # Inserting content
//
// Four things about insertion are worth knowing before relying on any of it,
// and each has its own section below: two calls of the same kind do not always
// come out in call order; nothing inserted is dispatched back to your handlers;
// neither content type is right inside a <script> or a <style>; and markup you
// build yourself is the only thing here that is not escaped for you.
//
// # What Text guarantees, and what it does not
//
// [Text] escapes the three characters that could begin markup, so nothing it
// writes becomes a tag. That is checkable, and it is checked: over every document
// and value the generator can produce, an insertion as Text leaves the sequence of
// tags in the output exactly as it was. properties/text_structure_test.go holds
// that, and the same for a streamed insertion and for [EscapeText] used by hand.
//
// The guarantee is about the markup, not about the tree a browser builds from it.
// Tree construction responds to the presence of text, so one character can change
// the tree while adding no tag at all. Measured on a formatting element misnested
// across a block boundary:
//
//	<p><a><div></div></a></p>   tree:  <p><a></a></p><div></div><p></p>
//	<p><a><div>x</div></a></p>  tree:  <p><a></a></p><div><a></a></div><p></p>
//
// The second tree has an <a> the markup does not contain, because inserting a
// character makes the parser reconstruct the active formatting elements at that
// point. Appending "x" as Text through this library does the same thing. Pinned in
// differential/textstructure_test.go, along with the shapes where it does not
// happen - well-nested documents are unaffected.
//
// So "this rewrite cannot change the structure" is true of the bytes and false of
// the tree, and a program promising the stronger version is promising something
// the format does not allow.
//
// # Two insertions of the same kind
//
// Every insertion goes immediately adjacent to the unit, and the one rule has a
// consequence that catches people: two calls to the same method do not always
// come out in the order they were made.
//
// Three calls inserting "1", "2" then "3":
//
//	Before   123<p>t</p>      in order
//	After    <p>t</p>321      reversed
//	Prepend  <p>321t</p>      reversed
//	Append   <p>t123</p>      in order
//
// The rule is the same in all four: the newest insertion is the one closest to
// the unit. For Before and Append that puts it last in reading order; for After
// and Prepend it puts it first. [EndTag.Before] and [EndTag.After] follow the
// same pattern, as does [Comment.After].
//
// It matters most when several calls assemble one thing. Building a comment out
// of three After calls - the delimiters and the text between them - emits them
// backwards and produces "-->text<!--". Pass the whole string in one call, or
// use Before, where the order reads as written.
//
// [DocumentEnd.Append] is in order, like the other Append.
//
// # Inserted content is not re-parsed
//
// Nothing a handler inserts is dispatched to any handler, including the one that
// inserted it and including handlers on other selectors in the same rewrite. It
// goes into the output as written.
//
// Two of the consequences are conveniences. There is no loop hazard: a handler
// that inserts an element matching its own selector fires once. And an
// accumulator is safe, so a text handler collecting a heading's text does not
// also collect a label an element handler prepended, which is what lets a
// rewrite read and write the same element without compounding.
//
// The third is a hazard. A rewrite that removes every <script> does not remove
// one that another of its own handlers inserted:
//
//	lolhtml.OnElement("script", func(e *lolhtml.Element) error { e.Remove(); return nil }),
//	lolhtml.OnElement("div", func(e *lolhtml.Element) error {
//		return e.Prepend(untrusted, lolhtml.HTML)   // never seen by the remover
//	})
//
// The document's own scripts go; the inserted one stays, in either registration
// order. Anything you insert has to be safe before it goes in - use [Text] for
// values you did not author, and see the section on inserting into a script for
// where even that is not enough.
//
// The same rule reaches the content that was already there, through
// [Element.SetTagName]: a rename writes over the tag and leaves the content alone,
// and whoever parses the output applies the new name's content model to it. Renaming
// a div that holds a paragraph to a table fosters the paragraph out of it, and
// renaming it to a select deletes the paragraph and a span beside it, merging their
// text. So a rename is safe when the new element accepts what the old one held. See
// differential/rename_test.go.
//
// # Inserting into a script or a style
//
// Neither [ContentType] is right for the inside of a <script> or a <style>, and
// the failures are quiet in opposite directions.
//
// Those two are *raw text* elements: an HTML parser does not read their content
// as markup and does not decode character references in it. Seven more elements
// behave the same way - iframe, noembed, noframes, noscript and xmp, plus
// textarea and title, which do decode references - and plaintext, which is raw
// text that runs to the end of the input and cannot be closed at all. Ten
// element names in total; the list is measured rather than quoted, in
// rawtext_test.go, and [IsRawText] answers it for a tag name so a caller who
// has to decide - a sanitiser unwrapping unknown elements, a text handler under a
// wide selector - does not have to copy it out of this paragraph.
//
// So [Text], which escapes <, > and &, produces content that is inert but no
// longer says what it said:
//
//	e.SetInnerContent(`if (a < b && c > d) {}`, lolhtml.Text)
//	// <script>if (a &lt; b &amp;&amp; c &gt; d) {}</script>
//
// The document is valid, nothing returns an error, and the script throws a
// syntax error in the browser. [Element.Attribute] and the HTML around it look
// exactly as intended, which is why this is easy to ship.
//
// [HTML] would insert the text as written, and the element would end wherever
// the content says it does:
//
//	e.SetInnerContent(`var s = "</script><img src=1 onerror=alert(1)>";`, lolhtml.HTML)
//
// That is a working injection out of a string literal, so it is refused:
// inserting into the content of one of these elements returns
// [ErrRawTextBreakout] when the content would close it. The check is the
// tokenizer's rule, so "</scriptx" is fine and "</script foo>" is not, and it
// covers [Element.Prepend], [Element.Append], [Element.SetInnerContent] and
// [EndTag.Before] on any of the nine that can be closed. Writing outside the
// element - Before, After, Replace - is ordinary markup and is not checked, and
// neither are the streaming insertions or the [TextChunk] ones; [ErrRawTextBreakout]
// says why, and a text handler editing a script has to guard itself.
//
// There is still no combination of the two that makes arbitrary text safe here.
// The refusal stops the injection; it does not give you a way to say what you
// meant. Escaping correctly needs to know where in the JavaScript the content
// lands - inside a string literal, "</script" has to become "<\/script", which
// is a JavaScript transformation rather than an HTML one, and JSON's own escaping
// of "/" exists for exactly this. So: build script and style bodies from values
// you control, and if untrusted data has to reach a script, put it in a data
// attribute or a JSON <script type="application/json"> block and read it from
// there.
//
// [Comment.SetText] refuses a comment-closing sequence for the same reason, and
// was the only such check for a while; [ErrRawTextBreakout] is the other half.
//
// A textarea and a title are *escapable* raw text, where references are
// decoded, so Text behaves normally in them. In the other five - iframe,
// noembed, noframes, noscript, xmp - references are not decoded and there is no
// inner language either, so there is no way to write the closing sequence inside
// the element and the content itself has to change.
//
// One more way this goes wrong, and it does not involve escaping at all. When the
// document's encoding cannot represent a character, [WithEncoding] emits a
// numeric character reference instead - which is right everywhere a reference is
// decoded, and raw text is where it is not:
//
//	WithEncoding("windows-1252")
//	e.SetInnerContent(`var s = '日'`, lolhtml.Text)
//	// <script>var s = '&#26085;'</script>
//
// The script now holds those eight characters instead of the one that was meant.
// Both rules were followed - the content type is right for the position and the
// fallback is the documented one - and the result is still wrong, so there is
// nothing to fix in the call. Either keep the script body inside the document's
// encoding, using an escape the target language understands - "\u65e5" for
// JavaScript, "\65e5" for CSS - or serve the document as UTF-8, where the
// question does not arise. Pinned in encoding_test.go.
//
// Rewriting text that is already there has the same two problems and one more.
// [TextChunk.Replace] with [Text] escapes what raw text must not have escaped, so a
// stylesheet's ".a > .b" comes back as ".a &gt; .b" - a selector that matches
// nothing - and a script's "a < b" as "a &lt; b". [HTML] is therefore the right
// content type for editing a script body or a stylesheet, which reads backwards and
// is worth knowing. And the breakout guard does not cover it: an [Element] method
// knows which element it is writing into and refuses a "</script>", while a
// [TextChunk] does not - lol-html hands a chunk over with no way to ask - so
// [TextChunk.Before], [TextChunk.After] and [TextChunk.Replace] write it out. A
// handler registered as OnText("style") knows the tag it asked for and can apply the
// check itself with [CheckRawText]. Measured in rawtextrewrite_test.go.
//
// # A table wrapper inside a paragraph depends on the doctype
//
// The rule below - that a wrapper is two insertions and the parser decides what they
// wrap - has one case whose answer is not a property of the markup at all. A <table>
// start tag closes an open <p> in a standards-mode document and not in a quirks-mode
// one, so wrapping content that sits inside a paragraph puts the table beside the
// paragraph or inside it depending on whether the document has a doctype. Measured
// against x/net/html:
//
//	wrapper     no doctype (quirks)   <!doctype html>
//	<table>     stays in the <p>      leaves the <p>
//	<div>       leaves                leaves
//	<section>   leaves                leaves
//	<ul>        leaves                leaves
//	<span>      stays                 stays
//
// Every other wrapper is mode-independent: a block one leaves the paragraph in both
// modes, an inline one stays in both. The table is the exception, and it is the
// wrapper a converter to email markup uses for everything - on documents that
// frequently have no doctype or a doctype from 1999, which is also quirks. So the
// same input gives two different trees and nothing reports it.
//
// Either put the wrapper somewhere a paragraph is not open, or know the document's
// mode. examples/gip/tablelayout refuses the conversion inside a paragraph and says
// how many it refused; differential/tablewrap_test.go has the matrix.
//
// # A wrapper is two insertions and the parser decides whether they wrap
//
// Putting a container around an element is [Element.Before] with an opening tag and
// a closing tag at the element's end. Both insertions succeed, the output looks
// exactly as intended, and whether the result is a container around the element is
// decided afterwards by whoever parses it. Inside a paragraph it often is not:
//
//	<p>text <iframe src="a"></iframe> more</p>
//
//	wrapped in a div     p > "text", div > iframe, "more", p
//	wrapped in a span    p > "text", span > iframe, "more"
//
// A div closes an open paragraph, so it takes the element out of the paragraph,
// leaves the text that followed it outside as well, and turns the source's </p>
// into a second, empty paragraph. Three changes to the tree from an edit that only
// meant to add a container, and nothing in the bytes looks wrong.
//
// A span does not close a paragraph - and cannot hold an element that does:
//
//	<p>text<pre>code</pre></p>
//
//	wrapped in a span    p > "text", span, then pre outside it: the span is empty
//	wrapped in a div     p > "text", div > pre, p
//
// So the question is not whether the wrapper is a block or an inline element, it is
// whether the wrapped element closes a paragraph by starting. Where it does, the div
// is right, because it leaves the paragraph with the element - which the element was
// doing anyway. Where it does not, the span is the only one that wraps.
//
// For a <table> the answer depends on the doctype, because a table closes a
// paragraph only outside quirks mode:
//
//	<p>text<table>…</table></p>          span holds the table, div moves it
//	<!DOCTYPE html> the same document    div holds the table, span comes out empty
//
// The doctype arrives before any element, so a rewrite can read it with [OnDoctype]
// and decide, which is what examples/gip/scrollwrap does.
//
// A wrapper around a table-internal element wraps nothing at all: it is fostered out
// to just before the table while the cell stays where it was. Measured, along with
// everything above, in differential/wrap_test.go.
//
// The closing half has the end-tag rule on top of all this: the position only
// belongs to the element when the end tag is the element's own, and an element
// nothing closes has no position at all. See [Element.OnEndTag].
//
// # Building markup yourself makes you the serialiser
//
// Every path that writes a value for you escapes it. [Element.SetAttribute]
// escapes the double quote, which is the character that could end the attribute;
// [ContentType] Text escapes the three characters that would be markup. The one
// path that escapes nothing is markup you construct and pass as [HTML] - and
// that is the tempting route for turning one element into another.
//
// A document-derived value dropped into an attribute you wrote yourself is an
// injection. A single-quoted attribute may contain a bare double quote, and it
// reads back as one:
//
//	<iframe title='" onload=alert(1) x="'>
//
//	e.Replace(`<div data-x="`+title+`">`, lolhtml.HTML)
//	// <div data-x="" onload=alert(1) x=""></div>
//
// The div now has a working event handler that came from the document. The same
// value through SetAttribute is inert:
//
//	e.SetAttribute("data-x", title)
//	// data-x="&quot; onload=alert(1) x=&quot;"
//
// So prefer changing the element to replacing it. [Element.SetTagName],
// SetAttribute and [Element.RemoveAttribute] between them turn an <iframe> into a
// <div> carrying whatever attributes you want, with every value escaped on the
// way out, and the result is less code than assembling a string.
//
// When you do have to build markup - a wrapper, a template, an element that does
// not exist yet and so has no handler to hold it - [EscapeText] and
// [EscapeAttribute] do that escaping for you.
//
// EscapeText is byte for byte what the library applies for [Text], asserted
// against the library rather than assumed, so a value built into markup keeps the
// guarantee it would have had.
//
// EscapeAttribute is not the same as what SetAttribute applies, and the
// difference is the point rather than an oversight. SetAttribute escapes the
// double quote alone, because the library writes the quotes and knows which ones
// they are. EscapeAttribute escapes five characters:
//
//	value      SetAttribute   EscapeAttribute
//	a"b        a&quot;b       a&quot;b
//	a'b        a'b            a&#39;b
//	a<b        a<b            a&lt;b
//	a&b        a&b            a&amp;b
//	a&amp;b    a&amp;b        a&amp;amp;b
//
// because the markup being built might use single quotes, and because an
// unescaped "&" in it could begin a reference the caller did not write. The last
// row is the one to watch: a value that came from the document is already source,
// so SetAttribute passes it through and EscapeAttribute escapes it again. Pinned
// in escape_test.go, which asserts the difference rather than assuming it.
//
//	e.Replace(`<div data-x="`+EscapeAttribute(title)+`">`+EscapeText(s)+`</div>`, HTML)
//
// Two things they do not do. They do not sanitise: a URL is still a URL after
// escaping, so EscapeAttribute will happily produce a well-formed href of
// "javascript:alert(1)", and deciding which schemes to allow is a separate job.
// And they are not idempotent, because nothing that escapes "&" can be: a value
// that came from the document is already raw source, so escaping it again turns
// "&amp;" into "&amp;amp;". Decode it first, or leave it raw and do not escape
// it; see the section on character references.
//
// "Already raw source" is source for the context it came from, and moving a value
// between contexts is where that bites. Each context lets through the character the
// other one ends on:
//
//	<span title="<img src=x onerror=alert(1)>">   an attribute may hold a raw "<"
//	<h2>a" onload=alert(1) x="b</h2>             text may hold a raw quote
//
// Both are inert where they sit. Move the title into an element's text unescaped -
// the obvious way to turn an alt into a <title>, or a label into a caption - and the
// img is an element with a working onerror. Move the heading's text into an
// attribute unescaped, and the div being built gets an onload. Measured both
// directions against golang.org/x/net/html in differential/context_test.go, by
// counting what the tree has rather than by reading the output.
//
// So a move needs the destination's terminator escaped and nothing else: the "<"
// for text, the quote for a double-quoted attribute. That is what [Text] and
// SetAttribute apply, and it is why an attribute value that has to become an
// attribute again can go through unchanged. Where the value has to be built into
// markup by hand, escaping only that character keeps the value's own references
// intact - EscapeText and EscapeAttribute are for a value that is not already
// source, and on one that is they escape its "&" a second time. The other answer,
// and usually the better one, is not to move it: keep a name in an attribute
// (aria-label rather than a <title> child), which is what examples/gip/sprite does.
//
// There is a third context and it has no escaper, because it cannot have one. A
// comment ends at "-->" or at "--!>", and nothing inside it is a reference, so
// there is no spelling of those four characters that a comment can hold:
//
//	e.Append("<!-- "+title+" -->", lolhtml.HTML)
//
//	title = `--><img src=x onerror=alert(1)><!--`
//	// <!-- --><img src=x onerror=alert(1)><!-- -->
//
// and the image is an element. Both closing sequences work, measured in
// differential/comment_test.go. Passing the value through [EscapeText] does stop
// it - "--&gt;" is not a closing sequence - and it also changes what the comment
// says, since a comment holds characters rather than references. So the choice is
// between a comment that is wrong and one that is dangerous, which is why
// [Comment.SetText] refuses instead: it is the only path that writes a comment's
// text for you, and it rejects a closing sequence rather than escaping one. Where
// the comment already exists, use it. Where it does not, remove the sequence from
// the value yourself and say in the comment that you did.
//
// # Reading an element's whole text
//
// [OnText] fires for every text chunk inside the matched element, including text
// inside its descendants, and [TextChunk.IsLastInTextNode] marks the end of a
// text node rather than the end of the element's content. Those are the same
// thing only when the element contains no markup.
//
//	<a href="/x">click <b>here</b></a>
//
// has two text nodes, "click " and "here". A handler that accumulates to
// IsLastInTextNode and replaces there runs twice and produces
// "REPLACED<b>REPLACED</b>". Tested on a document without nested markup, the
// same code looks correct.
//
// To act on an element's whole text, accumulate in the text handler and finish in
// [Element.OnEndTag] - and decode what you accumulated before writing it back,
// which is the part this example got wrong for a long time:
//
//	lolhtml.OnElement("a", func(e *lolhtml.Element) error {
//		acc.Reset()
//		return e.OnEndTag(func(t *lolhtml.EndTag) error {
//			return t.Before(rewrite(html.UnescapeString(acc.String())), lolhtml.Text)
//		})
//	}),
//	lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
//		acc.WriteString(tc.Text())
//		tc.Remove()
//		return nil
//	})
//
// [TextChunk.Text] is source, so the accumulator holds "caf&eacute;" and not
// "café". Writing that back as [Text] escapes the ampersand a second time, and
// the page shows the escaping. Measured on <a href="/x">caf&eacute; <b>&amp;
// more</b></a> with rewrite = strings.ToUpper:
//
//	without UnescapeString   CAF&amp;EACUTE; &amp;AMP; MORE
//	with it                  CAFÉ &amp; MORE
//
// The first renders as the literal text "CAF&EACUTE; &AMP; MORE". See
// [TextChunk.Text] for the two other ways to write text back and why neither is
// this one.
//
// That leaves the descendant elements behind as empty shells - "<b></b>" - since
// removing text does not remove markup. Add a handler on "a *" calling
// [Element.RemoveAndKeepContent] if the whole content is to be replaced rather
// than only its text.
//
// The alternative is to remove the element in its own handler and rebuild it at
// the end tag with [ContentType] HTML, which also lets you change its tag and
// attributes - at the cost of re-serialising those yourself, escaping included.
//
// # A table can contain things that are not in it
//
// A parser moves content that cannot be inside a table to just before the table,
// which the specification calls foster parenting. There is no tree here to move
// anything in, so that content is reported where it was written - inside the
// table - and emitted there. The output is byte-identical, because a browser
// reading it fosters the content out again, so nothing looks wrong:
//
//	<table>stray<tr><td>a</table>
//
//	in the tree      "stray" is a sibling before the table
//	here             a text handler on "table" is given it
//
// Measured for text before the first row, text inside a row, text after a cell,
// and an inline or block element in any of those places, in
// differential/table_test.go.
//
// Two things follow. Collecting an element's text is the wrong question to ask of
// a table this way, because the answer includes content that is not in it. And
// removing the table removes that content, where a tree-based edit would keep it:
//
//	<p>before</p><table>stray<tr><td>a</table><p>after</p>
//
//	Element.Remove here   <p>before</p><p>after</p>
//	the same edit on a    <p>before</p>stray<p>after</p>
//	tree
//
// A table extractor should therefore take cell content from cells rather than
// text from the table, which is what examples/gip/tablecsv does.
//
// The third thing that follows is the one that bites a rewrite rather than a
// reader: an insertion goes where the markup says, and tree construction may put
// it somewhere else. Measured against golang.org/x/net/html, prepending
// <input name="csrf"> to a form:
//
//	<form method=post><p>x</p>                 form > input     where it was put
//	<table><tr><td><form method=post>          td > form > input  the same
//	<table><form method=post><tr>              table > input     outside the form
//	<table><tbody><form method=post><tr>       tbody > input     outside the form
//	<select><form method=post>                 body > input      outside everything
//
// The bytes say the field is inside the form and the tree says it is beside it. For
// a hidden field carrying a token, a nonce on a script, or anything else whose
// position is the whole point, "the markup looks right" is not the test - and a
// rewrite that cannot tell the shapes apart should refuse the ones it cannot, which
// is what examples/gip/csrf does. Pinned in differential/table_test.go.
//
// # <image> is a spelling of <img>
//
// The parser renames one element. An <image> start tag in HTML content builds an img
// element, carrying every attribute it had - so a browser fetches the file and runs
// its onerror - while the rewriter reports what the document spelled:
//
//	<image src="x.png" onerror="alert(1)">
//
//	in the tree      img src="x.png" onerror="alert(1)"
//	here             TagName() == "image", and "img" matches nothing
//
// So every rewrite keyed on img has a hole in it: a sanitiser stripping event
// handlers, a URL rewriter, a mixed-content checker. The fix is to match both names
// and to check [Element.NamespaceURI] on the second, because SVG has an image element
// of its own that keeps its name and is not an img at all:
//
//	OnElement("img,image", func(e *Element) error {
//		if e.TagName() == "image" && e.NamespaceURI() != NamespaceHTML {
//			return nil // an SVG image
//		}
//		…
//	})
//
// Renaming it with [Element.SetTagName] is the tidiest answer where a rewrite is
// editing the document anyway: the output then says what the browser was going to
// build. Nothing else on the obsolete list is renamed - center, font, marquee,
// acronym, applet, keygen, isindex and the rest all reach the tree under their own
// names - so this is one alias rather than a habit. Measured against
// golang.org/x/net/html in differential/imagealias_test.go, and reported by
// examples/gip/deprecated.
//
// # An HTML tag name inside an <svg> ends the svg
//
// Foreign content is not a container the way an element is. The parser breaks out of
// SVG and MathML when it meets an HTML tag name, and 44 names do it - b, big,
// blockquote, body, br, center, code, dd, div, dl, dt, em, embed, h1 to h6, head,
// hr, i, img, li, listing, menu, meta, nobr, ol, p, pre, ruby, s, small, span,
// strong, strike, sub, sup, table, tt, u, ul, var - plus font, which breaks out only
// when it carries a color, face or size attribute:
//
//	<svg><rect/><p>x</p><circle/></svg>
//
//	in the tree   svg > rect, then p and circle beside the svg, both HTML elements
//
// Everything after the offending tag is document content rather than image content.
// That is the whole problem for a rewrite that inlines a file into an <svg>: a file
// holding one <p> puts the rest of itself in the page. Measured over the full list,
// including the font condition, in differential/foreign_test.go.
//
// The library's two views of this disagree, and both are reported from the same
// document at the same moment. [Element.NamespaceURI] follows the break-out and
// reports HTML for what comes after it. The selector engine does not: "svg circle"
// and even "svg > circle" match a circle the tree puts outside the svg, because the
// engine pops its stack on end tags and this was a start tag. So neither a selector
// nor a namespace check answers "is this still inside the image", and a rewrite that
// needs to know has to look for the names itself - which is what
// examples/gip/inlinesvg does before inlining a file at all.
//
// # A template is markup that is not on the page
//
// Handlers fire inside a <template> exactly as they do anywhere else, at any depth
// of nesting, and a descendant selector crosses the boundary - "template video"
// matches, and so does a bare "video". The content is parsed as markup, so a
// rewrite reaches every element in it.
//
// What it does not reach is the page. A template's content is inert until a script
// clones it: no video plays, no image loads, no script runs. So a match in there is
// a rewrite of a blueprint, and a count that adds the two together is a count of
// nothing in particular - a report saying "6 videos" for a page with two and a
// carousel template is wrong twice over. Decide, and count separately; that is what
// examples/gip/controls does with a depth counter, because the selector cannot tell
// you.
//
// The content also follows the template's own parsing rules rather than the
// surrounding document's, and this is where it stops being a curiosity. A template
// may hold table rows with no table around them:
//
//	<template><tr><td>x</td></tr></template>   template > tr > td > "x"
//	<div><tr><td>x</td></tr></div>             div > "x": the tags are dropped
//
// The rewriter fires a td handler in both, because it is reading tokens - so a
// handler call is not evidence that a cell exists. Measured in
// differential/template_test.go.
//
// A template is also the one element that a table does not foster out, so an
// insertion into it lands where the bytes say. The trade is the other way round
// from the table above: there the insertion moves and the content survives, and
// here the insertion stays and the content can be thrown away. A template holding
// rows is parsed in a mode that the first inserted *element* ends, and the rows go
// with it:
//
//	<table><template><tr><td>x</td></tr></template></table>
//
//	Prepend("<input>", HTML)   table > template > input > "x"   the rows are gone
//	Append("<input>", HTML)    table > template > tr > td > "x" > input
//	Prepend("<!--c-->", HTML)  table > template > tr > td > "x"
//	Prepend("hello", Text)     table > template > "hello" > tr > td > "x"
//	Before("<input>", HTML)    table > input, table > template > tr > td > "x"
//
// It is the parser's rule and not the insertion's fault - the same bytes written by
// hand lose the rows too - but a rewrite that prepends anything to elements it
// matched has no reason to expect it. Prepending a comment or text is safe,
// appending is safe, and for an element the safe positions are after the content or
// outside the template.
//
// # Removal suppresses output, not handler calls
//
// [Element.Remove] takes the element and its content out of the output. It does
// not stop handlers running for that content: a text handler still sees the text
// of a removed element, and an element handler still runs for its descendants.
// Their edits are discarded along with everything else, but a handler that
// accumulates - collecting a document's visible text, counting what it rewrote -
// has to notice for itself that the content it is looking at is on its way to
// being dropped. [Element.IsRemoved] is how an element handler checks.
//
// One corner does not behave the way Remove's description suggests. Removal
// decides the fate of the element's inner content at the moment it is called, so
// content inserted inside the element *after* that still reaches the output,
// with the element's tags no longer around it:
//
//	e.Remove()
//	e.Append("x", lolhtml.HTML)   // "x" is emitted, as a child of the parent
//	e.Append("x", lolhtml.HTML)
//	e.Remove()                    // "x" is discarded
//
// The two orders disagree, and only the second does what Remove promises. It
// matters most when two handlers share a selector, because then the order is
// decided by which option was written first rather than by either handler: one
// removing a <script> and one appending inside it will, in one of the two
// orders, emit the appended content as document markup. Insert first and remove
// last, or check [Element.IsRemoved] before inserting inside an element.
//
// [Element.Before], [Element.After] and [Element.Replace] position content
// outside the element, and surviving its removal is what they are for.
//
// Whether a handler can tell that it is inside a removed element depends on which
// handler it is, and the answer is not uniform:
//
//	an element handler   [Element.IsRemoved] is true for a descendant of a
//	                     removed element, so nothing has to be tracked
//	a text handler       [TextChunk.IsRemoved] is false: it reports only the
//	                     chunk's own removal
//	a comment handler    the same
//
// An insertion made by a descendant's handler is discarded along with the rest of
// the subtree - measured for every position - so the "insert first, remove last"
// hazard above is about one element and its own handlers rather than about a
// subtree. What a text handler cannot do is count: it is handed the text of a
// removed element with nothing to say so, so anything accumulating needs an
// element handler to tell it:
//
//	depth := 0
//	opts := []lolhtml.Option{
//		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
//			if !e.IsRemoved() || !e.CanHaveContent() {
//				return nil
//			}
//			depth++
//			return e.OnEndTag(func(*lolhtml.EndTag) error { depth--; return nil })
//		}),
//		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
//			if depth > 0 {
//				return nil // on its way out; not this document's text
//			}
//			// count it
//			return nil
//		}),
//	}
//
// which works because an end-tag handler still runs for an element inside a
// removed one. Measured in removedsubtree_test.go.
//
// # What counts as a comment
//
// A comment handler fires for what an HTML parser calls a comment, which is more
// than the "<!-- ... -->" the name suggests. The spec turns several malformed
// constructs into "bogus comments", and those arrive as comments here:
//
//	<?php echo "hi"; ?>     text: ?php echo "hi"; ?
//	<?xml version="1.0"?>   text: ?xml version="1.0"?
//	<!bogus>                text: bogus
//	<! spaced>              text:  spaced
//
// So a rewrite that removes every comment removes PHP blocks, XML declarations
// and processing instructions too - silently, since each of them is a
// well-formed comment as far as the parser is concerned.
//
// The first two can be told apart by their text, which keeps the "?" that opened
// them. The last two cannot: "<!x>" and "<!--x-->" both have the text "x", so
// nothing in the text distinguishes them.
//
// The delimiters do, and their length is knowable without the input. A comment's
// text is reported as raw source bytes - a carriage return and a NUL are passed
// through, not normalised - so the source range from [Comment.SourceLocation] is
// the text plus exactly the delimiters:
//
//	End - Start - len(Text) == 7   the document spelled it <!--...-->
//	anything else                  it did not
//
// which is the test a stripper wants, and it works from a stream. 3 is a bogus
// comment or a CDATA section, 2 a processing instruction, 8 a comment closed by
// "--!>", and 4, 5 and 6 the truncated and short-empty forms; two of the values
// collide, so what can be told reliably is the ordinary form from the rest rather
// than the unusual ones from each other. [Comment] has the measured table.
// Slicing the input at that range and looking for "<!--" is the other way, for a
// caller who has the input.
//
// Conditional comments are not one comment either. The downlevel-revealed form
//
//	<!--[if !IE]><!--><p>modern</p><!--<![endif]-->
//
// is two comments with real markup between them, and only the first contains
// "[if". A filter keyed on "[if" keeps that one, drops the closing half, and
// leaves markup that no longer means what it did.
//
// Not comments: the contents of <script>, <style> and <textarea>, which are raw
// text, so "<!--x-->" inside one of those is text and no handler sees it. Nor is
// a stray end tag like "</bogus end tag>", nor a second <!DOCTYPE>. A nested
// comment ends at the first "-->", leaving the remainder as text.
//
// Writing a comment has a rule of its own, and it is not escaping. Character
// references are not decoded inside comment data, so [EscapeText] does not
// protect a comment - it prevents the break-out and corrupts the text doing it:
//
//	SetText("a --> b")      // comment data is "a ", and " b -->" becomes text
//	SetText("a --&gt; b")   // comment data is literally "a --&gt; b"
//	SetText("a - -> b")     // comment data is "a - -> b", which is what was meant
//
// What ends a comment is two hyphens, so what keeps one intact is not letting two
// hyphens sit together. A comment must also not begin with ">" or "->": "<!-->"
// and "<!--->" are both empty comments, with everything after them left as text.
//
// # Cost
//
// A rewrite's cost tracks how many times your handlers run, not how long the
// document is. Passthrough with no handlers allocates a fixed amount however
// much goes through it, because the output sink hands the destination a slice
// over lol-html's own buffer rather than copying it, and a registered handler
// that never matches costs nothing per byte either.
//
// Per invocation, measured and gated by alloc_test.go:
//
//	the unit wrapper                      1 allocation
//	each string read or written           1 more
//	[Element.SourceLocation]              free, it is two ints
//	[Element.AttributeList], Attributes   4 per attribute
//
// So a handler that reads one attribute costs two allocations per match, one
// that reads the same attribute twice costs three - nothing is cached - and one
// that lists every attribute to find a single one costs four times the number of
// attributes on the element.
//
// A text handler starts at two calls per text node - the content and its empty
// boundary marker - and two is a floor rather than a figure. The writes split a
// node, and so does the tokenizer: a "<" in text that does not begin a tag is
// delivered as a chunk of its own, so "3 < 4 and 5 < 6" is six calls from one
// write. See [TextChunk.Text].
//
// Registering selectors has its own cost, paid once per [NewWriter]. Measured
// with the options built beforehand, so what is counted is registration rather
// than the caller's slice:
//
//	handlers   all distinct   all the same selector
//	       0             13                      13
//	       1             21                      22
//	       2             30                      28
//	       4             43                      38
//	       8             67                      57
//
// So the marginal cost is around seven allocations per distinct selector, and it
// falls as more are registered because the slices behind them grow in steps. A
// repeated selector costs about one and a half fewer, since each distinct
// selector is parsed once and reused - which is the part worth relying on: the
// saving is real but small, and registering the same selector twice to keep two
// handlers separate is not something to avoid on cost grounds.
//
// Paid once per NewWriter matters more than it sounds for a workload of many small
// documents, because a [Writer] cannot be reused - [Writer.Close] ends it, there is
// no reset, and a parsed selector belongs to the Writer that parsed it. So a queue
// pays the whole registration per item. Measured as the allocations for a complete
// rewrite:
//
//	selectors   empty document   1 KB   16 KB
//	        1               23      26      26
//	       10              105     106     106
//	       50              406     408     408
//
// Read down a column and the cost is the rule set; read across and the document
// adds almost nothing where nothing matches. In time, on an M3 Pro, fifty
// selectors is about 38 microseconds of construction, which is more than rewriting
// a one-kilobyte document with them costs - so under about sixteen kilobytes a
// document, a fifty-selector rewrite spends more time being built than running.
// Fewer selectors or bigger documents are the two ways out; there is no third.
//
// How many goroutines to run such a queue on is a property of the machine and not
// of this library, and it is lower than the core count: measured on a
// twelve-thread M3 Pro, 400 one-kilobyte documents with fifty selectors took 39 ms
// on one worker, 12.1 ms on four, and 20.2 ms on eight - the peak is at four and
// it gets worse above it, because a rewrite that is mostly allocation contends.
// The large-document case saturates rather than declining. examples/gip/queue
// measures both for a caller's own workload, and fixedcost_test.go gates the
// shape.
//
// That cost is per handler and not per selector, and a selector list is one
// selector. Measured over 500 elements that match nothing, so the numbers are
// registration and matching with no handler ever running:
//
//	no handlers                              16 allocations
//	one selector                             24
//	a twelve-clause list                     24
//	twelve separate registrations            96
//
// So naming twelve elements in one [OnElement] costs what naming one costs, while
// twelve OnElement calls cost eight times as much. The list is not free at match
// time - it was measured slower per element than a single clause and faster than
// twelve registrations - but it allocates nothing extra, and a rewrite that has a
// list of elements to look at should say so in one selector. That is what
// examples/gip/origins does with the twenty places a URL can hide. Gated in
// reportshape_test.go.
//
// Matching cost grows with the number registered as well, on every element -
// there is no index by tag or class - so a tool that registers one handler per
// rule in a stylesheet pays for all of them at every element of the document.
//
// It is still much cheaper than the alternative. A selector that does not match
// costs matching; a handler that runs costs a unit wrapper and whatever it reads,
// so a broad selector that lets the handler decide pays per element of the
// document rather than per element it cares about. Measured over a 2000-element
// page where about a tenth of the elements match:
//
//	three narrow selectors, all matching     439 allocations
//	one selector list "code,kbd,samp"        424
//	one "*" handler with a switch          4,228
//
// and where nothing matches at all, fifty narrow selectors still win by an order
// of magnitude:
//
//	fifty narrow selectors, none matching    351 allocations
//	one "*" handler with a fifty-name set  4,031
//	no handlers                               16
//
// So: prefer the narrowest selector that says what the rule is, and where one
// handler has to cover several names, a selector list is both the cheapest and the
// clearest way to write it. A "*" handler is for when the rule really is about
// every element - keeping a depth counter, say. Gated as a comparison rather than
// as numbers in alloc_test.go, since the figures move with the toolchain.
//
// The numbers above are gated by alloc_test.go as a range rather than a value,
// because they move with the toolchain: what is asserted is that the marginal
// cost is single-digit and that a repeat is cheaper than a distinct one.
//
// [Writer.Write] allocates nothing of its own, whatever the size of the write, so
// an allocation count measured with one big write is the count a caller streaming
// from a socket sees too. What a small write costs is the crossing into C: about
// 100 ns each on an M3 Pro, which makes a byte-at-a-time rewrite of a 64 KB page
// roughly eight times the time of the same page written whole - a constant factor
// rather than a change in shape. The per-byte cost is flat from 4 KB to 64 KB,
// including while the rewriter is buffering an unclosed tag, which is the cheap
// case rather than the expensive one: a pending tag produces no tokens to hand
// back. Releases up to v0.1.1 documented that case as quadratic; it is not, and
// bytecost_test.go gates the shape.
//
// The destination has a cost of its own, and it is not the one a reader of the
// above would guess. The number of writes it receives is decided by the rewrite
// rather than by how the document arrives, and what decides it first is *matching*
// rather than editing. A handler that does nothing at all splits the output around
// every element it matched. Measured on 200 anchors written as one 6200-byte
// Write:
//
//	no handlers                        1 write
//	a selector matching nothing        1 write
//	a handler that does nothing      400 writes
//	the same, reading an attribute   400 writes
//	an end-tag handler               600 writes
//	RemoveAttribute                 1200 writes
//	SetAttribute                    2600 writes, mostly of one byte
//
// Editing multiplies it again, because a mutated start tag is re-serialised piece
// by piece: 2000 elements turn one 132 KB write into 22,001 writes of median size
// one byte.
//
// So the case to watch is the one that looks free: a read-only instrumentation
// pass - a counter, an audit, a linter - over a rewrite streaming to an unbuffered
// destination turns one write per document into two per matched element. The
// output is identical, which is what makes it easy to miss; the write pattern is
// not.
//
// Wrap an unbuffered destination in a bufio.Writer and all of it collapses to the
// number of buffer-fulls - two or three writes for the document above. The library
// does not do that for you because a buffer is a promise not to write yet, and
// only the caller knows whether the thing at the other end is a browser waiting
// for a page or a file. See [NewWriter]; measured in sinkwrites_test.go and
// examples/gip/backpressure.
//
// # Errors
//
// A handler returning a non-nil error stops the rewrite; the error surfaces
// from the [Writer.Write] or [Writer.Close] that was running at the time,
// wrapped in a [HandlerError] you can unwrap. A handler that panics does not
// unwind through Rust: the panic is caught at the boundary and re-raised on the
// goroutine that called Write or Close.
//
// A value from outside the program can fail an insertion on its own: every path
// that takes content or a name refuses bytes that are not valid UTF-8, and that
// fails the rewrite rather than the insertion. The document path does not refuse
// them - they pass through, or become U+FFFD if a text handler is registered - so
// a rewrite can carry bytes it cannot write. [ErrInvalidUTF8] is the match, and
// says what to do about it.
//
// lol-html cannot resume after an error, so a Writer that has failed is
// poisoned and every later Write returns [ErrPoisoned]. A Writer that panics
// releases its native resources on the way out, so a caller who recovers does
// not leak them, but Close should still be deferred as a matter of course.
//
// # Rewriting an HTTP response
//
// A rewrite in a proxy or a middleware is four lines. Deciding which responses to
// apply it to is the rest of the work, and each of the four headers below is a way
// to break a site.
//
// Content-Encoding first, because it is the one that destroys responses. A
// compressed body is not text, and what a rewrite does to it depends on something
// a reader would not expect - whether a text handler is registered:
//
//	body                     element handlers only   with a text handler
//	gzip of a small page                 identical   longer, and not gzip any more
//	a PNG header                         identical   two bytes longer
//	256 arbitrary bytes                  identical   482 bytes
//	valid UTF-8                          identical   identical
//
// A text handler decodes and re-encodes, so a byte that is not valid in the
// declared encoding becomes U+FFFD - three bytes where there was one. With only
// element handlers nothing decodes and the body passes through untouched, which
// means the rewrite silently did nothing. Neither reports an error. So either ask
// upstream not to compress, or decompress before the rewriter and recompress after.
//
// Content-Type decides whether this is a document at all. Only text/html; a JSON
// body survives a rewrite unchanged and that makes the mistake look harmless until
// a body arrives that is not valid UTF-8.
//
// The charset parameter is the encoding the bytes are in, and it is the authority:
// a meta in the document is ordinary markup to the rewriter. Pass it to
// [WithEncoding]. A label the library cannot use is a reason to pass the body
// through rather than to guess, and the way to find out is to build the rewriter -
// [NewWriter] returns an [EncodingError] both for a label it does not know and for
// one that is not ASCII-compatible. The second matters here: "utf-16le" is a real
// encoding and a real Content-Type, and a rewriter cannot work in it at all.
//
// Content-Length has to go. The rewrite changes the length, and a Content-Length
// that disagrees with the body is a protocol error - the client truncates the page
// or waits for bytes that never come. Nothing in net/http fixes this for you.
//
// Then streaming, which is the reason to rewrite in a proxy rather than in a
// template: flush the response as output arrives, and set FlushInterval on an
// httputil.ReverseProxy. In a middleware, where the rewriter wraps the
// http.ResponseWriter rather than the body, the same thing takes three: forward
// Flush so the handler's own flushes are not stranded, implement Unwrap so
// http.ResponseController can still find Hijack and the deadline setters through
// the wrapper, and delete Content-Length in WriteHeader, since after that the
// header map has gone. Then close the rewriter after the handler returns, which is
// the one thing an ordinary io.Writer chain does not need. Measured, a handler
// writing five chunks forty milliseconds apart reaches the client's first byte
// after 411 microseconds through a streaming middleware and after 210 milliseconds
// through one that buffers; examples/gip/middleware is both. And a failure partway has already sent a prefix of the
// page, headers included, so a broken rewrite cannot become a 502 - see "Stopping
// early" for what the client is left holding.
//
// examples/gip/proxy is all of this in one file; lossytext_test.go gates the table.
//
// # Stopping early
//
// A rewrite over a stream that does not end - or one that only needs its first
// few kilobytes - has two ways to stop, and they answer different questions.
//
// Return an error from a handler when the condition is a place in the document.
// The error identity survives: wrap a sentinel, and errors.Is finds it in what
// Write returns and in what Close returns, the latter under [ErrPoisoned]. Every
// later Write is refused and still carries the cause, so a caller in a loop does
// not have to break out of it on the first error.
//
// What has reached the sink at that point is more than a truncation. It is byte
// for byte what a fresh rewriter produces from that many bytes of the input: no
// half-serialised element, no tag cut in the middle, and the unit whose handler
// stopped is not emitted at all. So the partial output can be kept or served.
// Where the prefix ends depends on which handler stopped:
//
//	an element handler   the bytes before that element's start tag
//	an end-tag handler   the bytes before that end tag
//	a comment handler    the bytes before that comment
//	a text handler       the bytes before that chunk
//
// The last row is the exception to the rest of this section, because a chunk is
// not a position in the document: how many chunks a text node arrives in depends
// on the caller's write sizes, so "stop at the fifth chunk" stops in a different
// place for a different reader upstream. Count to [TextChunk.IsLastInTextNode]
// and the position is the document's again. Measured in earlystop_test.go.
//
// Stop writing and call Close when the condition is the caller's rather than the
// document's - enough data, long enough, a budget spent. Close reports nil, the
// output is a rewrite of what was fed, and nothing is poisoned. The cost is
// granularity: the condition is checked between writes, so the rewrite overshoots
// by up to one write's worth of document. examples/gip/stopwhen runs both
// mechanisms over a stream that never ends and prints what each leaves behind.
//
// Either way Close has to be called, and either way it releases everything: the
// handles a rewrite held are gone afterwards whether it ended at the document's
// end, at a handler's error, or in the middle of a stream nobody intends to
// finish reading.
package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"bytes"
	"strconv"
	"unsafe"
)

// ContentType says how inserted content should be interpreted.
//
// The choice is context-insensitive: Text escapes the same three characters
// wherever the content lands. That is correct in element content, in escapable
// raw text (textarea and title) and inside a comment, and it is wrong
// inside <script> and <style>. See the package documentation on inserting into
// a script or a style.
type ContentType int

const (
	// Text inserts content as text, escaping <, > and & so that none of it can
	// be read as markup. This is the safe choice for untrusted values.
	//
	// It escapes nothing else. A quote, an apostrophe and a backtick pass
	// through, which is correct for element content. So does a NUL, as a literal
	// zero byte, and what a parser then does with it depends on where it landed:
	// measured, a NUL in element content is dropped, one in raw text or a comment
	// becomes U+FFFD, and one in an attribute value is kept. None of those is the
	// value that was written, so a NUL does not survive a round trip; see the
	// package documentation on source being unpreprocessed.
	Text ContentType = iota

	// HTML inserts content as raw markup, parsed as part of the document. The
	// caller is responsible for everything about it, including that it does not
	// end the element it is being inserted into.
	HTML
)

func (ct ContentType) isHTML() bool { return ct == HTML }

func (ct ContentType) String() string {
	if ct == HTML {
		return "html"
	}
	return "text"
}

// SourceLocation is the half-open byte range a unit occupied in the input
// document, counted from the first byte fed to the rewriter.
//
// The bytes fed, before anything is decoded or transcoded. Under [WithEncoding]
// the reported text of a unit is UTF-8 and the range is not: a text chunk reading
// "café" in a windows-1252 document has a four-byte range, because that is what
// the document spent on it. So slicing the input at the range works and measuring
// the reported string does not. The offsets are absolute and unaffected by how the
// document was written in - one byte at a time gives the same numbers as one call -
// which is what makes them usable as identity across two passes, as long as both
// passes are fed the same bytes.
//
// A text chunk is the exception, and it is the one that matters for a proxy reading
// from an io.Reader with a fixed buffer. When a multi-byte character straddles a
// write boundary, the chunk's range covers only the part of it that arrived in the
// last write, or the bytes held over are charged to the chunk already emitted.
// `<p>a€b</p>` fed in one call reports one chunk, 3..8, whose text is its own
// slice. Fed three bytes at a time it reports 3..6 for the text "a" - three bytes
// of range for one byte of text - and fed one byte at a time it reports 3..4 "a",
// 6..7 "€", 7..8 "b", leaving bytes 4 and 5 named by no chunk at all. The text is
// right in every case; the range is not. So for a text chunk, neither the
// write-invariance above nor slicing the input at the range can be relied on.
//
// The way to map the text of a document without depending on the write pattern is
// to take the ranges of the units around it, which do not move: an element, an end
// tag, a comment and a doctype report the same range at every write size, including
// when their own content is multi-byte. Everything between them is text (or a stray
// end tag, below), and it can be read from the caller's own copy of the input -
// which is also how to read the text of a body that is not text at all, since a
// registered text handler decodes and re-encodes and turns every undecodable byte
// into U+FFFD. examples/gip/textmap does this.
//
// What the range covers depends on the unit:
//
//	an element     its start tag, and nothing of its content
//	an end tag     the tag that closed the element, which may belong to an
//	               enclosing one - see [Element.OnEndTag]
//	a comment      the whole token, delimiters included; see [Comment]
//	a doctype      the whole declaration
//	a text chunk   the bytes of that chunk
//
// A range can be empty. The final chunk of a text node carries no bytes, and its
// range is the zero-width point where the node ended - which is the way to find a
// text node's extent, from the first chunk's Start to the last chunk's End. A
// replacement character the rewriter produced for a byte that could not be decoded
// can have one too: fed "caf\xe9" as UTF-8, the chunk reporting U+FFFD stands at a
// point rather than over any bytes. So the length of the reported text and the
// length of the range are unrelated numbers.
//
// Measured in sourcelocation_test.go.
type SourceLocation struct {
	Start int
	End   int
}

// Len reports the length of the range in bytes.
func (s SourceLocation) Len() int { return s.End - s.Start }

func (s SourceLocation) String() string {
	return strconv.Itoa(s.Start) + ".." + strconv.Itoa(s.End)
}

func sourceLocation(c C.lol_html_source_location_bytes_t) SourceLocation {
	return SourceLocation{Start: int(c.start), End: int(c.end)}
}

// Rewrite applies the handlers to a complete HTML document and returns the
// result.
//
// It is a convenience wrapper over [NewWriter] for input already in memory; for
// anything streamed, use NewWriter directly and avoid buffering the whole
// document.
func Rewrite(html []byte, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(html) + len(html)/8)

	w, err := NewWriter(&buf, opts...)
	if err != nil {
		return nil, err
	}
	// Deferred rather than only called on the error paths: a handler that
	// panics is re-raised by Write, and without this the native resources
	// would never be released. Close is idempotent, so the explicit call below
	// still reports the error from the final flush.
	defer w.Close()

	if _, err := w.Write(html); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RewriteString is [Rewrite] for strings.
func RewriteString(html string, opts ...Option) (string, error) {
	out, err := Rewrite(unsafe.Slice(unsafe.StringData(html), len(html)), opts...)
	return string(out), err
}

// Helpers --------------------------------------------------------------------

// emptyBytePtr backs the zero-length case where lol-html would panic-abort on a
// NULL pointer.
var emptyBytePtr = &[1]byte{}

func bytePtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return unsafe.Pointer(emptyBytePtr)
	}
	return unsafe.Pointer(&b[0])
}

// borrowBytes exposes a C buffer as a Go slice without copying.
//
// Valid only for the duration of the call it is handed to. This is used for the
// output sink, where the io.Writer contract already forbids the destination
// from retaining the slice, and where copying every chunk was measurably the
// dominant allocation cost. Anything that needs to outlive the call must copy.
func borrowBytes(p *C.char, n C.size_t) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
}

func quote(s string) string { return strconv.Quote(s) }

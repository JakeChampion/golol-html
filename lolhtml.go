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
// That is worth relying on. There is no cascade, no order-dependence in which
// handlers run, and no way for a rewrite to trigger itself. It also means a
// rewrite cannot act on what another handler produced: that needs a second pass.
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
//	div p     div > p                  descendant and child combinators
//	[a]  [a=v]  [a~=v]  [a|=v]         attribute presence and matching
//	[a^=v]  [a$=v]  [a*=v]
//	[a=v i]   [a=v s]                  case-sensitivity flags
//	:not(x)                            one simple selector only, see below
//	:first-child  :nth-child(2n+1)     odd, even and an+b all work
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
// Tag and attribute names are matched case-insensitively, so "LI" and "li" are
// the same selector and [CLASS=a] matches class="a". An attribute selector
// matches a present-but-empty attribute: [style] matches style="".
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
// attribute.
//
// An unsupported selector is rejected by [NewWriter], not silently ignored, with
// a [SelectorError] naming it and saying which part it could not use.
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
// match "title" and track whether it is inside <svg> or <math> itself, which is
// two more handlers and a counter. examples/gip/envbadge does that.
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
// # Character references are not decoded
//
// Text, comment text and attribute values are reported as raw source: the href
// of <a href="?a=1&amp;b=2"> is "?a=1&amp;b=2". lol-html has to be able to
// re-emit what it read, so it does not decode on the way in, and correspondingly
// escapes what you write. Reading a value and writing it back unchanged is
// therefore correct; comparing one against a decoded Go string is not.
//
// The rule: decide on the decoded form, rewrite the raw one. Use
// html.UnescapeString for the first and leave the value alone for the second.
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
// # Inserting content
//
// Four things about insertion are worth knowing before relying on any of it,
// and each has its own section below: two calls of the same kind do not always
// come out in call order; nothing inserted is dispatched back to your handlers;
// neither content type is right inside a <script> or a <style>; and markup you
// build yourself is the only thing here that is not escaped for you.
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
// # Inserting into a script or a style
//
// Neither [ContentType] is right for the inside of a <script> or a <style>, and
// the failures are quiet in opposite directions.
//
// Those two are *raw text* elements: an HTML parser does not decode character
// references in them. So [Text], which escapes <, > and &, produces content
// that is inert but no longer says what it said:
//
//	e.SetInnerContent(`if (a < b && c > d) {}`, lolhtml.Text)
//	// <script>if (a &lt; b &amp;&amp; c &gt; d) {}</script>
//
// The document is valid, nothing returns an error, and the script throws a
// syntax error in the browser. [Element.Attribute] and the HTML around it look
// exactly as intended, which is why this is easy to ship.
//
// [HTML] inserts the text as written, and then the element ends wherever the
// content says it does:
//
//	e.SetInnerContent(`var s = "</script><img src=1 onerror=alert(1)>";`, lolhtml.HTML)
//	// <script>var s = "</script><img src=1 onerror=alert(1)>";</script>
//
// That is a working injection out of a string literal, and it is the caller's
// responsibility rather than a defect: HTML means raw markup.
//
// There is no combination of the two that makes arbitrary text safe here, and
// escaping it correctly needs to know where in the JavaScript it lands - inside
// a string literal, "</script" has to become "<\/script", which is a JavaScript
// transformation rather than an HTML one. So: build script and style bodies from
// values you control, and if untrusted data has to reach a script, put it in a
// data attribute or a JSON <script type="application/json"> block and read it
// from there.
//
// [Comment.SetText] is the one context where this is checked for you: it refuses
// text containing a comment-closing sequence rather than emitting markup that
// escapes the comment.
//
// A textarea and a title are *escapable* raw text, where references are
// decoded, so Text behaves normally in them.
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
// [EscapeAttribute] are the escaping SetAttribute and Text would have done for
// you. EscapeText is byte for byte what the library applies for Text, which is
// asserted against the library rather than assumed, so a value built into markup
// keeps the guarantee it would have had:
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
// [Element.OnEndTag]:
//
//	lolhtml.OnElement("a", func(e *lolhtml.Element) error {
//		acc.Reset()
//		return e.OnEndTag(func(t *lolhtml.EndTag) error {
//			return t.Before(rewrite(acc.String()), lolhtml.Text)
//		})
//	}),
//	lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
//		acc.WriteString(tc.Text())
//		tc.Remove()
//		return nil
//	})
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
// nothing about the comment distinguishes them. [Comment.SourceLocation] does -
// slice the input at that range and look at whether it starts with "<!--" - and
// that is the only way. A stripper that has the input to hand can check; one
// working from a stream cannot, and should match the comments it wants to keep
// rather than the ones it wants to remove.
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
// attributes on the element. A text handler sees two chunks per text node, the
// content and its empty boundary marker, so it starts at two.
//
// Registering selectors has its own cost, paid once per [NewWriter]: about five
// allocations per distinct selector, one fewer for a repeat, since each distinct
// selector is parsed once and reused. Matching cost grows with the number
// registered as well, on every element - there is no index by tag or class - so a
// tool that registers one handler per rule in a stylesheet pays for all of them
// at every element of the document. Registering a few handlers with broad
// selectors and deciding inside them is cheaper than registering many narrow
// ones.
//
// [Writer.Write] is quadratic at byte granularity while the rewriter is
// buffering an unclosed tag, because each write rescans the pending buffer.
// Network-sized reads are far from this; writing a byte at a time is not.
//
// # Errors
//
// A handler returning a non-nil error stops the rewrite; the error surfaces
// from the [Writer.Write] or [Writer.Close] that was running at the time,
// wrapped in a [HandlerError] you can unwrap. A handler that panics does not
// unwind through Rust: the panic is caught at the boundary and re-raised on the
// goroutine that called Write or Close.
//
// lol-html cannot resume after an error, so a Writer that has failed is
// poisoned and every later Write returns [ErrPoisoned]. A Writer that panics
// releases its native resources on the way out, so a caller who recovers does
// not leak them, but Close should still be deferred as a matter of course.
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
	// zero byte: any parser reading the result replaces it with U+FFFD, so a
	// value containing one does not survive a round trip.
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

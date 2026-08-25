package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"iter"
	"runtime"
	"unsafe"
)

// An Element is a start tag matched by one of your selectors.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type Element struct {
	unit[*C.lol_html_element_t]

	// selector is the selector whose handler was given this element, carried so
	// that an end-tag or streaming handler registered from here can say which
	// handler it belongs to when it fails. Empty for a document-level handler,
	// which has no selector.
	selector string
}

// TagName returns the tag name, lowercased. Use TagNamePreserveCase for the
// spelling as it appeared in the source.
//
// ASCII-lowercased, which is what an HTML parser does and is only the same thing
// for an ASCII name. <DÉTAIL> reports "dÉtail": the D and the TAIL are folded and
// the É is not. So a name with a non-ASCII letter in it comes back in a spelling
// nobody wrote, and comparing it against a lower-cased Go literal fails.
// strings.EqualFold is the fix here, and it is more than a selector can do - a
// selector is folded the same way, so it matches one of the two spellings and not
// both. See the package documentation on selectors, and asciicase_test.go.
func (e *Element) TagName() string {
	p, err := e.live()
	if err != nil {
		return ""
	}
	return takeStr(C.lol_html_element_tag_name_get(p))
}

// TagNamePreserveCase returns the tag name exactly as spelled in the source
// document: <DiV> reports "DiV" and <svg><LINEARGRADIENT> reports
// "LINEARGRADIENT".
//
// As spelled, not as canonical, and for foreign content those differ. A parser
// applies the SVG tag-name adjustment, so a browser's DOM holds "linearGradient"
// however the page wrote it. Neither method here does that:
//
//	source                  TagName            TagNamePreserveCase
//	<linearGradient/>       lineargradient     linearGradient
//	<LINEARGRADIENT/>       lineargradient     LINEARGRADIENT
//	<lineargradient/>       lineargradient     lineargradient
//
// against "linearGradient" from an independent parser in all three cases. So
// comparing either result with a canonical SVG name is wrong for two spellings
// out of three, and which one a page used is not a thing to rely on. Match with a
// selector, which is case-insensitive and gets all three, or lower-case and map
// through the adjustment table yourself.
//
// Nothing is wrong with the output: the source spelling is emitted unchanged and
// a browser adjusts it on the way in, so a passthrough of <LINEARGRADIENT/> is
// still a linearGradient. [Element.SetTagName] writes what it is given, so a
// rewrite can normalise the spelling if it wants to. Pinned in
// differential/tagname_test.go.
func (e *Element) TagNamePreserveCase() string {
	p, err := e.live()
	if err != nil {
		return ""
	}
	return takeStr(C.lol_html_element_tag_name_get_preserve_case(p))
}

// SetTagName renames the element. The matching end tag, if any, is renamed too.
//
// The name is written as given: "sPan" produces <sPan>, unlike
// [Element.SetAttribute], which lower-cases a name it is adding. The first
// character has to be an ASCII letter - a digit or a non-ASCII letter is an error
// - and the characters that could end a tag are refused, as they are there.
//
// Renaming can change what the element's content means, because whether content
// is markup is decided by the element it is in. Renaming one of the raw-text
// elements to an ordinary one turns its text into markup:
//
//	<script>var x = "<img src=x onerror=alert(1)>"</script>
//	SetTagName("div")
//	<div>var x = "<img src=x onerror=alert(1)>"</div>
//
// and the image is now an element. Measured for script, style, textarea and
// title. That is the same hazard [ErrRawTextBreakout] refuses for an insertion,
// and a rename is the way round it - the content is not being inserted, it is
// being reinterpreted, and the library cannot see it coming: this call happens at
// the start tag, before any content has been reported.
//
// The other direction is quieter and still a change of meaning: renaming an
// ordinary element to a raw-text one turns its markup into text, so
// <div><img src=x></div> becomes <script><img src=x></script>, where the image is
// nine words of JavaScript.
//
// So a rename across that boundary is only safe when you know what the content
// is. [IsRawText] answers which side of the boundary a name is on, for the old
// name and the new one. Where you do not know the content, replace the element
// instead - [Element.Replace] with content you built - or read the content and
// decide at the end tag. Pinned in settagname_test.go.
//
// The second thing a rename writes is the closing tag, and where the source left
// the element's end tag out, what it writes over belongs to an enclosing element:
//
//	<h1>a <em>b</h1>          SetTagName("i")  ->  <h1>a <i>b</i>
//
// The </h1> is gone and an </i> stands in its place, so the heading never closes.
// Where a sibling's start tag closed the element, the new tag lands at the end of
// the enclosing element rather than where the element ended:
//
//	<ul><li><em>a<li>b</ul>   SetTagName("i")  ->  <ul><li><i>a<li>b</i>
//
// and "b", which was not emphasised, now is. Both are the general rule that an
// end tag is a token and not a fact about the element; see the package
// documentation, and [Element.OnEndTag] for the name guard that detects it.
// Measured in removeimplied_test.go.
func (e *Element) SetTagName(name string) error {
	p, err := e.live()
	if err != nil {
		return err
	}
	return withName(p, name, "element_tag_name_set", cfElementTagNameSet)
}

// IsSelfClosing reports whether the tag was *written* self-closing, as in
// <foo />. It is about the source text and nothing else.
//
// In HTML a trailing slash is ignored, and this method still returns true for it:
// <div/> reports true, and that div goes on to have content and an end tag like
// any other. So it is not a test for whether an element is empty, and using it as
// one is wrong wherever an author wrote a slash out of habit:
//
//	source              IsSelfClosing   CanHaveContent
//	<div/>              true            true
//	<div></div>         false           true
//	<br/>               true            false
//	<br>                false           false
//	<svg><rect/>        true            false
//	<svg><rect>         false           true
//
// [Element.CanHaveContent] is the method for "may this hold content", and it is
// right in every row above. Measured: <div/>text</div> reaches an OnEndTag
// handler and takes an Append, while <svg><rect/> does neither.
//
// What this is for is foreign content, where the slash is the only thing that
// closes an element - <svg><rect/> against <svg><rect> are two different trees -
// and for a rewrite that wants to reproduce the source spelling.
func (e *Element) IsSelfClosing() bool {
	p, err := e.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_element_is_self_closing(p))
}

// CanHaveContent reports whether the element may contain content. It is false
// for void elements such as <br> and for self-closing foreign elements.
//
// The four methods it governs do not fail alike when it is false. Append,
// Prepend and SetInnerContent - and their streaming forms - silently do nothing.
// OnEndTag returns an error, because there is no end tag to wait for, and that
// error fails the rewrite. So a handler on a selector that can match a void
// element must check this before calling OnEndTag; the others can be called
// blind.
//
// Before, After and Replace are unaffected: they position content outside the
// element and work on a void one.
//
// It reports true for <plaintext>, and that is correct - plaintext has content -
// but it is not the guarantee it looks like. A plaintext element ends only at the
// end of the input, so there is no end tag: OnEndTag returns nil and its handler
// never runs, and Append has no position and is dropped without error. Prepend
// and SetInnerContent work. Pinned in rawtext_test.go.
func (e *Element) CanHaveContent() bool {
	p, err := e.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_element_can_have_content(p))
}

// NamespaceURI returns the namespace that this element's children are parsed in,
// which is not always the element's own namespace.
//
// For almost everything the two are the same: "http://www.w3.org/1999/xhtml" in
// HTML content, "http://www.w3.org/2000/svg" inside <svg>,
// "http://www.w3.org/1998/Math/MathML" inside <math>. The exceptions are the
// integration points, where foreign content switches back to HTML parsing. Those
// elements report the HTML namespace even though they are SVG or MathML elements:
//
//	<svg>: foreignObject, desc, title
//	<math>: mi, mo, mn, ms, mtext
//	<math>: annotation-xml, but only when its encoding attribute is
//	        "text/html" or "application/xhtml+xml"
//
// Measured, not derived from the spec: <svg><title> reports the HTML namespace,
// and so does <math><mi>, while <svg><a>, <svg><script>, <svg><style> and
// <math><mrow> report their own.
//
// The consequence is worth stating plainly, because it removes the obvious use
// for this method. Selectors do not consider namespaces either - "title" matches
// both a document title and an SVG tooltip - so a handler that needs to tell
// them apart cannot do it with a selector alone and cannot do it with this
// method either, since both report HTML. What works is a selector that names the
// context ("svg title" matches only the tooltip), or the rule below.
//
// # The element's own namespace
//
// Read it one level up. An element is parsed in the namespace its parent's
// children are parsed in, which is exactly what this method reports for the
// parent, so a handler that keeps a stack of these values has the answer for
// every element at the top of it:
//
//	ns := []string{lolhtml.NamespaceHTML}
//	lolhtml.OnElement("*", func(e *lolhtml.Element) error {
//		own := ns[len(ns)-1]
//		if child := e.NamespaceURI(); child != own && e.CanHaveContent() {
//			ns = append(ns, child)
//			tag := e.TagName()
//			e.OnEndTag(func(t *lolhtml.EndTag) error {
//				if t.Name() == tag {
//					ns = ns[:len(ns)-1]
//				}
//				return nil
//			})
//		}
//		return nil
//	})
//
// with one exception: <svg> and <math> are the tags that enter foreign content,
// so they are themselves foreign and their own namespace is the one they report.
//
// Counting <svg> and <math> depth instead is the obvious thing and it is wrong
// at the integration points, in the direction that matters: it puts the <p> in
// <svg><foreignObject><p> in the SVG namespace, where it is an ordinary HTML
// paragraph. The stack gets that right because the switch back is exactly what
// foreignObject reports. Worked through in examples/gip/histogram.
//
// The returned string is one of [NamespaceHTML], [NamespaceSVG] and
// [NamespaceMathML], and is those constants rather than a copy of them: a fresh
// 28- or 32-byte string per element was measured at 1000 allocations and 32 KB
// per 1000 elements, for three distinct values.
func (e *Element) NamespaceURI() string {
	p, err := e.live()
	if err != nil {
		return ""
	}
	// Unlike the lol_html_str_t getters, this returns a static NUL-terminated
	// string that must not be freed - and must not be handed out either, so it
	// is compared against the constants and one of those is returned.
	return namespaceConstant(C.lol_html_element_namespace_uri_get(p))
}

// The three namespaces an element can be parsed in. [Element.NamespaceURI]
// returns one of these, so comparing its result against them compares two
// constants.
const (
	NamespaceHTML   = "http://www.w3.org/1999/xhtml"
	NamespaceSVG    = "http://www.w3.org/2000/svg"
	NamespaceMathML = "http://www.w3.org/1998/Math/MathML"
)

// namespaceConstant maps lol-html's static namespace string onto one of the
// constants without copying it.
//
// The view built here borrows C memory and must not escape: comparing a string
// against a constant does not copy it, and what is returned is the constant,
// which Go owns. Anything unrecognised is copied instead, which cannot happen
// with lol-html v3 and would be a silent wrong answer if it were dropped.
func namespaceConstant(p *C.char) string {
	if p == nil {
		return ""
	}
	s := unsafe.String((*byte)(unsafe.Pointer(p)), cStringLen(p))
	switch s {
	case NamespaceHTML:
		return NamespaceHTML
	case NamespaceSVG:
		return NamespaceSVG
	case NamespaceMathML:
		return NamespaceMathML
	}
	return string(s)
}

// cStringLen is strlen without the cgo call, which for strings this short costs
// more than the scan.
func cStringLen(p *C.char) int {
	b := unsafe.Pointer(p)
	n := 0
	for *(*byte)(unsafe.Add(b, n)) != 0 {
		n++
	}
	return n
}

// SourceLocation returns the byte range the start tag occupied in the input -
// the start tag alone, not the element.
//
// For the element's whole extent, hold this Start and take the End from the end
// tag's own location - but only once the end tag is known to be this element's,
// because an omitted end tag hands the handler an enclosing element's tag and
// this arithmetic then measures to the end of that one instead:
//
//	start, tag := e.SourceLocation().Start, e.TagName()
//	e.OnEndTag(func(t *lolhtml.EndTag) error {
//		if t.Name() != tag {
//			return nil // not this element's end tag; see OnEndTag
//		}
//		extent := t.SourceLocation().End - start
//		return nil
//	})
//
// Without the guard, both items in <ul><li>a<li>b</ul> measure as reaching the
// end of the list. An element whose end tag never arrives has no measurable
// extent at all, because the handler never runs.
func (e *Element) SourceLocation() SourceLocation {
	p, err := e.live()
	if err != nil {
		return SourceLocation{}
	}
	return sourceLocation(C.lol_html_element_source_location_bytes(p))
}

// Attributes ------------------------------------------------------------------

// Attribute returns the value of the named attribute. Names are matched
// case-insensitively. The second result is false if the attribute is absent,
// which distinguishes it from an attribute present with an empty value.
//
// It does not distinguish either of those from an element that is no longer
// valid: a detached element reports ("", false) for everything. [HasAttribute]
// does, because its signature has room for an error, and [Detached] answers
// directly. See [ErrDetached].
//
// The value is live rather than a snapshot: a read after [SetAttribute] or
// [RemoveAttribute] in the same handler sees the change, unlike [TextChunk.Text],
// which is always the source.
//
// An element can carry the same attribute twice - the HTML parsing specification
// calls that a parse error and requires a parser to drop all but the first, and
// lol-html keeps them all. Where that shows up is set out under "An attribute
// can appear twice" in the package documentation; the short version is that this
// method returns the first, which is the one a browser would have.
//
// The value is raw source text, with character references left encoded: the
// href of <a href="?a=1&amp;b=2"> is "?a=1&amp;b=2", not "?a=1&b=2". Use
// html.UnescapeString from the standard library if you need the decoded form.
//
// SetAttribute is the mirror image and takes raw source text too, escaping only
// the double quote, so a value read here and written straight back is
// unchanged. It does mean writing the five characters "&amp;" produces the
// single character "&" for whoever parses the result.
//
// One quirk to know: lol-html decodes on the way out and its decoder removes a
// leading byte-order mark, so a value starting with U+FEFF reads back without
// it. The value is still serialised faithfully, and a U+FEFF anywhere but the
// first position survives.
func (e *Element) Attribute(name string) (string, bool) {
	p, err := e.live()
	if err != nil {
		return "", false
	}
	np, nl := strPtr(name)
	s := C.lol_html_element_get_attribute(p, np, nl)
	runtime.KeepAlive(name)
	return takeOptStr(s)
}

// HasAttribute reports whether the named attribute is present.
func (e *Element) HasAttribute(name string) (bool, error) {
	p, err := e.live()
	if err != nil {
		return false, err
	}
	np, nl := strPtr(name)
	var cerr C.lol_html_str_t
	rc := C.golol_element_has_attribute(p, np, nl, &cerr)
	runtime.KeepAlive(name)
	if rc < 0 {
		return false, nativeErr("element_has_attribute", cerr)
	}
	return rc == 1, nil
}

// SetAttribute sets the named attribute, adding it if absent.
//
// The name's case is not kept when the attribute is being added: it is
// lower-cased, and there is no way to write a name with a capital in it that was
// not already in the document. Updating one that is there keeps the document's
// spelling, so the two directions differ:
//
//	<svg viewBox="0 0 1 1">    SetAttribute("viewBox", "0 0 9 9")  ->  viewBox="0 0 9 9"
//	<svg>                      SetAttribute("viewBox", "0 0 9 9")  ->  viewbox="0 0 9 9"
//
// In HTML that is nothing: attribute names are matched case-insensitively, and
// [Attributes] lower-cases them for the same reason. In SVG and MathML it is a
// silent breakage, because there the names are case-sensitive - viewbox is not
// viewBox, and a browser ignores it. The spec's list of the ones that need a
// capital runs to about sixty names, viewBox, preserveAspectRatio,
// gradientTransform, patternUnits, refX, textLength, stdDeviation and
// zoomAndPan among them.
//
// So a rewrite that reads an SVG attribute, computes a new value and writes it
// back works, and the same code adding the attribute to an element that did not
// have it produces one a browser will not read. Where the attribute has to be
// added, the tag has to be written: [Element.Replace] with markup you build, or
// the value carried into the document some other way. [Attribute.NamePreserveCase]
// is the read side of the same problem, and is how a rebuild keeps the spelling.
//
// The name is checked, and the characters it refuses are the ones that could end
// the attribute or start another: a space, a tab, a newline, "/", "=", ">", and
// the empty name. Those return an error rather than producing markup, so a name
// taken from a document cannot break the tag it is written into. What it accepts
// includes the merely odd - a quote, an apostrophe, a "<", a leading digit - each
// of which reads back as part of the name. [Element.SetTagName] is stricter: it
// requires the first character to be an ASCII letter, and it does not lower-case
// what it is given. Measured in attrnamecase_test.go.
//
// The value is raw attribute-value source, the mirror of what Attribute
// reports, so it needs no escaping from the caller: a value read from one
// element and written to another is unchanged, and a value containing a quote
// cannot end the attribute or start another. The only character rewritten on
// the way out is the double quote, which becomes &quot;.
//
// Raw source is not the same as text, and the difference is worth one sentence
// because it is silent when it bites. Passing the five characters "&amp;" sets
// an attribute that a browser reads as the single character "&", because that
// is what those five characters mean in an attribute. If what you have is a
// literal value rather than source - a string that should arrive at the other
// end byte for byte - encode it first with EscapeAttribute.
//
// Escaping is not sanitising, either. SetAttribute will set href to
// "javascript:alert(1)" without complaint, because that is a valid attribute
// value; which schemes to allow is the caller's decision.
//
// A boolean attribute comes out with a value. There is no way to write a bare
// one: SetAttribute("defer", "") emits defer="" and the C API takes no other
// shape. That is the same attribute as far as any parser is concerned - presence
// is what a boolean attribute means - but it does not match the spelling a page
// used, so a rewrite that adds one to a document full of bare attributes produces
// a diff in two styles. A bare attribute already in the input is passed through
// unchanged and reads back as an empty value.
//
// It writes the first copy and leaves the others, which is the opposite choice
// from [Element.RemoveAttribute] and the more dangerous one:
//
//	<a href="first" href="second">
//	e.SetAttribute("href", "safe")
//	<a href="safe" href="second">
//
// A browser reads the first, so the rewrite took effect there. The original is
// still in the bytes, and RemoveAttribute's reasoning applies here too: what a
// browser drops on parse is not necessarily what the next parser in the chain
// drops. A rewrite that sanitises by changing a value rather than removing it
// leaves the value it was sanitising.
//
// Remove first where that matters:
//
//	e.RemoveAttribute("href")      // every copy
//	e.SetAttribute("href", "safe") // one copy, at the end
//
// which costs the attribute its position. That is why this is not done for you:
// finding out whether a name is duplicated means listing every attribute, on
// every call to the most-used method in the package, to change the answer for the
// documents that have a duplicate and move the attribute in all the rest. See "An
// attribute can appear twice" in the package documentation.
func (e *Element) SetAttribute(name, value string) error {
	p, err := e.live()
	if err != nil {
		return err
	}
	np, nl := strPtr(name)
	vp, vl := strPtr(value)
	var cerr C.lol_html_str_t
	rc := C.golol_element_set_attribute(p, np, nl, vp, vl, &cerr)
	runtime.KeepAlive(name)
	runtime.KeepAlive(value)
	if rc != 0 {
		// The name and the value are both content this call was given, so either
		// could be the invalid one; the classification says only that one of them
		// is. See ErrInvalidUTF8.
		return nativeErrFor("element_set_attribute", cerr, name+value)
	}
	return nil
}

// RemoveAttribute removes the named attribute. Removing an absent attribute is
// not an error.
//
// Every copy goes, not just the first, which is deliberate: a filter that left a
// second copy behind would be a filter that does not filter, since what a
// browser drops on parse is not necessarily what the next parser in the chain
// drops. See "An attribute can appear twice" in the package documentation.
func (e *Element) RemoveAttribute(name string) error {
	p, err := e.live()
	if err != nil {
		return err
	}
	np, nl := strPtr(name)
	var cerr C.lol_html_str_t
	rc := C.golol_element_remove_attribute(p, np, nl, &cerr)
	runtime.KeepAlive(name)
	if rc != 0 {
		return nativeErrFor("element_remove_attribute", cerr, name)
	}
	return nil
}

// An Attribute is one attribute of an element.
type Attribute struct {
	// Name is the attribute name, lowercased.
	Name string
	// NamePreserveCase is the name as spelled in the source, which matters for
	// foreign content such as SVG's viewBox.
	NamePreserveCase string
	// Value is the attribute value as it appeared in the source, with
	// character references left encoded. See Element.Attribute.
	Value string
}

// Attributes iterates the element's attributes in source order, yielding
// lowercased names. Use AttributeList when the original spelling matters.
//
// Mutating the element while iterating is safe, and the iteration is over the
// attributes as they were: setting or removing one inside the loop takes effect
// on the element and does not disturb the walk, and an attribute added inside the
// loop is not visited. Measured - adding one per iteration terminates at the
// original count rather than growing without end.
//
// Like AttributeList, this yields repeats of the same name rather than the first
// only - see "An attribute can appear twice" in the package documentation.
//
// The iterator must be consumed inside the handler; once the element is
// detached it yields nothing.
func (e *Element) Attributes() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, a := range e.AttributeList() {
			if !yield(a.Name, a.Value) {
				return
			}
		}
	}
}

// AttributeList returns every attribute of the element, in source order.
//
// Every attribute means every one, including repeats of the same name that a
// browser's parser would have dropped. That is the opposite of what Attribute
// and the selectors do, and it is the choice that matters for anything reporting
// on a document rather than rewriting it: see "An attribute can appear twice" in
// the package documentation.
//
// This collects eagerly rather than iterating lazily because the underlying
// lol-html iterator invalidates each attribute when the next is fetched, and
// hands out pointers valid only while the element is.
func (e *Element) AttributeList() []Attribute {
	p, err := e.live()
	if err != nil {
		return nil
	}

	it := C.lol_html_attributes_iterator_get(p)
	if it == nil {
		return nil
	}
	defer C.lol_html_attributes_iterator_free(it)

	var out []Attribute
	for {
		a := C.lol_html_attributes_iterator_next(it)
		if a == nil {
			return out
		}
		out = append(out, Attribute{
			Name:             takeStr(C.lol_html_attribute_name_get(a)),
			NamePreserveCase: takeStr(C.lol_html_attribute_name_get_preserve_case(a)),
			Value:            takeStr(C.lol_html_attribute_value_get(a)),
		})
	}
}

// Content --------------------------------------------------------------------

// Before inserts content immediately before the element's start tag.
func (e *Element) Before(content string, ct ContentType) error {
	return e.content(content, ct, "element_before", cfElementBefore)
}

// After inserts content immediately after the element's end tag.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
//
// Where the element's end is depends on the source having an end tag for it. An
// element whose end tag HTML lets a document omit - a list item, a table cell, a
// paragraph - ends here at the enclosing element's end tag instead, so this
// writes somewhere else: see the package documentation on end tags being tokens.
func (e *Element) After(content string, ct ContentType) error {
	return e.content(content, ct, "element_after", cfElementAfter)
}

// Prepend inserts content as the element's first child.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
//
// Calling this after Remove still emits the content, without the element's tags
// around it; see the package documentation on removal.
func (e *Element) Prepend(content string, ct ContentType) error {
	return e.content(content, ct, "element_prepend", cfElementPrepend)
}

// Append inserts content as the element's last child.
//
// Where the element's end is depends on the source having an end tag for it. An
// element whose end tag HTML lets a document omit - a list item, a table cell, a
// paragraph - ends here at the enclosing element's end tag instead, so this
// writes somewhere else: see the package documentation on end tags being tokens.
//
// Calling this after Remove still emits the content, without the element's tags
// around it; see the package documentation on removal.
func (e *Element) Append(content string, ct ContentType) error {
	return e.content(content, ct, "element_append", cfElementAppend)
}

// SetInnerContent replaces everything inside the element, leaving its tags in
// place.
//
// "Everything inside it" reaches to the next end tag. For an element whose own
// end tag the source left out, that is the enclosing element's, so this replaces
// the enclosing element's remaining content too: see the package documentation
// on end tags being tokens.
//
// Calling this after Remove still emits the content, without the element's tags
// around it; see the package documentation on removal.
func (e *Element) SetInnerContent(content string, ct ContentType) error {
	return e.content(content, ct, "element_set_inner_content", cfElementSetInnerContent)
}

// Replace replaces the element, including its tags, with content.
//
// Where the element's end is depends on the source having an end tag for it. An
// element whose end tag HTML lets a document omit - a list item, a table cell, a
// paragraph - ends here at the enclosing element's end tag instead, so this
// writes somewhere else: see the package documentation on end tags being tokens.
func (e *Element) Replace(content string, ct ContentType) error {
	return e.content(content, ct, "element_replace", cfElementReplace)
}

func (e *Element) content(content string, ct ContentType, op string, fn contentOp[*C.lol_html_element_t]) error {
	p, err := e.live()
	if err != nil {
		return err
	}
	if ct.isHTML() {
		// Only for insertions into the element's own content; Before, After and
		// Replace go through the same helper but write outside it.
		switch op {
		case "element_prepend", "element_append", "element_set_inner_content":
			if err := checkRawText(e.TagName(), content); err != nil {
				return err
			}
		}
	}
	return withContent(p, content, ct.isHTML(), op, fn)
}

// Remove removes the element and everything inside it.
//
// "Everything inside it" is everything up to the next end tag, which is not the
// same thing when the source left this element's end tag out. Removing the first
// item of <ul><li>a<li>b<li>c</ul> removes all three, with no error: see the
// package documentation on end tags being tokens.
//
// Nor is it the same thing as everything a parser would say is inside it. A
// table is the case: content a parser moves out of one - text between <table>
// and the first <tr>, for instance - is inside it here, so removing the table
// removes content a tree-based edit would keep. See the package documentation on
// a table containing things that are not in it.
//
// Handlers still run for the content being removed, and their edits are
// discarded with it. Content inserted inside the element after this call is not
// discarded, which is a corner worth reading about: see the package
// documentation on removal.
func (e *Element) Remove() {
	if p, err := e.live(); err == nil {
		C.lol_html_element_remove(p)
	}
}

// RemoveAndKeepContent removes the element's tags but keeps its children, so
// <b>hi</b> becomes hi.
//
// "Its children" is what was inside the tags, and unwrapping one of the ten
// elements whose content is not markup turns that content into markup:
//
//	<script>var x = "<img src=x onerror=alert(1)>"</script>
//	e.RemoveAndKeepContent()
//	var x = "<img src=x onerror=alert(1)>"
//
// and the image is now an element. Measured for script, style, textarea, title,
// xmp, iframe, noembed, noframes, noscript and plaintext.
//
// That matters most for the shape this method invites: a sanitiser with an
// allowlist that unwraps everything not on it. Very few allowlists include
// noembed or xmp, so a payload placed inside one is inert until it is unwrapped -
// which is the sanitiser doing the work. Where the content of an unknown element
// might not be markup, remove the element instead, or ask [IsRawText] before
// unwrapping - the tag name is enough, and the answer is the same list the
// library checks insertions against.
//
// This is the same hazard as [ErrRawTextBreakout] and [Element.SetTagName],
// reached a third way: nothing is inserted and nothing is renamed, and the
// content is reinterpreted all the same. Pinned in settagname_test.go.
//
// The other thing this removes is the token that closed the element, and that is
// not always the element's own end tag. Where the source left the end tag out,
// the token that closed it belongs to an enclosing element, and it goes too:
//
//	<h1>a <em>b</h1><p>after</p>
//	em.RemoveAndKeepContent()
//	<h1>a b<p>after</p>
//
// The heading never closes, so the paragraph is now inside it. All the content is
// still there and nothing reports anything: the change is to the shape of the
// document rather than to what it says. The element that loses its tag need not
// be one the call named or an ancestor the caller was thinking about:
//
//	<h1><span>a <em>b</span> c</h1>
//	em.RemoveAndKeepContent()
//	<h1><span>a b c</h1>
//
// [Element.Remove] and [Element.Replace] have the same cause and the opposite
// symptom - they take the content up to that token with them, which is what
// those methods describe. Here the content survives and the nesting does not.
//
// The name guard on [Element.OnEndTag] detects it. Register the handler before
// removing, and a callback whose [EndTag.Name] is not this element's name is
// standing on the token that has just been deleted, so writing it back there
// repairs the document:
//
//	name := e.TagName()
//	e.OnEndTag(func(t *lolhtml.EndTag) error {
//		if t.Name() == name {
//			return nil // its own end tag; nothing borrowed
//		}
//		return t.Before("</"+t.Name()+">", lolhtml.HTML)
//	})
//	e.RemoveAndKeepContent()
//
// Measured in removeimplied_test.go, along with what happens without it.
//
// Appending after this is well defined, and puts the content after the children
// that were kept.
func (e *Element) RemoveAndKeepContent() {
	if p, err := e.live(); err == nil {
		C.lol_html_element_remove_and_keep_content(p)
	}
}

// IsRemoved reports whether the element has been removed by a handler, whether
// by Remove or by RemoveAndKeepContent. It does not distinguish them, so it
// cannot be used to decide whether inserting inside the element is safe - only
// whether the element itself will be emitted.
//
// It also answers for an ancestor. An element inside one that a handler has
// already removed with [Element.Remove] or [Element.Replace] reports true,
// because it is on its way out with everything else in there - so a handler
// accumulating over a document does not have to keep a depth counter to know that
// what it is looking at will not be in the output. [Element.RemoveAndKeepContent]
// is the exception, and the right one: the content is being kept, so a descendant
// reports false.
//
// The other units do not work this way. [TextChunk.IsRemoved] and
// [Comment.IsRemoved] report only whether that chunk or comment has itself been
// removed, so a text handler inside a removed element sees false and has to learn
// it some other way - an element handler tracking removed ancestors, which is what
// the package documentation on removal describes. Measured in
// removedsubtree_test.go.
func (e *Element) IsRemoved() bool {
	p, err := e.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_element_is_removed(p))
}

// End tags -------------------------------------------------------------------

// OnEndTag registers fn to run when this element's content ends, which is the
// way to act on an element after seeing what is inside it.
//
// "When its content ends" is not the same as "at its own end tag", and the
// difference is silent. HTML lets many elements leave their end tag out - a list
// item closed by the next item, a table cell closed by the next row, a paragraph
// closed by anything that cannot be inside one - and for those there is no end
// tag in the source. The handler then runs against the tag that did close them,
// which belongs to an enclosing element:
//
//	<ul><li>a<li>b</ul>
//
// Both items' handlers run at </ul>, innermost first, and [EndTag.Name] reports
// "ul" for both. Content inserted with [EndTag.Before] lands at the end of the
// list rather than at the end of an item, and nothing reports a problem:
//
//	<ul><li>a<li>b[end][end]</ul>
//
// If nothing closes the element at all - it runs to the end of the document, as
// in <p>a<p>b - the handler does not run.
//
// The test is the name. An end tag closes the nearest open element of that name,
// which is the element itself, so a name that differs is not this element's end
// tag and no position taken from it belongs to this element:
//
//	tag := e.TagName()
//	e.OnEndTag(func(t *lolhtml.EndTag) error {
//		if t.Name() != tag {
//			return nil // closed implicitly; this position is somewhere else
//		}
//		return t.Before("<span class=\"marker\"></span>", lolhtml.HTML)
//	})
//
// Measured across every shape in endtagposition_test.go, against what the source
// spells at that position.
//
// A handler that only wants to know that the element is over, rather than to
// write at its position, needs a finer distinction than that guard makes. There
// are three timings, not two:
//
//	<p><em>a</em> b</p>       at </em>, its own tag, exactly where it ends
//	<p><em>a</p>b             at </p>, an ancestor's tag, exactly where it ends
//	<ul><li><em>a<li>b</ul>   at </ul>, an ancestor's tag, and "b" was already
//	                          reported: the <em> ended at the second <li>
//
// So a foreign end tag is where the element ended when an ancestor's end tag is
// what closed it, and later than where it ended when a sibling's start tag was.
// Nothing in the callback separates those, and the difference matters to anything
// accumulating - a converter closing an emphasis at the third row's callback
// wraps the next item's text as well.
//
// The only way to be exact is to keep the stack of open elements and apply the
// specification's implied end tags, which is what examples/gip/markdown and
// examples/gip/depth do. Pinned in endtagposition_test.go.
//
// It can be called more than once to register several handlers, which run in
// registration order. It fails if the element cannot have content - a void
// element such as <br> has no end tag to wait for - so check CanHaveContent
// first when the tag is not known statically. It also returns nil and never runs
// for <plaintext>, which has no end tag at all; see CanHaveContent.
//
// Each registration costs memory until the rewrite ends, not until the end tag
// arrives. Measured on 100,000 sibling <div>s with a handler on "*" that registers
// one end-tag handler each: the live handle count rises to 100,001 and never falls
// until the Writer is closed, and the Go side allocates about 30 MB against about
// 6 MB for the same rewrite without the registration - roughly 300 bytes per
// element, and the same for a wide document as for a deep one.
//
// [MemorySettings.MaxMemory] does not bound it: the same document completes under a
// 64 KiB limit while allocating those 30 MB, because that limit is lol-html's
// parsing buffer and this is the binding's handle table. So a rewrite that must
// hold a memory budget has to bound its input, and register this only where an
// element actually needs it - a narrow selector, or a condition checked before
// registering rather than inside the callback. Measured in endtagcost_test.go.
func (e *Element) OnEndTag(fn func(*EndTag) error) error {
	p, err := e.live()
	if err != nil {
		return err
	}

	// The C API offers no drop callback for end-tag handlers, so the handle
	// lives on the rewriter and is released with it.
	h := e.c.nt.newHandle(&endTagCB{c: e.c, selector: e.selector, fn: fn})

	var cerr C.lol_html_str_t
	if C.golol_element_add_end_tag_handler(p, C.uintptr_t(h), &cerr) != 0 {
		return nativeErr("element_add_end_tag_handler", cerr)
	}
	return nil
}

// ClearEndTagHandlers removes every end-tag handler registered for this
// element, including any added by handlers that ran before this one.
func (e *Element) ClearEndTagHandlers() {
	if p, err := e.live(); err == nil {
		C.lol_html_element_clear_end_tag_handlers(p)
	}
}

// User data ------------------------------------------------------------------

var elementUserData = userDataAccessor[*C.lol_html_element_t]{
	get: func(p *C.lol_html_element_t) C.uintptr_t { return C.golol_element_user_data_get(p) },
	set: func(p *C.lol_html_element_t, h C.uintptr_t) { C.golol_element_user_data_set(p, h) },
}

// UserData returns the value most recently attached by SetUserData, or nil.
func (e *Element) UserData() any { return getUserData(&e.unit, elementUserData) }

// SetUserData attaches a value to this element, readable by another handler that
// is given the same element.
//
// "The same element" means one reported to two handlers, which happens when two
// selectors match it:
//
//	OnElement(".card", func(e *lolhtml.Element) error { return e.SetUserData(n) })
//	OnElement("[data-id]", func(e *lolhtml.Element) error { n := e.UserData() ... })
//
// Not an end-tag handler, which is what this documentation used to say and is not
// possible: [EndTag] has no user data - lol-html provides it for elements,
// comments, text chunks and the doctype, and not for end tags - and the element
// itself is detached by the time its end-tag handler runs, so reading through the
// captured [Element] returns nil. Measured. Close over a Go variable instead,
// which is what an end-tag handler is written inside a start-tag handler for.
//
// It is per unit and not per position: two elements never share it, and a value
// set on one text chunk is not readable from the next chunk of the same text
// node. The attached value is released with the Writer.
//
// Go handlers can usually just close over the value instead, which is why this
// exists mainly for parity with the C API.
func (e *Element) SetUserData(v any) error { return setUserData(&e.unit, elementUserData, v) }

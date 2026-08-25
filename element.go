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
}

// TagName returns the tag name, lowercased. Use TagNamePreserveCase for the
// spelling as it appeared in the source.
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
		return nativeErr("element_set_attribute", cerr)
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
		return nativeErr("element_remove_attribute", cerr)
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
func (e *Element) OnEndTag(fn func(*EndTag) error) error {
	p, err := e.live()
	if err != nil {
		return err
	}

	// The C API offers no drop callback for end-tag handlers, so the handle
	// lives on the rewriter and is released with it.
	h := e.c.nt.newHandle(&endTagCB{c: e.c, fn: fn})

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

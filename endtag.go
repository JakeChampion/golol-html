package lolhtml

/*
#include "shim.h"
*/
import "C"

import "unicode/utf8"

// An EndTag is a closing tag, delivered to a handler registered with
// [Element.OnEndTag]. It is the hook for acting on an element once its content
// has been seen.
//
// It is the tag that closed the element, which is not always the element's own:
// see [Element.OnEndTag] on end tags HTML lets a document leave out.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type EndTag struct {
	unit[*C.lol_html_end_tag_t]

	// selector is the selector of the element handler that registered this
	// end-tag handler; see the same field on Element.
	selector string

	// parsedName is the name the tag arrived with, captured by the first SetName
	// so that the raw-text check in Before keeps keying on the element the
	// content was tokenised inside. Empty until a rename.
	parsedName string
}

// Name returns the tag name, lowercased.
//
// It is not necessarily the name of the element whose handler this is. An
// element that left its end tag out is closed by an enclosing element's, and the
// handler is handed that one: in <ul><li>a<li>b</ul>, both items' handlers see a
// tag named "ul". Comparing this against the element's own tag name is how a
// handler tells the two apart; see [Element.OnEndTag].
func (t *EndTag) Name() string {
	p, err := t.live()
	if err != nil {
		return ""
	}
	return takeStr(C.lol_html_end_tag_name_get(p))
}

// NamePreserveCase returns the tag name as spelled in the source.
func (t *EndTag) NamePreserveCase() string {
	p, err := t.live()
	if err != nil {
		return ""
	}
	return takeStr(C.lol_html_end_tag_name_get_preserve_case(p))
}

// SetName renames the end tag. Renaming only this tag, and not the matching
// start tag, produces mismatched markup.
//
// The name is checked the way [Element.SetTagName] checks one: it must not be
// empty, must start with an ASCII letter, and must not contain a character
// that ends a tag name - whitespace, "/" or ">". lol-html checks a start tag's
// name and not an end tag's, and the difference was a way to write arbitrary
// bytes between "</" and ">": a name of `a>b` produced `</a>b>`, which a
// parser reads as an end tag followed by text, and one carrying a "<" opened a
// new element. Refused here with the same messages, so a name that is wrong
// is wrong in both places for the same reason.
func (t *EndTag) SetName(name string) error {
	p, err := t.live()
	if err != nil {
		return err
	}
	if err := checkTagName("end_tag_name_set", name); err != nil {
		return err
	}
	if t.parsedName == "" {
		t.parsedName = t.Name()
	}
	return withName(p, t.c.nt.cerr, name, "end_tag_name_set", cfEndTagNameSet)
}

// checkTagName applies lol-html's own start-tag name rules to a name, with its
// own messages, so that the two renames refuse the same names the same way.
// Invalid UTF-8 is left to lol-html, which checks it first and reports it as
// ErrInvalidUTF8; the rules here are only meaningful for a string that is text.
func checkTagName(op, name string) error {
	if !utf8.ValidString(name) {
		return nil
	}
	if name == "" {
		return &NativeError{Op: op, Message: "Tag name can't be empty."}
	}
	if c := name[0]; !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
		return &NativeError{Op: op, Message: "The first character of the tag name should be an ASCII alphabetical character."}
	}
	for i := 0; i < len(name); i++ {
		switch c := name[i]; c {
		case ' ', '\t', '\n', '\f', '\r', '/', '>':
			return &NativeError{Op: op, Message: "`" + string(c) + "` character is forbidden in the tag name"}
		}
	}
	return nil
}

// SourceLocation returns the byte range the end tag occupied in the input.
func (t *EndTag) SourceLocation() SourceLocation {
	p, err := t.live()
	if err != nil {
		return SourceLocation{}
	}
	return sourceLocation(C.lol_html_end_tag_source_location_bytes(p))
}

// Before inserts content immediately before the end tag, making it the last
// content inside the element.
//
// This is the insertion to reach for where [Element.Append] would also do, because
// the two differ when the source omits an end tag. Applied to every item of
// <ul><li>a<li>b<li>c</ul>, where all three handlers run at the single </ul>,
// this keeps all three insertions - innermost first - and Append keeps one.
// Neither lands at the item's own end, which the source does not have, but only
// one of them loses content, and neither reports anything.
func (t *EndTag) Before(content string, ct ContentType) error {
	return t.content(content, ct, "end_tag_before", cfEndTagBefore)
}

// After inserts content immediately after the end tag, making it the first
// content following the element.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
func (t *EndTag) After(content string, ct ContentType) error {
	return t.content(content, ct, "end_tag_after", cfEndTagAfter)
}

func (t *EndTag) content(content string, ct ContentType, op string, fn contentOp[*C.lol_html_end_tag_t]) error {
	p, err := t.live()
	if err != nil {
		return err
	}
	// Before an end tag is inside the element; After is outside it. The check
	// keys on the name the tag was parsed with: the content in front of it was
	// tokenised as raw text under that name and stays raw text whatever the tag
	// is renamed to, so a rename to "div" must not switch the check off.
	if ct.isHTML() && op == "end_tag_before" {
		name := t.parsedName
		if name == "" {
			name = t.Name()
		}
		if err := checkRawText(name, content); err != nil {
			return err
		}
	}
	return withContent(p, t.c.nt.cerr, content, ct.isHTML(), op, fn)
}

// Remove removes the end tag, leaving the element's content in place.
//
// The token being removed is not always the element's own end tag. Where the
// source left the end tag out, the callback runs against the token that closed
// the element, which belongs to an enclosing element - so this removes that
// element's closing tag and it never closes:
//
//	<h1>a <em>b</h1><p>after</p>
//	// in the em's end tag handler
//	t.Remove()
//	<h1>a <em>b<p>after</p>
//
// [EndTag.Name] is the test: a name that is not this element's is a token that
// belongs to something else, and removing it is almost never what the handler
// meant. See [Element.OnEndTag], and removeimplied_test.go for the measurement.
func (t *EndTag) Remove() {
	if p, err := t.live(); err == nil {
		C.lol_html_end_tag_remove(p)
	}
}

package lolhtml

/*
#include "shim.h"
*/
import "C"

// An EndTag is a closing tag, delivered to a handler registered with
// [Element.OnEndTag]. It is the hook for acting on an element once its content
// has been seen.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type EndTag struct {
	unit[*C.lol_html_end_tag_t]
}

// Name returns the tag name, lowercased.
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
func (t *EndTag) SetName(name string) error {
	p, err := t.live()
	if err != nil {
		return err
	}
	return withName(p, name, "end_tag_name_set", cfEndTagNameSet)
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
func (t *EndTag) Before(content string, ct ContentType) error {
	return t.content(content, ct, "end_tag_before", cfEndTagBefore)
}

// After inserts content immediately after the end tag, making it the first
// content following the element.
func (t *EndTag) After(content string, ct ContentType) error {
	return t.content(content, ct, "end_tag_after", cfEndTagAfter)
}

func (t *EndTag) content(content string, ct ContentType, op string, fn contentOp[*C.lol_html_end_tag_t]) error {
	p, err := t.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), op, fn)
}

// Remove removes the end tag, leaving the element's content in place.
func (t *EndTag) Remove() {
	if p, err := t.live(); err == nil {
		C.lol_html_end_tag_remove(p)
	}
}

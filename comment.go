package lolhtml

/*
#include "shim.h"
*/
import "C"

// A Comment is an HTML comment matched by a comment handler.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type Comment struct {
	unit[*C.lol_html_comment_t]
}

// Text returns the comment's text, without the <!-- --> delimiters, as raw
// source text with character references left encoded. See TextChunk.Text.
func (c *Comment) Text() string {
	p, err := c.live()
	if err != nil {
		return ""
	}
	return takeStr(C.lol_html_comment_text_get(p))
}

// SetText replaces the comment's text, and refuses a value that would end the
// comment early:
//
//	c.SetText("--><img src=x>")
//	// lolhtml: comment_text_set: Comment text shouldn't contain a
//	// comment-closing sequence.
//
// Refused, not escaped - which this documentation used to say, along with the
// conclusion that untrusted input is therefore safe to pass. It is safe in the
// sense that nothing breaks out, and it fails the rewrite: a caller handing this
// arbitrary text has to expect an error and decide what to do about it, not
// expect a sanitised comment.
//
// There is no escaping that would work. A comment ends at "-->" or at "--!>", and
// nothing inside a comment is a character reference, so there is no spelling of
// those four characters that a comment can hold and still mean. Refusing is the
// only honest option, which is the same reason [ErrRawTextBreakout] refuses an
// insertion into a script.
//
// Measured: "-->", "--!>", "->", "a-->b", "a--!>b" and "<!-->" are refused;
// "--", "--!", "<!--" and "a--b" are accepted, and each of those round-trips as
// one comment.
//
// This is the only path that writes a comment's text for you. Building a comment
// by hand out of [HTML] content has no equivalent guard - see the package
// documentation on building markup yourself.
func (c *Comment) SetText(text string) error {
	p, err := c.live()
	if err != nil {
		return err
	}
	return withName(p, text, "comment_text_set", cfCommentTextSet)
}

// SourceLocation returns the byte range the comment occupied in the input.
func (c *Comment) SourceLocation() SourceLocation {
	p, err := c.live()
	if err != nil {
		return SourceLocation{}
	}
	return sourceLocation(C.lol_html_comment_source_location_bytes(p))
}

// Before inserts content immediately before the comment.
func (c *Comment) Before(content string, ct ContentType) error {
	return c.content(content, ct, "comment_before", cfCommentBefore)
}

// After inserts content immediately after the comment.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
func (c *Comment) After(content string, ct ContentType) error {
	return c.content(content, ct, "comment_after", cfCommentAfter)
}

// Replace replaces the comment with content.
func (c *Comment) Replace(content string, ct ContentType) error {
	return c.content(content, ct, "comment_replace", cfCommentReplace)
}

func (c *Comment) content(content string, ct ContentType, op string, fn contentOp[*C.lol_html_comment_t]) error {
	p, err := c.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), op, fn)
}

// Remove removes the comment from the output.
func (c *Comment) Remove() {
	if p, err := c.live(); err == nil {
		C.lol_html_comment_remove(p)
	}
}

// IsRemoved reports whether the comment has been removed by a handler.
func (c *Comment) IsRemoved() bool {
	p, err := c.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_comment_is_removed(p))
}

var commentUserData = userDataAccessor[*C.lol_html_comment_t]{
	get: func(p *C.lol_html_comment_t) C.uintptr_t { return C.golol_comment_user_data_get(p) },
	set: func(p *C.lol_html_comment_t, h C.uintptr_t) { C.golol_comment_user_data_set(p, h) },
}

// UserData returns the value most recently attached by SetUserData, or nil.
func (c *Comment) UserData() any { return getUserData(&c.unit, commentUserData) }

// SetUserData attaches a value to this comment, readable by another handler that
// is given the same comment - an [OnComment] handler and an [OnDocumentComment]
// one both see it. Go handlers can usually close over the value instead.
func (c *Comment) SetUserData(v any) error { return setUserData(&c.unit, commentUserData, v) }

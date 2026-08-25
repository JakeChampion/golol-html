package lolhtml

/*
#include "shim.h"
*/
import "C"

// A Comment is a comment token matched by a comment handler, which is not the
// same thing as a comment.
//
// An HTML parser produces a comment token for four different pieces of source
// syntax, and all four arrive here with their delimiters stripped and nothing to
// say which they were:
//
//	<!--a-->          a comment                       Text "a"
//	<!bogus>          a bogus comment                 Text "bogus"
//	<?php echo 1; ?>  a processing instruction        Text "?php echo 1; ?"
//	<![CDATA[x]]>     a CDATA section in HTML         Text "[CDATA[x]]"
//
// The last two are the ones that matter, because they are not comments to
// whatever reads the document next: the second is a PHP block in a template, and
// the third is character data in every language that has CDATA - including SVG,
// where the same bytes are not a comment token at all and their content is
// reported as text.
//
// Sniffing [Comment.Text] does not answer it. A comment containing "?php x ?"
// reads exactly like the processing instruction, because the difference is in the
// delimiters and those are gone. What does answer it is how much source the token
// occupied, from [Comment.SourceLocation], measured against the text:
//
//	source            End-Start-len(Text)
//	<!--a-->          7    a comment, closed by -->
//	<!--a--!>         8    a comment, closed by --!>
//	<!--a             4    a comment, closed by the end of the input
//	<!-->             5    a comment, the short empty form
//	<!--->            6    the same, one dash longer
//	<!bogus>          3    a bogus comment
//	<![CDATA[x]]>     3    a CDATA section, which is a bogus comment in HTML
//	<?php a ?>        2    a processing instruction
//	<!bogus           2    a bogus comment, closed by the end of the input
//	<?a               1    a processing instruction, closed by the same
//
// So 7 is "the document spelled this <!--...-->" and everything else is "it did
// not", which is the test a rewrite that removes or edits comments wants: two of
// the values collide - a truncated bogus comment and a processing instruction are
// both 2 - so the distinction that can be made is with the ordinary form rather
// than between the unusual ones.
//
// Editing normalises the delimiters. [Comment.SetText] on any of the four writes
// <!--text-->, so a processing instruction becomes a comment and the template
// engine downstream stops seeing it. [Comment.Remove] removes the whole token,
// whatever it was spelled as, which is the one operation with no surprise in it.
// Measured in commentshapes_test.go.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type Comment struct {
	unit[*C.lol_html_comment_t]
}

// Text returns the comment's text, without the delimiters, as raw source text
// with character references left encoded - nothing inside a comment is a
// reference, so there is nothing to decode. See TextChunk.Text.
//
// The delimiters removed are whatever the document used, which is not always
// <!-- and -->: see [Comment] for the four syntaxes that arrive here and how to
// tell them apart.
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
//
// It also writes the delimiters, as <!-- and -->, whatever the document used. A
// token spelled <?php echo 1; ?> is a comment token, and setting its text turns
// it into <!--...-->, which is a comment to a browser and nothing to the template
// engine that was going to run it. [Comment] has the measured table and the test
// for telling one from the other before writing.
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

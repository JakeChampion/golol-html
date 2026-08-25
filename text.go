package lolhtml

/*
#include "shim.h"
*/
import "C"

import "unsafe"

// A TextChunk is a run of character data matched by a text handler.
//
// Text is reported in chunks with no guaranteed boundaries: one text node may
// arrive as several chunks, split wherever the parser happened to stop. Only
// the final chunk of a node has IsLastInTextNode set, and that last chunk is
// often empty - it exists to mark the boundary. Accumulate across chunks if you
// need whole text nodes.
//
// A TextChunk is valid only for the duration of the handler that received it;
// see the package documentation on handler lifetime.
type TextChunk struct {
	unit[*C.lol_html_text_chunk_t]
}

// Text returns the chunk's text exactly as it appeared in the source, with
// character references left encoded: the text of <p>caf&eacute;</p> is
// "caf&eacute;", not "café".
//
// This is deliberate on lol-html's part - a rewriter has to be able to re-emit
// what it read - but it is easy to trip over when comparing against a plain Go
// string. Use html.UnescapeString from the standard library when you need the
// decoded form.
//
// # Transforming text and writing it back
//
// That is the operation most text handlers perform, and only one of the three
// obvious spellings is right. Measured on <p>a < b &amp; caf&eacute;</p> with
// strings.ToUpper as the transform, applied once and then again to its own
// output:
//
//	Replace(f(Text()), Text)              A &lt; B &amp;AMP; CAF&amp;EACUTE;
//	                                      then A &amp;LT; B &amp;AMP;AMP; ...
//	Replace(f(Text()), HTML)              A < B &AMP; CAF&EACUTE;
//	                                      then the same
//	Replace(f(Unescape(Text())), Text)    A &lt; B &amp; CAFÉ
//	                                      then the same
//
// The first escapes references that were already escaped - on the first pass,
// not only on the second - so a page rewritten twice shows "&amp;LT;" where it
// used to show "<".
//
// The second is stable and wrong in a quieter way: the transform ran over the
// source, so "&eacute;" became "&EACUTE;", which is not a character reference at
// all and renders as those nine characters. It is also [HTML], so anything the
// transform produces is markup - fine for a transform you wrote, an injection
// for one driven by data.
//
// The third is the one that means what it says. Decode, transform, and let the
// library escape: the output is correct on the first pass and unchanged by the
// second.
func (t *TextChunk) Text() string {
	p, err := t.live()
	if err != nil {
		return ""
	}
	// Unlike the lol_html_str_t getters, this content pointer belongs to the
	// chunk and must not be freed; it dies with the chunk, so copy it out.
	c := C.lol_html_text_chunk_content_get(p)
	if c.data == nil || c.len == 0 {
		return ""
	}
	return C.GoStringN(c.data, C.int(c.len))
}

// Bytes returns the chunk's text as a freshly allocated byte slice. As with
// Text, character references are left encoded.
func (t *TextChunk) Bytes() []byte {
	p, err := t.live()
	if err != nil {
		return nil
	}
	c := C.lol_html_text_chunk_content_get(p)
	if c.data == nil || c.len == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(c.data), C.int(c.len))
}

// IsLastInTextNode reports whether this is the final chunk of its text node.
//
// That chunk is a call of its own and it carries no bytes: it exists to mark the
// boundary. Measured empty in every shape tried - a short text node, a 100 KB
// one, character references, each of the four raw-text elements, and the same
// document fed in one-, three- and five-byte writes, which changes how the
// content is chunked and not how it ends. An element with no text has no text
// node and so no final chunk at all.
//
// The consequence is a cost: a text handler runs at least twice per text node,
// and on a document of prose about half its calls are handed nothing. See
// [OnText].
//
// Its text node, not its element: an element containing nested markup has one
// text node per run of character data, and each one ends with its own final
// chunk. [Element.OnEndTag] is the boundary that means "this element's content
// is complete".
func (t *TextChunk) IsLastInTextNode() bool {
	p, err := t.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_text_chunk_is_last_in_text_node(p))
}

// SourceLocation returns the byte range the chunk occupied in the input.
func (t *TextChunk) SourceLocation() SourceLocation {
	p, err := t.live()
	if err != nil {
		return SourceLocation{}
	}
	return sourceLocation(C.lol_html_text_chunk_source_location_bytes(p))
}

// Before inserts content immediately before the chunk.
func (t *TextChunk) Before(content string, ct ContentType) error {
	return t.content(content, ct, "text_chunk_before", cfTextChunkBefore)
}

// After inserts content immediately after the chunk.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
func (t *TextChunk) After(content string, ct ContentType) error {
	return t.content(content, ct, "text_chunk_after", cfTextChunkAfter)
}

// Replace replaces the chunk with content.
func (t *TextChunk) Replace(content string, ct ContentType) error {
	return t.content(content, ct, "text_chunk_replace", cfTextChunkReplace)
}

func (t *TextChunk) content(content string, ct ContentType, op string, fn contentOp[*C.lol_html_text_chunk_t]) error {
	p, err := t.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), op, fn)
}

// Remove removes the chunk from the output.
func (t *TextChunk) Remove() {
	if p, err := t.live(); err == nil {
		C.lol_html_text_chunk_remove(p)
	}
}

// IsRemoved reports whether the chunk has been removed by a handler.
func (t *TextChunk) IsRemoved() bool {
	p, err := t.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_text_chunk_is_removed(p))
}

var textUserData = userDataAccessor[*C.lol_html_text_chunk_t]{
	get: func(p *C.lol_html_text_chunk_t) C.uintptr_t { return C.golol_text_chunk_user_data_get(p) },
	set: func(p *C.lol_html_text_chunk_t, h C.uintptr_t) { C.golol_text_chunk_user_data_set(p, h) },
}

// UserData returns the value most recently attached by SetUserData, or nil.
func (t *TextChunk) UserData() any { return getUserData(&t.unit, textUserData) }

// SetUserData attaches a value to this chunk, readable by any later handler
// that sees it. Go handlers can usually close over the value instead.
func (t *TextChunk) SetUserData(v any) error { return setUserData(&t.unit, textUserData, v) }

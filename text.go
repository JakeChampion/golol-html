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
// usually empty - it exists to mark the boundary, and it is not empty when the
// node ends with undecodable bytes; see [TextChunk.IsLastInTextNode].
// Accumulate across chunks, and that chunk's text with them, if you need whole
// text nodes.
//
// A TextChunk is valid only for the duration of the handler that received it;
// see the package documentation on handler lifetime.
type TextChunk struct {
	unit[*C.lol_html_text_chunk_t]

	// selector is the selector whose handler was given this chunk; see the same
	// field on Element.
	selector string
}

// Text returns the chunk's text exactly as it appeared in the source, with
// character references left encoded: the text of <p>caf&eacute;</p> is
// "caf&eacute;", not "café".
//
// A chunk never contains part of a character. Where the chunk boundaries fall is
// not a caller's choice - but they always fall between characters, measured at one
// byte per write over two-, three- and four-byte runes, in text, in a comment and
// in an attribute value. Content going the other way has the opposite rule:
// [Sink.WriteChunk] takes a partial sequence and joins it to the next write.
//
// Two things decide the boundaries, and only one of them is the writes. The
// tokenizer splits a text node of its own accord at a "<" that turns out not to
// begin a tag, and delivers that character as a chunk by itself:
//
//	<p>3 < 4 and 5 < 6</p>    "3 "  "<"  " 4 and 5 "  "<"  " 6"  ""
//
// Six chunks for one text node, from one write. So controlling the writes does not
// control the chunking, and prose with a bare "<" in it - arithmetic, a code
// sample outside a <code> element - arrives in more pieces than a caller sizing
// the work by writes would expect. A "&lt;" does not split anything, and neither
// does a "&", a NUL or a CRLF; "<!", "</" and "<?" do something else again, since
// each of those begins a comment token and so ends the text node.
//
// What a boundary does split is everything larger than a character. So a
// transform applied per chunk is safe per character and wrong per pattern:
// strings.ToUpper on a chunk is correct however the document arrived, a regular
// expression looking for a word is not, because the word can straddle two
// chunks. Accumulate to [TextChunk.IsLastInTextNode] for anything that spans
// more than one character, and see the package documentation on reading an
// element's whole text for why that is still not the element's text.
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
// That chunk is usually a call of its own carrying no bytes: it exists to mark
// the boundary. Measured empty for a short text node, a 100 KB one, character
// references, each of the four raw-text elements, and the same document fed in
// one-, three- and five-byte writes, which changes how the content is chunked and
// not how it ends. An element with no text has no text node and so no final chunk
// at all.
//
// It is not empty when the node ends with bytes that could not be decoded in the
// document's encoding: then it carries the replacement character produced for
// them. Fed "<p>ab\xe9</p>" as UTF-8 the calls are "ab" and then a final chunk
// whose text is U+FFFD, three bytes of it. So accumulate this chunk's own text
// before acting on the flag - a handler that treats it as a marker and returns
// loses a character, silently, on exactly the input that a text handler already
// rewrites lossily. Measured in every raw-text element, after 100 KB of text, at
// every write size, and with the document unterminated. The same bytes under
// [WithEncoding] "windows-1252" decode, so the final chunk there is empty again.
//
// The usual consequence is a cost: a text handler runs twice per text node, and
// on a document of prose about half its calls are handed nothing. See [OnText].
// It can run once - "<p>\xe9</p>" is a single call, which is both the first
// chunk of the node and its last, and carries the replacement character.
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
//
// Inside a raw-text element this is unguarded: see [TextChunk.Replace] and
// [CheckRawText].
func (t *TextChunk) Before(content string, ct ContentType) error {
	return t.content(content, ct, "text_chunk_before", cfTextChunkBefore)
}

// After inserts content immediately after the chunk.
//
// Called twice, the second insertion lands before the first: see the package
// documentation on two insertions of the same kind.
//
// Inside a raw-text element this is unguarded: see [TextChunk.Replace] and
// [CheckRawText].
func (t *TextChunk) After(content string, ct ContentType) error {
	return t.content(content, ct, "text_chunk_after", cfTextChunkAfter)
}

// Replace replaces the chunk with content.
//
// Rewriting the text of a raw-text element - a stylesheet, a script body - means
// [HTML] rather than [Text], because [Text] escapes the three markup characters and
// raw text does not decode references: a CSS ">" would come back as "&gt;" and a
// script's "a < b" as "a &lt; b". And [HTML] here is not checked for a breakout the
// way the [Element] methods are, because a chunk cannot say what element it is in,
// so a "</style>" in the content ends the element. Call [CheckRawText] with the tag
// name the handler asked for.
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
//
// This chunk, and not the element it is in: text inside an element another handler
// has removed reports false, because nothing has been done to the chunk itself.
// [Element.IsRemoved] does answer for an ancestor, so a text handler that needs to
// know - anything accumulating, since the text it is being handed may be on its way
// out - has to be told by an element handler. See the package documentation on
// removal. Measured in removedsubtree_test.go.
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

// SetUserData attaches a value to this chunk, readable by another handler that
// is given the same chunk.
//
// The same chunk, not the same text node: each chunk is its own unit, so this is
// not a place to accumulate across the chunks of one node. Measured - the second
// chunk of a two-chunk node reads nil. Go handlers can usually close over the
// value instead.
//
// The chunk being the unit makes this the one cost in the library that depends on
// how the caller fed the document rather than on what the document says. A handle
// is held per chunk until the rewrite ends, and how many chunks a node arrives in
// is decided by the write sizes:
//
//	one 2000-byte text node    written whole        2 chunks
//	                           1024-byte writes     3 chunks
//	                           64-byte writes      33 chunks
//	                           one byte at a time  2001 chunks
//
// A rewrite reading from a socket does not choose those sizes, so this is a shape
// to avoid rather than to budget for. Setting the value to nil releases the handle
// immediately; see [Element.SetUserData] for the cost and the mitigation, and
// userdatacost_test.go for the gate.
func (t *TextChunk) SetUserData(v any) error { return setUserData(&t.unit, textUserData, v) }

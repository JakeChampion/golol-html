package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"io"
	"runtime"
)

// A StreamFunc produces inserted content on demand, writing it into the sink
// instead of returning it.
//
// Use it when the content is large or generated incrementally: nothing has to be
// assembled in memory first. Returning an error aborts the rewrite, and the
// error surfaces from Write or Close.
//
// "On demand" means when the content is emitted, which is neither immediately
// nor at the end, and two things follow from that.
//
// It cannot see anything the rewriter has not parsed yet. A function registered
// on an element runs while that element is being written out, so state gathered
// from later in the document is not there yet, and the failure is silent - you
// get the empty result your closure computed, not an error. Building a table of
// contents at a marker near the top of a page is the usual way to meet this: one
// streaming pass cannot do it. Read the document twice, buffer it, or put the
// content at the document end with [OnDocumentEnd], which is the one position
// that has seen everything.
//
// It may never run at all. If the content is discarded - the element was removed
// by a later handler, or it is inside something that was removed - the function
// is not called. So a StreamFunc is the wrong place for a side effect you need:
// count and log in the handler, and write only content in the sink.
type StreamFunc func(*Sink) error

// A Sink receives the output of a StreamFunc.
//
// It is valid only for the duration of that call. The rewriter may run it on a
// different goroutine than the one that called Write, though never on more than
// one at a time, so a StreamFunc must not depend on goroutine-local state.
type Sink struct {
	unit[*C.lol_html_streaming_sink_t]
}

// WriteString writes s to the sink, escaping it when ct is Text.
//
// s must be complete, valid UTF-8. Use WriteChunk for content split at
// arbitrary byte boundaries.
func (s *Sink) WriteString(str string, ct ContentType) error {
	p, err := s.live()
	if err != nil {
		return err
	}
	sp, sl := strPtr(str)
	var cerr C.lol_html_str_t
	rc := C.golol_sink_write_str(p, sp, sl, C.bool(ct.isHTML()), &cerr)
	runtime.KeepAlive(str)
	if rc != 0 {
		return nativeErr("streaming_sink_write_str", cerr)
	}
	return nil
}

// WriteChunk writes a fragment of UTF-8 to the sink, escaping it when ct is
// Text.
//
// Unlike WriteString, b need not be complete UTF-8: a trailing partial sequence
// is buffered and flushed once a later call completes it, so content can be
// forwarded straight from a network read. Consecutive calls must still form
// valid UTF-8 overall, and WriteString must not be used after a WriteChunk that
// ended mid-sequence.
func (s *Sink) WriteChunk(b []byte, ct ContentType) error {
	p, err := s.live()
	if err != nil {
		return err
	}
	var cerr C.lol_html_str_t
	rc := C.golol_sink_write_utf8_chunk(p, (*C.char)(bytePtr(b)), C.size_t(len(b)),
		C.bool(ct.isHTML()), &cerr)
	runtime.KeepAlive(b)
	if rc != 0 {
		return nativeErr("streaming_sink_write_utf8_chunk", cerr)
	}
	return nil
}

// AsWriter adapts the sink to io.Writer, so content can be produced with
// io.Copy or fmt.Fprintf. Writes go through WriteChunk, so chunk boundaries may
// fall anywhere in a UTF-8 sequence.
func (s *Sink) AsWriter(ct ContentType) io.Writer {
	return sinkWriter{sink: s, ct: ct}
}

type sinkWriter struct {
	sink *Sink
	ct   ContentType
}

func (w sinkWriter) Write(p []byte) (int, error) {
	if err := w.sink.WriteChunk(p, w.ct); err != nil {
		return 0, err
	}
	return len(p), nil
}

// streamingCB carries a StreamFunc across the boundary. Unlike handler handles,
// these are released by lol-html's drop callback rather than with the rewriter,
// because each insertion creates its own.
type streamingCB struct {
	c  *core
	fn StreamFunc
}

func (cb *streamingCB) call(s *Sink) error { return cb.fn(s) }

// streamOp is the shape shared by every streaming insertion shim.
type streamOp[P comparable] func(P, C.uintptr_t, *C.lol_html_str_t) C.int

func withStream[P comparable](u *unit[P], fn StreamFunc, op string, call streamOp[P]) error {
	p, err := u.live()
	if err != nil {
		return err
	}
	if fn == nil {
		return errNilStreamFunc
	}

	// Released by golol_streaming_drop_cb, which lol-html calls exactly once
	// after the last use of the handler.
	h := newHandle(&streamingCB{c: u.c, fn: fn})

	var cerr C.lol_html_str_t
	if call(p, C.uintptr_t(h), &cerr) != 0 {
		// lol-html rejected the handler, so it will never call drop.
		deleteHandle(h)
		return nativeErr(op, cerr)
	}
	return nil
}

// Element streaming insertions ------------------------------------------------

// StreamBefore inserts content before the element's start tag, produced on
// demand by fn.
func (e *Element) StreamBefore(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_before", cfElementStreamBefore)
}

// StreamAfter inserts content after the element's end tag, produced on demand
// by fn.
func (e *Element) StreamAfter(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_after", cfElementStreamAfter)
}

// StreamPrepend inserts content as the element's first child, produced on
// demand by fn.
func (e *Element) StreamPrepend(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_prepend", cfElementStreamPrepend)
}

// StreamAppend inserts content as the element's last child, produced on demand
// by fn.
func (e *Element) StreamAppend(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_append", cfElementStreamAppend)
}

// StreamSetInnerContent replaces the element's content with output produced on
// demand by fn.
func (e *Element) StreamSetInnerContent(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_set_inner_content",
		cfElementStreamSetInnerContent)
}

// StreamReplace replaces the element, tags included, with output produced on
// demand by fn.
func (e *Element) StreamReplace(fn StreamFunc) error {
	return withStream(&e.unit, fn, "element_streaming_replace", cfElementStreamReplace)
}

// EndTag streaming insertions -------------------------------------------------

// StreamBefore inserts content just inside the end tag, produced on demand by fn.
func (t *EndTag) StreamBefore(fn StreamFunc) error {
	return withStream(&t.unit, fn, "end_tag_streaming_before", cfEndTagStreamBefore)
}

// StreamAfter inserts content just after the end tag, produced on demand by fn.
func (t *EndTag) StreamAfter(fn StreamFunc) error {
	return withStream(&t.unit, fn, "end_tag_streaming_after", cfEndTagStreamAfter)
}

// StreamReplace replaces the end tag with output produced on demand by fn.
func (t *EndTag) StreamReplace(fn StreamFunc) error {
	return withStream(&t.unit, fn, "end_tag_streaming_replace", cfEndTagStreamReplace)
}

// TextChunk streaming insertions ----------------------------------------------

// StreamBefore inserts content before the chunk, produced on demand by fn.
func (t *TextChunk) StreamBefore(fn StreamFunc) error {
	return withStream(&t.unit, fn, "text_chunk_streaming_before", cfTextChunkStreamBefore)
}

// StreamAfter inserts content after the chunk, produced on demand by fn.
func (t *TextChunk) StreamAfter(fn StreamFunc) error {
	return withStream(&t.unit, fn, "text_chunk_streaming_after", cfTextChunkStreamAfter)
}

// StreamReplace replaces the chunk with output produced on demand by fn.
func (t *TextChunk) StreamReplace(fn StreamFunc) error {
	return withStream(&t.unit, fn, "text_chunk_streaming_replace", cfTextChunkStreamReplace)
}

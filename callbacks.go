package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"runtime/cgo"
)

// This file holds the Go functions that Rust calls back into. Its cgo preamble
// must contain declarations only - a file with //export cannot define C
// functions - so the trampolines that reference these live in shim.c.
//
// Two invariants hold throughout:
//
//   - No Go error or panic may cross into Rust. Each callback parks it on the
//     shared state and returns STOP; Write or Close then reports it.
//   - A unit pointer is valid only for the duration of its callback, so each
//     wrapper is detached on the way out. Wrappers are allocated per call
//     rather than pooled: reusing one would let a retained *Element silently
//     start addressing a different element, which is a worse failure than the
//     ErrDetached a fresh wrapper gives.

type elementCB struct {
	c        *core
	selector string
	fn       func(*Element) error
}

type commentCB struct {
	c        *core
	selector string
	fn       func(*Comment) error
}

type textCB struct {
	c        *core
	selector string
	fn       func(*TextChunk) error
}

type doctypeCB struct {
	c  *core
	fn func(*Doctype) error
}

type docEndCB struct {
	c  *core
	fn func(*DocumentEnd) error
}

type endTagCB struct {
	c  *core
	fn func(*EndTag) error
}

type sinkCB struct {
	c *core
}

// runHandler invokes one user handler under the two invariants above.
//
// It takes the unit and the function separately, rather than a prepared
// closure, so that a callback allocates nothing beyond the unit wrapper itself.
func runHandler[U any](st *state, kind, selector string, u U, fn func(U) error) (d C.lol_html_rewriter_directive_t) {
	// Once anything has failed, stop promptly instead of running more handlers
	// against a document we are about to abandon.
	if st.handlerErr != nil || st.sinkErr != nil || st.panicVal != nil {
		return C.LOL_HTML_STOP
	}

	defer func() {
		if r := recover(); r != nil {
			// Unwinding into Rust would abort the process. Park the panic and
			// re-raise it from Write or Close, on the caller's goroutine.
			st.panicVal = r
			d = C.LOL_HTML_STOP
		}
	}()

	if err := fn(u); err != nil {
		st.handlerErr = &HandlerError{Kind: kind, Selector: selector, Err: err}
		return C.LOL_HTML_STOP
	}
	return C.LOL_HTML_CONTINUE
}

//export golol_element_cb
func golol_element_cb(ptr *C.lol_html_element_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*elementCB)
	el := &Element{unit: unit[*C.lol_html_element_t]{ptr: ptr, c: cb.c}}
	defer el.detach()
	return runHandler(cb.c.st, "element", cb.selector, el, cb.fn)
}

//export golol_comment_cb
func golol_comment_cb(ptr *C.lol_html_comment_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*commentCB)
	c := &Comment{unit: unit[*C.lol_html_comment_t]{ptr: ptr, c: cb.c}}
	defer c.detach()
	return runHandler(cb.c.st, "comment", cb.selector, c, cb.fn)
}

//export golol_text_cb
func golol_text_cb(ptr *C.lol_html_text_chunk_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*textCB)
	t := &TextChunk{unit: unit[*C.lol_html_text_chunk_t]{ptr: ptr, c: cb.c}}
	defer t.detach()
	return runHandler(cb.c.st, "text", cb.selector, t, cb.fn)
}

//export golol_doctype_cb
func golol_doctype_cb(ptr *C.lol_html_doctype_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*doctypeCB)
	d := &Doctype{unit: unit[*C.lol_html_doctype_t]{ptr: ptr, c: cb.c}}
	defer d.detach()
	return runHandler(cb.c.st, "doctype", "", d, cb.fn)
}

//export golol_doc_end_cb
func golol_doc_end_cb(ptr *C.lol_html_doc_end_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*docEndCB)
	d := &DocumentEnd{unit: unit[*C.lol_html_doc_end_t]{ptr: ptr, c: cb.c}}
	defer d.detach()
	return runHandler(cb.c.st, "document-end", "", d, cb.fn)
}

//export golol_end_tag_cb
func golol_end_tag_cb(ptr *C.lol_html_end_tag_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*endTagCB)
	et := &EndTag{unit: unit[*C.lol_html_end_tag_t]{ptr: ptr, c: cb.c}}
	defer et.detach()
	return runHandler(cb.c.st, "end-tag", "", et, cb.fn)
}

//export golol_sink_cb
func golol_sink_cb(chunk *C.char, n C.size_t, ud C.uintptr_t) {
	cb := cgo.Handle(uintptr(ud)).Value().(*sinkCB)

	// lol-html signals end of output with a zero-length chunk, which carries no
	// data and should not reach the destination as an empty Write.
	if n == 0 {
		return
	}
	// The sink cannot report failure to lol-html, and there is no directive to
	// return, so a destination error is parked and surfaces from Write or
	// Close. Later chunks are dropped: the output is already incomplete.
	if cb.c.st.sinkErr != nil {
		return
	}
	// borrowBytes rather than a copy: io.Writer implementations must not
	// retain p, so the destination may read but not keep lol-html's buffer.
	if _, err := cb.c.st.dst.Write(borrowBytes(chunk, n)); err != nil {
		cb.c.st.sinkErr = err
	}
}

//export golol_streaming_write_cb
func golol_streaming_write_cb(sink *C.lol_html_streaming_sink_t, ud C.uintptr_t) C.int {
	cb := cgo.Handle(uintptr(ud)).Value().(*streamingCB)
	s := &Sink{unit: unit[*C.lol_html_streaming_sink_t]{ptr: sink}}
	defer s.detach()

	if err := cb.call(s); err != nil {
		cb.c.st.handlerErr = &HandlerError{Kind: "streaming", Err: err}
		return 1
	}
	return 0
}

//export golol_streaming_drop_cb
func golol_streaming_drop_cb(ud C.uintptr_t) {
	// lol-html guarantees exactly one drop after the last use of the handler,
	// which is what makes streaming handles self-releasing rather than tied to
	// the lifetime of the rewriter.
	cgo.Handle(uintptr(ud)).Delete()
}

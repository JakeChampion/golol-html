package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"io"
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
	c *core
	// fns holds every OnDocumentEnd handler, in the order they were
	// registered, because they share a single native registration.
	//
	// lol-html dispatches its document-end handlers in reverse order of
	// registration, which is deliberate upstream but surprising here: the
	// sibling API, Element.OnEndTag, runs forwards. Left alone, two handlers
	// appending content emit it backwards, and if the second one fails the
	// first never runs at all. Coalescing them into one native handler makes
	// the order the caller wrote the order they run in.
	fns []func(*DocumentEnd) error
}

// run calls each handler in registration order, stopping at the first error so
// that a failure behaves the same as it does for any single handler.
func (cb *docEndCB) run(d *DocumentEnd) error {
	for _, fn := range cb.fns {
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

type endTagCB struct {
	c        *core
	selector string
	fn       func(*EndTag) error
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
	el := &Element{unit: unit[*C.lol_html_element_t]{ptr: ptr, c: cb.c}, selector: cb.selector}
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
	t := &TextChunk{unit: unit[*C.lol_html_text_chunk_t]{ptr: ptr, c: cb.c}, selector: cb.selector}
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
	return runHandler(cb.c.st, "document-end", "", d, cb.run)
}

//export golol_end_tag_cb
func golol_end_tag_cb(ptr *C.lol_html_end_tag_t, ud C.uintptr_t) C.lol_html_rewriter_directive_t {
	cb := cgo.Handle(uintptr(ud)).Value().(*endTagCB)
	et := &EndTag{unit: unit[*C.lol_html_end_tag_t]{ptr: ptr, c: cb.c}, selector: cb.selector}
	defer et.detach()
	return runHandler(cb.c.st, "end-tag", cb.selector, et, cb.fn)
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
	// Close. Later chunks are dropped: the output is already incomplete. A
	// parked panic drops them for the same reason, and because the rewrite it
	// belongs to is already being abandoned.
	if cb.c.st.sinkErr != nil || cb.c.st.panicVal != nil {
		return
	}
	// borrowBytes rather than a copy: io.Writer implementations must not
	// retain p, so the destination may read but not keep lol-html's buffer.
	b := borrowBytes(chunk, n)
	if err := writeSink(cb.c.st, b); err != nil {
		cb.c.st.sinkErr = err
	}
}

// writeSink hands one chunk to the destination under the same panic containment
// runHandler gives a handler.
//
// The destination is user code like any handler, and it runs on the same stack,
// called from Rust. A panic crossing back through those frames skips lol-html's
// own cleanup - the crate is built with panic = "abort", so the frames carry no
// landing pads - which leaks whatever they own, including the drop callbacks
// that release streaming handles. It also frees a rewriter abandoned mid-write,
// because Write's deferred recovery cannot tell this panic from a handler's.
//
// So it is parked like a handler panic and re-raised by takeDeferred, on the
// caller's goroutine. No error is recorded alongside it: takeDeferred prefers
// the panic anyway, and a sinkErr would only outlive it. Pinned in panic_test.go.
func writeSink(st *state, b []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			st.panicVal = r
		}
	}()

	written, err := st.dst.Write(b)

	// A short write with no error breaks io.Writer's contract, and trusting it
	// would truncate the response in silence: a destination that accepted five
	// bytes of every chunk delivered 14 bytes of a 213-byte document and
	// reported success from both Write and Close. io.Copy checks this for the
	// same reason, and reports the same error.
	if err == nil && written < len(b) {
		err = io.ErrShortWrite
	}
	return err
}

//export golol_streaming_write_cb
func golol_streaming_write_cb(sink *C.lol_html_streaming_sink_t, ud C.uintptr_t) C.int {
	cb := cgo.Handle(uintptr(ud)).Value().(*streamingCB)
	s := &Sink{unit: unit[*C.lol_html_streaming_sink_t]{ptr: sink, c: cb.c}}
	defer s.detach()

	// Through runHandler, like every other callback, because the two invariants
	// it keeps both matter here. A panic must not unwind through Rust: doing so
	// skips lol-html's own cleanup, so the drop callback that releases this
	// handle never runs and the handle leaks - one per rewrite, for as long as
	// the process lives. And once anything has failed there is no point running
	// more handlers against a document already being abandoned.
	//
	// lol-html reports a streaming failure with a nonzero return rather than a
	// directive, so the directive is mapped rather than passed through.
	// The completeness check runs after the StreamFunc rather than inside the
	// writes, because a partial sequence is only wrong once nothing more is
	// coming.
	call := func(s *Sink) error {
		if err := cb.call(s); err != nil {
			return err
		}
		return s.checkComplete()
	}

	if runHandler(cb.c.st, "streaming", cb.selector, s, call) == C.LOL_HTML_CONTINUE {
		return 0
	}
	return 1
}

//export golol_streaming_drop_cb
func golol_streaming_drop_cb(ud C.uintptr_t) {
	// lol-html guarantees exactly one drop after the last use of the handler,
	// which is what makes streaming handles self-releasing rather than tied to
	// the lifetime of the rewriter.
	deleteHandle(cgo.Handle(uintptr(ud)))
}

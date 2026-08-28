package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"fmt"
	"io"
	"runtime"
	"runtime/cgo"
	"sync"
)

// A Writer streams HTML through lol-html, applying the registered handlers and
// forwarding rewritten output to an underlying io.Writer.
//
// Write may be called as many times as you like with arbitrary chunk
// boundaries; handlers observe the document as if it had arrived in one piece.
// Close finishes the document and flushes the tail of the output, and must be
// called to get well-formed output.
//
// A Writer is not safe for concurrent use, and independent Writers on separate
// goroutines are fine - as long as their handlers are independent too. See
// [Option] on reusing one.
//
// Nor is it reentrant: a handler, or a destination writer, must not call Write
// or Close on the Writer that is running it. Both refuse with [ErrReentrant]
// rather than re-entering lol-html, which has no idea it is already running.
type Writer struct {
	c        *core
	closed   bool
	poisoned bool
	// inCall is set for as long as a call into lol-html is on the stack, which
	// is exactly when a handler or the destination writer can run and call back
	// in. See ErrReentrant.
	inCall bool
	// cause is the error that poisoned the Writer, kept so that every later
	// refusal can carry it. Without it, a caller who checks only Close - which
	// is where Go idiom puts the check - learns that something failed and never
	// what.
	cause error
}

// core is the state shared between a Writer, its handler callbacks and its
// cleanup. It deliberately holds no pointer back to the Writer: handler
// closures are kept alive by the cgo handle table, so a Writer -> handler ->
// Writer cycle would make the Writer permanently reachable and its cleanup
// unreachable.
type core struct {
	st *state
	nt *native
}

// state carries the mutable bookkeeping that callbacks write to and the Writer
// methods read back after a C call returns. Callbacks cannot propagate a Go
// error or panic through Rust, so they park it here and signal STOP instead.
type state struct {
	dst io.Writer

	handlerErr error // first error returned by a user handler
	sinkErr    error // first error returned by the destination writer
	panicVal   any   // first panic recovered at the C boundary
}

// native owns every C allocation belonging to one rewriter, so that a single
// release() frees them in the right order.
type native struct {
	rw *C.lol_html_rewriter_t
	// cerr is the out-parameter Write passes to C. It is allocated once with the
	// rewriter rather than declared inside Write, because taking the address of a
	// local forces it to the heap and a byte-at-a-time write would pay for that
	// once per byte - it was the whole cost of writing small chunks. It is its own
	// allocation rather than a field of this struct because cgo refuses a pointer
	// into memory that holds Go pointers, and this struct holds several.
	//
	// Reuse across writes is safe: a Writer is not safe for concurrent use, so
	// two writes on one rewriter never overlap. Close keeps its own local, since
	// once per document is not worth sharing state with the hot path for.
	cerr      *C.lol_html_str_t
	selectors []*C.lol_html_selector_t
	handles   []cgo.Handle
	userData  map[cgo.Handle]struct{}
	once      sync.Once
}

// NewWriter builds a rewriter that writes its output to dst.
//
// Options register handlers (see OnElement and friends) and tune the rewriter
// (see WithEncoding, WithMemorySettings, WithStrict). At least one handler is
// usually wanted; with none, the output is the input re-serialised.
//
// Put a bufio.Writer in front of dst unless it is already buffered. How many
// times dst is written to is decided by what the rewrite does, not by how the
// document is written, and a mutation makes it much larger. Measured on
// <div class="row"><a href="/p">link</a></div>, written in one call:
//
//	passthrough                      1 write
//	a handler that matches           3 writes
//	the handler reads an attribute   3 writes
//	the handler removes an attribute 5 writes
//	the handler sets an attribute   12 writes
//
// because a mutated start tag is re-serialised piece by piece: "<", "a", " ",
// `href="/p"`, " ", "rel", `="`, "noopener", `"`, ">". Over 2000 such elements
// that is one 132 KB write becoming 22,001 writes with a median size of one
// byte, which on a socket or a file is 22,001 system calls for 162 KB.
//
// Nothing is buffered here on purpose: a caller streaming to a client wants the
// bytes as they are produced, and a buffer belongs where that caller can flush
// it. Pinned in writecount_test.go.
//
// Close every Writer, including one being abandoned. There is a cleanup attached
// here that frees the native resources if a Writer is dropped without it, but it
// is a backstop rather than a second way of doing this, and it is one a caller can
// take away without noticing: handler payloads live in a process-global handle
// table until the Writer is released, so a handler that closes over the Writer -
// to count into it, to stop it, to reach it from a nested rewrite - makes the
// Writer permanently reachable through that table, and the cleanup that would have
// released it can never run. The rewriter, its selectors and every handle then
// leak for the life of the process. Nothing detects it at runtime; a deferred
// Close is the whole answer.
func NewWriter(dst io.Writer, opts ...Option) (*Writer, error) {
	if dst == nil {
		return nil, errNilDst
	}

	cfg := defaultConfig()
	for i, o := range opts {
		if o == nil {
			return nil, fmt.Errorf("%w: option %d of %d", ErrNilOption, i+1, len(opts))
		}
		o.apply(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	c := &core{st: &state{dst: dst}, nt: &native{cerr: new(C.lol_html_str_t)}}

	builder := C.lol_html_rewriter_builder_new()

	// The builder is only needed until the rewriter is built, and lol-html is
	// explicit that a selector outlives every builder that accepted it: "
	// Deallocate all dependant rewriter builders first and then use
	// lol_html_selector_free" (internal/include/lol_html.h). release() frees the
	// selectors, so the builder has to be gone before it is called - which a
	// deferred free is exactly the wrong shape for, since a defer runs after the
	// release on the error paths rather than before it. Freed explicitly on each
	// path instead, once and only once.
	if err := cfg.register(c, builder); err != nil {
		C.lol_html_rewriter_builder_free(builder)
		c.nt.release()
		return nil, err
	}

	rw, err := cfg.build(c, builder)
	C.lol_html_rewriter_builder_free(builder)
	if err != nil {
		c.nt.release()
		return nil, err
	}
	c.nt.rw = rw

	w := &Writer{c: c}
	// Backstop for a Writer that is dropped without Close. This is a leak
	// guard, not the supported path: without Close the document is never
	// finished, so the output is truncated.
	runtime.AddCleanup(w, (*native).release, c.nt)
	return w, nil
}

// Write feeds the next chunk of HTML to the rewriter. Rewritten output is
// forwarded to the destination writer as it becomes available, which may happen
// during this call or a later one.
//
// The destination can be another Writer, since a Writer is an io.Writer: that is
// a streaming second pass, which is what it takes to act on markup an earlier
// stage produced. Close the stages upstream first, because each one flushes into
// the next. See the package documentation on two passes.
//
// An error from one of your handlers, or from the destination writer, surfaces
// here. lol-html cannot resume after an error, so the Writer is poisoned and
// every later Write and the Close return ErrPoisoned wrapped around that first
// error - so errors.Is and errors.As still reach it, however late it is asked
// for.
//
// A destination failure stops the rewrite, and stops it completely: no further
// element, comment or text handler runs, and the [OnDocumentEnd] handler never
// runs at all. That last one is worth planning for, because the document end is
// where a rewrite naturally writes its accounting - a summary logged there logs
// nothing on the run where the client went away. Keep the counters where the
// caller can read them after the error, and treat them as what the rewrite
// reached rather than as what the page contains. examples/gip/clientgone prints
// the difference; sinkfailure_test.go gates it.
//
// Where a destination failure stops does not depend on the write sizes: the
// budget is a fact about the destination and the page is a fact about the
// document. A destination that accepts nothing at all still sees one handler run,
// because handlers run as tokens are parsed and the destination is written to
// afterwards.
//
// What the destination is handed is lol-html's own buffer, not a copy: the slice
// passed to dst.Write is a view of Rust memory that is reused or freed as soon as
// the call returns. io.Writer already forbids retaining p, so an ordinary
// destination is unaffected - but here the cost of breaking that rule is not a
// stale read of Go memory the garbage collector is still holding. It is a read of
// freed native memory, which the race detector cannot see and which fails at
// whatever distance the retained slice is finally looked at. A destination that
// queues its argument - an asynchronous logger, a tee that buffers slices - must
// copy before it does. This is by construction rather than by measurement: there
// is no copy to leave out, and no test can safely demonstrate the read.
//
// Failing is not atomic. Everything before the token whose handler failed has
// already reached the destination, at every write size and including a single
// Write of the whole document, and what it holds is a whole number of tokens -
// well-formed markup that a parser accepts. So a caller who returns an error to
// refuse a document has already delivered a short version of it unless it held
// the output itself: write into a buffer and forward only on success, which is
// what examples/gip/mixed does. Measured in handlerfailure_test.go, along with
// the two ends of the range - a handler that fails on the document's first
// element delivers nothing, and one that fails in [OnDocumentEnd] has already
// delivered all of it.
func (w *Writer) Write(p []byte) (int, error) {
	switch {
	case w.inCall:
		return 0, ErrReentrant
	case w.closed:
		return 0, ErrClosed
	case w.poisoned:
		return 0, w.poisonErr()
	case len(p) == 0:
		return 0, nil
	}

	// A handler panic is parked by the callback and re-raised below by
	// takeDeferred, at which point the C stack has already unwound. Releasing
	// here means a caller who recovers from that panic does not leak the
	// rewriter and its handles, however they were driving the Writer. The same
	// deferred call clears inCall, so a recovered panic does not leave the
	// Writer refusing every later call as reentrant.
	defer w.endCall()

	cerr := w.c.nt.cerr
	w.inCall = true
	rc := C.golol_rewriter_write(w.c.nt.rw, (*C.char)(bytePtr(p)), C.size_t(len(p)), cerr)
	w.inCall = false
	runtime.KeepAlive(p)

	if err := w.c.st.takeDeferred(nativeErrIf(rc != 0, "write", *cerr)); err != nil {
		w.poison(err)
		return 0, err
	}
	return len(p), nil
}

// Close finishes the document, flushes the remaining output and releases every
// native resource held by the Writer. It is safe to call more than once.
//
// The error from the final flush is reported here, so Close must not be ignored.
// Two handlers can still run inside it: [OnDocumentEnd] always, and a text handler
// for the last chunk of a text node the document left open - measured, a closed
// element delivers every chunk during Write and an unclosed one delivers its
// boundary chunk during Close. So an error or a panic from a text handler can
// surface from here rather than from Write, and a caller that recovers around
// Write alone has a gap. An end-tag handler for an element nothing closes never
// runs at all, so it is not a third case.
//
// A panic from a handler running inside Close leaves the Writer closed rather than
// poisoned, because Close marks it closed before it does anything: a later Write
// reports [ErrClosed] and a later Close reports nil. A panic from Write poisons it
// with the bare sentinel. Either way the native resources are released on the way
// out and the library is unaffected: examples/gip/panics prints the whole table.
//
// Close is also the call that discovers a destination that broke after the last
// Write - but only when Close is the call that writes. For most documents it
// writes nothing, because the bytes have already gone: measured, a document that
// ends cleanly, in the middle of text, or inside a raw-text element has been
// handed over entirely by the time Close is called, and Close reports nil however
// broken the destination is. Close writes, and so can fail, when the document ends
// inside a token - an unfinished end tag, attribute, comment, or a bare "<" - or
// when a handler appends at the document end. Gated in sinkfailure_test.go.
//
// If an earlier Write already failed, Close reports ErrPoisoned wrapped around
// that first error rather than the bare sentinel: checking only Close is the
// ordinary Go shape, and it should not lose the reason.
//
// The first Close, that is. "Safe to call more than once" means the later calls
// do nothing and return nil, including after a failure - so a caller whose only
// check is on a Close that runs second sees nil for a rewrite that failed. The
// shape to avoid is an explicit Close in an error path together with a deferred
// one that assigns to the returned error; keep one Close, and let it be the one
// whose error is checked. Measured in faults_test.go, which asserts the quiet
// second Close deliberately, and demonstrated in examples/gip/poisoned.
//
// Not from inside a handler, though, which is the one place "safe to call more
// than once" used to read as an invitation: closing from a handler would free the
// rewriter underneath the write still running on it. Called there, or from the
// destination writer, Close does nothing and returns [ErrReentrant]. A handler
// stops the document by returning an error instead.
func (w *Writer) Close() error {
	// Before the closed check, and before anything is marked: a reentrant Close
	// must leave the Writer exactly as it found it, because the call it
	// interrupted is still running and still owns the rewriter.
	if w.inCall {
		return ErrReentrant
	}
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.c.nt.release()
	defer w.endCall()

	if w.poisoned {
		return w.poisonErr()
	}

	var cerr C.lol_html_str_t
	w.inCall = true
	rc := C.golol_rewriter_end(w.c.nt.rw, &cerr)
	w.inCall = false
	if err := w.c.st.takeDeferred(nativeErrIf(rc != 0, "end", cerr)); err != nil {
		w.poison(err)
		return err
	}
	return nil
}

// poison marks the Writer unusable and remembers why. takeDeferred hands each
// error out once, so this is the only place it is kept.
func (w *Writer) poison(err error) {
	w.poisoned = true
	if w.cause == nil {
		w.cause = err
	}
}

// poisonErr is what every later call returns: the sentinel, wrapped around the
// error that caused it where there is one. A handler panic poisons the Writer
// without an error - the panic goes to the caller instead - so the sentinel
// stands alone in that case.
func (w *Writer) poisonErr() error {
	if w.cause == nil {
		return ErrPoisoned
	}
	return fmt.Errorf("%w: %w", ErrPoisoned, w.cause)
}

// takeDeferred picks the error that best explains a failed C call.
//
// A handler error, a destination-writer error or a panic is always a better
// explanation than lol-html's generic "content handler error", so those win
// over nativeErr. A parked panic is re-raised on the caller's goroutine, which
// is where the user's code can actually see it.
func (s *state) takeDeferred(nativeErr error) error {
	if p := s.panicVal; p != nil {
		s.panicVal = nil
		panic(p)
	}
	if err := s.handlerErr; err != nil {
		s.handlerErr = nil
		return err
	}
	if err := s.sinkErr; err != nil {
		s.sinkErr = nil
		return err
	}
	return nativeErr
}

// release frees the rewriter, then the handles, then the selectors. Selectors
// must outlive the builder that accepted them, and the builder is already gone
// by the time a Writer exists, so ordering here only has to keep the rewriter
// ahead of the handles its callbacks might reference.
func (n *native) release() {
	n.once.Do(func() {
		if n.rw != nil {
			C.lol_html_rewriter_free(n.rw)
			n.rw = nil
		}
		for _, h := range n.handles {
			deleteHandle(h)
		}
		n.handles = nil
		for h := range n.userData {
			deleteHandle(h)
		}
		n.userData = nil
		for _, s := range n.selectors {
			C.lol_html_selector_free(s)
		}
		n.selectors = nil
	})
}

// newHandle registers a callback payload and records it for release.
func (n *native) newHandle(v any) cgo.Handle {
	h := newHandle(v)
	n.handles = append(n.handles, h)
	return h
}

// endCall closes out one call into lol-html: the stack is back on the caller's
// side, so nothing can re-enter, and if a handler panic is on its way out the
// native resources are freed before it continues.
//
// Close stays safe afterwards: it returns ErrPoisoned before touching the
// rewriter, and release is guarded by a sync.Once, so neither the caller's
// deferred Close nor the cleanup can double-free.
func (w *Writer) endCall() {
	w.inCall = false
	if r := recover(); r != nil {
		w.poisoned = true
		w.c.nt.release()
		panic(r)
	}
}

func nativeErrIf(failed bool, op string, cerr C.lol_html_str_t) error {
	if !failed {
		return nil
	}
	return nativeErr(op, cerr)
}

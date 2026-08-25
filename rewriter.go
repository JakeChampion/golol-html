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
type Writer struct {
	c        *core
	closed   bool
	poisoned bool
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
	rw        *C.lol_html_rewriter_t
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

	c := &core{st: &state{dst: dst}, nt: &native{}}

	builder := C.lol_html_rewriter_builder_new()
	// The builder is only needed until the rewriter is built; selectors must
	// outlive the builder, so they are released after it.
	defer C.lol_html_rewriter_builder_free(builder)

	if err := cfg.register(c, builder); err != nil {
		c.nt.release()
		return nil, err
	}

	rw, err := cfg.build(c, builder)
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
// An error from one of your handlers, or from the destination writer, surfaces
// here. lol-html cannot resume after an error, so the Writer is poisoned and
// every later Write and the Close return ErrPoisoned wrapped around that first
// error - so errors.Is and errors.As still reach it, however late it is asked
// for.
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
	// rewriter and its handles, however they were driving the Writer.
	defer w.releaseOnPanic()

	var cerr C.lol_html_str_t
	rc := C.golol_rewriter_write(w.c.nt.rw, (*C.char)(bytePtr(p)), C.size_t(len(p)), &cerr)
	runtime.KeepAlive(p)

	if err := w.c.st.takeDeferred(nativeErrIf(rc != 0, "write", cerr)); err != nil {
		w.poison(err)
		return 0, err
	}
	return len(p), nil
}

// Close finishes the document, flushes the remaining output and releases every
// native resource held by the Writer. It is safe to call more than once.
//
// The error from the final flush - including any handler error raised while
// processing the document end - is reported here, so Close must not be ignored.
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
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.c.nt.release()
	defer w.releaseOnPanic()

	if w.poisoned {
		return w.poisonErr()
	}

	var cerr C.lol_html_str_t
	rc := C.golol_rewriter_end(w.c.nt.rw, &cerr)
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

// releaseOnPanic frees the native resources when a handler panic is on its way
// out, then lets the panic continue.
//
// Close stays safe afterwards: it returns ErrPoisoned before touching the
// rewriter, and release is guarded by a sync.Once, so neither the caller's
// deferred Close nor the cleanup can double-free.
func (w *Writer) releaseOnPanic() {
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

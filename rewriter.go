package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
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
// A Writer is not safe for concurrent use.
type Writer struct {
	c        *core
	closed   bool
	poisoned bool
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
func NewWriter(dst io.Writer, opts ...Option) (*Writer, error) {
	if dst == nil {
		return nil, errNilDst
	}

	cfg := defaultConfig()
	for _, o := range opts {
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
// every later Write returns ErrPoisoned.
func (w *Writer) Write(p []byte) (int, error) {
	switch {
	case w.closed:
		return 0, ErrClosed
	case w.poisoned:
		return 0, ErrPoisoned
	case len(p) == 0:
		return 0, nil
	}

	var cerr C.lol_html_str_t
	rc := C.golol_rewriter_write(w.c.nt.rw, (*C.char)(bytePtr(p)), C.size_t(len(p)), &cerr)
	runtime.KeepAlive(p)

	if err := w.c.st.takeDeferred(nativeErrIf(rc != 0, "write", cerr)); err != nil {
		w.poisoned = true
		return 0, err
	}
	return len(p), nil
}

// Close finishes the document, flushes the remaining output and releases every
// native resource held by the Writer. It is safe to call more than once.
//
// The error from the final flush - including any handler error raised while
// processing the document end - is reported here, so Close must not be ignored.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.c.nt.release()

	if w.poisoned {
		return ErrPoisoned
	}

	var cerr C.lol_html_str_t
	rc := C.golol_rewriter_end(w.c.nt.rw, &cerr)
	if err := w.c.st.takeDeferred(nativeErrIf(rc != 0, "end", cerr)); err != nil {
		w.poisoned = true
		return err
	}
	return nil
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
			h.Delete()
		}
		n.handles = nil
		for h := range n.userData {
			h.Delete()
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
	h := cgo.NewHandle(v)
	n.handles = append(n.handles, h)
	return h
}

func nativeErrIf(failed bool, op string, cerr C.lol_html_str_t) error {
	if !failed {
		return nil
	}
	return nativeErr(op, cerr)
}

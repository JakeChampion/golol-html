package lolhtml_test

// A panic in any handler, on any path, must leave nothing behind.
//
// A panic must not unwind through Rust: lol-html's frames would be skipped
// without running their cleanup, and the process might not survive it. So every
// //export'ed callback recovers, parks the panic, and re-raises it from Write or
// Close on the caller's goroutine.
//
// The streaming callback did not, and the cost was one leaked cgo handle per
// rewrite - unbounded, invisible in the output, and invisible to the fuzzer
// because nothing in it panicked from inside a StreamFunc. This file asserts the
// property for every kind of handler rather than for the one that was broken,
// since the next callback added is the next one that can forget.

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const panicValue = "panic from a handler"

var errStreamFailed = errors.New("streaming handler failed")

// panickers is one entry per //export'ed callback that can run user code.
var panickers = map[string]lolhtml.Option{
	"element": lolhtml.OnElement("p", func(*lolhtml.Element) error {
		panic(panicValue)
	}),
	"comment": lolhtml.OnComment("p", func(*lolhtml.Comment) error {
		panic(panicValue)
	}),
	"text": lolhtml.OnText("p", func(*lolhtml.TextChunk) error {
		panic(panicValue)
	}),
	"doctype": lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
		panic(panicValue)
	}),
	"document comment": lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
		panic(panicValue)
	}),
	"document text": lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error {
		panic(panicValue)
	}),
	"document end": lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
		panic(panicValue)
	}),
	"end tag": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.OnEndTag(func(*lolhtml.EndTag) error { panic(panicValue) })
	}),
	"streaming sink": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamAppend(func(*lolhtml.Sink) error { panic(panicValue) })
	}),
	"streaming sink on an end tag": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			return t.StreamBefore(func(*lolhtml.Sink) error { panic(panicValue) })
		})
	}),
	"streaming sink on a text chunk": lolhtml.OnText("p", func(t *lolhtml.TextChunk) error {
		return t.StreamAfter(func(*lolhtml.Sink) error { panic(panicValue) })
	}),
	"streaming sink replacing an element": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamReplace(func(*lolhtml.Sink) error { panic(panicValue) })
	}),
	"streaming sink setting inner content": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamSetInnerContent(func(*lolhtml.Sink) error { panic(panicValue) })
	}),
	"streaming sink prepending": lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamPrepend(func(*lolhtml.Sink) error { panic(panicValue) })
	}),
}

// panicDoc has a doctype, an element, text and a comment, so every handler
// above is reached by the same input.
const panicDoc = `<!DOCTYPE html><p>text<!--c--></p>`

// recovered runs fn and reports the value it panicked with, or nil.
func recovered(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

// TestPanicLeaksNoHandles is the regression test. A leak does not change a byte
// of the output, so the handle counter is the only thing that can see it.
func TestPanicLeaksNoHandles(t *testing.T) {
	const rounds = 30

	for name, opt := range panickers {
		t.Run(name, func(t *testing.T) {
			before := settledHandles()
			for i := 0; i < rounds; i++ {
				if v := recovered(func() {
					lolhtml.RewriteString(panicDoc, opt)
				}); v == nil {
					t.Fatalf("round %d did not panic", i)
				}
			}
			requireNoHandleLeak(t, before)
		})
	}
}

// TestPanicReachesTheCaller: recovering at the boundary must not swallow the
// panic. It is re-raised on the goroutine that called Write or Close, which is
// what lets a caller's own recover see it.
func TestPanicReachesTheCaller(t *testing.T) {
	for name, opt := range panickers {
		t.Run(name, func(t *testing.T) {
			v := recovered(func() { lolhtml.RewriteString(panicDoc, opt) })
			s, ok := v.(string)
			if !ok || s != panicValue {
				t.Errorf("re-raised %#v, want %q", v, panicValue)
			}
		})
	}
}

// TestPanicOnAManualWriterIsIdempotentToClose: a caller driving a Writer
// directly recovers the panic from Write, and the deferred Close that follows
// must neither panic again nor leak.
func TestPanicOnAManualWriterIsIdempotentToClose(t *testing.T) {
	for name, opt := range panickers {
		t.Run(name, func(t *testing.T) {
			before := settledHandles()

			// The Writer is held in a variable outside the closure, and kept
			// alive past the count below, so that its own cleanup cannot run
			// inside the window being measured. Otherwise this test could
			// release handles it is trying to count.
			var w *lolhtml.Writer
			var second any
			v := recovered(func() {
				var out bytes.Buffer
				var err error
				w, err = lolhtml.NewWriter(&out, opt)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					second = recovered(func() {
						w.Close()
						w.Close()
					})
				}()
				w.Write([]byte(panicDoc))
				w.Close()
			})

			if v == nil {
				t.Fatal("the panic did not reach the caller")
			}
			if second != nil {
				t.Errorf("Close after a recovered panic panicked again: %#v", second)
			}
			requireNoHandleLeak(t, before)
			runtime.KeepAlive(w)
		})
	}
}

// TestPanicValueIsNotWrapped: the value is re-raised as it was thrown, so a
// caller matching on a sentinel type still can.
func TestPanicValueIsNotWrapped(t *testing.T) {
	type sentinel struct{ n int }

	v := recovered(func() {
		lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(*lolhtml.Sink) error { panic(sentinel{42}) })
		}))
	})
	got, ok := v.(sentinel)
	if !ok {
		t.Fatalf("re-raised %#v, want a sentinel", v)
	}
	if got.n != 42 {
		t.Errorf("sentinel.n = %d, want 42", got.n)
	}
}

// TestPanicInOneOfManyStreamingInsertsReleasesThemAll: the leak was one handle
// per panicking stream, so a rewrite registering several insertions and
// panicking in one of them is the shape that would show a partial cleanup.
func TestPanicInOneOfManyStreamingInsertsReleasesThemAll(t *testing.T) {
	before := settledHandles()

	for i := 0; i < 30; i++ {
		v := recovered(func() {
			lolhtml.RewriteString(`<p>a</p><p>b</p><p>c</p>`,
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					if err := e.StreamBefore(func(s *lolhtml.Sink) error {
						return s.WriteString("before", lolhtml.Text)
					}); err != nil {
						return err
					}
					if err := e.StreamAfter(func(s *lolhtml.Sink) error {
						return s.WriteString("after", lolhtml.Text)
					}); err != nil {
						return err
					}
					return e.StreamAppend(func(*lolhtml.Sink) error { panic(panicValue) })
				}))
		})
		if v == nil {
			t.Fatalf("round %d did not panic", i)
		}
	}

	requireNoHandleLeak(t, before)
}

// TestStreamingHandlerErrorStillReleases is the neighbouring path, which was
// always correct. Pinned so a fix to the panic path cannot break it.
func TestStreamingHandlerErrorStillReleases(t *testing.T) {
	before := settledHandles()

	for i := 0; i < 30; i++ {
		_, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(*lolhtml.Sink) error {
					return errStreamFailed
				})
			}))
		if err == nil {
			t.Fatal("the rewrite succeeded despite a failing streaming handler")
		}
		if !strings.Contains(err.Error(), "streaming handler") {
			t.Errorf("error does not name the streaming handler: %v", err)
		}
	}

	requireNoHandleLeak(t, before)
}

// panicOnWrite is a destination whose Write panics. It stands for the class the
// panickers map above cannot hold: the destination is user code like a handler,
// but it is not an Option, so it needs its own cases.
type panicOnWrite struct{}

func (panicOnWrite) Write([]byte) (int, error) { panic(panicValue) }

// TestPanicFromTheDestinationReachesTheCaller: the output sink is the one
// //export'ed callback that runs user code without being a handler. It called
// the destination writer with no recover around it, so a panicking destination
// unwound straight through lol-html's frames - which are built with
// panic = "abort" and carry no cleanup - instead of being parked like every
// other panic. It is re-raised from Write, on the caller's goroutine.
func TestPanicFromTheDestinationReachesTheCaller(t *testing.T) {
	v := recovered(func() {
		w, err := lolhtml.NewWriter(panicOnWrite{})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		w.Write([]byte(panicDoc))
	})

	s, ok := v.(string)
	if !ok || s != panicValue {
		t.Errorf("re-raised %#v, want %q", v, panicValue)
	}
}

// TestPanicFromTheDestinationLeaksNoHandles is the same property the handler
// panics have, for the same reason: frames skipped by an unwind never run the
// drop callbacks that release streaming handles.
func TestPanicFromTheDestinationLeaksNoHandles(t *testing.T) {
	const rounds = 30
	before := settledHandles()

	for i := 0; i < rounds; i++ {
		if v := recovered(func() {
			lolhtml.RewriteString(panicDoc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(s *lolhtml.Sink) error {
					return s.WriteString("x", lolhtml.Text)
				})
			}))
		}); v != nil {
			t.Fatalf("round %d panicked: %v", i, v)
		}
	}

	for i := 0; i < rounds; i++ {
		if v := recovered(func() {
			w, err := lolhtml.NewWriter(panicOnWrite{}, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(s *lolhtml.Sink) error {
					return s.WriteString("x", lolhtml.Text)
				})
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			w.Write([]byte(panicDoc))
		}); v == nil {
			t.Fatalf("round %d did not panic", i)
		}
	}

	requireNoHandleLeak(t, before)
}

// TestPanicFromTheDestinationIsIdempotentToClose: the caller's deferred Close
// runs after the recovered panic, and must neither panic again nor leak.
func TestPanicFromTheDestinationIsIdempotentToClose(t *testing.T) {
	before := settledHandles()

	var w *lolhtml.Writer
	var second any
	v := recovered(func() {
		var err error
		w, err = lolhtml.NewWriter(panicOnWrite{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			second = recovered(func() {
				w.Close()
				w.Close()
			})
		}()
		w.Write([]byte(panicDoc))
	})

	if v == nil {
		t.Fatal("the panic did not reach the caller")
	}
	if second != nil {
		t.Errorf("Close after a recovered panic panicked again: %#v", second)
	}
	requireNoHandleLeak(t, before)
	runtime.KeepAlive(w)
}

// goexiters is the third way out of user code, which the panickers table
// cannot hold: runtime.Goexit is not a panic, so nothing recovers it, and the
// goroutine leaves through lol-html's frames the way a panic would have. The
// usual spelling is t.Fatal or t.Skip inside a handler. Each entry builds a
// Writer on dst whose first Write never returns.
var goexiters = map[string]func(dst io.Writer) (*lolhtml.Writer, error){
	// A streaming insertion is registered first, so the handle a pending
	// insertion holds is part of what the exit must not leave behind.
	"element handler": func(dst io.Writer) (*lolhtml.Writer, error) {
		return lolhtml.NewWriter(dst, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			if err := e.StreamAppend(func(s *lolhtml.Sink) error {
				return s.WriteString("x", lolhtml.Text)
			}); err != nil {
				return err
			}
			runtime.Goexit()
			return nil
		}))
	},
	"streaming sink": func(dst io.Writer) (*lolhtml.Writer, error) {
		return lolhtml.NewWriter(dst, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(*lolhtml.Sink) error {
				runtime.Goexit()
				return nil
			})
		}))
	},
	"destination": func(io.Writer) (*lolhtml.Writer, error) {
		return lolhtml.NewWriter(goexitOnWrite{}, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				return s.WriteString("x", lolhtml.Text)
			})
		}))
	},
}

// goexitOnWrite is a destination whose Write ends the goroutine.
type goexitOnWrite struct{}

func (goexitOnWrite) Write([]byte) (int, error) { runtime.Goexit(); return 0, nil }

// TestGoexitPoisonsTheWriterAndLeaksNoHandles. Goexit runs the deferred calls
// on its way out, and Write's own deferred call used to see nothing wrong - no
// panic to recover - and leave the Writer looking healthy. The caller's
// deferred Close then ran lol-html's end on a rewriter abandoned in the middle
// of a write, and reported nil for a truncated document. Now the deferred call
// notices that the C call never returned, and poisons and releases the
// rewriter before the exit continues: Close answers ErrPoisoned without
// touching lol-html, and the handle count is back where it started.
//
// Each case runs on its own goroutine, because the exit takes the goroutine
// with it, and the deferred Close inside that goroutine records what it saw.
func TestGoexitPoisonsTheWriterAndLeaksNoHandles(t *testing.T) {
	for name, newWriter := range goexiters {
		t.Run(name, func(t *testing.T) {
			before := settledHandles()

			// Held outside the goroutine and kept alive past the count, for
			// the same reason as in TestPanicOnAManualWriterIsIdempotentToClose.
			var w *lolhtml.Writer
			var newErr, closeErr error
			returned := false
			done := make(chan struct{})
			go func() {
				defer close(done)
				w, newErr = newWriter(io.Discard)
				if newErr != nil {
					return
				}
				defer func() { closeErr = w.Close() }()
				_, _ = w.Write([]byte(panicDoc))
				returned = true
			}()
			<-done

			if newErr != nil {
				t.Fatal(newErr)
			}
			if returned {
				t.Fatal("Write returned; the handler did not leave through Goexit")
			}
			if closeErr == nil {
				t.Fatal("Close after an abandoned Write reported nil")
			}
			if !errors.Is(closeErr, lolhtml.ErrPoisoned) {
				t.Errorf("Close after an abandoned Write = %v, want ErrPoisoned", closeErr)
			}
			requireNoHandleLeak(t, before)
			runtime.KeepAlive(w)
		})
	}
}

// TestGoexitThroughRewriteLeaksNoHandles is the same exit under Rewrite, whose
// own deferred Close is the one that runs. Rewrite never returns, so the only
// observable is the handle count afterwards.
func TestGoexitThroughRewriteLeaksNoHandles(t *testing.T) {
	const rounds = 30
	before := settledHandles()

	for i := 0; i < rounds; i++ {
		returned := false
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = lolhtml.RewriteString(panicDoc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				if err := e.StreamAppend(func(s *lolhtml.Sink) error {
					return s.WriteString("x", lolhtml.Text)
				}); err != nil {
					return err
				}
				runtime.Goexit()
				return nil
			}))
			returned = true
		}()
		<-done
		if returned {
			t.Fatalf("round %d: Rewrite returned", i)
		}
	}

	requireNoHandleLeak(t, before)
}

// TestPanicStackNamesTheHandler: the trace the runtime prints for a re-raised
// panic begins at the Write or Close that raised it, because the handler's
// frames were unwound at the boundary where the panic was caught. PanicStack is
// the record taken there, before they were, and it has to name the function that
// panicked - which is the one thing a caller with several handlers needs.
func TestPanicStackNamesTheHandler(t *testing.T) {
	var w *lolhtml.Writer
	var buf bytes.Buffer
	w, err := lolhtml.NewWriter(&buf,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return handlerThatPanicsForTheStackTest() }))
	if err != nil {
		t.Fatal(err)
	}
	if w.PanicStack() != nil {
		t.Fatal("a Writer that has not caught a panic reports a stack")
	}

	v := recovered(func() { w.Write([]byte(`<p>x</p>`)) })
	if v != "stack test" {
		t.Fatalf("re-raised %v, want the handler's value", v)
	}
	stack := string(w.PanicStack())
	if !strings.Contains(stack, "handlerThatPanicsForTheStackTest") {
		t.Errorf("PanicStack does not name the handler:\n%s", stack)
	}
	// The record survives the Close a caller's defer makes.
	_ = w.Close()
	if string(w.PanicStack()) != stack {
		t.Error("PanicStack changed across Close")
	}
}

// handlerThatPanicsForTheStackTest is a named function rather than a closure so
// the assertion has a name to look for in the trace.
func handlerThatPanicsForTheStackTest() error { panic("stack test") }

// TestAPanicFromTheDestinationHasAStackToo: the destination writer is the other
// place user code is caught at the boundary, and it keeps its stack the same way.
func TestAPanicFromTheDestinationHasAStackToo(t *testing.T) {
	w, err := lolhtml.NewWriter(destinationThatPanicsForTheStackTest{},
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	recovered(func() { w.Write([]byte(`<p>x</p>`)) })
	if !strings.Contains(string(w.PanicStack()), "destinationThatPanicsForTheStackTest") {
		t.Errorf("PanicStack does not name the destination:\n%s", w.PanicStack())
	}
}

type destinationThatPanicsForTheStackTest struct{}

func (destinationThatPanicsForTheStackTest) Write([]byte) (int, error) { panic("destination") }

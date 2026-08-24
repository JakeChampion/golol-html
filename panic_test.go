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
			before := lolhtml.LiveHandles()
			for i := 0; i < rounds; i++ {
				if v := recovered(func() {
					lolhtml.RewriteString(panicDoc, opt)
				}); v == nil {
					t.Fatalf("round %d did not panic", i)
				}
			}
			if after := lolhtml.LiveHandles(); after != before {
				t.Errorf("%d handles leaked over %d rewrites (%d before, %d after)",
					after-before, rounds, before, after)
			}
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
			before := lolhtml.LiveHandles()

			var second any
			v := recovered(func() {
				var out bytes.Buffer
				w, err := lolhtml.NewWriter(&out, opt)
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
			if after := lolhtml.LiveHandles(); after != before {
				t.Errorf("%d handles leaked (%d before, %d after)", after-before, before, after)
			}
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
	before := lolhtml.LiveHandles()

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

	if after := lolhtml.LiveHandles(); after != before {
		t.Errorf("%d handles leaked (%d before, %d after)", after-before, before, after)
	}
}

// TestStreamingHandlerErrorStillReleases is the neighbouring path, which was
// always correct. Pinned so a fix to the panic path cannot break it.
func TestStreamingHandlerErrorStillReleases(t *testing.T) {
	before := lolhtml.LiveHandles()

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

	if after := lolhtml.LiveHandles(); after != before {
		t.Errorf("%d handles leaked (%d before, %d after)", after-before, before, after)
	}
}

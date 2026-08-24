package lolhtml_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func TestSelectorError(t *testing.T) {
	_, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("a:not-a-real-pseudo", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("expected an error for an unsupported selector")
	}

	var se *lolhtml.SelectorError
	if !errors.As(err, &se) {
		t.Fatalf("error is %T (%v), want *lolhtml.SelectorError", err, err)
	}
	if se.Selector != "a:not-a-real-pseudo" {
		t.Errorf("Selector = %q, want %q", se.Selector, "a:not-a-real-pseudo")
	}
	// The message comes from lol-html's thread-local last-error slot. An empty
	// message here would mean the shim read it on the wrong OS thread.
	if se.Message == "" {
		t.Error("Message is empty: lol-html's last error was not retrieved")
	}
}

func TestHandlerErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")

	_, err := lolhtml.RewriteString(`<div><p>x</p></div>`,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return sentinel }))
	if err == nil {
		t.Fatal("expected the handler error to surface")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, sentinel) = false; err = %v", err)
	}

	var he *lolhtml.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("error is %T, want *lolhtml.HandlerError", err)
	}
	if he.Kind != "element" || he.Selector != "p" {
		t.Errorf("HandlerError{Kind: %q, Selector: %q}, want {element, p}", he.Kind, he.Selector)
	}
}

func TestHandlerPanicIsRepanickedOnCaller(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected the handler panic to reach the caller")
		}
		if got, ok := r.(string); !ok || got != "handler exploded" {
			t.Errorf("recovered %#v, want %q", r, "handler exploded")
		}
	}()

	//nolint:errcheck // the panic is the point
	lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		panic("handler exploded")
	}))
	t.Fatal("unreachable: RewriteString returned instead of panicking")
}

func TestUnitDetachedAfterHandlerReturns(t *testing.T) {
	var escaped *lolhtml.Element
	if _, err := lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		escaped = e
		if e.Detached() {
			t.Error("element reported detached inside its own handler")
		}
		return nil
	})); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if escaped == nil {
		t.Fatal("handler never ran")
	}
	if !escaped.Detached() {
		t.Fatal("element still attached after its handler returned")
	}
	// Every method must refuse rather than touch freed memory.
	if err := escaped.SetAttribute("x", "y"); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("SetAttribute after detach = %v, want ErrDetached", err)
	}
	if err := escaped.Before("x", lolhtml.Text); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("Before after detach = %v, want ErrDetached", err)
	}
	if err := escaped.OnEndTag(func(*lolhtml.EndTag) error { return nil }); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("OnEndTag after detach = %v, want ErrDetached", err)
	}
	if err := escaped.StreamAppend(func(*lolhtml.Sink) error { return nil }); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("StreamAppend after detach = %v, want ErrDetached", err)
	}
	if got := escaped.TagName(); got != "" {
		t.Errorf("TagName after detach = %q, want empty", got)
	}
	if got := escaped.AttributeList(); got != nil {
		t.Errorf("AttributeList after detach = %v, want nil", got)
	}
	// A detached element must yield nothing rather than walk freed memory.
	for range escaped.Attributes() {
		t.Error("Attributes yielded a value after detach")
	}
}

func TestWriterPoisonedAfterHandlerError(t *testing.T) {
	sentinel := errors.New("stop here")

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return sentinel }))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	if _, err := w.Write([]byte(`<p>x</p>`)); !errors.Is(err, sentinel) {
		t.Fatalf("first Write error = %v, want the handler error", err)
	}
	if _, err := w.Write([]byte(`<p>y</p>`)); !errors.Is(err, lolhtml.ErrPoisoned) {
		t.Errorf("second Write error = %v, want ErrPoisoned", err)
	}
	if err := w.Close(); !errors.Is(err, lolhtml.ErrPoisoned) {
		t.Errorf("Close error = %v, want ErrPoisoned", err)
	}
}

func TestWriteAfterClose(t *testing.T) {
	w, err := lolhtml.NewWriter(io.Discard)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close = %v, want nil (Close must be idempotent)", err)
	}
	if _, err := w.Write([]byte("<p>")); !errors.Is(err, lolhtml.ErrClosed) {
		t.Errorf("Write after Close = %v, want ErrClosed", err)
	}
}

// errWriter fails after letting n bytes through, to exercise the sink path that
// cannot report failure to lol-html directly.
type errWriter struct {
	n   int
	err error
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, w.err
	}
	w.n -= len(p)
	return len(p), nil
}

func TestDestinationErrorSurfaces(t *testing.T) {
	sentinel := errors.New("disk full")
	dst := &errWriter{n: 0, err: sentinel}

	w, err := lolhtml.NewWriter(dst)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// The failure may be reported by either call, depending on when lol-html
	// flushes, so accept it from Write or Close but require it to appear.
	_, writeErr := w.Write([]byte(`<p>hello</p>`))
	closeErr := w.Close()
	if !errors.Is(writeErr, sentinel) && !errors.Is(closeErr, sentinel) {
		t.Fatalf("destination error never surfaced: write = %v, close = %v", writeErr, closeErr)
	}
}

func TestMemoryLimitExceeded(t *testing.T) {
	// A long unclosed start tag forces the rewriter to buffer, so a small cap
	// is guaranteed to be hit.
	in := `<div ` + strings.Repeat("a", 4096)

	_, err := lolhtml.Rewrite([]byte(in),
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			PreallocatedParsingBuffer: 16,
			MaxMemory:                 64,
		}),
		lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("expected the memory limit to be exceeded")
	}

	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) {
		t.Fatalf("error is %T (%v), want *lolhtml.NativeError", err, err)
	}
	if ne.Message == "" {
		t.Error("Message is empty: lol-html's last error was not retrieved")
	}
	if !ne.MemoryLimitExceeded() {
		t.Errorf("MemoryLimitExceeded() = false for message %q", ne.Message)
	}
}

// TestGracefulBailOut pins down the difference the setting actually makes.
// Measured on lol-html v3.0.1 with these limits: without it the rewriter emits
// nothing at all, and with it every input byte reaches the sink.
func TestGracefulBailOut(t *testing.T) {
	// A long unclosed start tag forces buffering, so a small cap is certain to
	// be hit partway through the input.
	in := `<p>kept</p><div ` + strings.Repeat("a", 4096)

	run := func(graceful bool) (string, error) {
		var buf bytes.Buffer
		w, err := lolhtml.NewWriter(&buf,
			lolhtml.WithMemorySettings(lolhtml.MemorySettings{
				PreallocatedParsingBuffer: 16,
				MaxMemory:                 64,
				GracefulBailOut:           graceful,
			}),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error { return e.SetTagName("q") }))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		_, writeErr := w.Write([]byte(in))
		closeErr := w.Close()
		if writeErr == nil && closeErr == nil {
			t.Fatal("expected the memory limit to be exceeded")
		}
		return buf.String(), writeErr
	}

	t.Run("off truncates", func(t *testing.T) {
		got, err := run(false)
		var ne *lolhtml.NativeError
		if !errors.As(err, &ne) || !ne.MemoryLimitExceeded() {
			t.Fatalf("error = %v, want a memory-limit NativeError", err)
		}
		if len(got) == len(in) {
			t.Errorf("output is complete (%d bytes); expected the response to be broken", len(got))
		}
	})

	t.Run("on preserves every input byte", func(t *testing.T) {
		got, _ := run(true)
		// The guarantee is that nothing is lost, not that any particular
		// prefix was rewritten: the bail-out boundary may fall anywhere,
		// including before the first handler ran.
		if got != in {
			t.Errorf("output lost or altered input: got %d bytes, want the %d input bytes intact",
				len(got), len(in))
		}
	})
}

// TestShortWriteFromTheDestination: io.Writer requires an implementation that
// accepts fewer bytes than it was given to return an error, and not every
// implementation does. Trusting the count would truncate the response in
// silence, so it is checked, and io.ErrShortWrite is reported - the same error
// io.Copy reports for the same reason.
func TestShortWriteFromTheDestination(t *testing.T) {
	doc := "<p>" + strings.Repeat("abcdefghij", 20) + "</p>"

	t.Run("accepting part of each chunk", func(t *testing.T) {
		for _, accept := range []int{0, 1, 5} {
			dst := &partialWriter{accept: accept}
			w, err := lolhtml.NewWriter(dst)
			if err != nil {
				t.Fatal(err)
			}

			_, werr := w.Write([]byte(doc))
			cerr := w.Close()

			if !errors.Is(werr, io.ErrShortWrite) {
				t.Errorf("accept=%d: Write = %v, want io.ErrShortWrite", accept, werr)
			}
			if !errors.Is(cerr, lolhtml.ErrPoisoned) {
				t.Errorf("accept=%d: Close = %v, want ErrPoisoned", accept, cerr)
			}
			if dst.buf.Len() >= len(doc) {
				t.Errorf("accept=%d: the destination somehow received everything", accept)
			}
		}
	})

	t.Run("a destination that accepts everything is untouched", func(t *testing.T) {
		dst := &partialWriter{accept: -1}
		w, err := lolhtml.NewWriter(dst)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if dst.buf.String() != doc {
			t.Errorf("output was %d bytes, want %d", dst.buf.Len(), len(doc))
		}
	})

	t.Run("a destination's own error is reported, not ErrShortWrite", func(t *testing.T) {
		// A compliant writer returns n < len(p) together with an error. That
		// error is the caller's and must survive rather than being replaced.
		own := errors.New("the destination's own error")
		dst := &partialWriter{accept: 3, err: own}

		w, err := lolhtml.NewWriter(dst)
		if err != nil {
			t.Fatal(err)
		}
		_, werr := w.Write([]byte(doc))
		w.Close()

		if !errors.Is(werr, own) {
			t.Errorf("Write = %v, want the destination's own error", werr)
		}
		if errors.Is(werr, io.ErrShortWrite) {
			t.Error("the destination's error was replaced with io.ErrShortWrite")
		}
	})
}

// partialWriter accepts at most accept bytes of every write, or all of them when
// accept is negative, and returns err alongside the count.
type partialWriter struct {
	buf    bytes.Buffer
	accept int
	err    error
}

func (w *partialWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.accept >= 0 && n > w.accept {
		n = w.accept
	}
	w.buf.Write(p[:n])
	return n, w.err
}

func TestEncoding(t *testing.T) {
	t.Run("windows-1252", func(t *testing.T) {
		// 0xA9 is (c) in windows-1252 and invalid as standalone UTF-8, so a
		// correct decode proves the label was honoured.
		in := []byte{'<', 'p', '>', 0xA9, '<', '/', 'p', '>'}
		var got string
		if _, err := lolhtml.Rewrite(in,
			lolhtml.WithEncoding("windows-1252"),
			lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
				got += tc.Text()
				return nil
			})); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if got != "©" {
			t.Errorf("decoded text = %q, want %q", got, "©")
		}
	})

	// Both rejections name the label. The native message does not - "Unknown
	// character encoding has been provided" leaves a caller whose encoding came
	// from configuration with nothing to grep for.
	rejected := []struct{ name, label string }{
		{"unknown label", "not-an-encoding"},
		// lol-html requires an ASCII-compatible encoding.
		{"utf-16le", "utf-16le"},
		{"utf-16be", "utf-16be"},
		{"utf-16", "utf-16"},
		{"replacement", "replacement"},
	}
	for _, tt := range rejected {
		t.Run(tt.name+" is rejected", func(t *testing.T) {
			_, err := lolhtml.NewWriter(io.Discard, lolhtml.WithEncoding(tt.label))
			if err == nil {
				t.Fatalf("expected an error for %q", tt.label)
			}

			var ee *lolhtml.EncodingError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %T (%v), want *EncodingError", err, err)
			}
			if ee.Label != tt.label {
				t.Errorf("Label = %q, want %q", ee.Label, tt.label)
			}
			if ee.Message == "" {
				t.Error("Message is empty, so the reason is lost")
			}
			if !strings.Contains(err.Error(), tt.label) {
				t.Errorf("error text does not name the label: %v", err)
			}
		})
	}

	// The WHATWG labels are aliases, not encodings: these four all select
	// windows-1252, which is what the standard requires and what browsers do.
	// Anyone expecting true Latin-1 gets a different answer over 0x80 to 0x9F,
	// so it is asserted rather than left to be discovered.
	t.Run("latin-1 labels are windows-1252", func(t *testing.T) {
		for _, label := range []string{"windows-1252", "iso-8859-1", "latin1", "ascii", "us-ascii"} {
			var got string
			// 0x80 is the euro sign in windows-1252 and a control character in
			// true Latin-1.
			if _, err := lolhtml.Rewrite([]byte{'<', 'p', '>', 0x80, '<', '/', 'p', '>'},
				lolhtml.WithEncoding(label),
				lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
					got += tc.Text()
					return nil
				})); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if got != "\u20ac" {
				t.Errorf("%s decoded 0x80 as %q, want the euro sign", label, got)
			}
		}
	})

	// Inserted content is UTF-8 and is encoded on the way out. A character the
	// target cannot represent becomes a numeric character reference rather than
	// being dropped or replaced.
	t.Run("unrepresentable inserted characters become references", func(t *testing.T) {
		out, err := lolhtml.Rewrite([]byte(`<p>x</p>`),
			lolhtml.WithEncoding("windows-1252"),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent("euro \u20ac party \U0001f389", lolhtml.Text)
			}))
		if err != nil {
			t.Fatal(err)
		}
		// The euro exists in windows-1252 as 0x80; the emoji does not.
		want := []byte("<p>euro \x80 party &#127881;</p>")
		if !bytes.Equal(out, want) {
			t.Errorf("\n got: % x\nwant: % x", out, want)
		}
	})
}

func TestNewWriterValidation(t *testing.T) {
	tests := []struct {
		name string
		dst  io.Writer
		opts []lolhtml.Option
	}{
		{name: "nil destination", dst: nil},
		{name: "empty encoding", dst: io.Discard, opts: []lolhtml.Option{lolhtml.WithEncoding("")}},
		{
			name: "negative max memory",
			dst:  io.Discard,
			opts: []lolhtml.Option{lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: -1})},
		},
		{
			name: "prealloc exceeds max",
			dst:  io.Discard,
			opts: []lolhtml.Option{lolhtml.WithMemorySettings(lolhtml.MemorySettings{
				PreallocatedParsingBuffer: 128, MaxMemory: 64,
			})},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := lolhtml.NewWriter(tc.dst, tc.opts...); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestStreamFuncError(t *testing.T) {
	sentinel := errors.New("stream failed")
	_, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(*lolhtml.Sink) error { return sentinel })
		}))
	if err == nil {
		t.Fatal("expected the streaming handler error to surface")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(err, sentinel) = false; err = %v", err)
	}
}

func TestNilStreamFunc(t *testing.T) {
	_, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(nil)
		}))
	if err == nil {
		t.Fatal("expected an error for a nil StreamFunc")
	}
}

// TestConcurrentRewriters is the real test of the single-cgo-call error
// retrieval: many goroutines rewriting at once means calls land on whatever OS
// thread the scheduler picks, so an error read in a separate cgo call would
// come back empty some of the time.
func TestConcurrentRewriters(t *testing.T) {
	const goroutines = 32
	const iterations = 20

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range iterations {
				want := fmt.Sprintf("g%d-i%d", g, i)
				got, err := lolhtml.RewriteString(`<a href="x">t</a>`,
					lolhtml.OnElement("a", func(e *lolhtml.Element) error {
						return e.SetAttribute("href", want)
					}))
				if err != nil {
					errs <- fmt.Errorf("goroutine %d iteration %d: %w", g, i, err)
					return
				}
				if expect := fmt.Sprintf(`<a href="%s">t</a>`, want); got != expect {
					errs <- fmt.Errorf("goroutine %d: got %q, want %q", g, got, expect)
					return
				}

				// Provoke a native error and require the message to arrive.
				_, err = lolhtml.RewriteString(`<p>x</p>`,
					lolhtml.OnElement(":::bad", func(*lolhtml.Element) error { return nil }))
				var se *lolhtml.SelectorError
				if !errors.As(err, &se) {
					errs <- fmt.Errorf("goroutine %d: error is %T, want *SelectorError", g, err)
					return
				}
				if se.Message == "" {
					errs <- fmt.Errorf("goroutine %d: empty native error message", g)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func noop() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil })
}

func TestUnderBudget(t *testing.T) {
	const doc = `<p>hello</p><a href="/x">link</a>`
	var out bytes.Buffer
	res, err := Copy(&out, strings.NewReader(doc), Budget{MaxOutput: 1000}, noop())
	if err != nil {
		t.Fatalf("under budget: %v", err)
	}
	if out.String() != doc {
		t.Errorf("output = %q, want %q", out.String(), doc)
	}
	if res.Written != int64(len(doc)) || res.Read != int64(len(doc)) {
		t.Errorf("read %d wrote %d, want %d and %d", res.Read, res.Written, len(doc), len(doc))
	}
	if res.Stopped != "" {
		t.Errorf("Stopped = %q, want empty", res.Stopped)
	}
}

func TestOverBudgetStopsExactlyAtIt(t *testing.T) {
	const doc = `<p>hello world, this is longer than the budget</p>`
	for _, max := range []int64{1, 5, 10, 40} {
		var out bytes.Buffer
		res, err := Copy(&out, strings.NewReader(doc), Budget{MaxOutput: max}, noop())
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Errorf("max=%d: err = %v, want ErrBudgetExceeded", max, err)
			continue
		}
		if res.Stopped != "output" {
			t.Errorf("max=%d: Stopped = %q, want \"output\"", max, res.Stopped)
		}
		if int64(out.Len()) != max || res.Written != max {
			t.Errorf("max=%d: wrote %d bytes (Result says %d), want exactly %d",
				max, out.Len(), res.Written, max)
		}
		if !strings.HasPrefix(doc, out.String()) {
			t.Errorf("max=%d: output %q is not a prefix of the document", max, out.String())
		}
	}
}

func TestZeroBudgetIsUnlimited(t *testing.T) {
	doc := strings.Repeat(`<p>x</p>`, 1000)
	var out bytes.Buffer
	res, err := Copy(&out, strings.NewReader(doc), Budget{}, noop())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != int64(len(doc)) {
		t.Errorf("wrote %d, want %d", res.Written, len(doc))
	}
}

// The budget is on output because a rewrite can grow a document. Eighteen bytes
// in, a hundred and something out, and an input-side guard would have seen
// nothing worth stopping.
func TestTheBudgetCatchesGrowth(t *testing.T) {
	const doc = `<p>a</p><p>b</p><p>c</p>`
	grow := lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.Append(strings.Repeat("padding", 10), lolhtml.Text)
	})
	var out bytes.Buffer
	res, err := Copy(&out, strings.NewReader(doc), Budget{MaxOutput: 100}, grow)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if res.Written != 100 {
		t.Errorf("wrote %d, want 100", res.Written)
	}
	if res.Read >= 100 {
		t.Errorf("read %d bytes to produce 100; this test is meant to show the "+
			"output growing past an input that never would have", res.Read)
	}
}

// The memory limit is the other half, and it is the half that bounds what the
// parser holds. A document that is one enormous start tag emits nothing until
// the tag ends, so the output budget cannot see it coming.
func TestTheMemoryLimitStopsWhatTheOutputBudgetCannot(t *testing.T) {
	doc := "<div " + strings.Repeat("a", 200000) + ">"
	var out bytes.Buffer
	res, err := Copy(&out, strings.NewReader(doc), Budget{
		MaxOutput: 1 << 20,
		MaxMemory: 8192,
	}, lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("the memory limit did not stop it")
	}
	if !errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
		t.Fatalf("err = %v, want ErrMemoryLimitExceeded", err)
	}
	if res.Stopped != "memory" {
		t.Errorf("Stopped = %q, want \"memory\"", res.Stopped)
	}
	// Strict bail-out discards the buffer, so nothing reached the destination.
	if res.Written != 0 {
		t.Errorf("wrote %d bytes on a strict bail-out, want 0", res.Written)
	}
	if res.Read >= int64(len(doc)) {
		t.Errorf("read the whole document (%d bytes) before stopping", res.Read)
	}
}

// A graceful bail-out flushes what the parser had, and that flush is not
// bounded by the memory limit. The output budget is, because it is enforced at
// the destination.
func TestAGracefulFlushIsStillBoundedByTheOutputBudget(t *testing.T) {
	doc := "<div " + strings.Repeat("a", 200000) + ">"
	var out bytes.Buffer
	res, err := Copy(&out, strings.NewReader(doc), Budget{
		MaxOutput: 100,
		MaxMemory: 1024,
		Graceful:  true,
	}, lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("nothing stopped it")
	}
	if res.Written != 100 {
		t.Errorf("wrote %d bytes past a budget of 100", res.Written)
	}
	if out.Len() != 100 {
		t.Errorf("the destination got %d bytes, want 100", out.Len())
	}
}

// Without Graceful the same bail-out writes nothing, which is the difference
// between the two settings stated as a test rather than a sentence.
func TestStrictAndGracefulDifferInWhatReachesTheDestination(t *testing.T) {
	doc := "<div " + strings.Repeat("a", 200000) + ">"
	run := func(graceful bool) int64 {
		var out bytes.Buffer
		res, err := Copy(&out, strings.NewReader(doc), Budget{
			MaxOutput: 1 << 20,
			MaxMemory: 65536,
			Graceful:  graceful,
		}, lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
		if err == nil {
			t.Fatalf("graceful=%v: the memory limit did not fire", graceful)
		}
		if int64(out.Len()) != res.Written {
			t.Errorf("graceful=%v: Result says %d, destination got %d",
				graceful, res.Written, out.Len())
		}
		return res.Written
	}
	if n := run(false); n != 0 {
		t.Errorf("strict bail-out wrote %d bytes, want 0", n)
	}
	if n := run(true); n == 0 {
		t.Error("graceful bail-out wrote nothing")
	}
}

// The measurement the package comment rests on: how much input is consumed
// before the first output byte. It is a property of the rewriter, not of this
// program, and it decides how early any output budget can possibly stop.
func TestHowLateOutputCanBe(t *testing.T) {
	const n = 200000
	tests := []struct {
		name     string
		doc      string
		handlers bool
		// buffered is whether the first output byte waits for the whole
		// document rather than arriving in the first chunk.
		buffered bool
	}{
		{"one unterminated start tag", "<div " + strings.Repeat("a", n) + ">", true, true},
		{"one huge attribute", `<div a="` + strings.Repeat("b", n) + `">`, true, true},
		{"the same with no handlers", "<div " + strings.Repeat("a", n) + ">", false, false},
		{"plain text", strings.Repeat("x", n), true, false},
		{"many small elements", strings.Repeat("<p>x</p>", n/8), true, false},
		{"one huge comment", "<!--" + strings.Repeat("c", n) + "-->", true, false},
		{"one huge script", "<script>" + strings.Repeat("d", n) + "</script>", true, false},
		{"one huge text node", "<p>" + strings.Repeat("f", n) + "</p>", true, false},
		{"deep nesting", strings.Repeat("<div>", n/5), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []lolhtml.Option
			if tt.handlers {
				opts = append(opts,
					lolhtml.OnElement("div, p, script", func(*lolhtml.Element) error { return nil }),
					lolhtml.OnComment("*", func(*lolhtml.Comment) error { return nil }))
			}
			first := firstOutputAfter(t, tt.doc, opts)
			const chunk = 4096
			if tt.buffered {
				if first < int64(len(tt.doc)) {
					t.Errorf("first output after %d of %d bytes; expected the whole "+
						"document to be buffered", first, len(tt.doc))
				}
				return
			}
			if first > chunk {
				t.Errorf("first output after %d bytes, expected within the first chunk of %d",
					first, chunk)
			}
		})
	}
}

// firstOutputAfter returns how many input bytes had been handed to the rewriter
// when the first output byte reached the destination.
func firstOutputAfter(t *testing.T, doc string, opts []lolhtml.Option) int64 {
	t.Helper()
	var consumed, first int64
	w := &watcher{onFirst: func() { first = consumed }}
	rw, err := lolhtml.NewWriter(w, opts...)
	if err != nil {
		t.Fatal(err)
	}
	const chunk = 4096
	for i := 0; i < len(doc); i += chunk {
		end := min(i+chunk, len(doc))
		// Set before the write, so output produced during it is attributed to
		// the input that caused it.
		consumed = int64(end)
		if _, err := rw.Write([]byte(doc[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	if w.n == 0 {
		t.Fatalf("the rewrite produced no output for a %d-byte document", len(doc))
	}
	return first
}

type watcher struct {
	n       int64
	onFirst func()
}

func (w *watcher) Write(p []byte) (int, error) {
	if w.n == 0 && len(p) > 0 {
		w.onFirst()
	}
	w.n += int64(len(p))
	return len(p), nil
}

// Where the reader breaks must not change what the destination receives.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><p>one</p><a href="/x">two</a><p>three</p></body></html>`
	for _, max := range []int64{0, 10, 33, 1000} {
		var want bytes.Buffer
		if _, err := Copy(&want, strings.NewReader(doc), Budget{MaxOutput: max}, noop()); err != nil &&
			!errors.Is(err, ErrBudgetExceeded) {
			t.Fatal(err)
		}
		for n := 1; n <= len(doc); n++ {
			var got bytes.Buffer
			if _, err := Copy(&got, &chunked{s: doc, n: n}, Budget{MaxOutput: max}, noop()); err != nil &&
				!errors.Is(err, ErrBudgetExceeded) {
				t.Fatalf("max=%d chunk=%d: %v", max, n, err)
			}
			if got.String() != want.String() {
				t.Fatalf("max=%d chunk=%d: got %q, want %q", max, n, got.String(), want.String())
			}
		}
	}
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

// A destination that fails for its own reasons is not a budget failure, and the
// reason has to survive to the caller.
func TestADestinationErrorIsNotABudgetError(t *testing.T) {
	errDisk := errors.New("disk full")
	res, err := Copy(failing{errDisk}, strings.NewReader(`<p>a</p>`), Budget{MaxOutput: 1000}, noop())
	if !errors.Is(err, errDisk) {
		t.Fatalf("err = %v, want the destination's error", err)
	}
	if errors.Is(err, ErrBudgetExceeded) {
		t.Error("a destination error was reported as a budget failure")
	}
	if res.Stopped != "" {
		t.Errorf("Stopped = %q, want empty", res.Stopped)
	}
}

type failing struct{ err error }

func (f failing) Write([]byte) (int, error) { return 0, f.err }

package lolhtml_test

// What reaches the sink when the memory limit is exceeded.
//
// This has two variables, not one. GracefulBailOut is the documented one. The
// other is how the input was fed, and it changes the answer completely: the
// same document and the same cap produce an empty response when written in one
// call and a truncated one when written in chunks, because in the second case
// rewritten output has already been flushed before the limit is reached.
//
// The truncated case is the one to design for, since chunks are what io.Copy
// produces, and it is the dangerous one: the cut lands on an element boundary,
// so the result is well-formed HTML that a parser accepts without complaint.
// Nothing but the error says the document is incomplete.

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// bailOutDoc has links either side of one pathological tag, so a bail-out has
// rewritable content on both sides of it.
func bailOutDoc() string {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, `<a href="/%d">l</a>`, i)
	}
	b.WriteString(`<a ` + strings.Repeat(`data-x="y" `, 400) + `href="/fat">f</a>`)
	for i := 20; i < 40; i++ {
		fmt.Fprintf(&b, `<a href="/%d">l</a>`, i)
	}
	return b.String()
}

type bailOutResult struct {
	out       string
	sent      int
	bailedOut bool
	rewritten int
}

// feed writes doc in chunks of the given size, 0 meaning one write, stopping at
// the first error, and reports what came out.
func feed(t *testing.T, doc string, limit, chunk int, graceful bool) bailOutResult {
	t.Helper()

	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out,
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory: limit, GracefulBailOut: graceful,
		}),
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}))
	if err != nil {
		t.Fatal(err)
	}

	step := chunk
	if step == 0 {
		step = len(doc)
	}

	res := bailOutResult{}
	var werr error
	for i := 0; i < len(doc) && werr == nil; i += step {
		end := min(i+step, len(doc))
		_, werr = w.Write([]byte(doc[i:end]))
		// The rewriter received the whole chunk whether or not it reported an
		// error for it, which is what a caller resuming by hand has to know.
		res.sent = end
	}
	cerr := w.Close()

	var ne *lolhtml.NativeError
	res.bailedOut = (errors.As(werr, &ne) || errors.As(cerr, &ne)) && ne.MemoryLimitExceeded()
	res.out = out.String()
	res.rewritten = strings.Count(res.out, `rel="noopener"`)
	return res
}

// TestMemoryNeededDependsOnHowTheInputIsFed is the part that catches people
// sizing a limit. The same document and the same handlers need eight times as
// much MaxMemory when written in 256-byte chunks as when written in one call, so
// a limit chosen by testing with a single Write is far too low for the io.Copy
// the README recommends.
func TestMemoryNeededDependsOnHowTheInputIsFed(t *testing.T) {
	doc := bailOutDoc()

	smallestThatSucceeds := func(chunk int) int {
		for limit := 512; limit <= 1<<20; limit *= 2 {
			if !feed(t, doc, limit, chunk, false).bailedOut {
				return limit
			}
		}
		return 0
	}

	oneWrite := smallestThatSucceeds(0)
	chunked := smallestThatSucceeds(256)

	if oneWrite != 1024 {
		t.Errorf("one write succeeds from MaxMemory=%d, want 1024", oneWrite)
	}
	if chunked != 8192 {
		t.Errorf("256-byte writes succeed from MaxMemory=%d, want 8192", chunked)
	}
	if chunked <= oneWrite {
		t.Errorf("chunked (%d) should need more than one write (%d); if this has "+
			"changed, the README's sizing warning needs revisiting", chunked, oneWrite)
	}
}

// TestMemoryBailOutReachesTheSink pins every row of the table in the README. The
// numbers are exact rather than bounds: each one is what a caller's response
// looks like, and upstream is pinned, so a change should be noticed rather than
// absorbed.
func TestMemoryBailOutReachesTheSink(t *testing.T) {
	doc := bailOutDoc()

	tests := []struct {
		name     string
		limit    int
		chunk    int
		graceful bool
		wantOut  int
		wantRewr int
		// wantPrefix says the output must be exactly the leading bytes of the
		// input, which is what "flushed verbatim, nothing rewritten" means.
		wantPrefix bool
	}{{
		name:  "one write, not graceful: nothing reaches the sink",
		limit: 512, chunk: 0, graceful: false, wantOut: 0, wantRewr: 0,
	}, {
		name:  "one write, graceful: everything received, none rewritten",
		limit: 512, chunk: 0, graceful: true, wantOut: len(doc), wantRewr: 0,
		wantPrefix: true,
	}, {
		name:  "chunked, not graceful: a rewritten prefix, then it stops",
		limit: 1024, chunk: 256, graceful: false, wantOut: 670, wantRewr: 20,
	}, {
		name:  "chunked, graceful: rewritten prefix, then verbatim",
		limit: 1024, chunk: 256, graceful: true, wantOut: 1068, wantRewr: 20,
	}, {
		name:  "chunked, graceful, roomier limit: more of it rewritten",
		limit: 4096, chunk: 256, graceful: true, wantOut: 4140, wantRewr: 20,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feed(t, doc, tt.limit, tt.chunk, tt.graceful)

			if !got.bailedOut {
				t.Fatalf("expected a memory bail-out at MaxMemory=%d", tt.limit)
			}
			if len(got.out) != tt.wantOut {
				t.Errorf("%d bytes reached the sink, want %d", len(got.out), tt.wantOut)
			}
			if got.rewritten != tt.wantRewr {
				t.Errorf("%d links were rewritten, want %d", got.rewritten, tt.wantRewr)
			}
			if tt.wantPrefix && got.out != doc[:len(got.out)] {
				t.Error("the output is not a verbatim prefix of the input")
			}
		})
	}
}

// TestTruncatedBailOutLooksLikeAValidDocument is why the default deserves the
// warning it now has. The cut lands on an element boundary, so the response is
// well-formed HTML: a client cannot tell it is missing seven eighths of the
// page, and a cache has no reason to refuse it.
func TestTruncatedBailOutLooksLikeAValidDocument(t *testing.T) {
	doc := bailOutDoc()
	got := feed(t, doc, 1024, 256, false)

	if !got.bailedOut {
		t.Fatal("expected a memory bail-out")
	}
	if len(got.out) == 0 || len(got.out) >= len(doc) {
		t.Fatalf("expected a truncated response, got %d of %d bytes", len(got.out), len(doc))
	}
	if !strings.HasPrefix(doc, got.out[:strings.Index(got.out, ` rel=`)]) {
		t.Error("the truncated output is not a prefix of the document")
	}
	// The two things that make it dangerous rather than merely wrong.
	if strings.LastIndex(got.out, "<") > strings.LastIndex(got.out, ">") {
		t.Error("the output ends inside a tag, which would at least be visible")
	}
	if !strings.HasSuffix(got.out, "</a>") {
		t.Errorf("expected the cut to land on an element boundary, got %q",
			got.out[max(0, len(got.out)-20):])
	}
}

// TestGracefulBailOutFlushesEveryByteReceived is the property that makes the
// graceful path usable: the caller can resume from the byte after the last one
// it wrote, because everything up to there has reached the sink.
func TestGracefulBailOutFlushesEveryByteReceived(t *testing.T) {
	doc := bailOutDoc()

	for _, chunk := range []int{0, 64, 256, 1024} {
		got := feed(t, doc, 1024, chunk, true)
		if !got.bailedOut {
			continue
		}
		// The rewritten prefix is longer than the input it came from, so the
		// output is at least the bytes received.
		if len(got.out) < got.sent {
			t.Errorf("chunk %d: %d bytes out for %d sent; the graceful path lost input",
				chunk, len(got.out), got.sent)
		}
	}
}

// TestBailOutPoisonsTheWriter: whichever setting, the rewriter cannot continue,
// so a caller who wants the rest of the document has to write it themselves.
func TestBailOutPoisonsTheWriter(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		var out bytes.Buffer
		w, err := lolhtml.NewWriter(&out,
			lolhtml.WithMemorySettings(lolhtml.MemorySettings{
				MaxMemory: 512, GracefulBailOut: graceful,
			}),
			lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "noopener")
			}))
		if err != nil {
			t.Fatal(err)
		}

		if _, err := w.Write([]byte(bailOutDoc())); err == nil {
			t.Fatalf("graceful=%v: expected the write to fail", graceful)
		}
		if _, err := w.Write([]byte(`<a href="/after">a</a>`)); !errors.Is(err, lolhtml.ErrPoisoned) {
			t.Errorf("graceful=%v: second write = %v, want ErrPoisoned", graceful, err)
		}
		if err := w.Close(); !errors.Is(err, lolhtml.ErrPoisoned) {
			t.Errorf("graceful=%v: Close = %v, want ErrPoisoned", graceful, err)
		}
		if strings.Contains(out.String(), "/after") {
			t.Errorf("graceful=%v: content written after the bail-out reached the sink", graceful)
		}
	}
}

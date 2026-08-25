package lolhtml_test

// What a graceful bail-out serves.
//
// The option reads as the kinder of the two failures, and for a rewrite that adds
// something it is. What it flushes is input rather than output, so for a rewrite
// that removes or neutralises something - which is what most of the interesting
// ones do - continuing to serve means serving the thing the rewrite existed to
// stop. And the rewritten prefix can be empty, because the buffer requirement is
// decided early enough that a small limit fails on the first write.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// runToBailOut runs a document that needs more buffer than limit and reports what
// reached the destination.
func runToBailOut(t *testing.T, doc string, limit int, graceful bool) (out string, rewritten int, memory bool) {
	t.Helper()
	var b strings.Builder
	w, err := lolhtml.NewWriter(&b,
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory: limit, GracefulBailOut: graceful,
		}),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-seen", "1")
		}))
	if err != nil {
		t.Fatal(err)
	}
	var werr error
	for i := 0; i < len(doc) && werr == nil; i += 64 {
		_, werr = w.Write([]byte(doc[i:min(i+64, len(doc))]))
	}
	cerr := w.Close()
	memory = errors.Is(werr, lolhtml.ErrMemoryLimitExceeded) ||
		errors.Is(cerr, lolhtml.ErrMemoryLimitExceeded)
	out = b.String()
	return out, strings.Count(out, `data-seen="1"`), memory
}

// flushDoc needs more than a few hundred bytes of buffer and is long enough that a
// partial rewrite would be visible.
var flushDoc = strings.Repeat("<p>filler</p>", 200) + "<p>last</p>"

// TestTheDefaultBailOutServesNothing, and says so.
func TestTheDefaultBailOutServesNothing(t *testing.T) {
	out, rewritten, memory := runToBailOut(t, flushDoc, 560, false)
	if !memory {
		t.Fatal("the limit did not trip; this test needs a document that exceeds it")
	}
	if out != "" {
		t.Errorf("the destination got %d bytes: %q", len(out), out)
	}
	if rewritten != 0 {
		t.Errorf("%d elements were rewritten", rewritten)
	}
}

// TestAGracefulBailOutServesInput, unrewritten - which is the fact the option's
// name does not carry.
func TestAGracefulBailOutServesInput(t *testing.T) {
	out, rewritten, memory := runToBailOut(t, flushDoc, 560, true)
	if !memory {
		t.Fatal("the limit did not trip")
	}
	if out == "" {
		t.Fatal("the graceful mode served nothing, which is the other mode's behaviour")
	}
	if rewritten != 0 {
		t.Errorf("%d elements were rewritten; for this document and limit the answer "+
			"should be none, which is the point", rewritten)
	}
	// What it served is the document's own bytes.
	if !strings.HasPrefix(flushDoc, out) {
		t.Errorf("the output is not a prefix of the input: %q", out)
	}
}

// TestTheRewrittenPrefixCanBeEmpty. "Rewritten up to some boundary" can mean up to
// byte zero, because the buffer requirement is decided early: a limit too small for
// a document fails on the first write rather than part way through.
func TestTheRewrittenPrefixCanBeEmpty(t *testing.T) {
	for _, limit := range []int{560, 600, 700, 800} {
		out, rewritten, memory := runToBailOut(t, flushDoc, limit, true)
		if !memory {
			t.Errorf("limit %d did not trip", limit)
			continue
		}
		if rewritten != 0 {
			t.Errorf("limit %d rewrote %d elements before bailing out; if a partial "+
				"rewrite is now possible the documentation should say so", limit, rewritten)
		}
		if len(out) > 128 {
			t.Errorf("limit %d served %d bytes, want about one write", limit, len(out))
		}
	}
	// A limit that is enough rewrites everything, so the failure above is the
	// limit's and not the document's.
	out, rewritten, memory := runToBailOut(t, flushDoc, 4096, true)
	if memory {
		t.Fatalf("4096 tripped too, so this document cannot show the contrast")
	}
	if rewritten != 201 {
		t.Errorf("%d elements rewritten with room to work, want 201", rewritten)
	}
	if strings.Contains(out, "<p>") {
		t.Errorf("something was served verbatim in a successful rewrite: %q", tailOf(out, 40))
	}
}

// TestBothModesReportTheFailure, so a caller always knows - which is what makes
// discarding a choice rather than a guess.
func TestBothModesReportTheFailure(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		if _, _, memory := runToBailOut(t, flushDoc, 560, graceful); !memory {
			t.Errorf("graceful=%v did not report the memory limit", graceful)
		}
	}
}

// TestWhatAGracefulBailOutCostsARemovingRewrite is the decision spelled out: the
// same document, a rewrite that strips something, and the option that keeps serving.
func TestWhatAGracefulBailOutCostsARemovingRewrite(t *testing.T) {
	doc := strings.Repeat(`<p onclick="steal()">x</p>`, 200)
	var b strings.Builder
	w, err := lolhtml.NewWriter(&b,
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: 560, GracefulBailOut: true}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			return e.RemoveAttribute("onclick")
		}))
	if err != nil {
		t.Fatal(err)
	}
	var werr error
	for i := 0; i < len(doc) && werr == nil; i += 64 {
		_, werr = w.Write([]byte(doc[i:min(i+64, len(doc))]))
	}
	w.Close()
	out := b.String()
	if out == "" {
		t.Skip("this limit no longer bails out gracefully on this document")
	}
	if !strings.Contains(out, "onclick") {
		t.Errorf("the flushed bytes have no onclick in them, so this test is not "+
			"measuring what it says: %q", out)
	}
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

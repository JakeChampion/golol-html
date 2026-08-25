package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestBothMechanismsLeaveAPrefixRewrite is the guarantee the program checks, at write sizes
// from one byte to a page.
func TestBothMechanismsLeaveAPrefixRewrite(t *testing.T) {
	for _, write := range []int{1, 7, 64, 4096} {
		for _, run := range []struct {
			name string
			fn   func(int, int) (Stop, error)
		}{{"sentinel", RunSentinel}, {"quiet", RunQuiet}} {
			s, err := run.fn(3, write)
			if err != nil {
				t.Fatalf("%s at write size %d: %v", run.name, write, err)
			}
			if !s.PrefixRewrite {
				t.Errorf("%s at write size %d left %d bytes that are not a rewrite of any "+
					"prefix", run.name, write, s.Out)
			}
			if s.Out == 0 {
				t.Errorf("%s at write size %d left nothing", run.name, write)
			}
		}
	}
}

// TestTheSentinelStopsInTheSamePlaceHoweverItIsFed. The condition is a fact about the
// document - the third heading - so the place the rewrite stops has to be a fact about the
// document too, not about the reader upstream.
func TestTheSentinelStopsInTheSamePlaceHoweverItIsFed(t *testing.T) {
	var first int
	for _, write := range []int{1, 7, 64, 4096} {
		s, err := RunSentinel(3, write)
		if err != nil {
			t.Fatal(err)
		}
		if s.Seen != 3 {
			t.Errorf("write size %d: stopped after %d headings, want 3", write, s.Seen)
		}
		if first == 0 {
			first = s.Out
			continue
		}
		if s.Out != first {
			t.Errorf("write size %d left %d bytes and write size 1 left %d: the stopping "+
				"position depends on the writes", write, s.Out, first)
		}
	}

	// And it is the bytes before the stopping element, not including its start tag.
	s, err := RunSentinel(3, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if want := 2 * len(Unit); s.Out != want {
		t.Errorf("stopped at %d bytes, want %d - the two units before the third heading",
			s.Out, want)
	}
}

// TestTheSentinelSurvivesBothCalls: a caller checking Close alone, which is where Go idiom
// puts the check, still finds its own error.
func TestTheSentinelSurvivesBothCalls(t *testing.T) {
	s, err := RunSentinel(3, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.WriteErr, ErrDone) {
		t.Errorf("write reported %v", s.WriteErr)
	}
	if !errors.Is(s.CloseErr, ErrDone) {
		t.Errorf("close reported %v", s.CloseErr)
	}
	if !errors.Is(s.CloseErr, lolhtml.ErrPoisoned) {
		t.Errorf("close did not report the poisoning: %v", s.CloseErr)
	}
	// The write that failed reports no bytes accepted, though the rewriter consumed part
	// of what it was given - which is why the program prints both figures.
	if s.Fed == s.Written {
		t.Errorf("the failing write reported %d of %d accepted", s.Fed, s.Written)
	}
	if s.Out == 0 {
		t.Error("and nothing reached the sink")
	}
}

// TestQuietStopsOnlyBetweenWrites, which is its cost and the reason to prefer the sentinel
// when the condition is a place in the document.
func TestQuietStopsOnlyBetweenWrites(t *testing.T) {
	unitsPerWrite := func(write int) int { return write/len(Unit) + 1 }

	for _, write := range []int{1, 64, 4096} {
		s, err := RunQuiet(3, write)
		if err != nil {
			t.Fatal(err)
		}
		if s.Seen < 3 {
			t.Errorf("write size %d stopped after %d headings, before the condition was met",
				write, s.Seen)
		}
		// The overshoot is bounded by what one write can contain.
		if s.Seen > 3+unitsPerWrite(write) {
			t.Errorf("write size %d overshot to %d headings, more than one write's worth "+
				"past 3", write, s.Seen)
		}
	}

	// A large write overshoots and a small one does not, which is the whole point.
	big, err := RunQuiet(3, 4096)
	if err != nil {
		t.Fatal(err)
	}
	small, err := RunQuiet(3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if big.Seen <= small.Seen {
		t.Errorf("4096-byte writes saw %d headings and 7-byte writes %d", big.Seen, small.Seen)
	}
	if small.Seen != 3 {
		t.Errorf("7-byte writes saw %d headings, want 3", small.Seen)
	}
}

// TestTheStreamContinuesRatherThanRestarting. The generated stream has to be a document: a
// version of stream that wrote the same first-n-bytes chunk over and over produced fragments
// that no prefix of anything matches, and the prefix check above caught it. This is that bug
// as a test.
func TestTheStreamContinuesRatherThanRestarting(t *testing.T) {
	var got strings.Builder
	if _, _, err := stream(&got, 7, alreadyDone(12)); err != nil {
		t.Fatal(err)
	}
	if want := generate(got.Len())[:got.Len()]; got.String() != want {
		t.Errorf("streamed %q, want %q", got.String(), want)
	}

	// And the same total fed in different write sizes is the same bytes.
	var a, b strings.Builder
	if _, _, err := stream(&a, 4, alreadyDone(9)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream(&b, 6, alreadyDone(6)); err != nil {
		t.Fatal(err)
	}
	if a.Len() != b.Len() {
		t.Fatalf("fed %d bytes and %d, which is not a comparison", a.Len(), b.Len())
	}
	if a.String() != b.String() {
		t.Errorf("write size 4 gave %q and write size 6 gave %q", a.String(), b.String())
	}
}

// alreadyDone returns a condition that is false for n calls and true after, so stream can be
// tested without a rewriter in the way.
func alreadyDone(n int) func() bool {
	calls := 0
	return func() bool {
		calls++
		return calls > n
	}
}

// TestGenerateIsLongEnoughToWindow, since stream indexes into it rather than buffering.
func TestGenerateIsLongEnoughToWindow(t *testing.T) {
	for _, n := range []int{1, len(Unit), len(Unit) + 1, 4096} {
		if got := len(generate(n)); got < n+len(Unit) {
			t.Errorf("generate(%d) is %d bytes, too short to take a window of %d from any "+
				"offset within a unit", n, got, n)
		}
	}
}

// TestTheReportSaysWhatHappened, since it is what a reader takes away.
func TestTheReportSaysWhatHappened(t *testing.T) {
	s, err := RunSentinel(3, 4096)
	if err != nil {
		t.Fatal(err)
	}
	out := s.String()
	for _, want := range []string{
		"wanted 3 headings, saw 3",
		"a sentinel error from the handler",
		"a rewrite of the first",
		"errors.Is(done)  true",
		"errors.Is(poison) true",
		"a refused write reports none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	quiet, err := RunQuiet(3, 64)
	if err != nil {
		t.Fatal(err)
	}
	if out := quiet.String(); strings.Contains(out, "write error") {
		t.Errorf("stopping quietly reported a write error:\n%s", out)
	}

	// A failed guarantee reads as one.
	broken := Stop{How: "fabricated", Want: 1, Seen: 1, Out: 10}
	if out := broken.String(); !strings.Contains(out, "not a rewrite of any prefix") {
		t.Errorf("a failed guarantee reported:\n%s", out)
	}
}

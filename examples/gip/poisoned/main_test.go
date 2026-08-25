package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestEveryFailurePoisonsTheWriter, which is the one rule underneath all of them.
func TestEveryFailurePoisonsTheWriter(t *testing.T) {
	for _, o := range All().Outcomes {
		if o.Failure == "after Close" {
			// A closed writer is not poisoned, it is closed - a different sentinel
			// for a different mistake.
			if !errors.Is(o.LaterErr, lolhtml.ErrClosed) {
				t.Errorf("after Close: a later Write returned %v, want ErrClosed", o.LaterErr)
			}
			if o.CloseErr != nil {
				t.Errorf("after Close: Close returned %v, want nil - closing twice is fine", o.CloseErr)
			}
			continue
		}
		if !errors.Is(o.LaterErr, lolhtml.ErrPoisoned) {
			t.Errorf("%s: a later Write returned %v, want ErrPoisoned", o.Failure, o.LaterErr)
		}
		if !errors.Is(o.CloseErr, lolhtml.ErrPoisoned) {
			t.Errorf("%s: Close returned %v, want ErrPoisoned", o.Failure, o.CloseErr)
		}
	}
}

// TestTheCauseSurvivesToTheLastCall, so errors.Is reaches it however late it is asked.
func TestTheCauseSurvivesToTheLastCall(t *testing.T) {
	for _, o := range All().Outcomes {
		if o.Failure == "after Close" {
			continue
		}
		if !o.CauseKept {
			t.Errorf("%s: the cause did not survive to Close: first %v, close %v",
				o.Failure, o.FirstErr, o.CloseErr)
		}
	}
	// And by sentinel, for the four that have one.
	for _, tc := range []struct {
		failure string
		target  error
	}{
		{"handler error", ErrHandler},
		{"destination error", ErrSink},
		{"memory limit", lolhtml.ErrMemoryLimitExceeded},
		{"ambiguous tag", lolhtml.ErrAmbiguousTag},
	} {
		o := outcome(t, tc.failure)
		if !errors.Is(o.FirstErr, tc.target) {
			t.Errorf("%s: the first error is %v, want it to match its sentinel", tc.failure, o.FirstErr)
		}
		if !errors.Is(o.CloseErr, tc.target) {
			t.Errorf("%s: Close is %v, want it to still match the sentinel", tc.failure, o.CloseErr)
		}
	}
}

func outcome(t *testing.T, name string) Outcome {
	t.Helper()
	for _, o := range All().Outcomes {
		if o.Failure == name {
			return o
		}
	}
	t.Fatalf("no outcome named %q", name)
	return Outcome{}
}

// TestAPanicIsRaisedOnTheCallersGoroutineAndStillPoisons.
func TestAPanicIsRaisedOnTheCallersGoroutineAndStillPoisons(t *testing.T) {
	o := outcome(t, "handler panic")
	if o.Recovered == nil {
		t.Fatal("the panic did not reach this goroutine")
	}
	if s, ok := o.Recovered.(string); !ok || !strings.Contains(s, "panicked") {
		t.Errorf("recovered %v, want the handler's panic value", o.Recovered)
	}
	if o.FirstErr != nil {
		t.Errorf("the Write that panicked also returned %v", o.FirstErr)
	}
	if !errors.Is(o.LaterErr, lolhtml.ErrPoisoned) {
		t.Errorf("a later Write returned %v, want ErrPoisoned", o.LaterErr)
	}
}

// TestWhatTheDestinationHoldsDependsOnTheFailure, which is the part that decides what a
// client sees.
func TestWhatTheDestinationHoldsDependsOnTheFailure(t *testing.T) {
	// A handler error delivers everything before the failing token, and it ends at a
	// token boundary.
	handler := outcome(t, "handler error")
	if handler.Delivered == "" {
		t.Error("the handler error delivered nothing")
	}
	if handler.MidTag {
		t.Errorf("the delivered prefix ends mid-tag: %q", handler.Delivered)
	}
	if strings.Contains(handler.Delivered, "img") {
		t.Errorf("the failing element reached the destination: %q", handler.Delivered)
	}

	// A destination that refuses every write delivers nothing, by construction.
	if got := outcome(t, "destination error").Delivered; got != "" {
		t.Errorf("the broken sink received %q", got)
	}

	// The default memory bail-out at a limit this tight discards what it was holding.
	if got := outcome(t, "memory limit").Delivered; got != "" {
		t.Errorf("the memory bail-out delivered %q; at this limit it bails on the first "+
			"write and the default discards", got)
	}

	// A successful rewrite delivers everything.
	if got := outcome(t, "after Close").Delivered; got != "<p>one</p>" {
		t.Errorf("the clean run delivered %q", got)
	}
}

// TestNoDeliveredPrefixEndsMidTag, over every failure: that is what makes a truncated
// response read as a short page rather than as a broken one.
func TestNoDeliveredPrefixEndsMidTag(t *testing.T) {
	for _, o := range All().Outcomes {
		if o.MidTag {
			t.Errorf("%s delivered %q, which stops inside a tag", o.Failure, o.Delivered)
		}
	}
}

// TestTheReportIsATable, since the point of the program is to be read.
func TestTheReportIsATable(t *testing.T) {
	s := All().String()
	for _, want := range []string{
		"failure", "first Write", "later Write", "Close", "delivered",
		"handler error", "destination error", "memory limit", "ambiguous tag",
		"handler panic", "after Close", "ErrPoisoned + cause", "ErrClosed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the table is missing %q:\n%s", want, s)
		}
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) != len(Failures())+1 {
		t.Errorf("the table has %d lines for %d failures", len(lines), len(Failures()))
	}
}

// TestCloseIsWorthCallingAfterAFailure: it is the deterministic release, and calling it
// twice is not an error.
func TestCloseIsWorthCallingAfterAFailure(t *testing.T) {
	var sink strings.Builder
	w, err := lolhtml.NewWriter(&sink, lolhtml.OnElement("img", func(*lolhtml.Element) error {
		return ErrHandler
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p><img src="x">`)); !errors.Is(err, ErrHandler) {
		t.Fatalf("Write returned %v", err)
	}
	first := w.Close()
	second := w.Close()
	if !errors.Is(first, lolhtml.ErrPoisoned) {
		t.Errorf("the first Close returned %v", first)
	}
	// The second Close is quiet, deliberately - "safe to call more than once" means
	// the later calls do nothing and say nothing. It is worth knowing because a caller
	// whose only check is on a Close that runs second sees nil for a failed rewrite.
	if second != nil {
		t.Errorf("the second Close returned %v, want nil", second)
	}
}

// TestOnlyTheFirstCloseReportsTheFailure, which is the shape to avoid: an explicit Close
// in an error path and a deferred one that assigns to the returned error.
func TestOnlyTheFirstCloseReportsTheFailure(t *testing.T) {
	rewrite := func(explicit bool) (err error) {
		var sink strings.Builder
		w, nerr := lolhtml.NewWriter(&sink, lolhtml.OnElement("img", func(*lolhtml.Element) error {
			return ErrHandler
		}))
		if nerr != nil {
			return nerr
		}
		defer func() {
			if cerr := w.Close(); err == nil {
				err = cerr
			}
		}()
		if _, werr := w.Write([]byte(`<p>a</p><img src="x">`)); werr != nil {
			if explicit {
				// A caller that closes here and returns the write error is fine; one
				// that closes here and relies on the deferred Close is not.
				_ = w.Close()
				return nil
			}
			return nil
		}
		return nil
	}

	if err := rewrite(false); !errors.Is(err, lolhtml.ErrPoisoned) {
		t.Errorf("without an explicit Close the deferred one reported %v, want the failure", err)
	}
	if err := rewrite(true); err != nil {
		t.Errorf("with an explicit Close first the deferred one reported %v; nil is the "+
			"documented behaviour and the reason to keep one Close", err)
	}
}

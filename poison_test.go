package lolhtml_test

// What a Writer says after it has failed.
//
// lol-html cannot resume after an error, so the first failure is the only one
// the rewriter can explain - and it is reported from whichever call was running
// when it happened. Every call after that has to refuse, and the question is
// whether the refusal still carries the reason.

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var errHandler = errors.New("the handler said no")

// TestCloseReportsWhyAfterAFailedWrite is the case the fix is for: the ordinary
// Go shape is to write and then check Close, and Close used to answer with a
// sentinel naming a state rather than a cause.
func TestCloseReportsWhyAfterAFailedWrite(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return fmt.Errorf("wrapping: %w", errHandler)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p>`)); !errors.Is(err, errHandler) {
		t.Fatalf("Write did not report the handler error: %v", err)
	}

	closeErr := w.Close()
	if !errors.Is(closeErr, lolhtml.ErrPoisoned) {
		t.Errorf("Close does not report ErrPoisoned: %v", closeErr)
	}
	if !errors.Is(closeErr, errHandler) {
		t.Errorf("Close lost the cause: %v", closeErr)
	}
	if !strings.Contains(closeErr.Error(), "the handler said no") {
		t.Errorf("Close's message does not name the cause: %v", closeErr)
	}
}

// A caller who ignores the Write error entirely - io.Copy into a Writer whose
// result is discarded, a deferred Close - still gets the reason.
func TestTheCauseSurvivesAnIgnoredWrite(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return errHandler
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(w, strings.NewReader(`<p>a</p>`))
	if err := w.Close(); !errors.Is(err, errHandler) {
		t.Errorf("Close lost the cause: %v", err)
	}
}

// Every later Write says the same thing, so it does not matter which call the
// caller happens to check.
func TestEveryLaterCallCarriesTheCause(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return errHandler
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p>`)); !errors.Is(err, errHandler) {
		t.Fatalf("Write: %v", err)
	}
	for i := range 3 {
		_, err := w.Write([]byte(`<p>b</p>`))
		if !errors.Is(err, lolhtml.ErrPoisoned) || !errors.Is(err, errHandler) {
			t.Errorf("write %d after poisoning: %v", i, err)
		}
	}
	if err := w.Close(); !errors.Is(err, errHandler) {
		t.Errorf("Close: %v", err)
	}
}

// A destination that fails is the other way in, and it is the one where the
// cause is least guessable from the outside.
func TestADestinationErrorSurvivesToClose(t *testing.T) {
	errDst := errors.New("disk full")
	w, err := lolhtml.NewWriter(alwaysFails{errDst}, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p>`)); !errors.Is(err, errDst) {
		t.Fatalf("Write did not report the destination error: %v", err)
	}
	if err := w.Close(); !errors.Is(err, errDst) {
		t.Errorf("Close lost the destination error: %v", err)
	}
}

// alwaysFails is the simplest destination that cannot accept anything.
type alwaysFails struct{ err error }

func (f alwaysFails) Write([]byte) (int, error) { return 0, f.err }

// A handler panic poisons the Writer on its way to the caller and leaves no
// error behind, so the sentinel stands alone. Checked because the wrapping must
// not invent a cause.
func TestAPanicLeavesTheBareSentinel(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		panic("boom")
	}))
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the panic did not reach the caller")
			}
		}()
		_, _ = w.Write([]byte(`<p>a</p>`))
	}()
	err = w.Close()
	if !errors.Is(err, lolhtml.ErrPoisoned) {
		t.Errorf("Close after a panic: %v", err)
	}
	if err.Error() != lolhtml.ErrPoisoned.Error() {
		t.Errorf("Close after a panic added something: %v", err)
	}
}

// A Writer that never failed reports nothing from Close, and a second Close is
// still nil - the poisoning wrapper must not change either.
func TestACleanWriterIsUnaffected(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p>`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if out.String() != `<p>a</p>` {
		t.Errorf("output = %q", out.String())
	}
}

// Close on a poisoned Writer must still release the rewriter, which is what
// the handle counter is for.
func TestAPoisonedWriterStillReleases(t *testing.T) {
	before := lolhtml.LiveHandles()
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return errHandler
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<p>a</p>`))
	if err := w.Close(); !errors.Is(err, errHandler) {
		t.Fatalf("Close: %v", err)
	}
	if after := lolhtml.LiveHandles(); after != before {
		t.Errorf("handles: %d before, %d after", before, after)
	}
}

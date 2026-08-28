package lolhtml_test

// A handler runs in the middle of lol_html_rewriter_write, on the same stack,
// and lol-html has no idea it is being called. Calling back into the Writer
// from there is not a Go-level nuisance but memory-unsafety:
//
//	a nested Write   hands the same rewriter to Rust twice - a second &mut
//	                 alias - and corrupts the parser state, which surfaces, if
//	                 at all, as an internal consistency error against a
//	                 document that was fine
//	a nested Close   finishes the document and then frees the rewriter and
//	                 every handle underneath the call still running on them, so
//	                 the outer write continues on freed memory
//
// Both were reachable, and the second was demonstrated: a handler calling Close
// got nil back, and the process then died inside the output sink of a rewriter
// that had already been freed. This file pins the refusal that replaced it, and
// the two things the refusal must not cost - the interrupted call still runs to
// completion, and the guard is clear again afterwards.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const reentrantDoc = `<!DOCTYPE html><p>text<!--c--></p>`

// passthrough is what reentrantDoc rewrites to when the handler changes
// nothing, and so what a refused reentrant call must not disturb.
const passthrough = reentrantDoc

// callbackWriter runs on once, on its first Write, and is otherwise a buffer.
// The destination is called from the sink, which runs on the same stack as a
// handler and can re-enter the same way.
type callbackWriter struct {
	buf   bytes.Buffer
	on    func()
	fired bool
}

func (c *callbackWriter) Write(p []byte) (int, error) {
	if !c.fired {
		c.fired = true
		if c.on != nil {
			c.on()
		}
	}
	return c.buf.Write(p)
}

func TestReentrantWriteFromAHandlerIsRefused(t *testing.T) {
	before := settledHandles()

	var out bytes.Buffer
	var w *lolhtml.Writer
	var inner error
	calls := 0

	opt := lolhtml.OnElement("p", func(*lolhtml.Element) error {
		calls++
		_, inner = w.Write([]byte("<b>nested</b>"))
		return nil
	})

	w, err := lolhtml.NewWriter(&out, opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(reentrantDoc)); err != nil {
		t.Fatalf("the outer Write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if calls != 1 {
		t.Fatalf("the handler ran %d times, want 1", calls)
	}
	if !errors.Is(inner, lolhtml.ErrReentrant) {
		t.Errorf("the nested Write returned %v, want ErrReentrant", inner)
	}
	if got := out.String(); got != passthrough {
		t.Errorf("output = %q, want %q", got, passthrough)
	}
	requireNoHandleLeak(t, before)
}

// TestReentrantCloseFromAHandlerIsRefused is the case that crashed. The refusal
// has to leave the Writer completely untouched: the outer write is still
// running on the rewriter a successful Close would have freed, and the caller's
// own Close afterwards is the one that must do the work.
func TestReentrantCloseFromAHandlerIsRefused(t *testing.T) {
	before := settledHandles()

	var out bytes.Buffer
	var w *lolhtml.Writer
	var inner error

	opt := lolhtml.OnElement("p", func(*lolhtml.Element) error {
		inner = w.Close()
		return nil
	})

	w, err := lolhtml.NewWriter(&out, opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(reentrantDoc)); err != nil {
		t.Fatalf("the outer Write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("the real Close failed: %v", err)
	}

	if !errors.Is(inner, lolhtml.ErrReentrant) {
		t.Errorf("the nested Close returned %v, want ErrReentrant", inner)
	}
	if got := out.String(); got != passthrough {
		t.Errorf("output = %q, want %q", got, passthrough)
	}
	requireNoHandleLeak(t, before)
}

// TestReentrantCallsFromTheDestinationAreRefused: the destination writer is
// called from the output sink, which is as much inside the call as a handler is.
func TestReentrantCallsFromTheDestinationAreRefused(t *testing.T) {
	before := settledHandles()

	var w *lolhtml.Writer
	var wrote, closed error

	dst := &callbackWriter{}
	dst.on = func() {
		_, wrote = w.Write([]byte("<b>nested</b>"))
		closed = w.Close()
	}

	w, err := lolhtml.NewWriter(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(reentrantDoc)); err != nil {
		t.Fatalf("the outer Write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("the real Close failed: %v", err)
	}

	if !dst.fired {
		t.Fatal("the destination was never written to")
	}
	if !errors.Is(wrote, lolhtml.ErrReentrant) {
		t.Errorf("the nested Write returned %v, want ErrReentrant", wrote)
	}
	if !errors.Is(closed, lolhtml.ErrReentrant) {
		t.Errorf("the nested Close returned %v, want ErrReentrant", closed)
	}
	if got := dst.buf.String(); got != passthrough {
		t.Errorf("output = %q, want %q", got, passthrough)
	}
	requireNoHandleLeak(t, before)
}

// TestTheReentrancyGuardClearsBetweenCalls: the guard is per-call, not
// sticky. A Writer driven the ordinary way never sees it.
func TestTheReentrancyGuardClearsBetweenCalls(t *testing.T) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	for i, chunk := range []string{"<!DOCTYPE html>", "<p>te", "xt<!--c--></p>"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := out.String(); got != passthrough {
		t.Errorf("output = %q, want %q", got, passthrough)
	}
}

// TestTheReentrancyGuardClearsAfterARecoveredPanic: the guard is cleared by the
// same deferred call that releases on a panic, so a caller who recovers gets the
// poison it has always got rather than a Writer that refuses everything as
// reentrant for the rest of its life.
func TestTheReentrancyGuardClearsAfterARecoveredPanic(t *testing.T) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		panic(panicValue)
	}))
	if err != nil {
		t.Fatal(err)
	}

	if v := recovered(func() { w.Write([]byte(reentrantDoc)) }); v == nil {
		t.Fatal("the handler panic did not reach the caller")
	}

	_, werr := w.Write([]byte("more"))
	if errors.Is(werr, lolhtml.ErrReentrant) {
		t.Fatalf("Write after a recovered panic reported reentrancy: %v", werr)
	}
	if !errors.Is(werr, lolhtml.ErrPoisoned) {
		t.Errorf("Write after a recovered panic returned %v, want ErrPoisoned", werr)
	}
	if cerr := w.Close(); !errors.Is(cerr, lolhtml.ErrPoisoned) {
		t.Errorf("Close after a recovered panic returned %v, want ErrPoisoned", cerr)
	}
}

// TestReentrantErrorNamesTheProblem: the message is what a caller sees first,
// and "writer is closed" would have sent them looking in the wrong place.
func TestReentrantErrorNamesTheProblem(t *testing.T) {
	if got := lolhtml.ErrReentrant.Error(); !strings.Contains(got, "re-entered") {
		t.Errorf("ErrReentrant = %q, want it to say what happened", got)
	}
}

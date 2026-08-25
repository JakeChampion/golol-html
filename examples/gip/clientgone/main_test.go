package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestTheDocumentEndHandlerDoesNotRun is the point of the program: a summary written where a
// rewrite naturally writes one is exactly the summary a broken destination takes away.
func TestTheDocumentEndHandlerDoesNotRun(t *testing.T) {
	r, err := Rewrite(100, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	if r.DocumentEndRan {
		t.Error("the document-end handler ran after the destination failed")
	}
	if r.WriteErr == nil {
		t.Fatal("the destination did not fail")
	}

	// And it does run when the destination holds up, so the test above is about the
	// failure rather than about the handler never running at all.
	whole, err := Rewrite(1<<20, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	if !whole.DocumentEndRan {
		t.Error("the document-end handler did not run on a successful rewrite")
	}
	if whole.WriteErr != nil || whole.CloseErr != nil {
		t.Errorf("a generous budget still failed: %v / %v", whole.WriteErr, whole.CloseErr)
	}
}

// TestTheCountsAreWhatTheRewriteReached, and are smaller than the page's totals.
func TestTheCountsAreWhatTheRewriteReached(t *testing.T) {
	partial, err := Rewrite(100, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := Rewrite(1<<20, 0, 740)
	if err != nil {
		t.Fatal(err)
	}

	if partial.Links == 0 {
		t.Error("the rewrite stopped before any link, which makes the comparison empty")
	}
	if partial.Links >= whole.Links {
		t.Errorf("counted %d links against %d for the whole page", partial.Links, whole.Links)
	}
	if partial.Comments >= whole.Comments || partial.TextChunks >= whole.TextChunks {
		t.Errorf("comments %d/%d and chunks %d/%d", partial.Comments, whole.Comments,
			partial.TextChunks, whole.TextChunks)
	}
}

// TestTheFailureIsFindableFromEitherCall, which is what lets a caller check Close alone.
func TestTheFailureIsFindableFromEitherCall(t *testing.T) {
	r, err := Rewrite(100, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.WriteErr, ErrGone) {
		t.Errorf("write reported %v", r.WriteErr)
	}
	if !errors.Is(r.CloseErr, ErrGone) {
		t.Errorf("close reported %v", r.CloseErr)
	}
	if !errors.Is(r.CloseErr, lolhtml.ErrPoisoned) {
		t.Errorf("close did not report the poisoning: %v", r.CloseErr)
	}
	if r.ReportedBy() != "Write" {
		t.Errorf("reported by %q", r.ReportedBy())
	}
}

// TestWhereItStopsDoesNotDependOnTheWrites. The budget is a fact about the destination and the
// page is a fact about the document, so the place the rewrite stops should not move with the
// caller's write sizes.
func TestWhereItStopsDoesNotDependOnTheWrites(t *testing.T) {
	var first *Run
	for _, write := range []int{0, 64, 7, 1} {
		r, err := Rewrite(100, write, 740)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = r
			continue
		}
		if r.Links != first.Links || r.Comments != first.Comments || r.Accepted != first.Accepted {
			t.Errorf("write size %d stopped with %d links, %d comments and %d bytes accepted; "+
				"one write stopped with %d, %d and %d", write, r.Links, r.Comments, r.Accepted,
				first.Links, first.Comments, first.Accepted)
		}
	}
}

// TestOnlySomeDocumentsWriteDuringClose, which is what decides whether Close can be the call
// that discovers a broken destination.
func TestOnlySomeDocumentsWriteDuringClose(t *testing.T) {
	tests := []struct {
		doc    string
		writes bool
	}{
		{`<p>text</p>`, false},
		{`<p>unclosed text`, false},
		{`<script>var a =`, false},
		{`<p>a</p`, true},
		{`<div a="x`, true},
		{`<!--unclosed`, true},
		{`<p>text</p><`, true},
	}

	for _, tt := range tests {
		t.Run(tt.doc, func(t *testing.T) {
			_, during, closeErr, err := CloseWrites(tt.doc, false)
			if err != nil {
				t.Fatal(err)
			}
			if (during > 0) != tt.writes {
				t.Errorf("wrote %d times during Close, expected writes: %v", during, tt.writes)
			}
			// And a write during Close against a destination that cannot take it is an
			// error from Close, which is the reason the distinction matters.
			if tt.writes && !errors.Is(closeErr, ErrGone) {
				t.Errorf("Close wrote and reported %v", closeErr)
			}
			if !tt.writes && closeErr != nil {
				t.Errorf("Close wrote nothing and reported %v", closeErr)
			}
		})
	}

	// A handler appending at the document end makes any document write during Close.
	_, during, closeErr, err := CloseWrites(`<p>text</p>`, true)
	if err != nil {
		t.Fatal(err)
	}
	if during == 0 {
		t.Error("appending at the document end wrote nothing during Close")
	}
	if !errors.Is(closeErr, ErrGone) {
		t.Errorf("and Close reported %v", closeErr)
	}
}

// TestTheDestinationKeepsWhatItAccepted: the rewriter retracts nothing, so a partial response
// is a real response as far as it goes.
func TestTheDestinationKeepsWhatItAccepted(t *testing.T) {
	for _, budget := range []int{0, 1, 40, 100, 500} {
		r, err := Rewrite(budget, 0, 740)
		if err != nil {
			t.Fatal(err)
		}
		if r.Accepted > budget {
			t.Errorf("budget %d: the destination accepted %d bytes", budget, r.Accepted)
		}
		if budget > 0 && r.WriteErr == nil {
			t.Errorf("budget %d: nothing failed", budget)
		}
	}
}

// TestTheReportSaysWhatSurvivedAndWhatDidNot, since the output is the point of the program.
func TestTheReportSaysWhatSurvivedAndWhatDidNot(t *testing.T) {
	r, err := Rewrite(100, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	out := r.String()
	for _, want := range []string{
		"and then failed",
		"reported by",
		"the count when the rewrite stopped",
		"document end ran",
		"a summary written there would not have run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	whole, err := Rewrite(1<<20, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	if out := whole.String(); !strings.Contains(out, "accepted all") {
		t.Errorf("a successful run reported:\n%s", out)
	}
	if out := whole.String(); strings.Contains(out, "would not have run") {
		t.Errorf("a successful run warned about the summary:\n%s", out)
	}
}

// TestABudgetOfZeroFailsOnTheFirstWrite, which is the edge the report has to survive.
//
// One link is still rewritten, and that is not a bug: handlers run as tokens are parsed and
// the destination is written to afterwards, so the first handler has already done its work
// before the destination gets a chance to refuse anything. A destination that accepts nothing
// still sees one element's worth of rewriting attempted.
func TestABudgetOfZeroFailsOnTheFirstWrite(t *testing.T) {
	r, err := Rewrite(0, 0, 740)
	if err != nil {
		t.Fatal(err)
	}
	if r.Accepted != 0 {
		t.Errorf("accepted %d bytes on a budget of none", r.Accepted)
	}
	if r.Links != 1 {
		t.Errorf("counted %d links, want the one whose output the destination refused",
			r.Links)
	}
	if !errors.Is(r.WriteErr, ErrGone) {
		t.Errorf("write reported %v", r.WriteErr)
	}
	if out := r.String(); !strings.Contains(out, "accepted 0 of") {
		t.Errorf("the report says:\n%s", out)
	}
}

package main

import (
	"strings"
	"testing"
)

// smallSize keeps the suite quick: a megabyte is enough for an unbounded pattern to hold
// thousands of handles and for a bounded one to hold none, which is the difference being
// measured. The program's default is larger because a reader wants the peak figures to look
// like a real workload.
const smallSize = 1 << 20

// TestEveryPatternMeasuresWhatItClaims is the program as a test: each row says whether it is
// bounded, and the measurement has to agree.
func TestEveryPatternMeasuresWhatItClaims(t *testing.T) {
	gs, err := Measure(Patterns, smallSize, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Disagreements(gs) {
		t.Error(d)
	}
	if len(gs) != len(Patterns) {
		t.Errorf("measured %d patterns of %d", len(gs), len(Patterns))
	}
}

// TestBothAnswersAreRepresented. A table where everything is bounded would pass the test
// above while measuring nothing, so the point is that the corpus of patterns contains both
// kinds - and that the unbounded ones are the two documented calls rather than a surprise.
func TestBothAnswersAreRepresented(t *testing.T) {
	var bounded, held int
	for _, p := range Patterns {
		if p.Bounded {
			bounded++
			continue
		}
		held++
		if !strings.Contains(p.Name, "OnEndTag") && !strings.Contains(p.Name, "SetUserData") {
			t.Errorf("%q is listed as unbounded and is neither an end-tag registration nor "+
				"user data, which are the two calls that hold anything", p.Name)
		}
	}
	if bounded < 3 || held < 3 {
		t.Errorf("%d bounded patterns and %d unbounded: both need to be represented",
			bounded, held)
	}
}

// TestTheGrowthMeasurementCanTellTheDifference, on the two rows where the answer is known:
// reading elements holds nothing however large the document, and attaching user data to each
// one holds a handle apiece.
func TestTheGrowthMeasurementCanTellTheDifference(t *testing.T) {
	find := func(name string) Pattern {
		for _, p := range Patterns {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("no pattern %q", name)
		return Pattern{}
	}

	gs, err := Measure([]Pattern{find("element handler"), find("element + SetUserData")},
		smallSize, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	if gs[0].Held() {
		t.Errorf("reading elements measured as holding memory: %.2fx", gs[0].Ratio())
	}
	if !gs[1].Held() {
		t.Errorf("user data on every element measured as bounded: %.2fx", gs[1].Ratio())
	}
	if gs[1].Small.PeakHeap <= gs[0].Small.PeakHeap {
		t.Errorf("user data peaked at %d bytes and reading at %d",
			gs[1].Small.PeakHeap, gs[0].Small.PeakHeap)
	}
}

// TestClearingUserDataIsTheMitigation, which is the row a reader is meant to copy.
func TestClearingUserDataIsTheMitigation(t *testing.T) {
	var held, cleared Pattern
	for _, p := range Patterns {
		switch p.Name {
		case "element + SetUserData":
			held = p
		case "element + SetUserData(nil)":
			cleared = p
		}
	}
	if held.Options == nil || cleared.Options == nil {
		t.Fatal("the two user-data patterns are not both in the table")
	}

	gs, err := Measure([]Pattern{held, cleared}, smallSize, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !gs[0].Held() || gs[1].Held() {
		t.Errorf("holding measured %.2fx and clearing %.2fx: clearing is supposed to be the "+
			"difference", gs[0].Ratio(), gs[1].Ratio())
	}
}

// TestTheReportNamesTheDisagreement, since the exit status alone does not say which row.
func TestTheReportNamesTheDisagreement(t *testing.T) {
	// A fabricated pair: a pattern claiming to be bounded whose measurement grew. The
	// figures are above the visibility floor on purpose, since below it a ratio is noise
	// and the program says so by declining to draw a conclusion.
	gs := []Growth{
		{"honest", true, Result{PeakHeap: 16 << 20}, Result{PeakHeap: 16 << 20}, 4},
		{"wrong", true, Result{PeakHeap: 16 << 20}, Result{PeakHeap: 64 << 20}, 4},
	}
	d := Disagreements(gs)
	if len(d) != 1 || !strings.Contains(d[0], "wrong") {
		t.Errorf("reported %v", d)
	}
	out := report(gs, smallSize, 4096, 4)
	if !strings.Contains(out, "wrong is listed as bounded and measured unbounded") {
		t.Errorf("the report does not name it:\n%s", out)
	}
	if !strings.Contains(out, "peak heap") || !strings.Contains(out, "growth") {
		t.Errorf("the table lost its header:\n%s", out)
	}
}

// TestTheDocumentIsNeverHeld: the generator writes a repeating unit and the output goes to
// io.Discard, so a peak that grows is the rewriter's memory rather than the caller's. This
// checks the arithmetic of the feeder - that it writes exactly the size asked for, in writes
// of the size asked for.
func TestTheDocumentIsNeverHeld(t *testing.T) {
	res, err := Rewrite(Patterns[0], 40000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 40000 {
		t.Errorf("wrote %d bytes of 40000", res.Bytes)
	}

	// A size that is not a multiple of the write size still comes out exact.
	res, err = Rewrite(Patterns[0], 4097, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 4097 {
		t.Errorf("wrote %d bytes of 4097", res.Bytes)
	}
}

// TestASmallPeakIsNotAConclusion. Below the visibility floor the ratio between two
// measurements is the sampler's noise, so the program declines to call it growth - which is
// what stops "no handlers" being reported as unbounded because the heap moved by half a
// megabyte for reasons that have nothing to do with the rewrite.
func TestASmallPeakIsNotAConclusion(t *testing.T) {
	noisy := Growth{"tiny", true, Result{PeakHeap: 200 << 10}, Result{PeakHeap: 800 << 10}, 4}
	if noisy.Held() {
		t.Errorf("a peak of %d bytes growing to %d was called growth",
			noisy.Small.PeakHeap, noisy.Large.PeakHeap)
	}

	real := Growth{"large", false, Result{PeakHeap: 32 << 20}, Result{PeakHeap: 128 << 20}, 4}
	if !real.Held() {
		t.Error("a peak of 32 MB growing to 128 MB was not called growth")
	}
}

// TestPerMBFallsForABoundedPattern, which is the arithmetic behind the growth column.
func TestPerMBFallsForABoundedPattern(t *testing.T) {
	small := Result{Bytes: 1 << 20, PeakHeap: 4 << 20}
	large := Result{Bytes: 4 << 20, PeakHeap: 4 << 20}
	if !(large.PerMB() < small.PerMB()) {
		t.Errorf("per-MB rose from %.0f to %.0f for a flat peak", small.PerMB(), large.PerMB())
	}
	if (Result{}).PerMB() != 0 {
		t.Error("an empty result divided by zero")
	}
}

package main

import (
	"strings"
	"testing"
	"time"
)

// TestNoCrossTalk is the property the program checks: every output is what that document
// rewrites to on its own, whoever rewrote it.
func TestNoCrossTalk(t *testing.T) {
	for _, workers := range []int{1, 2, 8} {
		out, err := Run(makeItems(60, 256), workers, 4)
		if err != nil {
			t.Fatalf("%d workers: %v", workers, err)
		}
		if out.CrossTalk != 0 {
			t.Errorf("%d workers: %d of %d outputs were not what their document rewrites to",
				workers, out.CrossTalk, out.Items)
		}
		if out.Items != 60 {
			t.Errorf("%d workers: %d items came back", workers, out.Items)
		}
	}
}

// TestEveryItemComesBackExactlyOnce, which is the other half of a queue being correct: an item
// dropped or duplicated is a bug in the driving code rather than in the rewrites, and Run
// reports it as an error.
func TestEveryItemComesBackExactlyOnce(t *testing.T) {
	out, err := Run(makeItems(200, 128), 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.Items != 200 {
		t.Errorf("%d items", out.Items)
	}
	// Every document has one anchor per repetition, and each is rewritten once.
	if out.Matched == 0 {
		t.Error("nothing was rewritten")
	}
}

// TestTheDocumentsCarryTheirOwnIdentity, so that a swapped output would be caught. Without
// this, a cross-talk check over identical documents proves nothing.
func TestTheDocumentsCarryTheirOwnIdentity(t *testing.T) {
	items := makeItems(4, 64)
	for i, item := range items {
		want := "/" + itoa(i)
		if !strings.Contains(item.Doc, want) {
			t.Errorf("item %d does not mention %q: %s", i, want, item.Doc)
		}
		for j := range items {
			if j == i {
				continue
			}
			if strings.Contains(item.Doc, `"/`+itoa(j)+`"`) {
				t.Errorf("item %d contains item %d's URL", i, j)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestOneSlowItemBarelyMovesTheReading, which is why the reported figures are medians. This
// test does no timing of its own: it builds the samples a run would have collected, adds the
// one preempted item that a loaded machine hands out, and checks the reading holds. With a mean
// the same outlier moves the build share from 20% to 82%, which is the distance between two of
// advice's three answers.
func TestOneSlowItemBarelyMovesTheReading(t *testing.T) {
	quiet := Outcome{Items: 60}
	for range 60 {
		quiet.Builds = append(quiet.Builds, 2*time.Microsecond)
		quiet.Totals = append(quiet.Totals, 10*time.Microsecond)
	}
	loud := Outcome{Items: 60, Builds: append([]time.Duration(nil), quiet.Builds...),
		Totals: append([]time.Duration(nil), quiet.Totals...)}
	loud.Builds[17] += 2 * time.Millisecond
	loud.Totals[17] += 2 * time.Millisecond

	if quiet.BuildShare() != loud.BuildShare() {
		t.Errorf("one slow item moved the share from %.3f to %.3f",
			quiet.BuildShare(), loud.BuildShare())
	}

	// And the mean it replaced would have moved a long way, so the change is worth its
	// keep rather than a distinction without a difference.
	mean := func(o Outcome) float64 {
		var build, total time.Duration
		for i := range o.Builds {
			build += o.Builds[i]
			total += o.Totals[i]
		}
		return float64(build) / float64(total)
	}
	if quiet, loud := mean(quiet), mean(loud); loud-quiet < 0.3 {
		t.Errorf("the mean moved from %.3f to %.3f, so this outlier does not "+
			"demonstrate the problem", quiet, loud)
	}
}

// TestTheMedianIgnoresTheOrderTheSamplesArrivedIn, since a queue hands them back in whatever
// order the workers finish and the statistic must not depend on that.
func TestTheMedianIgnoresTheOrderTheSamplesArrivedIn(t *testing.T) {
	rising := []time.Duration{1, 2, 3, 4, 5}
	falling := []time.Duration{5, 4, 3, 2, 1}
	shuffled := []time.Duration{3, 1, 5, 2, 4}
	for _, samples := range [][]time.Duration{rising, falling, shuffled} {
		if got := median(samples); got != 3 {
			t.Errorf("median(%v) = %v", samples, got)
		}
	}
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %v", got)
	}
	if got := median([]time.Duration{7}); got != 7 {
		t.Errorf("median of one sample = %v", got)
	}
	// An even count takes a reading rather than averaging two of them.
	if got := median([]time.Duration{2, 4}); got != 4 {
		t.Errorf("median of two samples = %v", got)
	}
}

// TestBuildingIsMostOfTheWorkForSmallItemsWithManySelectors, which is the measurement the
// program exists to make. The assertion is on the ratio of two medians from the same run, so it
// does not depend on the machine's speed or on its load - only on the shape.
func TestBuildingIsMostOfTheWorkForSmallItemsWithManySelectors(t *testing.T) {
	many, err := Run(makeItems(60, 128), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	few, err := Run(makeItems(60, 128), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if many.BuildShare() <= few.BuildShare() {
		t.Errorf("fifty selectors spent %.0f%% of the work building and one selector %.0f%%",
			many.BuildShare()*100, few.BuildShare()*100)
	}
	if many.BuildShare() < 0.2 {
		t.Errorf("fifty selectors over 128-byte documents spent only %.0f%% of the work "+
			"building rewriters", many.BuildShare()*100)
	}

	// And a big document turns it round: the same rule set is a small share of the work.
	big, err := Run(makeItems(20, 32<<10), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if big.BuildShare() >= many.BuildShare() {
		t.Errorf("32 KB documents spent %.0f%% of the work building and 128-byte ones %.0f%%",
			big.BuildShare()*100, many.BuildShare()*100)
	}
}

// TestTheAdviceFollowsTheMeasurement, since it is the line a reader acts on.
func TestTheAdviceFollowsTheMeasurement(t *testing.T) {
	if got := advice(0.9); !strings.Contains(got, "most of the time") {
		t.Errorf("advice(0.9) = %q", got)
	}
	if got := advice(0.3); !strings.Contains(got, "noticeable") {
		t.Errorf("advice(0.3) = %q", got)
	}
	if got := advice(0.01); !strings.Contains(got, "noise") {
		t.Errorf("advice(0.01) = %q", got)
	}
}

// TestTheReportIsCoherent: the wall clock and the worker time are different measurements and
// the report says which is which, because summed worker time can exceed elapsed time.
func TestTheReportIsCoherent(t *testing.T) {
	out, err := Run(makeItems(40, 512), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{"cross-talk", "wall clock", "median item", "advice"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	if out.BuildShare() > 1 {
		t.Errorf("the build share is %.2f, which cannot be", out.BuildShare())
	}
	if out.PerItemBuild() > out.PerItemWork() {
		t.Errorf("building took %v of %v per item", out.PerItemBuild(), out.PerItemWork())
	}
}

// TestScanReportsEveryWorkerCountItTried, and picks the fastest rather than the largest.
func TestScanReportsEveryWorkerCountItTried(t *testing.T) {
	report, err := Scan(makeItems(40, 256), 4, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workers", "items/sec", "speedup", "fastest at"} {
		if !strings.Contains(report, want) {
			t.Errorf("the scan does not mention %q:\n%s", want, report)
		}
	}
	if lines := strings.Count(report, "\n"); lines != 7 {
		t.Errorf("the scan printed %d lines for two worker counts:\n%s", lines, report)
	}

	if _, err := Scan(nil, 4, []int{1}); err == nil {
		t.Error("scanning an empty queue succeeded")
	}
}

// TestAtLeastOneWorker, since zero would hang rather than fail.
func TestAtLeastOneWorker(t *testing.T) {
	if _, err := Run(makeItems(2, 64), 0, 1); err == nil {
		t.Error("zero workers was accepted")
	}
}

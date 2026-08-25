package main

import (
	"math"
	"strings"
	"testing"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
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

// TestTheClockCanResolveWhatWeAskItTo, which is the first thing that went wrong here: the
// Windows runner reported every per-item figure as exactly zero, because its clock ticks more
// coarsely than an item takes. So the tick is measured, it is in the report, and a queue too
// short for it gets a sentence instead of a number.
func TestTheClockCanResolveWhatWeAskItTo(t *testing.T) {
	tick := clockTick()
	t.Logf("this platform's clock ticks every %v", tick)
	if tick <= 0 {
		t.Fatalf("clockTick() = %v", tick)
	}

	// One item is a few microseconds of work, so this is unresolvable on any clock coarser
	// than that and the report has to say so rather than print 0%.
	short, err := Run(makeItems(1, 64), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if short.Wall < 20*short.Tick {
		if short.Resolvable() {
			t.Errorf("a queue of one item taking %v is resolvable at a tick of %v",
				short.Wall, short.Tick)
		}
		if !strings.Contains(short.String(), "too coarse") {
			t.Errorf("an unresolvable queue printed advice anyway:\n%s", short.String())
		}
	}

	// The tick belongs in the report either way, because a reader comparing two machines
	// needs to know which of them could see what.
	if !strings.Contains(short.String(), "clock tick") {
		t.Errorf("the report does not say what the clock tick was:\n%s", short.String())
	}
}

// TestTheAllocationShareBarelyMovesBetweenRuns, which is why the assertions further down rest on
// it: it is a count rather than an interval, so it has no clock in it and no scheduler either.
// Barely rather than not at all - the tolerance is one allocation, because the malloc counter
// includes the runtime's own and those depend on the state of the heap. Measured, the figure for
// one worker and for four differs by at most one in four hundred, in either direction, where the
// timing it replaced ranged two and a half fold.
func TestTheAllocationShareBarelyMovesBetweenRuns(t *testing.T) {
	items := makeItems(30, 128)
	first, err := Run(items, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(items, 4, 50)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(first.AllocsBuild-second.AllocsBuild) > 1 ||
		math.Abs(first.AllocsFull-second.AllocsFull) > 1 {
		t.Errorf("one worker counted %.0f of %.0f allocations and four counted %.0f of %.0f",
			first.AllocsBuild, first.AllocsFull, second.AllocsBuild, second.AllocsFull)
	}
	if math.Abs(first.AllocShare()-second.AllocShare()) > 0.01 {
		t.Errorf("the share moved from %.4f to %.4f", first.AllocShare(), second.AllocShare())
	}
	if first.AllocsBuild == 0 || first.AllocsFull == 0 {
		t.Fatalf("counted %.0f of %.0f allocations", first.AllocsBuild, first.AllocsFull)
	}
	if first.AllocsBuild > first.AllocsFull {
		t.Errorf("building alone allocated %.0f and the whole item %.0f",
			first.AllocsBuild, first.AllocsFull)
	}
}

// TestTheOverheadPassDiffersOnlyByTheWrite, since the whole method rests on that: if the two
// passes differed in any other way, their ratio would not be the cost of rewriting.
func TestTheOverheadPassDiffersOnlyByTheWrite(t *testing.T) {
	items := makeItems(40, 256)

	// The overhead pass writes nothing, so it must find no matches at all - if it did, it
	// would be doing the rewriting too and the ratio would mean nothing.
	_, matched, crossTalk, err := drive(items, 2, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 {
		t.Errorf("the overhead pass rewrote %d elements", matched)
	}
	if crossTalk != 0 {
		t.Errorf("the overhead pass reported %d cross-talk", crossTalk)
	}

	want := make([]string, len(items))
	for i, item := range items {
		opts, _ := options(4)
		got, err := lolhtml.RewriteString(item.Doc, opts...)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = got
	}
	_, matched, crossTalk, err = drive(items, 2, 4, want)
	if err != nil {
		t.Fatal(err)
	}
	if matched == 0 {
		t.Error("the work pass rewrote nothing")
	}
	if crossTalk != 0 {
		t.Errorf("the work pass reported %d cross-talk", crossTalk)
	}
}

// TestTheFastestPassIsTheOneKept, since noise can only lengthen an interval.
func TestTheFastestPassIsTheOneKept(t *testing.T) {
	samples := []time.Duration{9 * time.Millisecond, 3 * time.Millisecond, 40 * time.Millisecond}
	if got := fastest(samples); got != 3*time.Millisecond {
		t.Errorf("fastest(%v) = %v", samples, got)
	}
	if got := fastest(nil); got != 0 {
		t.Errorf("fastest(nil) = %v", got)
	}
	// Order must not matter: a queue's passes arrive in the order they ran.
	if got := fastest([]time.Duration{3, 9, 40}); got != 3 {
		t.Errorf("fastest of a rising run = %v", got)
	}
}

// TestBuildingIsMostOfTheWorkForSmallItemsWithManySelectors, which is the measurement the
// program exists to make. The gate is on allocation counts, which are the same number on every
// platform and under any load; the timing is checked too, where the clock can resolve it.
func TestBuildingIsMostOfTheWorkForSmallItemsWithManySelectors(t *testing.T) {
	many, err := Run(makeItems(200, 128), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	few, err := Run(makeItems(200, 128), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	big, err := Run(makeItems(20, 32<<10), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("allocation shares: fifty selectors %.3f, one selector %.3f, 32 KB %.3f; "+
		"time shares %.3f, %.3f, %.3f",
		many.AllocShare(), few.AllocShare(), big.AllocShare(),
		many.BuildShare(), few.BuildShare(), big.BuildShare())

	if many.AllocShare() <= few.AllocShare() {
		t.Errorf("fifty selectors put %.0f%% of the allocations in construction and one "+
			"selector %.0f%%", many.AllocShare()*100, few.AllocShare()*100)
	}
	if many.AllocShare() < 0.2 {
		t.Errorf("fifty selectors over 128-byte documents put only %.0f%% of the "+
			"allocations in construction", many.AllocShare()*100)
	}
	// And a big document turns it round: the same rule set is a small share of the work.
	if big.AllocShare() >= many.AllocShare() {
		t.Errorf("32 KB documents put %.0f%% of the allocations in construction and "+
			"128-byte ones %.0f%%", big.AllocShare()*100, many.AllocShare()*100)
	}

	// The time share is the figure a caller actually feels, and it should rank the three the
	// same way - but only where the clock can resolve the intervals. On a coarse clock this
	// says what it could not measure rather than passing quietly.
	for _, o := range []Outcome{many, few, big} {
		if !o.Resolvable() {
			t.Logf("not checking the time share: %d items of %d bytes with %d "+
				"selectors took %v, overhead pass %v, against a tick of %v",
				o.Items, o.Size, o.Selectors, o.Wall, o.Overhead, o.Tick)
			return
		}
	}
	if many.BuildShare() <= few.BuildShare() {
		t.Errorf("fifty selectors spent %.0f%% of the time building and one selector %.0f%%",
			many.BuildShare()*100, few.BuildShare()*100)
	}
	if big.BuildShare() >= many.BuildShare() {
		t.Errorf("32 KB documents spent %.0f%% of the time building and 128-byte ones %.0f%%",
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

// TestTheReportIsCoherent: the two passes are different measurements and the report says which
// is which, since the whole method is their ratio.
func TestTheReportIsCoherent(t *testing.T) {
	out, err := Run(makeItems(40, 512), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{"cross-talk", "wall clock", "build and close", "advice"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	if out.BuildShare() > 1 {
		t.Errorf("the build share is %.2f, which cannot be", out.BuildShare())
	}
	if out.PerItemBuild() > out.PerItem() {
		t.Errorf("building took %v of %v per item", out.PerItemBuild(), out.PerItem())
	}
	if out.Overhead > out.Wall {
		t.Errorf("the overhead pass took %v and the work pass %v", out.Overhead, out.Wall)
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

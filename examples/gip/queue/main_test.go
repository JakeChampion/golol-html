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
//
// Barely rather than not at all, and the size of "barely" is the same one alloc_test.go writes
// down for the same reason: a count is reproducible for a given input and toolchain, but the
// fixed part of it is not identical every time, because the malloc counter includes the
// runtime's own allocations and those depend on the state of the heap. This machine moves by one
// in four hundred; the macOS and Linux arm64 runners moved by two in four hundred and fifty.
// Eight is the tolerance the root gate uses, so it is the one used here.
//
// What the gate actually needs is the share, and that is asserted an order of magnitude tighter:
// the three shares it compares are 0.33 and 0.80 apart, so 0.01 separates signal from noise
// thirty times over. The timing this replaced ranged two and a half fold.
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
	const tolerance = 8
	if math.Abs(first.AllocsBuild-second.AllocsBuild) > tolerance ||
		math.Abs(first.AllocsFull-second.AllocsFull) > tolerance {
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
	// The overhead pass being shorter than the work pass is not an invariant: they are two
	// separate runs, and on a small queue a loaded machine can put them either way round.
	// What the program must not do is print a share it cannot support, so the assertion is
	// on the report rather than on the timings.
	if out.Resolvable() {
		if out.BuildShare() > 1 {
			t.Errorf("a resolvable run has a build share of %.2f", out.BuildShare())
		}
		if out.PerItemBuild() > out.PerItem() {
			t.Errorf("building took %v of %v per item",
				out.PerItemBuild(), out.PerItem())
		}
		if out.Overhead >= out.Wall {
			t.Errorf("a resolvable run had an overhead pass of %v against %v",
				out.Overhead, out.Wall)
		}
	} else if !strings.Contains(report, "could not be separated") &&
		!strings.Contains(report, "too coarse") {
		t.Errorf("an unresolvable run printed advice anyway:\n%s", report)
	}
}

// TestTwoPassesThatCouldNotBeSeparatedSayNothingElse, which the arm64 runner produced: the
// overhead pass came out longer than the work pass, so the share was 1.33. That is not a number
// to print, and this asserts the report says so instead. No timing here - the Outcome is built by
// hand, which is the only way to gate a case a fast machine will not produce.
func TestTwoPassesThatCouldNotBeSeparatedSayNothingElse(t *testing.T) {
	noisy := Outcome{
		Items: 60, Size: 128, Workers: 1, Selectors: 50,
		Tick:        41 * time.Nanosecond,
		Wall:        1514872 * time.Nanosecond,
		Overhead:    2009689 * time.Nanosecond,
		AllocsBuild: 415, AllocsFull: 431,
	}
	if noisy.Resolvable() {
		t.Error("an overhead pass longer than the work pass is resolvable")
	}
	if got := noisy.BuildShare(); got <= 1 {
		t.Fatalf("the share is %.2f, so this case does not reproduce the runner's", got)
	}
	report := noisy.String()
	if !strings.Contains(report, "could not be separated") {
		t.Errorf("the report does not say the passes could not be separated:\n%s", report)
	}
	for _, unwanted := range []string{"most of the time is construction", "noticeable slice",
		"in the noise here"} {
		if strings.Contains(report, unwanted) {
			t.Errorf("the report gave advice anyway (%q):\n%s", unwanted, report)
		}
	}
	// The counted share is unaffected, since it has no clock in it - so the report still has
	// a figure a reader can use.
	if got := noisy.AllocShare(); got < 0.9 || got > 1 {
		t.Errorf("the allocation share is %.3f", got)
	}
	if !strings.Contains(report, "allocations") {
		t.Errorf("the report dropped the counted share too:\n%s", report)
	}

	// And a clock too coarse for the queue is the other unresolvable case, with its own
	// sentence, so the two are not confused.
	coarse := Outcome{
		Items: 1, Workers: 1, Selectors: 1,
		Tick:     15 * time.Millisecond,
		Wall:     40 * time.Microsecond,
		Overhead: 20 * time.Microsecond,
	}
	if coarse.Resolvable() {
		t.Error("a queue below the clock tick is resolvable")
	}
	if !strings.Contains(coarse.String(), "too coarse") {
		t.Errorf("the coarse-clock case does not say so:\n%s", coarse.String())
	}
	if strings.Contains(coarse.String(), "could not be separated") {
		t.Errorf("the coarse-clock case blamed the passes:\n%s", coarse.String())
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

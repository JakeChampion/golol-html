package main

import (
	"strings"
	"testing"
)

// doc is a document with enough paragraphs to bail out well before the end at a small
// limit, and no token anywhere near it.
func doc(paragraphs int) []byte {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < paragraphs; i++ {
		b.WriteString("<p>paragraph with some text in it</p>")
	}
	b.WriteString("</body></html>")
	return []byte(b.String())
}

// TestBothModesBailOutAndReportIt, which is the part that is the same: the difference
// is only what the destination holds.
func TestBothModesBailOutAndReportIt(t *testing.T) {
	res := Compare(doc(120), 1024, 256, false)
	if len(res.Runs) != 3 {
		t.Fatalf("%d runs", len(res.Runs))
	}
	def, graceful, unlimited := res.Runs[0], res.Runs[1], res.Runs[2]
	for _, r := range []Run{def, graceful} {
		if !r.BailedOut {
			t.Errorf("%s did not bail out", r.Mode)
		}
		if r.Err == nil {
			t.Errorf("%s reported no error", r.Mode)
		}
	}
	if unlimited.BailedOut || unlimited.Err != nil {
		t.Errorf("the unlimited run failed: %v", unlimited.Err)
	}
	if unlimited.Delivered <= graceful.Delivered {
		t.Errorf("the unlimited run delivered %d and the graceful one %d",
			unlimited.Delivered, graceful.Delivered)
	}
}

// TestTheDefaultTruncatesAndGracefulDoesNot: the two shapes of failure, side by side.
func TestTheDefaultTruncatesAndGracefulDoesNot(t *testing.T) {
	d := doc(120)
	res := Compare(d, 1024, 256, false)
	def, graceful := res.Runs[0], res.Runs[1]

	if def.Delivered == 0 {
		t.Error("the default run delivered nothing; at this limit it should deliver a prefix")
	}
	if def.Delivered >= len(d) {
		t.Errorf("the default run delivered %d of %d bytes", def.Delivered, len(d))
	}
	// Graceful delivers at least as much: everything the rewriter had received.
	if graceful.Delivered < def.Delivered {
		t.Errorf("graceful delivered %d and the default %d; graceful should not deliver less",
			graceful.Delivered, def.Delivered)
	}
	// And the prefix the default delivers is well-formed, which is what makes it
	// dangerous: a client renders a short page without knowing.
	if !def.WellFormed {
		t.Errorf("the default run's prefix ends mid-tag: %q", def.Tail)
	}
}

// TestTheHandlerStopsWhereTheBailOutIs, so the counts say how much of the rewrite
// happened rather than only how many bytes came out.
func TestTheHandlerStopsWhereTheBailOutIs(t *testing.T) {
	d := doc(120)
	res := Compare(d, 1024, 256, false)
	def, unlimited := res.Runs[0], res.Runs[2]
	if def.Rewritten == 0 {
		t.Error("the default run rewrote nothing")
	}
	if def.Rewritten >= unlimited.Rewritten {
		t.Errorf("the default run rewrote %d of the %d the unlimited run did",
			def.Rewritten, unlimited.Rewritten)
	}
}

// TestALimitThatIsBigEnoughCompletes, and the floor search finds it.
func TestALimitThatIsBigEnoughCompletes(t *testing.T) {
	d := doc(120)
	res := Compare(d, 1<<20, 256, true)
	for _, r := range res.Runs {
		if r.BailedOut {
			t.Errorf("%s bailed out at a megabyte", r.Mode)
		}
	}
	if res.Floor == 0 {
		t.Fatal("the floor search found nothing")
	}
	// The floor is a real boundary: it completes and half of it does not.
	if r := run(d, Default, res.Floor, 256); r.BailedOut {
		t.Errorf("the floor %d bailed out", res.Floor)
	}
	if r := run(d, Default, res.Floor/2, 256); !r.BailedOut {
		t.Errorf("half the floor (%d) completed, so the floor is not tight", res.Floor/2)
	}
}

// TestTheFloorIsNotMonotonicInTheWriteSize, which is the reason this program searches
// for it rather than computing it. A bigger write does not always need a bigger limit:
// where the boundaries fall relative to the tokens decides, and two write sizes one byte
// apart can differ by a factor of six.
func TestTheFloorIsNotMonotonicInTheWriteSize(t *testing.T) {
	d := doc(400)
	sizes := []int{0, 64, 256, 512, 1024, 2048, 3072, 4095, 4096, 8192}
	floors := make(map[int]int, len(sizes))
	for _, c := range sizes {
		floors[c] = floor(d, c)
		if floors[c] == 0 {
			t.Fatalf("no floor found for a write size of %d", c)
		}
	}
	// Somewhere in that range a larger write size needs a smaller limit.
	fell := false
	for i := 1; i < len(sizes); i++ {
		if floors[sizes[i]] < floors[sizes[i-1]] {
			fell = true
			break
		}
	}
	if !fell {
		t.Errorf("the floor rose with every write size: %v", floors)
	}
	// And the body does not set it: ten times the document at one write size costs
	// about the same.
	small, large := floor(doc(40), 256), floor(doc(400), 256)
	if large > small*4 {
		t.Errorf("the floor went from %d to %d for ten times the body; it tracks the "+
			"writes, not the length", small, large)
	}
}

// TestTheReportNamesTheWriteSize, since a limit without one is not a number a caller can
// use.
func TestTheReportNamesTheWriteSize(t *testing.T) {
	s := Compare(doc(120), 1024, 256, true).String()
	for _, want := range []string{"limit 1024", "256-byte writes", "default:", "graceful:", "no limit:", "smallest limit"} {
		if !strings.Contains(s, want) {
			t.Errorf("the report is missing %q:\n%s", want, s)
		}
	}
	s = Compare(doc(10), 1<<20, 0, false).String()
	if !strings.Contains(s, "one Write") {
		t.Errorf("the report does not say it was one Write:\n%s", s)
	}
}

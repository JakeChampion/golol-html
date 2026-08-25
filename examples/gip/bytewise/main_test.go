package main

import (
	"strings"
	"testing"
	"time"
)

// runs is low on purpose: these tests assert allocation counts, which are exact after one
// rewrite, and a repeat count high enough for a stable timing would make the suite slow for
// nothing.
const runs = 2

// measureOrFail is Measure with the error handling every test would otherwise repeat.
func measureOrFail(t *testing.T, doc []byte, chunks ...int) Curve {
	t.Helper()
	c, err := Measure(doc, chunks, runs)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestEveryShapeGeneratesRoughlyTheSizeAsked, because the shapes are built by
// repetition and a rounding mistake would quietly measure a different document
// from the one named.
func TestEveryShapeGeneratesRoughlyTheSizeAsked(t *testing.T) {
	const size = 8 << 10
	for name, gen := range Shapes {
		doc := gen(size)
		if len(doc) < size/2 || len(doc) > size*2 {
			t.Errorf("%s: asked for %d bytes, got %d", name, size, len(doc))
		}
	}
}

// TestEveryShapeIsMeasurable - each one is a document the rewriter accepts,
// including the ones that end in the middle of a construct.
func TestEveryShapeIsMeasurable(t *testing.T) {
	for name, gen := range Shapes {
		t.Run(name, func(t *testing.T) {
			c := measureOrFail(t, []byte(gen(1<<10)), 1, 64)
			if len(c.Measurements) != 2 {
				t.Fatalf("measured %d write sizes, want 2", len(c.Measurements))
			}
			for _, m := range c.Measurements {
				if m.Allocs <= 0 || m.Duration <= 0 {
					t.Errorf("chunk %d measured %v allocations in %s, which cannot be right",
						m.Chunk, m.Allocs, m.Duration)
				}
			}
		})
	}
}

// TestTheWriteSizeDoesNotChangeTheAllocationCount is half of what the program reports: a
// rewrite allocates what the document costs, and the write size does not enter into it. The
// tolerance is a small constant rather than zero because a few allocations move with how the
// input happens to split; a per-write cost would show as thousands.
func TestTheWriteSizeDoesNotChangeTheAllocationCount(t *testing.T) {
	c := measureOrFail(t, []byte(Shapes["ordinary"](8<<10)), 1, 64, 4096, 0)

	whole := c.Measurements[len(c.Measurements)-1]
	for _, m := range c.Measurements {
		if diff := m.Allocs - whole.Allocs; diff > 8 || diff < -8 {
			t.Errorf("%s-byte writes cost %.0f allocations and one whole write %.0f: "+
				"the write path is allocating again", label(m.Chunk), m.Allocs, whole.Allocs)
		}
	}
}

// TestSmallWritesTakeLonger is the other half: what a small write costs is time, because
// every write is a crossing into C whatever it carries.
//
// The measured ratio on the machine this was written on is about eight; the assertion is
// two, so that a loaded runner does not fail it. A machine where the crossing is more
// expensive - which is every machine slower than this one - widens the gap rather than
// closing it.
func TestSmallWritesTakeLonger(t *testing.T) {
	c := measureOrFail(t, []byte(Shapes["ordinary"](8<<10)), 1, 0)

	one, whole := c.Measurements[0], c.Measurements[1]
	if one.Duration < whole.Duration*2 {
		t.Errorf("one-byte writes took %s and one whole write %s: the per-write cost of "+
			"crossing into C was expected to dominate", one.Duration, whole.Duration)
	}
}

// TestTheCostPerByteHoldsAsTheDocumentGrows - the program's -check mode, run
// small. This is the claim that replaces the quadratic one the docs used to
// make, so it is worth asserting here as well as in the library's own gate.
func TestTheCostPerByteHoldsAsTheDocumentGrows(t *testing.T) {
	for _, name := range []string{"ordinary", "unclosed-tag", "unclosed-comment"} {
		t.Run(name, func(t *testing.T) {
			doc := []byte(Shapes[name](4 << 10))
			small := measureOrFail(t, doc, 1)
			large := measureOrFail(t, grow(doc, 4), 1)

			linear, a, b := Linear(small, large, 1)
			if !linear {
				t.Errorf("%.4f allocations per byte at %d bytes and %.4f at %d: the "+
					"per-byte cost grew with the document", a, small.Bytes, b, large.Bytes)
			}
		})
	}
}

// TestLinearNoticesACostThatGrows, so that the check above is a test rather
// than a formality.
func TestLinearNoticesACostThatGrows(t *testing.T) {
	flat := Curve{Bytes: 100, Measurements: []Measurement{{Chunk: 1, Bytes: 100, Allocs: 200}}}
	grown := Curve{Bytes: 400, Measurements: []Measurement{{Chunk: 1, Bytes: 400, Allocs: 3200}}}

	if linear, _, _ := Linear(flat, grown, 1); linear {
		t.Error("eight allocations per byte against two was reported as linear")
	}
	if linear, _, _ := Linear(flat, flat, 1); !linear {
		t.Error("the same curve against itself was reported as not linear")
	}
}

// TestLinearNeedsTheWriteSizeItWasAskedFor: a missing write size is not a pass.
func TestLinearNeedsTheWriteSizeItWasAskedFor(t *testing.T) {
	c := Curve{Bytes: 100, Measurements: []Measurement{{Chunk: 64, Bytes: 100, Allocs: 200}}}
	if linear, _, _ := Linear(c, c, 1); linear {
		t.Error("a write size that was never measured was reported as linear")
	}
}

// TestTheBaselineIsTheLargestWrite, since every ratio printed is against it.
func TestTheBaselineIsTheLargestWrite(t *testing.T) {
	c := Curve{Measurements: []Measurement{
		{Chunk: 1, Duration: time.Second},
		{Chunk: 4096, Duration: time.Millisecond},
		{Chunk: 64, Duration: 10 * time.Millisecond},
	}}
	if got := c.Baseline().Chunk; got != 4096 {
		t.Errorf("baseline write size %d, want 4096", got)
	}

	// A write size of zero is one write of the whole document, so it is the largest
	// write there is however large the numbers beside it are.
	withWhole := Curve{Measurements: []Measurement{
		{Chunk: 4096, Duration: time.Millisecond},
		{Chunk: 0, Duration: time.Microsecond},
	}}
	if got := withWhole.Baseline().Chunk; got != 0 {
		t.Errorf("baseline write size %d, want the whole document", got)
	}
}

// TestTheTableSaysWhatItMeasured - one row per write size, and the baseline's
// own ratio is one.
func TestTheTableSaysWhatItMeasured(t *testing.T) {
	c := Curve{Bytes: 1000, Measurements: []Measurement{
		{Chunk: 1, Bytes: 1000, Allocs: 2000, Duration: 40 * time.Microsecond},
		{Chunk: 4096, Bytes: 1000, Allocs: 48, Duration: 4 * time.Microsecond},
	}}

	out := c.String()

	for _, want := range []string{"1000 bytes", "write size", "allocations", "10.0x", "1.0x"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not mention %q:\n%s", want, out)
		}
	}
	if lines := strings.Count(out, "\n"); lines != 4 {
		t.Errorf("the table has %d lines, want a title, a header and two rows:\n%s", lines, out)
	}
}

// TestPerByteDividesByTheDocument, which is the whole reason the figure is
// comparable between documents of different sizes.
func TestPerByteDividesByTheDocument(t *testing.T) {
	m := Measurement{Bytes: 500, Allocs: 1000, Duration: 5 * time.Microsecond}
	if got := m.PerByte(); got != 2 {
		t.Errorf("PerByte() = %v, want 2", got)
	}
	if got := m.NsPerByte(); got != 10 {
		t.Errorf("NsPerByte() = %v, want 10", got)
	}
}

// TestGrowKeepsTheShape: four times the document has to still be the same kind
// of document, or the comparison is between two different things.
func TestGrowKeepsTheShape(t *testing.T) {
	doc := []byte("<div a")
	got := grow(doc, 4)
	if len(got) != 24 {
		t.Fatalf("grew %d bytes to %d, want 24", len(doc), len(got))
	}
	if strings.Count(string(got), "<div a") != 4 {
		t.Errorf("grew to %q, which is not four of the original", got)
	}
}

// TestTheShapeNamesAreSortedAndComplete, because they are what the usage
// message offers.
func TestTheShapeNamesAreSortedAndComplete(t *testing.T) {
	names := strings.Split(shapeNames(), ", ")
	if len(names) != len(Shapes) {
		t.Fatalf("listed %d shapes, have %d", len(names), len(Shapes))
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("the shapes are not sorted: %q before %q", names[i-1], names[i])
		}
	}
	for _, name := range names {
		if _, ok := Shapes[name]; !ok {
			t.Errorf("listed a shape %q that does not exist", name)
		}
	}
}

// TestAWriteSizeOfZeroMeansTheWholeDocument, which is how the table's last row
// is measured.
func TestAWriteSizeOfZeroMeansTheWholeDocument(t *testing.T) {
	doc := []byte(Shapes["ordinary"](2 << 10))
	whole := measureOrFail(t, doc, 0)
	big := measureOrFail(t, doc, len(doc))

	// Within a couple of allocations rather than exactly: the counter is the runtime's
	// own, and under -race the runtime allocates a little on its own account between the
	// two reads. The two measurements are of the same rewrite, so anything larger than
	// that would mean they are not.
	if diff := whole.Measurements[0].Allocs - big.Measurements[0].Allocs; diff > 4 || diff < -4 {
		t.Errorf("a write size of zero cost %v allocations and one whole write %v: "+
			"they are meant to be the same measurement",
			whole.Measurements[0].Allocs, big.Measurements[0].Allocs)
	}
}

// TestAGeneratedDocumentNeedsANameThatExists, so a typo is an error rather than a
// measurement of nothing.
func TestAGeneratedDocumentNeedsANameThatExists(t *testing.T) {
	if _, err := document("unclosed-taj", 1<<10); err == nil {
		t.Error("a shape that does not exist was accepted")
	}
	if _, err := document("unclosed-tag", 0); err == nil {
		t.Error("a document of no bytes was accepted")
	}
	doc, err := document("unclosed-tag", 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(doc), "<div ") {
		t.Errorf("generated %q, which is not the shape asked for", doc[:min(16, len(doc))])
	}
}

// TestTheVerdictSaysWhichWayItWent, since it is the line a reader takes away.
func TestTheVerdictSaysWhichWayItWent(t *testing.T) {
	if got := verdict(true); got != "linear" {
		t.Errorf("verdict(true) = %q", got)
	}
	if got := verdict(false); !strings.Contains(got, "NOT linear") {
		t.Errorf("verdict(false) = %q, which does not say the check failed", got)
	}
}

// TestARewriteAtEveryWriteSizeProducesTheSameCost is the sanity check under the whole
// program: measuring must not change what the rewrite does, and a document written in
// pieces has to cost the same as one written whole up to the per-write price.
func TestARewriteAtEveryWriteSizeProducesTheSameCost(t *testing.T) {
	doc := []byte(Shapes["ordinary"](1 << 10))
	for _, chunk := range []int{1, 7, 64, 0} {
		if err := rewrite(doc, chunk); err != nil {
			t.Errorf("chunk %d: %v", chunk, err)
		}
	}
	// A write size larger than the document is one write, not a panic.
	if err := rewrite(doc, len(doc)*4); err != nil {
		t.Errorf("a write size larger than the document: %v", err)
	}
	if err := rewrite(nil, 1); err != nil {
		t.Errorf("an empty document: %v", err)
	}
}

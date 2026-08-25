package lolhtml_test

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// byteCostShapes are documents that make the rewriter hold something across
// writes, which is the case the cost of a small write was supposed to depend on.
var byteCostShapes = []struct {
	name string
	gen  func(int) string
}{
	{"ordinary markup", func(n int) string { return strings.Repeat(`<p class="a">text</p>`, n/21+1) }},
	{"an unclosed tag", func(n int) string { return "<div " + strings.Repeat("a", n) }},
	{"an unclosed tag with many attributes", func(n int) string { return "<div " + strings.Repeat(`a="b" `, n/6) }},
	{"an unclosed comment", func(n int) string { return "<!--" + strings.Repeat("x", n) }},
	{"an unclosed quoted value", func(n int) string { return `<div a="` + strings.Repeat("x", n) }},
	{"an unclosed raw-text element", func(n int) string { return "<script>" + strings.Repeat("x", n) }},
	{"one enormous text node", func(n int) string { return "<p>" + strings.Repeat("x", n) + "</p>" }},
}

// allocsForWrites reports the allocations one whole rewrite of in costs when the
// document is written chunk bytes at a time. A chunk of zero means one write.
//
// The document is a []byte and the writes are slices of it, deliberately. An
// earlier version of this measurement wrote []byte(in[i:end]) from a string and
// so paid one allocation per write of its own, which is the same figure the
// binding was being measured for - it read as one allocation per byte from the
// library and half of it was the harness. Allocation measurements have to own
// nothing.
func allocsForWrites(t *testing.T, in []byte, chunk int) float64 {
	t.Helper()

	if chunk <= 0 || chunk > len(in) {
		chunk = len(in)
	}
	rewrite := func() {
		w, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(in); i += chunk {
			if _, err := w.Write(in[i:min(i+chunk, len(in))]); err != nil {
				t.Fatal(err)
			}
		}
		// Close can legitimately fail: most of these documents end inside a
		// construct and strict mode reports that. The cost is the subject here,
		// not the error.
		_ = w.Close()
	}
	rewrite()

	return testing.AllocsPerRun(allocRuns, rewrite)
}

// TestTheWriteSizeDoesNotChangeTheAllocationCount: a rewrite costs what the
// document costs, and feeding it in smaller pieces does not add to it.
//
// This is a stronger statement than it looks. It means an allocation count
// measured with one big write is the count a caller streaming from a socket will
// see, so every figure in alloc_test.go and in the README describes both. Until
// the write path stopped allocating it was not true: one 16-byte allocation per
// Write made a byte-at-a-time rewrite of 64 KB cost 68,684 allocations against
// 3,143 for the same document written whole.
//
// The tolerance is a small constant rather than zero, and it is not slack for a
// per-write cost to hide in: over these shapes the measured spread between one
// whole write and byte-at-a-time is nought to two allocations, while the write
// counts differ by thousands. Anything proportional to the write count would
// exceed the tolerance by three orders of magnitude.
func TestTheWriteSizeDoesNotChangeTheAllocationCount(t *testing.T) {
	requireRealAllocationCounts(t)

	// A per-write allocation would show as thousands: an 8 KB document written a
	// byte at a time is 8192 writes.
	const size = 8 << 10
	const tolerance = 8

	for _, shape := range byteCostShapes {
		t.Run(shape.name, func(t *testing.T) {
			in := []byte(shape.gen(size))
			whole := allocsForWrites(t, in, 0)
			for _, chunk := range []int{1, 3, 64, 1024} {
				got := allocsForWrites(t, in, chunk)
				writes := (len(in) + chunk - 1) / chunk
				if diff := got - whole; diff > tolerance || diff < -tolerance {
					t.Errorf("%d-byte writes cost %.0f allocations and one whole write "+
						"%.0f, a difference of %.0f over %d writes: the write path is "+
						"allocating again", chunk, got, whole, diff, writes)
				}
			}
		})
	}
}

// TestWritingAByteAtATimeCostsLinearly, including while the rewriter is
// buffering an unclosed tag.
//
// This contradicts what this repository documented for several releases: that
// byte-at-a-time writes are quadratic while a tag is pending, because each write
// rescans the pending buffer. They are not, and nothing rescans. The cost per
// byte is flat, so quadrupling the document quadruples the work.
//
// The claim mattered, which is why it is gated now rather than merely corrected:
// it was the stated reason the fuzz harness caps input size, and the stated
// reason to avoid small writes. Small writes are still worth avoiding, for the
// per-write cost of crossing into C, which is a constant a caller can pay
// knowingly rather than a curve they have to design around.
func TestWritingAByteAtATimeCostsLinearly(t *testing.T) {
	requireRealAllocationCounts(t)

	// Four-fold steps: a quadratic cost per byte would grow four-fold with each,
	// which no tolerance would hide.
	sizes := []int{1 << 10, 4 << 10, 16 << 10}

	for _, shape := range byteCostShapes {
		t.Run(shape.name, func(t *testing.T) {
			var first float64
			for _, size := range sizes {
				in := []byte(shape.gen(size))
				per := allocsForWrites(t, in, 1) / float64(len(in))
				if first == 0 {
					first = per
				}
				if per > first*1.5 {
					t.Errorf("%d bytes cost %.4f allocations per byte, against %.4f "+
						"for %d: the per-byte cost grew with the document",
						len(in), per, first, sizes[0])
				}
			}
		})
	}
}

// TestAPendingTagIsNotTheExpensiveCase - the document that was supposed to be
// pathological is the cheap one.
//
// An unclosed tag produces no tokens to hand back, so there is almost nothing to
// pay for however it is written; ordinary markup costs an order of magnitude more
// per byte, because every element is a token and every token that matches is a
// crossing into Go. That inverts the old story, in which the pending tag was the
// case to fear.
func TestAPendingTagIsNotTheExpensiveCase(t *testing.T) {
	requireRealAllocationCounts(t)

	const size = 16 << 10
	ordinary := []byte(strings.Repeat(`<p class="a">text</p>`, size/21))
	pending := []byte("<div " + strings.Repeat("a", size))

	for _, chunk := range []int{1, 4 << 10} {
		a := allocsForWrites(t, pending, chunk) / float64(len(pending))
		b := allocsForWrites(t, ordinary, chunk) / float64(len(ordinary))
		if a > b {
			t.Errorf("at %d-byte writes an unclosed tag cost %.4f allocations per byte "+
				"and ordinary markup %.4f: the buffered tag was expected to be the "+
				"cheaper of the two, since it produces nothing to hand back", chunk, a, b)
		}
	}
}

// TestAWriteOfNothingCostsNothing, which is the boundary of the claim above.
func TestAWriteOfNothingCostsNothing(t *testing.T) {
	requireRealAllocationCounts(t)

	in := []byte("<p>text</p>")
	baseline := allocsForWrites(t, in, 0)

	withEmpties := func() {
		w, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			if _, err := w.Write(nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	withEmpties()

	if got := testing.AllocsPerRun(allocRuns, withEmpties); got != baseline {
		t.Errorf("a hundred empty writes cost %.0f allocations against %.0f without them",
			got, baseline)
	}
}

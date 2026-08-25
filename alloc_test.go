package lolhtml_test

// Allocation complexity, as opposed to allocation count.
//
// The benchmarks measure six fixed shapes. Nothing compares them across document
// sizes, so nothing notices the regression that actually matters: a path that
// goes from a constant number of allocations to one proportional to the
// document, or a per-match cost that quietly doubles. Either would leave every
// existing test green, and the benchmark table would move in a way that reads as
// noise unless someone happens to look.
//
// What the library promises, in the README, is that cost tracks handler
// invocations rather than document size. That is a checkable shape:
//
//	allocations = base + k * matches
//
// with base independent of the document's length. This file pins the slope k
// exactly and requires base to be flat, while allowing base itself to drift -
// the fixed overhead of building a rewriter is a toolchain detail, the slope is
// a design property.

import (
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// allocRuns is low deliberately: these are counted allocations, not timings, so
// repetition buys nothing beyond smoothing the first-run warmup.
const allocRuns = 8

// requireRealAllocationCounts skips a test whose subject is how many times the
// library allocates, when the build is one where that number is not the number
// that ships.
//
// AddressSanitizer replaces the allocator, and the replacement allocates on its
// own account: a rewrite that allocates once per match on a normal build
// allocates four times per match under -asan, and setting an attribute goes
// from 2 per match to 19.84. The slope this file pins is a design property of
// the binding, and under -asan it is a property of the sanitizer instead.
//
// The skip is not a way to keep the suite quiet. These tests still run in the
// three legs that count - plain, -race, and every platform in the matrix - and
// -asan is there to find memory errors across the cgo boundary, which it still
// does with these skipped.
func requireRealAllocationCounts(t *testing.T) {
	t.Helper()
	if asanEnabled {
		t.Skip("allocation counts under -asan are the sanitizer's, not the binding's")
	}
}

// allocsFor reports the average allocations for one whole rewrite of in.
func allocsFor(t *testing.T, in string, opts ...lolhtml.Option) int {
	t.Helper()

	// One warmup outside the measurement, so first-call initialisation is not
	// attributed to the run.
	rewrite := func() {
		w, err := lolhtml.NewWriter(io.Discard, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(in)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	rewrite()

	return int(testing.AllocsPerRun(allocRuns, rewrite))
}

// linkDoc has n matches for a[href] and grows with n.
func linkDoc(n int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<a href="/%d">link %d</a>`, i, i)
	}
	b.WriteString("</body></html>")
	return b.String()
}

// paraDoc grows with n and has no matches for a[href] at all.
func paraDoc(n int) string {
	return strings.Repeat("<p>text</p>", n)
}

// TestPassthroughAllocationsDoNotGrowWithTheDocument is the floor: with no
// handlers the sink hands the destination a slice over lol-html's own buffer
// rather than copying it, so the cost of a rewrite is independent of how much
// went through it. Copying every output chunk was measurably the dominant
// allocation cost before that change, and this is what would notice it coming
// back.
func TestPassthroughAllocationsDoNotGrowWithTheDocument(t *testing.T) {
	requireRealAllocationCounts(t)

	small := allocsFor(t, linkDoc(1))
	large := allocsFor(t, linkDoc(400))

	if small != large {
		t.Errorf("passthrough allocations grew with the document: %d for %d bytes, "+
			"%d for %d bytes", small, len(linkDoc(1)), large, len(linkDoc(400)))
	}
}

// TestUnmatchedContentAllocationsDoNotGrowWithTheDocument: a registered handler
// that never fires must not cost anything per byte either.
func TestUnmatchedContentAllocationsDoNotGrowWithTheDocument(t *testing.T) {
	requireRealAllocationCounts(t)

	opt := lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil })

	small := allocsFor(t, paraDoc(1), opt)
	large := allocsFor(t, paraDoc(400), opt)

	if small != large {
		t.Errorf("a non-matching handler allocated per byte: %d for %d bytes, "+
			"%d for %d bytes", small, len(paraDoc(1)), large, len(paraDoc(400)))
	}
}

// TestAllocationsPerMatchAreConstant pins the slope, which is the part that is
// a design property. The absolute numbers move with the toolchain and are not
// asserted; the cost of one more match is.
//
// The model the numbers describe: a unit wrapper costs one allocation, every
// string that crosses the boundary costs one more, and a source location costs
// none because it is two integers. A regression in any of those would leave
// every output identical.
func TestAllocationsPerMatchAreConstant(t *testing.T) {
	requireRealAllocationCounts(t)

	tests := []struct {
		name   string
		opt    lolhtml.Option
		perHit int
	}{{
		// The wrapper alone.
		name:   "element handler doing nothing",
		opt:    lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil }),
		perHit: 1,
	}, {
		// A source location is two ints and crosses no strings.
		name: "element handler reading a source location",
		opt: lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			_ = e.SourceLocation()
			return nil
		}),
		perHit: 1,
	}, {
		name: "element handler reading a tag name",
		opt: lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			_ = e.TagName()
			return nil
		}),
		perHit: 2,
	}, {
		name: "element handler reading an attribute",
		opt: lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("href")
			return nil
		}),
		perHit: 2,
	}, {
		// Writing costs the same as reading: both are one cgo call carrying one
		// string.
		name: "element handler setting an attribute",
		opt: lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}),
		perHit: 2,
	}, {
		// Two strings, two allocations. Nothing is cached, so reading the same
		// attribute twice costs twice.
		name: "element handler reading an attribute twice",
		opt: lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("href")
			_, _ = e.Attribute("href")
			return nil
		}),
		perHit: 3,
	}, {
		// A text node arrives as a content chunk plus its empty boundary chunk,
		// so two wrappers, and neither reads a string.
		name:   "text handler doing nothing",
		opt:    lolhtml.OnText("a", func(*lolhtml.TextChunk) error { return nil }),
		perHit: 2,
	}, {
		name: "text handler reading the text",
		opt: lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
			_ = tc.Text()
			return nil
		}),
		perHit: 3,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const lo, hi = 100, 400

			a := allocsFor(t, linkDoc(lo), tt.opt)
			b := allocsFor(t, linkDoc(hi), tt.opt)

			// The slope is compared within a tolerance rather than for
			// equality. An allocation count is reproducible for a given input
			// and toolchain, but the fixed part of it is not identical at every
			// document size: on darwin/amd64 under Rosetta this measured 222
			// allocations at 100 matches and 823 at 400, a slope of 2.003
			// rather than 2, because one allocation of setup appeared somewhere
			// between the two sizes.
			//
			// One stray allocation across a 300-match span moves the slope by
			// 0.003. The regression this gate exists to catch - a per-match cost
			// that doubles, or a per-match cost where there should be none -
			// moves it by at least 1. A tolerance of 0.05 is more than fifteen
			// times the noise and twenty times below the signal, so it
			// separates them without being a judgement call.
			const slopeTolerance = 0.05

			slope := float64(b-a) / float64(hi-lo)
			if math.Abs(slope-float64(tt.perHit)) > slopeTolerance {
				t.Errorf("%.3f allocations per match, want %d "+
					"(%d allocations at %d matches, %d at %d)",
					slope, tt.perHit, a, lo, b, hi)
			}

			// And the fixed part does not grow with the document. linkDoc(hi)
			// is four times the bytes of linkDoc(lo), so a base that tracked
			// length would differ here by hundreds; the tolerance is for the
			// same stray allocation as above and nothing larger.
			const baseTolerance = 8

			baseLo, baseHi := a-tt.perHit*lo, b-tt.perHit*hi
			if diff := baseLo - baseHi; diff > baseTolerance || diff < -baseTolerance {
				t.Errorf("the fixed cost grew with the document: %d extrapolated from "+
					"%d matches, %d from %d", baseLo, lo, baseHi, hi)
			}
		})
	}
}

// TestRegisteringSelectorsCostsLinearly, and a repeated selector costs less than
// a distinct one because the parse is cached.
//
// The cache is deliberate - config.register parses each distinct selector once
// and reuses it - and nothing tested it, so removing it would have broken nobody's
// build. The saving is one allocation per duplicate registration, which is small
// per selector and not small for a tool that registers one handler per rule in a
// stylesheet.
//
// The assertions are a band rather than an exact number: build allocations
// include slice growth, so the per-selector figure is not an integer. What is
// asserted is the shape - linear, not quadratic - and that the cache saves
// something that grows with the number of duplicates.
func TestRegisteringSelectorsCostsLinearly(t *testing.T) {
	requireRealAllocationCounts(t)

	build := func(opts []lolhtml.Option) int {
		f := func() {
			w, err := lolhtml.NewWriter(io.Discard, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
		f()
		return int(testing.AllocsPerRun(allocRuns, f))
	}

	distinct := func(n int) []lolhtml.Option {
		out := make([]lolhtml.Option, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, lolhtml.OnElement(fmt.Sprintf(".c%d", i),
				func(*lolhtml.Element) error { return nil }))
		}
		return out
	}
	same := func(n int) []lolhtml.Option {
		out := make([]lolhtml.Option, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, lolhtml.OnElement("div",
				func(*lolhtml.Element) error { return nil }))
		}
		return out
	}

	const lo, hi = 100, 400

	t.Run("linear in the number of selectors", func(t *testing.T) {
		a, b := build(distinct(lo)), build(distinct(hi))

		// Quadratic growth would be about sixteen times, not four.
		if b > 6*a {
			t.Errorf("%d selectors cost %d allocations and %d cost %d, which is worse "+
				"than linear", lo, a, hi, b)
		}
		perSelector := float64(b-a) / float64(hi-lo)
		if perSelector < 4 || perSelector > 8 {
			t.Errorf("%.2f allocations per selector; the shape has changed enough to "+
				"be worth looking at (%d at %d, %d at %d)", perSelector, a, lo, b, hi)
		}
	})

	t.Run("a repeated selector is parsed once", func(t *testing.T) {
		for _, n := range []int{lo, hi} {
			d, s := build(distinct(n)), build(same(n))
			if s >= d {
				t.Errorf("n=%d: %d allocations for one selector registered %d times, "+
					"%d for %d distinct ones; the parse cache is not saving anything",
					n, s, n, d, n)
			}
		}

		// And the saving grows with the number of duplicates, which is what
		// makes it a cache rather than a constant.
		savedLo := build(distinct(lo)) - build(same(lo))
		savedHi := build(distinct(hi)) - build(same(hi))
		if savedHi <= savedLo {
			t.Errorf("the cache saved %d allocations at %d selectors and %d at %d; "+
				"it should save more when there is more to save", savedLo, lo, savedHi, hi)
		}
	})
}

// TestAttributeIterationCostsPerAttribute: AttributeList and Attributes
// materialise every name and value, so their cost is per attribute rather than
// per element - four allocations each, measured. Reaching for one of them to read
// a single attribute is the easy mistake, and this is the number that says how
// much it costs.
func TestAttributeIterationCostsPerAttribute(t *testing.T) {
	requireRealAllocationCounts(t)

	// Two documents with the same number of matches and a different number of
	// attributes on each match.
	doc := func(attrs int) string {
		var b strings.Builder
		b.WriteString("<html><body>")
		for i := 0; i < 100; i++ {
			b.WriteString(`<a href="/x"`)
			for j := 1; j < attrs; j++ {
				fmt.Fprintf(&b, ` data-%d="v"`, j)
			}
			b.WriteString(">t</a>")
		}
		b.WriteString("</body></html>")
		return b.String()
	}

	opt := lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		_ = e.AttributeList()
		return nil
	})

	one := allocsFor(t, doc(1), opt)
	three := allocsFor(t, doc(3), opt)

	// Two more attributes on each of 100 elements. Four allocations each,
	// measured rather than derived: whatever the breakdown, the number is the
	// thing to notice changing.
	perAttr := float64(three-one) / 200
	if perAttr != 4 {
		t.Errorf("%.3f allocations per extra attribute, want 4; "+
			"%d for one attribute, %d for three", perAttr, one, three)
	}

	// And reading one attribute directly is cheaper than listing them all.
	direct := allocsFor(t, doc(3), lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("href")
		return nil
	}))
	if direct >= three {
		t.Errorf("reading one attribute (%d) is not cheaper than listing three (%d)",
			direct, three)
	}
}

// TestSetAttributeCostsNoMoreThanReadingOne records a property that is easy to
// lose: writing an attribute goes through the same single cgo call as reading
// one, so a rewrite that edits every match costs the same as one that only
// inspects them. A regression here would not change any output.
func TestSetAttributeCostsNoMoreThanReadingOne(t *testing.T) {
	requireRealAllocationCounts(t)

	doc := linkDoc(200)

	read := allocsFor(t, doc, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("href")
		return nil
	}))
	write := allocsFor(t, doc, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	}))

	if write != read {
		t.Errorf("setting an attribute allocates %d, reading one allocates %d", write, read)
	}
}

// TestSelectorRegistrationCost pins the numbers the Cost section gives for
// building a rewriter, which nothing measured before: the section said "about
// five allocations per distinct selector, one fewer for a repeat" and the
// measurement is nearer seven and one and a half.
//
// A range rather than a value, because these move with the toolchain and with
// how the slices behind them happen to grow. What is worth asserting is the
// shape: single-digit per selector, and a repeat cheaper than a distinct one.
func TestSelectorRegistrationCost(t *testing.T) {
	requireRealAllocationCounts(t)

	// Distinct selectors, one per handler.
	distinct := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	// The options are built outside the measured function, so the count is
	// NewWriter's work and not the caller's slice.
	build := func(n int, repeat bool) []lolhtml.Option {
		opts := make([]lolhtml.Option, 0, n)
		for i := range n {
			sel := distinct[i%len(distinct)]
			if repeat {
				sel = "a"
			}
			opts = append(opts, lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil }))
		}
		return opts
	}
	measure := func(opts []lolhtml.Option) float64 {
		return testing.AllocsPerRun(100, func() {
			w, err := lolhtml.NewWriter(io.Discard, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	base := measure(nil)
	if base < 5 || base > 40 {
		t.Errorf("a rewriter with no handlers cost %.0f allocations; the "+
			"documented figure is 13 and this range is generous", base)
	}

	const n = 8
	perDistinct := (measure(build(n, false)) - base) / n
	perRepeat := (measure(build(n, true)) - base) / n

	// The documented figure is about seven. One to fifteen is wide enough for a
	// toolchain and narrow enough to notice a change in kind.
	if perDistinct < 1 || perDistinct > 15 {
		t.Errorf("%.1f allocations per distinct selector; the documentation says "+
			"about seven", perDistinct)
	}
	// The saving from reusing a parsed selector is small, so this is a
	// comparison rather than a number.
	if perRepeat >= perDistinct {
		t.Errorf("a repeated selector cost %.1f against %.1f for a distinct one; "+
			"the documentation claims each distinct selector is parsed once and "+
			"reused, which should make a repeat cheaper", perRepeat, perDistinct)
	}
}

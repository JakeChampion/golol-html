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
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// allocRuns is low deliberately: these are counted allocations, not timings, so
// repetition buys nothing beyond smoothing the first-run warmup.
const allocRuns = 8

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

			slope := float64(b-a) / float64(hi-lo)
			if slope != float64(tt.perHit) {
				t.Errorf("%.3f allocations per match, want exactly %d "+
					"(%d allocations at %d matches, %d at %d)",
					slope, tt.perHit, a, lo, b, hi)
			}

			// And the fixed part is genuinely fixed: extrapolating back to zero
			// matches from either measurement must agree.
			if baseLo, baseHi := a-tt.perHit*lo, b-tt.perHit*hi; baseLo != baseHi {
				t.Errorf("the fixed cost is not fixed: %d extrapolated from %d matches, "+
					"%d from %d", baseLo, lo, baseHi, hi)
			}
		})
	}
}

// TestAttributeIterationCostsPerAttribute: AttributeList and Attributes
// materialise every name and value, so their cost is per attribute rather than
// per element - four allocations each, measured. Reaching for one of them to read
// a single attribute is the easy mistake, and this is the number that says how
// much it costs.
func TestAttributeIterationCostsPerAttribute(t *testing.T) {
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

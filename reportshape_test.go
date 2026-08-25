package lolhtml_test

// What a report-only rewrite costs, and how its shape changes that. A tool that
// reports on a document rather than editing it has a choice nothing in the library
// makes for it: match everything and ask each element about the attributes it might
// have, or name the elements that can have them. The two shapes have different costs
// in both places a rewrite pays - once at NewWriter and once per element - and the
// answer is not obvious from either.
//
// The numbers here are comparisons rather than figures, because the figures are the
// machine's. What is being pinned is the shape: registrations cost per Option and not
// per clause, so a selector list is free where a second OnElement is not; and a wide
// selector pays the per-element cost for every element in the document.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// urlSelectors are the elements that can name a URL, one clause each.
var urlSelectors = []string{
	"img[src]", "script[src]", "link[href]", "a[href]", "video[poster]",
	"source[src]", "iframe[src]", "object[data]", "form[action]", "embed[src]",
	"audio[src]", "track[src]",
}

// urlAttributes are the names a reporter asks about.
var urlAttributes = []string{"src", "href", "data", "poster", "action", "srcset"}

// reportPage is a document with a realistic ratio: most elements name nothing.
func reportPage(n int) string {
	var b strings.Builder
	b.WriteString(`<html><head><link rel="stylesheet" href="/s.css"></head><body>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<div class="c%d"><p>text %d</p><a href="/p%d">link</a><img src="/i%d.png" alt="x"></div>`, i, i, i, i)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func countingHandler(hits *int) func(*lolhtml.Element) error {
	return func(e *lolhtml.Element) error {
		for _, name := range urlAttributes {
			if v, ok := e.Attribute(name); ok && v != "" {
				*hits++
				break
			}
		}
		return nil
	}
}

// TestASelectorListCostsWhatOneSelectorCosts, so the twelve elements a reporter cares
// about can be named in one Option for the price of one.
func TestASelectorListCostsWhatOneSelectorCosts(t *testing.T) {
	requireRealAllocationCounts(t)

	const empty = "<p>x</p>"
	var hits int

	one := allocsFor(t, empty, lolhtml.OnElement(urlSelectors[0], countingHandler(&hits)))
	for _, n := range []int{2, 4, 8, 12} {
		list := strings.Join(urlSelectors[:n], ",")
		got := allocsFor(t, empty, lolhtml.OnElement(list, countingHandler(&hits)))
		if got != one {
			t.Errorf("a list of %d clauses costs %d allocations, one clause costs %d: "+
				"a list was flat when this was written", n, got, one)
		}
	}

	// Separate registrations are not flat: each one is its own handler.
	for _, n := range []int{2, 4, 8, 12} {
		opts := make([]lolhtml.Option, 0, n)
		for _, sel := range urlSelectors[:n] {
			opts = append(opts, lolhtml.OnElement(sel, countingHandler(&hits)))
		}
		got := allocsFor(t, empty, opts...)
		if got <= one {
			t.Errorf("%d separate registrations cost %d allocations, one costs %d: "+
				"registrations were per Option when this was written", n, got, one)
		}
		// And the growth is per registration rather than a one-off.
		if n == 12 && got < one+4*(n-1) {
			t.Errorf("12 registrations cost %d against one at %d, which is less growth "+
				"than the measurement this pins (about 8 per registration)", got, one)
		}
	}
}

// TestAWideSelectorPaysForEveryElement, which is the other half of the choice: the
// handler runs on elements that could not have named anything.
func TestAWideSelectorPaysForEveryElement(t *testing.T) {
	requireRealAllocationCounts(t)

	doc := reportPage(100)
	var wideHits, listHits int

	wide := allocsFor(t, doc, lolhtml.OnElement("*", countingHandler(&wideHits)))
	list := allocsFor(t, doc, lolhtml.OnElement(strings.Join(urlSelectors, ","), countingHandler(&listHits)))

	if wideHits != listHits {
		t.Fatalf("the two shapes found %d and %d URLs; they have to agree for the "+
			"comparison to mean anything", wideHits, listHits)
	}
	if wide <= list {
		t.Errorf("the wide selector cost %d allocations and the list cost %d, want the "+
			"wide one to cost more: it fires for every element", wide, list)
	}

	// The gap is per element rather than a constant, so it grows with the document.
	small := reportPage(10)
	var a, b int
	wideSmall := allocsFor(t, small, lolhtml.OnElement("*", countingHandler(&a)))
	listSmall := allocsFor(t, small, lolhtml.OnElement(strings.Join(urlSelectors, ","), countingHandler(&b)))
	if (wide - list) <= (wideSmall - listSmall) {
		t.Errorf("the gap is %d on 100 blocks and %d on 10, want it to grow with the document",
			wide-list, wideSmall-listSmall)
	}
}

// TestNamedLookupsOnAMissCostNothing, which is why the wide shape is only as
// expensive as the elements it visits: asking for an attribute that is not there does
// not allocate a string.
func TestNamedLookupsOnAMissCostNothing(t *testing.T) {
	requireRealAllocationCounts(t)

	doc := strings.Repeat(`<p>text</p>`, 100)
	one := allocsFor(t, doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("src")
		return nil
	}))
	six := allocsFor(t, doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		for _, name := range urlAttributes {
			_, _ = e.Attribute(name)
		}
		return nil
	}))
	if six != one {
		t.Errorf("six missing lookups per element cost %d and one costs %d, want the same: "+
			"a miss returned no string when this was written", six, one)
	}
	// A hit does cost, which is the contrast that makes the above meaningful.
	hitDoc := strings.Repeat(`<p title="x">text</p>`, 100)
	miss := allocsFor(t, hitDoc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("src")
		return nil
	}))
	hit := allocsFor(t, hitDoc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("title")
		return nil
	}))
	if hit <= miss {
		t.Errorf("reading a present attribute cost %d and a missing one %d, want the "+
			"present one to cost more", hit, miss)
	}
}

// TestAReporterWritesNothing: with io.Discard as the destination and no edits, the
// cost is matching and reading, and the passthrough itself does not grow it.
func TestAReporterWritesNothing(t *testing.T) {
	requireRealAllocationCounts(t)

	var hits int
	sel := strings.Join(urlSelectors, ",")
	small := allocsFor(t, reportPage(10), lolhtml.OnElement(sel, countingHandler(&hits)))
	large := allocsFor(t, reportPage(100), lolhtml.OnElement(sel, countingHandler(&hits)))

	// Ten times the document, ten times the matches: the cost is per match, so the
	// ratio should be close to ten rather than to the byte ratio.
	if large < 5*small || large > 20*small {
		t.Errorf("10 blocks cost %d and 100 cost %d, want roughly ten times: the cost "+
			"is per match", small, large)
	}
}

// TestClausesAreFreeAndHandlersAreNot, on a document where nothing matches at all, so
// what is measured is registration and matching rather than any handler running.
func TestClausesAreFreeAndHandlersAreNot(t *testing.T) {
	requireRealAllocationCounts(t)

	doc := strings.Repeat("<p>text</p>", 500)
	nop := func(*lolhtml.Element) error { return nil }

	none := allocsFor(t, doc)
	one := allocsFor(t, doc, lolhtml.OnElement(urlSelectors[0], nop))
	list := allocsFor(t, doc, lolhtml.OnElement(strings.Join(urlSelectors, ","), nop))

	separate := make([]lolhtml.Option, 0, len(urlSelectors))
	for _, sel := range urlSelectors {
		separate = append(separate, lolhtml.OnElement(sel, nop))
	}
	twelve := allocsFor(t, doc, separate...)

	if list != one {
		t.Errorf("a 12-clause list costs %d allocations and one clause costs %d, want the "+
			"same: clauses are parsed on the C side and the Go side allocates per handler",
			list, one)
	}
	if twelve <= one+4*(len(urlSelectors)-1) {
		t.Errorf("12 handlers cost %d and one costs %d, want the difference to scale with "+
			"the handler count", twelve, one)
	}
	if one <= none {
		t.Errorf("one handler costs %d and no handlers cost %d, want a handler to cost "+
			"something to register", one, none)
	}
	// Nothing here is per element: the same handlers over a document ten times the
	// size cost the same, because nothing matches in either.
	small := strings.Repeat("<p>text</p>", 50)
	if got := allocsFor(t, small, lolhtml.OnElement(strings.Join(urlSelectors, ","), nop)); got != list {
		t.Errorf("the list costs %d on 500 elements and %d on 50, want the same: matching "+
			"a selector that does not match allocates nothing", list, got)
	}
}

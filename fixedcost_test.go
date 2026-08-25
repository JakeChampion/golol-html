package lolhtml_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// selectorSet returns n handlers on distinct selectors, only the first of which matches
// anything in the documents below.
func selectorSet(n int) []lolhtml.Option {
	opts := make([]lolhtml.Option, 0, n)
	opts = append(opts, lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil }))
	for i := 1; i < n; i++ {
		opts = append(opts, lolhtml.OnElement(fmt.Sprintf(".c%d", i),
			func(*lolhtml.Element) error { return nil }))
	}
	return opts
}

// buildAndRun is one whole rewrite: build a Writer, feed it doc, close it. A Writer cannot be
// reused, so this is what a queue does per item.
func buildAndRun(t *testing.T, doc string, opts []lolhtml.Option) {
	t.Helper()

	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if doc != "" {
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestTheFixedCostOfARewriteIsTheSelectorList, and is paid per document.
//
// The registration cost is documented per NewWriter, in allocations. What is not said is what
// that means for a workload that rewrites many small documents: a Writer cannot be reused -
// Close ends it, and there is no reset - so the parse of every selector is repeated per item,
// and there is no way to share a parsed selector between Writers.
//
// Measured here as the allocations for a whole rewrite, which is the figure a queue pays:
//
//	selectors   empty document   1 KB   16 KB
//	        1               23      26      26
//	       10              105     106     106
//	       50              406     408     408
//
// Read down a column: the cost is the rule set. Read across a row: the document adds almost
// nothing when nothing matches, which is the case a big rule set is mostly in.
func TestTheFixedCostOfARewriteIsTheSelectorList(t *testing.T) {
	requireRealAllocationCounts(t)

	docs := []struct {
		name string
		doc  string
	}{
		{"nothing", ""},
		{"1 KB", strings.Repeat(`<b>x</b>`, 128)},
		{"16 KB", strings.Repeat(`<b>x</b>`, 2048)},
	}

	perSelectors := map[int]float64{}
	for _, n := range []int{1, 10, 50} {
		var first float64
		for _, d := range docs {
			run := func() { buildAndRun(t, d.doc, selectorSet(n)) }
			run()
			got := testing.AllocsPerRun(allocRuns, run)
			if first == 0 {
				first = got
				perSelectors[n] = got
				continue
			}
			// The document that matches nothing costs almost nothing: what a queue
			// pays is the rule set.
			if got > first*1.2 {
				t.Errorf("%d selectors: the empty document cost %.0f allocations and %s "+
					"cost %.0f", n, first, d.name, got)
			}
		}
	}

	if perSelectors[10] <= perSelectors[1] || perSelectors[50] <= perSelectors[10] {
		t.Errorf("the cost did not grow with the rule set: %v", perSelectors)
	}
	// Around eight allocations per selector, which is the figure worth stating: fifty
	// selectors is hundreds of allocations before a byte is written.
	if perSelectors[50] < 200 {
		t.Errorf("fifty selectors cost %.0f allocations, which is fewer than this test was "+
			"written against", perSelectors[50])
	}
}

// TestAWriterCannotBeReusedAcrossDocuments, which is why the cost above is per item. It is
// documented on Close; this is it stated as the queue's constraint.
func TestAWriterCannotBeReusedAcrossDocuments(t *testing.T) {
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<a href="/1">one</a>`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write([]byte(`<a href="/2">two</a>`)); !isClosed(err) {
		t.Errorf("a second document written to a closed Writer reported %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("closing twice reported %v", err)
	}
}

func isClosed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "closed")
}

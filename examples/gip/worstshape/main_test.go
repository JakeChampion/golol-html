package main

import (
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// size is small enough that the whole suite runs in a couple of seconds and large enough that a
// shape's character shows: the ratios below hold from a few tens of KB up.
const size = 60000

// TestEveryShapeBuildsRoughlyTheRequestedSize. The whole harness rests on the documents being the
// same size, since the metric is per byte - a shape that built half as much would look cheap.
func TestEveryShapeBuildsRoughlyTheRequestedSize(t *testing.T) {
	for _, s := range Shapes {
		got := len(s.Build(size))
		if got < size*9/10 || got > size*14/10 {
			t.Errorf("%s built %d bytes for a request of %d, which is too far off for a "+
				"per-byte comparison", s.Name, got, size)
		}
	}
}

// TestRankReturnsEveryShapeMostExpensiveFirst - the harness's contract.
func TestRankReturnsEveryShapeMostExpensiveFirst(t *testing.T) {
	results, err := Rank(DefaultHandlers, size, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(Shapes) {
		t.Fatalf("%d results for %d shapes", len(results), len(Shapes))
	}
	for i := 1; i < len(results); i++ {
		if results[i-1].NsPerByte() < results[i].NsPerByte() {
			t.Errorf("result %d (%s, %.3f ns/byte) is cheaper than result %d (%s, %.3f)",
				i-1, results[i-1].Shape, results[i-1].NsPerByte(),
				i, results[i].Shape, results[i].NsPerByte())
		}
	}
	for _, r := range results {
		if r.Bytes == 0 || r.Nanoseconds <= 0 {
			t.Errorf("%s: %d bytes in %d ns, which cannot be right", r.Shape, r.Bytes, r.Nanoseconds)
		}
		t.Logf("%-30s %8.3f ns/byte %10.1f alloc B/KB %8d calls",
			r.Shape, r.NsPerByte(), r.AllocPerByte()*1024, r.Calls)
	}
}

// TestCostFollowsHandlerCallsAndNotBytes is the finding, gated on the two numbers that do not
// depend on the machine: handler calls and allocations. The times are logged by the test above and
// asserted nowhere - how big the ratio is belongs to the host, as this project has learned twice.
func TestCostFollowsHandlerCallsAndNotBytes(t *testing.T) {
	results, err := Rank(DefaultHandlers, size, 3)
	if err != nil {
		t.Fatal(err)
	}

	byCalls := append([]Result(nil), results...)
	sort.SliceStable(byCalls, func(i, j int) bool {
		return byCalls[i].CallsPerByte() > byCalls[j].CallsPerByte()
	})
	most, fewest := byCalls[0], byCalls[len(byCalls)-1]
	t.Logf("most calls per byte: %s (%d calls, %.1f alloc B/KB)",
		most.Shape, most.Calls, most.AllocPerByte()*1024)
	t.Logf("fewest: %s (%d calls, %.1f alloc B/KB)",
		fewest.Shape, fewest.Calls, fewest.AllocPerByte()*1024)

	// Two-sided, because the quietest shape makes no calls at all and "more than a hundred
	// times zero" is not an assertion.
	if most.CallsPerByte() < 0.1 {
		t.Errorf("the busiest shape made %.4f calls per byte, which is too few for this "+
			"harness to be showing anything", most.CallsPerByte())
	}
	if fewest.Calls != 0 && most.Calls < fewest.Calls*100 {
		t.Errorf("the busiest shape made %d calls and the quietest %d, which is not the "+
			"spread this harness is about", most.Calls, fewest.Calls)
	}
	if most.AllocPerByte() < fewest.AllocPerByte()*20 {
		t.Errorf("allocations per byte were %.1f against %.1f, so cost does not track "+
			"calls the way this documents", most.AllocPerByte(), fewest.AllocPerByte())
	}

	// And the ordering by allocations has to look like the ordering by calls, or "cost
	// follows calls" is the wrong claim. Spearman-ish: the top three by calls must all be in
	// the top five by allocations.
	byAlloc := append([]Result(nil), results...)
	sort.SliceStable(byAlloc, func(i, j int) bool {
		return byAlloc[i].AllocPerByte() > byAlloc[j].AllocPerByte()
	})
	topAlloc := map[string]bool{}
	for _, r := range byAlloc[:5] {
		topAlloc[r.Shape] = true
	}
	for _, r := range byCalls[:3] {
		if !topAlloc[r.Shape] {
			t.Errorf("%s is in the top three by calls and not the top five by allocations, "+
				"so the two do not track each other", r.Shape)
		}
	}
}

// TestAStrayEndTagShapeCostsNoCalls is B194 as a cost. No handler ever sees a stray end tag, so a
// document made of them is the floor - and if that ever changes, this harness's cheapest shape
// stops being cheap for a reason worth knowing about.
func TestAStrayEndTagShapeCostsNoCalls(t *testing.T) {
	var shape Shape
	for _, s := range Shapes {
		if strings.Contains(s.Name, "stray end tags") {
			shape = s
		}
	}
	if shape.Build == nil {
		t.Fatal("no stray-end-tag shape in the catalogue")
	}

	calls := 0
	doc := shape.Build(size)
	if _, err := lolhtml.RewriteString(doc, DefaultHandlers(&calls)...); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("%d handler calls for %d bytes of stray end tags, want none", calls, len(doc))
	}

	// The same bytes as start tags are the other extreme, on the same handler set.
	starts := strings.ReplaceAll(doc, "</div>", "<div> ")
	calls = 0
	if _, err := lolhtml.RewriteString(starts, DefaultHandlers(&calls)...); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("the start-tag version made no calls either, so this proves nothing")
	}
	t.Logf("%d bytes: 0 calls as stray end tags, %d as start tags", len(doc), calls)
}

// TestTheWorstShapeIsThreeCallsPerElement, which is what makes it worst: the element, its text,
// and the empty final chunk that ends the text node.
func TestTheWorstShapeIsThreeCallsPerElement(t *testing.T) {
	const items = 1000
	doc := "<ul>" + strings.Repeat("<li>a", items) + "</ul>"

	calls := 0
	if _, err := lolhtml.RewriteString(doc, DefaultHandlers(&calls)...); err != nil {
		t.Fatal(err)
	}
	if want := items * 3; calls != want {
		t.Errorf("%d calls for %d list items, want %d - the element, its text and the "+
			"empty final chunk", calls, items, want)
	}

	// Closing the items changes nothing: the cost is the division, not the malformedness.
	closed := "<ul>" + strings.Repeat("<li>a</li>", items) + "</ul>"
	calls = 0
	if _, err := lolhtml.RewriteString(closed, DefaultHandlers(&calls)...); err != nil {
		t.Fatal(err)
	}
	if want := items * 3; calls != want {
		t.Errorf("closed items: %d calls, want %d", calls, want)
	}
}

// TestTheHarnessMeasuresTheHandlersAndNotTheLibrary - point it at a handler set that does nothing
// and the whole spread should collapse, because there are no calls to make.
func TestTheHarnessMeasuresTheHandlersAndNotTheLibrary(t *testing.T) {
	none := func(calls *int) []lolhtml.Option { return nil }
	results, err := Rank(none, size, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Calls != 0 {
			t.Errorf("%s: %d calls with no handlers registered", r.Shape, r.Calls)
		}
	}

	withHandlers, err := Rank(DefaultHandlers, size, 3)
	if err != nil {
		t.Fatal(err)
	}
	var maxNone, maxSome float64
	for _, r := range results {
		if a := r.AllocPerByte(); a > maxNone {
			maxNone = a
		}
	}
	for _, r := range withHandlers {
		if a := r.AllocPerByte(); a > maxSome {
			maxSome = a
		}
	}
	t.Logf("worst allocations per byte: %.2f with no handlers, %.2f with them", maxNone, maxSome)
	if maxSome < maxNone*5 {
		t.Errorf("the handler set barely changed the worst case (%.2f against %.2f), so this "+
			"harness is measuring the library rather than the handlers", maxSome, maxNone)
	}
}

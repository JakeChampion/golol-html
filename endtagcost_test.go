package lolhtml_test

// What registering an end-tag handler costs, and for how long.
//
// The cost is a live handle per registration, held until the rewrite ends rather
// than until the end tag arrives - so it is per matched element rather than per
// open element, and a wide document is as expensive as a deep one. Nothing in
// MemorySettings bounds it, which is the part worth measuring: a caller who set
// MaxMemory believing it caps a rewrite's memory has capped half of it.

import (
	"io"
	"runtime"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestAnEndTagRegistrationIsHeldUntilTheRewriteEnds, not until the end tag fires.
func TestAnEndTagRegistrationIsHeldUntilTheRewriteEnds(t *testing.T) {
	before := lolhtml.LiveHandles()
	var atStart, atEnd []int64
	if _, err := lolhtml.RewriteString(strings.Repeat("<div>x</div>", 5),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			atStart = append(atStart, lolhtml.LiveHandles())
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				atEnd = append(atEnd, lolhtml.LiveHandles())
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if len(atStart) != 5 || len(atEnd) != 5 {
		t.Fatalf("saw %d starts and %d ends", len(atStart), len(atEnd))
	}
	// Five siblings, each closed before the next opens: if the handle were released
	// at the end tag the count would not climb.
	for i := 1; i < len(atStart); i++ {
		if atStart[i] <= atStart[i-1] {
			t.Errorf("handles at the start tags were %v; they should climb, because a "+
				"registration outlives its end tag", atStart)
			break
		}
	}
	if atEnd[len(atEnd)-1] <= atEnd[0] {
		t.Errorf("handles at the end tags were %v; they should climb too", atEnd)
	}
	requireNoHandleLeak(t, before)
}

// TestTheCostIsPerMatchedElement, and a wide document pays it as much as a deep
// one - the registration is not scoped to what is open.
func TestTheCostIsPerMatchedElement(t *testing.T) {
	const n = 2000
	for _, tc := range []struct{ what, doc string }{
		{"wide", strings.Repeat("<div>x</div>", n)},
		{"deep", strings.Repeat("<div>", n) + "x" + strings.Repeat("</div>", n)},
	} {
		var peak int64
		before := lolhtml.LiveHandles()
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if h := lolhtml.LiveHandles(); h > peak {
				peak = h
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		})); err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		// One per element, give or take the writer's own handles.
		if peak < int64(n) {
			t.Errorf("%s: peak handles %d for %d elements; the cost should be one each",
				tc.what, peak, n)
		}
		requireNoHandleLeak(t, before)
	}

	// Without the registration, the count stays flat.
	var peak int64
	if _, err := lolhtml.RewriteString(strings.Repeat("<div>x</div>", n),
		lolhtml.OnElement("div", func(*lolhtml.Element) error {
			if h := lolhtml.LiveHandles(); h > peak {
				peak = h
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if peak > 10 {
		t.Errorf("peak handles %d without an end-tag registration, want a handful", peak)
	}
}

// TestMaxMemoryDoesNotBoundIt, which is the operational point: the option that
// looks like a memory budget bounds lol-html and not the binding.
func TestMaxMemoryDoesNotBoundIt(t *testing.T) {
	const n = 20000
	doc := strings.Repeat("<div>x</div>", n)

	var m0, m1 runtime.MemStats
	var peak int64
	runtime.GC()
	runtime.ReadMemStats(&m0)
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: 64 << 10}),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if h := lolhtml.LiveHandles(); h > peak {
				peak = h
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, doc); err != nil {
		t.Fatalf("the write failed, so the limit did bound it after all: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	runtime.ReadMemStats(&m1)

	if peak < int64(n) {
		t.Errorf("peak handles %d for %d elements under a 64 KiB limit, want one each",
			peak, n)
	}
	// The Go side allocated far more than the limit, which is the thing to know.
	if allocated := m1.TotalAlloc - m0.TotalAlloc; allocated < 64<<10 {
		t.Errorf("the Go side allocated %d bytes; if it is now under the limit this "+
			"documentation can be revisited", allocated)
	}
}

// TestRegisteringOnlyWhereItIsNeededIsTheAnswer: the same rewrite, deciding before
// registering rather than inside the callback.
func TestRegisteringOnlyWhereItIsNeededIsTheAnswer(t *testing.T) {
	const n = 2000
	var b strings.Builder
	for i := range n {
		if i%100 == 0 {
			b.WriteString(`<div class="interesting">x</div>`)
			continue
		}
		b.WriteString("<div>x</div>")
	}
	doc := b.String()

	var everything, narrow int64
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		if h := lolhtml.LiveHandles(); h > everything {
			everything = h
		}
		return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		if h := lolhtml.LiveHandles(); h > narrow {
			narrow = h
		}
		if cls, _ := e.Attribute("class"); cls != "interesting" {
			return nil
		}
		return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
	})); err != nil {
		t.Fatal(err)
	}
	if narrow*10 > everything {
		t.Errorf("registering on every element peaked at %d handles and registering on "+
			"one in a hundred peaked at %d; the second should be far smaller",
			everything, narrow)
	}
}

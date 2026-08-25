package lolhtml_test

// Case folding is ASCII, everywhere it happens.
//
// HTML folds the ASCII letters of a name and leaves everything else, so a name
// with an accent in it comes back in a spelling nobody wrote and a selector for it
// has to be spelled the same way. Nothing warns: a selector that matches no
// element is a valid selector.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// matchCount is how many elements a selector matched.
func matchCount(t *testing.T, doc, selector string) int {
	t.Helper()
	n := 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(selector, func(*lolhtml.Element) error {
		n++
		return nil
	})); err != nil {
		t.Fatalf("%s on %q: %v", selector, doc, err)
	}
	return n
}

// TestTagNameIsASCIILowercased, which is the fact everything else follows from.
func TestTagNameIsASCIILowercased(t *testing.T) {
	for _, tc := range []struct{ doc, name, source string }{
		{"<DIV>x</DIV>", "div", "DIV"},
		{"<DÉTAIL>x</DÉTAIL>", "dÉtail", "DÉTAIL"},
		{"<détail>x</détail>", "détail", "détail"},
		{"<MY-ÉLÉMENT>x</MY-ÉLÉMENT>", "my-ÉlÉment", "MY-ÉLÉMENT"},
	} {
		var name, source string
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			name, source = e.TagName(), e.TagNamePreserveCase()
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if name != tc.name || source != tc.source {
			t.Errorf("%q: TagName = %q and TagNamePreserveCase = %q, want %q and %q",
				tc.doc, name, source, tc.name, tc.source)
		}
	}
}

// TestATagNameHasToBeginWithAnASCIILetter, or it is not a tag at all: no element
// is reported and no selector can reach it.
func TestATagNameHasToBeginWithAnASCIILetter(t *testing.T) {
	for _, doc := range []string{"<ÉTAT>x</ÉTAT>", "<épais>x</épais>", "<3d>x</3d>"} {
		if got := matchCount(t, doc, "*"); got != 0 {
			t.Errorf("%q reported %d elements, want 0 - it is text", doc, got)
		}
		// And the bytes pass through, as text.
		out, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		if out != doc {
			t.Errorf("%q came out as %q", doc, out)
		}
	}
}

// TestASelectorHasToBeSpelledTheWayTheParserFolds. The lower-cased spelling - the
// one a caller would write - matches nothing.
func TestASelectorHasToBeSpelledTheWayTheParserFolds(t *testing.T) {
	for _, tc := range []struct {
		doc, selector string
		want          int
	}{
		{"<DIV>x</DIV>", "div", 1},
		{"<DIV>x</DIV>", "DIV", 1},
		{"<DÉTAIL>x</DÉTAIL>", "détail", 0},
		{"<DÉTAIL>x</DÉTAIL>", "dÉtail", 1},
		// The selector is folded too, so its ASCII letters may be written either
		// way and its É may not.
		{"<DÉTAIL>x</DÉTAIL>", "DÉTAIL", 1},
		{"<DÉTAIL>x</DÉTAIL>", "DÉtail", 1},
		{"<DÉTAIL>x</DÉTAIL>", "DéTAIL", 0},
		{"<détail>x</détail>", "détail", 1},
		{"<détail>x</détail>", "DÉTAIL", 0},
		{"<détail>x</détail>", "dÉtail", 0},
	} {
		if got := matchCount(t, tc.doc, tc.selector); got != tc.want {
			t.Errorf("%q with %q matched %d, want %d", tc.doc, tc.selector, got, tc.want)
		}
	}
}

// TestAnAttributeNameFoldsTheSameWay.
func TestAnAttributeNameFoldsTheSameWay(t *testing.T) {
	for _, tc := range []struct {
		doc, selector string
		want          int
	}{
		{`<p DATA-VALUE="1">x</p>`, "[data-value]", 1},
		{`<p data-value="1">x</p>`, "[DATA-VALUE]", 1},
		{`<p DATA-VALÉUR="1">x</p>`, "[data-valéur]", 0},
		{`<p DATA-VALÉUR="1">x</p>`, "[data-valÉur]", 1},
		{`<p data-valéur="1">x</p>`, "[DATA-VALÉUR]", 0},
		{`<p data-valéur="1">x</p>`, "[data-valéur]", 1},
	} {
		if got := matchCount(t, tc.doc, tc.selector); got != tc.want {
			t.Errorf("%q with %q matched %d, want %d", tc.doc, tc.selector, got, tc.want)
		}
	}
	// And the name the library reports is folded the same way.
	var names []string
	if _, err := lolhtml.RewriteString(`<p DATA-VALÉUR="1" DATA-VALUE="2">x</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				names = append(names, a.Name+"/"+a.NamePreserveCase)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if want := "data-valÉur/DATA-VALÉUR data-value/DATA-VALUE"; strings.Join(names, " ") != want {
		t.Errorf("names = %q, want %q", strings.Join(names, " "), want)
	}
}

// TestTheCaseFlagFoldsASCIIOnly. This is the sharpest one: the flag exists for
// callers who care about case, and it does not help with the case that is hardest
// to spot.
func TestTheCaseFlagFoldsASCIIOnly(t *testing.T) {
	for _, tc := range []struct {
		doc, selector string
		want          int
	}{
		{`<p data-x="ABC">y</p>`, `[data-x="abc" i]`, 1},
		{`<p data-x="ABC">y</p>`, `[data-x="abc" s]`, 0},
		{`<p data-x="É">y</p>`, `[data-x="é" i]`, 0},
		{`<p data-x="é">y</p>`, `[data-x="É" i]`, 0},
		{`<p data-x="CAFÉ">y</p>`, `[data-x="café" i]`, 0},
		// The ASCII half of the same value does fold, which is what makes the
		// failure quiet: most of the value matches the way the caller expects.
		{`<p data-x="CAFé">y</p>`, `[data-x="café" i]`, 1},
		{`<p data-currency="€">y</p>`, `[data-currency="€"]`, 1},
	} {
		if got := matchCount(t, tc.doc, tc.selector); got != tc.want {
			t.Errorf("%q with %q matched %d, want %d", tc.doc, tc.selector, got, tc.want)
		}
	}
}

// TestTheDocumentedListStillFoldsOnlyASCII, so the HTML case-insensitive attribute
// list is no exception to any of this.
func TestTheDocumentedListStillFoldsOnlyASCII(t *testing.T) {
	// rel is on the list, so its value folds - for ASCII.
	if got := matchCount(t, `<a rel="CANONICAL">x</a>`, `[rel="canonical"]`); got != 1 {
		t.Errorf("rel did not fold: %d", got)
	}
	if got := matchCount(t, `<a rel="CANONICÁL">x</a>`, `[rel="canonicál"]`); got != 0 {
		t.Errorf("rel folded a non-ASCII letter: %d", got)
	}
}

// TestTheWayRoundIsToFoldItYourself: a handler on a wide selector, comparing with
// strings.EqualFold, gets the answer a caller expected from the selector.
func TestTheWayRoundIsToFoldItYourself(t *testing.T) {
	const doc = `<DÉTAIL>a</DÉTAIL><détail>b</détail><DIV>c</DIV>`
	matched := 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if strings.EqualFold(e.TagName(), "détail") {
			matched++
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if matched != 2 {
		t.Errorf("EqualFold matched %d elements, want 2 - both spellings", matched)
	}
	// Which is more than any single selector can do.
	if got := matchCount(t, doc, "détail") + matchCount(t, doc, "dÉtail"); got != 2 {
		t.Errorf("two selectors matched %d, want 2", got)
	}
}

package lolhtml_test

// Which selectors are supported.
//
// The published guidance used to be "lol-html implements a subset of CSS
// selectors; see its README for which", which sends a caller to another
// project's documentation for something they need before their code compiles.
// The subset is small enough to write down, and one rule covers almost all of
// it: a selector works if the rewriter can decide it when it sees the start tag.
//
// This file is what keeps the written list true. Every row is exercised, so a
// selector gaining or losing support fails here rather than in a user's build.

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// selectorDoc has three list items with distinct classes, a paragraph with an
// id, and an element carrying an empty attribute.
const selectorDoc = `<ul><li class="a">1</li><li class="b">2</li><li class="c">3</li></ul>` +
	`<p id="p1" lang="en-GB" data-v="abc" style="">x</p>`

// matches reports how many elements a selector matched, or an error if the
// selector was rejected.
func matches(sel string) (int, error) {
	n := 0
	_, err := lolhtml.RewriteString(selectorDoc,
		lolhtml.OnElement(sel, func(*lolhtml.Element) error {
			n++
			return nil
		}))
	return n, err
}

func TestSupportedSelectors(t *testing.T) {
	tests := []struct {
		sel  string
		want int
	}{
		// Simple selectors.
		{"li", 3},
		{"*", 5},
		{".a", 1},
		{"#p1", 1},
		{"li.a", 1},
		{"li#p1", 0}, // the id is on a p, so this correctly matches nothing

		// Case insensitivity.
		{"LI", 3},
		{"Li", 3},
		{`[CLASS="a"]`, 1},

		// Selector lists.
		{"li, p", 4},

		// Combinators.
		{"ul li", 3},
		{"ul > li", 3},

		// Attribute selectors, every operator.
		{"[class]", 3},
		{"[data-v]", 1},
		{"[missing]", 0},
		{`[class="a"]`, 1},
		{"[class=a]", 1},
		{`[class~="a"]`, 1},
		{`[lang|="en"]`, 1},
		{`[lang|="en-GB"]`, 1},
		{`[lang|="fr"]`, 0},
		{`[data-v^="ab"]`, 1},
		{`[data-v$="bc"]`, 1},
		{`[data-v*="b"]`, 1},

		// Case-sensitivity flags.
		{`[data-v="ABC"]`, 0},
		{`[data-v="ABC" i]`, 1},
		{`[data-v="abc" s]`, 1},
		{`[data-v="ABC" s]`, 0},

		// A present-but-empty attribute is present.
		{"[style]", 1},

		// :not with a single simple selector, which is the only form that is
		// correct. See TestNotWithACompoundSelectorIsWrong.
		{":not(.a)", 4},
		{":not(li)", 2},
		{":not(:first-child)", 3},
		{"li:not(:first-child)", 2},

		// Positional selectors that only need what came before. The counts
		// include the <ul>, which is itself the first child of <body>.
		{":first-child", 2},
		{":nth-child(2)", 2},
		{":nth-child(odd)", 3},
		{":nth-child(even)", 2},
		{":nth-child(2n+1)", 3},
		{":first-of-type", 3},
		{":nth-of-type(1)", 3},

		// Any namespace.
		{"*|li", 3},

		// Compound.
		{"li:nth-child(2):not(.z)", 1},
		{"[class^=a][class$=a]", 1},
	}

	for _, tt := range tests {
		t.Run(tt.sel, func(t *testing.T) {
			got, err := matches(tt.sel)
			if err != nil {
				t.Fatalf("rejected, but documented as supported: %v", err)
			}
			if got != tt.want {
				t.Errorf("matched %d elements, want %d", got, tt.want)
			}
		})
	}
}

func TestUnsupportedSelectors(t *testing.T) {
	// Grouped by why, because the grouping is the documentation.
	groups := map[string][]string{
		"needs what follows the start tag": {
			":last-child", ":only-child", ":empty",
			":last-of-type", ":nth-last-child(1)", ":nth-last-of-type(1)",
		},
		"sibling combinators": {"li + li", "li ~ li"},
		"state or a document a stream does not have": {
			":root", ":scope", ":host", ":checked", ":disabled", ":hover",
		},
		"grouping pseudo-classes": {":is(li)", ":where(li)", ":has(li)"},
		"pseudo-elements":         {"::before", "::first-line", "li::marker", ":before"},
		"explicit namespaces":     {"svg|circle", "li|*"},
		"malformed":               {"", "[class=]", "li[", ">"},
	}

	for why, sels := range groups {
		t.Run(why, func(t *testing.T) {
			for _, sel := range sels {
				_, err := matches(sel)
				if err == nil {
					t.Errorf("%q was accepted, but is documented as unsupported", sel)
					continue
				}

				// The rejection has to name the selector, or a caller whose
				// selector came from configuration cannot find it.
				var se *lolhtml.SelectorError
				if !errors.As(err, &se) {
					t.Errorf("%q: err = %T, want *SelectorError", sel, err)
					continue
				}
				if se.Selector != sel {
					t.Errorf("%q: SelectorError.Selector = %q", sel, se.Selector)
				}
				if se.Message == "" {
					t.Errorf("%q: no reason given", sel)
				}
				if sel != "" && !strings.Contains(err.Error(), sel) {
					t.Errorf("%q: the message does not contain the selector: %v", sel, err)
				}
			}
		})
	}
}

// TestNotWithACompoundSelectorIsWrong pins a defect rather than a limitation.
//
// :not() is correct with one simple selector. With a compound one it negates
// each part separately and requires all of them - :not(div.a) evaluated as
// :not(div):not(.a) - which is the wrong half of De Morgan's law. Upstream
// applies the negation per component in add_selector_components, and its own
// tests only ever put a single simple selector inside :not(), which is why this
// survived.
//
// It is silent: no error, just the wrong set of elements. For a filter written
// as ":not(a.trusted)" that is a hole, since every anchor is skipped.
//
// This test asserts the *wrong* behaviour on purpose, so that a fix upstream
// fails it and the documentation gets corrected rather than quietly rotting.
func TestNotWithACompoundSelectorIsWrong(t *testing.T) {
	const doc = `<div class="a">1</div><div class="b">2</div>` +
		`<span class="a">3</span><span class="b">4</span>`

	matched := func(sel string) []string {
		var got []string
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
				cls, _ := e.Attribute("class")
				got = append(got, e.TagName()+"."+cls)
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", sel, err)
		}
		return got
	}

	tests := []struct {
		sel string
		// correct is what CSS says; actual is what happens.
		correct, actual []string
	}{{
		sel:     ":not(div.a)",
		correct: []string{"div.b", "span.a", "span.b"},
		actual:  []string{"span.b"},
	}, {
		sel:     `:not(div[class="a"])`,
		correct: []string{"div.b", "span.a", "span.b"},
		actual:  []string{"span.b"},
	}, {
		sel:     ":not(div.a, span.b)",
		correct: []string{"div.b", "span.a"},
		actual:  nil,
	}}

	for _, tt := range tests {
		t.Run(tt.sel, func(t *testing.T) {
			got := matched(tt.sel)

			if strings.Join(got, " ") == strings.Join(tt.correct, " ") {
				t.Fatalf("%s now matches %v, which is what CSS says - the defect has "+
					"been fixed upstream, so the package documentation and this test "+
					"should be updated", tt.sel, got)
			}
			if strings.Join(got, " ") != strings.Join(tt.actual, " ") {
				t.Errorf("%s matched %v; expected the known-wrong %v", tt.sel, got, tt.actual)
			}
		})
	}

	// And the equivalence that explains it: the compound form behaves exactly
	// like negating each part separately.
	if a, b := matched(":not(div.a)"), matched(":not(div):not(.a)"); strings.Join(a, " ") != strings.Join(b, " ") {
		t.Errorf(":not(div.a) matched %v and :not(div):not(.a) matched %v; "+
			"they are equal today, and that equality is the bug", a, b)
	}

	// The single-simple-selector forms are correct, which is what makes the
	// recommendation in the documentation usable.
	for _, tt := range []struct {
		sel  string
		want []string
	}{
		{":not(div)", []string{"span.a", "span.b"}},
		{":not(.a)", []string{"div.b", "span.b"}},
		{`:not([class="a"])`, []string{"div.b", "span.b"}},
	} {
		if got := matched(tt.sel); strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("%s matched %v, want %v", tt.sel, got, tt.want)
		}
	}
}

// TestAnUnsupportedSelectorFailsAtNewWriter, rather than being ignored or
// failing later on a document that happens to contain a match. A selector is a
// build-time mistake and should be reported like one.
func TestAnUnsupportedSelectorFailsAtNewWriter(t *testing.T) {
	_, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement(":last-child", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("NewWriter accepted an unsupported selector")
	}

	var se *lolhtml.SelectorError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T, want *SelectorError", err)
	}
	if se.Selector != ":last-child" {
		t.Errorf("Selector = %q", se.Selector)
	}
}

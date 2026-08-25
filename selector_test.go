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

// A combinator inside :not() is rejected, which the rule the documentation gives
// - supported if the rewriter can decide it at the start tag - does not predict.
// :not(div p) asks whether an element is inside a div, which is exactly what the
// plain descendant selector div p decides, at the start tag, and that one works.
//
// The sibling combinators are unsupported anywhere, so :not(div + p) and
// :not(div ~ p) are no surprise. The descendant and child ones are supported
// everywhere except inside :not().
//
// It matters because "not inside an X" is a real thing to want - an annotator
// looking for outermost regions asks it of every element - and the answer is that
// there is no selector for it, so a handler has to keep a stack.
//
// The error names the pseudo-class rather than what is inside it, which is the
// part that costs the time. This test pins both the rejection and the message, so
// upstream accepting it, or saying something more useful, fails here and the
// documentation gets corrected rather than quietly rotting.
func TestACombinatorInsideNotIsRejected(t *testing.T) {
	const doc = `<div><p class="a">1</p></div><p class="a">2</p>`

	rejected := []string{
		":not(div p)",
		":not(div > p)",
		":not(div + p)",
		":not(div ~ p)",
		"p:not(div p)",
		":not(.a .b)",
	}
	for _, sel := range rejected {
		_, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil }))
		if err == nil {
			t.Errorf("%s was accepted; the documentation says it is rejected", sel)
			continue
		}
		if !strings.Contains(err.Error(), "Unsupported pseudo-class or pseudo-element") {
			t.Errorf("%s: %v", sel, err)
		}
	}

	// Everything else :not() takes, so the boundary is pinned on both sides and a
	// change either way is visible.
	accepted := []string{
		":not(div)", ":not(.a)", ":not(#i)", ":not([class])", `:not([class="a"])`,
		":not(*)", ":not(div.a)", ":not(div, span)", ":not(:first-child)",
		":not(:nth-child(2))", ":not(:not(div))", ":not(*|div)",
	}
	for _, sel := range accepted {
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%s was rejected: %v", sel, err)
		}
	}

	// And the combinators themselves are fine outside :not(), which is what makes the
	// rejection a gap rather than a rule.
	for _, sel := range []string{"div p", "div > p", ".a .b"} {
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%s was rejected outside :not(): %v", sel, err)
		}
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

// caseInsensitiveAttributes is the HTML specification's list of attributes whose
// values a selector matches ASCII-case-insensitively. Written out rather than
// summarised, because the whole point of the list is that it is arbitrary: there
// is no rule connecting rel and valign that also excludes name and src.
var caseInsensitiveAttributes = []string{
	"accept", "accept-charset", "align", "alink", "axis", "bgcolor", "charset",
	"checked", "clear", "codetype", "color", "compact", "declare", "defer",
	"dir", "direction", "disabled", "enctype", "face", "frame", "hreflang",
	"http-equiv", "lang", "language", "link", "media", "method", "multiple",
	"nohref", "noresize", "noshade", "nowrap", "readonly", "rel", "rev",
	"rules", "scope", "scrolling", "selected", "shape", "target", "text",
	"type", "valign", "valuetype", "vlink",
}

// caseSensitiveAttributes is a control group: attributes a rewrite is likely to
// select on, none of which are on the list above.
var caseSensitiveAttributes = []string{
	"id", "class", "href", "src", "alt", "title", "name", "value", "style",
	"data-x", "content", "for", "width", "height", "role", "srcset", "integrity",
}

// matchesIgnoringCase reports whether [attr="abc"] matches attr="ABC".
func matchesIgnoringCase(t *testing.T, attr string) bool {
	t.Helper()
	matched := 0
	if _, err := lolhtml.RewriteString(`<p `+attr+`="ABC">x</p>`,
		lolhtml.OnElement(`[`+attr+`="abc"]`, func(*lolhtml.Element) error {
			matched++
			return nil
		})); err != nil {
		t.Fatalf("%s: %v", attr, err)
	}
	return matched == 1
}

// TestAttributeValueCaseFollowsTheHTMLList. Both halves matter: a value matched
// case-insensitively when it should not be rewrites elements the selector was not
// meant to reach, and one matched exactly when it should not be misses them.
//
// The list is checked in full rather than sampled, since it has no internal
// logic to sample from.
func TestAttributeValueCaseFollowsTheHTMLList(t *testing.T) {
	for _, attr := range caseInsensitiveAttributes {
		if !matchesIgnoringCase(t, attr) {
			t.Errorf(`[%s="abc"] did not match %s="ABC"; this attribute is on the `+
				"HTML case-insensitive list", attr, attr)
		}
	}
	for _, attr := range caseSensitiveAttributes {
		if matchesIgnoringCase(t, attr) {
			t.Errorf(`[%s="abc"] matched %s="ABC"; this attribute is not on the `+
				"HTML case-insensitive list, so the match should be exact", attr, attr)
		}
	}
}

// TestTheCaseFlagsOverrideTheDefault, in both directions and for attributes on
// either side of the list - which is what makes them the answer when it matters.
func TestTheCaseFlagsOverrideTheDefault(t *testing.T) {
	for _, tt := range []struct {
		doc, selector string
		want          int
	}{
		// rel is on the list, so exact matching needs to be asked for.
		{`<link rel="CANONICAL">`, `[rel="canonical"]`, 1},
		{`<link rel="CANONICAL">`, `[rel="canonical" s]`, 0},
		{`<link rel="CANONICAL">`, `[rel="CANONICAL" s]`, 1},

		// name is not, so case-insensitive matching needs to be asked for.
		{`<p name="Foo">x</p>`, `[name="foo"]`, 0},
		{`<p name="Foo">x</p>`, `[name="foo" i]`, 1},
		{`<p name="Foo">x</p>`, `[name="Foo"]`, 1},

		// And the flags work with the other operators too.
		{`<p class="AbC dEf">x</p>`, `[class~="abc" i]`, 1},
		{`<p class="AbC dEf">x</p>`, `[class~="abc"]`, 0},
		{`<p href="/A/B">x</p>`, `[href^="/a" i]`, 1},
		{`<p href="/A/B">x</p>`, `[href^="/a"]`, 0},
		{`<p href="/A/B">x</p>`, `[href$="/b" i]`, 1},
		{`<p href="/A/B">x</p>`, `[href*="/a/" i]`, 1},
	} {
		matched := 0
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.selector, func(*lolhtml.Element) error {
				matched++
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.selector, err)
		}
		if matched != tt.want {
			t.Errorf("%s on %s matched %d times, want %d",
				tt.selector, tt.doc, matched, tt.want)
		}
	}
}

// TestTheClassAndIDShorthandsAreExact, which is the same rule as [class=] and
// [id=] - neither attribute is on the list - and worth pinning separately because
// the shorthands look like they might be special.
func TestTheClassAndIDShorthandsAreExact(t *testing.T) {
	for _, tt := range []struct {
		doc, selector string
		want          int
	}{
		{`<p class="Foo">x</p>`, `.Foo`, 1},
		{`<p class="Foo">x</p>`, `.foo`, 0},
		{`<p class="Foo">x</p>`, `[class="foo"]`, 0},
		{`<p class="Foo">x</p>`, `[class="foo" i]`, 1},
		{`<p id="Bar">x</p>`, `#Bar`, 1},
		{`<p id="Bar">x</p>`, `#bar`, 0},
		{`<p id="Bar">x</p>`, `[id="bar" i]`, 1},
	} {
		matched := 0
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.selector, func(*lolhtml.Element) error {
				matched++
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.selector, err)
		}
		if matched != tt.want {
			t.Errorf("%s on %s matched %d times, want %d",
				tt.selector, tt.doc, matched, tt.want)
		}
	}
}

// TestNamesAreMatchedWithoutRegardToCase, in both directions, so the difference
// from the value rule above is on the record.
func TestNamesAreMatchedWithoutRegardToCase(t *testing.T) {
	for _, tt := range []struct{ doc, selector string }{
		{`<P>x</P>`, `p`},
		{`<p>x</p>`, `P`},
		{`<p REL="x">y</p>`, `[rel]`},
		{`<p rel="x">y</p>`, `[REL]`},
		{`<svg><textPath/></svg>`, `textpath`},
		{`<svg><textPath/></svg>`, `textPath`},
	} {
		matched := 0
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.selector, func(*lolhtml.Element) error {
				matched++
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.selector, err)
		}
		if matched != 1 {
			t.Errorf("%s on %s matched %d times, want 1", tt.selector, tt.doc, matched)
		}
	}
}

// TestAColonOrDotInANameHasToBeEscaped is the table in the package
// documentation. The colon is the one that matters: the message a caller gets
// names a pseudo-class they did not write, so nothing points at the answer.
func TestAColonOrDotInANameHasToBeEscaped(t *testing.T) {
	tests := []struct {
		sel     string
		doc     string
		matches int
		// fails is whether the selector is rejected at all. A selector that
		// parses and matches nothing is the worse outcome, so the distinction
		// is asserted.
		fails bool
	}{
		{sel: `esi:include`, doc: `<esi:include src=a>`, fails: true},
		{sel: `esi\:include`, doc: `<esi:include src=a>`, matches: 1},
		{sel: `ESI\:INCLUDE`, doc: `<esi:include src=a>`, matches: 1},
		{sel: `[xlink:href]`, doc: `<svg><a xlink:href="x"/></svg>`, fails: true},
		{sel: `[xlink\:href]`, doc: `<svg><a xlink:href="x"/></svg>`, matches: 1},
		// Parses, and means "an element in class a and in class b".
		{sel: `.a.b`, doc: `<p class="a.b">x</p>`, matches: 0},
		{sel: `.a\.b`, doc: `<p class="a.b">x</p>`, matches: 1},
		{sel: `#a\.b`, doc: `<p id="a.b">x</p>`, matches: 1},
		// A hyphen is a name character and needs nothing.
		{sel: `my-element`, doc: `<my-element>x</my-element>`, matches: 1},
		{sel: `a\:b\:c`, doc: `<a:b:c>x</a:b:c>`, matches: 1},
		// The escape hatch, for anyone who has not read this far: an attribute
		// selector reaches an element whose name cannot be written.
		{sel: `[src]`, doc: `<esi:include src=a>`, matches: 1},
	}
	for _, tt := range tests {
		n := 0
		_, err := lolhtml.RewriteString(tt.doc, lolhtml.OnElement(tt.sel, func(*lolhtml.Element) error {
			n++
			return nil
		}))
		if tt.fails {
			if err == nil {
				t.Errorf("%q was accepted", tt.sel)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.sel, err)
			continue
		}
		if n != tt.matches {
			t.Errorf("%q matched %d times, want %d", tt.sel, n, tt.matches)
		}
	}
}

// The error for an unescaped colon says what to do about it, because lol-html's
// own message points at a pseudo-class the caller did not write.
func TestTheSelectorErrorSuggestsEscapingAColon(t *testing.T) {
	_, err := lolhtml.RewriteString(`<p>a</p>`,
		lolhtml.OnElement("esi:include", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"esi:include", "must be escaped", `esi\:include`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}

	// Not for a pseudo-element, where a colon cannot be part of a name.
	_, err = lolhtml.RewriteString(`<p>a</p>`,
		lolhtml.OnElement("p::before", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("a pseudo-element was accepted")
	}
	if strings.Contains(err.Error(), "must be escaped") {
		t.Errorf("a pseudo-element got the escaping hint: %v", err)
	}

	// Nor for a failure that has nothing to do with a colon.
	_, err = lolhtml.RewriteString(`<p>a</p>`,
		lolhtml.OnElement("[", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("an unterminated attribute selector was accepted")
	}
	if strings.Contains(err.Error(), "must be escaped") {
		t.Errorf("an unrelated failure got the escaping hint: %v", err)
	}
}

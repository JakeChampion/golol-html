package main

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const sample = `<div><p>before</p><esi:include src="/frag"/><p>after</p></div>`

var frags = Fragments{"/frag": `<b>F</b>`}

// TestOnlyAnInsertionAtTheStartTagIsLossless, which is the measurement the whole program is
// about. Without the option an esi: element is an ordinary unclosed container, so its "end" is
// the enclosing element's end tag, and every operation positioned there acts on the wrong range.
func TestOnlyAnInsertionAtTheStartTagIsLossless(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
		op   func(*lolhtml.Element) error
	}{
		{"Replace", `<div><p>before</p><b>F</b>`,
			func(e *lolhtml.Element) error { return e.Replace(`<b>F</b>`, lolhtml.HTML) }},
		{"Before then Remove", `<div><p>before</p><b>F</b>`,
			func(e *lolhtml.Element) error {
				if err := e.Before(`<b>F</b>`, lolhtml.HTML); err != nil {
					return err
				}
				e.Remove()
				return nil
			}},
		{"SetInnerContent", `<div><p>before</p><esi:include src="/frag"/><b>F</b></div>`,
			func(e *lolhtml.Element) error {
				return e.SetInnerContent(`<b>F</b>`, lolhtml.HTML)
			}},
		{"RemoveAndKeepContent", `<div><p>before</p><p>after</p>`,
			func(e *lolhtml.Element) error {
				e.RemoveAndKeepContent()
				return nil
			}},
		{"Before then RemoveAndKeep", `<div><p>before</p><b>F</b><p>after</p>`,
			func(e *lolhtml.Element) error {
				if err := e.Before(`<b>F</b>`, lolhtml.HTML); err != nil {
					return err
				}
				e.RemoveAndKeepContent()
				return nil
			}},
		{"Before alone",
			`<div><p>before</p><b>F</b><esi:include src="/frag"/><p>after</p></div>`,
			func(e *lolhtml.Element) error { return e.Before(`<b>F</b>`, lolhtml.HTML) }},
	} {
		out, err := lolhtml.RewriteString(sample, lolhtml.OnElement(`esi\:include`, tt.op))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out != tt.want {
			t.Errorf("%s:\n  got  %s\n  want %s", tt.name, out, tt.want)
		}
	}

	// Only the last of those keeps every element the source had. The rest lose at least the
	// closing tag of the element that encloses the include.
	source := counts(t, sample)
	for _, tt := range []struct {
		name  string
		op    func(*lolhtml.Element) error
		whole bool
	}{
		{"Replace", func(e *lolhtml.Element) error {
			return e.Replace(`<b>F</b>`, lolhtml.HTML)
		}, false},
		{"Before alone", func(e *lolhtml.Element) error {
			return e.Before(`<b>F</b>`, lolhtml.HTML)
		}, true},
	} {
		out, err := lolhtml.RewriteString(sample, lolhtml.OnElement(`esi\:include`, tt.op))
		if err != nil {
			t.Fatal(err)
		}
		after := counts(t, out)
		kept := true
		for name, n := range source {
			if after[name] < n {
				kept = false
			}
		}
		if kept != tt.whole {
			t.Errorf("%s kept everything = %v, want %v (source %v, after %v)",
				tt.name, kept, tt.whole, source, after)
		}
	}
}

// counts returns the elements a parser finds, by name.
func counts(t *testing.T, doc string) map[string]int {
	t.Helper()
	out := map[string]int{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		out[e.TagName()]++
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWithTheOptionEveryOperationIsCorrect, which is the other half of the comparison: the option
// makes an esi: element void, so there is no end tag to be positioned at wrongly.
func TestWithTheOptionEveryOperationIsCorrect(t *testing.T) {
	const want = `<div><p>before</p><b>F</b><p>after</p></div>`
	for _, tt := range []struct {
		name string
		op   func(*lolhtml.Element) error
	}{
		{"Replace", func(e *lolhtml.Element) error {
			return e.Replace(`<b>F</b>`, lolhtml.HTML)
		}},
		{"Before then Remove", func(e *lolhtml.Element) error {
			if err := e.Before(`<b>F</b>`, lolhtml.HTML); err != nil {
				return err
			}
			e.Remove()
			return nil
		}},
		{"Before then RemoveAndKeep", func(e *lolhtml.Element) error {
			if err := e.Before(`<b>F</b>`, lolhtml.HTML); err != nil {
				return err
			}
			e.RemoveAndKeepContent()
			return nil
		}},
	} {
		out, err := lolhtml.RewriteString(sample,
			lolhtml.WithESITags(), lolhtml.OnElement(`esi\:include`, tt.op))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out != want {
			t.Errorf("%s:\n  got  %s\n  want %s", tt.name, out, want)
		}
	}

	// And RemoveAndKeepContent with the option removes only the marker, where without it
	// the enclosing end tag goes too.
	out, err := lolhtml.RewriteString(sample, lolhtml.WithESITags(),
		lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
			e.RemoveAndKeepContent()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div><p>before</p><p>after</p></div>`; out != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

// TestTheTwoExpansionsDifferOnlyByTheMarkers, over documents with structure, which is the claim
// the program's report makes.
func TestTheTwoExpansionsDifferOnlyByTheMarkers(t *testing.T) {
	for depth := 1; depth <= 4; depth++ {
		for n := 1; n <= 3; n++ {
			doc := nest(depth, n)
			c, err := Compare(doc, frags)
			if err != nil {
				t.Fatalf("depth %d n %d: %v", depth, n, err)
			}
			if !c.SameElements {
				t.Errorf("depth %d n %d: differ by %v\n  with:    %s\n  without: %s",
					depth, n, c.Extra, c.With.Doc, c.Without.Doc)
			}
			if c.With.Expanded != c.Without.Expanded {
				t.Errorf("depth %d n %d: %d expanded against %d",
					depth, n, c.With.Expanded, c.Without.Expanded)
			}
			if c.Without.Markers != c.With.Expanded {
				t.Errorf("depth %d n %d: %d markers for %d includes",
					depth, n, c.Without.Markers, c.With.Expanded)
			}
			// The manual output is longer by exactly the markers it left in.
			if len(c.Without.Doc) <= len(c.With.Doc) {
				t.Errorf("depth %d n %d: the manual output is not longer", depth, n)
			}
		}
	}
}

// nest builds a document with includes at several depths.
func nest(depth, n int) string {
	if depth == 0 {
		return `<esi:include src="/frag"/>`
	}
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, `<div class="d%dn%d"><p>t</p>%s<p>u</p></div>`,
			depth, i, nest(depth-1, n))
	}
	return b.String()
}

// TestAMissingFragmentIsLeftAloneByBoth, so a resolver that does not know a source cannot blank
// part of the page.
func TestAMissingFragmentIsLeftAloneByBoth(t *testing.T) {
	const doc = `<div><esi:include src="/gone"/><p>after</p></div>`
	c, err := Compare(doc, frags)
	if err != nil {
		t.Fatal(err)
	}
	if c.With.Expanded != 0 || c.Without.Expanded != 0 {
		t.Errorf("%d and %d expanded", c.With.Expanded, c.Without.Expanded)
	}
	if c.With.Missing != 1 || c.Without.Missing != 1 {
		t.Errorf("%d and %d missing", c.With.Missing, c.Without.Missing)
	}
	if c.With.Doc != doc || c.Without.Doc != doc {
		t.Errorf("the document changed:\n  %s\n  %s", c.With.Doc, c.Without.Doc)
	}
	if !strings.Contains(c.String(), "no fragment") {
		t.Errorf("the report does not say so:\n%s", c)
	}
}

// TestTheColonHasToBeEscapedInTheSelector, because the error blames a pseudo-class and the
// mistake is easy to make from a string.
func TestTheColonHasToBeEscapedInTheSelector(t *testing.T) {
	if _, err := lolhtml.RewriteString(sample,
		lolhtml.OnElement("esi:include", func(*lolhtml.Element) error { return nil })); err == nil {
		t.Error("an unescaped colon was accepted")
	} else if !strings.Contains(err.Error(), "pseudo-class") {
		t.Errorf("err = %v", err)
	}

	n := 0
	if _, err := lolhtml.RewriteString(sample,
		lolhtml.OnElement(`esi\:include`, func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the escaped selector matched %d", n)
	}
}

// TestTheOptionDoesNotChangeADocumentWithNoIncludes, since it is a parsing option and a page
// without ESI should not be able to tell.
func TestTheOptionDoesNotChangeADocumentWithNoIncludes(t *testing.T) {
	for _, doc := range []string{
		`<div><p>a</p><img src="/x"><ul><li>b<li>c</ul></div>`,
		`<p>a &amp; b</p><script>var a = 1 < 2;</script>`,
		`<!-- c --><table><tr><td>x</table>`,
	} {
		with, err := lolhtml.RewriteString(doc, lolhtml.WithESITags())
		if err != nil {
			t.Fatal(err)
		}
		without, err := lolhtml.RewriteString(doc)
		if err != nil {
			t.Fatal(err)
		}
		if with != doc || without != doc {
			t.Errorf("%s:\n  with:    %s\n  without: %s", doc, with, without)
		}
	}
}

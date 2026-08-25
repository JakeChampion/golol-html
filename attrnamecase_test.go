package lolhtml_test

// What happens to a name on the way in.
//
// Reading an attribute keeps the document's spelling and offers it as
// NamePreserveCase, with a comment about SVG's viewBox. Writing one does not
// always, and the difference is only visible in foreign content - where it is the
// difference between an attribute and a dead attribute.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestAddingAnAttributeLowerCasesItsName, and updating one does not. The same
// call on two documents, one of which has the attribute already.
func TestAddingAnAttributeLowerCasesItsName(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		// Updating: the document's spelling survives.
		{`<svg viewBox="0 0 1 1">x</svg>`, `<svg viewBox="0 0 9 9">x</svg>`},
		// Adding: it does not, and viewbox is not viewBox to a browser.
		{`<svg>x</svg>`, `<svg viewbox="0 0 9 9">x</svg>`},
		// Updating is case-insensitive about which attribute it found, and still
		// keeps what the document wrote.
		{`<svg VIEWBOX="0 0 1 1">x</svg>`, `<svg VIEWBOX="0 0 9 9">x</svg>`},
	} {
		got, err := lolhtml.RewriteString(tc.doc, lolhtml.OnElement("svg", func(e *lolhtml.Element) error {
			return e.SetAttribute("viewBox", "0 0 9 9")
		}))
		if err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheReadSideKeepsWhatTheWriteSideDoesNot, which is what makes this worth
// documenting: the library carries the spelling carefully in one direction.
func TestTheReadSideKeepsWhatTheWriteSideDoesNot(t *testing.T) {
	var list []lolhtml.Attribute
	if _, err := lolhtml.RewriteString(`<svg viewBox="0 0 1 1" preserveAspectRatio="none">x</svg>`,
		lolhtml.OnElement("svg", func(e *lolhtml.Element) error {
			list = e.AttributeList()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("%d attributes, want 2", len(list))
	}
	for i, want := range []struct{ lower, source string }{
		{"viewbox", "viewBox"},
		{"preserveaspectratio", "preserveAspectRatio"},
	} {
		if list[i].Name != want.lower || list[i].NamePreserveCase != want.source {
			t.Errorf("attribute %d is %q/%q, want %q/%q",
				i, list[i].Name, list[i].NamePreserveCase, want.lower, want.source)
		}
	}
	// And a rebuild through NamePreserveCase is the way to add one with a capital.
	got, err := lolhtml.RewriteString(`<svg>x</svg>`, lolhtml.OnElement("svg", func(e *lolhtml.Element) error {
		return e.Replace(`<svg viewBox="0 0 9 9">x</svg>`, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `viewBox="0 0 9 9"`) {
		t.Errorf("got %q, want a viewBox with its capital", got)
	}
}

// TestAnAttributeNameThatCouldBreakTheTagIsRefused. A name taken from a document
// is the interesting case, and every character that could end the attribute or
// start another is an error rather than markup.
func TestAnAttributeNameThatCouldBreakTheTagIsRefused(t *testing.T) {
	for _, name := range []string{"a b", "a\tb", "a\nb", "a\fb", "a/b", "a=b", "a>b", ""} {
		out, err := lolhtml.RewriteString("<div>x</div>", lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetAttribute(name, "1")
		}))
		if err == nil {
			t.Errorf("SetAttribute(%q) was accepted, producing %q", name, out)
		}
		if out != "" {
			t.Errorf("SetAttribute(%q) failed and still produced %q", name, out)
		}
	}
}

// TestAnOddButHarmlessAttributeNameIsAccepted, and reads back as itself: none of
// these can end a tag, so refusing them would be the library deciding what a
// document may contain.
func TestAnOddButHarmlessAttributeNameIsAccepted(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{`a"b`, `a"b`},
		{"a'b", "a'b"},
		{"a<b", "a<b"},
		{"0a", "0a"},
		{"a:b", "a:b"},
		{"a-b", "a-b"},
		{"ä", "ä"},
		// Lower-cased on the way in, like any added name.
		{"aB", "ab"},
	} {
		out, err := lolhtml.RewriteString("<div>x</div>", lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetAttribute(tc.name, "1")
		}))
		if err != nil {
			t.Errorf("SetAttribute(%q): %v", tc.name, err)
			continue
		}
		if want := `<div ` + tc.want + `="1">x</div>`; out != want {
			t.Errorf("SetAttribute(%q)\n got %q\nwant %q", tc.name, out, want)
			continue
		}
		// And the document that comes out has one attribute with that name.
		var got []lolhtml.Attribute
		if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			got = e.AttributeList()
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].NamePreserveCase != tc.want {
			t.Errorf("SetAttribute(%q) produced %v, want one attribute named %q",
				tc.name, got, tc.want)
		}
	}
}

// TestSetTagNameKeepsItsCaseAndWantsALetterFirst, which is the other half of the
// asymmetry: two methods that take a name and treat it differently.
func TestSetTagNameKeepsItsCaseAndWantsALetterFirst(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string // "" means an error is expected
	}{
		{"span", "<span>x</span>"},
		{"sPan", "<sPan>x</sPan>"},
		{"a:b", "<a:b>x</a:b>"},
		{"a<b", "<a<b>x</a<b>"},
		{"0a", ""},
		{"ä", ""},
		{"a b", ""},
		{"a/b", ""},
		{"a>b", ""},
		{"", ""},
	} {
		got, err := lolhtml.RewriteString("<div>x</div>", lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetTagName(tc.name)
		}))
		if tc.want == "" {
			if err == nil {
				t.Errorf("SetTagName(%q) was accepted, producing %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SetTagName(%q): %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SetTagName(%q)\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

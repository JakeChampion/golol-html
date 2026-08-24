package differential

// Tag names, against the names a parser puts in the tree.
//
// An HTML tag name is lower-cased on tokenisation, and both this library and a
// parser agree about that. A foreign one is different: the specification's SVG
// tag-name adjustment maps the tokenised name to a canonical spelling, so a
// browser's DOM holds linearGradient however the page wrote it. lol-html applies
// no adjustment, which is what this file pins - a rewrite comparing a tag name
// with a canonical SVG name is wrong for two spellings out of three.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// svgNames returns the names a parser gives the SVG children of the document.
func svgNames(t *testing.T, doc string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Namespace == "svg" && n.Data != "svg" {
			out = append(out, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// reported returns what the rewriter says about the same elements.
func reported(t *testing.T, doc string) (lower, preserved []string) {
	t.Helper()
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("svg > *", func(e *lolhtml.Element) error {
			lower = append(lower, e.TagName())
			preserved = append(preserved, e.TagNamePreserveCase())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return lower, preserved
}

// TestNoSVGTagNameAdjustment. Three spellings of one element: a parser reports
// the canonical name for all three, TagName reports the lower-cased one for all
// three, and TagNamePreserveCase reports whatever was typed.
func TestNoSVGTagNameAdjustment(t *testing.T) {
	for _, tt := range []struct {
		doc, canonical, preserved string
	}{
		{`<svg><linearGradient/></svg>`, "linearGradient", "linearGradient"},
		{`<svg><LINEARGRADIENT/></svg>`, "linearGradient", "LINEARGRADIENT"},
		{`<svg><lineargradient/></svg>`, "linearGradient", "lineargradient"},
		{`<svg><textPath/></svg>`, "textPath", "textPath"},
		{`<svg><TEXTPATH/></svg>`, "textPath", "TEXTPATH"},
		{`<svg><clipPath/></svg>`, "clipPath", "clipPath"},
		{`<svg><foreignObject>x</foreignObject></svg>`, "foreignObject", "foreignObject"},
		{`<svg><FOREIGNOBJECT>x</FOREIGNOBJECT></svg>`, "foreignObject", "FOREIGNOBJECT"},
	} {
		parsed := svgNames(t, tt.doc)
		if len(parsed) != 1 || parsed[0] != tt.canonical {
			t.Errorf("%s: the parser reports %v, want [%s]", tt.doc, parsed, tt.canonical)
			continue
		}

		lower, preserved := reported(t, tt.doc)
		if len(lower) != 1 || len(preserved) != 1 {
			t.Errorf("%s: reported %v / %v", tt.doc, lower, preserved)
			continue
		}
		if lower[0] != strings.ToLower(tt.canonical) {
			t.Errorf("%s: TagName is %q, want the lower-cased name %q",
				tt.doc, lower[0], strings.ToLower(tt.canonical))
		}
		if preserved[0] != tt.preserved {
			t.Errorf("%s: TagNamePreserveCase is %q, want the source spelling %q",
				tt.doc, preserved[0], tt.preserved)
		}

		// The point of the file: neither is the canonical name unless the page
		// happened to write it that way.
		if tt.preserved != tt.canonical && preserved[0] == tt.canonical {
			t.Errorf("%s: TagNamePreserveCase now gives the canonical name, so the "+
				"adjustment is applied and the documentation can be simplified", tt.doc)
		}
	}
}

// TestHTMLTagNamesAgree, so the divergence above is known to be about foreign
// content rather than about case in general.
func TestHTMLTagNamesAgree(t *testing.T) {
	for _, tt := range []struct{ doc, want, preserved string }{
		{`<P>x</P>`, "p", "P"},
		{`<DiV>x</DiV>`, "div", "DiV"},
		{`<div>x</div>`, "div", "div"},
		{`<CUSTOM-Element>x</CUSTOM-Element>`, "custom-element", "CUSTOM-Element"},
	} {
		root, err := html.Parse(strings.NewReader(tt.doc))
		if err != nil {
			t.Fatal(err)
		}
		var parsed []string
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && (n.Data == tt.want) {
				parsed = append(parsed, n.Data)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		if len(parsed) != 1 {
			t.Errorf("%s: the parser reports %v", tt.doc, parsed)
		}

		var lower, preserved string
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.want, func(e *lolhtml.Element) error {
				lower, preserved = e.TagName(), e.TagNamePreserveCase()
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if lower != tt.want || preserved != tt.preserved {
			t.Errorf("%s: reported %q / %q, want %q / %q",
				tt.doc, lower, preserved, tt.want, tt.preserved)
		}
	}
}

// TestTheOutputKeepsTheSourceSpelling, which is why the divergence costs nothing
// on the way out: a browser adjusts it on the way in.
func TestTheOutputKeepsTheSourceSpelling(t *testing.T) {
	const doc = `<svg><LINEARGRADIENT/></svg>`

	out, err := lolhtml.RewriteString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if out != doc {
		t.Errorf("passthrough changed the spelling: %q", out)
	}
	// And the parser still sees the canonical element.
	if names := svgNames(t, out); len(names) != 1 || names[0] != "linearGradient" {
		t.Errorf("the output parses to %v", names)
	}

	// A rewrite can normalise it if it wants to.
	out, err = lolhtml.RewriteString(doc,
		lolhtml.OnElement("svg > *", func(e *lolhtml.Element) error {
			return e.SetTagName("linearGradient")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<svg><linearGradient/></svg>` {
		t.Errorf("SetTagName produced %q", out)
	}
}

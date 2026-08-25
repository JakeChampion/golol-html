package differential

// An HTML tag name inside an <svg> ends the svg. The parser's foreign-content rules
// break out when they meet one of a fixed list of names, and everything after it in
// the source is page content rather than image content - which matters most to a
// rewrite that inlines a file into an <svg>, because a file holding one <p> puts the
// rest of itself in the document body.
//
// The library's two views of that disagree. NamespaceURI follows the break-out and
// reports HTML for what comes after, while the selector engine does not: "svg >
// circle" still matches a circle the tree puts outside the svg. So neither answers
// "is this still inside the image".

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// breakOut are the names the HTML specification breaks out of foreign content on.
// The list is asserted against the parser below rather than trusted.
var breakOut = []string{
	"b", "big", "blockquote", "body", "br", "center", "code", "dd", "div", "dl",
	"dt", "em", "embed", "h1", "h2", "h3", "h4", "h5", "h6", "head", "hr", "i",
	"img", "li", "listing", "menu", "meta", "nobr", "ol", "p", "pre", "ruby", "s",
	"small", "span", "strong", "strike", "sub", "sup", "table", "tt", "u", "ul",
	"var",
}

// staysIn are names that look like they should break out and do not, so the list
// above reads as a list rather than as "HTML tags".
var staysIn = []string{"font", "title", "desc", "a", "script", "style", "g", "text"}

// insideSVG reports whether the tree puts an element of this name inside an svg.
func insideSVG(t *testing.T, doc, name string) bool {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var found bool
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, in bool) {
		if n.Type == html.ElementNode {
			if n.Data == name && in {
				found = true
			}
			if n.Data == "svg" {
				in = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, in)
		}
	}
	walk(root, false)
	return found
}

// TestAnHTMLTagNameEndsTheSvg, over the whole list, with a circle after it standing
// for the rest of a file.
func TestAnHTMLTagNameEndsTheSvg(t *testing.T) {
	for _, name := range breakOut {
		doc := `<svg><rect/><` + name + `>x</` + name + `><circle/></svg>`
		if insideSVG(t, doc, "circle") {
			t.Errorf("<%s>: the circle is still inside the svg, so this name does not break out", name)
		}
		// The rect before it is not affected: the file up to that point is an image.
		if !insideSVG(t, doc, "rect") {
			t.Errorf("<%s>: the rect before it left the svg too", name)
		}
	}
	for _, name := range staysIn {
		doc := `<svg><rect/><` + name + `>x</` + name + `><circle/></svg>`
		if !insideSVG(t, doc, "circle") {
			t.Errorf("<%s>: the circle left the svg, so this name does break out", name)
		}
	}
}

// TestAFontBreaksOutOnlyWithAPresentationAttribute, which is the one conditional
// entry and the reason the list cannot be "HTML tag names".
func TestAFontBreaksOutOnlyWithAPresentationAttribute(t *testing.T) {
	for _, tc := range []struct {
		tag   string
		wants bool // wants the circle still inside
	}{
		{`<font>`, true},
		{`<font class="x">`, true},
		{`<font id="x">`, true},
		{`<font color="red">`, false},
		{`<font face="a">`, false},
		{`<font size="1">`, false},
		{`<font COLOR="red">`, false},
	} {
		doc := `<svg><rect/>` + tc.tag + `a</font><circle/></svg>`
		if got := insideSVG(t, doc, "circle"); got != tc.wants {
			t.Errorf("%s: circle inside the svg = %v, want %v", tc.tag, got, tc.wants)
		}
	}
}

// TestTheLibraryFollowsTheBreakOutForNamespacesAndNotForSelectors, which is the part
// a rewrite has to know: the two answers come from the same document at the same
// moment and they do not agree.
func TestTheLibraryFollowsTheBreakOutForNamespacesAndNotForSelectors(t *testing.T) {
	const doc = `<svg><rect/><b>x</b><circle/></svg>`
	if insideSVG(t, doc, "circle") {
		t.Fatal("the tree puts the circle inside the svg, so there is nothing to compare")
	}
	var namespaces []string
	var descendant, child int
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("circle", func(e *lolhtml.Element) error {
			namespaces = append(namespaces, e.NamespaceURI())
			return nil
		}),
		lolhtml.OnElement("svg circle", func(*lolhtml.Element) error { descendant++; return nil }),
		lolhtml.OnElement("svg > circle", func(*lolhtml.Element) error { child++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 1 || namespaces[0] != lolhtml.NamespaceHTML {
		t.Errorf("the circle reports %v, want the HTML namespace", namespaces)
	}
	if descendant != 1 || child != 1 {
		t.Errorf("svg circle matched %d and svg > circle matched %d, want 1 each: the selector keeps the svg open",
			descendant, child)
	}
	// The elements before the break-out are in the SVG namespace, so the report is
	// about the break-out and not about namespaces being wrong everywhere.
	var rect string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("rect", func(e *lolhtml.Element) error {
		rect = e.NamespaceURI()
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if rect != lolhtml.NamespaceSVG {
		t.Errorf("the rect reports %q, want the SVG namespace", rect)
	}
}

// TestWhatComesAfterIsInTheDocument, which is what makes this a hazard for an
// inliner rather than a curiosity: the tail of the file is page content.
func TestWhatComesAfterIsInTheDocument(t *testing.T) {
	// A file inlined into a page, with a paragraph in the middle of it.
	const page = `<div id="wrap"><svg><rect/><p>oops</p><circle/></svg></div>`
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	// The circle is inside the wrapper - the page is not damaged - but it is a
	// sibling of the svg rather than part of it, and it is an HTML element.
	var circle *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "circle" {
			circle = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if circle == nil {
		t.Fatal("no circle in the tree")
	}
	if circle.Namespace != "" {
		t.Errorf("the circle is in the %q namespace, want the HTML one", circle.Namespace)
	}
	if circle.Parent == nil || circle.Parent.Data != "div" {
		t.Errorf("the circle's parent is %v, want the wrapper div", circle.Parent)
	}
}

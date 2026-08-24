package differential

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// An element whose end tag the source leaves out is a complete element in a
// parser's tree, and it is not a complete element to a streaming rewriter. This
// is the measurement that says which of the two is the page's meaning: the tree
// is, and the rewriter's span is wider.
func TestOmittedEndTagsStillMakeSeparateElements(t *testing.T) {
	tests := []struct {
		doc   string
		tag   string
		texts []string
	}{
		{`<ul><li>a<li>b<li>c</ul>`, "li", []string{"a", "b", "c"}},
		{`<ul><li>a</li><li>b</li><li>c</li></ul>`, "li", []string{"a", "b", "c"}},
		{`<table><tr><td>a<td>b</table>`, "td", []string{"a", "b"}},
		{`<select><option>a<option>b</select>`, "option", []string{"a", "b"}},
		{`<p>a<p>b`, "p", []string{"a", "b"}},
	}
	for _, tt := range tests {
		root, err := html.Parse(strings.NewReader(tt.doc))
		if err != nil {
			t.Fatalf("%q: %v", tt.doc, err)
		}
		var got []string
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == tt.tag {
				got = append(got, textOf(n))
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		if strings.Join(got, "|") != strings.Join(tt.texts, "|") {
			t.Errorf("%q: tree has %q, want %q", tt.doc, got, tt.texts)
		}

		// The rewriter reports the same number of elements - the start tags are
		// all there - so the disagreement is about where each one ends, not
		// about how many there are.
		starts := 0
		if _, err := lolhtml.RewriteString(tt.doc, lolhtml.OnElement(tt.tag, func(*lolhtml.Element) error {
			starts++
			return nil
		})); err != nil {
			t.Fatalf("%q: %v", tt.doc, err)
		}
		if starts != len(tt.texts) {
			t.Errorf("%q: rewriter saw %d <%s>, tree has %d", tt.doc, starts, tt.tag, len(tt.texts))
		}
	}
}

// And the consequence, stated against the oracle: removing the first list item
// should leave the other two, and it removes them.
func TestRemovingAnImplicitlyClosedItemRemovesItsSiblings(t *testing.T) {
	const doc = `<ul><li>a<li>b<li>c</ul>`
	n := 0
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		if n++; n == 1 {
			e.Remove()
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	root, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "li" {
			left = append(left, textOf(node))
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	// What a tree-based edit would leave, and what this leaves.
	if len(left) != 0 {
		t.Errorf("output %q still has items %q; this test records that it has none", out, left)
	}
}

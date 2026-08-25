package differential

// Structural selectors are computed against the tokens. The engine pushes on a start
// tag and pops on an end tag, and HTML lets a document leave most end tags out - so
// in a list written the ordinary way, the second item is not a sibling of the first,
// it is inside it. Every selector that depends on position or parentage answers a
// different question from the one it looks like:
//
//	<ul><li>a<li>b<li>c</ul>
//
//	ul > li           1 of 3
//	li > li           2, which the tree never has
//	li:first-child    all 3
//	li:nth-child(2)   none
//
// Same document with </li> spelled: 3, 0, 1, 1. So the answers are right on the pages
// written one way and wrong on the pages written the other, and a document that mixes
// the two is partly right, which is worse.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// treeCount counts the elements of a name whose parent is of a given name, straight
// from the tree, so a claim about parentage is checked and not assumed.
func treeCount(t *testing.T, doc, parent, child string) int {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var n int
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == child &&
			node.Parent != nil && node.Parent.Data == parent {
			n++
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return n
}

// matches counts what a selector fires on.
func matches(t *testing.T, doc, selector string) int {
	t.Helper()
	var n int
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(selector, func(*lolhtml.Element) error {
		n++
		return nil
	})); err != nil {
		t.Fatalf("%q on %q: %v", selector, doc, err)
	}
	return n
}

// TestAnOmittedEndTagMakesSiblingsIntoChildren, which is the mechanism: the tree has
// three items in one list and the selectors see a list of one holding a chain.
func TestAnOmittedEndTagMakesSiblingsIntoChildren(t *testing.T) {
	const implied = `<ul><li>a<li>b<li>c</ul>`
	const closed = `<ul><li>a</li><li>b</li><li>c</li></ul>`

	// The tree agrees about both documents: three items in the list, none nested.
	for _, doc := range []string{implied, closed} {
		if got := treeCount(t, doc, "ul", "li"); got != 3 {
			t.Fatalf("%q: the tree has %d items in the list, want 3", doc, got)
		}
		if got := treeCount(t, doc, "li", "li"); got != 0 {
			t.Fatalf("%q: the tree has %d items inside items, want 0", doc, got)
		}
	}

	for _, tc := range []struct {
		selector             string
		wantClosed, wantOpen int
	}{
		{"ul > li", 3, 1},
		{"li > li", 0, 2},
		{"li li", 0, 2},
		{"li:first-child", 1, 3},
		{"li:nth-child(1)", 1, 3},
		{"li:nth-child(2)", 1, 0},
		{"li:nth-child(3)", 1, 0},
		{"ul > li:nth-child(2)", 1, 0},
		{"li:first-of-type", 1, 3},
		{"li:nth-of-type(2)", 1, 0},
	} {
		if got := matches(t, closed, tc.selector); got != tc.wantClosed {
			t.Errorf("%q with end tags matched %d, want %d", tc.selector, got, tc.wantClosed)
		}
		if got := matches(t, implied, tc.selector); got != tc.wantOpen {
			t.Errorf("%q without end tags matched %d, want %d", tc.selector, got, tc.wantOpen)
		}
	}
}

// TestTheCountIsOfWhateverTheTokensNested, which is why a nesting-aware answer is not
// merely off by one: with an element inside each item, the second item is the second
// child of the first item.
func TestTheCountIsOfWhateverTheTokensNested(t *testing.T) {
	const implied = `<ul><li><img a><li><img b></ul>`
	const closed = `<ul><li><img a></li><li><img b></li></ul>`

	if got := treeCount(t, implied, "ul", "li"); got != 2 {
		t.Fatalf("the tree has %d items, want 2", got)
	}
	for _, tc := range []struct {
		selector             string
		wantClosed, wantOpen int
	}{
		{"ul > li", 2, 1},
		{"li > li", 0, 1},
		{"ul > li:nth-child(2)", 1, 0},
		// This one matches in both documents and means something different in each:
		// the second item of the list, and the second child of the first item.
		{"li:nth-child(2)", 1, 1},
	} {
		if got := matches(t, closed, tc.selector); got != tc.wantClosed {
			t.Errorf("%q with end tags matched %d, want %d", tc.selector, got, tc.wantClosed)
		}
		if got := matches(t, implied, tc.selector); got != tc.wantOpen {
			t.Errorf("%q without end tags matched %d, want %d", tc.selector, got, tc.wantOpen)
		}
	}
}

// TestItIsNotOnlyLists: paragraphs, table cells and definition lists are the same
// shape, and paragraphs are the most common markup there is.
func TestItIsNotOnlyLists(t *testing.T) {
	for _, tc := range []struct {
		name, doc, selector string
		want, tree          int
	}{
		{"paragraphs", `<div><p>a<p>b<p>c</div>`, "div > p", 1, 3},
		{"paragraphs nested", `<div><p>a<p>b<p>c</div>`, "p > p", 2, 0},
		{"cells", `<table><tr><td>a<td>b</table>`, "tr > td", 1, 2},
		{"cells nested", `<table><tr><td>a<td>b</table>`, "td > td", 1, 0},
		// The second row's token parent is the cell that was still open, so it is
		// not even a row inside a row: it is a row inside a cell.
		{"rows", `<table><tr><td>a<tr><td>b</table>`, "td > tr", 1, 0},
		{"definitions", `<dl><dt>a<dd>b<dt>c</dl>`, "dl > dd", 0, 1},
	} {
		if got := matches(t, tc.doc, tc.selector); got != tc.want {
			t.Errorf("%s: %q matched %d, want %d", tc.name, tc.selector, got, tc.want)
		}
		parts := strings.Split(tc.selector, " > ")
		if got := treeCount(t, tc.doc, parts[0], parts[1]); got != tc.tree {
			t.Errorf("%s: the tree has %d %s under %s, want %d", tc.name, got, parts[1], parts[0], tc.tree)
		}
	}
}

// TestAMixedDocumentIsPartlyRight, which is the failure mode that gets shipped: the
// rewrite works on the items whose end tags are there.
func TestAMixedDocumentIsPartlyRight(t *testing.T) {
	const mixed = `<ul><li>a</li><li>b<li>c</ul>`
	if got := treeCount(t, mixed, "ul", "li"); got != 3 {
		t.Fatalf("the tree has %d items, want 3", got)
	}
	for _, tc := range []struct {
		selector string
		want     int
	}{
		{"ul > li", 2},
		{"li > li", 1},
		{"li:nth-child(2)", 1},
		{"li:first-child", 2},
	} {
		if got := matches(t, mixed, tc.selector); got != tc.want {
			t.Errorf("%q matched %d, want %d", tc.selector, got, tc.want)
		}
	}
}

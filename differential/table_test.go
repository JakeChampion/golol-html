package differential

// Foster parenting: content that cannot be inside a table is moved out of it by
// a parser, and cannot be moved by a streaming rewriter that has no tree. So the
// stream and the tree disagree about where that content is, and the disagreement
// is invisible in the output - which is byte-identical, because a browser reading
// it foster-parents the content out again.
//
// It matters when a rewrite acts on the table: removing it removes content a
// browser keeps, and collecting its text collects text that is not in it.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// fosterCases are the shapes where a parser moves content before the table.
var fosterCases = []struct {
	name  string
	doc   string
	stray string
}{
	{"text before the first row", `<table>stray<tr><td>a</table>`, "stray"},
	{"text inside a row", `<table><tr>stray<td>a</table>`, "stray"},
	{"inline element before the first row", `<table><b>stray</b><tr><td>a</table>`, "stray"},
	{"text after a cell", `<table><tr><td>a</td>stray</tr></table>`, "stray"},
	{"block element before the first row", `<table><div>stray</div><tr><td>a</table>`, "stray"},
}

// TestFosteredContentIsOutsideTheTableInTheTree is the fact underneath: every one
// of these leaves the stray content outside the table.
func TestFosteredContentIsOutsideTheTableInTheTree(t *testing.T) {
	for _, c := range fosterCases {
		root, err := html.Parse(strings.NewReader(c.doc))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		inside, outside := 0, 0
		var walk func(*html.Node, bool)
		walk = func(n *html.Node, inTable bool) {
			if n.Type == html.TextNode && strings.Contains(n.Data, c.stray) {
				if inTable {
					inside++
				} else {
					outside++
				}
			}
			for k := n.FirstChild; k != nil; k = k.NextSibling {
				walk(k, inTable || (n.Type == html.ElementNode && n.Data == "table"))
			}
		}
		walk(root, false)
		if inside != 0 || outside != 1 {
			t.Errorf("%s: %d inside the table, %d outside; expected it to be fostered out",
				c.name, inside, outside)
		}
	}
}

// TestTheRewriterReportsFosteredContentInsideTheTable is the other half. A text
// handler on the table sees it, which is what makes "the text of this table" the
// wrong question to ask this way.
func TestTheRewriterReportsFosteredContentInsideTheTable(t *testing.T) {
	for _, c := range fosterCases {
		var seen []string
		out, err := lolhtml.RewriteString(c.doc,
			lolhtml.OnText("table", func(tc *lolhtml.TextChunk) error {
				if s := strings.TrimSpace(tc.Text()); s != "" {
					seen = append(seen, s)
				}
				return nil
			}))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out != c.doc {
			t.Errorf("%s: passthrough changed the document", c.name)
		}
		found := false
		for _, s := range seen {
			if strings.Contains(s, c.stray) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: a text handler on the table saw %v, and the fostered "+
				"text is expected to be among them", c.name, seen)
		}
	}
}

// TestRemovingATableTakesFosteredContentWithIt is the consequence worth knowing:
// the same edit keeps the content in a tree and loses it in a stream.
func TestRemovingATableTakesFosteredContentWithIt(t *testing.T) {
	const doc = `<p>before</p><table>stray<tr><td>a</table><p>after</p>`

	stream, err := lolhtml.RewriteString(doc, lolhtml.OnElement("table", func(e *lolhtml.Element) error {
		e.Remove()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stream, "stray") {
		t.Errorf("the stream kept the fostered text: %q", stream)
	}

	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var drop func(*html.Node)
	drop = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && c.Data == "table" {
				n.RemoveChild(c)
			} else {
				drop(c)
			}
			c = next
		}
	}
	drop(root)
	var tree strings.Builder
	if err := html.Render(&tree, root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tree.String(), "stray") {
		t.Fatalf("the tree edit lost it too, so this test is not measuring the "+
			"difference: %q", tree.String())
	}
}

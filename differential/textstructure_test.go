package differential

// Inserting text can change the tree without inserting any markup.
//
// ContentType.Text guarantees that nothing it writes becomes a tag: the sequence
// of tags in the output is the sequence that went in, which properties/ checks
// over every generated document. The tree a browser builds from that output is a
// different question, because tree construction responds to the presence of text.
//
// The case is a formatting element misnested across a block boundary. Adding one
// character inside the block makes the parser reconstruct the formatting element
// there, so the tree gains an element that the markup does not contain.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// treeShape renders the parsed tree's elements, without text or attributes.
func treeShape(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			sb.WriteString("<" + n.Data + ">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			sb.WriteString("</" + n.Data + ">")
		}
	}
	walk(root)
	s := sb.String()
	s = strings.TrimPrefix(s, "<html><head></head><body>")
	return strings.TrimSuffix(s, "</body></html>")
}

// tagSequence is what the rewriter reports, which is the level the library is
// answerable for.
func tagSequence(t *testing.T, doc string) string {
	t.Helper()
	var sb strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		sb.WriteString("<" + e.TagName() + ">")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// TestTextCanChangeTheTreeWithoutChangingTheTags is the minimal case, and both
// halves of it matter: the tags are the same and the tree is not.
func TestTextCanChangeTheTreeWithoutChangingTheTags(t *testing.T) {
	const empty = `<p><a><div></div></a></p>`
	const withText = `<p><a><div>x</div></a></p>`

	if a, b := tagSequence(t, empty), tagSequence(t, withText); a != b {
		t.Fatalf("the tags differ (%s against %s); this test needs two documents "+
			"with the same tags", a, b)
	}

	before, after := treeShape(t, empty), treeShape(t, withText)
	if before == after {
		t.Fatalf("both trees are %s; the reconstruction this test is about did not "+
			"happen", before)
	}
	if want := `<p><a></a></p><div></div><p></p>`; before != want {
		t.Errorf("without text the tree is %s, want %s", before, want)
	}
	// The <a> reappears inside the div: an element in the tree that is not in
	// the markup.
	if want := `<p><a></a></p><div><a></a></div><p></p>`; after != want {
		t.Errorf("with text the tree is %s, want %s", after, want)
	}
}

// And through the library: appending text as Text does the same thing, which is
// what makes it worth documenting rather than filing as a parser curiosity.
func TestAppendingTextAsTextCanChangeTheTree(t *testing.T) {
	const doc = `<p><a><div></div></a></p>`

	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		return e.Append("x", lolhtml.Text)
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Nothing was added to the markup but a character.
	if a, b := tagSequence(t, doc), tagSequence(t, out); a != b {
		t.Errorf("the tags changed from %s to %s", a, b)
	}
	if strings.Count(out, "<") != strings.Count(doc, "<") {
		t.Errorf("a tag was added: %q", out)
	}
	// And the tree gained an element.
	if treeShape(t, doc) == treeShape(t, out) {
		t.Errorf("the tree did not change: %s", treeShape(t, out))
	}
}

// The shapes where it does not happen, so the rule is not read as "text changes
// the tree".
func TestTextDoesNotChangeAWellNestedTree(t *testing.T) {
	pairs := []struct{ empty, withText string }{
		{`<div><p></p></div>`, `<div><p>x</p></div>`},
		{`<a><span></span></a>`, `<a><span>x</span></a>`},
		{`<b><i></i></b>`, `<b><i>x</i></b>`},
		// Misnested but with no formatting element to reconstruct.
		{`<div><div></div></div>`, `<div><div>x</div></div>`},
	}
	for _, p := range pairs {
		if before, after := treeShape(t, p.empty), treeShape(t, p.withText); before != after {
			t.Errorf("%q and %q have different trees: %s against %s",
				p.empty, p.withText, before, after)
		}
	}
}

package differential

// Character references inside a comment.
//
// What matters for a comment built by hand is whether escaping the closing
// sequence still prevents it from closing the comment. It does. What escaping
// costs is a separate question, and the two libraries do not even agree on how a
// comment's text should be reported, so the advice does not rest on that.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// TestTheTwoLibrariesReportCommentTextDifferently records a difference in
// reporting, not a claim about what HTML means.
//
// golol-html gives a comment's text as the source says it, like everything else
// it reports: <!-- caf&eacute; --> is " caf&eacute; ". x/net/html decodes the
// reference into its Data field, so the same comment reads " café ".
//
// Which of those a browser agrees with is not something this test can settle, and
// the advice on building a comment by hand does not rest on it: what matters
// there is whether an escaped closing sequence still closes the comment, which is
// the test below.
func TestTheTwoLibrariesReportCommentTextDifferently(t *testing.T) {
	const doc = `<!-- caf&eacute; -->`

	var fromRewriter string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
		fromRewriter = c.Text()
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if fromRewriter != " caf&eacute; " {
		t.Errorf("the rewriter reported %q, want the source unchanged", fromRewriter)
	}

	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var fromTree string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.CommentNode && fromTree == "" {
			fromTree = n.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if fromTree != " café " {
		t.Errorf("the tree reported %q; this test records that it decodes the "+
			"reference where the rewriter does not", fromTree)
	}
}

// And the escaped closing sequence really does stay inside the comment, which is
// what makes escaping safe even though it is wrong.
func TestAnEscapedClosingSequenceDoesNotCloseTheComment(t *testing.T) {
	const doc = `<div><!-- --&gt;<img src=x> --></div>`
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	imgs, comments := 0, 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.CommentNode:
			comments++
		case n.Type == html.ElementNode && n.Data == "img":
			imgs++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if imgs != 0 {
		t.Errorf("the escaped sequence let %d img elements out", imgs)
	}
	if comments != 1 {
		t.Errorf("got %d comments, want 1", comments)
	}
}

// The unescaped one does close it, in both spellings, which is what there is to
// protect against.
func TestBothClosingSequencesEndAComment(t *testing.T) {
	for _, doc := range []string{
		`<div><!-- --><img src=x> --></div>`,
		`<div><!-- --!><img src=x> --></div>`,
	} {
		root, err := html.Parse(strings.NewReader(doc))
		if err != nil {
			t.Fatal(err)
		}
		imgs := 0
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				imgs++
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		if imgs != 1 {
			t.Errorf("%q produced %d img elements, want 1", doc, imgs)
		}
	}
}

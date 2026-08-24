package differential

// Where an insertion lands, according to a parser that builds the tree.
//
// A rewriter inserts at a position in a byte stream; what matters to the page is
// which element the inserted node ends up under. For anything that has to be in
// the head - a robots meta, a base, a preload - those are not the same question,
// because <head> is optional in HTML and a parser creates one.
//
// x/net/html is the oracle: these are claims about where a browser would put the
// content, and the rewriter cannot answer them itself.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

const meta = `<meta name="robots" content="noindex">`

// parentOfMeta returns the tag name of the element the meta ended up under.
func parentOfMeta(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	var found string
	var walk func(n *html.Node, parent string)
	walk = func(n *html.Node, parent string) {
		if n.Type == html.ElementNode && n.Data == "meta" && found == "" {
			found = parent
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			name := parent
			if n.Type == html.ElementNode {
				name = n.Data
			}
			walk(c, name)
		}
	}
	walk(root, "")
	return found
}

// TestInsertingBeforeBodyLandsInTheHead is the claim the noindex example relies
// on: with no head element in the source, the start of <body> is the end of the
// implied head, so inserting before it puts the content in the head a parser
// builds.
func TestInsertingBeforeBodyLandsInTheHead(t *testing.T) {
	for _, doc := range []string{
		`<html><body>x</body></html>`,
		`<!DOCTYPE html><html><body>x</body></html>`,
		`<html lang="en"><body class="a">x</body></html>`,
		`<!DOCTYPE html><html><body><p>x</p></body></html>`,
	} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("body", func(e *lolhtml.Element) error {
				return e.Before(meta, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if got := parentOfMeta(t, out); got != "head" {
			t.Errorf("%q -> %q: the meta ended up under %q, want head", doc, out, got)
		}
	}
}

// TestInsertingAtTheEndOfHeadLandsInTheHead, which is the case where the
// document says where its head is.
func TestInsertingAtTheEndOfHeadLandsInTheHead(t *testing.T) {
	for _, doc := range []string{
		`<html><head></head><body>x</body></html>`,
		`<html><head><title>t</title></head><body>x</body></html>`,
		`<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>x</body></html>`,
	} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("head", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(end *lolhtml.EndTag) error {
					return end.Before(meta, lolhtml.HTML)
				})
			}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if got := parentOfMeta(t, out); got != "head" {
			t.Errorf("%q -> %q: the meta ended up under %q, want head", doc, out, got)
		}
	}
}

// TestInsertingInsideBodyStaysInTheBody. The complement, so that the tests above
// are known to be about position and not about metas being hoisted from anywhere.
func TestInsertingInsideBodyStaysInTheBody(t *testing.T) {
	out, err := lolhtml.RewriteString(`<html><body><p>x</p></body></html>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.Before(meta, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got := parentOfMeta(t, out); got != "body" {
		t.Errorf("%q: the meta ended up under %q, want body", out, got)
	}
}

// TestInsertingBeforeTheFirstElementOfAFragment is nearly the answer for a
// document with no head and no body, and not quite. Where it lands depends on
// what came before the element: leading text opens the body, so the insertion is
// then in the body rather than the head.
//
// Which, with the fact that at the first element it is not yet known whether the
// fragment already has a robots meta further on, is why examples/gip/noindex
// declines this position rather than using it.
func TestInsertingBeforeTheFirstElementOfAFragment(t *testing.T) {
	for _, tt := range []struct {
		doc, want string
	}{
		{`<p>x</p>`, "head"},
		{`<div><p>x</p></div>`, "head"},
		{`<!DOCTYPE html><p>x</p>`, "head"},
		{`<!-- c --><p>x</p>`, "head"},

		// Text before the element has already opened the body.
		{`text then <p>x</p>`, "body"},
		{` <p>x</p>`, "head"}, // whitespace alone does not
	} {
		first := true
		out, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				if !first {
					return nil
				}
				first = false
				return e.Before(meta, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%q: %v", tt.doc, err)
		}
		if got := parentOfMeta(t, out); got != tt.want {
			t.Errorf("%q -> %q: the meta ended up under %q, want %q",
				tt.doc, out, got, tt.want)
		}
	}
}

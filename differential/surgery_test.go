package differential

// The same edit, done two ways.
//
// A streaming rewrite and tree surgery are different machines: one writes bytes as they
// pass, the other builds a document, changes it and writes it out. Where they agree, a
// caller can reason about a rewrite as if it were surgery - which is how most people think
// about editing HTML. Where they cannot, the documentation says why: an element whose end
// tag the document left out is bigger here than it is in a tree, so anything positioned at
// its end lands somewhere else.
//
// This file does both edits and compares the trees, which is a stronger check than
// comparing a document with itself: it says what the answer should be rather than only
// that nothing moved.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// surgeryShapes are the documents the two machines are compared on. Three of them close
// every element and three do not, which is the line the comparison is about.
var surgeryShapes = []string{
	`<div><p>x</p></div>`,
	`<table><tr><td><div>a</div></td></tr></table>`,
	`<div><b>bold</b></div>`,
	`<p>a<img src="x">b</p>`,
	`<ul><li>a<li>b</ul>`,
	`<ul><li><div>a</div><li><div>b</div></ul>`,
}

// canonicalDoc is the document a parser builds, written back out. Comparing this rather
// than the node structure is what makes the two machines comparable: removing an element
// between two runs of text leaves one text node here and two in a tree, and both mean the
// same document.
func canonicalDoc(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		t.Fatalf("rendering %q: %v", doc, err)
	}
	return b.String()
}

// surgeryShape renders the tree for a failure message, where the node structure is what a reader
// wants to see.
func surgeryShape(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	return shapeOfNode(root, 0)
}

// bySurgery parses doc, applies fn to every element of the given name, and returns the
// result as a document.
func bySurgery(t *testing.T, doc, name string, fn func(*html.Node)) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		kids := n.FirstChild
		if n.Type == html.ElementNode && n.Data == name {
			fn(n)
		}
		for c := kids; c != nil; {
			next := c.NextSibling
			walk(c)
			c = next
		}
	}
	walk(root)
	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return canonicalDoc(t, b.String())
}

// byStreaming applies the rewrite and returns the result as a document.
func byStreaming(t *testing.T, doc string, opts ...lolhtml.Option) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, opts...)
	if err != nil {
		t.Fatalf("rewriting %q: %v", doc, err)
	}
	return canonicalDoc(t, out)
}

// shapeOfNode renders a node and its descendants, indented, for failure messages.
func shapeOfNode(n *html.Node, d int) string {
	var b strings.Builder
	switch n.Type {
	case html.ElementNode:
		var attrs []string
		for _, a := range n.Attr {
			attrs = append(attrs, a.Key+"="+a.Val)
		}
		sort.Strings(attrs)
		fmt.Fprintf(&b, "%s%s[%s]\n", strings.Repeat(" ", d), n.Data, strings.Join(attrs, " "))
	case html.TextNode:
		if s := strings.TrimSpace(n.Data); s != "" {
			fmt.Fprintf(&b, "%s#%s\n", strings.Repeat(" ", d), s)
		}
	case html.CommentNode:
		fmt.Fprintf(&b, "%s<!--%s-->\n", strings.Repeat(" ", d), n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(shapeOfNode(c, d+1))
	}
	return b.String()
}

// edits are the four that the two machines agree about.
var edits = []struct {
	name    string
	on      string
	stream  func() []lolhtml.Option
	surgery func(*html.Node)
}{
	{
		name: "add a class to every div",
		on:   "div",
		stream: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.SetAttribute("class", "m")
			})}
		},
		surgery: func(n *html.Node) {
			n.Attr = append(n.Attr, html.Attribute{Key: "class", Val: "m"})
		},
	},
	{
		name: "insert a comment before every image",
		on:   "img",
		stream: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("img", func(e *lolhtml.Element) error {
				return e.Before("<!--m-->", lolhtml.HTML)
			})}
		},
		surgery: func(n *html.Node) {
			n.Parent.InsertBefore(&html.Node{Type: html.CommentNode, Data: "m"}, n)
		},
	},
	{
		name: "rename b to strong",
		on:   "b",
		stream: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
				return e.SetTagName("strong")
			})}
		},
		surgery: func(n *html.Node) {
			n.Data, n.DataAtom = "strong", atom.Strong
		},
	},
	{
		name: "remove every image",
		on:   "img",
		stream: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("img", func(e *lolhtml.Element) error {
				e.Remove()
				return nil
			})}
		},
		surgery: func(n *html.Node) {
			n.Parent.RemoveChild(n)
		},
	},
}

// TestAStreamingEditEqualsTheSameSurgery, over every shape.
func TestAStreamingEditEqualsTheSameSurgery(t *testing.T) {
	for _, e := range edits {
		for _, doc := range surgeryShapes {
			streamed := byStreaming(t, doc, e.stream()...)
			cut := bySurgery(t, doc, e.on, e.surgery)
			if streamed != cut {
				t.Errorf("%s on %q\nstreamed: %q\nsurgery:  %q\nstreamed tree:\n%s",
					e.name, doc, streamed, cut, surgeryShape(t, streamed))
			}
		}
	}
}

// TestReplacingContentAgreesOnlyWhereTheEndTagIsThere, which is the documented line: an
// element whose end tag the document left out reaches to the enclosing element's end, so
// replacing its content takes the siblings with it.
func TestReplacingContentAgreesOnlyWhereTheEndTagIsThere(t *testing.T) {
	stream := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("li", func(e *lolhtml.Element) error {
			return e.SetInnerContent("x", lolhtml.Text)
		})}
	}
	surgery := func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			n.RemoveChild(c)
			c = next
		}
		n.AppendChild(&html.Node{Type: html.TextNode, Data: "x"})
	}

	// Closed items: the two machines agree.
	for _, doc := range []string{
		`<ul><li>a</li><li>b</li></ul>`,
		`<ul><li><div>a</div></li><li><div>b</div></li></ul>`,
	} {
		if streamed, cut := byStreaming(t, doc, stream()...), bySurgery(t, doc, "li", surgery); streamed != cut {
			t.Errorf("on %q the two machines disagree\nstreamed: %q\nsurgery:  %q", doc, streamed, cut)
		}
	}

	// Implied end tags: they do not, and the streaming one loses the later items.
	for _, doc := range []string{
		`<ul><li>a<li>b</ul>`,
		`<ul><li><div>a</div><li><div>b</div></ul>`,
	} {
		streamed, cut := byStreaming(t, doc, stream()...), bySurgery(t, doc, "li", surgery)
		if streamed == cut {
			t.Errorf("on %q the two machines agree, which the end-tag rule says they cannot", doc)
			continue
		}
		if strings.Count(streamed, "<li>") >= strings.Count(cut, "<li>") {
			t.Errorf("on %q the streaming edit kept %d items and the surgery %d; the "+
				"streaming one is meant to lose them\nstreamed: %q", doc,
				strings.Count(streamed, "<li>"), strings.Count(cut, "<li>"), streamed)
		}
	}
}

// TestTheGuardMakesTheEndPositionAgree: the same edit, positioned at the end tag only when
// the name matches, agrees with surgery on both kinds of list.
func TestTheGuardMakesTheEndPositionAgree(t *testing.T) {
	stream := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("li", func(e *lolhtml.Element) error {
			tag := e.TagName()
			return e.OnEndTag(func(x *lolhtml.EndTag) error {
				if x.Name() != tag {
					return nil
				}
				return x.Before("<!--m-->", lolhtml.HTML)
			})
		})}
	}
	surgery := func(n *html.Node) {
		n.AppendChild(&html.Node{Type: html.CommentNode, Data: "m"})
	}
	for _, doc := range []string{
		`<ul><li>a</li><li>b</li></ul>`,
		`<ul><li><div>a</div></li><li><div>b</div></li></ul>`,
	} {
		if streamed, cut := byStreaming(t, doc, stream()...), bySurgery(t, doc, "li", surgery); streamed != cut {
			t.Errorf("on %q\nstreamed: %q\nsurgery:  %q", doc, streamed, cut)
		}
	}
	// On a list with implied end tags the guard declines, so the streaming edit does
	// less than the surgery rather than something else - which is the choice the
	// documentation recommends.
	const implied = `<ul><li>a<li>b</ul>`
	streamed, cut := byStreaming(t, implied, stream()...), bySurgery(t, implied, "li", surgery)
	if streamed == cut {
		t.Errorf("the guarded edit matched the surgery on %q, so the guard did not decline", implied)
	}
	if strings.Count(streamed, "<!--m-->") != 0 {
		t.Errorf("the guarded edit inserted %d comments on %q, want none",
			strings.Count(streamed, "<!--m-->"), implied)
	}
	if strings.Count(cut, "<!--m-->") != 2 {
		t.Errorf("the surgery inserted %d comments, want two", strings.Count(cut, "<!--m-->"))
	}
}

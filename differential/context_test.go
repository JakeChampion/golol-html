package differential

// A value is only source for the context it came from. The package documentation
// says that a value taken from the document is already raw source, so building
// markup with it means not escaping it again - which is right when it goes back into
// the same kind of place and wrong when it does not.
//
// An attribute value may hold a raw "<", because "<" is an ordinary character
// inside an attribute. Moved into an element's text it is markup, and an attribute
// the document had made inert becomes an element. A text chunk may hold a raw '"',
// for the same reason in the other direction, and moved into an attribute it ends
// the attribute and whatever follows is more attributes.
//
// Both moves are ordinary things to want: an alt into a title, a heading into an
// aria-label. Neither escaper fixes them on its own, because escaping "&" is not
// idempotent - so the value has to be decoded first, or kept in the context it
// arrived in, which is what examples/gip/sprite does.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// elements counts elements of a name in the tree, which is how "it became an
// element" gets checked rather than asserted.
func elements(t *testing.T, doc, name string) int {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var n int
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == name {
			n++
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return n
}

// attrs returns the attribute names of the first element of a name, so a value that
// turned into attributes can be seen doing it.
func attrs(t *testing.T, doc, name string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var found []string
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		if node.Type == html.ElementNode && node.Data == name {
			for _, a := range node.Attr {
				found = append(found, a.Key)
			}
			return true
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(root)
	return found
}

// payload is an attribute value that is inert where it sits and an element anywhere
// text is expected.
const payload = `<img src=x onerror=alert(1)>`

// TestAnAttributeValueHoldingMarkupIsInert, which is the starting point: the
// document is not the problem, the move is.
func TestAnAttributeValueHoldingMarkupIsInert(t *testing.T) {
	doc := `<span title="` + payload + `">x</span>`
	if n := elements(t, doc, "img"); n != 0 {
		t.Fatalf("%d img elements in the source tree, want 0", n)
	}
	// And the library reports it with the "<" still a "<", because that is what the
	// source spells.
	var value string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		value, _ = e.Attribute("title")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if value != payload {
		t.Errorf("the attribute reads back as %q, want %q", value, payload)
	}
}

// TestMovingItIntoTextMakesItAnElement: the rewrite that follows the "already
// source, do not escape it" advice across contexts creates the element.
func TestMovingItIntoTextMakesItAnElement(t *testing.T) {
	doc := `<span title="` + payload + `">x</span>`
	raw, err := lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("title")
		return e.Prepend(v, lolhtml.HTML) // the mistake
	}))
	if err != nil {
		t.Fatal(err)
	}
	if n := elements(t, raw, "img"); n != 1 {
		t.Errorf("%d img elements after moving it into text, want 1: %q", n, raw)
	}
	if got := attrs(t, raw, "img"); len(got) != 2 {
		t.Errorf("the img carries %v, want src and onerror", got)
	}

	// Text as a ContentType escapes it, and so does EscapeText for markup built by
	// hand. Either way nothing new is an element.
	for _, name := range []string{"Text content type", "EscapeText"} {
		var out string
		var err error
		switch name {
		case "Text content type":
			out, err = lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
				v, _ := e.Attribute("title")
				return e.Prepend(v, lolhtml.Text)
			}))
		default:
			out, err = lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
				v, _ := e.Attribute("title")
				return e.Prepend(`<b>`+lolhtml.EscapeText(v)+`</b>`, lolhtml.HTML)
			}))
		}
		if err != nil {
			t.Fatal(err)
		}
		if n := elements(t, out, "img"); n != 0 {
			t.Errorf("%s: %d img elements, want 0: %q", name, n, out)
		}
	}
}

// TestEscapingItAgainChangesWhatItSays, which is why "escape everything on the way
// out" is not the answer either: the value is already source, and only its markup
// characters need attention.
func TestEscapingItAgainChangesWhatItSays(t *testing.T) {
	const doc = `<span title="Ben &amp; Jerry">x</span>`
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("title")
		return e.SetInnerContent(lolhtml.EscapeText(v), lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	root, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			text += n.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if text != "Ben &amp; Jerry" {
		t.Errorf("the text reads %q, want %q - the reference was escaped twice", text, "Ben &amp; Jerry")
	}
	// The same value that has not been escaped again says what the attribute said.
	out, err = lolhtml.RewriteString(doc, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("title")
		return e.SetInnerContent(v, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	text = ""
	root, err = html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	walk(root)
	if text != "Ben & Jerry" {
		t.Errorf("the text reads %q, want %q", text, "Ben & Jerry")
	}
}

// TestATextChunkHoldingAQuoteEndsAnAttribute, the same rule the other way round: a
// quote is an ordinary character in text and the end of a value in an attribute.
func TestATextChunkHoldingAQuoteEndsAnAttribute(t *testing.T) {
	const doc = `<h2>a" onload=alert(1) x="b</h2>`
	if got := attrs(t, doc, "h2"); len(got) != 0 {
		t.Fatalf("the source h2 carries %v, want nothing", got)
	}
	var text string
	raw, err := lolhtml.RewriteString(doc,
		lolhtml.OnText("h2", func(c *lolhtml.TextChunk) error {
			text += c.Text()
			return nil
		}),
		lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.After(`<div data-title="`+text+`"></div>`, lolhtml.HTML) // the mistake
			})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := attrs(t, raw, "div")
	if len(got) < 2 {
		t.Fatalf("the div carries %v, want the value to have become attributes: %q", got, raw)
	}
	var onload bool
	for _, a := range got {
		if a == "onload" {
			onload = true
		}
	}
	if !onload {
		t.Errorf("the div carries %v, want an onload among them", got)
	}

	// Escaping the one character the destination context ends on is enough, and it
	// is what the library itself writes for an attribute. Neither adds a reference
	// the value did not have.
	quoted := strings.ReplaceAll(text, `"`, "&quot;")
	safe, err := lolhtml.RewriteString(doc, lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			return t.After(`<div data-title="`+quoted+`"></div>`, lolhtml.HTML)
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := attrs(t, safe, "div"); len(got) != 1 || got[0] != "data-title" {
		t.Errorf("the div carries %v, want only data-title: %q", got, safe)
	}
	mirror, err := lolhtml.RewriteString(`<div></div>`, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-title", text)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div data-title="` + quoted + `"></div>`; mirror != want {
		t.Errorf("SetAttribute wrote %q, want %q - the quote-only rule is the library's own", mirror, want)
	}
}

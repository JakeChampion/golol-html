package differential

// <image> is a spelling of <img>. The parser renames it: in HTML content an
// <image> start tag builds an img element, carrying every attribute it had, so a
// browser loads it and runs its onerror. The rewriter reports what the document
// spelled - "image" - so a selector for img does not match it and a selector for
// image does.
//
// It matters to anything keyed on img: a sanitiser stripping event handlers, a URL
// rewriter, a mixed-content checker. And it cannot be fixed by matching both names
// blindly, because SVG has an image element of its own that keeps its name.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// firstElement returns the name and attributes of the first element under body, from
// the tree.
func firstElement(t *testing.T, doc string) (string, map[string]string) {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data != "html" && n.Data != "head" && n.Data != "body" {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		return "", nil
	}
	attrs := map[string]string{}
	for _, a := range found.Attr {
		attrs[a.Key] = a.Val
	}
	return found.Data, attrs
}

// TestTheParserRenamesImageToImg, keeping the attributes.
func TestTheParserRenamesImageToImg(t *testing.T) {
	for _, doc := range []string{
		`<image src="x.png">`,
		`<IMAGE SRC="x.png">`,
		`<image src="x.png" onerror="alert(1)">`,
		`<image src="x.png"/>`,
		`<image src="x.png"></image>`,
	} {
		name, attrs := firstElement(t, doc)
		if name != "img" {
			t.Errorf("%q builds a %q element, want img", doc, name)
		}
		if attrs["src"] != "x.png" {
			t.Errorf("%q: attributes are %v, want src carried over", doc, attrs)
		}
	}
	// The event handler comes with it, which is why a sanitiser cares.
	_, attrs := firstElement(t, `<image src="x.png" onerror="alert(1)">`)
	if attrs["onerror"] != "alert(1)" {
		t.Errorf("attributes are %v, want the onerror carried over", attrs)
	}
}

// TestTheRewriterReportsWhatTheDocumentSpelled, so an img selector misses it.
func TestTheRewriterReportsWhatTheDocumentSpelled(t *testing.T) {
	const doc = `<image src="x.png" onerror="alert(1)">`

	var names []string
	var imgs, images int
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			names = append(names, e.TagName())
			return nil
		}),
		lolhtml.OnElement("img", func(*lolhtml.Element) error { imgs++; return nil }),
		lolhtml.OnElement("image", func(*lolhtml.Element) error { images++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, " ") != "image" {
		t.Errorf("the handler saw %v, want image", names)
	}
	if imgs != 0 {
		t.Errorf("an img selector matched %d times, want 0", imgs)
	}
	if images != 1 {
		t.Errorf("an image selector matched %d times, want 1", images)
	}
	// An attribute selector for img is no better.
	var attr int
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("img[src]", func(*lolhtml.Element) error {
		attr++
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if attr != 0 {
		t.Errorf("img[src] matched %d times, want 0", attr)
	}
}

// TestRenamingItIsWhatARewriteShouldDo, and the output then matches what a browser
// was going to build anyway.
func TestRenamingItIsWhatARewriteShouldDo(t *testing.T) {
	out, err := lolhtml.RewriteString(`<image src="x.png" onerror="alert(1)">`,
		lolhtml.OnElement("image", func(e *lolhtml.Element) error {
			if e.NamespaceURI() != lolhtml.NamespaceHTML {
				return nil // an SVG image is a different element
			}
			if err := e.SetTagName("img"); err != nil {
				return err
			}
			return e.RemoveAttribute("onerror")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<img src="x.png">`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
	name, attrs := firstElement(t, out)
	if name != "img" || attrs["onerror"] != "" {
		t.Errorf("the rewritten document builds %q with %v", name, attrs)
	}
}

// TestAnSVGImageKeepsItsName, which is why matching both names needs a namespace
// check rather than a rename.
func TestAnSVGImageKeepsItsName(t *testing.T) {
	const doc = `<svg><image xlink:href="x.png"/></svg>`
	name, _ := firstElement(t, doc)
	if name != "svg" {
		t.Fatalf("the first element is %q", name)
	}
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var found string
	var ns string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "image" || n.Data == "img") {
			found, ns = n.Data, n.Namespace
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if found != "image" || ns != "svg" {
		t.Errorf("the tree has %q in namespace %q, want image in svg", found, ns)
	}

	// The library says which one it is, so a rewrite can tell them apart.
	var namespaces []string
	if _, err := lolhtml.RewriteString(doc+`<image src="y.png">`,
		lolhtml.OnElement("image", func(e *lolhtml.Element) error {
			namespaces = append(namespaces, e.NamespaceURI())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 2 {
		t.Fatalf("matched %d elements, want 2", len(namespaces))
	}
	if namespaces[0] != lolhtml.NamespaceSVG || namespaces[1] != lolhtml.NamespaceHTML {
		t.Errorf("namespaces are %v, want SVG then HTML", namespaces)
	}
}

// TestOtherObsoleteNamesAreNotRenamed, so this is one alias rather than a habit of
// the parser: everything else a document spells is what the tree gets.
func TestOtherObsoleteNamesAreNotRenamed(t *testing.T) {
	for _, tag := range []string{
		"center", "font", "marquee", "blink", "nobr", "acronym", "big", "strike",
		"tt", "applet", "keygen", "isindex", "spacer", "menuitem", "dir", "basefont",
	} {
		doc := "<" + tag + "></" + tag + ">"
		name, _ := firstElement(t, doc)
		if name != tag {
			t.Errorf("<%s> builds a %q element, want the same name", tag, name)
		}
		var matched int
		if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(tag, func(*lolhtml.Element) error {
			matched++
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if matched != 1 {
			t.Errorf("<%s>: a selector for its own name matched %d times", tag, matched)
		}
	}
}

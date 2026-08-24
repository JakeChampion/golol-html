package differential

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Which elements decode character references in their content, measured against
// golang.org/x/net/html rather than read off the specification.
//
// This is the fact behind what the raw-text guard can suggest. Where references
// decode, inserting as ContentType Text is a correct escape and reads back as
// written. Where they do not, Text is safe and wrong: nothing breaks out and the
// content no longer says what it said, so the only honest advice is that the
// sequence cannot appear in the element at all.
func TestWhichRawTextElementsDecodeReferences(t *testing.T) {
	tests := []struct {
		tag     string
		decodes bool
	}{
		// Escapable raw text.
		{"textarea", true},
		{"title", true},
		// Raw text.
		{"script", false},
		{"style", false},
		{"iframe", false},
		{"noembed", false},
		{"noframes", false},
		{"noscript", false},
		{"xmp", false},
		// A control: an ordinary element decodes, because its content is markup.
		{"div", true},
		{"pre", true},
	}
	for _, tt := range tests {
		doc := "<" + tt.tag + ">&lt;/" + tt.tag + "&gt;</" + tt.tag + ">"
		root, err := html.Parse(strings.NewReader(doc))
		if err != nil {
			t.Fatalf("%s: %v", tt.tag, err)
		}
		el := elementNamed(root, tt.tag)
		if el == nil {
			t.Fatalf("<%s> is not in the parsed tree of %q", tt.tag, doc)
		}
		got := textOf(el)
		decoded := got == "</"+tt.tag+">"
		if decoded != tt.decodes {
			t.Errorf("<%s> content %q: decoded = %v, want %v", tt.tag, got, decoded, tt.decodes)
		}
	}
}

// elementNamed returns the first element with the given tag name.
func elementNamed(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := elementNamed(c, tag); found != nil {
			return found
		}
	}
	return nil
}

package differential

// A descendant selector keeps matching after the ancestor has ended. The selector
// engine pops on end tag tokens, and a start tag never pops anything - so for every
// element whose end tag HTML lets a document leave out, "ancestor descendant" goes
// on matching until an explicit end tag arrives, over content the tree puts nowhere
// near the ancestor.
//
// It is the matching side of the end tag rule the package documentation covers for
// insertion positions, and it is worse in one way: there the silence is visible in
// the output, and here the handler simply runs on the wrong element.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// ancestry returns the element names above every occurrence of want in the tree, so
// a claim about "inside" can be checked rather than assumed.
func ancestry(t *testing.T, doc, want string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var found []string
	var walk func(*html.Node, []string)
	walk = func(n *html.Node, above []string) {
		if n.Type == html.ElementNode {
			if n.Data == want {
				found = append(found, strings.Join(above, ">"))
			}
			above = append(above, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, above)
		}
	}
	walk(root, nil)
	return found
}

// impliedCases are shapes where a start tag ends the video, each with a real end tag
// later so the document is finished.
var impliedCases = []struct {
	name string
	doc  string
}{
	{"a second list item", `<ul><li><video><li><track x></ul>`},
	{"a second paragraph", `<div><p><video><p><track x></div>`},
	{"a second cell", `<table><tr><td><video><td><track x></table>`},
	{"a second row", `<table><tr><td><video><tr><td><track x></table>`},
	{"a definition after a term", `<dl><dt><video><dd><track x></dl>`},
	{"a second option", `<select><option><video><option><track x></select>`},
	{"a deeper element after the close", `<ul><li><video><li><p><span><track x></span></p></ul>`},
}

// TestADescendantSelectorMatchesPastTheElementsEnd is the finding: in every shape,
// the tree has no video above the track and the selector fires anyway.
func TestADescendantSelectorMatchesPastTheElementsEnd(t *testing.T) {
	for _, c := range impliedCases {
		for _, above := range ancestry(t, c.doc, "track") {
			if strings.Contains(above, "video") {
				t.Fatalf("%s: the tree puts the track under %q, so this shape proves nothing",
					c.name, above)
			}
		}
		var descendant, child int
		if _, err := lolhtml.RewriteString(c.doc,
			lolhtml.OnElement("video track", func(*lolhtml.Element) error { descendant++; return nil }),
			lolhtml.OnElement("video > track", func(*lolhtml.Element) error { child++; return nil }),
		); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if descendant != 1 {
			t.Errorf("%s: %q matched %d times, want 1 - the over-match is the point",
				c.name, "video track", descendant)
		}
		if child != 0 {
			t.Errorf("%s: %q matched %d times, want 0", c.name, "video > track", child)
		}
	}
}

// TestAnEndTagDoesClose, which is what makes this a rule about start tags rather
// than about the selector engine losing track altogether.
func TestAnEndTagDoesClose(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"its own end tag", `<video></video><track x>`},
		{"an ancestor's end tag", `<div><video></div><track x>`},
		{"the list's end tag", `<ul><li><video><li>a</ul><track x>`},
		{"a closed list item", `<ul><li><video></li><li><track x></li></ul>`},
	} {
		var descendant int
		if _, err := lolhtml.RewriteString(tc.doc,
			lolhtml.OnElement("video track", func(*lolhtml.Element) error { descendant++; return nil }),
		); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if descendant != 0 {
			t.Errorf("%s: %q matched %d times, want 0", tc.name, "video track", descendant)
		}
	}
}

// TestTheOverMatchRunsToTheEnclosingEndTag: it is not one element but everything
// after the element until something explicit closes it.
func TestTheOverMatchRunsToTheEnclosingEndTag(t *testing.T) {
	const doc = `<ul><li><video><li><track a><li><track b></ul><track c>`
	var descendant int
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("video track", func(*lolhtml.Element) error { descendant++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	// a and b, both in later list items; c is after </ul> and is not matched.
	if descendant != 2 {
		t.Errorf("matched %d times, want 2", descendant)
	}
	for _, above := range ancestry(t, doc, "track") {
		if strings.Contains(above, "video") {
			t.Fatalf("the tree puts a track under %q", above)
		}
	}
}

// TestTheChildCombinatorCannotBeFooledThisWay, because the start tag that ended the
// element is also the parent of whatever comes next. That makes ">" the safe
// question for a child that can only be a child, which is what
// examples/gip/captions asks about a track.
func TestTheChildCombinatorCannotBeFooledThisWay(t *testing.T) {
	// Where the track really is a child, it matches.
	for _, doc := range []string{
		`<video><track x></video>`,
		`<ul><li><video><track x></video></ul>`,
		`<video><source y><track x></video>`,
	} {
		var child int
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("video > track", func(*lolhtml.Element) error { child++; return nil }),
		); err != nil {
			t.Fatal(err)
		}
		if child != 1 {
			t.Errorf("%q: %q matched %d times, want 1", doc, "video > track", child)
		}
	}
	// And where it is not, in every implied shape above, it does not.
	for _, c := range impliedCases {
		var child int
		if _, err := lolhtml.RewriteString(c.doc,
			lolhtml.OnElement("video > track", func(*lolhtml.Element) error { child++; return nil }),
		); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if child != 0 {
			t.Errorf("%s: %q matched %d times, want 0", c.name, "video > track", child)
		}
	}
}

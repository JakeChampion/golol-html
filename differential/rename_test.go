package differential

// Renaming an element changes how its content is parsed. SetTagName writes over the
// tag and nothing else - the content inside it was tokenised under the old name and is
// not looked at again - but whoever reads the output parses it under the new name's
// content model. Where that model does not accept what the element held, the content
// moves out of the element or disappears, with no error and nothing odd in the output.
//
// It is the same rule as "inserted content is not re-parsed", seen from the other
// side: here it is the content that was already there.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// tree returns the element names and non-blank text, indented, so a move or a deletion
// is visible.
func tree(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var parts []string
	var walk func(*html.Node, int)
	walk = func(n *html.Node, d int) {
		switch n.Type {
		case html.ElementNode:
			parts = append(parts, strings.Repeat(".", d)+n.Data)
		case html.TextNode:
			if s := strings.TrimSpace(n.Data); s != "" {
				parts = append(parts, strings.Repeat(".", d)+"#"+s)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, d+1)
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		walk(c, 0)
	}
	return strings.Join(parts, " ")
}

// renamed applies SetTagName to every element of the given name.
func renamed(t *testing.T, doc, from, to string) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(from, func(e *lolhtml.Element) error {
		return e.SetTagName(to)
	}))
	if err != nil {
		t.Fatalf("renaming %s to %s in %q: %v", from, to, doc, err)
	}
	return out
}

// TestARenameCanMoveTheContentOutOfTheElement: a table does not accept a paragraph, so
// the paragraph is fostered out of it.
func TestARenameCanMoveTheContentOutOfTheElement(t *testing.T) {
	const doc = `<div><p>x</p></div>`
	if got, want := tree(t, doc), "html .head .body ..div ...p ....#x"; got != want {
		t.Fatalf("before: %q, want %q", got, want)
	}
	out := renamed(t, doc, "div", "table")
	if want := `<table><p>x</p></table>`; out != want {
		t.Fatalf("the output is %q, want %q - the bytes are what was asked for", out, want)
	}
	if got, want := tree(t, out), "html .head .body ..p ...#x ..table"; got != want {
		t.Errorf("after: %q, want %q", got, want)
	}
}

// TestARenameCanDeleteTheContent: a select accepts option, optgroup, hr and script, so
// a paragraph and a span inside it are dropped and their text is merged.
func TestARenameCanDeleteTheContent(t *testing.T) {
	const doc = `<div><p>x</p><span>y</span></div>`
	out := renamed(t, doc, "div", "select")
	if want := `<select><p>x</p><span>y</span></select>`; out != want {
		t.Fatalf("the output is %q, want %q", out, want)
	}
	if got, want := tree(t, out), "html .head .body ..select ...#xy"; got != want {
		t.Errorf("after: %q, want %q - both elements are gone", got, want)
	}
}

// TestARenameIntoRawTextTurnsMarkupIntoText, and out of it turns text into markup:
// the two directions the documentation already covers, kept here so the family reads
// together.
func TestARenameIntoRawTextTurnsMarkupIntoText(t *testing.T) {
	out := renamed(t, `<div><b>x</b></div>`, "div", "xmp")
	if got, want := tree(t, out), "html .head .body ..xmp ...#<b>x</b>"; got != want {
		t.Errorf("into raw text: %q, want %q", got, want)
	}
	out = renamed(t, `<xmp><b>x</b></xmp>`, "xmp", "pre")
	if got, want := tree(t, out), "html .head .body ..pre ...b ....#x"; got != want {
		t.Errorf("out of raw text: %q, want %q", got, want)
	}
}

// TestTheModernisingRenamesAreSafe, which is the condition to check before renaming
// anything: does the new element's content model accept what the old one held?
func TestTheModernisingRenamesAreSafe(t *testing.T) {
	for _, tc := range []struct{ doc, from, to string }{
		{`<center><p>x</p></center>`, "center", "div"},
		{`<marquee><p>x</p></marquee>`, "marquee", "div"},
		{`<big>x<b>y</b></big>`, "big", "span"},
		{`<strike>x</strike>`, "strike", "s"},
		{`<tt>x</tt>`, "tt", "code"},
		{`<nobr>x<i>y</i></nobr>`, "nobr", "span"},
		{`<blink>x</blink>`, "blink", "span"},
		{`<acronym title="t">WWW</acronym>`, "acronym", "abbr"},
		{`<dir><li>a</li><li>b</li></dir>`, "dir", "ul"},
		{`<font color="red">a<b>b</b></font>`, "font", "span"},
		{`<image src="x.png">`, "image", "img"},
	} {
		before := tree(t, tc.doc)
		out := renamed(t, tc.doc, tc.from, tc.to)
		after := tree(t, out)
		// The trees have to match once the name is substituted: nothing moved,
		// nothing vanished, nothing gained.
		want := strings.ReplaceAll(before, "."+tc.from, "."+tc.to)
		if after != want {
			t.Errorf("%s -> %s\n before %q\n  after %q\n   want %q", tc.from, tc.to, before, after, want)
		}
	}
}

// TestARenameStealsAnImpliedEndTag, which is the other way a rename reaches past the
// element: the token it writes over may belong to something else.
func TestARenameStealsAnImpliedEndTag(t *testing.T) {
	const doc = `<h1>a <em>b</h1>`
	out := renamed(t, doc, "em", "i")
	if want := `<h1>a <i>b</i>`; out != want {
		t.Errorf("the output is %q, want %q", out, want)
	}
	// The heading's end tag is gone from the output, so the heading runs on.
	if strings.Contains(out, "</h1>") {
		t.Errorf("the h1 end tag survived: %q", out)
	}
	if got, want := tree(t, out), "html .head .body ..h1 ...#a ...i ....#b"; got != want {
		t.Errorf("after: %q, want %q", got, want)
	}
}

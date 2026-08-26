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

// TestARenameIntoAVoidNameHasFourAnswers. The documented content-model cases - a table that
// fosters, a select that deletes - are about where the content ends up. Renaming a container to
// a name that cannot hold content is about the element itself, and there are four outcomes.
// None of them is "this element, with its content inside it".
//
// The stray end tag is the reason. HTML has one end tag it treats as a start tag, and it is
// </br>; every other void element's end tag is a parse error and ignored. On top of that, two
// void elements are not allowed where the rename put them: a col outside a table is dropped, and
// a meta is moved to the head.
func TestARenameIntoAVoidNameHasFourAnswers(t *testing.T) {
	const doc = `<div class="w">x</div>`

	for _, tt := range []struct {
		to   string
		tree string
		why  string
	}{
		// The stray </br> becomes a second br, so the rename duplicated the element.
		{"br", "html .head .body ..br ..#x ..br", "</br> is parsed as <br>"},

		// The end tag is ignored, the element is void, and the content that was inside
		// it is now its sibling.
		{"img", "html .head .body ..img ..#x", "the end tag is ignored"},
		{"hr", "html .head .body ..hr ..#x", "the end tag is ignored"},
		{"input", "html .head .body ..input ..#x", "the end tag is ignored"},
		{"wbr", "html .head .body ..wbr ..#x", "the end tag is ignored"},
		{"area", "html .head .body ..area ..#x", "the end tag is ignored"},

		// A col outside a table is dropped, so the element is gone entirely.
		{"col", "html .head .body ..#x", "a col outside a table is dropped"},

		// A meta belongs in the head, so the element moves and its content does not.
		{"meta", "html .head ..meta .body ..#x", "a meta is moved to the head"},
	} {
		out := renamed(t, doc, "div", tt.to)
		if want := `<` + tt.to + ` class="w">x</` + tt.to + `>`; out != want {
			t.Errorf("renaming to %s gave %q, want %q", tt.to, out, want)
		}
		if got := tree(t, out); got != tt.tree {
			t.Errorf("renaming to %s: tree %q, want %q (%s)", tt.to, got, tt.tree, tt.why)
		}
	}

	// The element count is the part that surprises: one in, and zero, one or two out.
	counts := map[string]int{}
	for _, to := range []string{"br", "img", "col", "meta"} {
		counts[to] = strings.Count(tree(t, renamed(t, doc, "div", to)), to)
	}
	if counts["br"] != 2 || counts["img"] != 1 || counts["col"] != 0 || counts["meta"] != 1 {
		t.Errorf("element counts %v, want br 2, img 1, col 0, meta 1", counts)
	}

	// And a rename into another container keeps everything, which is what makes the void
	// names the thing to watch rather than renames in general.
	for _, to := range []string{"section", "my-widget", "span"} {
		out := renamed(t, doc, "div", to)
		if got, want := tree(t, out), "html .head .body .."+to+" ...#x"; got != want {
			t.Errorf("renaming to %s: tree %q, want %q", to, got, want)
		}
	}
}

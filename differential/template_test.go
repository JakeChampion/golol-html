package differential

// Templates: the rewriter goes into a <template> and the parser does too, so the
// selectors and the tree agree about where the content is - which makes a template
// the one place where an insertion is not fostered out. The trade is the other way
// round from a table. Inside a table, the insertion moves and the content survives.
// Inside a template, the insertion stays and the content can be thrown away: a
// template holding table rows is parsed in a mode that the first inserted element
// ends, and the rows go with it.
//
// The other half of it is that none of this is on the page. A template's content is
// inert until a script clones it, so a handler firing in there is a rewrite of a
// blueprint, and a count that mixes the two is a count of nothing in particular.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// path returns the tree as a flat list of element names and non-blank text, which
// is enough to say what survived and where it went.
func path(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			b.WriteString(n.Data + " ")
		case html.TextNode:
			if s := strings.TrimSpace(n.Data); s != "" {
				b.WriteString("#" + s + " ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return strings.TrimSpace(b.String())
}

// TestHandlersFireInsideATemplate, at any depth, and a descendant selector crosses
// the boundary. Nothing about a template hides its content from a rewrite.
func TestHandlersFireInsideATemplate(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want string
	}{
		{`<template><video src="a"></video></template>`, "template video"},
		{`<template><template><video src="a"></video></template></template>`, "template template video"},
		{`<table><template><tr><td>x</td></tr></template></table>`, "table template tr td"},
	} {
		var seen []string
		var descendants int
		_, err := lolhtml.RewriteString(tc.doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				seen = append(seen, e.TagName())
				return nil
			}),
			lolhtml.OnElement("template video", func(*lolhtml.Element) error {
				descendants++
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(seen, " "); got != tc.want {
			t.Errorf("%q fired on %q, want %q", tc.doc, got, tc.want)
		}
		if want := strings.Count(tc.doc, "<video"); descendants != want {
			t.Errorf("%q: %d descendant matches, want %d", tc.doc, descendants, want)
		}
	}
}

// TestATemplateKeepsMarkupADivDrops: the content follows the template's own parsing
// rules, which are not the surrounding document's. A row with no table is a row
// inside a template and is thrown away anywhere else - while the rewriter fires on
// both, because it is reading tokens.
func TestATemplateKeepsMarkupADivDrops(t *testing.T) {
	const rows = `<tr><td>x</td></tr>`
	if got, want := path(t, `<template>`+rows+`</template>`), "html head template tr td #x body"; got != want {
		t.Errorf("in a template the tree is %q, want %q", got, want)
	}
	if got, want := path(t, `<div>`+rows+`</div>`), "html head body div #x"; got != want {
		t.Errorf("in a div the tree is %q, want %q", got, want)
	}
	// The rewriter fires the same way in both, which is the disagreement worth
	// knowing about: a handler on td is not evidence that a cell exists.
	for _, doc := range []string{`<template>` + rows + `</template>`, `<div>` + rows + `</div>`} {
		var cells int
		if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("td", func(*lolhtml.Element) error {
			cells++
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if cells != 1 {
			t.Errorf("%q: %d cells fired, want 1", doc, cells)
		}
	}
}

// TestATemplateInATableIsNotFosteredOut, where a div in the same position is. So an
// insertion into a template lands in the template even inside a table.
func TestATemplateInATableIsNotFosteredOut(t *testing.T) {
	if got, want := path(t, `<table><template><tr><td>x</td></tr></template></table>`),
		"html head body table template tr td #x"; got != want {
		t.Errorf("the template is at %q, want %q", got, want)
	}
	if got, want := path(t, `<table><div>x</div></table>`), "html head body div #x table"; got != want {
		t.Errorf("the div is at %q, want %q", got, want)
	}
}

// TestPrependingAnElementIntoATemplateCanDeleteItsContent, which is the hazard.
// The insertion is not moved - it is exactly where the bytes say - and the rows it
// was inserted before are gone from the tree.
func TestPrependingAnElementIntoATemplateCanDeleteItsContent(t *testing.T) {
	const doc = `<table><template><tr><td>x</td></tr></template></table>`
	for _, op := range []struct {
		name string
		fn   func(*lolhtml.Element) error
		want string
	}{
		{"prepending an element", func(e *lolhtml.Element) error {
			return e.Prepend(`<input hidden="">`, lolhtml.HTML)
		}, "html head body table template input #x"},
		{"appending an element", func(e *lolhtml.Element) error {
			return e.Append(`<input hidden="">`, lolhtml.HTML)
		}, "html head body table template tr td #x input"},
		{"prepending a comment", func(e *lolhtml.Element) error {
			return e.Prepend(`<!--c-->`, lolhtml.HTML)
		}, "html head body table template tr td #x"},
		{"prepending text", func(e *lolhtml.Element) error {
			return e.Prepend(`hello`, lolhtml.Text)
		}, "html head body table template #hello tr td #x"},
		{"inserting before it", func(e *lolhtml.Element) error {
			return e.Before(`<input hidden="">`, lolhtml.HTML)
		}, "html head body input table template tr td #x"},
	} {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("template", op.fn))
		if err != nil {
			t.Fatalf("%s: %v", op.name, err)
		}
		if got := path(t, out); got != op.want {
			t.Errorf("%s gave %q\n  tree %q\n  want %q", op.name, out, got, op.want)
		}
	}
	// It is the parser's rule rather than the rewriter's insertion: the same bytes
	// written by hand lose the rows too.
	if got, want := path(t, `<template><input><tr><td>x</td></tr></template>`),
		"html head template input #x body"; got != want {
		t.Errorf("written by hand the tree is %q, want %q", got, want)
	}
	// And a table is the other way round: the insertion is fostered out of the
	// table and the rows survive.
	out, err := lolhtml.RewriteString(`<table><tbody><tr><td>x</td></tr></tbody></table>`,
		lolhtml.OnElement("tbody", func(e *lolhtml.Element) error {
			return e.Prepend(`<input hidden="">`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := path(t, out), "html head body input table tbody tr td #x"; got != want {
		t.Errorf("in a table the tree is %q, want %q", got, want)
	}
}

// TestALeadingTemplateIsInTheHead, which is where the content model puts it: a
// rewrite inserting before the first element in the body has to look past it.
func TestALeadingTemplateIsInTheHead(t *testing.T) {
	if got, want := path(t, `<template><video></video></template><p>x</p>`),
		"html head template video body p #x"; got != want {
		t.Errorf("the tree is %q, want %q", got, want)
	}
}

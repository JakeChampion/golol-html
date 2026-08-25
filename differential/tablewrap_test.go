package differential

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// wrapperAncestors parses doc and reports the ancestor chain of the first element carrying
// data-wrap, innermost first. It is how these tests see where an inserted wrapper landed, which
// the bytes alone do not say.
func wrapperAncestors(t *testing.T, doc string) string {
	t.Helper()

	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var chain string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if chain == "" && n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key != "data-wrap" {
					continue
				}
				var names []string
				for p := n.Parent; p != nil && p.Type == html.ElementNode; p = p.Parent {
					names = append(names, p.Data)
				}
				chain = strings.Join(names, "<")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if chain == "" {
		t.Fatalf("no element carrying data-wrap in %q", doc)
	}
	return chain
}

// wrapInParagraph wraps the <b> of a document in the given markup and returns the output.
func wrapInParagraph(t *testing.T, doctype, open, close string) string {
	t.Helper()

	doc := doctype + `<body><p>text <b class="x">c</b> after</p></body>`
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("b.x", func(e *lolhtml.Element) error {
		if err := e.Before(open, lolhtml.HTML); err != nil {
			return err
		}
		return e.After(close, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWhetherATableWrapperEscapesAParagraphDependsOnTheDoctype.
//
// The package documentation says a wrapper is two insertions and the parser decides whether they
// wrap, and B146 says a block wrapper inside a <p> takes the content out of it. Both are right,
// and there is one wrapper whose answer depends on something else entirely: a <table> leaves the
// paragraph in a standards-mode document and stays inside it in a quirks-mode one, because the
// specification's rule for a table start tag closes an open <p> only when the document is not in
// quirks mode. Every other wrapper is mode-independent:
//
//	wrapper     no doctype (quirks)   <!doctype html>
//	<table>     stays in the <p>      leaves the <p>
//	<div>       leaves                leaves
//	<section>   leaves                leaves
//	<ul>        leaves                leaves
//	<span>      stays                 stays
//
// It matters where table wrappers are the technique rather than an accident - converting a page
// to table-based layout for email clients, which is examples/gip/tablelayout - and it matters
// most because the documents that get converted are the ones with a missing or ancient doctype.
func TestWhetherATableWrapperEscapesAParagraphDependsOnTheDoctype(t *testing.T) {
	tests := []struct {
		wrapper     string
		open, close string
		quirks      string // where it lands with no doctype
		standards   string // where it lands with <!doctype html>
	}{
		{"table", `<table data-wrap role="presentation"><tr><td>`, `</td></tr></table>`,
			"p<body<html", "body<html"},
		{"div", `<div data-wrap>`, `</div>`, "body<html", "body<html"},
		{"section", `<section data-wrap>`, `</section>`, "body<html", "body<html"},
		{"ul", `<ul data-wrap><li>`, `</li></ul>`, "body<html", "body<html"},
		{"span", `<span data-wrap>`, `</span>`, "p<body<html", "p<body<html"},
	}

	for _, tt := range tests {
		t.Run(tt.wrapper, func(t *testing.T) {
			quirks := wrapperAncestors(t, wrapInParagraph(t, "", tt.open, tt.close))
			if quirks != tt.quirks {
				t.Errorf("with no doctype the wrapper landed in %s, want %s", quirks, tt.quirks)
			}

			standards := wrapperAncestors(t, wrapInParagraph(t, "<!doctype html>", tt.open, tt.close))
			if standards != tt.standards {
				t.Errorf("with a doctype the wrapper landed in %s, want %s", standards, tt.standards)
			}
		})
	}

	// The table is the only row where the two columns differ, which is the finding rather
	// than a detail of the table above.
	var differ []string
	for _, tt := range tests {
		if tt.quirks != tt.standards {
			differ = append(differ, tt.wrapper)
		}
	}
	if len(differ) != 1 || differ[0] != "table" {
		t.Errorf("the mode-dependent wrappers are %v, and the point of this test is that they "+
			"are exactly one", differ)
	}
}

// TestALegacyDoctypeIsQuirksToo, since the documents that get converted to table layout are
// exactly the ones with a doctype from 1999.
func TestALegacyDoctypeIsQuirksToo(t *testing.T) {
	const legacy = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`
	const html5 = `<!doctype html>`
	const open, close = `<table data-wrap role="presentation"><tr><td>`, `</td></tr></table>`

	if got := wrapperAncestors(t, wrapInParagraph(t, legacy, open, close)); got != "p<body<html" {
		t.Errorf("with a transitional doctype the wrapper landed in %s, want it inside the p", got)
	}
	if got := wrapperAncestors(t, wrapInParagraph(t, html5, open, close)); got != "body<html" {
		t.Errorf("with an HTML5 doctype the wrapper landed in %s, want it outside the p", got)
	}
}

// TestAWrapperOutsideAParagraphLandsWhereItWasPut, in every context a table-layout converter
// meets - so the paragraph is the exception rather than the rule.
func TestAWrapperOutsideAParagraphLandsWhereItWasPut(t *testing.T) {
	contexts := []struct {
		name, doc, want string
	}{
		{"in the body", `<!doctype html><body><b class="x">c</b></body>`, "body<html"},
		{"in a div", `<!doctype html><body><div><b class="x">c</b></div></body>`, "div<body<html"},
		{"in a cell", `<!doctype html><body><table><tr><td><b class="x">c</b></td></tr></table></body>`,
			"td<tr<tbody<table<body<html"},
		{"in a list item", `<!doctype html><body><ul><li><b class="x">c</b></li></ul></body>`,
			"li<ul<body<html"},
		{"in a span", `<!doctype html><body><span><b class="x">c</b></span></body>`, "span<body<html"},
	}

	for _, c := range contexts {
		t.Run(c.name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(c.doc, lolhtml.OnElement("b.x", func(e *lolhtml.Element) error {
				if err := e.Before(`<table data-wrap role="presentation"><tr><td>`, lolhtml.HTML); err != nil {
					return err
				}
				return e.After(`</td></tr></table>`, lolhtml.HTML)
			}))
			if err != nil {
				t.Fatal(err)
			}
			if got := wrapperAncestors(t, out); got != c.want {
				t.Errorf("the wrapper landed in %s, want %s", got, c.want)
			}
		})
	}
}

// TestTheContentGoesInsideTheWrapperEitherWay. Where the wrapper lands is one question; whether
// it wrapped the content is another, and the answer to the second is yes even when the first
// goes badly - the content moves with it.
func TestTheContentGoesInsideTheWrapperEitherWay(t *testing.T) {
	for _, doctype := range []string{"", "<!doctype html>"} {
		out := wrapInParagraph(t, doctype, `<table data-wrap role="presentation"><tr><td>`,
			`</td></tr></table>`)

		root, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		// The <b> has to be inside a <td>, whatever the table's own parent turned out to
		// be.
		var found bool
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "b" {
				for p := n.Parent; p != nil; p = p.Parent {
					if p.Type == html.ElementNode && p.Data == "td" {
						found = true
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		if !found {
			t.Errorf("doctype %q: the wrapped content is not inside the cell: %s", doctype, out)
		}
	}
}

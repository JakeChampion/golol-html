package differential

// A wrapper is two insertions, and the parser decides whether they wrap. The
// rewriter puts the opening tag before the element and the closing tag after it, and
// what comes out is a container around the element only if the two tags can nest
// where they were put. Inside a paragraph they often cannot, and nothing about the
// output looks wrong: the wrapper is either empty or it has taken the element out of
// the paragraph, with the text that followed it left behind.
//
// Which of those happens depends on the wrapper's element, on the wrapped element,
// and - for a table - on the doctype.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// shape returns the tree as indented element names and non-blank text, which is
// enough to see what is inside what.
func shape(t *testing.T, doc string) string {
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

// wrap puts open before the selected element and close after it, using the end tag
// only when it is the element's own - the discipline the end-tag documentation asks
// for, so that what is being measured here is the parser and not that mistake.
func wrap(t *testing.T, doc, selector, open, close string) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(selector, func(e *lolhtml.Element) error {
		if err := e.Before(open, lolhtml.HTML); err != nil {
			return err
		}
		if !e.CanHaveContent() {
			return e.After(close, lolhtml.HTML)
		}
		tag := e.TagName()
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			if t.Name() != tag {
				return nil
			}
			return t.After(close, lolhtml.HTML)
		})
	}))
	if err != nil {
		t.Fatalf("wrapping %q: %v", doc, err)
	}
	return out
}

// TestADivWrapperInsideAParagraphTakesTheElementOutOfIt, and orphans the text that
// followed it, and leaves an empty paragraph behind. Three changes to the tree from
// an edit that only meant to add a container.
func TestADivWrapperInsideAParagraphTakesTheElementOutOfIt(t *testing.T) {
	const doc = `<p>text <iframe src="a"></iframe> more</p>`
	if got, want := shape(t, doc), "html .head .body ..p ...#text ...iframe ...#more"; got != want {
		t.Fatalf("before: %q, want %q", got, want)
	}
	out := wrap(t, doc, "iframe", `<div class="s">`, `</div>`)
	if got, want := shape(t, out), "html .head .body ..p ...#text ..div ...iframe ..#more ..p"; got != want {
		t.Errorf("a div wrapper gave %q\n  tree %q\n  want %q", out, got, want)
	}
	// A span wrapper is a wrapper: the paragraph keeps everything it had.
	out = wrap(t, doc, "iframe", `<span class="s">`, `</span>`)
	if got, want := shape(t, out), "html .head .body ..p ...#text ...span ....iframe ...#more"; got != want {
		t.Errorf("a span wrapper gave %q\n  tree %q\n  want %q", out, got, want)
	}
}

// TestASpanWrapperCannotHoldWhatClosesAParagraph: the span comes out empty and the
// element is outside it, because the element's start tag ended the paragraph and the
// span with it.
func TestASpanWrapperCannotHoldWhatClosesAParagraph(t *testing.T) {
	const doc = `<p>text<pre>code</pre></p>`
	out := wrap(t, doc, "pre", `<span class="s">`, `</span>`)
	if got, want := shape(t, out), "html .head .body ..p ...#text ...span ..pre ...#code ..p"; got != want {
		t.Errorf("a span wrapper gave %q\n  tree %q\n  want %q", out, got, want)
	}
	// The div is right here: it leaves the paragraph with the element, which the
	// element was leaving anyway.
	out = wrap(t, doc, "pre", `<div class="s">`, `</div>`)
	if got, want := shape(t, out), "html .head .body ..p ...#text ..div ...pre ....#code ..p"; got != want {
		t.Errorf("a div wrapper gave %q\n  tree %q\n  want %q", out, got, want)
	}
}

// TestTheDoctypeDecidesWhichWrapperATableWants, which is the part no amount of
// looking at the element can tell you: without a doctype the document is in quirks
// mode, a table does not close a paragraph, and the answer flips.
func TestTheDoctypeDecidesWhichWrapperATableWants(t *testing.T) {
	const body = `<p>text<table><tr><td>x</table></p>`
	for _, tc := range []struct {
		mode, doc, div, span string
	}{
		{
			mode: "quirks",
			doc:  body,
			div:  "html .head .body ..p ...#text ..div ...table ....tbody .....tr ......td .......#x ..p",
			span: "html .head .body ..p ...#text ...span ....table .....tbody ......tr .......td ........#x",
		},
		{
			mode: "standards",
			doc:  `<!DOCTYPE html>` + body,
			div:  "html .head .body ..p ...#text ..div ...table ....tbody .....tr ......td .......#x ..p",
			span: "html .head .body ..p ...#text ...span ..table ...tbody ....tr .....td ......#x ..p",
		},
	} {
		if got := shape(t, wrap(t, tc.doc, "table", `<div class="s">`, `</div>`)); got != tc.div {
			t.Errorf("%s, div wrapper: %q\n              want %q", tc.mode, got, tc.div)
		}
		if got := shape(t, wrap(t, tc.doc, "table", `<span class="s">`, `</span>`)); got != tc.span {
			t.Errorf("%s, span wrapper: %q\n               want %q", tc.mode, got, tc.span)
		}
	}
	// Read the two span rows together: in quirks mode the span holds the table, and
	// in standards mode the table is outside it and the span is empty. Same bytes
	// either side of the element, opposite results.
}

// TestAWrapperAroundATableInternalElementWrapsNothing: it is fostered out of the
// table, ending up before it, while the cell stays exactly where it was.
func TestAWrapperAroundATableInternalElementWrapsNothing(t *testing.T) {
	const doc = `<table><tr><td>x</table>`
	out := wrap(t, doc, "td", `<div class="s">`, `</div>`)
	if got, want := shape(t, out), "html .head .body ..div ..table ...tbody ....tr .....td ......#x"; got != want {
		t.Errorf("got %q\n  tree %q\n  want %q", out, got, want)
	}
}

// TestOutsideAParagraphADivWrapperWraps, over the shapes a wide element sits in, so
// that the cases above read as exceptions rather than as the rule.
func TestOutsideAParagraphADivWrapperWraps(t *testing.T) {
	for _, tc := range []struct{ sel, doc, want string }{
		{"table", `<div>text<table><tr><td>x</table></div>`,
			"html .head .body ..div ...#text ...div ....table .....tbody ......tr .......td ........#x"},
		{"pre", `<blockquote><pre>code</pre></blockquote>`,
			"html .head .body ..blockquote ...div ....pre .....#code"},
		{"table", `<li>a<table><tr><td>x</table>`,
			"html .head .body ..li ...#a ...div ....table .....tbody ......tr .......td ........#x"},
		{"table", `<table><tr><td><table><tr><td>x</table></table>`,
			"html .head .body ..div ...table ....tbody .....tr ......td .......div ........table .........tbody ..........tr ...........td ............#x"},
	} {
		out := wrap(t, tc.doc, tc.sel, `<div class="s">`, `</div>`)
		if got := shape(t, out); got != tc.want {
			t.Errorf("%q gave %q\n  tree %q\n  want %q", tc.doc, out, got, tc.want)
		}
	}
}

package differential

// A comment inserted between the doctype and the first element: does the document still parse in
// standards mode? A build stamp at the top is the natural place for one, and a doctype has to be
// first to count, so this is worth knowing rather than assuming.
//
// B174 established the test from the outside: a table start tag closes an open paragraph in
// standards mode and not in quirks mode.

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// mode reports which parsing mode a document is in, by the one behaviour that differs.
func mode(t *testing.T, prefix string) string {
	t.Helper()
	got := tree(t, prefix+`<p>a<table><tr><td>x</table>`)
	// In standards mode the table is a sibling of the paragraph; in quirks mode it is
	// inside it.
	switch {
	case strings.Contains(got, "..p ...#a ..table"), strings.Contains(got, "..p ...#a ...# "):
		return "standards"
	case strings.Contains(got, "..p ...#a ...table"):
		return "quirks"
	}
	return "unknown: " + got
}

func TestACommentDoesNotStopADoctypeCounting(t *testing.T) {
	for _, tt := range []struct {
		name, prefix, want string
	}{
		{"a doctype alone", `<!doctype html>`, "standards"},
		{"a comment before it", `<!-- build 9f3a1c2 --><!doctype html>`, "standards"},
		{"two comments before it", `<!-- a --><!-- b --><!doctype html>`, "standards"},
		{"a comment after it", `<!doctype html><!-- build 9f3a1c2 -->`, "standards"},
		{"whitespace before it", "\n \t<!doctype html>", "standards"},
		{"no doctype at all", ``, "quirks"},
		{"a legacy doctype", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`,
			"quirks"},
	} {
		if got := mode(t, tt.prefix); got != tt.want {
			t.Errorf("%s: %s mode, want %s", tt.name, got, tt.want)
		}
	}
}

// TestTheParserAddsWhatTheSourceLeavesOut, which is why a rewrite cannot anchor on <head>: the
// tree has one and the source does not, and a rewriter reports the source.
func TestTheParserAddsWhatTheSourceLeavesOut(t *testing.T) {
	for _, tt := range []struct{ doc, want string }{
		{`<!doctype html><p>x</p>`, "html .head .body ..p ...#x"},
		{`<p>x</p>`, "html .head .body ..p ...#x"},
		{`just text`, "html .head .body ..#just text"},
		{`<title>t</title>`, "html .head ..title ...#t .body"},
	} {
		if got := tree(t, tt.doc); got != tt.want {
			t.Errorf("%q: tree %q, want %q", tt.doc, got, tt.want)
		}
	}

	// So the tree always has html, head and body, and the source usually does not - which is
	// the gap examples/gip/buildinfo works around by anchoring on the first element of any
	// kind.
	root, err := html.Parse(strings.NewReader(`<p>x</p>`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			names = append(names, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if want := "html head body p"; strings.Join(names, " ") != want {
		t.Errorf("the tree holds %v, want %s", names, want)
	}
}

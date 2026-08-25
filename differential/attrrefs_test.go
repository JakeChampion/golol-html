package differential

// html.UnescapeString is not the parser's decoder for an attribute value. In an
// attribute, a named reference that does not end in a semicolon is not a reference at
// all when the character after it is "=" or ASCII alphanumeric - a rule the HTML
// specification keeps for the URLs the web already had - and the standard library
// decodes it anyway.
//
// It matters wherever the library's own advice is followed: decide on the decoded form
// and rewrite the raw one. For a query string the decoded form the standard library
// hands back is not the one a browser has, so a filter can act on a URL nobody will
// request, and a rewrite that decodes, edits and re-encodes produces a different URL.

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// hrefFromTree reads the anchor's href out of the parsed tree, which is the decoded
// form a browser has.
func hrefFromTree(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var out string
	var done bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if done {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					out, done = a.Val, true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// hrefFromLibrary reads it the way a handler does: raw source, references encoded.
func hrefFromLibrary(t *testing.T, doc string) string {
	t.Helper()
	var out string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		out, _ = e.Attribute("href")
		return nil
	})); err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// TestTheStandardLibraryOverDecodesAnAttributeValue, over the shapes a query string
// takes.
func TestTheStandardLibraryOverDecodesAnAttributeValue(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		tree  string // what a browser has
		agree bool   // whether html.UnescapeString gives the same
	}{
		// No semicolon, and the next character is "=" or alphanumeric: not a
		// reference. The standard library decodes all of these.
		{`?a=1&notit=2`, `?a=1&notit=2`, false},
		{`?a=1&not=2`, `?a=1&not=2`, false},
		{`?a=1&noti`, `?a=1&noti`, false},
		{`?a=1&amp=2`, `?a=1&amp=2`, false},
		{`?a=1&lt=2`, `?a=1&lt=2`, false},
		{`?a=1&copy=2`, `?a=1&copy=2`, false},
		// With the semicolon it is a reference, and the two agree.
		{`?a=1&not;=2`, `?a=1¬=2`, true},
		{`?a=1&amp;b=2`, `?a=1&b=2`, true},
		{`?a=1&copy;=2`, `?a=1©=2`, true},
		// Without a semicolon but at the end of the value, or followed by
		// something that is neither "=" nor alphanumeric, it is a reference again.
		{`?x=&gt`, `?x=>`, true},
		{`?x=&gt.`, `?x=>.`, true},
	} {
		doc := `<a href="` + tc.raw + `">x</a>`
		want := strings.ReplaceAll(strings.ReplaceAll(tc.tree, `¬`, "¬"), `©`, "©")

		if got := hrefFromTree(t, doc); got != want {
			t.Errorf("%q: the tree has %q, want %q", tc.raw, got, want)
		}
		raw := hrefFromLibrary(t, doc)
		if raw != tc.raw {
			t.Errorf("%q: the library reports %q, want the source", tc.raw, raw)
		}
		std := stdhtml.UnescapeString(raw)
		if agreed := std == want; agreed != tc.agree {
			t.Errorf("%q: html.UnescapeString gives %q and the browser has %q; agreement = %v, want %v",
				tc.raw, std, want, agreed, tc.agree)
		}
	}
}

// TestInTextTheyAgree, which is why the advice is only wrong for attributes: the
// semicolon rule is an attribute rule.
func TestInTextTheyAgree(t *testing.T) {
	for _, raw := range []string{
		`a&notit;b`, `a&not;b`, `a&notb`, `a&ampb`, `a&amp;b`, `a&gtb`, `a&copy2b`,
		`a&#62;b`, `a&#x3e;b`, `a&unknown;b`,
	} {
		doc := `<p>` + raw + `</p>`
		root, err := html.Parse(strings.NewReader(doc))
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.TextNode {
				b.WriteString(n.Data)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		if got, want := stdhtml.UnescapeString(raw), b.String(); got != want {
			t.Errorf("%q: html.UnescapeString gives %q, the tree has %q", raw, got, want)
		}
	}
}

// TestTheRuleIsTheNextCharacter, stated as a function so a caller can implement it:
// a named reference in an attribute value needs its semicolon when what follows is
// "=" or ASCII alphanumeric.
func TestTheRuleIsTheNextCharacter(t *testing.T) {
	for _, tc := range []struct {
		after string
		isRef bool
	}{
		{"=", false}, {"a", false}, {"Z", false}, {"0", false},
		{".", true}, {"/", true}, {"&", true}, {" ", true}, {"", true}, {"-", true},
	} {
		raw := `?x=&gt` + tc.after
		doc := `<a href="` + raw + `">x</a>`
		got := hrefFromTree(t, doc)
		decoded := `?x=>` + tc.after
		if isRef := got == decoded; isRef != tc.isRef {
			t.Errorf("&gt followed by %q gives %q; decoded = %v, want %v", tc.after, got, isRef, tc.isRef)
		}
	}
}

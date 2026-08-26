package differential

// A meta has to be in the head or a browser ignores it, and a rewriter cannot see the head a
// parser will build. What it can do is insert before the first element and let the parser decide -
// which works, until text gets there first.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// beforeFirstElement inserts markup before the first element of any kind, which is the only anchor
// a rewriter is sure of.
func beforeFirstElement(t *testing.T, doc, markup string) string {
	t.Helper()
	done := false
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if done {
			return nil
		}
		done = true
		return e.Before(markup, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// metaInHead reports whether the tree has a meta among the head's children.
func metaInHead(treeText string) bool {
	parts := strings.Fields(treeText)
	for i, p := range parts {
		if strings.TrimLeft(p, ".") != "head" {
			continue
		}
		depth := len(p) - len(strings.TrimLeft(p, "."))
		for _, q := range parts[i+1:] {
			qd := len(q) - len(strings.TrimLeft(q, "."))
			if qd <= depth {
				break
			}
			if strings.TrimLeft(q, ".") == "meta" {
				return true
			}
		}
	}
	return false
}

// TestAMetaInsertedBeforeTheFirstElementLandsInTheHead, because a parser in its "before head" mode
// meets the meta, creates the head, and puts the meta in it. So a rewrite does not have to build
// the head - and a <head> of its own would be dropped as a duplicate where the source has one.
func TestAMetaInsertedBeforeTheFirstElementLandsInTheHead(t *testing.T) {
	const meta = `<meta name="deploy-id" content="d1">`

	for _, tt := range []struct {
		name string
		doc  string
		head bool
	}{
		{"a whole page", `<!doctype html><html><head><title>t</title></head><body><p>x</p></body></html>`, true},
		{"no head spelled", `<!doctype html><p>x</p>`, true},
		{"html but no head", `<!doctype html><html><body><p>x</p></body></html>`, true},
		{"body spelled first", `<!doctype html><body><p>x</p></body>`, true},
		{"title first", `<!doctype html><title>t</title><p>x</p>`, true},
		{"a comment first", `<!doctype html><!-- c --><p>x</p>`, true},
		{"whitespace first", "<!doctype html>\n  \t<p>x</p>", true},
		{"no doctype", `<p>x</p>`, true},

		// Text ends the head, so the insertion is in the body and a browser ignores it.
		{"text first", `<!doctype html>text<p>x</p>`, false},
		{"a nbsp first", "<!doctype html>\u00a0<p>x</p>", false},
		{"an entity first", `<!doctype html>&amp;<p>x</p>`, false},
	} {
		out := beforeFirstElement(t, tt.doc, meta)
		if got := metaInHead(tree(t, out)); got != tt.head {
			t.Errorf("%s: meta in head = %v, want %v\n  %s",
				tt.name, got, tt.head, tree(t, out))
		}
	}

	// Wrapping the meta in a head of its own gives the same tree, so it buys nothing - and
	// where the source has a head, the second one is a parse error and dropped, so the meta
	// joins the first.
	for _, doc := range []string{
		`<!doctype html><p>x</p>`,
		`<!doctype html><html><head><title>t</title></head><body>x</body></html>`,
	} {
		bare := tree(t, beforeFirstElement(t, doc, meta))
		wrapped := tree(t, beforeFirstElement(t, doc, "<head>"+meta+"</head>"))
		if bare != wrapped {
			t.Errorf("%q: wrapping changed the tree\n  bare:    %s\n  wrapped: %s",
				doc, bare, wrapped)
		}
		if !metaInHead(bare) {
			t.Errorf("%q: %s", doc, bare)
		}
	}
}

// TestOnlyTheFiveWhitespaceCharactersKeepTheHeadOpen, which is where a template that indents with
// a non-breaking space loses every meta tag it adds.
func TestOnlyTheFiveWhitespaceCharactersKeepTheHeadOpen(t *testing.T) {
	const meta = `<meta name="deploy-id" content="d1">`
	for _, tt := range []struct {
		prefix string
		head   bool
	}{
		{"\t", true},
		{"\n", true},
		{"\f", true},
		{"\r", true},
		{" ", true},
		{" \t\r\n\f ", true},

		{"\u00a0", false}, // no-break space
		{"\u2007", false}, // figure space
		{"\u202f", false}, // narrow no-break space
		{"\u3000", false}, // ideographic space
		{"\v", false},     // vertical tab
		{"x", false},
	} {
		doc := "<!doctype html>" + tt.prefix + "<p>x</p>"
		out := beforeFirstElement(t, doc, meta)
		if got := metaInHead(tree(t, out)); got != tt.head {
			t.Errorf("prefix %q: meta in head = %v, want %v\n  %s",
				tt.prefix, got, tt.head, tree(t, out))
		}
	}
}

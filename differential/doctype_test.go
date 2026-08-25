package differential

// What a doctype handler is told, against what a parser keeps.
//
// OnDoctype reports doctype tokens. An HTML parser turns some of those into a
// document type node and discards the rest: a DOCTYPE is only honoured before
// anything else has been seen, so one that arrives after an element, after text,
// or after another DOCTYPE is a parse error and dropped.
//
// The difference matters to anything that decides something from the doctype -
// whether the page is in standards mode, whether a legacy doctype needs
// upgrading, whether to strip one. x/net/html is the oracle here because it
// builds the tree the spec describes, which is what a browser would agree with.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

func doctypeTokens(t *testing.T, doc string) []string {
	t.Helper()
	var names []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			name, _ := d.Name()
			names = append(names, name)
			return nil
		})); err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return names
}

func doctypeNodes(t *testing.T, doc string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	var names []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.DoctypeNode {
			names = append(names, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return names
}

// TestDoctypeHandlerAgreesWhereTheDoctypeIsHonoured. These are the positions a
// DOCTYPE is allowed in, and the two agree on all of them - so the divergence
// below is about position and not about parsing the doctype itself.
func TestDoctypeHandlerAgreesWhereTheDoctypeIsHonoured(t *testing.T) {
	for _, doc := range []string{
		`<!DOCTYPE html><html><body>x</body></html>`,
		` <!DOCTYPE html><html><body>x</body></html>`,
		"\n\t<!DOCTYPE html><html><body>x</body></html>",
		`<!-- a comment --><!DOCTYPE html><html><body>x</body></html>`,
		`<!DOCTYPE html>`,
		`<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN"><p>x</p>`,
		`<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd"><p>x</p>`,
		`<!doctype html><p>x</p>`,
		`<p>x</p>`,
	} {
		tokens, nodes := doctypeTokens(t, doc), doctypeNodes(t, doc)
		if strings.Join(tokens, ",") != strings.Join(nodes, ",") {
			t.Errorf("%q: handler saw %v, the parser kept %v", doc, tokens, nodes)
		}
	}
}

// TestDoctypeHandlerFiresForDoctypesAParserDiscards is the finding. In each of
// these the handler is told about a doctype that has no effect on the document,
// so "a doctype was seen" is not the same as "this page has a doctype".
func TestDoctypeHandlerFiresForDoctypesAParserDiscards(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		// wantTokens is what OnDoctype reports; wantNodes what a parser keeps.
		wantTokens, wantNodes int
	}{
		{"after an element", `<meta charset="utf-8"><!DOCTYPE html><html><body>x</body></html>`, 1, 0},
		{"inside html", `<html><!DOCTYPE html><body>x</body></html>`, 1, 0},
		{"after text", `x<!DOCTYPE html><html><body>y</body></html>`, 1, 0},
		{"a second doctype", `<!DOCTYPE html><!DOCTYPE html><html><body>x</body></html>`, 2, 1},
		{"in the body", `<html><body><!DOCTYPE html>x</body></html>`, 1, 0},
		{"three of them", `<!DOCTYPE html><!DOCTYPE a><!DOCTYPE b><p>x</p>`, 3, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokens, nodes := doctypeTokens(t, tt.doc), doctypeNodes(t, tt.doc)
			if len(tokens) != tt.wantTokens {
				t.Errorf("the handler fired %d times, want %d: %v", len(tokens), tt.wantTokens, tokens)
			}
			if len(nodes) != tt.wantNodes {
				t.Errorf("the parser kept %d doctypes, want %d: %v", len(nodes), tt.wantNodes, nodes)
			}
			if len(tokens) == len(nodes) {
				t.Errorf("these no longer diverge; the documentation on OnDoctype "+
					"and this test can go: handler %v, parser %v", tokens, nodes)
			}
		})
	}
}

// TestRemovingEveryDoctypeTokenIsSafeButNotSufficient. Removal follows the
// tokens, so a rewrite that strips doctypes removes the ones a parser ignored
// too - harmless - and the resulting document has none, which is the point.
//
// The reverse is what bites: a rewrite that decides "there is already a doctype,
// leave it alone" can be wrong, because the one it saw may be the discarded kind.
func TestRemovingEveryDoctypeTokenIsSafeButNotSufficient(t *testing.T) {
	for _, doc := range []string{
		`<!DOCTYPE html><html><body>x</body></html>`,
		`<meta charset="utf-8"><!DOCTYPE html><html><body>x</body></html>`,
		`<!DOCTYPE html><!DOCTYPE html><p>x</p>`,
	} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				d.Remove()
				return nil
			}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if n := len(doctypeNodes(t, out)); n != 0 {
			t.Errorf("%q -> %q still has %d doctypes", doc, out, n)
		}
		if strings.Contains(strings.ToUpper(out), "<!DOCTYPE") {
			t.Errorf("%q -> %q still contains a doctype token", doc, out)
		}
	}
}

// TestADoctypeAfterAnElementLeavesTheDocumentInQuirksMode is the consequence
// spelled out with the oracle: the document the parser builds has no doctype, so
// a browser renders it in quirks mode however much the source looks like it has
// one.
func TestADoctypeAfterAnElementLeavesTheDocumentInQuirksMode(t *testing.T) {
	const doc = `<meta charset="utf-8"><!DOCTYPE html><html><body>x</body></html>`

	if tokens := doctypeTokens(t, doc); len(tokens) != 1 {
		t.Fatalf("the handler saw %v", tokens)
	}
	if nodes := doctypeNodes(t, doc); len(nodes) != 0 {
		t.Fatalf("the parser kept %v, so this document is not the example it was "+
			"chosen to be", nodes)
	}
}

// upgradeDoctype is the only shape available for changing a declaration: remove the
// old one, and insert the new one before the first element, because a Doctype has
// no way to write at its own position.
func upgradeDoctype(t *testing.T, doc string) string {
	t.Helper()
	pending := false
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			if _, has := d.PublicID(); !has {
				if name, _ := d.Name(); name == "html" {
					return nil // already modern
				}
			}
			d.Remove()
			pending = true
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !pending {
				return nil
			}
			pending = false
			return e.Before("<!DOCTYPE html>", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// publicIDs is what the parser kept, which is the question: an empty string means a
// modern doctype and no entry at all means quirks mode.
func publicIDs(t *testing.T, doc string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.DoctypeNode {
			public := ""
			for _, a := range n.Attr {
				if a.Key == "public" {
					public = a.Val
				}
			}
			out = append(out, public)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// TestUpgradingADoctypeWorksAndWhereItSilentlyDoesNot. The three shapes it handles
// and the three it turns into quirks-mode documents, which is what the Doctype
// documentation now says.
func TestUpgradingADoctypeWorksAndWhereItSilentlyDoesNot(t *testing.T) {
	const legacy = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN">`
	for _, tc := range []struct {
		what, doc string
		upgraded  bool
	}{
		{"an ordinary document", legacy + `<html><body>x</body></html>`, true},
		{"a comment first", legacy + `<!--c--><html>x</html>`, true},
		{"whitespace first", legacy + `   <html>x</html>`, true},
		{"text before the first element", legacy + `text<html>x</html>`, false},
		{"no elements at all", legacy, false},
		{"only text", legacy + `just text`, false},
	} {
		// The input is a quirks-mode risk already: the legacy public id is what a
		// browser reads.
		if got := publicIDs(t, tc.doc); len(got) != 1 || got[0] == "" {
			t.Fatalf("%s: the input has %v, want one legacy doctype", tc.what, got)
		}
		out := upgradeDoctype(t, tc.doc)
		got := publicIDs(t, out)
		switch {
		case tc.upgraded:
			if len(got) != 1 || got[0] != "" {
				t.Errorf("%s: %q -> %q, the parser keeps %v, want one modern doctype",
					tc.what, tc.doc, out, got)
			}
		default:
			if len(got) != 0 {
				t.Errorf("%s: %q -> %q, the parser keeps %v, want none - this shape is "+
					"the silent failure the documentation describes", tc.what, tc.doc, out, got)
			}
		}
	}
}

// TestASecondDoctypeIsIgnored, which is why "add one without removing the old" is
// not an alternative: the legacy declaration still applies.
func TestASecondDoctypeIsIgnored(t *testing.T) {
	const doc = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN"><!DOCTYPE html><html>x</html>`
	if got := doctypeTokens(t, doc); len(got) != 2 {
		t.Fatalf("the handler saw %v, want both tokens", got)
	}
	got := publicIDs(t, doc)
	if len(got) != 1 {
		t.Fatalf("the parser kept %v, want one", got)
	}
	if got[0] == "" {
		t.Errorf("the parser kept the second doctype; the first should win")
	}
}

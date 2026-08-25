package differential

// Bytes that a parser changes before anything else happens, and that a rewriter
// cannot change and still be a rewriter.
//
// The library reports source. That is documented for character references, and
// there are four more differences with the same cause: HTML normalises newlines
// in the input stream, treats a NUL differently depending on where it is, and
// drops one leading newline inside a pre. Each of these is measured against
// x/net/html rather than argued from the specification, and each is a difference
// in what the two libraries hand you rather than a bug in either.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// oracleUnits is what x/net/html's tree holds: text, comments and attributes, in
// document order.
func oracleUnits(t *testing.T, doc string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			out = append(out, fmt.Sprintf("text %q", n.Data))
		case html.CommentNode:
			out = append(out, fmt.Sprintf("comment %q", n.Data))
		case html.ElementNode:
			for _, a := range n.Attr {
				out = append(out, fmt.Sprintf("attr %s=%q", a.Key, a.Val))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// libraryUnits is what golol-html hands a handler, in the same shape.
func libraryUnits(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				out = append(out, fmt.Sprintf("attr %s=%q", a.Name, a.Value))
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				out = append(out, fmt.Sprintf("text %q", c.Text()))
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			out = append(out, fmt.Sprintf("comment %q", c.Text()))
			return nil
		})); err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// TestANewlineIsNormalisedBeforeTheTokenizerSeesIt. A CR and a CRLF are both a LF
// to a parser, everywhere, and the library reports the bytes.
func TestANewlineIsNormalisedBeforeTheTokenizerSeesIt(t *testing.T) {
	for _, tc := range []struct {
		doc, oracle, library string
	}{
		{"<p>a\rb</p>", `text "a\nb"`, `text "a\rb"`},
		{"<p>a\r\nb</p>", `text "a\nb"`, `text "a\r\nb"`},
		{"<p>a\nb</p>", `text "a\nb"`, `text "a\nb"`},
		{"<!--a\rb-->", `comment "a\nb"`, `comment "a\rb"`},
		{"<!--a\r\nb-->", `comment "a\nb"`, `comment "a\r\nb"`},
		{`<p title="a` + "\r" + `b">x</p>`, `attr title="a\nb"`, `attr title="a\rb"`},
		{`<p title="a` + "\r\n" + `b">x</p>`, `attr title="a\nb"`, `attr title="a\r\nb"`},
		// A reference survives, which is the way to write a real CR.
		{`<p title="a&#13;b">x</p>`, `attr title="a\rb"`, `attr title="a&#13;b"`},
	} {
		if got := strings.Join(oracleUnits(t, tc.doc), " "); !strings.Contains(got, tc.oracle) {
			t.Errorf("%q: the oracle has %v, want it to contain %s", tc.doc, got, tc.oracle)
		}
		if got := strings.Join(libraryUnits(t, tc.doc), " "); !strings.Contains(got, tc.library) {
			t.Errorf("%q: the library has %v, want it to contain %s", tc.doc, got, tc.library)
		}
	}
}

// TestWhatAParserDoesWithANULDependsOnWhereItIs. Three answers, none of them the
// byte that was written - which is why a NUL does not round-trip through an
// insertion.
func TestWhatAParserDoesWithANULDependsOnWhereItIs(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		// Element content: dropped.
		{"<p>a\x00b</p>", `text "ab"`},
		{"<div>a\x00b</div>", `text "ab"`},
		// Raw text and comments: U+FFFD.
		{"<script>a\x00b</script>", `text "a�b"`},
		{"<style>a\x00b</style>", `text "a�b"`},
		{"<textarea>a\x00b</textarea>", `text "a�b"`},
		{"<title>a\x00b</title>", `text "a�b"`},
		{"<!--a\x00b-->", `comment "a�b"`},
		// An attribute value: kept.
		{"<p title=\"a\x00b\">x</p>", `attr title="a\x00b"`},
		// Written as a reference: U+FFFD, which is the tokenizer's rule for a
		// numeric reference to the null character.
		{"<p>a&#0;b</p>", `text "a�b"`},
	} {
		got := strings.Join(oracleUnits(t, tc.doc), " ")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: the oracle has %v, want it to contain %s", tc.doc, got, tc.want)
		}
		// The library reports the byte in every one of these positions.
		lib := strings.Join(libraryUnits(t, tc.doc), " ")
		// The formatted form of a NUL, since these are %q-quoted.
		if !strings.Contains(lib, `\x00`) && !strings.Contains(tc.doc, "&#0;") {
			t.Errorf("%q: the library reported %v, want the NUL as written", tc.doc, lib)
		}
	}
}

// TestOneLeadingNewlineInsideAPreIsDropped, and only one, and only there.
func TestOneLeadingNewlineInsideAPreIsDropped(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"<pre>\nx</pre>", `text "x"`},
		{"<pre>\n\nx</pre>", `text "\nx"`},
		{"<pre>\r\nx</pre>", `text "x"`},
		{"<listing>\nx</listing>", `text "x"`},
		{"<textarea>\nx</textarea>", `text "x"`},
		// Not at the start, and not in an ordinary element.
		{"<pre>x\n</pre>", `text "x\n"`},
		{"<p>\nx</p>", `text "\nx"`},
	} {
		got := strings.Join(oracleUnits(t, tc.doc), " ")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: the oracle has %v, want it to contain %s", tc.doc, got, tc.want)
		}
	}
	// The library keeps it, which means a rewrite that inserts a newline at the
	// start of a pre has it swallowed, and one that reads a pre's text sees a
	// character the page never showed.
	for _, doc := range []string{"<pre>\nx</pre>", "<textarea>\nx</textarea>"} {
		if got := strings.Join(libraryUnits(t, doc), " "); !strings.Contains(got, `"\nx"`) {
			t.Errorf("%q: the library has %v, want the newline kept", doc, got)
		}
	}
}

// TestCopyingAValueIsUnaffected, which is the reassuring half: both sides of a
// copy are source, so a rewrite that moves values around is exact even where the
// parsed forms differ.
func TestCopyingAValueIsUnaffected(t *testing.T) {
	const doc = "<a href=\"?a=1&amp;b=2\" title=\"x\ry\">l</a>"
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		href, _ := e.Attribute("href")
		title, _ := e.Attribute("title")
		if err := e.SetAttribute("data-href", href); err != nil {
			return err
		}
		return e.SetAttribute("data-title", title)
	}))
	if err != nil {
		t.Fatal(err)
	}
	// The parsed copies match the parsed originals, in both libraries' terms.
	got := oracleUnits(t, out)
	joined := strings.Join(got, " ")
	for _, want := range []string{
		`attr href="?a=1&b=2"`, `attr data-href="?a=1&b=2"`,
		`attr title="x\ny"`, `attr data-title="x\ny"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q: want %s in %v", out, want, got)
		}
	}
}

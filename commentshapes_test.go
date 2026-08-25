package lolhtml_test

// What a comment token can be, and how to tell which.
//
// Four pieces of source syntax become a comment token, and the delimiters are
// gone by the time a handler sees one - so a rewrite that strips comments strips
// PHP blocks and CDATA sections too. The distinguishing measurement is the source
// range against the text, and this file is that measurement: the arithmetic, why
// it is exact, and where it stops working.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// delimiterLength is the recipe: the source the token occupied, less its text, is
// what the document spelled around it.
func delimiterLength(c *lolhtml.Comment) int {
	loc := c.SourceLocation()
	return (loc.End - loc.Start) - len(c.Text())
}

// TestTheDelimiterLengthSaysWhatTheDocumentSpelled is the table the Comment
// documentation carries. 7 is <!--...--> and everything else is not.
func TestTheDelimiterLengthSaysWhatTheDocumentSpelled(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		text string
		want int
	}{
		{"<!--a-->", "a", 7},
		{"<!---a-->", "-a", 7},
		{"<!---->", "", 7},
		{"<!--a--!>", "a", 8},
		{"<!--a", "a", 4},
		{"<!-->", "", 5},
		{"<!--->", "", 6},
		{"<!bogus>", "bogus", 3},
		{"<!>", "", 3},
		{"<![CDATA[x]]>", "[CDATA[x]]", 3},
		{"<!bogus", "bogus", 2},
		{"<?php a ?>", "?php a ?", 2},
		{"<?a", "?a", 1},
	} {
		seen := 0
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			seen++
			if got := c.Text(); got != tc.text {
				t.Errorf("%q: Text = %q, want %q", tc.doc, got, tc.text)
			}
			if got := delimiterLength(c); got != tc.want {
				t.Errorf("%q: delimiter length %d, want %d", tc.doc, got, tc.want)
			}
			return nil
		})); err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
		if seen != 1 {
			t.Errorf("%q: %d comment tokens, want 1", tc.doc, seen)
		}
	}
}

// TestTheArithmeticIsExactBecauseCommentTextIsSource. The recipe would be a
// guess if the parser rewrote anything inside a comment: a CRLF normalised to a
// LF or a NUL replaced by U+FFFD would change the text's length without changing
// the source's, and the difference would stop being the delimiters. It does not
// rewrite them.
func TestTheArithmeticIsExactBecauseCommentTextIsSource(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		text string
		want int
	}{
		{"<!--a\r\nb-->", "a\r\nb", 7},
		{"<!--a\rb-->", "a\rb", 7},
		{"<!--a\x00b-->", "a\x00b", 7},
		{"<!--\r-->", "\r", 7},
		// And the same in a bogus comment, so neither direction drifts into the
		// other: four CRLFs would be exactly the four bytes between 3 and 7.
		{"<!a\r\n\r\n\r\n\r\nb>", "a\r\n\r\n\r\n\r\nb", 3},
	} {
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if got := c.Text(); got != tc.text {
				t.Errorf("%q: Text = %q, want %q - the parser rewrote it, and the "+
					"arithmetic is not exact after all", tc.doc, got, tc.text)
			}
			if got := delimiterLength(c); got != tc.want {
				t.Errorf("%q: delimiter length %d, want %d", tc.doc, got, tc.want)
			}
			return nil
		})); err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
	}
}

// TestSniffingTheTextDoesNotWork, which is why the arithmetic is needed: a
// comment can hold the text of a processing instruction.
func TestSniffingTheTextDoesNotWork(t *testing.T) {
	texts := map[string][]string{}
	for _, doc := range []string{"<!--?php echo 1; ?-->", "<?php echo 1; ?>"} {
		if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			texts[c.Text()] = append(texts[c.Text()], doc)
			return nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	for text, docs := range texts {
		if len(docs) != 2 {
			continue
		}
		t.Logf("text %q comes from %q and %q, so the text cannot say which", text, docs[0], docs[1])
		return
	}
	t.Error("the two documents reported different text, so this test is not measuring what it says")
}

// TestAStreamCanTellThemApart. The claim being pinned is that no input is needed:
// the handler sees a source range and a text, and that is enough. The document is
// fed in one-byte writes so that nothing about it is available except what the
// handler is given.
func TestAStreamCanTellThemApart(t *testing.T) {
	const doc = `<p>a</p><!--drop me--><?php keep(); ?><!--[if IE]>keep<![endif]-->` +
		`<![CDATA[keep]]><p>b</p><!--drop me too-->`
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
		if delimiterLength(c) != 7 {
			return nil // not spelled as a comment; not this pass's business
		}
		if strings.HasPrefix(c.Text(), "[if") || strings.HasPrefix(c.Text(), "!") {
			return nil // conditional or licence
		}
		c.Remove()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i++ {
		if _, err := w.Write([]byte(doc[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	const want = `<p>a</p><?php keep(); ?><!--[if IE]>keep<![endif]--><![CDATA[keep]]><p>b</p>`
	if out.String() != want {
		t.Errorf("\n got %q\nwant %q", out.String(), want)
	}
}

// TestEditingNormalisesTheDelimiters. SetText writes <!--text-->, whatever the
// document used, so a processing instruction becomes a comment. Remove is the one
// operation with no surprise in it.
func TestEditingNormalisesTheDelimiters(t *testing.T) {
	for _, tc := range []struct{ doc, set, removed string }{
		{"<?php echo 1; ?><p>x</p>", "<!--Z--><p>x</p>", "<p>x</p>"},
		{"<!bogus><p>x</p>", "<!--Z--><p>x</p>", "<p>x</p>"},
		{"<![CDATA[y]]><p>x</p>", "<!--Z--><p>x</p>", "<p>x</p>"},
		{"<!--ok--><p>x</p>", "<!--Z--><p>x</p>", "<p>x</p>"},
		{"<!--ok--!><p>x</p>", "<!--Z--><p>x</p>", "<p>x</p>"},
	} {
		got, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText("Z")
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.set {
			t.Errorf("SetText on %q\n got %q\nwant %q", tc.doc, got, tc.set)
		}
		got, err = lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.removed {
			t.Errorf("Remove on %q\n got %q\nwant %q", tc.doc, got, tc.removed)
		}
	}
}

// TestCDATAInForeignContentIsNotACommentToken, so the same bytes are one thing in
// HTML and another inside an <svg>. Nothing to tell apart there: no comment
// handler runs at all.
func TestCDATAInForeignContentIsNotACommentToken(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		comments int
	}{
		{"<![CDATA[x]]>", 1},
		{"<svg><![CDATA[x]]></svg>", 0},
		{"<math><![CDATA[x]]></math>", 0},
	} {
		got := 0
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			got++
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if got != tc.comments {
			t.Errorf("%q: %d comment tokens, want %d", tc.doc, got, tc.comments)
		}
	}
}

package lolhtml_test

// What a handler that only reads costs the document.
//
// The answer is nothing, for every kind of handler but one. The text path decodes
// and re-encodes, so a document holding bytes that are not valid in the declared
// encoding comes out different for having had a text handler registered - whether
// or not that handler looked at anything. Instrumentation added to a rewrite that
// has to be byte-exact is where this bites.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// readOnly are handlers that observe and change nothing.
func readOnly() []struct {
	name string
	opts []lolhtml.Option
} {
	return []struct {
		name string
		opts []lolhtml.Option
	}{
		{"none", nil},
		{"an element handler that reads", []lolhtml.Option{
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				_ = e.TagName()
				_ = e.AttributeList()
				_ = e.SourceLocation()
				return nil
			})}},
		{"a comment handler that reads", []lolhtml.Option{
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				_ = c.Text()
				return nil
			})}},
		{"a doctype handler that reads", []lolhtml.Option{
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				_, _ = d.Name()
				return nil
			})}},
		{"an end tag handler that reads", []lolhtml.Option{
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					_ = t.Name()
					return nil
				})
			})}},
	}
}

// TestAReadOnlyRewriteIsByteIdentical, for every kind of handler except the text
// one, on documents that include bytes no encoding could decode.
func TestAReadOnlyRewriteIsByteIdentical(t *testing.T) {
	docs := []string{
		`<!DOCTYPE html><html><body><p class="a">café</p><!--note--></body></html>`,
		"<p>caf\xe9</p>",
		"<p>a\xffb</p>",
		"<p title=\"caf\xe9\">x</p>",
		"<!--caf\xe9-->",
		"<script>caf\xe9</script>",
		"<ul><li>a<li>b</ul>",
		"",
	}
	for _, doc := range docs {
		for _, kind := range readOnly() {
			out, err := lolhtml.RewriteString(doc, kind.opts...)
			if err != nil {
				t.Errorf("%q with %s: %v", doc, kind.name, err)
				continue
			}
			if out != doc {
				t.Errorf("%q with %s came out as %q", doc, kind.name, out)
			}
		}
	}
}

// TestATextHandlerReEncodesWhetherItReadsOrNot, which is the exception and the
// reason the sentence above needs the word "except".
func TestATextHandlerReEncodesWhetherItReadsOrNot(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"<p>caf\xe9</p>", "<p>caf�</p>"},
		{"<p>a\xffb</p>", "<p>a�b</p>"},
		{"<script>caf\xe9</script>", "<script>caf�</script>"},
		// Valid UTF-8 is untouched, which is why this is easy to miss.
		{"<p>café</p>", "<p>café</p>"},
		{"<p>a &amp; b</p>", "<p>a &amp; b</p>"},
	} {
		for _, name := range []string{"reading", "ignoring"} {
			handler := func(c *lolhtml.TextChunk) error {
				if name == "reading" {
					_ = c.Text()
				}
				return nil
			}
			out, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentText(handler))
			if err != nil {
				t.Fatalf("%q %s: %v", tc.doc, name, err)
			}
			if out != tc.want {
				t.Errorf("%q with a text handler %s\n got %q\nwant %q", tc.doc, name, out, tc.want)
			}
		}
	}
}

// TestTheOtherPathsKeepTheirBytes, stated as its own test because it is the
// asymmetry rather than the exception: the same undecodable bytes survive in a
// comment and in an attribute value while a text handler is registered.
func TestTheOtherPathsKeepTheirBytes(t *testing.T) {
	const doc = "<p title=\"caf\xe9\">text</p><!--caf\xe9-->"
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			_ = c.Text()
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			_ = c.Text()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			_ = e.AttributeList()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != doc {
		t.Errorf("\n got %q\nwant %q - the text of that document is all valid, so "+
			"nothing should have been re-encoded", out, doc)
	}
	// And the text path, on the same document, with the bad bytes in the text.
	const inText = "<p title=\"caf\xe9\">caf\xe9</p><!--caf\xe9-->"
	out, err = lolhtml.RewriteString(inText, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "�") != 1 {
		t.Errorf("got %q, want exactly one replacement - the text's, not the "+
			"attribute's or the comment's", out)
	}
	if !strings.Contains(out, "title=\"caf\xe9\"") || !strings.Contains(out, "<!--caf\xe9-->") {
		t.Errorf("got %q, want the attribute and the comment untouched", out)
	}
}

// TestWritingToDiscardMakesTheQuestionMoot, which is the answer for a program
// whose output is a report rather than a document.
func TestWritingToDiscardMakesTheQuestionMoot(t *testing.T) {
	const doc = "<h1>caf\xe9</h1><h3>skipped a level</h3>"
	levels := []string{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("h1,h2,h3", func(e *lolhtml.Element) error {
		levels = append(levels, e.TagName())
		return nil
	}), lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		_ = c.Text()
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if strings.Join(levels, ",") != "h1,h3" {
		t.Errorf("levels = %v", levels)
	}
}

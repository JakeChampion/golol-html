package lolhtml_test

// What ContentType does per context.
//
// Text escapes <, > and & and nothing else, wherever the content lands. The
// choice does not consult the element it is landing in, and there are two
// elements where that matters: <script> and <style> are raw text, so an HTML
// parser does not decode references inside them. Text there is inert but
// corrupted; HTML there is verbatim but can end the element.
//
// Both failures are quiet - valid HTML out, no error - so they are pinned here.
// If either of these tests changes, the escaping became context-sensitive and
// the package documentation on inserting into a script needs rewriting.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestTextEscapesExactlyThreeCharacters. The set matters: escaping a quote as
// well would be harmless here but would corrupt attribute-shaped text, and
// escaping less would be an injection.
func TestTextEscapesExactlyThreeCharacters(t *testing.T) {
	tests := []struct{ in, want string }{
		{"<", "&lt;"},
		{">", "&gt;"},
		{"&", "&amp;"},
		{"&amp;", "&amp;amp;"},
		{`"`, `"`},
		{"'", "'"},
		{"`", "`"},
		{"-", "-"},
		{"-->", "--&gt;"},
		// A NUL is not escaped and not replaced: it is emitted as a zero byte.
		// Any parser reading the output turns it into U+FFFD, so a value holding
		// one does not survive a round trip.
		{"\x00", "\x00"},
		{"plain", "plain"},
	}
	for _, tt := range tests {
		out, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent(tt.in, lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if want := "<p>" + tt.want + "</p>"; out != want {
			t.Errorf("Text %q\n got: %s\nwant: %s", tt.in, out, want)
		}
	}
}

// TestTextIntoRawTextElementsIsCorruptedNotDecoded is the quiet half. A parser
// does not decode references in a script or a style, so the escaped form is what
// the script actually runs.
func TestTextIntoRawTextElementsIsCorruptedNotDecoded(t *testing.T) {
	tests := []struct{ tag, in, want string }{
		{"script", `if (a < b && c > d) {}`, `if (a &lt; b &amp;&amp; c &gt; d) {}`},
		{"style", `a > b { content: "&"; }`, `a &gt; b { content: "&amp;"; }`},
		// Escapable raw text: a parser does decode these, so Text is right.
		{"textarea", "a < b", "a &lt; b"},
		{"title", "a < b", "a &lt; b"},
	}
	for _, tt := range tests {
		in := "<" + tt.tag + "></" + tt.tag + ">"
		out, err := lolhtml.RewriteString(in,
			lolhtml.OnElement(tt.tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent(tt.in, lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.tag, err)
		}
		if want := "<" + tt.tag + ">" + tt.want + "</" + tt.tag + ">"; out != want {
			t.Errorf("%s + Text\n got: %s\nwant: %s", tt.tag, out, want)
		}
	}
}

// TestHTMLIntoAScriptCanEndTheElement is the other half, and the reason the
// documentation tells callers not to put untrusted data in a script body at all.
// HTML means raw markup, so this is the contract rather than a defect - but it
// is asserted so that nobody "fixes" the corruption above by switching to HTML
// without noticing what it allows.
func TestHTMLIntoAScriptCanEndTheElement(t *testing.T) {
	out, err := lolhtml.RewriteString(`<script></script>`,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`var s = "</script><img src=1 onerror=alert(1)>";`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `</script><img src=1`) {
		t.Fatalf("expected the script to be ended by its own content: %s", out)
	}
	if strings.Count(out, "</script>") != 2 {
		t.Errorf("expected two end tags, one from the content: %s", out)
	}
}

// TestCommentTextRefusesAClosingSequence is the context where lol-html does
// check, and the model for what the script context is missing.
func TestCommentTextRefusesAClosingSequence(t *testing.T) {
	_, err := lolhtml.RewriteString(`<!--c-->`,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText("a --> b")
		}))
	if err == nil {
		t.Fatal("SetText accepted a comment-closing sequence")
	}
	if !strings.Contains(err.Error(), "comment-closing sequence") {
		t.Errorf("error does not explain itself: %v", err)
	}
}

// TestTextIntoACommentWeConstructOurselves: assembling a comment out of HTML
// delimiters and Text content is safe, because a comment can only be ended by a
// literal > and Text escapes it. Pinned because it is the pattern a report
// comment uses, and it would be an injection if Text ever stopped escaping >.
func TestTextIntoACommentWeConstructOurselves(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if err := d.Append("<!-- note: ", lolhtml.HTML); err != nil {
				return err
			}
			if err := d.Append(`a --> <img src=1 onerror=alert(1)>`, lolhtml.Text); err != nil {
				return err
			}
			return d.Append(" -->", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "-->") != 1 {
		t.Errorf("the payload escaped the comment: %s", out)
	}
	if strings.Contains(out, "<img") {
		t.Errorf("markup survived inside the comment: %s", out)
	}
}

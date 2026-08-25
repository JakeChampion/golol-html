package lolhtml_test

// What Comment.SetText accepts and what it refuses.
//
// Its documentation said the value "is escaped so that it cannot terminate the
// comment early, so untrusted input is safe". It is refused rather than escaped,
// and the difference is what a caller has to handle: passing arbitrary text
// fails the rewrite instead of producing a sanitised comment.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// setText applies SetText to the document's one comment.
func setText(t *testing.T, text string) (string, error) {
	t.Helper()
	return lolhtml.RewriteString(`<div><!--x--></div>`,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText(text)
		}))
}

func TestCommentSetTextRefusesAClosingSequence(t *testing.T) {
	refused := []string{
		"-->",
		"--!>",
		"->",
		"a-->b",
		"a--!>b",
		"<!-->",
		"--><img src=x onerror=alert(1)><!--",
	}
	for _, text := range refused {
		out, err := setText(t, text)
		if err == nil {
			t.Errorf("SetText(%q) was accepted, giving %q", text, out)
			continue
		}
		// The message has to name the problem, since the caller's next move is
		// to decide what to do with the value.
		if !strings.Contains(err.Error(), "comment-closing sequence") {
			t.Errorf("SetText(%q): %v", text, err)
		}
	}
}

// What looks close and is fine. Each has to come back as one comment, which is
// the property the refusal protects.
func TestCommentSetTextAcceptsWhatCannotClose(t *testing.T) {
	accepted := []string{
		"plain",
		"--",
		"--!",
		"<!--",
		"a--b",
		"caf&eacute;",
		"a & b",
	}
	for _, text := range accepted {
		out, err := setText(t, text)
		if err != nil {
			t.Errorf("SetText(%q) was refused: %v", text, err)
			continue
		}
		comments, elements := countComments(t, out)
		if comments != 1 {
			t.Errorf("SetText(%q) gave %d comments in %q, want 1", text, comments, out)
		}
		if elements != 1 {
			t.Errorf("SetText(%q) gave %d elements in %q, want just the div",
				text, elements, out)
		}
	}
}

// countComments re-parses markup and counts its comments and elements, which is
// the only way to ask whether the comment stayed one comment.
func countComments(t *testing.T, doc string) (comments, elements int) {
	t.Helper()
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { comments++; return nil }),
		lolhtml.OnElement("*", func(*lolhtml.Element) error { elements++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	return comments, elements
}

// A comment built by hand has no guard, which is the gap the documentation now
// names. This is what it looks like.
func TestAHandBuiltCommentHasNoGuard(t *testing.T) {
	const title = `--><img src=x onerror=alert(1)><!--`

	out, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.Append("<!-- "+title+" -->", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	_, elements := countComments(t, out)
	if elements < 2 {
		t.Fatalf("expected the image to have escaped the comment: %q", out)
	}

	// EscapeText stops it, at the cost of what the comment says.
	out, err = lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.Append("<!-- "+lolhtml.EscapeText(title)+" -->", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	comments, elements := countComments(t, out)
	if elements != 1 || comments != 1 {
		t.Errorf("escaping did not keep it inside one comment: %q", out)
	}
	if !strings.Contains(out, "--&gt;") {
		t.Errorf("the escaped form is not in the output: %q", out)
	}
}

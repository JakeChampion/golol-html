package lolhtml_test

// What a comment handler sees.
//
// "Comment" here is the HTML parser's word, not the author's. The spec turns
// several malformed constructs into bogus comments, and those arrive as comments
// - so a rewrite that removes every comment also removes PHP blocks, XML
// declarations and processing instructions. Each of them is a well-formed comment
// as far as the parser is concerned, so there is no error and nothing to notice
// except the missing code.
//
// The other half is conditional comments, which are not one comment: the
// downlevel-revealed form is two, with real markup between them, and only the
// first contains "[if".

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// comments extracts what a comment handler sees, as text.
func comments(t *testing.T, doc string) []string {
	t.Helper()

	var seen []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			seen = append(seen, c.Text())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return seen
}

// TestBogusCommentsAreComments is the finding. Each of these is code, and each
// arrives as a comment.
func TestBogusCommentsAreComments(t *testing.T) {
	tests := []struct {
		doc  string
		want []string
	}{
		{`<!--plain-->`, []string{"plain"}},
		{`<!---->`, []string{""}},
		{`<!-->`, []string{""}},
		{`<!--->`, []string{""}},
		{`<!-- unterminated`, []string{" unterminated"}},

		// The ones that are not prose.
		{`<?php echo "hi"; ?>`, []string{`?php echo "hi"; ?`}},
		{`<?xml version="1.0"?>`, []string{`?xml version="1.0"?`}},
		{`<!bogus>`, []string{"bogus"}},
		{`<! spaced>`, []string{" spaced"}},

		// And the ones that are not comments.
		{`<!DOCTYPE html><!DOCTYPE again>`, nil},
		{`</bogus end tag>`, nil},
		{`<script><!--in script--></script>`, nil},
		{`<style><!--in style--></style>`, nil},
		{`<textarea><!--in textarea--></textarea>`, nil},

		// A nested comment ends at the first close, leaving the rest as text.
		{`<!-- nested <!-- inner --> outer -->`, []string{" nested <!-- inner "}},
		{`<!--a--><!--b-->`, []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.doc, func(t *testing.T) {
			got := comments(t, tt.doc)
			if len(got) != len(tt.want) {
				t.Fatalf("saw %d comments %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("comment %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestRemovingEveryCommentRemovesCode is the consequence, spelled out so it
// cannot be mistaken for anything else.
func TestRemovingEveryCommentRemovesCode(t *testing.T) {
	doc := `<p>a</p><?php echo "hi"; ?><p>b</p>`

	got, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>a</p><p>b</p>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}

	// The way to keep them: match on the text rather than removing everything.
	// A bogus comment's text begins with the character that opened it.
	got, err = lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if strings.HasPrefix(c.Text(), "?") || strings.HasPrefix(c.Text(), "!") {
				return nil
			}
			c.Remove()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got != doc {
		t.Errorf("the php block was not preserved:\n got: %s\nwant: %s", got, doc)
	}
}

// TestConditionalCommentsAreNotOneComment: the downlevel-revealed form is two
// comments with markup between them, and a filter keyed on "[if" keeps the first
// and drops the second.
func TestConditionalCommentsAreNotOneComment(t *testing.T) {
	t.Run("downlevel hidden is one comment", func(t *testing.T) {
		got := comments(t, `<!--[if IE]><p>ie</p><![endif]-->`)
		if len(got) != 1 {
			t.Fatalf("saw %d comments, want 1: %q", len(got), got)
		}
		if !strings.HasPrefix(got[0], "[if IE]") {
			t.Errorf("comment = %q", got[0])
		}
	})

	t.Run("downlevel revealed is two", func(t *testing.T) {
		got := comments(t, `<!--[if !IE]><!--><p>modern</p><!--<![endif]-->`)
		if len(got) != 2 {
			t.Fatalf("saw %d comments, want 2: %q", len(got), got)
		}
		if !strings.Contains(got[0], "[if") {
			t.Errorf("the opening half should contain [if: %q", got[0])
		}
		if strings.Contains(got[1], "[if") {
			t.Errorf("the closing half should not contain [if, which is the trap: %q", got[1])
		}
	})

	t.Run("a filter keyed on [if breaks the revealed form", func(t *testing.T) {
		doc := `<!--[if !IE]><!--><p>modern</p><!--<![endif]-->`
		got, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				if strings.Contains(c.Text(), "[if") {
					return nil
				}
				c.Remove()
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		// The closing half is gone, so the conditional no longer closes.
		if strings.Contains(got, "endif") {
			t.Errorf("expected the closing half to have been removed: %s", got)
		}
		if !strings.Contains(got, "[if !IE]") {
			t.Errorf("expected the opening half to survive: %s", got)
		}
	})

	t.Run("keeping both halves needs the endif too", func(t *testing.T) {
		doc := `<!--[if !IE]><!--><p>modern</p><!--<![endif]-->`
		got, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				text := c.Text()
				if strings.Contains(text, "[if") || strings.Contains(text, "[endif]") {
					return nil
				}
				c.Remove()
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if got != doc {
			t.Errorf("\n got: %s\nwant: %s", got, doc)
		}
	})
}

// TestABogusCommentIsIndistinguishableByItsText, except for the ones opened with
// "?", whose text keeps the "?". Everything else looks like a comment with the
// same content, and SourceLocation against the input is the only discriminator.
func TestABogusCommentIsIndistinguishableByItsText(t *testing.T) {
	// Same text, different construct.
	for _, doc := range []string{`<!--x-->`, `<!x>`} {
		if got := comments(t, doc); len(got) != 1 || got[0] != "x" {
			t.Errorf("%s: text = %q, want [x]", doc, got)
		}
	}

	// The source range tells them apart.
	for _, tt := range []struct {
		doc  string
		real bool
	}{
		{`<!--x-->`, true},
		{`<!x>`, false},
		{`<!bogus>`, false},
		{`<! spaced>`, false},
		{`<?php x ?>`, false},
		{`<!-->`, true},
	} {
		var real bool
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				loc := c.SourceLocation()
				src := tt.doc[loc.Start:loc.End]
				real = strings.HasPrefix(src, "<!--")
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if real != tt.real {
			t.Errorf("%s: source says real-comment=%v, want %v", tt.doc, real, tt.real)
		}
	}
}

// TestCommentSourceLocationCoversTheDelimiters, so a report can point at the
// whole construct rather than its contents.
func TestCommentSourceLocationCoversTheDelimiters(t *testing.T) {
	doc := `<p>a</p><!--note--><p>b</p>`

	var loc lolhtml.SourceLocation
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			loc = c.SourceLocation()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if got := doc[loc.Start:loc.End]; got != `<!--note-->` {
		t.Errorf("range sliced %q, want the whole comment", got)
	}
}

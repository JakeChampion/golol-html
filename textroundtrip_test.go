package lolhtml_test

// Transforming text and writing it back.
//
// TextChunk.Text is source, so a transform reads "caf&eacute;" and not "café".
// Of the three obvious ways to write the result back, two are wrong and both
// look right on text that happens to contain no character references - which is
// how the recipe in the package documentation stayed wrong.

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// mixed contains a literal "<", an escaped ampersand and a named reference: the
// three things a round trip can damage.
const mixed = `<p>a < b &amp; caf&eacute;</p>`

// upperText applies strings.ToUpper the given way, and returns the output of one
// pass and of a second pass over that output.
func upperText(t *testing.T, doc string, replace func(*lolhtml.TextChunk, string) error) (string, string) {
	t.Helper()
	opts := func() lolhtml.Option {
		return lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
			if len(c.Bytes()) == 0 {
				return nil
			}
			return replace(c, c.Text())
		})
	}
	once, err := lolhtml.RewriteString(doc, opts())
	if err != nil {
		t.Fatal(err)
	}
	twice, err := lolhtml.RewriteString(once, opts())
	if err != nil {
		t.Fatal(err)
	}
	return once, twice
}

func TestTheThreeWaysToWriteTextBack(t *testing.T) {
	tests := []struct {
		name        string
		replace     func(*lolhtml.TextChunk, string) error
		once, twice string
		rendersAs   string // what a reader sees for `once`
		idempotent  bool
	}{
		{
			name: "as Text, without decoding",
			replace: func(c *lolhtml.TextChunk, s string) error {
				return c.Replace(strings.ToUpper(s), lolhtml.Text)
			},
			once:      `<p>A &lt; B &amp;AMP; CAF&amp;EACUTE;</p>`,
			twice:     `<p>A &amp;LT; B &amp;AMP;AMP; CAF&amp;AMP;EACUTE;</p>`,
			rendersAs: "A < B &AMP; CAF&EACUTE;",
		},
		{
			name: "as HTML, without decoding",
			replace: func(c *lolhtml.TextChunk, s string) error {
				return c.Replace(strings.ToUpper(s), lolhtml.HTML)
			},
			once:       `<p>A < B &AMP; CAF&EACUTE;</p>`,
			twice:      `<p>A < B &AMP; CAF&EACUTE;</p>`,
			rendersAs:  "A < B & CAF&EACUTE;",
			idempotent: true,
		},
		{
			name: "decoded, then as Text",
			replace: func(c *lolhtml.TextChunk, s string) error {
				return c.Replace(strings.ToUpper(stdhtml.UnescapeString(s)), lolhtml.Text)
			},
			once:       `<p>A &lt; B &amp; CAFÉ</p>`,
			twice:      `<p>A &lt; B &amp; CAFÉ</p>`,
			rendersAs:  "A < B & CAFÉ",
			idempotent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			once, twice := upperText(t, mixed, tt.replace)
			if once != tt.once {
				t.Errorf("one pass  = %q, want %q", once, tt.once)
			}
			if twice != tt.twice {
				t.Errorf("two passes = %q, want %q", twice, tt.twice)
			}
			if (once == twice) != tt.idempotent {
				t.Errorf("idempotent = %v, want %v", once == twice, tt.idempotent)
			}
			// What a reader would see, which is the thing that is actually
			// wrong in two of these three.
			if got := renderedText(t, once); got != tt.rendersAs {
				t.Errorf("renders as %q, want %q", got, tt.rendersAs)
			}
		})
	}
}

// renderedText decodes the text of the paragraph, which is what a reader sees.
func renderedText(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return stdhtml.UnescapeString(b.String())
}

// The recipe in the package documentation, transcribed, on text that contains a
// character reference. Without the decode it produced "CAF&amp;EACUTE;", which
// renders as those characters; the example it was written against had no
// references in it and looked correct.
func TestTheWholeTextRecipeFromTheDocumentation(t *testing.T) {
	const doc = `<a href="/x">caf&eacute; <b>&amp; more</b></a>`
	rewrite := strings.ToUpper

	run := func(decode bool) string {
		var acc strings.Builder
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				acc.Reset()
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					s := acc.String()
					if decode {
						s = stdhtml.UnescapeString(s)
					}
					return t.Before(rewrite(s), lolhtml.Text)
				})
			}),
			lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
				acc.WriteString(tc.Text())
				tc.Remove()
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got, want := run(false), `<a href="/x"><b></b>CAF&amp;EACUTE; &amp;AMP; MORE</a>`; got != want {
		t.Errorf("without decoding: got %q, want %q", got, want)
	}
	if got, want := run(true), `<a href="/x"><b></b>CAFÉ &amp; MORE</a>`; got != want {
		t.Errorf("with decoding: got %q, want %q", got, want)
	}
}

// Text with no character references in it makes all three look the same, which
// is why this went unnoticed.
func TestTextWithNoReferencesHidesTheDifference(t *testing.T) {
	const plain = `<p>click here</p>`
	ways := []func(*lolhtml.TextChunk, string) error{
		func(c *lolhtml.TextChunk, s string) error {
			return c.Replace(strings.ToUpper(s), lolhtml.Text)
		},
		func(c *lolhtml.TextChunk, s string) error {
			return c.Replace(strings.ToUpper(s), lolhtml.HTML)
		},
		func(c *lolhtml.TextChunk, s string) error {
			return c.Replace(strings.ToUpper(stdhtml.UnescapeString(s)), lolhtml.Text)
		},
	}
	for i, w := range ways {
		once, twice := upperText(t, plain, w)
		if once != `<p>CLICK HERE</p>` || twice != once {
			t.Errorf("way %d: once=%q twice=%q; on text with no references all "+
				"three are supposed to agree", i, once, twice)
		}
	}
}

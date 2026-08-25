package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func convert(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Convert(&b, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Convert(%q): %v", doc, err)
	}
	return b.String(), res
}

func TestQuotesAndDashes(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"a pair of doubles", `<p>"a"</p>`, `<p>“a”</p>`},
		{"two pairs", `<p>"a" and "b"</p>`, `<p>“a” and “b”</p>`},
		{"a pair of singles", `<p>'a'</p>`, `<p>‘a’</p>`},
		{"an apostrophe", `<p>don't</p>`, `<p>don’t</p>`},
		{"an apostrophe and a pair", `<p>'don't'</p>`, `<p>‘don’t’</p>`},
		{"a trailing apostrophe", `<p>dogs' bowls</p>`, `<p>dogs’ bowls</p>`},
		{"an en dash", `<p>a--b</p>`, `<p>a–b</p>`},
		{"an em dash", `<p>a---b</p>`, `<p>a—b</p>`},
		{"a hyphen is left alone", `<p>well-known</p>`, `<p>well-known</p>`},
		{"an ellipsis", `<p>and...</p>`, `<p>and…</p>`},
		{"two dots are left alone", `<p>a..b</p>`, `<p>a..b</p>`},
		{"nothing to do", `<p>plain text</p>`, `<p>plain text</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, _ := convert(t, tt.doc); out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
		})
	}
}

// A quotation is a pattern that spans more than the text node it starts in, which
// is the reason the pairing state lives outside the handler.
func TestAQuotationSpansMarkup(t *testing.T) {
	tests := []struct{ doc, want string }{
		{`<p>"a <b>b</b> c"</p>`, `<p>“a <b>b</b> c”</p>`},
		{`<p>"a <em>b <strong>c</strong></em>"</p>`, `<p>“a <em>b <strong>c</strong></em>”</p>`},
		{`<p><b>"a"</b></p>`, `<p><b>“a”</b></p>`},
		// The opening quote is in one node and the closing quote three nodes
		// later.
		{`<p>"<b>a</b><i>b</i>c"</p>`, `<p>“<b>a</b><i>b</i>c”</p>`},
	}
	for _, tt := range tests {
		if out, _ := convert(t, tt.doc); out != tt.want {
			t.Errorf("%q: got %q, want %q", tt.doc, out, tt.want)
		}
	}
}

// A block ends a quotation, so an unclosed quote cannot reach into the next
// paragraph - and the count says how many were left open, which is the signal
// that a page is using the character for something else.
func TestABlockEndsAQuotation(t *testing.T) {
	out, res := convert(t, `<p>"unclosed</p><p>"a"</p>`)
	if want := `<p>“unclosed</p><p>“a”</p>`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if res.Unclosed != 1 {
		t.Errorf("Unclosed = %d, want 1", res.Unclosed)
	}

	// Inches, which is what an unclosed double quote usually is.
	out, res = convert(t, `<p>a 3" pipe</p><p>a 4" pipe</p>`)
	if res.Unclosed != 2 {
		t.Errorf("Unclosed = %d, want 2 for two paragraphs of inches: %q", res.Unclosed, out)
	}
}

// The context on each side decides what a single quote is, which is one rule
// rather than a mode.
func TestApostropheVersusClosingQuote(t *testing.T) {
	tests := []struct{ doc, want string }{
		{`<p>it's</p>`, `<p>it’s</p>`},
		{`<p>'quoted'</p>`, `<p>‘quoted’</p>`},
		{`<p>'it's'</p>`, `<p>‘it’s’</p>`},
		{`<p>the '90s</p>`, `<p>the ‘90s</p>`},
		{`<p>rock 'n' roll</p>`, `<p>rock ‘n’ roll</p>`},
	}
	for _, tt := range tests {
		if out, _ := convert(t, tt.doc); out != tt.want {
			t.Errorf("%q: got %q, want %q", tt.doc, out, tt.want)
		}
	}
}

// Code is not prose, and applying typography to it changes what it means.
func TestVerbatimElementsAreUntouched(t *testing.T) {
	for _, doc := range []string{
		`<code>a "b" -- c...</code>`,
		`<pre>x = "y" -- z</pre>`,
		`<kbd>ctrl--c</kbd>`,
		`<samp>"out"</samp>`,
		`<script>var s = "a" -- 1</script>`,
		`<style>p{content:"a"}</style>`,
		`<textarea>"a"</textarea>`,
		`<title>"a"</title>`,
	} {
		out, res := convert(t, doc)
		if out != doc {
			t.Errorf("%q became %q", doc, out)
		}
		if res.Total() != 0 {
			t.Errorf("%q: %d substitutions", doc, res.Total())
		}
	}
}

// A quotation that opens in prose and meets a code sample keeps its state on the
// prose side, which is the behaviour that makes mixed content work.
func TestProseAroundCode(t *testing.T) {
	out, _ := convert(t, `<p>He said "use <code>a "b"</code> here"</p>`)
	want := `<p>He said “use <code>a "b"</code> here”</p>`
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// Nothing this program writes can become markup, because every insertion is Text
// and every replacement is a character.
func TestTheTagsNeverChange(t *testing.T) {
	for _, doc := range []string{
		`<p>"a"</p>`,
		`<p>&lt;script&gt;"a"&lt;/script&gt;</p>`,
		`<p>"a" &amp; 'b' -- c...</p>`,
		`<div><p>"a <b>b</b>"</p></div>`,
	} {
		out, _ := convert(t, doc)
		if before, after := tagSequence(t, doc), tagSequence(t, out); before != after {
			t.Errorf("%q: tags went from %s to %s: %q", doc, before, after, out)
		}
	}
}

// Running it again changes nothing: what it wrote is not what it looks for.
func TestConvertingTwiceChangesNothing(t *testing.T) {
	const doc = `<p>He said "a" and don't -- really...</p>`
	once, res1 := convert(t, doc)
	if res1.Total() == 0 {
		t.Fatal("the first pass changed nothing")
	}
	twice, res2 := convert(t, once)
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Total() != 0 {
		t.Errorf("the second pass made %d substitutions", res2.Total())
	}
}

// References come through as they were, since nothing decodes and re-encodes
// them.
func TestReferencesSurvive(t *testing.T) {
	out, _ := convert(t, `<p>caf&eacute; "a" &amp; b</p>`)
	if !strings.Contains(out, "caf") || !strings.Contains(out, "“a”") {
		t.Errorf("got %q", out)
	}
	if strings.Contains(out, "&amp;amp;") {
		t.Errorf("a reference was escaped twice: %q", out)
	}
}

func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>He said "a <b>b</b> c" and don't -- really...</p>`,
		`<code>a "b"</code><p>"c"</p>`,
		`<p>"unclosed</p><p>"a"</p>`,
		`<p>rock 'n' roll</p>`,
		`<p>plain</p>`,
	}
	for _, doc := range docs {
		want, wantRes := convert(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Convert(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Total() != wantRes.Total() {
				t.Fatalf("%q at writes of %d:\n got %q (%d)\nwant %q (%d)",
					doc, n, b.String(), res.Total(), want, wantRes.Total())
			}
		}
	}
}

func tagSequence(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

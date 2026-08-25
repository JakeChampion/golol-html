package main

import (
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func highlight(t *testing.T, doc string, terms ...string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Highlight(&b, strings.NewReader(doc), terms, DefaultMarks)
	if err != nil {
		t.Fatalf("Highlight(%q, %q): %v", doc, terms, err)
	}
	return b.String(), res
}

// tagSequence is the sequence of tags the rewriter reports, which is what the
// program promises not to change.
func tagSequence(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			b.WriteString("<" + e.TagName() + ">")
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			b.WriteString("!")
			return nil
		}),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
			b.WriteString("D")
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// corpus is aimed at everything that could turn an insertion into markup.
var corpus = []string{
	`<p>streaming</p>`,
	`<p>a streaming b</p><div>streaming</div>`,
	`<!DOCTYPE html><html><head><title>streaming</title></head><body><p>streaming</p></body></html>`,
	`<p>caf&eacute; and &amp; and &lt;streaming&gt;</p>`,
	`<p title="streaming">streaming</p>`,
	`<script>var streaming = "<p>"</script><p>streaming</p>`,
	`<style>p{content:"streaming"}</style><p>streaming</p>`,
	`<textarea>streaming</textarea><p>streaming</p>`,
	`<p>a</p><!-- streaming --><p>streaming</p>`,
	`<table><tr><td>streaming</table>`,
	`<svg><rect/></svg><p>streaming</p>`,
	`<ul><li>streaming<li>streaming</ul>`,
	`<p>streaming <b>streaming</b> streaming</p>`,
	`<xmp>streaming</xmp><p>streaming</p>`,
}

// TestTheTagsNeverChange is the promise: whatever the terms, whatever the
// document, the markup gains no tag.
func TestTheTagsNeverChange(t *testing.T) {
	// Terms chosen to include the characters that would be markup if they were
	// not escaped.
	termSets := [][]string{
		{"streaming"},
		{"streaming", "café"},
		{"a", "b"},
		{"<script>"},
		{"&"},
		{"p"},
	}
	for _, doc := range corpus {
		want := tagSequence(t, doc)
		for _, terms := range termSets {
			out, _ := highlight(t, doc, terms...)
			if got := tagSequence(t, out); got != want {
				t.Errorf("terms %q on %q changed the tags\n from %s\n to   %s\n out  %q",
					terms, doc, want, got, out)
			}
		}
	}
}

// A term that is markup is marked as text, not as markup.
func TestATermThatLooksLikeMarkupIsEscaped(t *testing.T) {
	out, res := highlight(t, `<p>a &lt;script&gt; here</p>`, "<script>")
	if res.Total != 1 {
		t.Fatalf("marked %d times, want 1: %q", res.Total, out)
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("a script tag reached the output: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("the term was not escaped: %q", out)
	}
	// And the marks are there, around it.
	if !strings.Contains(out, "«&lt;script&gt;»") {
		t.Errorf("the marks are not around the term: %q", out)
	}
}

func TestMatching(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		terms []string
		marks int
	}{
		{"one term", `<p>streaming</p>`, []string{"streaming"}, 1},
		{"every occurrence", `<p>a a a</p>`, []string{"a"}, 3},
		{"case insensitive", `<p>Streaming STREAMING</p>`, []string{"streaming"}, 2},
		{"whole words only", `<p>stream streaming streams</p>`, []string{"stream"}, 1},
		{"longest term wins", `<p>streaming rewrite</p>`, []string{"streaming", "streaming rewrite"}, 1},
		{"entity decoded before matching", `<p>caf&eacute;</p>`, []string{"café"}, 1},
		{"ampersand as a term", `<p>a &amp; b</p>`, []string{"&"}, 1},
		{"no match", `<p>nothing</p>`, []string{"streaming"}, 0},
		{"no terms", `<p>streaming</p>`, nil, 0},
		{"empty term ignored", `<p>streaming</p>`, []string{"", "  "}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := highlight(t, tt.doc, tt.terms...)
			if res.Total != tt.marks {
				t.Errorf("marked %d times, want %d: %q", res.Total, tt.marks, out)
			}
		})
	}
}

// Where nothing is marked, because escaping there would corrupt rather than
// protect.
func TestSkippedElements(t *testing.T) {
	for _, doc := range []string{
		`<script>streaming</script>`,
		`<style>streaming</style>`,
		`<title>streaming</title>`,
		`<textarea>streaming</textarea>`,
		`<template>streaming</template>`,
		`<xmp>streaming</xmp>`,
	} {
		out, res := highlight(t, doc, "streaming")
		if res.Total != 0 {
			t.Errorf("%q: marked %d times, giving %q", doc, res.Total, out)
		}
		if out != doc {
			t.Errorf("%q changed to %q", doc, out)
		}
	}
}

// Attributes are not text, and a term in one is left alone - which also means the
// program cannot break an attribute by writing into it.
func TestAttributesAreUntouched(t *testing.T) {
	const doc = `<p title="streaming" data-x="streaming">streaming</p>`
	out, res := highlight(t, doc, "streaming")
	if res.Total != 1 {
		t.Errorf("marked %d times, want 1: %q", res.Total, out)
	}
	if !strings.Contains(out, `title="streaming"`) || !strings.Contains(out, `data-x="streaming"`) {
		t.Errorf("an attribute was changed: %q", out)
	}
}

// What a reader sees is the original text with marks added, and nothing else -
// checked by decoding both and removing the marks.
func TestTheTextIsOtherwiseUnchanged(t *testing.T) {
	for _, doc := range corpus {
		out, _ := highlight(t, doc, "streaming", "café")
		before, after := renderedText(t, doc), renderedText(t, out)
		stripped := strings.NewReplacer(DefaultMarks.Open, "", DefaultMarks.Close, "").Replace(after)
		if stripped != before {
			t.Errorf("%q: text changed\n before %q\n after  %q", doc, before, stripped)
		}
	}
}

// renderedText is the decoded text of a document, which is what a reader sees.
func renderedText(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return stdhtml.UnescapeString(b.String())
}

// A term split across chunks is still a term, which is why matching is over the
// node rather than the chunk.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		want, wantRes := highlight(t, doc, "streaming", "café")
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Highlight(&b, &chunked{s: doc, n: n}, []string{"streaming", "café"}, DefaultMarks)
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Total != wantRes.Total {
				t.Fatalf("%q at writes of %d:\n got %q (%d marks)\nwant %q (%d marks)",
					doc, n, b.String(), res.Total, want, wantRes.Total)
			}
		}
	}
}

// Marking twice marks again, and that is worth pinning rather than hiding: this
// program is not idempotent, and making it so would need a marker it could
// recognise - which is the markup it refuses to insert. A caller running it twice
// gets "««streaming»»", and should not.
//
// This test is also what found the boundary bug below: it expected the second run
// to mark and it did not, because "»" defeated the byte-indexed boundary check.
func TestItIsNotIdempotentAndSaysSo(t *testing.T) {
	once, _ := highlight(t, `<p>streaming</p>`, "streaming")
	if once != `<p>«streaming»</p>` {
		t.Fatalf("first run gave %q", once)
	}
	twice, res := highlight(t, once, "streaming")
	if twice == once {
		t.Errorf("running twice changed nothing, so this test is stale: %q", once)
	}
	if res.Total != 1 {
		t.Errorf("the second run marked %d times, want 1: %q", res.Total, twice)
	}
	if !strings.Contains(twice, "««streaming»»") {
		t.Errorf("the second run gave %q, want the marks doubled", twice)
	}
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

// A term next to a multi-byte character. This is the case that found the bug:
// the boundary check indexed bytes, and the first byte of "»" read as a rune is
// "Â", which is a letter - so a term followed by any non-ASCII character was
// silently not matched.
func TestATermNextToAMultiByteCharacter(t *testing.T) {
	tests := []struct {
		doc   string
		marks int
	}{
		{`<p>«streaming»</p>`, 1},
		{`<p>café streaming café</p>`, 1},
		{`<p>—streaming—</p>`, 1},
		// Still not matched inside a word made of letters.
		{`<p>xstreamingx</p>`, 0},
		// A CJK character is a letter, so it binds as a word character and the
		// term is not a whole word. That is consistent rather than clever: a
		// boundary rule built on letters cannot serve a language that does not
		// separate words with spaces, and pretending otherwise would mark
		// fragments.
		{`<p>streaming日本</p>`, 0},
		{`<p>日本streaming</p>`, 0},
	}
	for _, tt := range tests {
		out, res := highlight(t, tt.doc, "streaming")
		if res.Total != tt.marks {
			t.Errorf("%q: marked %d times, want %d: %q", tt.doc, res.Total, tt.marks, out)
		}
	}
}

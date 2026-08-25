package lolhtml_test

// What a handler's calls actually are.
//
// Two things about text handlers are easy to assume and wrong: that a selector
// reaches all the text, and that a call carries text. Both are documented now
// because both are silent, and both are pinned here because both are the kind of
// fact that a refactor upstream could change without anything noticing.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// countCalls runs a selector-based text handler and a document one over the same
// document and returns how many times each fired.
func countCalls(t *testing.T, doc, selector string) (star, document int) {
	t.Helper()
	_, err := lolhtml.RewriteString(doc,
		lolhtml.OnText(selector, func(*lolhtml.TextChunk) error { star++; return nil }),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { document++; return nil }),
	)
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return star, document
}

// TestNoSelectorReachesTextOutsideEveryElement. A fragment is the shape where
// this bites, and a full document is the shape that hides it.
func TestNoSelectorReachesTextOutsideEveryElement(t *testing.T) {
	tests := []struct {
		doc            string
		star, document int
	}{
		{`hello`, 0, 2},
		{`<p>a</p>tail`, 2, 4},
		{`before<p>a</p>after`, 2, 6},
		{`<html><body>a</body></html>`, 2, 2},
		{`<!-- c -->text`, 0, 2},
	}
	for _, tt := range tests {
		star, document := countCalls(t, tt.doc, "*")
		if star != tt.star || document != tt.document {
			t.Errorf("%q: OnText(\"*\") fired %d times and OnDocumentText %d, want %d and %d",
				tt.doc, star, document, tt.star, tt.document)
		}
	}
}

// The same shape for comments, where the consequence is a sanitiser that leaves
// the comments outside the root element in place.
func TestNoSelectorReachesCommentsOutsideEveryElement(t *testing.T) {
	const doc = `<!-- before --><div><!-- inside --></div><!-- after -->`
	star, document := 0, 0
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnComment("*", func(*lolhtml.Comment) error { star++; return nil }),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { document++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	if star != 1 || document != 3 {
		t.Errorf(`OnComment("*") fired %d times and OnDocumentComment %d, want 1 and 3`, star, document)
	}
}

// TestTheFinalChunkOfATextNodeIsEmpty across every shape that might chunk
// differently. The claim in the documentation is "in every shape measured", and
// this is that measurement.
func TestTheFinalChunkOfATextNodeIsEmpty(t *testing.T) {
	docs := []string{
		`<p>hello</p>`,
		`<p>a<b>c</b>d</p>`,
		`<p>` + strings.Repeat("x", 100000) + `</p>`,
		`<p>caf&eacute;</p>`,
		"<p>a\nb</p>",
		`<script>var x = 1</script>`,
		`<style>p{}</style>`,
		`<textarea>hi</textarea>`,
		`<title>t</title>`,
	}
	for _, doc := range docs {
		lasts, empties := 0, 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnText("p, script, style, textarea, title", func(c *lolhtml.TextChunk) error {
				if !c.IsLastInTextNode() {
					if len(c.Bytes()) == 0 {
						t.Errorf("%.30q: a chunk that is not the last carried nothing", doc)
					}
					return nil
				}
				lasts++
				if len(c.Bytes()) == 0 {
					empties++
				}
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if lasts == 0 {
			t.Errorf("%.30q: no chunk was marked last", doc)
		}
		if empties != lasts {
			t.Errorf("%.30q: %d of %d final chunks carried bytes", doc, lasts-empties, lasts)
		}
	}
}

// And it does not depend on how the document is written: the write pattern
// changes how the content is chunked, not how a node ends.
func TestTheFinalChunkIsEmptyHoweverTheInputIsWritten(t *testing.T) {
	const doc = `<p>hello world</p>`
	for _, n := range []int{1, 2, 3, 5, 7, len(doc)} {
		var sb strings.Builder
		lasts, empties, calls := 0, 0, 0
		w, err := lolhtml.NewWriter(&sb, lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
			calls++
			if c.IsLastInTextNode() {
				lasts++
				if len(c.Bytes()) == 0 {
					empties++
				}
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += n {
			if _, err := w.Write([]byte(doc[i:min(i+n, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if lasts != 1 || empties != 1 {
			t.Errorf("writes of %d: %d final chunks, %d empty, want 1 and 1", n, lasts, empties)
		}
		if calls < 2 {
			t.Errorf("writes of %d: %d calls, want at least two per text node", n, calls)
		}
	}
}

// An element with no text has no text node, so there is no final chunk to mark.
func TestAnEmptyElementProducesNoTextCalls(t *testing.T) {
	for _, doc := range []string{`<p></p>`, `<p><b></b></p>`, `<div></div>`} {
		calls := 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnText("p, div", func(*lolhtml.TextChunk) error { calls++; return nil })); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Errorf("%q: %d text calls, want none", doc, calls)
		}
	}
}

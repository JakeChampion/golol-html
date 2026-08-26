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

// TestTheFinalChunkOfATextNodeIsEmptyWhenItDecodes across every shape that might chunk
// differently. The claim in the documentation is "in every shape measured", and
// this is that measurement.
func TestTheFinalChunkOfATextNodeIsEmptyWhenItDecodes(t *testing.T) {
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
		// Every document here decodes cleanly, which is the condition. A node
		// ending in undecodable bytes has a final chunk that carries the
		// replacement character; see
		// TestTheFinalChunkCarriesAReplacementCharacterItProduced.
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
			t.Errorf("writes of %d: %d calls, want two per text node for a node that "+
				"decodes cleanly", n, calls)
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

// TestTheFinalChunkCarriesAReplacementCharacterItProduced is the exception to the two claims
// above, and it is the one that matters: the documentation said the final chunk carries no bytes,
// so the natural handler acts on the flag and returns - and that drops a character.
//
// The trigger is a node whose last bytes could not be decoded. The replacement character the
// rewriter produces for them arrives as the final chunk, with the flag set and three bytes of
// text, standing at a zero-width source range because it is not in the input.
func TestTheFinalChunkCarriesAReplacementCharacterItProduced(t *testing.T) {
	type result struct {
		calls     int
		lastText  string
		lastBytes int
		lastRange lolhtml.SourceLocation
	}
	run := func(t *testing.T, doc string, size int, opts ...lolhtml.Option) result {
		t.Helper()
		var got result
		var out strings.Builder
		all := append([]lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			got.calls++
			if c.IsLastInTextNode() {
				got.lastText = c.Text()
				got.lastBytes = len(c.Bytes())
				got.lastRange = c.SourceLocation()
			}
			return nil
		})}, opts...)
		w, err := lolhtml.NewWriter(&out, all...)
		if err != nil {
			t.Fatal(err)
		}
		if size <= 0 {
			size = len(doc) + 1
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return got
	}

	const replacement = "\ufffd"

	// Every shape the old claim was measured in, with an undecodable byte at the end of the
	// node. In each one the final chunk carries the replacement character.
	for _, doc := range []string{
		"<p>ab\xe9</p>",
		"<p>ab\xe9",        // unterminated
		"<p>a\xe9\xe9</p>", // two of them
		"<script>ab\xe9</script>",
		"<style>ab\xe9</style>",
		"<title>ab\xe9</title>",
		"<textarea>ab\xe9</textarea>",
		"<p>" + strings.Repeat("a", 100000) + "\xe9</p>",
	} {
		got := run(t, doc, 0)
		if got.lastText != replacement {
			t.Errorf("%.30q: final chunk text %q, want the replacement character",
				doc, got.lastText)
		}
		if got.lastBytes != 3 {
			t.Errorf("%.30q: final chunk carried %d bytes, want 3", doc, got.lastBytes)
		}
		if got.lastRange.Len() != 0 {
			t.Errorf("%.30q: final chunk range %v, want a zero-width point - those bytes "+
				"are not in the input", doc, got.lastRange)
		}
	}

	// And at every write size, since the write pattern changes how content is chunked and
	// not how a node ends.
	for _, size := range []int{1, 2, 3, 5, 7, 0} {
		if got := run(t, "<p>ab\xe9</p>", size); got.lastText != replacement {
			t.Errorf("writes of %d: final chunk text %q, want the replacement character",
				size, got.lastText)
		}
	}

	// One call, not two, where the node is nothing but a truncated sequence: its first chunk
	// is also its last. A standalone invalid byte is still two, because it is replaced inside
	// the content chunk - which is the distinction, and it is not the one I first assumed.
	for _, doc := range []string{"<p>\xe9</p>", "<p>\xc3</p>"} {
		if got := run(t, doc, 0); got.calls != 1 || got.lastText != replacement {
			t.Errorf("%q: %d calls with final text %q, want one call carrying the "+
				"replacement character", doc, got.calls, got.lastText)
		}
	}
	if got := run(t, "<p>\x80</p>", 0); got.calls != 2 || got.lastText != "" {
		t.Errorf("<p>\\x80</p>: %d calls with final text %q, want two and an empty final "+
			"chunk - a standalone invalid byte is replaced inside the content",
			got.calls, got.lastText)
	}

	// The trigger is the encoding, not the byte: declared windows-1252, the same document
	// decodes and the final chunk is empty again.
	if got := run(t, "<p>ab\xe9</p>", 0, lolhtml.WithEncoding("windows-1252")); got.lastText != "" {
		t.Errorf("as windows-1252: final chunk text %q, want it empty", got.lastText)
	}

	// The consequence, written as the handler the old documentation invited: acting on the
	// flag without reading its text loses the character.
	accumulate := func(doc string, readLast bool) string {
		var acc strings.Builder
		var whole string
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				if readLast {
					acc.WriteString(c.Text())
				}
				whole = acc.String()
				acc.Reset()
				return nil
			}
			acc.WriteString(c.Text())
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return whole
	}
	if got := accumulate("<p>ab\xe9</p>", false); got != "ab" {
		t.Errorf("ignoring the final chunk's text gave %q, want the character dropped", got)
	}
	if got := accumulate("<p>ab\xe9</p>", true); got != "ab"+replacement {
		t.Errorf("reading the final chunk's text gave %q, want %q", got, "ab"+replacement)
	}
}

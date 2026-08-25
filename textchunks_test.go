package lolhtml_test

// What splits a text node into chunks.
//
// The documentation used to attribute the boundaries to the writes alone. They
// are not: the tokenizer splits a node at a "<" that does not begin a tag, which
// happens on ordinary prose and happens however the document was written to the
// rewriter.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// markedChunks returns each text chunk, with the final empty one marked, for a
// document written in one call.
func markedChunks(t *testing.T, doc string) []string {
	t.Helper()
	var got []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		if c.IsLastInTextNode() {
			got = append(got, fmt.Sprintf("%q|end", c.Text()))
			return nil
		}
		got = append(got, fmt.Sprintf("%q", c.Text()))
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestTheTokenizerSplitsTextAtABareLessThan, whatever the writes were.
func TestTheTokenizerSplitsTextAtABareLessThan(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{"<p>abc</p>", []string{`"abc"`, `""|end`}},
		{"<p>a < b</p>", []string{`"a "`, `"<"`, `" b"`, `""|end`}},
		{"<p>3 < 4 and 5 < 6</p>", []string{`"3 "`, `"<"`, `" 4 and 5 "`, `"<"`, `" 6"`, `""|end`}},
		// A "<" followed by something that could start a tag name does start one,
		// so the text node ends there instead.
		{"<p>a <b> c</p>", []string{`"a "`, `""|end`, `" c"`, `""|end`}},
		// A digit cannot start a tag name, so this is text again.
		{"<p>a <2 b</p>", []string{`"a "`, `"<"`, `"2 b"`, `""|end`}},
		// An escaped one does not split anything, and neither do the other
		// characters a caller might expect to be special.
		{"<p>a &lt; b</p>", []string{`"a &lt; b"`, `""|end`}},
		{"<p>a &amp; b</p>", []string{`"a &amp; b"`, `""|end`}},
		{"<p>a > b</p>", []string{`"a > b"`, `""|end`}},
		{"<p>a\r\nb</p>", []string{"\"a\\r\\nb\"", `""|end`}},
		{"<p>a\x00b</p>", []string{"\"a\\x00b\"", `""|end`}},
		// Raw text is no different: the tokenizer is still looking for a tag.
		{"<title>a < b</title>", []string{`"a "`, `"<"`, `" b"`, `""|end`}},
		{"<script>a < b</script>", []string{`"a "`, `"<"`, `" b"`, `""|end`}},
	} {
		got := markedChunks(t, tc.doc)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%q\n got %v\nwant %v", tc.doc, got, tc.want)
		}
	}
}

// TestTheseSequencesEndTheTextNodeInstead: each begins a comment token, so the
// text before it is a complete node rather than a piece of one.
func TestTheseSequencesEndTheTextNodeInstead(t *testing.T) {
	for _, doc := range []string{"<p>a <! b</p>", "<p>a </ b</p>", "<p>a <?b</p>"} {
		got := markedChunks(t, doc)
		if want := []string{`"a "`, `""|end`}; strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q\n got %v\nwant %v", doc, got, want)
		}
		// And the bytes are unchanged, so nothing was lost by becoming a comment.
		out, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		if out != doc {
			t.Errorf("%q came out as %q", doc, out)
		}
	}
}

// TestALongTextNodeIsNotSplitBySize, which is the other half of the cost claim: it
// is the writes and the tokenizer that split a node, not its length.
func TestALongTextNodeIsNotSplitBySize(t *testing.T) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		doc := "<p>" + strings.Repeat("x", size) + "</p>"
		if got := len(markedChunks(t, doc)); got != 2 {
			t.Errorf("%d bytes of text arrived in %d chunks, want 2", size, got)
		}
	}
}

// TestTheWritesSplitItToo, so both causes are measured in one place: the same
// document is two chunks in one write and as many as the writes otherwise.
func TestTheWritesSplitItToo(t *testing.T) {
	const doc = "<p>abcdefgh</p>"
	for _, size := range []int{1, 2, 4, len(doc)} {
		var out strings.Builder
		chunks := 0
		w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error {
			chunks++
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if size == len(doc) && chunks != 2 {
			t.Errorf("one write gave %d chunks, want 2", chunks)
		}
		if size == 1 && chunks <= 2 {
			t.Errorf("one-byte writes gave %d chunks, want more than 2", chunks)
		}
		if out.String() != doc {
			t.Errorf("chunks of %d: output %q", size, out.String())
		}
	}
}

// TestAccumulatingIsStillTheAnswer. The rule the documentation gives - accumulate
// to IsLastInTextNode - is what makes the splitting a non-issue, and this is the
// document where a per-chunk transform would go wrong.
func TestAccumulatingIsStillTheAnswer(t *testing.T) {
	const doc = "<p>3 < 4 and 5 < 6</p>"
	var whole strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		whole.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if got := whole.String(); got != "3 < 4 and 5 < 6" {
		t.Errorf("accumulated %q", got)
	}

	// A count of a pattern per chunk gets the wrong answer on the same document,
	// which is what the splitting costs a caller who does not accumulate.
	perChunk := 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		perChunk += strings.Count(c.Text(), "< 4")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if perChunk != 0 {
		t.Errorf("a per-chunk count found %d, and the point is that it finds none", perChunk)
	}
}

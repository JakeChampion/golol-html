package lolhtml_test

// Where a text chunk can begin and end.
//
// The boundaries are not a caller's choice: the writes split a node, and so does
// the tokenizer - see textchunks_test.go for that half. The claim here is that
// wherever they fall, they fall between characters - which makes a per-character
// transform safe per chunk, and is the opposite of the rule for content going
// into a sink, where a partial sequence is accepted and joined to the next write.

import (
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// chunksOf writes doc in writeSize-byte pieces and returns what the text handler
// was handed, per call.
func chunksOf(t *testing.T, doc string, writeSize int) []string {
	t.Helper()
	var got []string
	var sb strings.Builder
	w, err := lolhtml.NewWriter(&sb, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		got = append(got, c.Text())
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i += writeSize {
		if _, err := w.Write([]byte(doc[i:min(i+writeSize, len(doc))])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if sb.String() != doc {
		t.Fatalf("writes of %d changed the document: %q", writeSize, sb.String())
	}
	return got
}

// TestNoTextChunkSplitsACharacter, at the write size that would do it if
// anything did.
func TestNoTextChunkSplitsACharacter(t *testing.T) {
	tests := map[string]string{
		"two-byte":   "é",
		"three-byte": "日",
		"four-byte":  "🎉",
		"mixed":      "aé日🎉",
	}
	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			body := strings.Repeat(r, 40)
			for _, n := range []int{1, 2, 3, 4, 5, 7} {
				chunks := chunksOf(t, "<p>"+body+"</p>", n)
				for i, c := range chunks {
					if !utf8.ValidString(c) {
						t.Errorf("writes of %d: chunk %d is not valid UTF-8: % x", n, i, c)
					}
				}
				if joined := strings.Join(chunks, ""); joined != body {
					t.Errorf("writes of %d: joined text is %q, want %q", n, joined, body)
				}
			}
		})
	}
}

// The same guarantee for the other places a string crosses the boundary.
func TestNoCommentOrAttributeSplitsACharacter(t *testing.T) {
	const body = "日本語テキスト"
	for _, n := range []int{1, 2, 3} {
		var comments, titles []string
		var sb strings.Builder
		w, err := lolhtml.NewWriter(&sb,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				comments = append(comments, c.Text())
				return nil
			}),
			lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				v, _ := e.Attribute("title")
				titles = append(titles, v)
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		doc := `<!--` + body + `--><a title="` + body + `">l</a>`
		for i := 0; i < len(doc); i += n {
			if _, err := w.Write([]byte(doc[i:min(i+n, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if len(comments) != 1 || comments[0] != body {
			t.Errorf("writes of %d: comments = %q, want one of %q", n, comments, body)
		}
		if len(titles) != 1 || titles[0] != body {
			t.Errorf("writes of %d: titles = %q, want one of %q", n, titles, body)
		}
	}
}

// What a boundary does split, so that the guarantee is not read as more than it
// is: a per-chunk transform is safe per character and wrong per pattern.
func TestABoundarySplitsEverythingLargerThanACharacter(t *testing.T) {
	const doc = `<p>hello world</p>`

	// One write: one chunk with content, so a per-chunk search finds the word.
	whole := chunksOf(t, doc, len(doc))
	found := 0
	for _, c := range whole {
		if strings.Contains(c, "hello world") {
			found++
		}
	}
	if found != 1 {
		t.Errorf("written whole, the phrase was found in %d chunks, want 1: %q", found, whole)
	}

	// Byte at a time: no chunk contains it, and a handler searching per chunk
	// finds nothing at all.
	split := chunksOf(t, doc, 1)
	for _, c := range split {
		if strings.Contains(c, "hello world") {
			t.Fatalf("a one-byte write produced a chunk containing the phrase: %q", c)
		}
	}
	if joined := strings.Join(split, ""); joined != "hello world" {
		t.Errorf("joined = %q, want %q", joined, "hello world")
	}

	// A per-character transform is unaffected, which is the other half of the
	// rule.
	for _, n := range []int{1, 3, len(doc)} {
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
			if len(c.Bytes()) == 0 {
				return nil
			}
			return c.Replace(strings.ToUpper(c.Text()), lolhtml.Text)
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
		if want := `<p>HELLO WORLD</p>`; out.String() != want {
			t.Errorf("writes of %d: got %q, want %q", n, out.String(), want)
		}
	}
}

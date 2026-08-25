package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestNothingButTextChunkingMoves is the program as a test.
func TestNothingButTextChunkingMoves(t *testing.T) {
	diffs, checks, err := Check(Documents())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diffs {
		t.Error(d)
	}
	if checks < 100 {
		t.Errorf("only %d comparisons; the corpus or the write sizes have shrunk", checks)
	}
}

// A checker whose handlers never fire reports everything invariant. This is the
// test that says the machinery is looking at something: the text handler has to
// be called a different number of times at different write sizes, and the
// structural observations have to be non-empty.
func TestTheCheckerActuallySeesTheChunking(t *testing.T) {
	docs := Documents()

	moved := 0
	for name, doc := range docs {
		lo, hi, err := TextCallSpread(doc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lo != hi {
			moved++
		}
	}
	if moved < 5 {
		t.Errorf("the text handler's call count moved for only %d of %d documents; "+
			"if it never moved, this whole check would be measuring nothing", moved, len(docs))
	}

	o, err := Observe(docs["page"], 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Events) < 8 {
		t.Errorf("the page produced %d structural events; the handlers are not "+
			"seeing the document", len(o.Events))
	}
	if len(o.TextNodes) == 0 {
		t.Error("the page produced no text nodes")
	}
	if o.Output != docs["page"] {
		t.Errorf("the page did not round-trip:\n got %q\nwant %q", o.Output, docs["page"])
	}
}

// A text chunk never contains part of a character, whatever the write size. The
// opposite rule holds for content going the other way - Sink.WriteChunk accepts
// a partial sequence and joins it to the next write - so this is worth its own
// test rather than being folded into the invariance check.
func TestNoChunkContainsPartOfACharacter(t *testing.T) {
	runes := map[string]string{
		"two byte":   "é",
		"three byte": "日",
		"four byte":  "🎉",
		"mixed":      "aé日🎉",
	}
	for name, r := range runes {
		doc := "<p>" + strings.Repeat(r, 40) + "</p>"
		for _, n := range []int{1, 2, 3, 4, 5} {
			o, err := Observe(doc, n)
			if err != nil {
				t.Fatalf("%s at %d: %v", name, n, err)
			}
			if o.PartialRunes != 0 {
				t.Errorf("%s at writes of %d: %d handovers contained part of a character",
					name, n, o.PartialRunes)
			}
			// And the text arrived whole.
			joined := strings.Join(o.TextNodes, "")
			if joined != strings.Repeat(r, 40) {
				t.Errorf("%s at writes of %d: text arrived as %q", name, n, joined)
			}
			if !utf8.ValidString(joined) {
				t.Errorf("%s at writes of %d: the joined text is not valid UTF-8", name, n)
			}
		}
	}
}

// The corpus has to contain the shapes a write boundary can land inside, or the
// invariance it proves is invariance over the easy cases.
func TestTheCorpusCoversTheBoundaries(t *testing.T) {
	docs := Documents()
	needs := map[string]func(string) bool{
		"a long attribute value": func(d string) bool { return strings.Contains(d, `title="xxxx`) },
		"a long tag name":        func(d string) bool { return strings.Contains(d, "<aaaaaaaaaa") },
		"a character reference":  func(d string) bool { return strings.Contains(d, "&eacute;") },
		"a four-byte character":  func(d string) bool { return strings.Contains(d, "🎉") },
		"raw text":               func(d string) bool { return strings.Contains(d, "<script>") },
		"a comment":              func(d string) bool { return strings.Contains(d, "<!--") },
		"a doctype":              func(d string) bool { return strings.Contains(d, "<!DOCTYPE") },
		"foreign content":        func(d string) bool { return strings.Contains(d, "<svg>") },
		"an unclosed element":    func(d string) bool { return d == `<div><span>a` },
	}
	for what, has := range needs {
		found := false
		for _, d := range docs {
			if has(d) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the corpus has no document with %s", what)
		}
	}
}

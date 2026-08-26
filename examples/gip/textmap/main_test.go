package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// multibyte is twelve bytes with one three-byte character, which is the smallest document that
// shows every failure mode at some write size.
const multibyte = `<p>a€b</p>`

// TestATextChunksRangeMovesWithTheWritePattern is the finding. The documentation says the
// offsets are unaffected by how the document was written in and that slicing the input at a
// range works; for a text chunk both are conditional on where the write boundaries fell.
func TestATextChunksRangeMovesWithTheWritePattern(t *testing.T) {
	whole, err := Map([]byte(multibyte), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || whole[0].Text != "a€b" || whole[0].Start != 3 || whole[0].End != 8 {
		t.Fatalf("fed in one call: %v, want one chunk 3..8 %q", whole, "a€b")
	}
	if !whole[0].SliceIsText() {
		t.Fatalf("fed in one call the slice is not the text: %q", whole[0].Slice)
	}

	var mismatched, unnamed []int
	for size := 1; size <= len(multibyte); size++ {
		cs, err := Map([]byte(multibyte), size)
		if err != nil {
			t.Fatal(err)
		}
		var desc []string
		covered := map[int]bool{}
		bad := false
		for _, c := range cs {
			desc = append(desc, fmt.Sprintf("%d..%d %q", c.Start, c.End, c.Text))
			if !c.SliceIsText() {
				bad = true
			}
			for i := c.Start; i < c.End; i++ {
				covered[i] = true
			}
		}
		var gaps []int
		for i := 3; i < 8; i++ { // the text bytes of this document
			if !covered[i] {
				gaps = append(gaps, i)
			}
		}
		t.Logf("size %2d: %-34s slices are the text: %-5v named by nothing: %v",
			size, strings.Join(desc, " "), !bad, gaps)
		if bad {
			mismatched = append(mismatched, size)
		}
		if len(gaps) > 0 {
			unnamed = append(unnamed, size)
		}
	}

	// Which sizes fail is lol-html's buffering and could reasonably change; that some do is
	// the claim, and it is what a caller has to program against.
	if len(mismatched) == 0 {
		t.Errorf("at no write size did a text chunk's range hold something other than its " +
			"own text, so the documented slicing recipe is unconditional after all and " +
			"this program has nothing to say")
	}
	if len(unnamed) == 0 {
		t.Errorf("at no write size were text bytes named by no chunk, so the range union " +
			"is write-invariant after all")
	}
	t.Logf("slices are not the text at sizes %v; text bytes named by nothing at sizes %v",
		mismatched, unnamed)
}

// TestTheOtherUnitsRangesDoNotMove is the other half, and the reason the anchored recipe works:
// an element, an end tag, a comment and a doctype report the same range at every write size,
// including when their own content is multi-byte.
func TestTheOtherUnitsRangesDoNotMove(t *testing.T) {
	for _, doc := range []string{
		`<p class="é">x</p>`,
		`<!--é-->`,
		`<p title="€"/>`,
		`<!doctype html><p>é</p><!--ü-->`,
		`<script>if (a<b) {} // é</script>`,
	} {
		want, err := anchorsOf([]byte(doc), 0)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if len(want) == 0 {
			t.Fatalf("%q: no anchors", doc)
		}
		for size := 1; size <= len(doc); size++ {
			got, err := anchorsOf([]byte(doc), size)
			if err != nil {
				t.Fatalf("%q size %d: %v", doc, size, err)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%q size %d: anchors %v, want %v", doc, size, got, want)
			}
		}
	}
}

// TestTextRegionsAreWriteInvariantAndChunkRangesAreNot is the core comparison, and it is
// two-sided on purpose: the map computed from the tags around the text has to be identical at
// every write size, and the map computed from the text chunks' own ranges has to differ at some
// write size. If the second ever stops differing there is no reason to prefer the first.
func TestTextRegionsAreWriteInvariantAndChunkRangesAreNot(t *testing.T) {
	docs := []string{
		multibyte,
		`<p>a</p><p>é</p><p>b</p>`,
		`<!doctype html><!--c--><p>caf€</p><b>x</b>`,
		`<ul><li>é<li>b</ul>`,
	}
	movedSomewhere := false
	for _, doc := range docs {
		want, err := TextRegions([]byte(doc), 0)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if len(want) == 0 {
			t.Fatalf("%q: no text regions", doc)
		}
		for size := 1; size <= len(doc); size++ {
			got, err := TextRegions([]byte(doc), size)
			if err != nil {
				t.Fatalf("%q size %d: %v", doc, size, err)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%q size %d: text regions %v, want %v", doc, size, got, want)
			}
		}

		chunkWhole, err := ChunkRegions([]byte(doc), 0)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		var moved []int
		for size := 1; size <= len(doc); size++ {
			got, err := ChunkRegions([]byte(doc), size)
			if err != nil {
				t.Fatalf("%q size %d: %v", doc, size, err)
			}
			if fmt.Sprint(got) != fmt.Sprint(chunkWhole) {
				moved = append(moved, size)
			}
		}
		t.Logf("%-42q regions from tags %v at every size; from chunks, different at %v",
			doc, want, moved)
		if len(moved) > 0 {
			movedSomewhere = true
		}
	}
	if !movedSomewhere {
		t.Error("the chunk-derived map was the same at every write size for every document, " +
			"so it is as good as the tag-derived one and this program's advice is wrong")
	}
}

// TestReconstructingFromChunkTextIsWrong. Rebuilding the document by taking the text from the
// handler and the rest from the caller's buffer is the obvious reading of "reconstruct the
// document from the text chunks reported", and it is wrong at some write sizes.
func TestReconstructingFromChunkTextIsWrong(t *testing.T) {
	var wrong []int
	for size := 0; size <= len(multibyte); size++ {
		got, err := ReconstructFromChunkText([]byte(multibyte), size)
		if err != nil {
			t.Fatal(err)
		}
		if got != multibyte {
			wrong = append(wrong, size)
			t.Logf("size %d: %q (%d bytes for %d)", size, got, len(got), len(multibyte))
		}
	}
	if len(wrong) == 0 {
		t.Error("rebuilding from chunk text was exact at every write size, so there is no " +
			"trap here and this program has nothing to say")
	} else {
		t.Logf("wrong at write sizes %v of 0..%d", wrong, len(multibyte))
	}
	// Fed in one call it is exact, which is why the trap is easy to miss.
	if got, err := ReconstructFromChunkText([]byte(multibyte), 0); err != nil {
		t.Fatal(err)
	} else if got != multibyte {
		t.Errorf("fed in one call it gave %q, so the failure is not about the write pattern "+
			"after all", got)
	}
}

// TestGapPlusSliceCannotFail pins the trap that made the first draft of this file pass. Filling
// the gaps from the caller's buffer and taking each chunk by slicing the input reproduces the
// document even when the ranges are nonsense, because the two writes are adjacent by
// construction. So it is not a check, and a test that uses it is measuring memcpy.
func TestGapPlusSliceCannotFail(t *testing.T) {
	rebuild := func(doc string, ranges [][2]int) string {
		var b bytes.Buffer
		pos := 0
		for _, r := range ranges {
			if r[0] < pos {
				continue
			}
			b.WriteString(doc[pos:r[0]])
			b.WriteString(doc[r[0]:r[1]])
			pos = r[1]
		}
		b.WriteString(doc[pos:])
		return b.String()
	}
	const doc = `<p>a€b</p>`
	for _, ranges := range [][][2]int{
		{{3, 8}},                   // the true text range
		{{0, 1}, {5, 6}, {11, 12}}, // arbitrary nonsense
		{{4, 4}},                   // an empty range
		nil,                        // nothing at all
	} {
		if got := rebuild(doc, ranges); got != doc {
			t.Fatalf("ranges %v rebuilt %q - the trap is not a trap and this test should go",
				ranges, got)
		}
	}
	t.Log("every range list reproduces the document, including one that is nonsense")
}

// TestExtractingTextViaRegionsIsLosslessWhereTheHandlerIsNot is the reason to compute the map
// this way. A text handler decodes and re-encodes, so every byte invalid in the declared
// encoding becomes U+FFFD: the handler's own text for a windows-1252 body is longer than the
// body and has lost the bytes. Extracting the same regions from the caller's buffer is exact,
// and exact at every write size.
func TestExtractingTextViaRegionsIsLosslessWhereTheHandlerIsNot(t *testing.T) {
	everyByte := make([]byte, 0, 300)
	everyByte = append(everyByte, "<p>"...)
	for i := 0; i < 256; i++ {
		if b := byte(i); b != '<' && b != '&' {
			everyByte = append(everyByte, b)
		}
	}
	everyByte = append(everyByte, "</p><b>x</b>"...)

	for _, tt := range []struct {
		name string
		body []byte
		text string // the text bytes of the body, by construction
	}{
		{"a windows-1252 page", []byte("<p>caf\xe9</p><b>x</b>"), "caf\xe9x"},
		{"one undecodable byte", []byte("<p>\x80</p>"), "\x80"},
		{"every byte value", everyByte, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			regions, err := TextRegions(tt.body, 0)
			if err != nil {
				t.Fatal(err)
			}
			extracted := ExtractText(tt.body, regions)
			if tt.text != "" && extracted != tt.text {
				t.Fatalf("extracted %q, want the body's own text bytes %q", extracted, tt.text)
			}

			// The handler's own text has to differ, or there is nothing being avoided.
			handlerText, err := ConcatChunkText(tt.body, 0)
			if err != nil {
				t.Fatal(err)
			}
			if handlerText == extracted {
				t.Fatalf("the handler's text is the same %d bytes, so nothing is lost and "+
					"this test proves nothing", len(handlerText))
			}
			t.Logf("%d text bytes in the body; the handler reports %d",
				len(extracted), len(handlerText))

			for _, size := range []int{1, 2, 7, 64} {
				got, err := TextRegions(tt.body, size)
				if err != nil {
					t.Fatalf("size %d: %v", size, err)
				}
				if ExtractText(tt.body, got) != extracted {
					t.Errorf("size %d: extracted %q, want %q", size,
						ExtractText(tt.body, got), extracted)
				}
			}
		})
	}
}

// anchorsOf reports the ranges TextRegions anchors on, so the invariance test measures the same
// thing the recipe relies on rather than a re-implementation of it.
func anchorsOf(doc []byte, size int) ([]lolhtml.SourceLocation, error) {
	var got []lolhtml.SourceLocation
	note := func(l lolhtml.SourceLocation) {
		if l.Start != l.End {
			got = append(got, l)
		}
	}
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { note(d.SourceLocation()); return nil }),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { note(c.SourceLocation()); return nil }),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			note(e.SourceLocation())
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(et *lolhtml.EndTag) error { note(et.SourceLocation()); return nil })
		}),
	)
	if err != nil {
		return nil, err
	}
	if err := feed(w, doc, size); err != nil {
		return nil, err
	}
	return got, w.Close()
}

package lolhtml_test

// What a source range is a range of.
//
// It is the identity a two-pass rewrite uses - the pattern the documentation
// recommends wherever a decision needs what comes later - so the questions are
// which bytes the numbers index and what each unit's range covers. That the
// offsets are absolute and chunk-invariant is measured in sourceloc_test.go; this
// is the rest of what the type promises.

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// ranged is one unit and the input it claims.
type ranged struct {
	kind  string
	text  string
	start int
	end   string // the input sliced at the range
}

func ranges(t *testing.T, doc string, opts ...lolhtml.Option) []ranged {
	t.Helper()
	var got []ranged
	slice := func(l lolhtml.SourceLocation) string {
		if l.Start < 0 || l.End > len(doc) || l.Start > l.End {
			t.Fatalf("range %d..%d is not inside a %d-byte document", l.Start, l.End, len(doc))
		}
		return doc[l.Start:l.End]
	}
	opts = append(opts,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			l := e.SourceLocation()
			got = append(got, ranged{"element", e.TagName(), l.Start, slice(l)})
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(tag *lolhtml.EndTag) error {
				l := tag.SourceLocation()
				got = append(got, ranged{"end", tag.Name(), l.Start, slice(l)})
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			got = append(got, ranged{"text", c.Text(), l.Start, slice(l)})
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			l := c.SourceLocation()
			got = append(got, ranged{"comment", c.Text(), l.Start, slice(l)})
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			l := d.SourceLocation()
			got = append(got, ranged{"doctype", "", l.Start, slice(l)})
			return nil
		}))
	if _, err := lolhtml.RewriteString(doc, opts...); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestEachUnitsRangeCoversWhatItSaysItCovers.
func TestEachUnitsRangeCoversWhatItSaysItCovers(t *testing.T) {
	const doc = `<!DOCTYPE html><p id="a">ab</p><!--c-->`
	want := []ranged{
		{"doctype", "", 0, "<!DOCTYPE html>"},
		{"element", "p", 15, `<p id="a">`}, // the start tag, and nothing inside it
		{"text", "ab", 25, "ab"},
		{"text", "", 27, ""}, // the node's end, as a zero-width point
		{"end", "p", 27, "</p>"},
		{"comment", "c", 31, "<!--c-->"},
	}
	got := ranges(t, doc)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("\n got %v\nwant %v", got, want)
	}
}

// TestATextNodesExtentIsTheFirstChunkToTheLast, which is what the empty final
// chunk's range is for.
func TestATextNodesExtentIsTheFirstChunkToTheLast(t *testing.T) {
	const doc = "<p>hello there</p>"
	start, end := -1, -1
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		l := c.SourceLocation()
		if start < 0 {
			start = l.Start
		}
		end = l.End
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if got := doc[start:end]; got != "hello there" {
		t.Errorf("the node's extent is %q, want %q", got, "hello there")
	}
}

// TestTheRangeIndexesTheBytesFedAndNotTheReportedText. Under an encoding that is
// not UTF-8 the two are different lengths, and the range is the document's.
func TestTheRangeIndexesTheBytesFedAndNotTheReportedText(t *testing.T) {
	const doc = "<p>caf\xe9</p>" // windows-1252: four bytes of text
	var text string
	var l lolhtml.SourceLocation
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				text, l = c.Text(), c.SourceLocation()
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if text != "café" {
		t.Fatalf("text = %q, want %q", text, "café")
	}
	if len(text) != 5 {
		t.Fatalf("the reported text is %d bytes, so this test is not measuring what it says", len(text))
	}
	if l.End-l.Start != 4 {
		t.Errorf("range %d..%d covers %d bytes, want the four the document spent",
			l.Start, l.End, l.End-l.Start)
	}
	if got := doc[l.Start:l.End]; got != "caf\xe9" {
		t.Errorf("the input sliced at the range is %q", got)
	}
}

// TestAReplacementCharacterCanHaveAnEmptyRange, so the reported text's length and
// the range's length are unrelated numbers.
//
// The byte here is a lead byte with nothing after it, so the rewriter finishes the
// chunk before it and then reports the replacement character as a chunk of its own,
// standing at a point rather than over any bytes. A byte that cannot begin a
// sequence at all - 0xff - stays inside the chunk around it, which is the same rule
// seen from the other side: the range is whatever bytes the chunk came from.
func TestAReplacementCharacterCanHaveAnEmptyRange(t *testing.T) {
	for _, tc := range []struct {
		doc, text string
		wantEmpty bool
	}{
		{"<p>caf\xe9</p>", "\uFFFD", true},
		{"<p>a\xffb</p>", "a\uFFFDb", false},
	} {
		var chunks []ranged
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			chunks = append(chunks, ranged{"text", c.Text(), l.Start, tc.doc[l.Start:l.End]})
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range chunks {
			if c.text != tc.text {
				continue
			}
			found = true
			if empty := c.end == ""; empty != tc.wantEmpty {
				t.Errorf("%q: the chunk %q claims %q, want an empty range = %v",
					tc.doc, c.text, c.end, tc.wantEmpty)
			}
		}
		if !found {
			t.Errorf("%q: no chunk reported %q, only %v", tc.doc, tc.text, chunks)
		}
	}
}

// TestAnImpliedEndTagsRangeIsTheTokenThatClosedIt, which is the same borrowed
// token the name guard is for: three list items, one range.
func TestAnImpliedEndTagsRangeIsTheTokenThatClosedIt(t *testing.T) {
	const doc = "<ul><li>a<li>b<li>c</ul>"
	var ends []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		return e.OnEndTag(func(tag *lolhtml.EndTag) error {
			l := tag.SourceLocation()
			ends = append(ends, fmt.Sprintf("%s:%d..%d", tag.Name(), l.Start, l.End))
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	want := "ul:19..24 ul:19..24 ul:19..24"
	if strings.Join(ends, " ") != want {
		t.Errorf("end tag ranges %q, want %q", strings.Join(ends, " "), want)
	}
}

// TestTheGuardedExtentOfAnElement: the recipe for "what did this element occupy",
// which needs the name guard because an end tag may not be this element's.
func TestTheGuardedExtentOfAnElement(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<div><p id="a">text</p></div>`, `<p id="a">text</p>`},
		{`<div><p id="a">text</div>`, ``}, // no end tag of its own: no extent
		{`<p id="a"></p>`, `<p id="a"></p>`},
	} {
		extent := ""
		if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			name, start := e.TagName(), e.SourceLocation().Start
			return e.OnEndTag(func(tag *lolhtml.EndTag) error {
				if tag.Name() != name {
					return nil // the token belongs to something else
				}
				extent = tc.doc[start:tag.SourceLocation().End]
				return nil
			})
		})); err != nil {
			t.Fatal(err)
		}
		if extent != tc.want {
			t.Errorf("%q: extent %q, want %q", tc.doc, extent, tc.want)
		}
	}
}

// TestTheSameElementHasTheSameRangeInTwoPasses, which is the property a two-pass
// rewrite rests on.
func TestTheSameElementHasTheSameRangeInTwoPasses(t *testing.T) {
	const doc = `<article><p>one</p><p>two</p><p>three</p></article>`

	// Pass one records what it learned, keyed by where the element was.
	lengths := map[int]int{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		at := e.SourceLocation().Start
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			lengths[at] = 0
			return nil
		})
	}), lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		return nil
	})); err != nil {
		t.Fatal(err)
	}

	// Pass two acts on it.
	seen := 0
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		if _, ok := lengths[e.SourceLocation().Start]; !ok {
			t.Errorf("element at %d was not in the first pass's map %v",
				e.SourceLocation().Start, lengths)
			return nil
		}
		seen++
		return e.SetAttribute("data-pass", "2")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if seen != 3 || strings.Count(out, `data-pass="2"`) != 3 {
		t.Errorf("matched %d of 3: %q", seen, out)
	}
}

// TestATextChunksRangeMovesWithTheWritePattern pins the exception to the invariance the type
// promises. The text is right at every write size; the range is not, and the recipe of slicing
// the input at it fails with it.
func TestATextChunksRangeMovesWithTheWritePattern(t *testing.T) {
	const doc = `<p>a€b</p>` // twelve bytes, one three-byte character

	type chunk struct {
		text  string
		loc   lolhtml.SourceLocation
		slice string
	}
	report := func(size int) []chunk {
		var got []chunk
		var out bytes.Buffer
		w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			l := tc.SourceLocation()
			if l.Start == l.End && tc.Text() == "" {
				return nil
			}
			got = append(got, chunk{tc.Text(), l, doc[l.Start:l.End]})
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
		return got
	}

	whole := report(len(doc) + 1)
	if len(whole) != 1 || whole[0].loc != (lolhtml.SourceLocation{Start: 3, End: 8}) ||
		whole[0].slice != whole[0].text {
		t.Fatalf("fed in one call: %v, want one chunk 3..8 whose slice is its text", whole)
	}

	var sliceWrong, unnamed []int
	for size := 1; size <= len(doc); size++ {
		got := report(size)
		covered := map[int]bool{}
		bad := false
		for _, c := range got {
			if c.slice != c.text {
				bad = true
			}
			for i := c.loc.Start; i < c.loc.End; i++ {
				covered[i] = true
			}
		}
		if bad {
			sliceWrong = append(sliceWrong, size)
		}
		for i := 3; i < 8; i++ { // the text bytes of this document
			if !covered[i] {
				unnamed = append(unnamed, size)
				break
			}
		}
		t.Logf("size %2d: %v", size, got)
	}

	// Which sizes fail is lol-html's buffering, and pinning that list would break on a change
	// that is not a regression. That some size fails is the documented claim.
	if len(sliceWrong) == 0 {
		t.Error("slicing the input at a text chunk's range gave the chunk's text at every " +
			"write size, so the exception documented on SourceLocation is not real")
	}
	if len(unnamed) == 0 {
		t.Error("every text byte was named by some chunk at every write size, so the range " +
			"union is write-invariant after all")
	}
	t.Logf("the slice is not the text at sizes %v; text bytes named by nothing at sizes %v",
		sliceWrong, unnamed)
}

// TestTheOtherUnitsRangesDoNotMoveWithTheWritePattern is the other side of it, and the reason
// the documented workaround works: everything except a text chunk reports the same range however
// the input arrived, multi-byte content included.
func TestTheOtherUnitsRangesDoNotMoveWithTheWritePattern(t *testing.T) {
	for _, doc := range []string{
		`<p class="é">x</p>`,
		`<!--é-->`,
		`<p title="€"/>`,
		`<!doctype html><p>é</p><!--ü-->`,
		`<script>if (a<b) {} // é</script>`,
		`<ul><li>€<li>b</ul>`,
	} {
		report := func(size int) string {
			var got []string
			var out bytes.Buffer
			note := func(kind string, l lolhtml.SourceLocation) {
				got = append(got, fmt.Sprintf("%s %v", kind, l))
			}
			w, err := lolhtml.NewWriter(&out,
				lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { note("doctype", d.SourceLocation()); return nil }),
				lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { note("comment", c.SourceLocation()); return nil }),
				lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					note("element", e.SourceLocation())
					if !e.CanHaveContent() {
						return nil
					}
					return e.OnEndTag(func(et *lolhtml.EndTag) error {
						note("end", et.SourceLocation())
						return nil
					})
				}),
			)
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
			return strings.Join(got, " ")
		}

		want := report(len(doc) + 1)
		if want == "" {
			t.Fatalf("%q: nothing reported", doc)
		}
		for size := 1; size <= len(doc); size++ {
			if got := report(size); got != want {
				t.Errorf("%q size %d: %s, want %s", doc, size, got, want)
			}
		}
	}
}

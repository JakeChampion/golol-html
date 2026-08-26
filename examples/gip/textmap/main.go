// Command textmap reports the source location of every text chunk and reconstructs the document
// from what it was told. The reconstruction is the point: it is how you find out that one of the
// two obvious recipes is not reliable, and what to use instead.
//
// The unreliable recipe is the direct one - slice the input at each text chunk's range and
// concatenate. The SourceLocation documentation says the offsets are "absolute and unaffected by
// how the document was written in - one byte at a time gives the same numbers as one call", and
// that "slicing the input at the range works and measuring the reported string does not". Both
// hold for an element, an end tag, a comment and a doctype, measured at every write size. For a
// text chunk they hold only when no multi-byte character straddles a write boundary.
//
// `<p>a€b</p>`, twelve bytes with one three-byte character, reports this:
//
//	write size   text chunks                     slices are the text   bytes named by nothing
//	1            3..4 "a" 6..7 "€" 7..8 "b"      no                    4, 5
//	2            3..4 "a" 6..8 "€b"              no                    4, 5
//	3            3..6 "a" 6..8 "€b"              no                    -
//	4            3..4 "a" 4..8 "€b"              yes                   -
//	5            3..5 "a" 5..8 "€b"              no                    -
//	6            3..6 "a" 6..8 "€b"              no                    -
//	7            3..7 "a€" 7..8 "b"              yes                   -
//	8 and up     3..8 "a€b"                      yes                   -
//
// At size 3 the chunk whose text is the single byte "a" reports a three-byte range, because the
// two bytes of the euro sign that arrived in the same write are held for the rest of the
// character and charged to the chunk already emitted. At size 1 they are charged to nothing, so
// two bytes of the document are named by no chunk at all. The chunk text is right throughout;
// it is the range that moves.
//
// So a proxy that copies from an io.Reader into the rewriter with a fixed buffer gets ranges
// that depend on where its buffer edges fell. Nothing errors, and ASCII never shows it.
//
// The reliable recipe anchors on the units whose ranges do not move and takes everything between
// them from the caller's own buffer. Exact is that: element, end tag, comment and doctype ranges
// as anchors, gaps copied from the input. It is exact at every write size from one byte up, for
// a document with multi-byte text, for one with character references, for a stray end tag (which
// no handler reports at all - B194 - and which the gap copy therefore recovers for free), and
// for a body that is not HTML.
//
// That last one is the reason to bother. A text handler makes a rewrite lossy: every byte that
// is not valid in the declared encoding becomes U+FFFD, so a gzip body comes out 67 bytes in and
// 117 out. The anchored reconstruction of that same body is byte-exact at every write size,
// because it never reads a chunk's text - it reads the caller's bytes. An observer that has to
// be lossless on a body that might not be text can be.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Chunk is one reported text chunk: its text, its range, and the input at that range.
type Chunk struct {
	Text  string
	Start int
	End   int
	Slice string // the input sliced at Start..End, which is not always Text
}

// SliceIsText reports whether slicing the input at this chunk's range gives the chunk's own
// text. It is false when a character straddled a write boundary.
func (c Chunk) SliceIsText() bool { return c.Slice == c.Text }

// Map reports every non-empty text chunk, with the input feeding in fixed-size writes. Size 0
// means one write for the whole document.
func Map(doc []byte, size int) ([]Chunk, error) {
	var out bytes.Buffer
	var cs []Chunk
	w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
		l := tc.SourceLocation()
		if l.Start == l.End && tc.Text() == "" {
			return nil // the empty final chunk of a node names nothing
		}
		cs = append(cs, Chunk{tc.Text(), l.Start, l.End, string(doc[l.Start:l.End])})
		return nil
	}))
	if err != nil {
		return nil, err
	}
	if err := feed(w, doc, size); err != nil {
		return nil, err
	}
	return cs, w.Close()
}

// A Region is a byte range of the input that is not a tag, a comment or a doctype - so text,
// or a stray end tag, which no handler reports at all (B194).
type Region struct{ Start, End int }

// TextRegions reports the text regions as the complement of the units whose ranges do not move
// with the write pattern. The list is the same at every write size, which is the property that
// makes it worth computing this way rather than from the text chunks themselves.
func TextRegions(doc []byte, size int) ([]Region, error) {
	type span struct{ start, end int }
	var anchors []span
	note := func(l lolhtml.SourceLocation) {
		if l.Start != l.End {
			anchors = append(anchors, span{l.Start, l.End})
		}
	}

	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { note(d.SourceLocation()); return nil }),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { note(c.SourceLocation()); return nil }),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			note(e.SourceLocation())
			if !e.CanHaveContent() {
				// A void element has no end tag to wait for, and registering a handler for
				// one is an error rather than a no-op.
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
	if err := w.Close(); err != nil {
		return nil, err
	}

	var regions []Region
	pos := 0
	for _, a := range anchors {
		if a.start < pos {
			continue // an enclosing or repeated anchor; one end tag closes several elements
		}
		if a.start > pos {
			regions = append(regions, Region{pos, a.start})
		}
		pos = a.end
	}
	if pos < len(doc) {
		regions = append(regions, Region{pos, len(doc)})
	}
	return regions, nil
}

// ChunkRegions is the same map computed the other way, from the union of the text chunks' own
// ranges. This is the one that moves.
func ChunkRegions(doc []byte, size int) ([]Region, error) {
	cs, err := Map(doc, size)
	if err != nil {
		return nil, err
	}
	var regions []Region
	for _, c := range cs {
		if n := len(regions); n > 0 && regions[n-1].End >= c.Start {
			if c.End > regions[n-1].End {
				regions[n-1].End = c.End
			}
			continue
		}
		regions = append(regions, Region{c.Start, c.End})
	}
	return regions, nil
}

// ExtractText returns the bytes of the input the regions cover. On a body a text handler would
// corrupt, this is the lossless way to read its text: the bytes come from the caller and are
// never decoded.
func ExtractText(doc []byte, regions []Region) string {
	var b bytes.Buffer
	for _, r := range regions {
		b.Write(doc[r.Start:r.End])
	}
	return b.String()
}

// ConcatChunkText is the recipe the obvious reading of "reconstruct the text from the chunks
// reported" gives: concatenate what the handler said. It is what a text handler can offer and it
// is lossy, because the text path decodes and re-encodes.
func ConcatChunkText(doc []byte, size int) (string, error) {
	cs, err := Map(doc, size)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Text)
	}
	return b.String(), nil
}

// ReconstructFromChunkText rebuilds the whole document by filling the space between text chunks
// from the caller's buffer and taking the text from the handler. It is wrong at some write
// sizes, which is the finding.
//
// The tempting alternative - fill the gaps the same way but take the text by slicing the input
// at each chunk's range - cannot be used as a check on anything. Gap plus slice is
// doc[pos:c.Start] followed by doc[c.Start:c.End], which is a contiguous copy of the input
// whatever the ranges say, so it reproduces the document even when every range is wrong. The
// first draft of this program's test suite asserted exactly that and passed for that reason.
func ReconstructFromChunkText(doc []byte, size int) (string, error) {
	cs, err := Map(doc, size)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	pos := 0
	for _, c := range cs {
		if c.Start < pos {
			continue
		}
		b.Write(doc[pos:c.Start])
		b.WriteString(c.Text)
		pos = c.End
	}
	b.Write(doc[pos:])
	return b.String(), nil
}

// Rewritten is what the rewriter itself emits with a text handler registered, which is the
// baseline the anchored reconstruction beats on a body that is not text.
func Rewritten(doc []byte, size int) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if err != nil {
		return "", err
	}
	if err := feed(w, doc, size); err != nil {
		return "", err
	}
	return out.String(), w.Close()
}

func feed(w *lolhtml.Writer, doc []byte, size int) error {
	if size <= 0 || size >= len(doc) {
		_, err := w.Write(doc)
		return err
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write(doc[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "textmap: give a file, and optionally a write size")
		os.Exit(1)
	}
	doc, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	size := 0
	if len(os.Args) > 2 {
		if size, err = strconv.Atoi(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "textmap:", err)
			os.Exit(1)
		}
	}

	cs, err := Map(doc, size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	for _, c := range cs {
		note := ""
		if !c.SliceIsText() {
			note = fmt.Sprintf("  <- the range holds %q, not this text", c.Slice)
		}
		fmt.Printf("%d-%d %q%s\n", c.Start, c.End, c.Text, note)
	}

	regions, err := TextRegions(doc, size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	chunked, err := ChunkRegions(doc, size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	fmt.Printf("\ntext regions from the tags around them: %v\n", regions)
	fmt.Printf("text regions from the chunks themselves: %v\n", chunked)

	rebuilt, err := ReconstructFromChunkText(doc, size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	fmt.Printf("\nrebuilt from chunk text:  %s\n", verdict(rebuilt, doc))

	lossy, err := Rewritten(doc, size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	fmt.Printf("the rewrite's own output: %s\n", verdict(lossy, doc))

	whole, err := TextRegions(doc, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "textmap:", err)
		os.Exit(1)
	}
	fmt.Printf("text extracted via regions matches the one-call extraction: %v\n",
		ExtractText(doc, regions) == ExtractText(doc, whole))
}

func verdict(got string, doc []byte) string {
	if got == string(doc) {
		return "exact"
	}
	return fmt.Sprintf("differs (%d bytes against %d)", len(got), len(doc))
}

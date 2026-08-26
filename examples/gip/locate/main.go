// Command locate reports the source location of every match and proves each report by slicing
// the caller's own copy of the input. It is grep for HTML: a selector list in, a list of
// `start-end kind bytes` out, where the bytes are lifted from the input rather than rebuilt
// from what the handler said.
//
// That works because a SourceLocation is an absolute offset into the whole stream and does not
// move with the chunking (B56). What this adds is the exactness, which the library's own test
// only asserted as a prefix: for an element the slice is the start tag byte-for-byte, including
// the forms a naive scanner gets wrong - `<a title="a<b">` has a `<` inside the quotes and the
// slice still ends at the right `>`, and `<p attr=>`, `<p/ >` and `<p a="1"b="2">` all slice
// exactly. So the slice is also the only way back to what the author wrote: `<P>` reports
// TagName "p" and slices to "<P>".
//
// The second half is coverage, and it is the half with a hole in it. Registering every handler
// the library has - element, end tag, comment, text, doctype, all on `*` - names every byte of
// an ordinary document. It does not name a stray end tag. `</p>stray` reports one text span
// covering "stray" and nothing at all for the four bytes in front of it; `<p>a</p></p>` covers
// the first eight bytes and stops. The bytes are still written to the output, so a program that
// reconstructs a document from the spans it was told about silently drops them. Measured for
// `</p>`, `</span>`, `</br>`, `</img>`, `</p class=x>`, `</>` and `</circle>`: all invisible.
//
// Cover writes the document in one call, which is the scope of the claim that stray end tags are
// the only unnamed bytes. What a text chunk's range covers does depend on the write pattern, and
// that is a separate question from this one.
//
// This follows from B76 - end tags are observable only through Element.OnEndTag, so an end tag
// with no start tag has no element to hang off - but the consequence is sharper than the cause:
// it is not that a stray end tag is awkward to observe, it is that no handler exists that ever
// sees it. Which is why Unnamed reports those ranges instead of leaving a caller to assume the
// spans are the document.
//
// One byte decides it. `</x>` is an end tag and invisible; `</ x>`, with a space after the
// slash, is not a tag at all but a bogus comment, so a comment handler sees it and it is named.
package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Span is one reported match: the kind of unit, the absolute byte range, and the raw input
// bytes at that range. Raw is sliced from the caller's copy of the input, not rebuilt.
type Span struct {
	Kind  string // "doctype", "start", "end", "comment", "text"
	Start int
	End   int
	Raw   string
}

func (s Span) String() string { return fmt.Sprintf("%d-%d %s %q", s.Start, s.End, s.Kind, s.Raw) }

// Locate reports every match of every selector, in document order. Every Span's Raw is the
// input sliced at its own offsets, so a caller comparing Raw against what it expected is
// checking the library's offsets and not this program's bookkeeping.
func Locate(doc []byte, selectors []string) ([]Span, error) {
	var spans []Span
	add := func(kind string, l lolhtml.SourceLocation) {
		if l.Start < 0 || l.End > len(doc) || l.Start > l.End {
			// Not reachable for a real unit; a corrupt range would slice-panic below, and a
			// tool that reports offsets should say so rather than crash.
			spans = append(spans, Span{Kind: kind + "(bad range)", Start: l.Start, End: l.End})
			return
		}
		spans = append(spans, Span{kind, l.Start, l.End, string(doc[l.Start:l.End])})
	}

	opts := make([]lolhtml.Option, 0, len(selectors))
	for _, sel := range selectors {
		opts = append(opts, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
			add("start", e.SourceLocation())
			if !e.CanHaveContent() {
				// A void element has no end tag to wait for and registering a handler
				// for one is an error, not a no-op (element.go:975). `<img src=a/>`
				// is the whole match.
				return nil
			}
			return e.OnEndTag(func(et *lolhtml.EndTag) error {
				add("end", et.SourceLocation())
				return nil
			})
		}))
	}

	w, err := lolhtml.NewWriter(&bytes.Buffer{}, opts...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(doc); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return dedupe(spans), nil
}

// Coverage is what a total pass could account for and what it could not.
type Coverage struct {
	Spans   []Span // every unit any handler named, in order, deduplicated
	Unnamed []Span // ranges no handler named, with Kind "unnamed"
}

// Cover registers every handler the library offers and reports both what was named and what was
// not. The Unnamed ranges are the point: they are the bytes an offset-keyed tool cannot see.
func Cover(doc []byte) (Coverage, error) {
	var spans []Span
	add := func(kind string, l lolhtml.SourceLocation) {
		if l.Start == l.End {
			return // the empty final text chunk names no bytes
		}
		spans = append(spans, Span{kind, l.Start, l.End, string(doc[l.Start:l.End])})
	}

	w, err := lolhtml.NewWriter(&bytes.Buffer{},
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { add("doctype", d.SourceLocation()); return nil }),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { add("comment", c.SourceLocation()); return nil }),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error { add("text", tc.SourceLocation()); return nil }),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			add("start", e.SourceLocation())
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(et *lolhtml.EndTag) error { add("end", et.SourceLocation()); return nil })
		}),
	)
	if err != nil {
		return Coverage{}, err
	}
	if _, err := w.Write(doc); err != nil {
		return Coverage{}, err
	}
	if err := w.Close(); err != nil {
		return Coverage{}, err
	}

	spans = dedupe(spans)
	cov := Coverage{Spans: spans}
	pos := 0
	for _, s := range spans {
		if s.Start > pos {
			cov.Unnamed = append(cov.Unnamed, Span{"unnamed", pos, s.Start, string(doc[pos:s.Start])})
		}
		if s.End > pos {
			pos = s.End
		}
	}
	if pos < len(doc) {
		cov.Unnamed = append(cov.Unnamed, Span{"unnamed", pos, len(doc), string(doc[pos:])})
	}
	return cov, nil
}

// Reconstruct rebuilds the document from the spans and the unnamed ranges. It is the proof that
// the two together are the whole input - and, run against Spans alone, the demonstration that
// they are not.
func Reconstruct(spans []Span) string {
	ordered := append([]Span(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	var b strings.Builder
	pos := 0
	for _, s := range ordered {
		if s.Start < pos {
			continue // already written by an enclosing or duplicate span
		}
		b.WriteString(s.Raw)
		pos = s.End
	}
	return b.String()
}

// dedupe drops the repeats an end tag produces. One `</ul>` reaches a program once per element
// it closes (B76), and both spans carry the same range, so keying on the range turns that back
// into one event per token. Sorting is by start then end so an enclosing span comes first.
func dedupe(spans []Span) []Span {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End > spans[j].End
	})
	out := spans[:0:0]
	seen := map[[2]int]bool{}
	for _, s := range spans {
		key := [2]int{s.Start, s.End}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func main() {
	doc, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate: give a file and optionally selectors before it")
		os.Exit(1)
	}
	selectors := os.Args[1 : len(os.Args)-1]
	if len(selectors) == 0 {
		selectors = []string{"*"}
	}

	spans, err := Locate(doc, selectors)
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate:", err)
		os.Exit(1)
	}
	for _, s := range spans {
		fmt.Println(s)
	}

	cov, err := Cover(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate:", err)
		os.Exit(1)
	}
	for _, u := range cov.Unnamed {
		fmt.Printf("%d-%d unnamed %q (no handler reports this; it is still in the output)\n",
			u.Start, u.End, u.Raw)
	}
}

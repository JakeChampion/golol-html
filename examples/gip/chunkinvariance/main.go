// Command chunkinvariance checks what does and does not depend on how the input
// was written.
//
// A caller does not choose its chunk boundaries: they come from whatever reader
// is upstream, so anything that varies with them is a bug that appears in
// production and not in a test. The output bytes are the obvious thing to check
// and the least interesting, because a difference there would be loud. What a
// handler is told is the quiet half.
//
// So this program records every observation a handler can make, over a corpus,
// at write sizes from one byte to the whole document, and reports which ones move.
// Three answers, and only the third is the documented one:
//
//	invariant   element and comment and doctype calls, in order; tag names;
//	            attribute names and values; source locations; end tags; the text
//	            of each text node; how many text nodes there are
//	varies      how many times a text handler is called, and how the text of a
//	            node is divided between those calls
//	guaranteed  a text chunk never contains part of a character - measured at
//	            one byte per write over two-, three- and four-byte runes, in
//	            text, in a comment and in an attribute
//
// The last one is worth stating because the neighbouring rule is the opposite:
// Sink.WriteChunk accepts a partial sequence from the caller and joins it to the
// next write. Content going in may be split anywhere; content coming out never
// is. A per-chunk transform is therefore safe per character and still wrong per
// pattern - strings.ToUpper on a chunk is fine, a regexp for a word is not,
// because the word can straddle two chunks.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// An Observation is everything the handlers were told during one run.
type Observation struct {
	// Events are the structural handler calls, in order, with what each was
	// told. One string per call so a difference names the call.
	Events []string
	// TextNodes is the text of each node, concatenated across its chunks.
	TextNodes []string
	// TextCalls is how many times the text handler ran, which is the one thing
	// expected to move.
	TextCalls int
	// PartialRunes counts chunks, comments or attribute values that were handed
	// over containing part of a character. It must be zero.
	PartialRunes int
	// Output is the rewritten document.
	Output string
}

// Observe runs doc through every kind of handler, writing it in chunks of
// writeSize bytes, and records what they saw. A writeSize of zero writes it all
// at once.
func Observe(doc string, writeSize int) (Observation, error) {
	var o Observation
	var node strings.Builder
	var out strings.Builder

	note := func(parts ...string) { o.Events = append(o.Events, strings.Join(parts, "|")) }

	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			var attrs []string
			for k, v := range e.Attributes() {
				if !utf8.ValidString(k) || !utf8.ValidString(v) {
					o.PartialRunes++
				}
				attrs = append(attrs, k+"="+v)
			}
			// Sorted, because the check is that the set and the values are the
			// same, and source order is checked by AttributeList elsewhere.
			sort.Strings(attrs)
			note("element", e.TagName(), e.TagNamePreserveCase(),
				e.SourceLocation().String(), strings.Join(attrs, ","),
				fmt.Sprint(e.CanHaveContent()), fmt.Sprint(e.IsSelfClosing()),
				e.NamespaceURI())
			if !e.CanHaveContent() {
				return nil
			}
			tag := e.TagName()
			return e.OnEndTag(func(x *lolhtml.EndTag) error {
				note("endtag", tag, x.Name(), x.SourceLocation().String())
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			o.TextCalls++
			if !utf8.ValidString(c.Text()) {
				o.PartialRunes++
			}
			node.WriteString(c.Text())
			if c.IsLastInTextNode() {
				o.TextNodes = append(o.TextNodes, node.String())
				node.Reset()
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if !utf8.ValidString(c.Text()) {
				o.PartialRunes++
			}
			note("comment", c.Text(), c.SourceLocation().String())
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			n, _ := d.Name()
			p, _ := d.PublicID()
			s, _ := d.SystemID()
			note("doctype", n, p, s, d.SourceLocation().String())
			return nil
		}),
	)
	if err != nil {
		return o, err
	}

	if writeSize <= 0 {
		if _, err := w.Write([]byte(doc)); err != nil {
			w.Close()
			return o, err
		}
	} else {
		for i := 0; i < len(doc); i += writeSize {
			if _, err := w.Write([]byte(doc[i:min(i+writeSize, len(doc))])); err != nil {
				w.Close()
				return o, err
			}
		}
	}
	if err := w.Close(); err != nil {
		return o, err
	}
	o.Output = out.String()
	return o, nil
}

// A Difference is one observation that moved between two write sizes.
type Difference struct {
	Doc       string
	WriteSize int
	What      string
	Detail    string
}

func (d Difference) String() string {
	return fmt.Sprintf("%s at writes of %d: %s %s", d.Doc, d.WriteSize, d.What, d.Detail)
}

// WriteSizes are the patterns to compare against writing the document at once.
var WriteSizes = []int{1, 2, 3, 5, 7, 64, 1024}

// Check compares every document at every write size against writing it whole,
// and returns the observations that moved and the number of comparisons.
//
// TextCalls is expected to move and is reported separately by TextCallSpread.
func Check(docs map[string]string) ([]Difference, int, error) {
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)

	var diffs []Difference
	checks := 0
	for _, name := range names {
		base, err := Observe(docs[name], 0)
		if err != nil {
			return nil, checks, fmt.Errorf("%s: %w", name, err)
		}
		if base.PartialRunes > 0 {
			diffs = append(diffs, Difference{name, 0, "partial characters",
				fmt.Sprintf("%d handed over", base.PartialRunes)})
		}
		for _, n := range WriteSizes {
			got, err := Observe(docs[name], n)
			if err != nil {
				return nil, checks, fmt.Errorf("%s at %d: %w", name, n, err)
			}
			checks++
			add := func(what string, a, b []string) {
				if len(a) != len(b) {
					diffs = append(diffs, Difference{name, n, what,
						fmt.Sprintf("%d against %d", len(b), len(a))})
					return
				}
				for i := range a {
					if a[i] != b[i] {
						diffs = append(diffs, Difference{name, n, what,
							fmt.Sprintf("#%d %q against %q", i, b[i], a[i])})
						return
					}
				}
			}
			add("events", base.Events, got.Events)
			add("text nodes", base.TextNodes, got.TextNodes)
			if base.Output != got.Output {
				diffs = append(diffs, Difference{name, n, "output", "bytes differ"})
			}
			if got.PartialRunes > 0 {
				diffs = append(diffs, Difference{name, n, "partial characters",
					fmt.Sprintf("%d handed over", got.PartialRunes)})
			}
		}
	}
	return diffs, checks, nil
}

// TextCallSpread reports how much the text-handler call count moves for one
// document, which is the thing that is meant to move.
func TextCallSpread(doc string) (lo, hi int, err error) {
	base, err := Observe(doc, 0)
	if err != nil {
		return 0, 0, err
	}
	lo, hi = base.TextCalls, base.TextCalls
	for _, n := range WriteSizes {
		got, err := Observe(doc, n)
		if err != nil {
			return 0, 0, err
		}
		lo, hi = min(lo, got.TextCalls), max(hi, got.TextCalls)
	}
	return lo, hi, nil
}

func main() {
	docs := Documents()
	diffs, checks, err := Check(docs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chunkinvariance:", err)
		os.Exit(1)
	}
	fmt.Printf("%d documents, %d comparisons\n", len(docs), checks)
	for _, d := range diffs {
		fmt.Println(" ", d)
	}

	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		lo, hi, err := TextCallSpread(docs[n])
		if err != nil {
			fmt.Fprintln(os.Stderr, "chunkinvariance:", err)
			os.Exit(1)
		}
		if lo != hi {
			fmt.Printf("  %-14s text handler ran %d to %d times, and saw the same nodes\n", n, lo, hi)
		}
	}
	if len(diffs) > 0 {
		os.Exit(1)
	}
	fmt.Println("everything except text chunking is invariant")
}

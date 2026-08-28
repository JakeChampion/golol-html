// Command numbering prefixes each heading with its position in the document's
// outline: 1, 1.1, 1.2, 2, and so on.
//
//	numbering -min 2 -max 4 < page.html > out.html
//	numbering -style roman < page.html
//
// It is a single pass, unlike examples/gip/toc and examples/gip/slugs, and the
// reason is worth naming: the number depends only on headings already seen, so it
// is known when the start tag goes past. Anything that depends on the rest of the
// document is not, and needs two passes or a buffer.
//
// The number is inserted with Prepend, and the heading's own text is read by a
// text handler in the same rewrite. That works because inserted content is
// emitted verbatim and never dispatched to handlers: the accumulator sees
// "Intro", not "1. Intro".
//
// What that does not buy is a second run for free. Prepend happens at the start
// tag, before a character of the heading has been seen, and "does this heading
// already carry a number" cannot be answered until the end tag - by which time
// the label has been written and cannot be withdrawn. So running this over its
// own output compounds: "1. Intro" becomes "1. 1. Intro". -skip-numbered does not
// prevent that either; it decides whether such a heading is counted as numbered
// or reported as skipped, and the report names how many now carry two labels. A
// pipeline that has to be re-runnable wants the second pass examples/gip/toc and
// examples/gip/slugs pay for, where the decision is made before anything is
// written.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	minLevel := flag.Int("min", 1, "shallowest heading level to number")
	maxLevel := flag.Int("max", 6, "deepest heading level to number")
	style := flag.String("style", "decimal", "decimal or roman for the top level")
	sep := flag.String("sep", ". ", "separator between the number and the heading text")
	skip := flag.Bool("skip-numbered", true,
		"report a heading that already starts with a number as skipped rather than numbered; "+
			"the label is inserted either way, because it goes in before the text is known")
	flag.Parse()

	if *minLevel < 1 || *maxLevel > 6 || *minLevel > *maxLevel {
		fmt.Fprintln(os.Stderr, "numbering: -min and -max must satisfy 1 <= min <= max <= 6")
		os.Exit(2)
	}
	if *style != "decimal" && *style != "roman" {
		fmt.Fprintln(os.Stderr, "numbering: -style must be decimal or roman")
		os.Exit(2)
	}

	n := newNumberer()
	n.minLevel, n.maxLevel, n.style, n.sep, n.skipNumbered = *minLevel, *maxLevel, *style, *sep, *skip
	if err := n.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "numbering:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, n.report())
}

type numberer struct {
	minLevel, maxLevel int
	style              string
	sep                string
	skipNumbered       bool

	// counters[i] is the count at depth i, relative to minLevel.
	counters []int
	numbered int
	skipped  int
	// jumped counts headings that went more than one level deeper than the last,
	// which shows up in the label as a zero: an h2 followed by an h4 is 1.2.0.1,
	// because there was no h3 to count. That is the document's outline being
	// wrong rather than the label, so it is reported rather than smoothed over.
	jumped int
	// lastDepth starts at -1 so that a document whose first numbered heading is
	// deeper than -min counts as having skipped a level, which it has: an h2
	// with no h1 above it numbers as 0.1.
	lastDepth int
	// open is the heading being read, so its existing text can be inspected at
	// the end tag even though the number was already inserted at the start.
	open *pending
	text strings.Builder
}

// newNumberer returns a numberer with the defaults, including the lastDepth
// sentinel that makes a document starting below -min count as a skipped level.
func newNumberer() *numberer {
	return &numberer{
		minLevel: 1, maxLevel: 6, style: "decimal", sep: ". ",
		skipNumbered: true, lastDepth: -1,
	}
}

type pending struct {
	label string
	depth int
}

func (n *numberer) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, n.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (n *numberer) selector() string {
	parts := make([]string, 0, 6)
	for l := n.minLevel; l <= n.maxLevel; l++ {
		parts = append(parts, "h"+strconv.Itoa(l))
	}
	return strings.Join(parts, ", ")
}

func (n *numberer) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(n.selector(), func(e *lolhtml.Element) error {
			level, err := strconv.Atoi(strings.TrimPrefix(e.TagName(), "h"))
			if err != nil {
				return nil
			}
			depth := level - n.minLevel
			if depth > n.lastDepth+1 {
				n.jumped++
			}
			n.lastDepth = depth

			// Advance the outline. A level deeper than the last resets the
			// counters below it, which is what makes 1.1, 1.2, then 2 come out
			// right rather than 1.1, 1.2, 2.3.
			for len(n.counters) <= depth {
				n.counters = append(n.counters, 0)
			}
			n.counters = n.counters[:depth+1]
			n.counters[depth]++

			label := n.label()
			n.open = &pending{label: label, depth: depth}
			n.text.Reset()
			n.numbered++

			// Text, not HTML: the label is ours, but Text is the honest content
			// type for something that is text, and it keeps a separator like
			// " > " from becoming markup.
			return e.Prepend(label+n.sep, lolhtml.Text)
		}),

		// The heading's own text, which does not include the label just
		// inserted: content a handler inserts is emitted verbatim and never
		// dispatched, so this accumulator sees the document's text only.
		lolhtml.OnText(n.selector(), func(t *lolhtml.TextChunk) error {
			if n.open != nil {
				n.text.WriteString(t.Text())
			}
			return nil
		}),

		lolhtml.OnElement(n.selector(), func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				open := n.open
				n.open = nil
				if open == nil {
					return nil
				}
				// A heading that already carries a number would end up with two,
				// so it is counted as skipped. The label cannot be withdrawn -
				// it is already in the output - which is why this is reported
				// rather than fixed, and why -skip-numbered has to be decided
				// before the text is known.
				if n.skipNumbered && startsWithNumber(strings.TrimSpace(n.text.String())) {
					n.skipped++
					n.numbered--
				}
				return nil
			})
		}),
	}
}

// label renders the current counter path.
func (n *numberer) label() string {
	parts := make([]string, 0, len(n.counters))
	for i, c := range n.counters {
		if i == 0 && n.style == "roman" {
			parts = append(parts, roman(c))
			continue
		}
		parts = append(parts, strconv.Itoa(c))
	}
	return strings.Join(parts, ".")
}

// startsWithNumber recognises a heading that is already numbered, so a second
// run can be reported rather than compounding. It accepts "1", "1.2", "IV" and
// the separators those are usually followed by.
func startsWithNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	digits := false
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		if s[i] != '.' {
			digits = true
		}
		i++
	}
	if digits {
		return true
	}

	i = 0
	for i < len(s) && strings.ContainsRune("IVXLCDM", rune(s[i])) {
		i++
	}
	if i == 0 {
		return false
	}
	// A single leading capital could be a word: require a separator after it.
	rest := strings.TrimLeft(s[i:], " ")
	return len(rest) < len(s[i:]) || strings.HasPrefix(s[i:], ".")
}

var romanNumerals = []struct {
	value int
	sym   string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
	{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
	{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

func roman(n int) string {
	if n <= 0 {
		return strconv.Itoa(n)
	}
	var sb strings.Builder
	for _, r := range romanNumerals {
		for n >= r.value {
			sb.WriteString(r.sym)
			n -= r.value
		}
	}
	return sb.String()
}

func (n *numberer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "numbered=%d already-numbered=%d skipped-a-level=%d\n",
		n.numbered, n.skipped, n.jumped)
	if n.jumped > 0 {
		fmt.Fprintf(&sb, "note: %d heading(s) go more than one level deeper than the "+
			"heading before them, which shows in the label as a zero\n", n.jumped)
	}
	if n.skipped > 0 {
		fmt.Fprintf(&sb, "note: %d heading(s) already started with a number and now carry two; "+
			"the label is inserted before the text is known, so this cannot be undone mid-stream\n",
			n.skipped)
	}
	return sb.String()
}

func numberString(in string, opts ...func(*numberer)) (string, *numberer, error) {
	n := newNumberer()
	for _, o := range opts {
		o(n)
	}
	var out bytes.Buffer
	err := n.run(strings.NewReader(in), &out)
	return out.String(), n, err
}

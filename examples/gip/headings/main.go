// Command headings reports headings that skip a level, and the other things a
// heading outline can get wrong.
//
//	page.html:12:3: h1 -> h3 skips h2
//	page.html:40:1: the document has no h1
//
// It is the first of these programs that does not rewrite anything, and that
// changes three things.
//
// The output goes to [io.Discard]. Not because the document is unwanted but
// because a read-only rewrite is not quite free: the text path decodes and
// re-encodes, so a document holding bytes that are not valid in the declared
// encoding comes out different for having had a text handler registered - and this
// program needs a text handler, to tell an empty heading from one with a name.
// Discarding the output is what makes that irrelevant rather than a bug to
// explain. See [lolhtml.OnText].
//
// The findings need line and column numbers, and the library reports byte offsets.
// That is the right division: the caller owns the bytes, so the caller owns the
// line numbers. This one records where the newlines are as it feeds the document
// in, and converts at the end - which also means the columns are counted in
// characters rather than bytes, because a column is a thing a person counts in an
// editor.
//
// And the rule is a decision rather than a specification. HTML's own outline
// algorithm - the one where a <section> renumbers the headings inside it - was
// never implemented by any browser or screen reader, so a program that applied it
// would be reporting on a document nobody is reading. What this reports on is the
// document order of the headings, which is what a screen reader's heading list
// shows.
//
// Four findings:
//
//	a level skipped        h1 then h3, or an h3 with no heading before it
//	no h1                  nothing to be the page's name
//	more than one h1        allowed, and worth knowing about
//	an empty heading        in the list with no name, which is a dead entry
//
// Two things count as a heading. The obvious six, and role="heading" with an
// aria-level, which is what a component library emits when it wants a heading that
// is not an h-tag. An aria-level on an h-tag overrides the tag, because that is
// what a screen reader does with it - so <h2 aria-level="4"> is a level 4 heading
// and a program reading the tag name alone gets it wrong.
//
// What is not in the accessibility tree is not a heading: this stays out of
// anything inside [hidden] or aria-hidden="true". It cannot see CSS, so a heading
// hidden by a stylesheet is one it will report on, and there is no way round that
// from here.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what is wrong.
type Kind string

const (
	Skipped   Kind = "skips"
	NoH1      Kind = "no-h1"
	ManyH1    Kind = "many-h1"
	EmptyName Kind = "empty"
)

// A Finding is one thing worth reporting, with where it was.
type Finding struct {
	Kind Kind
	// At is the byte offset of the heading's start tag, and Line and Column are
	// that offset in the terms a person reads.
	At           int
	Line, Column int
	// Message says what is wrong, without the location.
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%d:%d: %s", f.Line, f.Column, f.Message)
}

// A Result is the report.
type Result struct {
	Findings []Finding
	// Headings seen, and Hidden ones skipped.
	Headings, Hidden int
}

// OK reports whether the document had nothing worth reporting.
func (r Result) OK() bool { return len(r.Findings) == 0 }

func (r Result) String() string {
	return fmt.Sprintf("headings: %d headings, %d hidden; %d findings",
		r.Headings, r.Hidden, len(r.Findings))
}

// A heading as this program sees it.
type heading struct {
	level int
	at    int
	// named is whether anything in it would give a screen reader something to say.
	named bool
}

// Check reads doc and reports on its headings. Nothing is written anywhere: the
// document goes to io.Discard, because the report is the output.
func Check(doc []byte) (Result, error) {
	c := &checker{}
	w, err := lolhtml.NewWriter(io.Discard, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.report(doc), nil
}

type checker struct {
	res Result
	// open is the headings whose end tags have not arrived, innermost last. A
	// stack rather than one pointer: HTML does not allow a heading inside a
	// heading, and a document can write one anyway - "<h1>a<h2>b</h2>" - and then
	// two headings are open at once as far as the token stream is concerned.
	open []*heading
	// hidden is how many elements this position is inside that are out of the
	// accessibility tree.
	hidden int
	// found is every heading, finished. Not in document order: an implied end tag
	// finishes the inner heading first, so this is sorted by offset before use.
	found []heading
}

func (c *checker) options() []lolhtml.Option {
	return []lolhtml.Option{
		// The hidden regions first, because two element handlers on the same
		// element run in the order they were registered - and an element that is
		// itself hidden has to be known to be hidden before it is counted as a
		// heading. That ordering rule is documented; this program depends on it.
		lolhtml.OnElement("[hidden],[aria-hidden=true]", c.hiddenRegion),
		// Narrow selectors rather than one "*" handler: see the cost section.
		lolhtml.OnElement("h1,h2,h3,h4,h5,h6,[role=heading]", c.heading),
		lolhtml.OnDocumentText(c.text),
		// A heading closed by a sibling's start tag with nothing enclosing it -
		// "<h1>a<h2>b</h2>" - gets no end tag callback at all, which the
		// documentation on OnEndTag says in as many words. The document end is
		// where those are finished.
		lolhtml.OnDocumentEnd(c.end),
	}
}

func (c *checker) hiddenRegion(e *lolhtml.Element) error {
	// aria-hidden="false" is not hidden, and the attribute selector cannot say so.
	if v, ok := e.Attribute("aria-hidden"); ok && !strings.EqualFold(strings.TrimSpace(v), "true") {
		if _, isHidden := e.Attribute("hidden"); !isHidden {
			return nil
		}
	}
	c.hidden++
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		c.hidden--
		return nil
	})
}

func (c *checker) heading(e *lolhtml.Element) error {
	level, ok := headingLevel(e)
	if !ok {
		return nil
	}
	if c.hidden > 0 {
		c.res.Hidden++
		return nil
	}
	c.res.Headings++
	h := &heading{level: level, at: e.SourceLocation().Start}
	// A heading's name can come from an aria-label rather than from its text.
	if label, has := e.Attribute("aria-label"); has && strings.TrimSpace(label) != "" {
		h.named = true
	}
	if !e.CanHaveContent() || e.IsSelfClosing() {
		c.found = append(c.found, *h)
		return nil
	}
	c.open = append(c.open, h)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		// This heading's own entry, wherever it is in the stack: an end tag that
		// is not this element's closes it and anything still open inside it.
		for i, candidate := range c.open {
			if candidate == h {
				c.found = append(c.found, *h)
				c.open = append(c.open[:i], c.open[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

// end finishes any heading still open, which is one nothing closed.
func (c *checker) end(*lolhtml.DocumentEnd) error {
	for _, h := range c.open {
		c.found = append(c.found, *h)
	}
	c.open = nil
	return nil
}

// text gives the innermost open heading a name if there is anything in it to say.
func (c *checker) text(t *lolhtml.TextChunk) error {
	if len(c.open) == 0 {
		return nil
	}
	h := c.open[len(c.open)-1]
	if h.named {
		return nil
	}
	if strings.TrimSpace(t.Text()) != "" {
		h.named = true
	}
	return nil
}

// headingLevel is the level this element is a heading at, if it is one.
//
// aria-level wins over the tag name, because that is what a screen reader does
// with it. An element with role="heading" and no aria-level is a heading of level
// 2 by the ARIA specification's default, and one whose aria-level does not parse is
// not treated as a heading at all - guessing which level a typo meant would be
// this program inventing a document.
func headingLevel(e *lolhtml.Element) (int, bool) {
	tag := e.TagName()
	role, hasRole := e.Attribute("role")
	isRole := hasRole && strings.EqualFold(strings.TrimSpace(role), "heading")
	tagLevel := 0
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		tagLevel = int(tag[1] - '0')
	}
	if tagLevel == 0 && !isRole {
		return 0, false
	}

	if v, has := e.Attribute("aria-level"); has {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return 0, false
		}
		return n, true
	}
	if tagLevel > 0 {
		return tagLevel, true
	}
	return 2, true // ARIA's default for role="heading"
}

// report turns the headings into findings, with the locations converted.
func (c *checker) report(doc []byte) Result {
	lines := newlines(doc)
	add := func(kind Kind, at int, format string, args ...any) {
		line, col := position(lines, doc, at)
		c.res.Findings = append(c.res.Findings, Finding{
			Kind: kind, At: at, Line: line, Column: col,
			Message: fmt.Sprintf(format, args...),
		})
	}

	// Document order, which is offset order: the finishing order is not, because
	// an implied end tag finishes the innermost heading first.
	found := append([]heading(nil), c.found...)
	sort.Slice(found, func(i, j int) bool { return found[i].at < found[j].at })

	previous := 0
	h1s := 0
	for _, h := range found {
		if h.level == 1 {
			h1s++
		}
		switch {
		case previous == 0 && h.level != 1:
			add(Skipped, h.at, "the first heading is h%d; a document starts at h1", h.level)
		case previous > 0 && h.level > previous+1:
			add(Skipped, h.at, "h%d -> h%d skips h%d", previous, h.level, previous+1)
		}
		if !h.named {
			add(EmptyName, h.at, "h%d has no text, so it is a heading with no name", h.level)
		}
		// The previous heading is the one before this in document order, whichever
		// direction it went: going back up a level is fine, and the next heading
		// after an h4 that went back to h2 may be an h3.
		previous = h.level
	}
	if h1s == 0 && len(found) > 0 {
		add(NoH1, found[0].at, "the document has no h1")
	}
	if h1s > 1 {
		add(ManyH1, found[0].at, "the document has %d h1 elements", h1s)
	}
	return c.res
}

// newlines is the offset of every line feed in the document, which is all that is
// needed to turn an offset into a line.
func newlines(doc []byte) []int {
	var at []int
	for i, b := range doc {
		if b == '\n' {
			at = append(at, i)
		}
	}
	return at
}

// position is the one-based line and column of an offset. The column counts
// characters rather than bytes, because that is what an editor shows.
func position(lines []int, doc []byte, at int) (line, column int) {
	line = 1
	start := 0
	for _, nl := range lines {
		if nl >= at {
			break
		}
		line++
		start = nl + 1
	}
	if at > len(doc) {
		at = len(doc)
	}
	return line, utf8.RuneCount(doc[start:at]) + 1
}

func main() {
	name := "-"
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: headings [file] < page")
		os.Exit(2)
	}
	var doc []byte
	var err error
	if len(os.Args) == 2 {
		name = os.Args[1]
		doc, err = os.ReadFile(name)
	} else {
		doc, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "headings:", err)
		os.Exit(1)
	}

	res, err := Check(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "headings:", err)
		os.Exit(1)
	}
	for _, f := range res.Findings {
		fmt.Printf("%s:%s\n", name, f)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

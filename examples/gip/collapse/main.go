// Command collapse collapses runs of insignificant whitespace to a single space
// and leaves the elements where whitespace is significant alone.
//
// The rule a browser applies is that a run of spaces, tabs and newlines in
// element content renders as one space, so a document can be indented for a
// reader and served without the indentation. Doing it in a stream is a state
// machine with one bit: was the last character written a space. That bit has to
// survive things the whitespace itself does not care about.
//
// A run can be split across two writes, because a text node arrives in as many
// chunks as the writes it was fed in - so the bit outlives a chunk. A run can
// also be split by markup, since a tag is not a character:
//
//	<p>a  <b>  b</b></p>  ->  <p>a <b>b</b></p>
//
// which is one run of five characters as far as rendering is concerned. So the
// bit outlives a text node too, and the program has no selector for "the text
// either side of an inline tag": it keeps the bit and lets the tags go past.
//
// Where whitespace is significant the program has to keep out, and that is not a
// list it invents. A <pre> and a <textarea> render their whitespace, and every
// element whose content is not markup at all - a script, a style, an xmp - holds
// something that is not prose and must not be reflowed: collapsing inside a
// script would rewrite a template literal, and inside a style a selector. So the
// test is [lolhtml.IsRawText] plus pre and its obsolete synonym listing, and the
// program counts the regions it stayed out of.
//
// Knowing when such a region ends is the hard part, and this program does the
// cheap version of it. It counts depth, and treats an end tag callback as the end
// of the element - which is right when the element has its own end tag and right
// when an ancestor's end tag closed it, and late when a sibling's start tag did:
//
//	<ul><li><pre>a  b<li>c  d</ul>
//
// The pre was closed by the second <li> and the callback arrives at </ul>, so
// "c  d" is treated as preformatted and comes out with its two spaces. Being late
// here means leaving whitespace alone, which is the harmless direction; getting it
// right needs the stack of open elements and the specification's implied end tags,
// which examples/gip/markdown and examples/gip/depth pay for. The case is
// measured rather than assumed - see TestAPreClosedByASiblingIsOverpaid.
//
// A comment is not a character either, and the program treats it the way it
// treats a tag: it renders as nothing, so the whitespace on both sides of one is
// a single run. Its own text is left alone, because a comment can be a licence
// banner or a conditional and neither is prose.
//
// Two things this deliberately does not do.
//
// It never deletes a space entirely, only shortens a run to one. Whether the
// remaining space is visible depends on whether the surrounding elements are
// inline, which is a CSS question, and a minifier that answers it with a list of
// block element names gets it wrong on the first page that sets display in a
// stylesheet.
//
// It works on the characters the document wrote, not on the characters a browser
// would decode: text is reported as the document spells it, so "a&#32;&#32;b" is
// twelve characters with no whitespace in them and is left as it is. Collapsing
// it would mean decoding references, and writing text back decoded would change
// every other reference on the page.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Verbatim are the elements this stays out of, on top of everything
// lolhtml.IsRawText reports. A pre renders its whitespace; listing is the
// obsolete synonym a parser still treats the same way.
var Verbatim = map[string]bool{"pre": true, "listing": true}

// A Result counts what the pass did and what it left.
type Result struct {
	// BytesIn and BytesOut are the text this saw and the text it wrote, so the
	// difference is what a page saves.
	BytesIn, BytesOut int
	// Runs is the number of whitespace runs that came out different from how they
	// went in.
	Runs int
	// Regions is the number of elements this stayed out of.
	Regions int
	// LateRegions is how many of those were closed by a sibling's start tag, so
	// the program kept out of more than the element. See the note on depth.
	LateRegions int
}

func (r Result) String() string {
	return fmt.Sprintf("collapse: %d bytes of text -> %d (%d saved), %d runs collapsed, %d verbatim regions (%d closed late)",
		r.BytesIn, r.BytesOut, r.BytesIn-r.BytesOut, r.Runs, r.Regions, r.LateRegions)
}

type collapser struct {
	res Result
	// depth is how many verbatim elements are open. Whitespace inside them is
	// significant, so the program does nothing at all.
	depth int
	// lastSpace is the whole state: was the last character written a space. It
	// survives chunk boundaries and markup, which is the point of it.
	lastSpace bool
}

// Collapse copies src to dst with insignificant whitespace collapsed.
func Collapse(dst io.Writer, src io.Reader) (Result, error) {
	c := &collapser{}
	w, err := lolhtml.NewWriter(dst, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.res, nil
}

func (c *collapser) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.element),
		lolhtml.OnDocumentText(c.text),
	}
}

func (c *collapser) element(e *lolhtml.Element) error {
	name := e.TagName()
	if !Verbatim[name] && !lolhtml.IsRawText(name) {
		return nil
	}
	if !e.CanHaveContent() {
		// A self-closing foreign element - <svg><title/>, <svg><style/> - holds
		// nothing, so there is no region to stay out of. The depth must not be
		// raised for it either: OnEndTag returns an error for an element that
		// cannot have content, so nothing would ever lower it again and every
		// text chunk in the rest of the document would be treated as
		// significant. That is the whole of the failure - the output is
		// unchanged from there on and the report says nothing about it - so the
		// test goes before the counters rather than after them.
		return nil
	}
	c.res.Regions++
	c.depth++
	// A <plaintext> is the one element this leaves permanently raised, and that
	// is correct: it ends only with the input, so OnEndTag returns nil and this
	// handler never runs. Nothing after it is prose.
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		if t.Name() != name {
			// The element was closed by something that is not its own end tag. If
			// that was a sibling's start tag the element ended earlier than this,
			// and everything between has been left alone: late, and in the
			// harmless direction.
			c.res.LateRegions++
		}
		c.depth--
		// Whatever the region ended with, the next space outside it is not
		// following a space this program wrote.
		c.lastSpace = false
		return nil
	})
}

func (c *collapser) text(t *lolhtml.TextChunk) error {
	s := t.Text()
	if s == "" {
		return nil
	}
	if c.depth > 0 {
		// Significant whitespace. Not counted as bytes seen either: this pass did
		// not consider it.
		c.lastSpace = false
		return nil
	}
	c.res.BytesIn += len(s)
	out, runs := c.collapse(s)
	c.res.BytesOut += len(out)
	c.res.Runs += runs
	if out == s {
		return nil
	}
	if out == "" {
		t.Remove()
		return nil
	}
	// Written back as markup rather than as text: the chunk is the document's own
	// spelling, references and all, and inserting it as text would escape every
	// ampersand in it a second time.
	return t.Replace(out, lolhtml.HTML)
}

// collapse shortens every run of whitespace to a single space, carrying the state
// that says whether the previous character - in this chunk, in the previous
// chunk, or before the tag before that - was already one.
//
// It works a byte at a time, which is safe because no byte of a multi-byte
// sequence is ASCII whitespace, and because the only characters it writes are
// spaces.
func (c *collapser) collapse(s string) (string, int) {
	var b strings.Builder
	runs := 0
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			b.WriteByte(s[i])
			c.lastSpace = false
			continue
		}
		j := i
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		run := s[i:j]
		switch {
		case c.lastSpace:
			runs++ // dropped entirely: a space is already there
		case run == " ":
			b.WriteByte(' ') // already what it should be
			c.lastSpace = true
		default:
			b.WriteByte(' ')
			c.lastSpace = true
			runs++
		}
		i = j - 1
	}
	return b.String(), runs
}

// isSpace is the whitespace an HTML parser collapses: space, tab, line feed,
// form feed and carriage return. Not a non-breaking space, which is a character
// that renders.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

func main() {
	res, err := Collapse(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "collapse:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

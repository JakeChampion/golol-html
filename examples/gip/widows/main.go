// Command widows joins the last two words of every heading with a
// non-breaking space, so a heading cannot wrap with one word alone on the last
// line.
//
// The rule is old typesetting practice and the implementation is a one-line
// regular expression in most languages. It is not one here, and the reason is
// the ordering constraint: an insertion can only go where the rewriter has not
// been. The gap to replace is the last one in the heading, and you do not know
// which gap is the last one until the heading ends - by which time the gap has
// been written out.
//
//	<h1>The quick brown fox</h1>
//	                  ^ this space, known only at </h1>
//
// So this program buys the ability to edit backwards, and the price is a buffer:
// the heading's content is removed as it arrives, rebuilt in memory, and written
// back at the end tag with the gap replaced. A heading is small, so the buffer is
// bounded - and bounded by a number, not by hope, because a document can put
// anything inside an <h1>. Past MaxBuffer bytes the heading is flushed where it
// stands and passed through, and counted.
//
// Rebuilding a region makes this program the serialiser for it, and three
// measured things follow.
//
// Text and attribute values are reported as the document spells them: character
// references are not decoded, so "a &amp; b" arrives with the six characters of
// the reference in it. Writing them back verbatim is therefore right, and
// escaping them would double every ampersand on the page. The one character that
// has to be escaped is the quote inside an attribute value, because a
// single-quoted attribute can contain a bare double quote and this program writes
// double-quoted attributes: see the package documentation on building markup
// yourself.
//
// An element's end tag is not always its own. In "<h1>a <em>b</h1>" the em is
// closed by "</h1>", and taking the em's tags away takes that token with them -
// so the heading loses its closing tag unless the program writes it back. Nothing
// reports a problem; the output parses, with everything after the heading inside
// it. This program watches for an end tag whose name is not the element's own,
// which is the guard [lolhtml.Element.OnEndTag] documents, and uses it to decide
// what to write rather than only where.
//
// An element that is never closed gets no end tag callback at all, so
// "<h1>a <em>b" would lose its buffered content entirely. OnDocumentEnd is the
// backstop: whatever is still buffered when the document ends is written there.
//
// An element whose content is not markup cannot be rebuilt this way at all -
// unwrapping it turns its text into elements, which is the hazard
// [lolhtml.Element.RemoveAndKeepContent] documents. A <script> inside a heading
// is unusual and entirely legal, so the program asks [lolhtml.IsRawText] and
// gives up on that heading rather than rewriting a payload into markup.
//
// The join itself is applied to the buffered text and not to the markup, so a
// gap before an inline element is still a gap:
//
//	<h1>A long <em>title</em></h1>  ->  <h1>A long&#160;<em>title</em></h1>
//
// and a heading whose last word already carries a non-breaking space - as a
// character or as a reference - is left alone, which is what makes running this
// twice the same as running it once.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// nbsp is the character the gap becomes: a space that is not a break
// opportunity.
const nbsp = ' '

// MinWords is how many words a heading needs before the last gap is worth
// joining. A two-word heading that wraps puts one word on each line, which is
// not a widow, and joining it can only make the heading overflow instead.
const MinWords = 3

// MaxBuffer is how much of a heading is held in memory. Past this the heading is
// written out as it came in: the alternative is a document choosing how much
// memory this program uses.
const MaxBuffer = 4096

// Headings are the elements this applies to.
var Headings = map[string]bool{"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true}

// nonBreaking are the ways a document can already have joined the last two
// words. The first is what this program writes; the rest are what a person
// writes.
var nonBreaking = []string{" ", "&nbsp", "&#160", "&#xa0"}

// A Result counts what happened to each heading. Every heading is in exactly one
// of these.
type Result struct {
	Headings int
	// Joined had their last gap replaced.
	Joined int
	// AlreadyJoined ended in a word already carrying a non-breaking space, so
	// this had run before or the document did it by hand.
	AlreadyJoined int
	// TooFewWords had fewer than MinWords, or no gap at all.
	TooFewWords int
	// RawText contained an element whose content is not markup.
	RawText int
	// TooLong exceeded MaxBuffer.
	TooLong int
}

func (r Result) String() string {
	return fmt.Sprintf("widows: %d headings: %d joined, %d already joined, %d too few words, %d raw text, %d too long",
		r.Headings, r.Joined, r.AlreadyJoined, r.TooFewWords, r.RawText, r.TooLong)
}

// A piece is one part of a buffered heading. Markup is rebuilt and written back
// as-is; text is held as the document spelled it, as runes, because the join
// edits it.
type piece struct {
	markup string
	text   []rune
}

// A ref names one rune of one text piece, so the join can walk the heading's
// text backwards across the markup in between.
type ref struct{ p, i int }

type joiner struct {
	res Result

	// gen changes when a heading ends, so an end-tag callback left over from a
	// heading that was abandoned does nothing.
	gen     int
	heading string
	// bailed says this heading is being passed through: everything from here on
	// is left where it is.
	bailed bool
	// ateEnd says an inner element's removal took the heading's own closing tag
	// with it, so this program has to write it back.
	ateEnd bool

	pieces []piece
	size   int
	// open is the inner elements whose tags have been taken away and whose end
	// tag has not arrived.
	open []string
}

// Join copies src to dst with the last gap of every heading made
// non-breaking.
func Join(dst io.Writer, src io.Reader) (Result, error) {
	j := &joiner{}
	// The options are built here, per rewrite, because the handlers close over j.
	// See the note on lolhtml.Option: an Option can be reused, and the function
	// inside it is shared with every Writer it is given to.
	w, err := lolhtml.NewWriter(dst, j.options()...)
	if err != nil {
		return j.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return j.res, err
	}
	if err := w.Close(); err != nil {
		return j.res, err
	}
	return j.res, nil
}

func (j *joiner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", j.element),
		lolhtml.OnDocumentText(j.text),
		lolhtml.OnDocumentComment(j.comment),
		lolhtml.OnDocumentEnd(j.end),
	}
}

func (j *joiner) inside() bool { return j.heading != "" }

func (j *joiner) element(e *lolhtml.Element) error {
	name := e.TagName()

	if !j.inside() {
		if !Headings[name] {
			return nil
		}
		return j.start(e)
	}

	if Headings[name] {
		// A heading inside a heading: the parser closes the first one, and
		// whether that arrives as an end tag is the parser's business rather than
		// this program's. Give up on the outer heading, keep what has been
		// buffered where it was, and start again on this one.
		if err := j.flush(e.Before); err != nil {
			return err
		}
		j.res.TooFewWords++
		j.reset()
		return j.start(e)
	}

	if j.bailed {
		return nil
	}

	// An element whose content is not markup cannot be rebuilt as markup: taking
	// its tags away turns its text into elements. Nothing here is worth that, so
	// this heading is passed through from here on.
	if lolhtml.IsRawText(name) {
		j.res.RawText++
		return j.bail(e.Before)
	}

	if err := j.add(piece{markup: startTag(e)}, e.Before); err != nil {
		return err
	}
	if j.bailed {
		// The buffer filled on this element, so it stays where it is and so does
		// everything after it in this heading.
		return nil
	}

	if !e.CanHaveContent() {
		// Nothing inside and no end tag, so the whole element is in the buffer.
		//
		// CanHaveContent and not IsSelfClosing, which reports how the tag was
		// written rather than whether the element is empty. In HTML a trailing
		// slash is ignored, so <a href="/x"/> goes on to have content and an end
		// tag like any other, and Remove takes the element and its whole
		// subtree - here the rest of the heading, ending with the heading's own
		// end tag. The slash is only decisive in foreign content, where
		// CanHaveContent is false and this branch is the one that runs.
		e.Remove()
		return nil
	}

	gen := j.gen
	if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
		if j.gen != gen {
			return nil
		}
		j.pop(name)
		if t.Name() != name {
			// This element had no end tag of its own: the token that closed it
			// belongs to an enclosing element, and removing this element's tags
			// removed that token. It is not this element's to write back - the
			// element it belongs to writes it, and if that is the heading, the
			// heading has to know.
			if t.Name() == j.heading {
				j.ateEnd = true
			}
			return nil
		}
		if j.bailed {
			// The end tag was taken away with the start tag, so it has to go back
			// or the element never closes.
			return t.Before("</"+name+">", lolhtml.HTML)
		}
		return j.add(piece{markup: "</" + name + ">"}, t.Before)
	}); err != nil {
		return err
	}
	j.open = append(j.open, name)
	// The tags go; the content is removed piece by piece as it arrives.
	e.RemoveAndKeepContent()
	return nil
}

func (j *joiner) start(e *lolhtml.Element) error {
	name := e.TagName()
	j.res.Headings++
	j.heading = name
	j.bailed, j.ateEnd = false, false
	j.pieces, j.size, j.open = j.pieces[:0], 0, j.open[:0]
	gen := j.gen
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		if j.gen != gen {
			return nil
		}
		return j.finish(t)
	})
}

func (j *joiner) text(t *lolhtml.TextChunk) error {
	if !j.inside() || j.bailed {
		return nil
	}
	if s := t.Text(); s != "" {
		if err := j.add(piece{text: []rune(s)}, t.Before); err != nil {
			return err
		}
		if j.bailed {
			return nil // buffered nothing, so remove nothing
		}
	}
	t.Remove()
	return nil
}

func (j *joiner) comment(c *lolhtml.Comment) error {
	if !j.inside() || j.bailed {
		return nil
	}
	if err := j.add(piece{markup: "<!--" + c.Text() + "-->"}, c.Before); err != nil {
		return err
	}
	if j.bailed {
		return nil
	}
	c.Remove()
	return nil
}

// end is the backstop for a heading the document never closed: nothing closes it,
// so no end tag callback runs, and without this the buffer would be dropped.
func (j *joiner) end(d *lolhtml.DocumentEnd) error {
	if !j.inside() {
		return nil
	}
	if j.bailed {
		j.reset()
		return nil
	}
	// Nothing to write back for the open elements: their end tags were never in
	// the document, so they were never removed from the output.
	j.count(joinLastGap(j.pieces))
	out := render(j.pieces)
	j.reset()
	if out == "" {
		return nil
	}
	return d.Append(out, lolhtml.HTML)
}

// add buffers a piece, or gives up on the heading if the buffer is full. insert
// is how to write at the position the caller is at, which is where the buffer
// has to go if it is being abandoned.
func (j *joiner) add(p piece, insert func(string, lolhtml.ContentType) error) error {
	size := len(p.markup)
	for _, r := range p.text {
		size += utf8.RuneLen(r)
	}
	if j.size+size > MaxBuffer {
		j.res.TooLong++
		if err := j.bail(insert); err != nil {
			return err
		}
		// This piece has not been removed from the output yet, and leaving it
		// alone is the whole of passing it through.
		return nil
	}
	j.pieces = append(j.pieces, p)
	j.size += size
	return nil
}

// bail writes out what has been buffered and stops buffering this heading.
func (j *joiner) bail(insert func(string, lolhtml.ContentType) error) error {
	if err := j.flush(insert); err != nil {
		return err
	}
	j.bailed = true
	return nil
}

func (j *joiner) flush(insert func(string, lolhtml.ContentType) error) error {
	if j.bailed || len(j.pieces) == 0 {
		return nil
	}
	s := render(j.pieces)
	j.pieces, j.size = j.pieces[:0], 0
	return insert(s, lolhtml.HTML)
}

func (j *joiner) finish(t *lolhtml.EndTag) error {
	if j.bailed {
		j.reset()
		return nil
	}
	j.count(joinLastGap(j.pieces))
	out := render(j.pieces)
	if j.ateEnd {
		// An inner element with no end tag of its own was closed by this tag, and
		// removing that element removed this tag. Written back here, after the
		// content, which is where it was.
		out += "</" + j.heading + ">"
	}
	j.reset()
	if out == "" {
		return nil
	}
	return t.Before(out, lolhtml.HTML)
}

func (j *joiner) count(o outcome) {
	switch o {
	case joined:
		j.res.Joined++
	case already:
		j.res.AlreadyJoined++
	default:
		j.res.TooFewWords++
	}
}

func (j *joiner) reset() {
	j.heading = ""
	j.bailed, j.ateEnd = false, false
	j.pieces, j.size, j.open = j.pieces[:0], 0, j.open[:0]
	j.gen++
}

func (j *joiner) pop(name string) {
	for i := len(j.open) - 1; i >= 0; i-- {
		if j.open[i] == name {
			j.open = j.open[:i]
			return
		}
	}
}

// startTag rebuilds an element's start tag.
//
// The names come from AttributeList rather than from the Attributes iterator,
// because the iterator lower-cases them and this is a rebuild: an <svg
// viewBox=""> inside a heading would come out as viewbox, which a browser
// ignores. In HTML the case does not matter; the rebuild cannot tell which it is
// looking at, so it keeps what the document wrote either way.
//
// Every attribute the parser reported is here, repeats included, so a duplicated
// name survives the rebuild as a duplicate.
//
// Values are written back as the document spelled them, because that is how they
// are reported: escaping them again would turn every &amp; into &amp;amp;. The
// quote is the exception - a single-quoted attribute may hold a bare double
// quote, and these are double-quoted - and it is the only character that can end
// the attribute.
func startTag(e *lolhtml.Element) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(e.TagName())
	for _, a := range e.AttributeList() {
		name, value := a.NamePreserveCase, a.Value
		b.WriteByte(' ')
		b.WriteString(name)
		if value != "" {
			b.WriteString(`="`)
			b.WriteString(strings.ReplaceAll(value, `"`, "&quot;"))
			b.WriteByte('"')
		}
	}
	if e.IsSelfClosing() {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteByte('>')
	return b.String()
}

func render(pieces []piece) string {
	var b strings.Builder
	for _, p := range pieces {
		if p.text == nil {
			b.WriteString(p.markup)
			continue
		}
		b.WriteString(string(p.text))
	}
	return b.String()
}

type outcome int

const (
	joined outcome = iota
	already
	tooFew
)

// isBreak reports whether r is whitespace a line can break at. A non-breaking
// space is not, which is the point of it: it is the character this program
// writes, and finding one is how it knows it has already run.
func isBreak(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
}

// isWordSep reports whether r separates two words. That is not the same question
// as isBreak: a non-breaking space still separates words, and a heading joined by
// an earlier run has as many words as it had before.
func isWordSep(r rune) bool { return isBreak(r) || r == nbsp }

// joinLastGap replaces the last gap in the heading's text with non-breaking
// spaces, editing the pieces in place.
//
// The text is the text pieces read in order with the markup between them
// ignored: markup inside a heading is inline, so a word can start on one side of
// a tag and finish on the other, and a gap before a tag is still a gap.
func joinLastGap(pieces []piece) outcome {
	var flat []ref
	words := 0
	inWord := false
	for p := range pieces {
		for i, r := range pieces[p].text {
			flat = append(flat, ref{p, i})
			if isWordSep(r) {
				inWord = false
				continue
			}
			if !inWord {
				inWord = true
				words++
			}
		}
	}
	if words < MinWords {
		return tooFew
	}
	at := func(k int) rune { return pieces[flat[k].p].text[flat[k].i] }

	k := len(flat) - 1
	// Trailing whitespace is not a gap: there is no word after it.
	for k >= 0 && isBreak(at(k)) {
		k--
	}
	// The last word, read backwards. If it already holds a non-breaking space
	// this has run before, and joining again would take the gap before it too.
	var word []rune
	for ; k >= 0 && !isBreak(at(k)); k-- {
		word = append(word, at(k))
	}
	if isJoined(word) {
		return already
	}
	if k < 0 {
		return tooFew // one word, however much whitespace is around it
	}
	// The gap, which can be more than one character and can have markup in it.
	gapEnd := k
	for ; k >= 0 && isBreak(at(k)); k-- {
	}
	if k < 0 {
		return tooFew // whitespace all the way back, so nothing to join to
	}
	for g := k + 1; g <= gapEnd; g++ {
		pieces[flat[g].p].text[flat[g].i] = nbsp
	}
	return joined
}

// isJoined reports whether the last word already carries a non-breaking space.
// word is backwards, which does not matter to the character and does to the
// references, so it is reversed first.
func isJoined(word []rune) bool {
	for i, j := 0, len(word)-1; i < j; i, j = i+1, j-1 {
		word[i], word[j] = word[j], word[i]
	}
	s := strings.ToLower(string(word))
	for _, ref := range nonBreaking {
		if strings.Contains(s, ref) {
			return true
		}
	}
	return false
}

func main() {
	res, err := Join(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "widows:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

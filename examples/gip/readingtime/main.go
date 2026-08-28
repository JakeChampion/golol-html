// Command readingtime counts the words in a document and estimates how long it
// takes to read, ignoring anything that is not prose.
//
// Counting words is the smallest possible streaming task and it has two ways to
// be wrong here, both of which come from the same place: a word is a pattern, and
// this library reports characters.
//
// A word can be split across chunks. Text arrives in pieces whose boundaries
// follow the writes, so counting with strings.Fields per chunk counts "hello"
// twice if the boundary falls inside it. Measured on a 200-word document written
// a byte at a time, that is 1093 words instead of 200. So the counter carries one
// bit of state - am I inside a word - across chunks, and never looks at more than
// one character at a time.
//
// A word can also be split across markup, and there the answer is the opposite.
// <p>hello<b>world</b></p> is one word: inline markup does not separate text, so
// the state has to survive an element boundary too. <p>hello</p><p>world</p> is
// two, because a block does separate. The counter therefore resets its state on
// blocks and keeps it across everything else.
//
// The rest is the same discipline as the other converters here: a character
// reference is decoded after the text node is accumulated, not per chunk, because
// a reference is another pattern a boundary can split - and it matters for
// counting, since "&#32;" is a word separator and "&amp;" is not.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"math"
	"os"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// DefaultWPM is the reading speed the estimate uses when none is given. Adult
// silent reading of prose is usually put between 200 and 250 words a minute; the
// number is a convention, not a measurement, and it is a parameter for that
// reason.
const DefaultWPM = 220

// blocks separate words. Anything not in this set is inline as far as counting
// is concerned, which is the assumption that makes <b> inside a word work.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "caption": true, "dd": true, "details": true, "dialog": true,
	"div": true, "dl": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true, "hr": true,
	"li": true, "main": true, "nav": true, "ol": true, "p": true, "pre": true,
	"section": true, "summary": true, "table": true, "tbody": true, "td": true,
	"tfoot": true, "th": true, "thead": true, "tr": true, "ul": true,
	// The document itself is a boundary at both ends.
	"body": true, "html": true,
}

// skipped elements hold content that is not prose.
//
// <head> is deliberately not on the list, and the reason is the counter below. Its
// end tag is omissible, and an element whose end tag the source leaves out is
// reported as ending at the tag that did close it - for a head that is </html>, so
// a skip that started at <head> would still be running at the end of the document
// and the whole body would count as nothing. It does not need to be here anyway:
// everything in a head that holds text - title, script, style - is on the list in
// its own right, and text a document puts directly in the head is text a browser
// moves into the body and shows, which is prose.
var skipped = map[string]bool{
	"script": true, "style": true, "title": true, "template": true,
	"noscript": true, "select": true, "option": true, "iframe": true,
	"noembed": true, "noframes": true,
}

// A Report is what the count came to.
type Report struct {
	// Words is the number of runs of non-space characters, counting a run that
	// spans inline markup as one.
	Words int
	// Characters is the number of non-space characters, which is the other
	// number people ask for.
	Characters int
	// Minutes is the estimate, rounded up, and zero for a document with no
	// words.
	Minutes int
	// WPM is the speed the estimate used.
	WPM int
}

// String renders the report.
func (r Report) String() string {
	if r.Words == 0 {
		return "no words"
	}
	unit := "minutes"
	if r.Minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("%d words, %d characters, about %d %s at %d words a minute",
		r.Words, r.Characters, r.Minutes, unit, r.WPM)
}

// A Counter counts words as the document streams past.
type Counter struct {
	words int
	chars int

	// inWord is the whole state. It survives chunk boundaries and inline
	// elements, and is cleared by blocks, which is the difference between
	// "hello<b>world</b>" and "hello</p><p>world".
	inWord bool

	node      strings.Builder
	skipDepth int
}

// Options are the handlers.
func (c *Counter) Options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			c.flush()
			if blocks[tag] {
				// A block boundary ends whatever word was in progress.
				c.inWord = false
			}
			if !e.CanHaveContent() || !skipped[tag] {
				return nil
			}
			c.skipDepth++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				// Lowered whatever the token is named. The handler runs exactly
				// once for this element, and where the source left its end tag out
				// - </head> and </option> are both omissible, and both tags are on
				// the skip list - the token that closed it belongs to an enclosing
				// element. Comparing the names, which is the right test for a
				// handler writing at the position, would leave the counter above
				// zero and count nothing at all for the rest of the document.
				c.flush()
				c.skipDepth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if c.skipDepth > 0 {
				return nil
			}
			c.node.WriteString(t.Text())
			if t.IsLastInTextNode() {
				c.flush()
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			c.flush()
			return nil
		}),
	}
}

// flush decodes the accumulated node and counts it. Decoding happens here rather
// than per chunk because a character reference is a pattern too, and "&#32;" is a
// separator while "&amp;" is not.
func (c *Counter) flush() {
	if c.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(c.node.String())
	c.node.Reset()
	for _, r := range s {
		if unicode.IsSpace(r) {
			c.inWord = false
			continue
		}
		c.chars++
		if !c.inWord {
			c.words++
			c.inWord = true
		}
	}
}

// Report finishes and returns the count.
func (c *Counter) Report(wpm int) Report {
	if wpm <= 0 {
		wpm = DefaultWPM
	}
	minutes := 0
	if c.words > 0 {
		minutes = int(math.Ceil(float64(c.words) / float64(wpm)))
	}
	return Report{Words: c.words, Characters: c.chars, Minutes: minutes, WPM: wpm}
}

// Count reads a document and reports its length.
func Count(r io.Reader, wpm int) (Report, error) {
	var c Counter
	w, err := lolhtml.NewWriter(io.Discard, c.Options()...)
	if err != nil {
		return Report{}, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return Report{}, err
	}
	if err := w.Close(); err != nil {
		return Report{}, err
	}
	return c.Report(wpm), nil
}

// CountPerChunk is the version that looks right and is not: strings.Fields on
// each chunk, which counts a word once per chunk it is split across. It is here
// so the difference can be measured rather than described.
func CountPerChunk(r io.Reader) (int, error) {
	words := 0
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			words += len(strings.Fields(t.Text()))
			return nil
		}))
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return words, nil
}

func main() {
	wpm := DefaultWPM
	if len(os.Args) > 1 {
		if _, err := fmt.Sscan(os.Args[1], &wpm); err != nil || wpm <= 0 {
			fmt.Fprintln(os.Stderr, "usage: readingtime [words-per-minute]")
			os.Exit(2)
		}
	}
	rep, err := Count(os.Stdin, wpm)
	if err != nil {
		fmt.Fprintln(os.Stderr, "readingtime:", err)
		os.Exit(1)
	}
	fmt.Println(rep)
}

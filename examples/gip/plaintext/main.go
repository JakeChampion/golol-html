// Command plaintext converts a document to text, keeping the block structure as
// blank lines and newlines.
//
// It is the shape of program that this library makes easy and gets wrong in
// three specific ways if written the obvious way. All three are consequences of
// things measured elsewhere in examples/gip, and each one has a test here that
// fails without the fix.
//
// Breaks go on start tags, not end tags. An element whose end tag the source
// leaves out - a list item, a table cell, a paragraph - has no end tag token, so
// its OnEndTag handler runs against the enclosing element's and content
// positioned there lands somewhere else. Emitting the break before each block
// begins needs no end tag at all, and the last break comes from the document
// end.
//
// References are decoded per text node, not per chunk. A chunk never splits a
// character, but it splits everything larger: "&amp;" can arrive as "&am" and
// "p;", and html.UnescapeString on each piece leaves both alone. So the text of
// a node is accumulated first and decoded once.
//
// Whitespace is collapsed with state that survives a chunk boundary. Two spaces
// can arrive in two chunks, so "have I just emitted a space" has to live outside
// the handler, or the output depends on how the input was written.
//
// What is skipped is tracked by depth rather than by selector, because there is
// no selector for "text that is not inside a script". A script, a style, the
// head and a template hold content that is not the document's prose, and each is
// counted in and out.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// blocks are the elements that begin a new line of output. A break before each
// one is enough: the element that follows a block begins its own.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"caption": true, "dd": true, "details": true, "dialog": true, "div": true,
	"dl": true, "dt": true, "fieldset": true, "figcaption": true, "figure": true,
	"footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "header": true, "hgroup": true, "hr": true, "li": true,
	"main": true, "nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"summary": true, "table": true, "tbody": true, "td": true, "tfoot": true,
	"th": true, "thead": true, "tr": true, "ul": true,
}

// paragraphs get a blank line rather than a single newline, so the output reads
// as prose rather than as a list of lines.
var paragraphs = map[string]bool{
	"p": true, "blockquote": true, "pre": true, "figure": true, "table": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// skipped elements hold content that is not the document's text.
var skipped = map[string]bool{
	"script": true, "style": true, "head": true, "template": true,
	"noscript": true, "title": true, "iframe": true, "noembed": true,
	"noframes": true, "select": true, "option": true, "datalist": true,
}

// A Converter turns a document into text.
type Converter struct {
	out strings.Builder

	// node accumulates one text node, because a character reference can be
	// split across chunks and must be decoded whole.
	node strings.Builder

	// skipDepth is how many skipped elements the parser is inside. Text is
	// dropped while it is above zero.
	skipDepth int

	// preDepth is how many <pre> elements the parser is inside. Whitespace is
	// kept verbatim while it is above zero.
	preDepth int

	// pendingBreak is the number of newlines owed before the next text: 1 for a
	// line break, 2 for a paragraph. Held rather than written so that trailing
	// breaks never reach the output.
	pendingBreak int

	// atLineStart and afterSpace are the collapsing state, and they live here
	// rather than in a handler because a chunk boundary must not change the
	// answer.
	atLineStart bool
	afterSpace  bool
}

// NewConverter returns a Converter ready to use.
func NewConverter() *Converter {
	return &Converter{atLineStart: true}
}

// Options are the handlers, in the order they need to run.
func (c *Converter) Options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.element),
		lolhtml.OnDocumentText(c.text),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			c.flushNode()
			return nil
		}),
	}
}

func (c *Converter) element(e *lolhtml.Element) error {
	tag := e.TagName()

	// A text node ends wherever markup begins, and the accumulator has to be
	// flushed there rather than at the next text chunk, or the words either
	// side of an element run together.
	c.flushNode()

	if tag == "br" {
		c.breakLines(1)
		return nil
	}
	if blocks[tag] {
		if paragraphs[tag] {
			c.breakLines(2)
		} else {
			c.breakLines(1)
		}
	}

	// Depth counters. A void or self-closing element has no content and no end
	// tag, so nothing to count.
	if !e.CanHaveContent() {
		return nil
	}
	if skipped[tag] {
		c.skipDepth++
	}
	if tag == "pre" {
		c.preDepth++
	}
	if !skipped[tag] && tag != "pre" {
		return nil
	}
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		// An omitted end tag hands this handler the enclosing element's, and
		// decrementing on that would unwind the wrong element. These elements
		// do not have omissible end tags, so the guard states that rather than
		// working around it.
		if t.Name() != tag {
			return nil
		}
		c.flushNode()
		if skipped[tag] {
			c.skipDepth--
		}
		if tag == "pre" {
			c.preDepth--
		}
		return nil
	})
}

func (c *Converter) text(t *lolhtml.TextChunk) error {
	if c.skipDepth > 0 {
		return nil
	}
	c.node.WriteString(t.Text())
	if t.IsLastInTextNode() {
		c.flushNode()
	}
	return nil
}

// flushNode decodes the accumulated node and appends it, collapsing whitespace
// unless inside a <pre>.
func (c *Converter) flushNode() {
	if c.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(c.node.String())
	c.node.Reset()

	if c.preDepth > 0 {
		c.writeRaw(s)
		return
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			c.afterSpace = true
			continue
		}
		c.writeRune(r)
	}
}

// breakLines asks for n newlines before the next text. The larger request wins,
// so a paragraph inside a div still gets its blank line.
func (c *Converter) breakLines(n int) {
	if c.out.Len() == 0 && c.node.Len() == 0 {
		// Nothing written yet: a leading break would be a leading blank line.
		return
	}
	if n > c.pendingBreak {
		c.pendingBreak = n
	}
}

// writeRune emits one character, paying any breaks and collapsed space owed
// first. Nothing is owed once it has been paid, which is what keeps trailing
// whitespace out of the output.
func (c *Converter) writeRune(r rune) {
	if c.pendingBreak > 0 {
		c.out.WriteString(strings.Repeat("\n", c.pendingBreak))
		c.pendingBreak = 0
		c.afterSpace = false
		c.atLineStart = true
	}
	if c.afterSpace {
		c.afterSpace = false
		if !c.atLineStart {
			c.out.WriteByte(' ')
		}
	}
	c.out.WriteRune(r)
	c.atLineStart = false
}

// writeRaw emits text without collapsing it, which is what <pre> means.
func (c *Converter) writeRaw(s string) {
	for _, r := range s {
		if r == '\n' {
			if c.pendingBreak > 0 {
				c.out.WriteString(strings.Repeat("\n", c.pendingBreak))
				c.pendingBreak = 0
			}
			c.out.WriteByte('\n')
			c.atLineStart = true
			c.afterSpace = false
			continue
		}
		c.writeRune(r)
	}
}

// String returns the text collected so far.
func (c *Converter) String() string { return c.out.String() }

// Convert reads a document and returns its text.
func Convert(r io.Reader) (string, error) {
	c := NewConverter()
	w, err := lolhtml.NewWriter(io.Discard, c.Options()...)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return c.String(), nil
}

func main() {
	text, err := Convert(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plaintext:", err)
		os.Exit(1)
	}
	fmt.Println(text)
}

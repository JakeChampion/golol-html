// Command markdown converts the subset of HTML it understands to Markdown, and
// says what it dropped.
//
// The subset is deliberate: headings, paragraphs, lists, links, emphasis, code,
// blockquotes, horizontal rules and line breaks. Everything else keeps its text
// and loses its markup, and the tags that were dropped are reported, because a
// converter that silently discards <table> is worse than one that says it cannot
// do tables.
//
// The reason this is a different program from examples/gip/plaintext, rather
// than the same one with delimiters, is that Markdown needs to know where an
// element *ends*, and this library will not reliably tell you.
//
// An end-tag handler runs when the element's content ends, and it is handed the
// tag that closed it, which is not always the element's own. Measured, with
// "when" meaning where in the stream of reported content the callback lands:
//
//	<p><em>a</em> b</p>       closes at </em>, own tag, exactly at the end
//	<p><em>a</p>b             closes at </p>, an ancestor's, exactly at the end
//	<ul><li><em>a<li>b</ul>   closes at </ul>, an ancestor's, but "b" was
//	                          reported first: the <em> ended at the second <li>
//	<p><em>a                  never closes at all
//
// The third row is why the callback is not enough on its own: the emphasis ended
// two tokens ago and nothing says so. So this program keeps the stack of open
// elements itself and applies the specification's implied end tags, the same way
// examples/gip/depth does, and closes emphasis when the stack pops rather than
// when a callback arrives. The fourth row is why it also closes everything left
// open at the document end.
//
// The other half of the work is escaping, in the opposite direction from the
// library's. Text that means nothing in HTML means something in Markdown -
// "*emphasis*", "# heading", "1. item", a backslash - so it is escaped on the
// way out, and not escaped inside code, where Markdown does not read it.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Converter turns HTML into Markdown.
type Converter struct {
	out  strings.Builder
	node strings.Builder

	// open is the stack of element names the parser is inside, innermost last.
	// It is this program's own, because the library reports tokens and Markdown
	// needs structure; see the package comment.
	open []string

	// emphasis is the delimiter owed for each open inline element, parallel to
	// the entries in open that have one.
	emphasis []string

	skipDepth int
	codeDepth int
	preDepth  int

	// link is the href of the innermost open <a>, and linkAt is where its text
	// began in the output, so the whole thing can be rewritten as [text](href)
	// when it closes.
	links []link

	pendingBreak int
	atLineStart  bool
	afterSpace   bool

	// listStack is one entry per open list, holding whether it is ordered and
	// how many items it has had.
	listStack []listState

	// dropped counts the tags whose markup was discarded, so the report can
	// name them.
	dropped map[string]int
}

type link struct {
	href string
	at   int
}

type listState struct {
	ordered bool
	items   int
}

// NewConverter returns a Converter ready to use.
func NewConverter() *Converter {
	return &Converter{atLineStart: true, dropped: map[string]int{}}
}

// Supported are the tags this converter has Markdown for. Anything else is
// dropped and counted.
var supported = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "br": true, "hr": true, "ul": true, "ol": true, "li": true,
	"a": true, "em": true, "i": true, "strong": true, "b": true, "code": true,
	"pre": true, "blockquote": true, "del": true, "s": true,
	// Structural elements that carry no Markdown of their own and are not
	// "dropped" in any meaningful sense.
	"html": true, "head": true, "body": true, "div": true, "span": true,
	"section": true, "article": true, "main": true, "header": true,
	"footer": true, "nav": true, "aside": true,
}

// skipped hold content that is not prose.
var skipped = map[string]bool{
	"script": true, "style": true, "title": true, "template": true,
	"noscript": true, "select": true, "option": true, "iframe": true,
}

// inline maps an element to the delimiter that surrounds its text.
var inline = map[string]string{
	"em": "*", "i": "*", "strong": "**", "b": "**", "del": "~~", "s": "~~",
	"code": "`",
}

// Options are the handlers.
func (c *Converter) Options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.element),
		lolhtml.OnDocumentText(c.text),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			c.flushNode()
			// Everything still open ends here, which is the only way to balance
			// a document that never closed its emphasis.
			c.popTo(0)
			return nil
		}),
	}
}

func (c *Converter) element(e *lolhtml.Element) error {
	tag := e.TagName()
	c.flushNode()

	// A start tag can close elements before it opens one, and it closes
	// everything inside them too: the second <li> of <ul><li><em>a<li>b</ul>
	// ends the <em> as well as the item. So this pops through to the element
	// being closed rather than testing the top of the stack, which is the
	// mistake that lets emphasis leak into the next item.
	c.closeImplied(tag)

	c.begin(tag, e)

	if !e.CanHaveContent() {
		return nil
	}
	c.push(tag)
	depth := len(c.open)
	return e.OnEndTag(func(x *lolhtml.EndTag) error {
		c.flushNode()
		// The callback may be firing at an ancestor's end tag, in which case
		// everything from here in is ending too. Popping to the depth this
		// element was pushed at handles both cases with one rule.
		c.popTo(depth - 1)
		return nil
	})
}

// begin writes whatever the start of tag produces.
func (c *Converter) begin(tag string, e *lolhtml.Element) {
	switch {
	case skipped[tag]:
		return
	case !supported[tag]:
		c.dropped[tag]++
		return
	}

	switch tag {
	case "br":
		c.breakLines(1)
	case "hr":
		c.breakLines(2)
		c.writeMarkup("---")
		c.breakLines(2)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.breakLines(2)
		c.writeMarkup(strings.Repeat("#", int(tag[1]-'0')) + " ")
	case "p":
		c.breakLines(2)
	case "blockquote":
		c.breakLines(2)
		c.writeMarkup("> ")
	case "ul", "ol":
		// A list nested inside an item continues that item's line rather than
		// starting a paragraph.
		if len(c.listStack) > 0 {
			c.breakLines(1)
		} else {
			c.breakLines(2)
		}
		c.listStack = append(c.listStack, listState{ordered: tag == "ol"})
	case "li":
		c.breakLines(1)
		c.writeMarkup(c.bullet())
	case "pre":
		c.breakLines(2)
		c.writeMarkup("```")
		c.breakLines(1)
	case "a":
		href, _ := e.Attribute("href")
		c.links = append(c.links, link{href: stdhtml.UnescapeString(href), at: c.out.Len()})
		c.writeMarkup("[")
	}
}

// bullet is the marker for the next list item, and counts it.
func (c *Converter) bullet() string {
	if len(c.listStack) == 0 {
		return "- "
	}
	top := &c.listStack[len(c.listStack)-1]
	top.items++
	indent := strings.Repeat("  ", len(c.listStack)-1)
	if top.ordered {
		return fmt.Sprintf("%s%d. ", indent, top.items)
	}
	return indent + "- "
}

// push records an open element and any delimiter it owes.
func (c *Converter) push(tag string) {
	c.open = append(c.open, tag)
	d := ""
	if !skipped[tag] && supported[tag] {
		d = inline[tag]
	}
	if tag == "code" && c.preDepth > 0 {
		// <pre><code> is the standard HTML spelling of a code block, and the fence
		// is already open. A backtick inside a fenced block is not a delimiter -
		// Markdown renders it as a backtick in the code - so the block came out
		// holding two characters the source did not have. The delimiter is decided
		// here and remembered, so dropping it here drops the closing one too.
		d = ""
	}
	c.emphasis = append(c.emphasis, d)
	if d != "" {
		c.writeMarkup(d)
	}
	if skipped[tag] {
		c.skipDepth++
	}
	if tag == "code" {
		c.codeDepth++
	}
	if tag == "pre" {
		c.preDepth++
	}
}

// pop closes the innermost open element.
func (c *Converter) pop() {
	if len(c.open) == 0 {
		return
	}
	i := len(c.open) - 1
	tag := c.open[i]
	if d := c.emphasis[i]; d != "" {
		c.writeMarkup(d)
	}
	c.open, c.emphasis = c.open[:i], c.emphasis[:i]

	if skipped[tag] {
		c.skipDepth--
	}
	if tag == "code" {
		c.codeDepth--
	}
	if tag == "pre" {
		c.preDepth--
		c.breakLines(1)
		c.writeMarkup("```")
		c.breakLines(2)
	}
	switch tag {
	case "ul", "ol":
		if len(c.listStack) > 0 {
			c.listStack = c.listStack[:len(c.listStack)-1]
		}
		c.breakLines(2)
	case "a":
		if len(c.links) > 0 {
			l := c.links[len(c.links)-1]
			c.links = c.links[:len(c.links)-1]
			c.writeMarkup("](" + l.href + ")")
		}
	case "h1", "h2", "h3", "h4", "h5", "h6", "p", "blockquote":
		c.breakLines(2)
	case "li":
		c.breakLines(1)
	}
}

// popTo closes elements until the stack is n deep.
func (c *Converter) popTo(n int) {
	for len(c.open) > n {
		c.pop()
	}
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

// flushNode decodes the accumulated node - decoding has to happen after
// accumulating, because a character reference can be split across chunks - and
// appends it, escaped for Markdown unless inside code.
func (c *Converter) flushNode() {
	if c.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(c.node.String())
	c.node.Reset()

	if c.preDepth > 0 {
		c.writeVerbatim(s)
		return
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			c.afterSpace = true
			continue
		}
		if c.codeDepth == 0 && needsEscape(r) {
			c.writeRune('\\')
			c.atLineStart = false
		}
		c.writeRune(r)
	}
}

// needsEscape reports whether a character means something to Markdown. Kept
// deliberately wide: an unnecessary backslash is ugly, a missing one changes
// what the document says.
func needsEscape(r rune) bool {
	switch r {
	case '\\', '`', '*', '_', '[', ']', '<', '>', '#', '|', '~':
		return true
	}
	return false
}

func (c *Converter) breakLines(n int) {
	if c.out.Len() == 0 {
		return
	}
	if n > c.pendingBreak {
		c.pendingBreak = n
	}
}

// writeMarkup emits characters this program generated, which are never escaped.
func (c *Converter) writeMarkup(s string) {
	for _, r := range s {
		c.writeRune(r)
	}
}

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

// writeVerbatim emits text without collapsing or escaping, which is what a
// fenced code block holds.
func (c *Converter) writeVerbatim(s string) {
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

// String returns the Markdown collected so far.
func (c *Converter) String() string { return strings.TrimRight(c.out.String(), "\n") }

// Dropped returns the tags whose markup was discarded, most frequent first.
func (c *Converter) Dropped() []string {
	names := make([]string, 0, len(c.dropped))
	for n := range c.dropped {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if c.dropped[names[i]] != c.dropped[names[j]] {
			return c.dropped[names[i]] > c.dropped[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// Convert reads a document and returns its Markdown, plus the tags it dropped.
func Convert(r io.Reader) (string, []string, error) {
	c := NewConverter()
	w, err := lolhtml.NewWriter(io.Discard, c.Options()...)
	if err != nil {
		return "", nil, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return "", nil, err
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return c.String(), c.Dropped(), nil
}

// closeImplied applies the specification's implied end tags for a start tag
// named next: the element it closes, and everything still open inside that.
//
// Each rule has a barrier, because these only reach within their own structure.
// A <li> closes an open list item in the same list and not one in a list two
// levels out, which is what stops a malformed document from unwinding the whole
// stack.
func (c *Converter) closeImplied(next string) {
	switch next {
	case "li":
		c.popThrough(set("li"), set("ul", "ol", "menu"))
	case "dd", "dt":
		c.popThrough(set("dd", "dt"), set("dl"))
	case "td", "th":
		c.popThrough(set("td", "th"), set("tr", "table"))
	case "tr":
		c.popThrough(set("tr"), set("table", "tbody", "thead", "tfoot"))
	case "option":
		c.popThrough(set("option"), set("select", "datalist"))
	case "optgroup":
		c.popThrough(set("option", "optgroup"), set("select"))
	}
	// A paragraph is closed by any of the block elements that cannot be inside
	// one. Nothing above a <p> on the stack is a block, so there is no barrier
	// to respect.
	if closesAParagraph[next] {
		c.popThrough(set("p"), nil)
	}
}

// popThrough closes elements down to and including the innermost one named in
// want, if there is one above every barrier. It closes nothing if there is not,
// which is what makes a stray end tag harmless.
func (c *Converter) popThrough(want, barrier map[string]bool) {
	for i := len(c.open) - 1; i >= 0; i-- {
		if want[c.open[i]] {
			c.popTo(i)
			return
		}
		if barrier[c.open[i]] {
			return
		}
	}
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var closesAParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"details": true, "div": true, "dl": true, "dt": true, "dd": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "header": true, "hgroup": true, "hr": true, "li": true,
	"main": true, "menu": true, "nav": true, "ol": true, "p": true, "pre": true,
	"search": true, "section": true, "summary": true, "table": true, "ul": true,
}

func main() {
	md, dropped, err := Convert(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "markdown:", err)
		os.Exit(1)
	}
	fmt.Println(md)
	if len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "markdown: kept the text and dropped the markup of: %s\n",
			strings.Join(dropped, ", "))
	}
}

// Command keywords counts word frequencies in a document's own content, leaving
// out the parts every page on the site shares.
//
// Excluding a region is the whole difficulty. There is no selector for "text
// that is not inside a nav", so the program tracks depth: it counts into an
// excluded region on the start tag and out again on the end tag, and drops text
// while the count is above zero. That is the same shape as skipping scripts, with
// one difference that matters - the regions here are chosen by attribute as well
// as by tag name, and an attribute value has to be compared in Go rather than in
// the selector.
//
//	[role=navigation]     misses role="Navigation"
//	[role]  then fold     matches both
//
// Selectors match attribute values case-insensitively only for the attributes on
// the HTML specification's list, and role is not one of them. Neither is class,
// so the same applies to matching a class by name.
//
// Counting a word needs more state than counting boundaries. A word can be split
// across chunks and across inline markup, so the characters are accumulated until
// a separator arrives - which is another reason the exclusion has to be tracked
// rather than filtered afterwards: a word that begins outside a nav and continues
// inside it belongs to neither, and the boundary between them is where it ends.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// boilerplate tags are the regions a page shares with every other page.
var boilerplate = map[string]bool{
	"nav": true, "header": true, "footer": true, "aside": true,
}

// boilerplateRoles are the ARIA landmarks that mean the same thing when the tags
// do not say it. Compared folded, because role is not an attribute selectors
// match case-insensitively.
var boilerplateRoles = map[string]bool{
	"navigation": true, "banner": true, "contentinfo": true, "search": true,
	"complementary": true,
}

// skipped hold content that is not prose at all.
//
// <head> is deliberately not on the list, because a depth counter cannot unwind it.
// Its end tag is omissible, and an element whose end tag the source leaves out ends
// at the tag that did close it - for a head that is </html>, if the document spells
// one at all - so a skip that started at <head> would still be running over the
// whole body. It is not needed here either: everything in a head that holds text -
// title, script, style - is on the list in its own right, and text a document puts
// directly in the head is text a browser moves into the body and shows.
var skipped = map[string]bool{
	"script": true, "style": true, "title": true, "template": true,
	"noscript": true, "select": true, "option": true, "iframe": true,
}

// blocks end a word.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "caption": true, "dd": true, "div": true, "dl": true, "dt": true,
	"figcaption": true, "figure": true, "footer": true, "form": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "td": true, "th": true,
	"tr": true, "ul": true, "body": true, "html": true,
}

// stopwords are left out of the ranking. A short list on purpose: a long one
// starts making editorial decisions about the document.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "for": true, "from": true, "had": true,
	"has": true, "have": true, "he": true, "her": true, "his": true, "i": true,
	"in": true, "is": true, "it": true, "its": true, "not": true, "of": true,
	"on": true, "or": true, "she": true, "that": true, "the": true, "they": true,
	"this": true, "to": true, "was": true, "were": true, "which": true,
	"with": true, "you": true,
}

// A Count is one word and how often it appeared.
type Count struct {
	Word string
	N    int
}

// A Report is the ranking plus what was left out, because a keyword report that
// does not say what it excluded cannot be checked.
type Report struct {
	// Top are the most frequent words, most first, ties broken alphabetically.
	Top []Count
	// Words is how many words were counted, Distinct how many different ones.
	Words, Distinct int
	// Excluded is how many characters fell inside a boilerplate region, and
	// Regions how many such regions were entered.
	Excluded, Regions int
	// Stopped is how many words were dropped as stopwords.
	Stopped int
}

// A Counter accumulates the ranking.
type Counter struct {
	counts map[string]int
	words  int

	// word is the characters of the word in progress, which survives chunk and
	// inline-element boundaries.
	word strings.Builder
	node strings.Builder

	excludeDepth int
	skipDepth    int

	excluded, regions, stopped int
}

// NewCounter returns a Counter ready to use.
func NewCounter() *Counter { return &Counter{counts: map[string]int{}} }

// Options are the handlers.
func (c *Counter) Options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.element),
		lolhtml.OnDocumentText(c.text),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			c.flushNode()
			c.endWord()
			return nil
		}),
	}
}

func (c *Counter) element(e *lolhtml.Element) error {
	tag := e.TagName()
	c.flushNode()
	if blocks[tag] {
		c.endWord()
	}

	exclude := boilerplate[tag] || c.hasBoilerplateRole(e)
	skip := skipped[tag]
	if !exclude && !skip {
		return nil
	}
	// A void element cannot contain anything, so there is no region to count
	// into - and nothing to count out of either.
	if !e.CanHaveContent() {
		return nil
	}

	if exclude {
		c.excludeDepth++
		c.regions++
	}
	if skip {
		c.skipDepth++
	}
	c.endWord()

	return e.OnEndTag(func(*lolhtml.EndTag) error {
		// Lowered whatever the token is named, and deliberately so. The name
		// guard is the right test for a handler writing at an end tag's
		// position - it asks "is this position mine" - and the wrong one for a
		// counter, which asks "has this element ended". The handler runs exactly
		// once for this element, and where the source left the end tag out
		// - </option> is omissible, and option is on the skip list - the token
		// that closed it belongs to an enclosing element, so a name comparison
		// would leave the counter raised and drop every word after it.
		c.flushNode()
		c.endWord()
		if exclude {
			c.excludeDepth--
		}
		if skip {
			c.skipDepth--
		}
		return nil
	})
}

// hasBoilerplateRole compares the role attribute in Go, because selectors match
// attribute values case-insensitively only for the attributes on the HTML
// specification's list and role is not one of them.
func (c *Counter) hasBoilerplateRole(e *lolhtml.Element) bool {
	v, ok := e.Attribute("role")
	if !ok {
		return false
	}
	for _, token := range strings.Fields(v) {
		if boilerplateRoles[strings.ToLower(token)] {
			return true
		}
	}
	return false
}

func (c *Counter) text(t *lolhtml.TextChunk) error {
	if c.skipDepth > 0 {
		return nil
	}
	c.node.WriteString(t.Text())
	if t.IsLastInTextNode() {
		c.flushNode()
	}
	return nil
}

// flushNode decodes the node - after accumulating it, because a character
// reference can be split across chunks - and feeds it in character by character.
func (c *Counter) flushNode() {
	if c.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(c.node.String())
	c.node.Reset()
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) && r != '\'' && r != '-' {
			c.endWord()
			continue
		}
		if c.excludeDepth > 0 {
			c.excluded++
			continue
		}
		c.word.WriteRune(unicode.ToLower(r))
	}
}

// endWord counts whatever word was in progress.
func (c *Counter) endWord() {
	if c.word.Len() == 0 {
		return
	}
	w := strings.Trim(c.word.String(), "'-")
	c.word.Reset()
	if w == "" {
		return
	}
	c.words++
	if stopwords[w] {
		c.stopped++
		return
	}
	c.counts[w]++
}

// Report returns the top n words and the accounting.
func (c *Counter) Report(n int) Report {
	rep := Report{
		Words:    c.words,
		Distinct: len(c.counts),
		Excluded: c.excluded,
		Regions:  c.regions,
		Stopped:  c.stopped,
	}
	all := make([]Count, 0, len(c.counts))
	for w, k := range c.counts {
		all = append(all, Count{w, k})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].N != all[j].N {
			return all[i].N > all[j].N
		}
		return all[i].Word < all[j].Word
	})
	if n > 0 && n < len(all) {
		all = all[:n]
	}
	rep.Top = all
	return rep
}

// Analyse reads a document and returns its keyword report.
func Analyse(r io.Reader, top int) (Report, error) {
	c := NewCounter()
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
	return c.Report(top), nil
}

// String renders the report.
func (r Report) String() string {
	var b strings.Builder
	for _, c := range r.Top {
		fmt.Fprintf(&b, "%6d  %s\n", c.N, c.Word)
	}
	fmt.Fprintf(&b, "%d words, %d distinct, %d stopwords dropped\n",
		r.Words, r.Distinct, r.Stopped)
	if r.Regions > 0 {
		fmt.Fprintf(&b, "%d boilerplate regions excluded, %d characters\n",
			r.Regions, r.Excluded)
	}
	return b.String()
}

func main() {
	top := 10
	if len(os.Args) > 1 {
		if _, err := fmt.Sscan(os.Args[1], &top); err != nil || top < 1 {
			fmt.Fprintln(os.Stderr, "usage: keywords [how-many]")
			os.Exit(2)
		}
	}
	rep, err := Analyse(os.Stdin, top)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keywords:", err)
		os.Exit(1)
	}
	fmt.Print(rep)
}

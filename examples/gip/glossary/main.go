// Command glossary reads the definition lists in a document and links every
// mention of their terms in the body.
//
// It cannot be done in one pass, and the reason is the ordinary one: a term is
// defined by a <dl> that may appear anywhere - usually at the end - and the
// mentions to be linked are everywhere else. A one-pass rewrite would link the
// mentions that happen to come after the definition list and silently leave the
// ones before it, which is worse than not doing it at all because the result
// looks like it worked.
//
// So: read once to collect the terms, then rewrite. The cost of that is a
// doubling, and it is a doubling of everything rather than of a fixed overhead -
// the second pass re-parses the document and runs every handler again. The
// program skips the second pass entirely when the first found no terms, which is
// the common case for most pages and the only way to make the choice cheap.
//
// The linking itself is three rules, each of which is a depth counter rather than
// a selector, because a selector cannot say "not inside".
//
// Not inside an existing link, or the result is a link in a link, which a parser
// unnests into something nobody wrote.
//
// Not inside a <dl>, or every term links to itself.
//
// Not inside code, a heading, or anything where a link would be wrong: kbd, samp,
// var, pre, script, style, textarea, title.
//
// And not inside a raw-text element, which is a separate rule from the taste one
// above and a harder one. The mentions are linked by replacing a whole text node
// with lolhtml.HTML, and the library does not raw-text check a TextChunk
// insertion - it has no way to know which element the chunk came from. So a <a
// href> written into an <xmp> or a <noscript> is not a link, it is the literal
// text of that element as far as a parser is concerned, and a "</xmp>" arriving
// from anywhere would end it. A hand-written list of names is the wrong guard
// for that, because it falls behind the parser silently; lolhtml.IsRawText is
// the list, measured against the parser.
//
// And the text has to be matched across chunk boundaries, so the mentions are
// found in the accumulated text of a node rather than per chunk - a term split
// across two chunks is not a term to a per-chunk search, and where the chunks
// fall is not something a caller controls.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Term is one glossary entry.
type Term struct {
	// Text is the term as the <dt> spelled it.
	Text string
	// Anchor is the fragment to link to.
	Anchor string
}

// Glossary is the terms found, keyed by their lower-cased text.
type Glossary map[string]Term

// noLink are the elements inside which a link would be wrong. It is a taste
// judgement, not a correctness one - the correctness half is lolhtml.IsRawText,
// applied alongside this map below. The two overlap on script, style, textarea
// and title, which are here because a link in them reads wrong and there because
// their content is not markup at all.
var noLink = map[string]bool{
	"a": true, "dl": true, "code": true, "kbd": true, "samp": true, "var": true,
	"pre": true, "script": true, "style": true, "textarea": true, "title": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"button": true, "option": true, "select": true, "template": true,
}

// Collect is the first pass: the terms defined by every <dt> in the document.
func Collect(r io.Reader) (Glossary, error) {
	g := Glossary{}
	var node, term strings.Builder
	inTerm := false

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("dt", func(e *lolhtml.Element) error {
			flush(&node, &term, inTerm)
			if !e.CanHaveContent() {
				return nil
			}
			inTerm = true
			term.Reset()
			// A <dt> is usually written without an end tag, so the next <dt> or
			// <dd> is what ends it - and this handler would fire at the </dl>,
			// too late. The end of the term is taken at the next element
			// instead, below.
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if tag == "dt" {
				return nil
			}
			flush(&node, &term, inTerm)
			if inTerm && endsATerm(tag) {
				add(g, term.String())
				term.Reset()
				inTerm = false
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			node.WriteString(t.Text())
			if t.IsLastInTextNode() {
				flush(&node, &term, inTerm)
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			flush(&node, &term, inTerm)
			if inTerm {
				add(g, term.String())
			}
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return g, nil
}

// endsATerm reports whether a start tag ends an open <dt>. A <dt> is closed by
// the next <dt> or <dd>, or by the end of the list; anything inline inside it is
// part of the term.
func endsATerm(tag string) bool {
	switch tag {
	case "dd", "dl", "p", "div", "section", "article", "table", "ul", "ol":
		return true
	}
	return false
}

// flush moves the accumulated text node into the term being read, decoding it
// once it is whole - a character reference can be split across chunks.
func flush(node, term *strings.Builder, inTerm bool) {
	if node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(node.String())
	node.Reset()
	if inTerm {
		term.WriteString(s)
	}
}

func add(g Glossary, raw string) {
	text := collapse(raw)
	if text == "" {
		return
	}
	key := strings.ToLower(text)
	if _, ok := g[key]; ok {
		return
	}
	g[key] = Term{Text: text, Anchor: anchor(text)}
}

// anchor turns a term into a fragment: lower-cased, spaces to hyphens, and
// nothing that would need escaping in an attribute or a URL.
func anchor(text string) string {
	var b strings.Builder
	b.WriteString("term-")
	dash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// A Result says what the two passes did.
type Result struct {
	// Terms is the glossary, keyed by lower-cased term.
	Terms Glossary
	// Linked is how many mentions were linked.
	Linked int
	// SecondPass says whether one was needed at all.
	SecondPass bool
}

// Rewrite reads src, collects the glossary, and writes src with the mentions
// linked. It buffers src, because a second pass has to read it again.
func Rewrite(dst io.Writer, src io.Reader) (Result, error) {
	buf, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}

	terms, err := Collect(strings.NewReader(string(buf)))
	if err != nil {
		return Result{}, err
	}
	res := Result{Terms: terms}
	if len(terms) == 0 {
		// Nothing to link, so there is no second pass to pay for.
		if _, err := dst.Write(buf); err != nil {
			return res, err
		}
		return res, nil
	}
	res.SecondPass = true

	// Longest first, so "streaming rewrite" wins over "streaming".
	keys := make([]string, 0, len(terms))
	for k := range terms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	var depth int
	var node strings.Builder

	// The whole node is written back on its last chunk, linked or not. Not
	// "only when something changed": the earlier chunks have already been
	// removed to accumulate the node, so returning without writing loses the
	// text. That is how this program lost "See also " the first time.
	//
	// Writing source back as HTML is a round trip through nothing, so a node
	// with no term in it comes out byte-identical.
	linkText := func(c *lolhtml.TextChunk, s string) error {
		out, n := link(s, keys, terms)
		res.Linked += n
		return c.Replace(out, lolhtml.HTML)
	}

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			// IsRawText as well as the map: inserting HTML into one of those
			// ten writes text, not markup, and the library cannot check a
			// TextChunk insertion for us. Six of the ten - iframe, noembed,
			// noframes, noscript, xmp, plaintext - are not in noLink and never
			// would be, which is the point of asking rather than listing.
			if !noLink[tag] && !lolhtml.IsRawText(tag) {
				return nil
			}
			if !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				// No name guard here, deliberately. The usual guard - ignore an
				// end tag whose name is not this element's - is for a handler
				// that writes at the end tag's position, because a foreign one
				// is somewhere else in the document. This handler writes
				// nothing; it only has to undo its own increment exactly once,
				// and OnEndTag runs at most once per element.
				//
				// Guarding on the name here is what breaks the counter, because
				// not every one of these elements has a mandatory end tag:
				// </option> is omissible, so <option>x<option>y</select> closes
				// both options at the </select> and reports "select" for both.
				// Misnesting does the same to an element that does have one:
				// <p><code>x</p> closes the code at the </p>. Either way the
				// guarded decrement never runs, depth stays above zero, and the
				// text handler below silently stops linking for the rest of the
				// document.
				//
				// The remaining inexactness is conservative: an element closed
				// by a sibling's start tag is reported at a later end tag (see
				// Element.OnEndTag), so a few nodes stay excluded that need not
				// be. Missing a link is the safe direction; a link inside a
				// <code> is not.
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if depth > 0 {
				return nil
			}
			// The whole node, because a term can be split across chunks. The
			// replacement goes on the last chunk, and the earlier ones are
			// removed, so the text is written once.
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			s := node.String()
			node.Reset()
			return linkText(t, s)
		}),
	)
	if err != nil {
		return res, err
	}
	if _, err := w.Write(buf); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}
	return res, nil
}

// link finds the terms in the source text of one node and returns it with links
// inserted, plus how many it inserted.
//
// The text is source, so the search is over source and what is emitted is source:
// nothing is decoded and re-encoded, which is what keeps a character reference
// from being escaped twice.
func link(source string, keys []string, terms Glossary) (string, int) {
	lower := strings.ToLower(source)
	var b strings.Builder
	n := 0
	for i := 0; i < len(source); {
		matched := false
		for _, k := range keys {
			if !strings.HasPrefix(lower[i:], k) {
				continue
			}
			if !wordBoundary(source, i, i+len(k)) {
				continue
			}
			t := terms[k]
			b.WriteString(`<a href="#` + t.Anchor + `" class="glossary">`)
			b.WriteString(source[i : i+len(k)])
			b.WriteString(`</a>`)
			i += len(k)
			n++
			matched = true
			break
		}
		if !matched {
			b.WriteByte(source[i])
			i++
		}
	}
	return b.String(), n
}

// wordBoundary reports whether source[from:to] is a whole word, so that "cat"
// does not match inside "catalogue".
//
// Decoded rather than indexed: source[from-1] is a byte, and the last byte of a
// multi-byte character is not that character - reading one as a rune makes some
// of them look like letters, which silently stops a term next to any non-ASCII
// character from matching.
func wordBoundary(source string, from, to int) bool {
	if from > 0 {
		if r, _ := utf8.DecodeLastRuneInString(source[:from]); isWord(r) {
			return false
		}
	}
	if to < len(source) {
		if r, _ := utf8.DecodeRuneInString(source[to:]); isWord(r) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func main() {
	res, err := Rewrite(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "glossary:", err)
		os.Exit(1)
	}
	pass := "one pass"
	if res.SecondPass {
		pass = "two passes"
	}
	fmt.Fprintf(os.Stderr, "glossary: %d terms, %d mentions linked, %s\n",
		len(res.Terms), res.Linked, pass)
}

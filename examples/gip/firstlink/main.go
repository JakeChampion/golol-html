// Command firstlink links each glossary term once, the first time it is
// mentioned, and leaves alone any term the page already links.
//
// "Once" and "already linked" are two different problems and only one of them
// can be solved as the document streams.
//
// Once is easy: link the first mention and remember the term. The state is a set,
// the decision is local, and it needs nothing the rewriter has not already
// reported.
//
// Already linked is not. If the page links "streaming" to its own glossary
// somewhere, adding a second link is worse than adding none - and that link may
// be anywhere, including after the mention this program would otherwise take. A
// one-pass version links the mention and then meets the existing link, with the
// insertion already emitted and nothing to take back. So the first pass collects
// three things: the terms, the terms the page already links, and nothing else.
//
// The rest is the same discipline as examples/gip/glossary: the exclusions are
// depth counters, because no selector says "not inside an existing link"; text is
// matched over the accumulated node, because a term can be split across chunks;
// and the node is written back whether or not anything changed, because
// accumulating it means removing the chunks it arrived in.
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

// noLink are the elements inside which a link must not be inserted.
var noLink = map[string]bool{
	"a": true, "dl": true, "code": true, "kbd": true, "samp": true, "var": true,
	"pre": true, "script": true, "style": true, "textarea": true, "title": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"button": true, "option": true, "select": true, "template": true,
}

// A Term is one glossary entry and what happened to it.
type Term struct {
	// Text is the term as its <dt> spelled it.
	Text string
	// Anchor is the fragment a link points at.
	Anchor string
	// AlreadyLinked is set when the page links this term itself, anywhere.
	AlreadyLinked bool
	// Linked is set when this program linked a mention of it.
	Linked bool
}

// A Result says what the two passes decided.
type Result struct {
	// Terms are the glossary entries, keyed by lower-cased text.
	Terms map[string]*Term
	// Linked, Skipped and Absent count the three outcomes, which together
	// account for every term.
	Linked, Skipped, Absent int
}

// Summary lists the terms in each outcome, for a report a person reads.
func (r Result) Summary() string {
	var linked, skipped, absent []string
	for _, t := range r.Terms {
		switch {
		case t.Linked:
			linked = append(linked, t.Text)
		case t.AlreadyLinked:
			skipped = append(skipped, t.Text)
		default:
			absent = append(absent, t.Text)
		}
	}
	sort.Strings(linked)
	sort.Strings(skipped)
	sort.Strings(absent)
	var b strings.Builder
	fmt.Fprintf(&b, "%d terms: %d linked, %d already linked by the page, %d not mentioned\n",
		len(r.Terms), len(linked), len(skipped), len(absent))
	for label, list := range map[string][]string{
		"linked":         linked,
		"already linked": skipped,
		"not mentioned":  absent,
	} {
		if len(list) > 0 {
			fmt.Fprintf(&b, "  %-14s %s\n", label+":", strings.Join(list, ", "))
		}
	}
	return b.String()
}

// survey is the first pass: the glossary, and which of its terms the page links
// already.
func survey(doc string) (map[string]*Term, error) {
	terms := map[string]*Term{}

	var node, term, linkText strings.Builder
	inTerm, linkDepth := false, 0
	var linked []string

	flush := func() {
		if node.Len() == 0 {
			return
		}
		s := stdhtml.UnescapeString(node.String())
		node.Reset()
		if inTerm {
			term.WriteString(s)
		}
		if linkDepth > 0 {
			linkText.WriteString(s)
		}
	}
	endTerm := func() {
		if !inTerm {
			return
		}
		inTerm = false
		text := collapse(term.String())
		term.Reset()
		if text == "" {
			return
		}
		key := strings.ToLower(text)
		if _, ok := terms[key]; !ok {
			terms[key] = &Term{Text: text, Anchor: anchor(text)}
		}
	}

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			flush()

			switch tag {
			case "dt":
				endTerm()
				if e.CanHaveContent() {
					inTerm = true
					term.Reset()
				}
				return nil
			case "dd", "dl", "p", "div", "section", "article", "table", "ul", "ol":
				endTerm()
			}

			// The text of every link, so a term the page links can be found -
			// wherever the link is, before or after the mention this program
			// would otherwise take.
			if tag == "a" && e.CanHaveContent() {
				linkDepth++
				if linkDepth == 1 {
					linkText.Reset()
				}
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					flush()
					// Lowered whatever the token is named. The handler runs once
					// for this element, and where the source left an end tag out
					// it runs against an enclosing element's tag instead - so a
					// name guard here, which is the right test for a handler
					// writing at the position, would leave the counter raised for
					// the rest of the document.
					linkDepth--
					if linkDepth == 0 {
						linked = append(linked, collapse(linkText.String()))
					}
					return nil
				})
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			node.WriteString(t.Text())
			if t.IsLastInTextNode() {
				flush()
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			flush()
			endTerm()
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, doc); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	// A link whose text is a term, or contains one as a whole word, counts as
	// the page having linked it.
	for _, text := range linked {
		lower := strings.ToLower(text)
		for key, term := range terms {
			if containsWord(lower, key) {
				term.AlreadyLinked = true
			}
		}
	}
	return terms, nil
}

// Rewrite reads src, surveys it, and writes it with the first mention of each
// unlinked term linked.
func Rewrite(dst io.Writer, src io.Reader) (Result, error) {
	buf, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	doc := string(buf)

	terms, err := survey(doc)
	if err != nil {
		return Result{}, err
	}
	res := Result{Terms: terms}

	// Only the terms that need linking, longest first so a longer term wins.
	var keys []string
	for k, t := range terms {
		if !t.AlreadyLinked {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	if len(keys) == 0 {
		// Nothing to do, so nothing to pay for.
		if _, err := dst.Write(buf); err != nil {
			return res, err
		}
		res.count()
		return res, nil
	}

	depth := 0
	var node strings.Builder

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if !noLink[tag] || !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				// Lowered whatever the token is named: see the same counter in
				// survey. <option> is in this list and its end tag is omissible,
				// so a name guard sticks the counter above zero and every later
				// text node in the document goes unexamined - which here also
				// makes the report say a term is not mentioned when it is.
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if depth > 0 {
				return nil
			}
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				// Removed so the node can be written back once, whole.
				t.Remove()
				return nil
			}
			s := node.String()
			node.Reset()
			// Written back either way: the chunks it arrived in are gone.
			return t.Replace(linkFirst(s, keys, terms), lolhtml.HTML)
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
	res.count()
	return res, nil
}

func (r *Result) count() {
	r.Linked, r.Skipped, r.Absent = 0, 0, 0
	for _, t := range r.Terms {
		switch {
		case t.Linked:
			r.Linked++
		case t.AlreadyLinked:
			r.Skipped++
		default:
			r.Absent++
		}
	}
}

// linkFirst links the first mention of each term that has not been linked yet,
// and marks it. The text is source in and source out, so nothing is escaped
// twice.
func linkFirst(source string, keys []string, terms map[string]*Term) string {
	lower := strings.ToLower(source)
	var b strings.Builder
	for i := 0; i < len(source); {
		matched := false
		for _, k := range keys {
			t := terms[k]
			if t.Linked {
				continue
			}
			if !strings.HasPrefix(lower[i:], k) || !wordBoundary(source, i, i+len(k)) {
				continue
			}
			b.WriteString(`<a href="#` + t.Anchor + `" class="glossary">`)
			b.WriteString(source[i : i+len(k)])
			b.WriteString(`</a>`)
			i += len(k)
			t.Linked = true
			matched = true
			break
		}
		if !matched {
			b.WriteByte(source[i])
			i++
		}
	}
	return b.String()
}

// containsWord reports whether text contains term as a whole word.
func containsWord(text, term string) bool {
	for i := 0; i+len(term) <= len(text); i++ {
		if text[i:i+len(term)] == term && wordBoundary(text, i, i+len(term)) {
			return true
		}
	}
	return false
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
		fmt.Fprintln(os.Stderr, "firstlink:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, res.Summary())
}

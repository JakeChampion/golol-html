// Command highlight marks search terms in a document's text without ever
// injecting markup.
//
// Never injecting markup is the constraint, and it decides the design: every
// insertion goes through ContentType Text, so nothing this program writes can
// become a tag. A highlighter that wrapped matches in <mark> would be a different
// program with a different risk, and the point of this one is that the risk is
// absent by construction rather than by care.
//
// So a match is marked with characters instead - "«term»" by default - and the
// guarantee is checkable: the sequence of tags in the output is the sequence that
// went in. The tests check exactly that, over every document in a corpus, rather
// than checking that the output looks right.
//
// The guarantee is about the markup and not about the tree. Inserting text can
// change the tree a browser builds, without adding any tag, when a formatting
// element is misnested across a block boundary - see the package documentation on
// what Text guarantees. This program cannot avoid that and does not claim to; what
// it claims is that it writes no markup, which is the part a caller can rely on
// when the input is untrusted.
//
// The rest is the discipline the other text programs share. Matching happens over
// the accumulated text of a node, because a term can be split across chunks.
// Raw-text elements are skipped, because Text escaping inside a script produces
// "&lt;" rather than "<" and corrupts the script rather than protecting it.
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

// skipped elements hold content that is not prose, and where escaping would
// corrupt rather than protect.
var skipped = map[string]bool{
	"script": true, "style": true, "title": true, "textarea": true,
	"template": true, "noscript": true, "iframe": true, "xmp": true,
	"noembed": true, "noframes": true, "option": true, "select": true,
}

// Marks are the characters put around a match. They must not be markup, which is
// the whole point, and the defaults are not.
type Marks struct {
	Open, Close string
}

// DefaultMarks are guillemets, which need no escaping and are visible in plain
// text.
var DefaultMarks = Marks{Open: "«", Close: "»"}

// A Result says what was marked.
type Result struct {
	// Counts is how many times each term was marked, keyed by the term as
	// given.
	Counts map[string]int
	// Total is the number of marks inserted.
	Total int
}

// Highlight copies src to dst with every occurrence of each term marked.
func Highlight(dst io.Writer, src io.Reader, terms []string, marks Marks) (Result, error) {
	res := Result{Counts: map[string]int{}}
	if marks.Open == "" && marks.Close == "" {
		marks = DefaultMarks
	}

	// Longest first, so a longer term wins over a prefix of it.
	needles := make([]string, 0, len(terms))
	for _, term := range terms {
		if t := strings.TrimSpace(term); t != "" {
			needles = append(needles, t)
		}
	}
	sort.Slice(needles, func(i, j int) bool {
		if len(needles[i]) != len(needles[j]) {
			return len(needles[i]) > len(needles[j])
		}
		return needles[i] < needles[j]
	})
	lowered := make([]string, len(needles))
	for i, n := range needles {
		lowered[i] = strings.ToLower(n)
	}

	depth := 0
	var node strings.Builder

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if !skipped[tag] || !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != tag {
					return nil
				}
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if depth > 0 {
				return nil
			}
			// The whole node: a term can be split across chunks, and a
			// character reference can be too.
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			source := node.String()
			node.Reset()

			// Decoded before matching, so "caf&eacute;" matches "café", and
			// written back as Text, which escapes it again. Source in, text
			// out: the one round trip that is right on the first pass.
			text := stdhtml.UnescapeString(source)
			marked, counts := mark(text, needles, lowered, marks)
			for term, n := range counts {
				res.Counts[term] += n
				res.Total += n
			}
			// Written back either way, because the chunks it arrived in have
			// been removed.
			return t.Replace(marked, lolhtml.Text)
		}),
	)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}
	return res, nil
}

// mark surrounds each occurrence of a term, matching without regard to case and
// only on whole words.
func mark(text string, needles, lowered []string, marks Marks) (string, map[string]int) {
	lower := strings.ToLower(text)
	counts := map[string]int{}
	var b strings.Builder
	for i := 0; i < len(text); {
		matched := false
		for k, needle := range lowered {
			if !strings.HasPrefix(lower[i:], needle) {
				continue
			}
			if !wholeWord(text, i, i+len(needle)) {
				continue
			}
			b.WriteString(marks.Open)
			b.WriteString(text[i : i+len(needle)])
			b.WriteString(marks.Close)
			counts[needles[k]]++
			i += len(needle)
			matched = true
			break
		}
		if !matched {
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String(), counts
}

// wholeWord reports whether text[from:to] is bounded by non-word characters.
//
// Decoded rather than indexed: text[from-1] is a byte, and the last byte of a
// multi-byte character is not that character. Reading it as a rune made
// "\u00c2" - the first byte of "\u00bb" - look like a letter, so a term next to
// any non-ASCII character was not matched.
func wholeWord(text string, from, to int) bool {
	if from > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:from]); isWord(r) {
			return false
		}
	}
	if to < len(text) {
		if r, _ := utf8.DecodeRuneInString(text[to:]); isWord(r) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: highlight term [term ...]")
		os.Exit(2)
	}
	res, err := Highlight(os.Stdout, os.Stdin, os.Args[1:], DefaultMarks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "highlight:", err)
		os.Exit(1)
	}
	terms := make([]string, 0, len(res.Counts))
	for term := range res.Counts {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	fmt.Fprintf(os.Stderr, "highlight: %d marks\n", res.Total)
	for _, term := range terms {
		fmt.Fprintf(os.Stderr, "  %-20s %d\n", term, res.Counts[term])
	}
}

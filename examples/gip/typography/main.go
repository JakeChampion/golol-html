// Command typography applies typographic quotes and dashes to a document's prose,
// and leaves code alone.
//
// Quotes are the interesting part, because a pair of them is a pattern that spans
// more than the text node it starts in:
//
//	<p>"a <b>b</b> c"</p>
//
// The opening quote is in one text node and the closing quote is in another, with
// an element between them. So "am I inside a quotation" is state that has to
// survive a text node boundary and an inline element, and be cleared by a block -
// which is the same shape as the word state in examples/gip/readingtime, one level
// up: there a word spans markup, here a quotation does.
//
// The apostrophe is the other half. A single quote is a closing quote after a
// letter and an apostrophe inside a word, and the two are the same character, so
// the decision needs the character on each side rather than a mode. "don't" keeps
// its apostrophe and "'quoted'" gets a pair, from one rule.
//
// Everything the program writes is a character, inserted as Text, so it cannot
// add markup - which the tests check by comparing the tag sequence rather than by
// reading the output.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// The characters this program writes.
const (
	openDouble  = "“" // “
	closeDouble = "”" // ”
	openSingle  = "‘" // ‘
	closeSingle = "’" // ’
	enDash      = "–" // –
	emDash      = "—" // —
	ellipsis    = "…" // …
)

// verbatim are the elements whose text is not prose. Applying typography to a
// code sample changes what it means, which is worse than leaving it plain.
var verbatim = map[string]bool{
	"code": true, "pre": true, "kbd": true, "samp": true, "var": true,
	"script": true, "style": true, "textarea": true, "title": true,
	"template": true, "noscript": true, "iframe": true, "xmp": true,
	"noembed": true, "noframes": true, "option": true, "select": true,
	"tt": true,
}

// blocks end a quotation. An unclosed quote in one paragraph must not reach into
// the next.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "caption": true, "dd": true, "div": true, "dl": true, "dt": true,
	"figcaption": true, "figure": true, "footer": true, "form": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "td": true, "th": true,
	"tr": true, "ul": true, "body": true, "html": true,
}

// A Result counts what was changed.
type Result struct {
	Quotes, Dashes, Ellipses int
	// Unclosed counts the quotations still open when a block ended, which is
	// worth reporting: a page full of them usually means the text uses the
	// character for something else, like inches.
	Unclosed int
}

// Total is every substitution.
func (r Result) Total() int { return r.Quotes + r.Dashes + r.Ellipses }

func (r Result) String() string {
	return fmt.Sprintf("%d substitutions: %d quotes, %d dashes, %d ellipses; %d quotations left open",
		r.Total(), r.Quotes, r.Dashes, r.Ellipses, r.Unclosed)
}

type converter struct {
	res Result

	// inDouble and inSingle are the pairing state, and they live here rather
	// than in a handler because a quotation spans text nodes and inline
	// elements.
	inDouble, inSingle bool

	// last is the character before the position being considered, carried across
	// nodes so that a quote after "<b>b</b>" knows it follows a letter.
	last rune

	verbatimDepth int
	node          strings.Builder
}

// Convert copies src to dst with typography applied to its prose.
func Convert(dst io.Writer, src io.Reader) (Result, error) {
	c := &converter{last: ' '}

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			c.flush()

			if blocks[tag] {
				// A block ends any open quotation, and resets the context so a
				// quote at the start of the next block is an opening one.
				if c.inDouble || c.inSingle {
					c.res.Unclosed++
				}
				c.inDouble, c.inSingle = false, false
				c.last = ' '
			}

			if !verbatim[tag] || !e.CanHaveContent() {
				return nil
			}
			c.verbatimDepth++
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != tag {
					return nil
				}
				c.flush()
				c.verbatimDepth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if c.verbatimDepth > 0 {
				return nil
			}
			c.node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			source := c.node.String()
			c.node.Reset()
			out := c.apply(stdhtml.UnescapeString(source))
			// As Text: every character this program writes is a character, so
			// nothing it does can become markup.
			return t.Replace(out, lolhtml.Text)
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			c.flush()
			if c.inDouble || c.inSingle {
				c.res.Unclosed++
			}
			return nil
		}),
	)
	if err != nil {
		return c.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.res, nil
}

// flush drops a pending node that will not be written, which happens when markup
// arrives while the accumulator holds text from a verbatim element.
func (c *converter) flush() { c.node.Reset() }

// apply rewrites one text node, carrying the pairing state and the previous
// character across the boundary.
func (c *converter) apply(text string) string {
	var b strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		next := ' '
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		switch r {
		case '"':
			if c.inDouble {
				b.WriteString(closeDouble)
				c.inDouble = false
			} else {
				b.WriteString(openDouble)
				c.inDouble = true
			}
			c.res.Quotes++
			c.last = r
			continue

		case '\'':
			// An apostrophe sits between two word characters; a closing quote
			// follows one and is not followed by one. One rule, both cases.
			switch {
			case isWord(c.last) && isWord(next):
				b.WriteString(closeSingle) // an apostrophe, same character
				c.res.Quotes++
			case c.inSingle:
				b.WriteString(closeSingle)
				c.inSingle = false
				c.res.Quotes++
			case !isWord(c.last):
				b.WriteString(openSingle)
				c.inSingle = true
				c.res.Quotes++
			default:
				b.WriteString(closeSingle)
				c.res.Quotes++
			}
			c.last = r
			continue

		case '-':
			// Three hyphens are an em dash, two an en dash, one a hyphen.
			if i+2 < len(runes) && runes[i+1] == '-' && runes[i+2] == '-' {
				b.WriteString(emDash)
				c.res.Dashes++
				i += 2
				c.last = '-'
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '-' {
				b.WriteString(enDash)
				c.res.Dashes++
				i++
				c.last = '-'
				continue
			}

		case '.':
			if i+2 < len(runes) && runes[i+1] == '.' && runes[i+2] == '.' {
				b.WriteString(ellipsis)
				c.res.Ellipses++
				i += 2
				c.last = '.'
				continue
			}
		}

		b.WriteRune(r)
		c.last = r
	}
	return b.String()
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func main() {
	res, err := Convert(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "typography:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "typography:", res)
	// Every replacement has to be valid UTF-8 and free of markup characters,
	// which is what makes the Text insertion unable to change the structure.
	for _, s := range []string{openDouble, closeDouble, openSingle, closeSingle, enDash, emDash, ellipsis} {
		if !utf8.ValidString(s) || strings.ContainsAny(s, `<>&"'`) {
			fmt.Fprintf(os.Stderr, "typography: bad replacement %q\n", s)
			os.Exit(1)
		}
	}
}

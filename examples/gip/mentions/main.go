// Command mentions turns @names and #tags in a document's text into links.
//
// The difference from examples/gip/linkify, which links URLs the document already
// contains, is that this one *builds* a URL out of document content - and that
// changes where the risk is. Escaping is the last step and the least important of
// three:
//
//	validate     is this a name at all? Only the characters a name may contain,
//	             and a length limit, so "@../admin" is not a mention.
//	encode       percent-encode it for the path, so a name that got through
//	             validation still cannot mean anything but itself.
//	escape       EscapeAttribute for the href, EscapeText for the link text.
//
// Skipping the first two and relying on the third is the mistake worth
// demonstrating: EscapeAttribute produces a perfectly well-formed href of
// "/u/../../admin", because a path traversal is not a markup problem and no
// escaper has an opinion about it. The tests measure that version.
//
// Everything else is the discipline the other text programs share: matching over
// the accumulated node, no linking inside an existing anchor or inside code, and
// text written back with EscapeText so a document that had "&amp;" still has it.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// candidate finds something that might be a mention or a tag. Deliberately
// permissive, because what is allowed is decided by validate rather than here:
// a pattern that also does the validating is a pattern nobody can read.
var candidate = regexp.MustCompile(`[@#][^\s<>"'&]+`)

// name is what a name may contain. Anything else is not a mention, which is the
// check that makes "@../admin" text rather than a link.
var name = regexp.MustCompile(`^[A-Za-z0-9_]{1,30}$`)

// noLink are the elements inside which a link must not be inserted.
var noLink = map[string]bool{
	"a": true, "script": true, "style": true, "title": true, "textarea": true,
	"template": true, "noscript": true, "iframe": true, "xmp": true,
	"noembed": true, "noframes": true, "option": true, "select": true,
	"button": true, "code": true, "pre": true, "kbd": true, "samp": true,
}

// A Result counts what happened.
type Result struct {
	Mentions, Tags int
	// Rejected is how many candidates failed validation, which is worth
	// reporting: a page full of "@../" is worth looking at.
	Rejected int
}

// Total is every link inserted.
func (r Result) Total() int { return r.Mentions + r.Tags }

// Linkify copies src to dst, linking mentions and tags in text.
func Linkify(dst io.Writer, src io.Reader) (Result, error) {
	var res Result
	depth := 0
	var node strings.Builder

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if !noLink[tag] || !e.CanHaveContent() {
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
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			source := node.String()
			node.Reset()

			markup, r := link(stdhtml.UnescapeString(source))
			res.Mentions += r.Mentions
			res.Tags += r.Tags
			res.Rejected += r.Rejected
			if r.Total() == 0 {
				// Nothing linked, so the node goes back as it came - source in,
				// source out, which is what stops a reference being escaped
				// twice. Written back either way, because the chunks it arrived
				// in have been removed.
				return t.Replace(source, lolhtml.HTML)
			}
			return t.Replace(markup, lolhtml.HTML)
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

// link builds the markup for one text node.
func link(text string) (string, Result) {
	var res Result
	var b strings.Builder
	last := 0
	for _, m := range candidate.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		body, trailing := trimTrailing(raw[1:])

		href, ok := buildURL(raw[0], body)
		if !ok {
			res.Rejected++
			continue
		}

		b.WriteString(lolhtml.EscapeText(text[last:m[0]]))
		// The href is percent-encoded already; EscapeAttribute is what keeps it
		// inside the quotes, and EscapeText is what keeps the label out of the
		// markup. Two escapers, and neither of them is the validation.
		b.WriteString(`<a href="` + lolhtml.EscapeAttribute(href) + `">`)
		b.WriteString(lolhtml.EscapeText(raw[0:1] + body))
		b.WriteString(`</a>`)
		b.WriteString(lolhtml.EscapeText(trailing))
		last = m[1]
		if raw[0] == '@' {
			res.Mentions++
		} else {
			res.Tags++
		}
	}
	b.WriteString(lolhtml.EscapeText(text[last:]))
	return b.String(), res
}

// buildURL validates the name and builds the path, in that order. The encoding
// is belt and braces: validate has already refused anything that would need it,
// and doing it anyway means a change to validate cannot turn into a traversal.
func buildURL(sigil byte, body string) (string, bool) {
	if !name.MatchString(body) {
		return "", false
	}
	base := "/u/"
	if sigil == '#' {
		base = "/t/"
	}
	return base + url.PathEscape(body), true
}

// trimTrailing takes the sentence punctuation off the end of a name.
func trimTrailing(s string) (string, string) {
	cut := len(s)
	for cut > 0 {
		switch s[cut-1] {
		case '.', ',', ';', ':', '!', '?', ')', ']', '}':
			cut--
		default:
			return s[:cut], s[cut:]
		}
	}
	return "", s
}

func main() {
	res, err := Linkify(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentions:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "mentions: %d mentions, %d tags, %d rejected\n",
		res.Mentions, res.Tags, res.Rejected)
}

// Command linkify turns bare URLs in a document's text into links, without
// breaking the links it already has.
//
// This is the first program here that inserts markup on purpose, and it needs
// both escapers at once, for the same URL:
//
//	<a href="` + EscapeAttribute(u) + `">` + EscapeText(u) + `</a>
//
// The two are not the same function. EscapeAttribute escapes five characters
// because the markup is being built by hand and could use either quote;
// EscapeText escapes three because that is what the library writes for Text. A
// URL containing "&" needs both, differently, and using one for both positions
// is the mistake this program exists to not make.
//
// Escaping is not sanitising, which matters more here than anywhere else in these
// examples: EscapeAttribute will produce a perfectly well-formed href of
// "javascript:alert(1)". So the scheme is checked before anything is built, and
// only http and https get through. That check is the security boundary; the
// escaping only keeps the markup well-formed.
//
// Not breaking existing anchors is a depth counter, because no selector says "not
// inside an <a>". Nesting a link inside a link produces something a parser
// unnests into markup nobody wrote, so the counter is the difference between a
// rewrite and a corruption.
//
// The text is matched over the accumulated node, decoded once, and the parts
// around each URL are written back with EscapeText - so a document whose text
// contains "&amp;" keeps it, rather than gaining another escape on every pass.
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

// bareURL is deliberately narrow: it must start with a scheme this program
// allows, and it stops at whitespace or a character no URL ends with.
var bareURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// allowedSchemes is the security boundary. Escaping a URL does not make it safe -
// EscapeAttribute will happily produce href="javascript:alert(1)" - so what may
// become an href is decided here, before anything is built.
var allowedSchemes = map[string]bool{"http": true, "https": true}

// noLink are the elements inside which a link must not be inserted: an existing
// anchor, and anything whose content is not prose.
var noLink = map[string]bool{
	"a": true, "script": true, "style": true, "title": true, "textarea": true,
	"template": true, "noscript": true, "iframe": true, "xmp": true,
	"noembed": true, "noframes": true, "option": true, "select": true,
	"button": true, "code": true, "pre": true, "kbd": true, "samp": true,
}

// A Result counts what happened.
type Result struct {
	// Linked is how many URLs became links.
	Linked int
	// Rejected is how many candidates were dropped because their scheme is not
	// allowed or they would not parse.
	Rejected int
}

// Linkify copies src to dst, linking bare URLs in text.
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
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				// Lowered whatever the token is named. The handler runs exactly
				// once for this element, and where the source left its end tag out
				// - </p>, </li> and </option> are all omissible, and <option> and
				// <code> are both on this list - the token belongs to an enclosing
				// element. Comparing names, which is the right test for a handler
				// writing at the position, would leave the counter above zero for
				// the rest of the document and silently stop linking anything.
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

			// Decoded before matching, so a URL written with references is
			// found, and every piece is escaped for where it goes.
			markup, linked, rejected := link(stdhtml.UnescapeString(source), &res)
			res.Linked += linked
			res.Rejected += rejected
			if linked == 0 {
				// Nothing to link, so put the text back as text. Written back
				// either way, because the chunks it arrived in are gone.
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

// link builds the markup for one text node: the text escaped as text, each
// allowed URL as an anchor with the URL escaped for an attribute and for text.
func link(text string, _ *Result) (string, int, int) {
	var b strings.Builder
	linked, rejected := 0, 0
	last := 0
	for _, m := range bareURL.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		trimmed, extra := trimTrailing(raw)

		if !allowed(trimmed) {
			rejected++
			continue
		}

		b.WriteString(lolhtml.EscapeText(text[last:m[0]]))
		// Two escapers, one URL: the attribute needs both quotes and the
		// ampersand escaped, the text needs the three that could be markup.
		b.WriteString(`<a href="` + lolhtml.EscapeAttribute(trimmed) + `">`)
		b.WriteString(lolhtml.EscapeText(trimmed))
		b.WriteString(`</a>`)
		b.WriteString(lolhtml.EscapeText(extra))
		last = m[1]
		linked++
	}
	b.WriteString(lolhtml.EscapeText(text[last:]))
	return b.String(), linked, rejected
}

// allowed reports whether a candidate may become an href. Escaping is not
// sanitising, so this is the check that matters.
func allowed(candidate string) bool {
	u, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	return allowedSchemes[strings.ToLower(u.Scheme)] && u.Host != ""
}

// trimTrailing takes the punctuation off the end of a URL that a sentence put
// there, and returns it separately so it can be written back outside the link.
//
// A closing bracket is kept when the URL opened one, because
// "http://x.example/a_(b)" is a URL and "(see http://x.example/a)" is not.
func trimTrailing(u string) (string, string) {
	cut := len(u)
	for cut > 0 {
		switch c := u[cut-1]; c {
		case '.', ',', ';', ':', '!', '?', '\'', '"':
			cut--
		case ')':
			// Strip it only if it is in excess. With "(" and ")" balanced the
			// bracket belongs to the URL, which is what makes
			// "(see http://x.example/a_(b))" come out with the inner pair kept
			// and the outer one outside the link.
			if strings.Count(u[:cut], ")") <= strings.Count(u[:cut], "(") {
				return u[:cut], u[cut:]
			}
			cut--
		case ']':
			if strings.Count(u[:cut], "]") <= strings.Count(u[:cut], "[") {
				return u[:cut], u[cut:]
			}
			cut--
		default:
			return u[:cut], u[cut:]
		}
	}
	return "", u
}

func main() {
	res, err := Linkify(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkify:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "linkify: %d linked, %d rejected\n", res.Linked, res.Rejected)
}

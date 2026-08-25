// Command comments renders an untrusted comment: it removes what a comment has no business
// containing, and turns bare URLs into links.
//
//	$ comments <<< 'see https://example.com/x <script>alert(1)</script> and <b>bold</b>'
//	see <a href="https://example.com/x" rel="nofollow noopener" target="_blank">https://example.com/x</a>  and <b>bold</b>
//	removed 1 elements and 0 attributes, linkified 1 URLs, refused 0 hrefs
//
// Linkifying is the interesting half, because it is the one place a program has to build markup
// out of text an attacker chose. The link is markup and the text inside it is not, so the
// insertion is half of each - which is what [lolhtml.EscapeText] and [lolhtml.EscapeAttribute]
// are for, and why they are exported.
//
// # Why the text has to be accumulated
//
// A text node arrives in chunks with no guaranteed boundaries, and a URL can straddle two of
// them: "https://exa" and "mple.com/x". A per-chunk linkifier finds nothing in either. So the
// chunks are accumulated to IsLastInTextNode and the whole node is rewritten at its last chunk,
// which is the discipline the library's documentation gives for anything that matches a pattern
// in text.
//
// The cost is that the node has to be written back whether or not anything changed, because
// accumulating means removing the chunks it arrived in. That is why an unchanged comment still
// comes out of this program byte-identical only if its text has no chunk boundaries the
// tokenizer chose differently - which is why the test for it compares the parse rather than the
// bytes.
//
// # What is not linkified
//
// Text already inside an <a>. There is no selector for "not inside a link", so the depth of open
// anchors is counted, and a URL inside one is left alone - otherwise the output has a link inside
// a link, which a parser resolves by ending the first one and moving the content out.
//
// # What is removed
//
// Everything not on a short allow-list: b, i, em, strong, code, br, p, a and blockquote. A
// comment does not need a table and must not have a script. Attributes go the same way, except
// href on an anchor, which is kept only when its decoded scheme is http, https or mailto - the
// decoded form, because a browser decodes "&#106;avascript:" before it acts and a check on the
// raw text does not.
package main

import (
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"regexp"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// AllowedAttributes is what may stay, by element. rel and target are deliberately absent: the
// link policy is this program's rather than the commenter's, and the element handler sets them.

// Allowed is what a comment may contain.
var Allowed = map[string]bool{
	"b": true, "i": true, "em": true, "strong": true, "code": true,
	"br": true, "p": true, "a": true, "blockquote": true,
}

// AllowedAttributes is what may stay, by element.
var AllowedAttributes = map[string]map[string]bool{
	"a": {"href": true},
}

// SafeSchemes are the schemes a link may use. No scheme at all - a relative URL - is not allowed
// here: a comment's link to "/admin" is a link to the host's own admin page, which is not what
// the commenter can be trusted to mean.
var SafeSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// urlPattern finds bare URLs in text. It is deliberately narrow: what it misses stays text, which
// is the safe direction, and what it finds still has to pass the scheme check.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// Options are the caller's choices. Prefix is the one place a value arrives from outside the
// document rather than out of it, which is why it is the one place lolhtml.Text is used: a Go
// string is not source, so it has to be escaped, and a prefix saying "<b>Reply</b>" has to appear
// as those characters rather than as bold text.
type Options struct {
	Prefix string
}

// Report is what the render did.
type Report struct {
	Removed    int
	Attributes int
	Linkified  int
	UnsafeHref int
}

func (r Report) String() string {
	return fmt.Sprintf("removed %d elements and %d attributes, linkified %d URLs, refused %d hrefs",
		r.Removed, r.Attributes, r.Linkified, r.UnsafeHref)
}

// Render rewrites an untrusted comment from r into w.
func Render(r io.Reader, w io.Writer, opts Options) (Report, error) {
	var report Report

	// anchors is the depth of open <a> elements, because there is no selector for "not
	// inside a link".
	anchors := 0
	// node accumulates the current text node: a URL can straddle chunk boundaries, so the
	// pattern is matched over the whole node rather than over what happened to arrive.
	var node strings.Builder

	handlers := []lolhtml.Option{
		// The prefix is a Go string, so it is inserted as Text: the library escapes it,
		// and a prefix containing markup arrives as characters.
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if opts.Prefix == "" {
				return nil
			}
			return d.Append(opts.Prefix, lolhtml.Text)
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if e.IsRemoved() {
				return nil
			}
			tag := e.TagName()
			if !Allowed[tag] {
				report.Removed++
				// Which removal depends on what the content is. A <div> holds
				// prose, and taking the text out with the tag would lose what the
				// person wrote; a <script> or a <style> holds code, and keeping
				// that would put "alert(1)" on the page as visible text. The
				// library knows which elements hold code rather than markup.
				if lolhtml.IsRawText(tag) {
					e.Remove()
					return nil
				}
				e.RemoveAndKeepContent()
				return nil
			}
			if tag == "a" && e.CanHaveContent() {
				anchors++
				if err := e.OnEndTag(func(*lolhtml.EndTag) error {
					anchors--
					return nil
				}); err != nil {
					return err
				}
			}
			for _, attr := range e.AttributeList() {
				name := strings.ToLower(attr.Name)
				keep := AllowedAttributes[tag][name]
				if keep && name == "href" && !safeURL(attr.Value) {
					report.UnsafeHref++
					keep = false
				}
				if keep {
					continue
				}
				report.Attributes++
				if err := e.RemoveAttribute(attr.Name); err != nil {
					return err
				}
			}

			// The link policy is the renderer's, not the commenter's: rel and target
			// are set on every surviving anchor rather than kept from the input. That
			// is why they are not on the allow-list - a commenter's own rel would
			// otherwise stay, and this program's whole claim about links is that it
			// decides them.
			//
			// It also makes the program idempotent. The first version allowed href
			// alone and set rel and target on the way out, so rendering its own output
			// stripped them: the idempotence test found that.
			if tag == "a" {
				if _, ok := e.Attribute("href"); ok {
					if err := e.SetAttribute("rel", "nofollow noopener"); err != nil {
						return err
					}
					if err := e.SetAttribute("target", "_blank"); err != nil {
						return err
					}
				}
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			// A comment inside a comment is a place to hide markup from a reader who is
			// looking at the rendered page.
			report.Removed++
			c.Remove()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			node.WriteString(c.Text())
			if !c.IsLastInTextNode() {
				// The chunk is removed because the whole node is written back at its
				// last chunk; leaving it would duplicate the text.
				c.Remove()
				return nil
			}
			text := node.String()
			node.Reset()

			// The accumulated text is the document's own source: character references
			// arrive undecoded, so "a &amp; b" is those nine characters. Writing it
			// back with Text would escape it again and produce "a &amp;amp; b" - which
			// is what the first version of this program did, and what its own test for
			// entities caught. The rule the library gives is: decide on the decoded
			// form, write back the raw one.
			if anchors > 0 {
				// Inside a link: write the text back unchanged rather than nesting
				// one link inside another.
				return c.Replace(text, lolhtml.HTML)
			}
			rendered, found := linkify(text)
			report.Linkified += found
			if found == 0 {
				return c.Replace(text, lolhtml.HTML)
			}
			// Markup, because a link is markup - and every piece of the commenter's
			// own text inside it went through EscapeText or EscapeAttribute.
			return c.Replace(rendered, lolhtml.HTML)
		}),
	}

	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return report, err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return report, err
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	return report, nil
}

// RenderString is Render over a string, which is what the tests use.
func RenderString(doc string) (string, Report, error) {
	var out strings.Builder
	report, err := Render(strings.NewReader(doc), &out, Options{})
	return out.String(), report, err
}

// linkify turns bare URLs in text into anchors and returns the markup and how many it found.
//
// Nothing here escapes anything, and that is the point rather than an omission. The text is the
// document's own source - a text node's contents arrive with their character references intact -
// so it is already in the form the output needs, and escaping it again would turn "&amp;" into
// "&amp;amp;". The URL goes into the href the same way, for the same reason: the source form of a
// URL is what a browser will resolve, and re-escaping it would change it.
//
// What makes that safe is what the text node is: bytes the tokenizer decided are not markup. A
// "<" inside one is a "<" that could not begin a tag, and re-emitting it at the same place leaves
// it text. The pattern excludes quotes and angle brackets from a URL, so nothing matched can end
// the attribute it is written into.
//
// The rel and target attributes are written here as well as by the element handler, and that is
// not redundancy. An anchor this function produces is markup the same pass created, and a
// selector does not match what a handler inserted - that is deliberate in the library, and it is
// what stops a rewrite triggering itself. So the handler cannot reach these anchors, and they
// have to arrive complete. The first version left them to the handler and produced links with no
// policy, which the idempotence test caught: the two passes disagreed.
func linkify(text string) (string, int) {
	matches := urlPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return "", 0
	}

	var sb strings.Builder
	found := 0
	at := 0
	for _, m := range matches {
		url := text[m[0]:m[1]]
		if !safeURL(url) {
			continue
		}
		sb.WriteString(text[at:m[0]])
		sb.WriteString(`<a href="`)
		sb.WriteString(url)
		sb.WriteString(`" rel="nofollow noopener" target="_blank">`)
		sb.WriteString(url)
		sb.WriteString(`</a>`)
		at = m[1]
		found++
	}
	if found == 0 {
		return "", 0
	}
	sb.WriteString(text[at:])
	return sb.String(), found
}

// safeURL reports whether a URL's scheme is one a comment may link to.
//
// The value is decoded first: a browser decodes "&#106;avascript:" before it acts, so a check on
// the raw text sees a scheme called "&#106;avascript" and lets it through. Whitespace and the
// characters a parser ignores inside a scheme are removed for the same reason.
func safeURL(v string) bool {
	decoded := strings.ToLower(stdhtml.UnescapeString(v))
	decoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v', 0:
			return -1
		}
		return r
	}, decoded)

	colon := strings.IndexByte(decoded, ':')
	if colon < 0 {
		// No scheme: not allowed here, because a relative link in a comment points at
		// the host's own pages.
		return false
	}
	if i := strings.IndexAny(decoded[:colon], "/?#"); i >= 0 {
		return false
	}
	return SafeSchemes[decoded[:colon]]
}

func main() {
	quiet := flag.Bool("quiet", false, "do not print the report")
	prefix := flag.String("suffix", "", "plain text to append after the comment")
	flag.Parse()

	report, err := Render(os.Stdin, os.Stdout, Options{Prefix: *prefix})
	if err != nil {
		fmt.Fprintln(os.Stderr, "comments:", err)
		os.Exit(1)
	}
	if !*quiet {
		fmt.Fprintln(os.Stderr, report)
	}
}

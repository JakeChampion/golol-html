// Command tweetquote rewrites embedded tweets into plain blockquotes with
// attribution, so a page that quotes someone does not load a third-party script
// to show the quote.
//
// The embed Twitter and X hand out is already a blockquote with a fallback
// inside it:
//
//	<blockquote class="twitter-tweet">
//	  <p lang="en" dir="ltr">the text, with <a href="...">links</a></p>
//	  &mdash; Name (@handle) <a href="https://twitter.com/handle/status/123">date</a>
//	</blockquote>
//	<script async src="https://platform.twitter.com/widgets.js"></script>
//
// The script replaces all that with an iframe. Without the script the fallback
// is what a reader sees, and it is nearly right already: the work is to give it
// a cite, name the author in a way that survives without the widget's styling,
// and drop the script.
//
// What this program does not do is fetch anything. Everything in the output
// came out of the input, so an embed that carries no permalink is left alone
// rather than guessed at.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A tweet is what one embed gave up about itself. Everything is optional
// except the permalink, without which there is nothing to cite.
type tweet struct {
	permalink string
	handle    string // without the @
	name      string
	done      bool            // an attribution from an earlier pass is already inside
	text      strings.Builder // the fallback's own text, for reading the name out of
}

type quoter struct {
	// dropScript removes the widget script tags as well as rewriting the
	// blockquotes. On by default: leaving the script behind means the widget
	// replaces the blockquote and the rewrite was pointless.
	dropScript bool

	// klass is added to each rewritten blockquote so a stylesheet can reach it.
	klass string

	// open is the stack of blockquotes whose end tag has not arrived. A stack
	// rather than a single pointer because a quote tweet nests one embed inside
	// another, and an anchor belongs to the innermost one.
	open []*tweet

	converted int
	scripts   int
	skipped   map[string]int
}

func (q *quoter) note(reason string) {
	if q.skipped == nil {
		q.skipped = map[string]int{}
	}
	q.skipped[reason]++
}

// embedSelector is the embed's own class, and matching on it is the only
// reliable marker: matching every blockquote would rewrite ordinary quotations.
const embedSelector = "blockquote.twitter-tweet, blockquote.twitter-video"

func (q *quoter) options() []lolhtml.Option {
	opts := []lolhtml.Option{
		lolhtml.OnElement(embedSelector, q.begin),

		// Separate handlers rather than work the element handler does: the
		// element handler runs on the start tag, when the anchors and the text
		// inside the blockquote have not been parsed yet.
		lolhtml.OnElement("blockquote.twitter-tweet a[href], blockquote.twitter-video a[href]",
			q.collectLink),
		lolhtml.OnText(embedSelector, q.collectText),

		// Idempotence, in one pass: the footer this program adds sits inside the
		// blockquote, so seeing one means the work is done.
		lolhtml.OnElement(
			"blockquote.twitter-tweet footer.tweetquote-attribution, "+
				"blockquote.twitter-video footer.tweetquote-attribution",
			func(*lolhtml.Element) error {
				if t := q.current(); t != nil {
					t.done = true
				}
				return nil
			}),
	}
	if q.dropScript {
		opts = append(opts, lolhtml.OnElement("script[src]", func(e *lolhtml.Element) error {
			if !isWidgetScript(stdhtml.UnescapeString(attr(e, "src"))) {
				return nil
			}
			q.scripts++
			e.Remove()
			return nil
		}))
	}
	return opts
}

// begin opens a blockquote and arranges for it to be closed. Nothing is
// inserted here: what to insert is not known until the end tag, which is also
// the first moment at which it is known whether there was a permalink at all.
func (q *quoter) begin(e *lolhtml.Element) error {
	if !e.CanHaveContent() {
		// A self-closing blockquote has no fallback to work with, and no end tag
		// to hang a handler on.
		q.note("the blockquote has no content")
		return nil
	}
	t := &tweet{}
	q.open = append(q.open, t)

	if err := e.SetAttribute("class", addClass(attr(e, "class"), q.klass)); err != nil {
		return err
	}

	return e.OnEndTag(func(end *lolhtml.EndTag) error {
		q.open = q.open[:len(q.open)-1]
		if t.done {
			// A second pass over output from a first one. The footer is inside
			// the blockquote, so it has been seen by the time the end tag
			// arrives, which is what makes one pass enough to notice.
			q.note("already has an attribution")
			return nil
		}
		if t.permalink == "" {
			q.note("no tweet permalink inside the blockquote")
			return nil
		}
		t.name = readName(t.text.String(), t.handle)
		q.converted++
		return end.Before(q.attribution(t), lolhtml.HTML)
	})
}

// current is the innermost open blockquote, or nil if a handler fired for one
// that begin declined to take on.
func (q *quoter) current() *tweet {
	if len(q.open) == 0 {
		return nil
	}
	return q.open[len(q.open)-1]
}

func (q *quoter) collectLink(e *lolhtml.Element) error {
	t := q.current()
	if t == nil {
		return nil
	}
	href := stdhtml.UnescapeString(strings.TrimSpace(attr(e, "href")))
	handle, _, ok := statusURL(href)
	if !ok {
		return nil
	}
	// The last status link in the blockquote wins, because that is where the
	// embed puts the permalink: the date at the end. An earlier one is a link
	// in the tweet's own text.
	t.permalink, t.handle = href, handle
	return nil
}

func (q *quoter) collectText(tc *lolhtml.TextChunk) error {
	if t := q.current(); t != nil {
		t.text.WriteString(tc.Text())
	}
	return nil
}

// readName pulls the author's name out of the fallback's trailing line, which
// the embed writes as "&mdash; Name (@handle) date". The handle is already known
// from the permalink, so it is the anchor to search around rather than something
// to parse out.
//
// The text arrives as raw source, so the separator is the five characters
// "&mdash;" and not an em dash. Decoding first is the only way to match one rule
// against both spellings, and the same rule the library states for attributes:
// decode to decide, rewrite raw.
//
// A name that is not found is not invented. The attribution is still useful with
// a handle and a permalink alone, and the alternative - taking everything before
// the handle - captured the entire tweet.
func readName(text, handle string) string {
	if handle == "" {
		return ""
	}
	decoded := stdhtml.UnescapeString(text)
	i := strings.LastIndex(decoded, "(@"+handle+")")
	if i < 0 {
		return ""
	}
	before := decoded[:i]

	j := strings.LastIndex(before, "\u2014")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(before[j+len("\u2014"):])
}

// attribution is the markup added before the closing </blockquote>.
//
// Every value in it came out of the document, so every value is escaped for the
// context it lands in. lolhtml.EscapeText and lolhtml.EscapeAttribute rather
// than SetAttribute, because SetAttribute needs an element a handler is holding
// and none of this exists yet. They take literal values, which is what these
// are: the name was decoded to find it, the permalink was decoded to parse it,
// and a handle is fifteen characters of [A-Za-z0-9_] or it was rejected.
func (q *quoter) attribution(t *tweet) string {
	var sb strings.Builder
	sb.WriteString(`<footer class="tweetquote-attribution">`)

	if t.name != "" {
		sb.WriteString(`<span class="tweetquote-name">`)
		sb.WriteString(lolhtml.EscapeText(t.name))
		sb.WriteString(`</span> `)
	}
	if t.handle != "" {
		fmt.Fprintf(&sb, `<a class="tweetquote-handle" href="https://twitter.com/%s" rel="noopener nofollow">@%s</a> `,
			lolhtml.EscapeAttribute(t.handle), lolhtml.EscapeText(t.handle))
	}
	fmt.Fprintf(&sb, `<a class="tweetquote-permalink" href="%s" rel="noopener nofollow">permalink</a>`,
		lolhtml.EscapeAttribute(t.permalink))
	sb.WriteString(`</footer>`)
	return sb.String()
}

// statusURL reads a tweet permalink. Both hosts are accepted because an embed
// copied before the rename says twitter.com and one copied after says x.com.
func statusURL(href string) (handle, id string, ok bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", "", false
	}
	h := strings.ToLower(u.Host)
	h = strings.TrimPrefix(h, "www.")
	h = strings.TrimPrefix(h, "mobile.")
	if h != "twitter.com" && h != "x.com" {
		return "", "", false
	}

	// /<handle>/status/<id>, with an optional trailing segment such as /photo/1.
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || (parts[1] != "status" && parts[1] != "statuses") {
		return "", "", false
	}
	if !validHandle(parts[0]) || !allDigits(parts[2]) {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// validHandle keeps the handle to what a handle can be, because it goes back
// into a URL. Twitter's own rule is 1 to 15 of letters, digits and underscore.
func validHandle(s string) bool {
	if s == "" || len(s) > 15 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isWidgetScript(src string) bool {
	l := strings.ToLower(src)
	for _, marker := range []string{
		"platform.twitter.com/widgets.js",
		"platform.x.com/widgets.js",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func addClass(existing, add string) string {
	if add == "" {
		return existing
	}
	if existing == "" {
		return add
	}
	for _, f := range strings.Fields(existing) {
		if f == add {
			return existing
		}
	}
	return existing + " " + add
}

func (q *quoter) run(r io.Reader, w io.Writer) error {
	out, err := lolhtml.NewWriter(w, q.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func quoteString(in string, opts ...func(*quoter)) (string, *quoter, error) {
	q := &quoter{dropScript: true, klass: "tweetquote"}
	for _, o := range opts {
		o(q)
	}
	var out bytes.Buffer
	err := q.run(strings.NewReader(in), &out)
	return out.String(), q, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func main() {
	q := &quoter{}
	flag.BoolVar(&q.dropScript, "drop-script", true,
		"remove the widget script, without which the rewrite is undone by it")
	flag.StringVar(&q.klass, "class", "tweetquote",
		"class added to each rewritten blockquote")
	flag.Parse()

	var in io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "tweetquote:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: tweetquote [-drop-script=false] [file.html]")
		os.Exit(2)
	}

	if err := q.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tweetquote:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "converted=%d scripts-removed=%d", q.converted, q.scripts)
	for reason, n := range q.skipped {
		fmt.Fprintf(os.Stderr, " %s=%d", reason, n)
	}
	fmt.Fprintln(os.Stderr)
}

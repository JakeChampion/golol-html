// Command linktext finds links whose text says nothing about where they go, and
// either flags or fixes them.
//
//	linktext < page.html                 # report only
//	linktext -flag < page.html > out.html # mark them in the document
//	linktext -fix page.html > out.html    # rewrite the text
//
// The three modes exist because of one constraint, which is worth naming rather
// than working around quietly: whether a link's text is generic is not known
// until the link's closing tag, and by then the only thing that can still be done
// is to insert content. An attribute cannot be set on an element whose end tag
// has already arrived, and the element's content cannot be replaced either.
//
// So -flag inserts a marker after the link, which the end tag allows, and -fix
// reads the document twice: once to decide, once to rewrite. Reporting needs
// neither.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	doFlag := flag.Bool("flag", false, "insert a marker after each offending link")
	doFix := flag.Bool("fix", false, "rewrite the text, reading the input twice")
	flag.Parse()

	if *doFlag && *doFix {
		fmt.Fprintln(os.Stderr, "linktext: -flag and -fix are alternatives")
		os.Exit(2)
	}

	c := &checker{}

	switch {
	case *doFix:
		if flag.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: linktext -fix file.html > out.html")
			os.Exit(2)
		}
		in, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "linktext:", err)
			os.Exit(1)
		}
		if err := c.fix(in, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "linktext:", err)
			os.Exit(1)
		}
	case *doFlag:
		c.mark = true
		if err := c.pass(os.Stdin, os.Stdout, false); err != nil {
			fmt.Fprintln(os.Stderr, "linktext:", err)
			os.Exit(1)
		}
	default:
		if err := c.pass(os.Stdin, io.Discard, false); err != nil {
			fmt.Fprintln(os.Stderr, "linktext:", err)
			os.Exit(1)
		}
	}

	fmt.Fprint(os.Stderr, c.report())
	if len(c.bad) > 0 && !*doFix && !*doFlag {
		os.Exit(1)
	}
}

// generic is text that describes nothing. A screen reader user listing a page's
// links hears only this, so a page of them is a page with no navigation.
var generic = map[string]bool{
	"click here": true, "here": true, "read more": true, "more": true,
	"link": true, "this": true, "this link": true, "learn more": true,
	"details": true, "info": true, "continue": true, "go": true,
	"download": true, "view": true, "see more": true, "full story": true,
	"read on": true, "find out more": true, "click": true, "open": true,
}

type offender struct {
	ord         int
	text        string
	href        string
	replacement string
	source      string
}

type checker struct {
	mark bool

	nth int
	bad []offender
	// plan maps a link's ordinal to its replacement, filled by the deciding
	// pass and consulted by the rewriting one.
	plan map[int]offender

	open *pending
	text strings.Builder
}

type pending struct {
	ord      int
	href     string
	label    string // aria-label or title, known at the start tag
	altParts []string
}

func (c *checker) fix(in []byte, dst io.Writer) error {
	if err := c.pass(bytes.NewReader(in), io.Discard, false); err != nil {
		return err
	}

	c.plan = make(map[int]offender, len(c.bad))
	for _, o := range c.bad {
		if o.replacement != "" {
			c.plan[o.ord] = o
		}
	}
	c.nth = 0

	return c.pass(bytes.NewReader(in), dst, true)
}

func (c *checker) pass(src io.Reader, dst io.Writer, rewrite bool) error {
	w, err := lolhtml.NewWriter(dst, c.options(rewrite)...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (c *checker) options(rewrite bool) []lolhtml.Option {
	opts := []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			c.nth++
			href, _ := e.Attribute("href")
			p := &pending{ord: c.nth, href: stdhtml.UnescapeString(strings.TrimSpace(href))}

			// aria-label and title are the accessible name when present, and
			// both are known here, which is what makes them usable as the
			// replacement source in a single pass.
			if v, ok := e.Attribute("aria-label"); ok && strings.TrimSpace(v) != "" {
				p.label = stdhtml.UnescapeString(strings.TrimSpace(v))
			} else if v, ok := e.Attribute("title"); ok && strings.TrimSpace(v) != "" {
				p.label = stdhtml.UnescapeString(strings.TrimSpace(v))
			}

			c.open = p
			c.text.Reset()

			if !rewrite {
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					return c.decide(t)
				})
			}

			// Rewriting pass: the decision is already in hand, so the content
			// can be replaced at the start tag - the only place it can be.
			o, ok := c.plan[p.ord]
			if !ok {
				c.open = nil
				return nil
			}
			c.open = nil
			return e.SetInnerContent(o.replacement, lolhtml.Text)
		}),

		lolhtml.OnText("a[href]", func(t *lolhtml.TextChunk) error {
			if c.open != nil {
				c.text.WriteString(t.Text())
			}
			return nil
		}),

		// An image inside the link contributes its alt text, which is what a
		// screen reader reads.
		lolhtml.OnElement("a[href] img[alt]", func(e *lolhtml.Element) error {
			if c.open == nil {
				return nil
			}
			if alt, ok := e.Attribute("alt"); ok && strings.TrimSpace(alt) != "" {
				c.open.altParts = append(c.open.altParts, stdhtml.UnescapeString(alt))
			}
			return nil
		}),
	}
	return opts
}

// decide runs at the link's end tag, where the text is finally complete. By then
// the element is gone: an attribute cannot be set and the content cannot be
// replaced. Inserting after the end tag is the only mutation still available,
// which is what -flag uses and why -fix needs a second pass.
func (c *checker) decide(t *lolhtml.EndTag) error {
	p := c.open
	c.open = nil
	if p == nil {
		return nil
	}

	visible := strings.Join(strings.Fields(stdhtml.UnescapeString(c.text.String())), " ")
	if visible == "" && len(p.altParts) > 0 {
		visible = strings.Join(strings.Fields(strings.Join(p.altParts, " ")), " ")
	}

	if !isGeneric(visible) {
		return nil
	}

	o := offender{ord: p.ord, text: visible, href: p.href}
	o.replacement, o.source = bestReplacement(p, visible)
	c.bad = append(c.bad, o)

	if !c.mark {
		return nil
	}

	note := fmt.Sprintf("%q says nothing about %q", visible, p.href)
	if o.replacement != "" {
		note += fmt.Sprintf("; suggest %q from %s", o.replacement, o.source)
	}
	// One call, not three. Successive After calls put the newest closest to the
	// unit, so building the comment out of a delimiter, the note and a closing
	// delimiter emits them backwards and produces "-->note<!--" - which an
	// earlier version of this did, and which looks like valid output until you
	// read it.
	//
	// The note is document-derived and this is a comment being assembled, so it
	// has to be made safe - but not by escaping. Character references are not
	// decoded inside comment data, so an escaped ">" stays the four characters
	// "&gt;" in the comment for anyone who reads it. What ends a comment is two
	// hyphens, so what keeps one intact is not letting two sit together.
	return t.After("<!-- linktext: "+commentSafe(note)+" -->", lolhtml.HTML)
}

// commentSafe makes document-derived text safe inside a comment being built,
// without changing what it says.
//
// A space between any two hyphens is enough: it is "--" that ends a comment,
// whether as "-->" or "--!>", so no two adjacent hyphens means no early end. A
// leading ">" or "->" is the other rule - "<!-->" and "<!--->" are both empty
// comments - and a space in front settles it. Everything else, including "&" and
// "<", is left exactly as it arrived, because a comment is not parsed.
func commentSafe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' && prevHyphen {
			b.WriteByte(' ')
		}
		b.WriteByte(c)
		prevHyphen = c == '-'
	}
	out := b.String()
	if strings.HasPrefix(out, ">") || strings.HasPrefix(out, "->") {
		return " " + out
	}
	return out
}

func isGeneric(text string) bool {
	if text == "" {
		return true
	}
	lower := strings.ToLower(strings.Trim(text, " .!>»→"))
	return generic[lower]
}

// bestReplacement picks the most trustworthy description available, and names
// where it came from so a reviewer can judge it.
func bestReplacement(p *pending, visible string) (string, string) {
	if p.label != "" && !isGeneric(p.label) {
		return p.label, "aria-label or title"
	}
	if alt := strings.Join(p.altParts, " "); strings.TrimSpace(alt) != "" && !isGeneric(alt) {
		return strings.Join(strings.Fields(alt), " "), "the image alt text"
	}
	if d := fromHref(p.href); d != "" {
		return d, "the target URL"
	}
	return "", ""
}

// fromHref humanises the last meaningful path segment. It is a last resort: a
// URL is a poor description, and a bad one is worse than a flagged one, so
// anything that does not look like words is refused.
func fromHref(href string) string {
	u, err := url.Parse(href)
	if err != nil || u.Path == "" {
		return ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	last := segs[len(segs)-1]
	if i := strings.LastIndexByte(last, '.'); i > 0 {
		last = last[:i]
	}
	last = strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == '_' || r == '+':
			return ' '
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ':
			return r
		default:
			return -1
		}
	}, last)

	words := strings.Fields(last)
	if len(words) == 0 {
		return ""
	}
	// A segment that is mostly digits or a single short token is an id, not a
	// description.
	letters := 0
	for _, r := range strings.Join(words, "") {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 3 {
		return ""
	}

	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

func (c *checker) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "links=%d uninformative=%d\n", c.nth, len(c.bad))

	sorted := append([]offender(nil), c.bad...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ord < sorted[j].ord })
	for _, o := range sorted {
		if o.replacement == "" {
			fmt.Fprintf(&sb, "  %q -> %q: no better description available\n", o.text, o.href)
			continue
		}
		fmt.Fprintf(&sb, "  %q -> %q: use %q (from %s)\n", o.text, o.href, o.replacement, o.source)
	}
	return sb.String()
}

func checkString(in string, mark bool) (string, *checker, error) {
	c := &checker{mark: mark}
	var out bytes.Buffer
	err := c.pass(strings.NewReader(in), &out, false)
	return out.String(), c, err
}

func fixString(in string) (string, *checker, error) {
	c := &checker{}
	var out bytes.Buffer
	err := c.fix([]byte(in), &out)
	return out.String(), c, err
}

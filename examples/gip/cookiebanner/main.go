// Command cookiebanner injects a consent banner before the closing body
// content, without a script and without a template engine.
//
// The banner is a form: a POST to a path the operator gives, with the page's own
// URL in a hidden field so the server can send the reader back. No JavaScript,
// so it works with scripting off, and nothing is fetched at rewrite time.
//
// The interesting part is not the HTML, it is that every value in it comes from
// somewhere else - a flag, a configuration file, the page being rewritten - and
// the banner is assembled as a string, which makes this program the serialiser.
// lolhtml.EscapeText and lolhtml.EscapeAttribute are what SetAttribute and
// ContentType Text would have done, for markup that has no element yet.
//
// An existing banner is left alone: matching on the marker class means a second
// pass adds nothing, and a page that already asks for consent is not asked twice.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// banner is the copy and the endpoints. Everything here is operator input, which
// is trusted no more than the document: a message pasted from a legal team's
// document arrives with typographic quotes and the occasional stray angle
// bracket, and a policy URL arrives with query parameters in it.
type banner struct {
	message  string
	accept   string // the accept button's label
	reject   string // the reject button's label
	action   string // where the form posts
	policy   string // link to the privacy policy, optional
	policyBy string // the policy link's text
	klass    string // marker class, also how a second pass recognises its work
}

type injector struct {
	b banner

	// canonical is the page's own URL, put in a hidden field so the server can
	// redirect back. Read from a <link rel=canonical> when the page has one.
	canonical string

	injected int
	skipped  map[string]int
}

func (in *injector) note(reason string) {
	if in.skipped == nil {
		in.skipped = map[string]int{}
	}
	in.skipped[reason]++
}

func (in *injector) options() []lolhtml.Option {
	var present bool
	var done bool

	return []lolhtml.Option{
		// The canonical link is in the head, so it has been seen by the time the
		// document ends. A page without one gets a banner with an empty return
		// field rather than no banner.
		lolhtml.OnElement("link[rel=canonical][href]", func(e *lolhtml.Element) error {
			if in.canonical == "" {
				in.canonical = stdhtml.UnescapeString(strings.TrimSpace(attr(e, "href")))
			}
			return nil
		}),

		// A banner already on the page. Matched by class, which is the same
		// marker this program adds, so its own output is recognised.
		lolhtml.OnElement("."+in.b.klass, func(*lolhtml.Element) error {
			present = true
			return nil
		}),

		// The banner belongs before the closing body content, so the body's end
		// tag is the place for it. A rewriter has no tree, so "the body" means
		// the first <body> whose end tag arrives: a document with two of them
		// would otherwise get two banners.
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if present || done {
					return nil
				}
				done = true
				in.injected++
				return end.Before(in.render(), lolhtml.HTML)
			})
		}),

		// A fragment has no body, and one is not invented for it: the end of the
		// document is where the banner goes instead.
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if present {
				in.note("a banner is already on the page")
				return nil
			}
			if done {
				return nil
			}
			done = true
			in.injected++
			return d.Append(in.render(), lolhtml.HTML)
		}),
	}
}

// render builds the banner. Every interpolated value is escaped for the position
// it lands in: EscapeText between tags, EscapeAttribute inside the quotes this
// function chose.
//
// The values are literals rather than markup - flags, and a canonical URL
// decoded on the way in - which is what these two functions take. A value left
// raw from the document would be double-escaped by them.
func (in *injector) render() string {
	b := in.b

	var sb strings.Builder
	sb.WriteString("\n<div class=\"")
	sb.WriteString(lolhtml.EscapeAttribute(b.klass))
	sb.WriteString("\" role=\"region\" aria-label=\"Cookie consent\">")

	fmt.Fprintf(&sb, `<form method="post" action="%s">`,
		lolhtml.EscapeAttribute(b.action))

	fmt.Fprintf(&sb, `<p class="%s-message">%s`,
		lolhtml.EscapeAttribute(b.klass), lolhtml.EscapeText(b.message))
	if b.policy != "" {
		fmt.Fprintf(&sb, ` <a href="%s" rel="noopener">%s</a>`,
			lolhtml.EscapeAttribute(b.policy), lolhtml.EscapeText(b.policyBy))
	}
	sb.WriteString("</p>")

	if in.canonical != "" {
		fmt.Fprintf(&sb, `<input type="hidden" name="return_to" value="%s">`,
			lolhtml.EscapeAttribute(in.canonical))
	}

	fmt.Fprintf(&sb, `<button type="submit" name="consent" value="accept">%s</button>`,
		lolhtml.EscapeText(b.accept))
	fmt.Fprintf(&sb, `<button type="submit" name="consent" value="reject">%s</button>`,
		lolhtml.EscapeText(b.reject))

	sb.WriteString("</form></div>\n")
	return sb.String()
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// validate checks the one value that is not going into markup. The marker class
// is also interpolated into a selector, which is a third escaping context with
// its own rules - and the library refuses an invalid selector rather than
// guessing at it, which is the right answer but a confusing one to meet in the
// middle of a rewrite. So it is checked here, where the message can say what is
// wrong.
func (in *injector) validate() error {
	if !validClass(in.b.klass) {
		return fmt.Errorf("the marker class %q is not a CSS identifier: it is used "+
			"in a selector as well as in markup, so it has to be letters, digits, "+
			"hyphens and underscores, not starting with a digit", in.b.klass)
	}
	return nil
}

func validClass(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (in *injector) run(r io.Reader, w io.Writer) error {
	if err := in.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, in.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func defaults() banner {
	return banner{
		message:  "We use cookies to measure how this site is used.",
		accept:   "Accept",
		reject:   "Reject",
		action:   "/consent",
		policyBy: "Privacy policy",
		klass:    "cookie-banner",
	}
}

func injectString(input string, opts ...func(*injector)) (string, *injector, error) {
	in := &injector{b: defaults()}
	for _, o := range opts {
		o(in)
	}
	var out bytes.Buffer
	err := in.run(strings.NewReader(input), &out)
	return out.String(), in, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func main() {
	in := &injector{b: defaults()}
	flag.StringVar(&in.b.message, "message", in.b.message, "the banner's text")
	flag.StringVar(&in.b.accept, "accept", in.b.accept, "the accept button's label")
	flag.StringVar(&in.b.reject, "reject", in.b.reject, "the reject button's label")
	flag.StringVar(&in.b.action, "action", in.b.action, "where the consent form posts")
	flag.StringVar(&in.b.policy, "policy", "", "url of the privacy policy, omitted if empty")
	flag.StringVar(&in.b.policyBy, "policy-text", in.b.policyBy, "the policy link's text")
	flag.StringVar(&in.b.klass, "class", in.b.klass,
		"marker class; also how an existing banner is recognised")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "cookiebanner:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: cookiebanner [flags] [file.html]")
		os.Exit(2)
	}

	if err := in.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cookiebanner:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "injected=%d", in.injected)
	for reason, n := range in.skipped {
		fmt.Fprintf(os.Stderr, " %s=%d", reason, n)
	}
	fmt.Fprintln(os.Stderr)
}

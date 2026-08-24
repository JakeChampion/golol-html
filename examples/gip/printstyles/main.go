// Command printstyles makes a page printable: a print stylesheet in the head,
// and a hint before every h2 so a section does not start at the bottom of a page.
//
//	printstyles -stylesheet /print.css page.html
//	<link rel="stylesheet" href="/print.css" media="print">
//	...<h2 class="page-break-before">Section</h2>
//
// The hint is a class, not an inline style, and that is the whole design
// decision. An inline style cannot be overridden by the page's own stylesheet
// without !important, cannot be turned off for one section, and puts CSS in the
// markup where a Content-Security-Policy may refuse it. A class costs one rule in
// the stylesheet this program is already adding.
//
// The first h2 is deliberately skipped. A break before the first section pushes
// the article's own title onto a page of its own, which is worse than the problem
// being solved.
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

	lolhtml "github.com/JakeChampion/golol-html"
)

type styler struct {
	stylesheet string // linked with media=print
	class      string // added to each h2 after the first
	selector   string // which elements get the hint
	skipFirst  bool

	linked  int
	hinted  int
	skipped map[string]int
}

func (s *styler) note(reason string) {
	if s.skipped == nil {
		s.skipped = map[string]int{}
	}
	s.skipped[reason]++
}

func defaults() *styler {
	return &styler{class: "page-break-before", selector: "h2", skipFirst: true}
}

func (s *styler) validate() error {
	if s.stylesheet == "" && s.class == "" {
		return fmt.Errorf("nothing to do: give -stylesheet, or -class for the hints")
	}
	if s.stylesheet != "" {
		if _, err := url.Parse(s.stylesheet); err != nil {
			return fmt.Errorf("-stylesheet %q is not a url: %w", s.stylesheet, err)
		}
	}
	if s.class != "" && !validClass(s.class) {
		return fmt.Errorf("-class %q is not a CSS identifier: it goes into a class "+
			"attribute and into a selector", s.class)
	}
	if s.selector == "" {
		return fmt.Errorf("-selector cannot be empty")
	}
	return nil
}

func validClass(v string) bool {
	if v == "" || (v[0] >= '0' && v[0] <= '9') {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (s *styler) options() []lolhtml.Option {
	sawHead := false
	placed := s.stylesheet == ""
	haveSheet := false
	seen := 0

	opts := []lolhtml.Option{
		// A print stylesheet the page already links. Matched on the media
		// attribute, since the same file can be linked under any name.
		lolhtml.OnElement(`link[rel~="stylesheet"][media]`, func(e *lolhtml.Element) error {
			if strings.Contains(strings.ToLower(decoded(attr(e, "media"))), "print") {
				haveSheet = true
			}
			return nil
		}),
	}

	if s.class != "" {
		opts = append(opts, lolhtml.OnElement(s.selector, func(e *lolhtml.Element) error {
			seen++
			if s.skipFirst && seen == 1 {
				return nil
			}
			existing := decoded(attr(e, "class"))
			if hasClass(existing, s.class) {
				s.note("an element already carries the hint class")
				return nil
			}
			s.hinted++
			return e.SetAttribute("class", addClass(existing, s.class))
		}))
	}

	if s.stylesheet != "" {
		opts = append(opts,
			lolhtml.OnElement("head", func(e *lolhtml.Element) error {
				sawHead = true
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(end *lolhtml.EndTag) error {
					if placed {
						return nil
					}
					placed = true
					if haveSheet {
						s.note("the page already links a print stylesheet")
						return nil
					}
					s.linked++
					return end.Before(s.link(), lolhtml.HTML)
				})
			}),

			lolhtml.OnElement("body", func(e *lolhtml.Element) error {
				if sawHead || placed {
					return nil
				}
				placed = true
				if haveSheet {
					s.note("the page already links a print stylesheet")
					return nil
				}
				s.linked++
				return e.Before(s.link(), lolhtml.HTML)
			}),

			lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
				if !placed {
					s.note("no head and no body to link the stylesheet from")
				}
				return nil
			}))
	}

	return opts
}

// link is the stylesheet element. Assembled as markup, so the href is escaped
// for an attribute first.
func (s *styler) link() string {
	return `<link rel="stylesheet" href="` + lolhtml.EscapeAttribute(s.stylesheet) +
		`" media="print">`
}

// hasClass reports whether a class attribute already carries the token. Compared
// exactly, because a class is case-sensitive.
func hasClass(existing, want string) bool {
	for _, f := range strings.Fields(existing) {
		if f == want {
			return true
		}
	}
	return false
}

func addClass(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + " " + add
}

func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (s *styler) run(r io.Reader, w io.Writer) error {
	if err := s.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, s.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func styleString(in string, opts ...func(*styler)) (string, *styler, error) {
	s := defaults()
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run(strings.NewReader(in), &out)
	return out.String(), s, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (s *styler) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "linked=%d hinted=%d\n", s.linked, s.hinted)
	reasons := make([]string, 0, len(s.skipped))
	for r := range s.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, s.skipped[r])
	}
	return sb.String()
}

func main() {
	s := defaults()
	flag.StringVar(&s.stylesheet, "stylesheet", "", "stylesheet to link with media=print")
	flag.StringVar(&s.class, "class", s.class, "class added as the page-break hint")
	flag.StringVar(&s.selector, "selector", s.selector, "elements to hint before")
	flag.BoolVar(&s.skipFirst, "skip-first", s.skipFirst,
		"do not hint the first match, which would break before the first section")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "printstyles:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: printstyles [-stylesheet URL] [file.html]")
		os.Exit(2)
	}

	if err := s.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "printstyles:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.report())
}

// Command envbadge marks a page as belonging to a non-production environment: a
// visible badge in the corner and a prefix on the browser tab.
//
// The badge is a <div> with inline styles, no script and no stylesheet, because
// the point of it is to be visible on a page that may itself be broken. The tab
// prefix is the half that gets used: a reviewer with eight tabs open needs to see
// which one is staging without switching to it.
//
//	envbadge -env staging page.html
//	<title>[staging] Checkout</title>
//	...<div class="envbadge" style="...">staging</div>
//
// The title is where this program earns its keep, and not for the reason it looks
// like. A selector matches a tag name in any namespace, so "title" matches an SVG
// tooltip as well as the document title:
//
//	<svg><title>Sales for Q3</title></svg>
//
// and Element.NamespaceURI does not separate them, because it reports the
// namespace an element's children are parsed in, and SVG's <title> is an HTML
// integration point - so it says HTML, exactly like the real title. Prefixing
// what "title" matches puts "[staging]" inside every chart tooltip on the page.
//
// The selector "head title" would be exact, and it is not usable: <head> is
// optional in HTML, so a document beginning "<title>Checkout</title>" has a title
// and no head element, and the selector matches nothing. So this program matches
// "title" and tracks <svg> and <math> depth itself, which is the workaround the
// package documentation describes.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type badger struct {
	env     string // the environment name shown in the badge and the prefix
	corner  string // top-left, top-right, bottom-left, bottom-right
	colour  string // background colour of the badge
	noTitle bool   // leave the document title alone
	noBadge bool   // only prefix the title
	marker  string // class on the badge, and how a second pass recognises it

	badged        int
	titled        int
	foreignTitles int
	skipped       map[string]int
}

func (b *badger) note(reason string) {
	if b.skipped == nil {
		b.skipped = map[string]int{}
	}
	b.skipped[reason]++
}

func defaults() *badger {
	return &badger{env: "staging", corner: "bottom-right", colour: "#b30000",
		marker: "envbadge"}
}

var corners = map[string]string{
	"top-left":     "top:0;left:0",
	"top-right":    "top:0;right:0",
	"bottom-left":  "bottom:0;left:0",
	"bottom-right": "bottom:0;right:0",
}

func (b *badger) validate() error {
	if b.env == "" {
		return fmt.Errorf("-env cannot be empty: there is nothing to say")
	}
	if _, ok := corners[b.corner]; !ok {
		return fmt.Errorf("-corner %q is not one of top-left, top-right, "+
			"bottom-left, bottom-right", b.corner)
	}
	if !validClass(b.marker) {
		return fmt.Errorf("-marker %q is not a CSS identifier: it is used in a "+
			"selector as well as in markup", b.marker)
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

func (b *badger) options() []lolhtml.Option {
	// foreign counts how many <svg> or <math> elements are open. A counter
	// rather than a boolean because they nest, and a <title> inside a nested
	// <svg> is still a tooltip.
	//
	// This is the workaround for a selector that cannot say "not inside svg".
	// It has to be a counter kept by hand: neither the selector nor
	// NamespaceURI can answer the question.
	foreign := 0

	var present, titleDone bool

	opts := []lolhtml.Option{
		lolhtml.OnElement("svg, math", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				// A self-closing <svg/> opens nothing, so it must not be
				// counted - and it has no end tag to decrement on either.
				return nil
			}
			foreign++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				foreign--
				return nil
			})
		}),

		// The badge this program already added, on a previous pass.
		lolhtml.OnElement("."+b.marker, func(*lolhtml.Element) error {
			present = true
			return nil
		}),
	}

	if !b.noTitle {
		opts = append(opts, lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			switch {
			case foreign > 0:
				// An SVG or MathML <title> is a tooltip. Counted, because the
				// count is the evidence that the workaround is doing something.
				b.foreignTitles++
				return nil
			case titleDone:
				b.note("more than one document title")
				return nil
			case !e.CanHaveContent():
				return nil
			}
			titleDone = true
			b.titled++
			return e.Prepend(b.prefix(), lolhtml.Text)
		}))
	}

	if !b.noBadge {
		opts = append(opts,
			lolhtml.OnElement("body", func(e *lolhtml.Element) error {
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(end *lolhtml.EndTag) error {
					if present || b.badged > 0 {
						return nil
					}
					b.badged++
					return end.Before(b.markup(), lolhtml.HTML)
				})
			}),

			// </body> is optional in HTML, so the handler above may never run.
			// Appending at the end of the output instead is not a safe fallback:
			// if the response was cut off mid-construct the badge lands inside a
			// script or a comment and is not markup. See DocumentEnd.Append.
			lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
				switch {
				case present:
					b.note("a badge is already on the page")
				case b.badged == 0:
					b.note("no </body> to insert the badge before")
				}
				return nil
			}))
	}

	return opts
}

// prefix is inserted as Text, so the library escapes it and this program does
// not have to. It is deliberately not part of markup.
func (b *badger) prefix() string { return "[" + b.env + "] " }

// markup is the badge. It is assembled as a string, so every value goes through
// EscapeAttribute or EscapeText first: -env and -colour are operator input, and
// operator input is not more trustworthy than the document.
func (b *badger) markup() string {
	style := "position:fixed;z-index:2147483647;" + corners[b.corner] +
		";background:" + b.colour +
		";color:#fff;font:bold 12px/1 sans-serif;padding:4px 8px;" +
		"pointer-events:none;border-radius:0 0 0 4px"

	var sb strings.Builder
	sb.WriteString(`<div class="`)
	sb.WriteString(lolhtml.EscapeAttribute(b.marker))
	sb.WriteString(`" style="`)
	sb.WriteString(lolhtml.EscapeAttribute(style))
	sb.WriteString(`" role="status" aria-label="Environment">`)
	sb.WriteString(lolhtml.EscapeText(b.env))
	sb.WriteString(`</div>`)
	return sb.String()
}

func (b *badger) run(r io.Reader, w io.Writer) error {
	if err := b.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, b.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func badgeString(in string, opts ...func(*badger)) (string, *badger, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	var out bytes.Buffer
	err := b.run(strings.NewReader(in), &out)
	return out.String(), b, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (b *badger) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "badged=%d titled=%d svg-titles-left-alone=%d",
		b.badged, b.titled, b.foreignTitles)
	for reason, n := range b.skipped {
		fmt.Fprintf(&sb, " [%s]=%d", reason, n)
	}
	return sb.String()
}

func main() {
	b := defaults()
	flag.StringVar(&b.env, "env", b.env, "environment name")
	flag.StringVar(&b.corner, "corner", b.corner,
		"where the badge sits: top-left, top-right, bottom-left, bottom-right")
	flag.StringVar(&b.colour, "colour", b.colour, "badge background colour")
	flag.StringVar(&b.marker, "marker", b.marker,
		"class on the badge; also how an existing badge is recognised")
	flag.BoolVar(&b.noTitle, "no-title", false, "leave the document title alone")
	flag.BoolVar(&b.noBadge, "no-badge", false, "only prefix the title")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "envbadge:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: envbadge [-env name] [file.html]")
		os.Exit(2)
	}

	if err := b.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "envbadge:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, b.report())
}

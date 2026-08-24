// Command canonical enforces exactly one canonical link element.
//
//	canonical -href https://example.com/page page.html
//
// A page with several rel=canonical links is worse than a page with none: a
// crawler is entitled to take any of them, so the effect is a coin toss. The
// first in the head is rewritten to the wanted href and the rest are removed,
// and if the head had none, one is inserted.
//
// A canonical link outside the head is removed rather than counted. It is not
// honoured there, so it is not a competing declaration - but leaving it would
// mean the document still contained two, which is the thing being fixed.
//
// That distinction also settles an ordering problem. The insertion has to be
// decided at the end of the head, and a link in the body arrives after that; a
// first version inserted into the head and then rewrote the body's link too,
// producing exactly the two links it was meant to prevent.
//
// Two details of selector matching this leans on, both measured rather than
// assumed:
//
// The rel attribute's value is matched case-insensitively. That is not a general
// rule for attribute values - [name="foo"] does not match name="Foo" - but rel is
// on the HTML specification's list of attributes whose values are matched without
// regard to case, so link[rel="canonical"] does find rel="CANONICAL".
//
// It does not find rel="alternate canonical". A rel is a token list, and [rel=v]
// compares the whole value, so the selector for one token in a list is
// [rel~="canonical"] - which is on the list too, so it is also
// case-insensitive. This program uses the token form, because a link declaring
// itself both alternate and canonical is still a canonical link.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type enforcer struct {
	href  string // the canonical url to enforce
	keep  bool   // keep the first link's own href rather than replacing it
	noAdd bool   // never insert, only deduplicate

	rewrote        int
	removed        int
	inserted       int
	droppedOutside int
	skipped        map[string]int
}

func (e *enforcer) note(reason string) {
	if e.skipped == nil {
		e.skipped = map[string]int{}
	}
	e.skipped[reason]++
}

func defaults() *enforcer { return &enforcer{} }

func (e *enforcer) validate() error {
	if e.href == "" && !e.keep {
		return fmt.Errorf("-href is required unless -keep is given")
	}
	if e.href != "" {
		u, err := url.Parse(e.href)
		if err != nil {
			return fmt.Errorf("-href %q is not a url: %w", e.href, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("-href %q is not absolute: a canonical link has to be, "+
				"or a crawler resolves it against the page it happens to be on", e.href)
		}
	}
	return nil
}

// canonicalSelector matches a link declaring itself canonical, including one
// that declares itself something else as well. The token operator rather than
// equality: rel is a list, and "alternate canonical" is a canonical link.
const canonicalSelector = `link[rel~="canonical"]`

func (e *enforcer) options() []lolhtml.Option {
	// headRegion is true until the head ends - the explicit </head>, or the
	// start of <body> when the head was implied. Both are after every position a
	// canonical link could occupy in the head, which is what makes "rewrite the
	// first or insert one, never both" a single-pass decision.
	headRegion := true
	sawHead := false
	seen := 0

	return []lolhtml.Option{
		lolhtml.OnElement(canonicalSelector, func(el *lolhtml.Element) error {
			if !headRegion {
				// A canonical link outside the head is not honoured, so it is
				// noise rather than a competing declaration - and leaving it
				// would mean the document still had two.
				e.droppedOutside++
				el.Remove()
				return nil
			}
			seen++
			if seen > 1 {
				// Every one after the first in the head goes: two canonical
				// links are a coin toss.
				e.removed++
				el.Remove()
				return nil
			}
			if e.keep {
				return nil
			}
			e.rewrote++
			return el.SetAttribute("href", e.href)
		}),

		lolhtml.OnElement("head", func(el *lolhtml.Element) error {
			sawHead = true
			if !el.CanHaveContent() {
				return nil
			}
			return el.OnEndTag(func(end *lolhtml.EndTag) error {
				defer func() { headRegion = false }()
				return e.insertIfMissing(seen, func(markup string) error {
					return end.Before(markup, lolhtml.HTML)
				})
			})
		}),

		lolhtml.OnElement("body", func(el *lolhtml.Element) error {
			defer func() { headRegion = false }()
			if sawHead {
				return nil
			}
			return e.insertIfMissing(seen, func(markup string) error {
				return el.Before(markup, lolhtml.HTML)
			})
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			switch {
			case seen > 0:
				return nil
			case e.noAdd:
				e.note("no canonical link and -no-add was given")
			case e.inserted == 0 && !headRegion:
				e.note("no canonical link, and the head had already closed")
			case e.inserted == 0:
				e.note("no head and no body to insert the canonical link into")
			}
			return nil
		}),
	}
}

// insertIfMissing adds the link when the document had none. The caller supplies
// the insertion, because the position differs and the decision does not.
func (e *enforcer) insertIfMissing(seen int, insert func(string) error) error {
	if seen > 0 || e.noAdd || e.inserted > 0 {
		return nil
	}
	if e.keep && e.href == "" {
		e.note("-keep with no -href, so there was nothing to insert")
		return nil
	}
	e.inserted++
	return insert(e.markup())
}

// markup is the link. Assembled as a string because there is no element to hold,
// so the href is escaped for an attribute first.
func (e *enforcer) markup() string {
	return `<link rel="canonical" href="` + lolhtml.EscapeAttribute(e.href) + `">`
}

func attr(el *lolhtml.Element, name string) string {
	v, _ := el.Attribute(name)
	return v
}

func (e *enforcer) run(r io.Reader, w io.Writer) error {
	if err := e.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, e.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func enforceString(in string, opts ...func(*enforcer)) (string, *enforcer, error) {
	e := defaults()
	for _, o := range opts {
		o(e)
	}
	var out bytes.Buffer
	err := e.run(strings.NewReader(in), &out)
	return out.String(), e, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (e *enforcer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "rewrote=%d removed=%d inserted=%d dropped-outside-head=%d",
		e.rewrote, e.removed, e.inserted, e.droppedOutside)
	reasons := make([]string, 0, len(e.skipped))
	for r := range e.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, e.skipped[r])
	}
	return sb.String()
}

func main() {
	e := defaults()
	flag.StringVar(&e.href, "href", "", "the canonical url to enforce; must be absolute")
	flag.BoolVar(&e.keep, "keep", false,
		"keep the first canonical link's own href instead of replacing it")
	flag.BoolVar(&e.noAdd, "no-add", false,
		"only deduplicate; do not insert a canonical link into a page without one")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "canonical:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: canonical -href URL [file.html]")
		os.Exit(2)
	}

	if err := e.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "canonical:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, e.report())
}

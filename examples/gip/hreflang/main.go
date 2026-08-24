// Command hreflang injects alternate-language links from a locale table.
//
//	hreflang -alt en-GB=https://example.com/en/p -alt fr=https://example.com/fr/p \
//	         -self en-GB page.html
//
// Each entry becomes one <link rel="alternate" hreflang="..." href="..."> in the
// head. An entry already present with the same hreflang is rewritten rather than
// duplicated, because two alternates for one language are a contradiction rather
// than a repetition.
//
// The table is the point of the exercise: it is operator input, several values at
// a time, and every one of them lands in an attribute of markup this program
// assembles. So each is escaped for an attribute, and each hreflang is checked
// against what a language tag can be before it goes anywhere - a tag that is not
// a tag makes the whole set suspect rather than just its own link.
//
// A note on encodings, because a locale table is where non-ASCII arrives. The
// hreflang value is ASCII by definition, and an href can hold anything: if the
// document is served in a legacy encoding, a character that encoding cannot
// represent is emitted as a numeric character reference, which is decoded in an
// attribute and so comes out right. That is not true everywhere - inside a
// script or a style the reference stays literal - which is why this program puts
// nothing in either.
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

// An alternate is one row of the table.
type alternate struct {
	tag  string // the hreflang value
	href string
}

type injector struct {
	alts     []alternate
	self     string // the tag for this page, emitted first when present
	xdefault string // the tag to also emit as x-default

	inserted int
	rewrote  int
	removed  int
	skipped  map[string]int
}

func (in *injector) note(reason string) {
	if in.skipped == nil {
		in.skipped = map[string]int{}
	}
	in.skipped[reason]++
}

func defaults() *injector { return &injector{} }

func (in *injector) validate() error {
	if len(in.alts) == 0 {
		return fmt.Errorf("nothing to inject: give -alt at least once")
	}
	seen := map[string]bool{}
	for _, a := range in.alts {
		if !validLanguageTag(a.tag) {
			return fmt.Errorf("-alt %q: %q is not a language tag; it has to be "+
				"letters, digits and hyphens, starting with letters, or the exact "+
				"value x-default", a.tag+"="+a.href, a.tag)
		}
		key := strings.ToLower(a.tag)
		if seen[key] {
			return fmt.Errorf("-alt %q appears twice; two alternates for one "+
				"language contradict each other", a.tag)
		}
		seen[key] = true
		if err := checkAbsolute(a.href); err != nil {
			return fmt.Errorf("-alt %s: %w", a.tag, err)
		}
	}
	if in.self != "" && !seen[strings.ToLower(in.self)] {
		return fmt.Errorf("-self %q is not one of the -alt tags", in.self)
	}
	if in.xdefault != "" && !seen[strings.ToLower(in.xdefault)] {
		return fmt.Errorf("-x-default %q is not one of the -alt tags", in.xdefault)
	}
	return nil
}

func checkAbsolute(href string) error {
	u, err := url.Parse(href)
	if err != nil {
		return fmt.Errorf("%q is not a url: %w", href, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%q is not absolute; an alternate has to be, or a "+
			"crawler resolves it against whichever page it found it on", href)
	}
	return nil
}

// validLanguageTag is deliberately narrow: the shape of a BCP 47 tag rather than
// the registry. A value that is not this shape goes into an attribute a crawler
// reads, and refusing is better than emitting nonsense.
func validLanguageTag(s string) bool {
	if s == "x-default" {
		return true
	}
	if s == "" || len(s) > 35 {
		return false
	}
	for i, part := range strings.Split(s, "-") {
		if part == "" {
			return false
		}
		letters, digits := 0, 0
		for j := 0; j < len(part); j++ {
			c := part[j]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
				letters++
			case c >= '0' && c <= '9':
				digits++
			default:
				return false
			}
		}
		if i == 0 && (digits > 0 || letters < 2 || letters > 8) {
			// A primary language subtag is two to eight letters. The only
			// one-letter subtag that matters here is the x of x-default, which
			// is handled above as the exact string it has to be.
			return false
		}
	}
	return true
}

// ordered returns the table in the order the links are emitted: the page's own
// language first, then the rest as given, then x-default.
func (in *injector) ordered() []alternate {
	out := make([]alternate, 0, len(in.alts)+1)
	if in.self != "" {
		for _, a := range in.alts {
			if strings.EqualFold(a.tag, in.self) {
				out = append(out, a)
			}
		}
	}
	for _, a := range in.alts {
		if in.self != "" && strings.EqualFold(a.tag, in.self) {
			continue
		}
		out = append(out, a)
	}
	if in.xdefault != "" {
		for _, a := range in.alts {
			if strings.EqualFold(a.tag, in.xdefault) {
				out = append(out, alternate{tag: "x-default", href: a.href})
			}
		}
	}
	return out
}

const alternateSelector = `link[rel~="alternate"][hreflang]`

func (in *injector) options() []lolhtml.Option {
	// Same shape as the other head-injecting programs here: the head region ends
	// at </head>, or at the start of <body> when the head was implied, and both
	// are after every position an alternate could occupy in the head.
	headRegion := true
	sawHead := false
	done := map[string]bool{}

	return []lolhtml.Option{
		lolhtml.OnElement(alternateSelector, func(e *lolhtml.Element) error {
			tag := strings.ToLower(strings.TrimSpace(attr(e, "hreflang")))
			want, ok := in.lookup(tag)
			if !ok {
				return nil
			}
			if !headRegion {
				in.note("an alternate outside the head was left alone")
				return nil
			}
			if done[tag] {
				// Leaving it would mean the document still declared this
				// language twice, which is the contradiction being fixed.
				in.removed++
				e.Remove()
				return nil
			}
			done[tag] = true
			in.rewrote++
			return e.SetAttribute("href", want)
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				defer func() { headRegion = false }()
				return in.insertMissing(done, func(markup string) error {
					return end.Before(markup, lolhtml.HTML)
				})
			})
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			defer func() { headRegion = false }()
			if sawHead {
				return nil
			}
			return in.insertMissing(done, func(markup string) error {
				return e.Before(markup, lolhtml.HTML)
			})
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if in.inserted == 0 && in.rewrote == 0 {
				in.note("no head and no body to insert the alternates into")
			}
			return nil
		}),
	}
}

// lookup finds the wanted href for a language tag, comparing without regard to
// case because a language tag is case-insensitive.
func (in *injector) lookup(tag string) (string, bool) {
	for _, a := range in.ordered() {
		if strings.EqualFold(a.tag, tag) {
			return a.href, true
		}
	}
	return "", false
}

// insertMissing emits one link for every row the document did not already carry,
// in table order. One call rather than one per row: Before with several calls
// puts the newest closest to the unit, so the links would come out reversed.
func (in *injector) insertMissing(done map[string]bool, insert func(string) error) error {
	var sb strings.Builder
	n := 0
	for _, a := range in.ordered() {
		if done[strings.ToLower(a.tag)] {
			continue
		}
		done[strings.ToLower(a.tag)] = true
		sb.WriteString(markup(a))
		n++
	}
	if n == 0 {
		return nil
	}
	in.inserted += n
	return insert(sb.String())
}

// markup is one link. Both values are escaped for an attribute: the table is
// operator input, and this element does not exist yet, so nothing else will.
func markup(a alternate) string {
	return `<link rel="alternate" hreflang="` + lolhtml.EscapeAttribute(a.tag) +
		`" href="` + lolhtml.EscapeAttribute(a.href) + `">`
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
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

func injectString(input string, opts ...func(*injector)) (string, *injector, error) {
	in := defaults()
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

func (in *injector) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "inserted=%d rewrote=%d removed=%d", in.inserted, in.rewrote, in.removed)
	reasons := make([]string, 0, len(in.skipped))
	for r := range in.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, in.skipped[r])
	}
	return sb.String()
}

type altList struct{ v *[]alternate }

func (a altList) String() string {
	if a.v == nil {
		return ""
	}
	parts := make([]string, 0, len(*a.v))
	for _, x := range *a.v {
		parts = append(parts, x.tag+"="+x.href)
	}
	return strings.Join(parts, ",")
}

func (a altList) Set(v string) error {
	tag, href, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("expected tag=url, got %q", v)
	}
	*a.v = append(*a.v, alternate{tag: strings.TrimSpace(tag), href: strings.TrimSpace(href)})
	return nil
}

func main() {
	in := defaults()
	flag.Var(altList{&in.alts}, "alt", "tag=url alternate, repeatable")
	flag.StringVar(&in.self, "self", "", "the tag for this page, emitted first")
	flag.StringVar(&in.xdefault, "x-default", "",
		"also emit this tag's url as hreflang=x-default")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "hreflang:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: hreflang -alt tag=url [file.html]")
		os.Exit(2)
	}

	if err := in.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hreflang:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, in.report())
}

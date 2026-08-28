// Command preconnect adds resource hints for the third-party origins a page
// actually uses.
//
//	preconnect -self https://example.com/p page.html
//	<link rel="preconnect" href="https://fonts.example">
//	<link rel="dns-prefetch" href="https://fonts.example">
//
// The origins are in the body and the hints belong in the head, so this is two
// passes: the document is read to collect origins and rewritten to add the links.
// See the package documentation on insertion positions for why that cannot be one.
//
// Both rel values are emitted for each origin, and that is not redundancy.
// preconnect opens a connection and dns-prefetch only resolves the name; a
// browser that does not support the first still benefits from the second, and one
// that supports both ignores the weaker hint. Emitting only preconnect leaves
// older clients with nothing.
//
// The count is capped, and low. A preconnect is a held-open connection, so a page
// with forty third-party origins does not want forty hints - it wants the first
// few and a look at why there are forty. The limit is a flag and the excess is
// reported rather than silently dropped.
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

type hinter struct {
	self    string // the page's own url, whose origin is not third-party
	max     int    // most origins to hint
	dnsOnly bool   // emit dns-prefetch only, no preconnect

	// collected by the reading pass, in first-appearance order
	origins []string
	seen    map[string]bool

	added   int
	passes  int
	skipped map[string]int
}

func (h *hinter) note(reason string) {
	if h.skipped == nil {
		h.skipped = map[string]int{}
	}
	h.skipped[reason]++
}

func defaults() *hinter { return &hinter{max: 4, seen: map[string]bool{}} }

func (h *hinter) validate() error {
	if h.max < 1 {
		return fmt.Errorf("-max %d leaves no room for a hint", h.max)
	}
	if h.self == "" {
		return fmt.Errorf("-self is required: without the page's own origin every " +
			"origin looks third-party, including its own")
	}
	u, err := url.Parse(h.self)
	if err != nil {
		return fmt.Errorf("-self %q is not a url: %w", h.self, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("-self %q is not absolute", h.self)
	}
	return nil
}

// urlAttributes are the attributes that carry a URL, by element. Rather than
// scanning every attribute for something that looks like one: a src on a script
// is a fetch and a data-src is whatever a framework decided it is.
var urlAttributes = map[string][]string{
	"script": {"src"},
	"link":   {"href"},
	"img":    {"src", "srcset"},
	"source": {"src", "srcset"},
	"video":  {"src", "poster"},
	"audio":  {"src"},
	"iframe": {"src"},
	"embed":  {"src"},
	"object": {"data"},
	"track":  {"src"},
	"use":    {"href"},
	"image":  {"href"},
}

func (h *hinter) readPass(doc []byte) error {
	h.passes++
	self, err := url.Parse(h.self)
	if err != nil {
		return err
	}

	_, err = lolhtml.Rewrite(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			names, ok := urlAttributes[strings.ToLower(e.TagName())]
			if !ok {
				return nil
			}
			for _, name := range names {
				raw, present := e.Attribute(name)
				if !present {
					continue
				}
				for _, candidate := range splitURLs(name, decoded(raw)) {
					h.collect(self, candidate)
				}
			}
			return nil
		}))
	return err
}

// splitURLs turns one attribute value into the URLs it carries. srcset is a
// comma-separated list of url-and-descriptor pairs; everything else is one URL.
func splitURLs(attribute, value string) []string {
	if attribute != "srcset" {
		return []string{strings.TrimSpace(value)}
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if fields := strings.Fields(part); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

// collect records the origin of one URL if it is third-party.
func (h *hinter) collect(self *url.URL, raw string) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		h.note("an unparseable url was ignored")
		return
	}
	if u.Host == "" {
		return // relative: same origin by definition
	}
	// A scheme-relative url inherits the page's scheme.
	scheme := u.Scheme
	if scheme == "" {
		scheme = self.Scheme
	}
	if scheme != "http" && scheme != "https" {
		h.note("a non-http origin was ignored")
		return
	}
	origin := scheme + "://" + u.Host
	if strings.EqualFold(u.Host, self.Host) {
		return
	}
	if h.seen[origin] {
		return
	}
	h.seen[origin] = true
	h.origins = append(h.origins, origin)
}

// hinted is the origins to emit, in first-appearance order. First appearance
// rather than frequency: the first origin a page reaches is the one whose
// connection is wanted soonest.
//
// Pure, deliberately. An earlier version reported the over-cap origins from here,
// and both markup and report call it, so the note was counted twice.
func (h *hinter) hinted() []string {
	if len(h.origins) <= h.max {
		return h.origins
	}
	return h.origins[:h.max]
}

func (h *hinter) markup() string {
	var sb strings.Builder
	for _, origin := range h.hinted() {
		escaped := lolhtml.EscapeAttribute(origin)
		if !h.dnsOnly {
			sb.WriteString(`<link rel="preconnect" href="` + escaped + `">`)
		}
		sb.WriteString(`<link rel="dns-prefetch" href="` + escaped + `">`)
		h.added++
	}
	return sb.String()
}

// existing records the origins a page already hints, so a second run adds
// nothing and a page that hints its own origins is left alone.
func (h *hinter) readExisting(doc []byte) error {
	_, err := lolhtml.Rewrite(doc,
		lolhtml.OnElement(`link[rel~="preconnect"], link[rel~="dns-prefetch"]`,
			func(e *lolhtml.Element) error {
				href := decoded(attr(e, "href"))
				if u, err := url.Parse(href); err == nil && u.Host != "" {
					scheme := u.Scheme
					if scheme == "" {
						scheme = "https"
					}
					h.seen[scheme+"://"+u.Host] = true
				}
				return nil
			}))
	return err
}

func (h *hinter) writePass(doc []byte, w io.Writer) error {
	h.passes++
	if over := len(h.origins) - h.max; over > 0 {
		h.note(fmt.Sprintf("%d origins beyond -max=%d were not hinted", over, h.max))
	}
	markup := h.markup()

	placed := markup == ""

	out, err := lolhtml.NewWriter(w,
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if end.Name() != "head" {
					// </head> is optional and nothing synthesises one.
					// Left out, the head is closed by <body>, and this
					// callback runs against whatever tag did close it -
					// </body> or </html>. Writing there puts the hints
					// after the whole document has been parsed, which is
					// after every resource they were meant to warm up:
					// the exact opposite of "the origins are in the body
					// and the hints belong in the head". The <body>
					// handler below covers that shape, and it runs first.
					return nil
				}
				if placed {
					return nil
				}
				placed = true
				return end.Before(markup, lolhtml.HTML)
			})
		}),
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			// Whether the hints have gone in, not whether a head was seen: a
			// document with a head but no </head> arrives here with the head
			// still open, and <body> is where that head ends. Inserting
			// before it lands in the head a parser builds.
			if placed {
				return nil
			}
			placed = true
			return e.Before(markup, lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !placed {
				// markup() counted the links as it built them, and none of
				// them went anywhere - so the count is undone rather than
				// reported as work done.
				h.added = 0
				h.note("no head and no body to put the hints in")
			}
			return nil
		}))
	if err != nil {
		return err
	}
	if _, err := out.Write(doc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (h *hinter) run(r io.Reader, w io.Writer) error {
	if err := h.validate(); err != nil {
		return err
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	// Existing hints first, so an origin the page already hints is not collected
	// as one to add.
	if err := h.readExisting(doc); err != nil {
		return err
	}
	if err := h.readPass(doc); err != nil {
		return err
	}
	return h.writePass(doc, w)
}

func hintString(in string, opts ...func(*hinter)) (string, *hinter, error) {
	h := defaults()
	h.self = "https://example.com/p"
	for _, o := range opts {
		o(h)
	}
	var out bytes.Buffer
	err := h.run(strings.NewReader(in), &out)
	return out.String(), h, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (h *hinter) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "passes=%d links=%d origins=%s\n", h.passes, h.added,
		strings.Join(h.hinted(), " "))
	reasons := make([]string, 0, len(h.skipped))
	for r := range h.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, h.skipped[r])
	}
	return sb.String()
}

func main() {
	h := defaults()
	flag.StringVar(&h.self, "self", "", "the page's own url, to tell first-party from third")
	flag.IntVar(&h.max, "max", h.max, "most origins to hint")
	flag.BoolVar(&h.dnsOnly, "dns-only", false,
		"emit dns-prefetch only, without holding connections open")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "preconnect:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: preconnect -self URL [file.html]")
		os.Exit(2)
	}

	if err := h.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "preconnect:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, h.report())
}

// Command untrack removes tracking from an HTML document as it streams past:
// tracking parameters from every URL, and tracking pixels from the markup.
//
//	untrack < page.html > clean.html
//	untrack -pixels=false -params utm_source,utm_medium < page.html
//
// It reports what it removed on stderr, and exits 0 whether or not it found
// anything, because finding nothing is a normal outcome rather than a failure.
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

// defaultParams are the query parameters that identify a campaign or a click
// rather than a resource. Removing one never changes which document a URL
// addresses, which is what makes this safe to do blind.
var defaultParams = []string{
	"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content",
	"utm_id", "utm_name", "utm_cid", "utm_reader", "utm_social",
	"fbclid", "gclid", "gclsrc", "dclid", "wbraid", "gbraid", "msclkid",
	"mc_cid", "mc_eid", "igshid", "igsh", "twclid", "ttclid", "li_fat_id",
	"yclid", "_openstat", "vero_id", "vero_conv", "s_cid", "ss_source",
	"oly_anon_id", "oly_enc_id", "hsa_cam", "_hsenc", "_hsmi", "__hssc",
	"__hstc", "hsCtaTracking", "wickedid", "ref_src", "ref_url",
}

// pixelHosts are hosts whose images exist to be requested rather than seen.
var pixelHosts = []string{
	"facebook.com/tr", "google-analytics.com", "googletagmanager.com",
	"doubleclick.net", "scorecardresearch.com", "quantserve.com",
	"hotjar.com", "mixpanel.com", "segment.io", "matomo.cloud",
}

func main() {
	pixels := flag.Bool("pixels", true, "remove tracking pixels as well as parameters")
	params := flag.String("params", "", "comma-separated parameter list, replacing the default set")
	mark := flag.Bool("mark", false, "leave a comment where each pixel was removed")
	quiet := flag.Bool("quiet", false, "do not append the report comment")
	flag.Parse()

	s := newStripper()
	s.removePixels, s.mark, s.quiet = *pixels, *mark, *quiet

	if *params != "" {
		s.params = map[string]bool{}
		for _, p := range strings.Split(*params, ",") {
			if p = strings.TrimSpace(p); p != "" {
				s.params[p] = true
			}
		}
	}

	if err := s.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "untrack:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.summary())
}

type stripper struct {
	params       map[string]bool
	removePixels bool
	mark         bool
	quiet        bool

	// Counts, keyed by what was removed, so the report says which tracker a
	// page carried rather than only how many.
	strippedParams map[string]int
	removedPixels  []string
	commentHits    int
	urlsSeen       int
	urlsChanged    int
}

func (s *stripper) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, s.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// urlAttrs is the same set absolutise walks, minus the ones that cannot carry a
// query worth stripping.
var urlAttrs = []struct{ selector, attr string }{
	{"a[href], area[href], link[href]", "href"},
	{"img[src], script[src], iframe[src], embed[src], source[src], video[src], audio[src]", "src"},
	{"form[action]", "action"},
	{"button[formaction], input[formaction]", "formaction"},
	{"video[poster]", "poster"},
}

func (s *stripper) options() []lolhtml.Option {
	opts := make([]lolhtml.Option, 0, len(urlAttrs)+3)

	// Pixel removal is registered first, so an element that is going away is
	// not also rewritten. Handlers of one kind run in registration order.
	if s.removePixels {
		opts = append(opts, lolhtml.OnElement("img[src], iframe[src]", func(e *lolhtml.Element) error {
			src, ok := e.Attribute("src")
			if !ok || !isPixel(src) {
				return nil
			}
			s.removedPixels = append(s.removedPixels, src)
			e.Remove()
			if !s.mark {
				return nil
			}
			// Text, not HTML: the URL came from the document and must not be
			// able to become markup. The marker is inserted after the element
			// rather than replacing it, so removal still happens.
			return e.After("removed tracker: "+src, lolhtml.Text)
		}))
	}

	for _, ua := range urlAttrs {
		ua := ua
		opts = append(opts, lolhtml.OnElement(ua.selector, func(e *lolhtml.Element) error {
			// An element another handler has already removed is not going to
			// be emitted, so rewriting its URL is wasted work and counting it
			// would overstate what this program did. Handler invocation is not
			// suppressed inside removed content; only the output is.
			if e.IsRemoved() {
				return nil
			}
			raw, ok := e.Attribute(ua.attr)
			if !ok || raw == "" {
				return nil
			}
			s.urlsSeen++
			out, n := s.clean(raw)
			if n == 0 {
				return nil
			}
			s.urlsChanged++
			return e.SetAttribute(ua.attr, out)
		}))
	}

	opts = append(opts,
		// A tracker hiding in a comment cannot be rewritten, because a comment
		// is not parsed. Worth counting rather than ignoring.
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if isPixel(c.Text()) {
				s.commentHits++
			}
			return nil
		}),

		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if s.quiet || (len(s.strippedParams) == 0 && len(s.removedPixels) == 0) {
				return nil
			}
			// HTML: the comment delimiters have to survive as markup, so what
			// goes between them must not. The stripped parameter names come
			// from the document's own URLs, so one of them containing "-->"
			// would end this comment early and leave the rest as live markup.
			text, err := commentData(" untrack: " + s.oneLine() + " ")
			if err != nil {
				return err
			}
			return d.Append("\n<!--"+text+"-->\n", lolhtml.HTML)
		}),
	)

	return opts
}

// clean removes tracking parameters from one URL, returning how many it took
// out. A URL it cannot parse is left exactly as it was: guessing at the query
// string of something malformed is how a rewriter breaks a page.
func (s *stripper) clean(raw string) (string, int) {
	// Attribute values are raw source, so &amp; is five characters here and one
	// & to whoever parses the result. Whichever form the document used is the
	// form it gets back, because SetAttribute escapes neither.
	sep := "&"
	if strings.Contains(raw, "&amp;") {
		sep = "&amp;"
	}

	u, err := url.Parse(strings.ReplaceAll(raw, "&amp;", "&"))
	if err != nil || u.RawQuery == "" {
		return raw, 0
	}

	kept := make([]string, 0, 8)
	removed := 0
	for _, pair := range strings.Split(u.RawQuery, "&") {
		name := pair
		if i := strings.IndexByte(pair, '='); i >= 0 {
			name = pair[:i]
		}
		if s.match(name) {
			removed++
			if s.strippedParams == nil {
				s.strippedParams = map[string]int{}
			}
			s.strippedParams[name]++
			continue
		}
		kept = append(kept, pair)
	}
	if removed == 0 {
		return raw, 0
	}

	u.RawQuery = strings.Join(kept, "&")
	out := u.String()
	if u.RawQuery == "" {
		// Drop the "?" too, rather than leaving a bare one behind.
		out = strings.TrimSuffix(out, "?")
	}
	return strings.ReplaceAll(out, "&", sep), removed
}

// match allows a trailing * so that a caller can strip a whole family.
func (s *stripper) match(name string) bool {
	lower := strings.ToLower(name)
	if s.params[lower] {
		return true
	}
	for p := range s.params {
		if strings.HasSuffix(p, "*") && strings.HasPrefix(lower, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

func isPixel(s string) bool {
	lower := strings.ToLower(s)
	for _, h := range pixelHosts {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

func (s *stripper) oneLine() string {
	names := make([]string, 0, len(s.strippedParams))
	for n, c := range s.strippedParams {
		names = append(names, fmt.Sprintf("%s=%d", n, c))
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "urls=%d changed=%d pixels=%d", s.urlsSeen, s.urlsChanged, len(s.removedPixels))
	if len(names) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(names, " "))
	}
	return sb.String()
}

func (s *stripper) summary() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", s.oneLine())
	for _, p := range s.removedPixels {
		fmt.Fprintf(&sb, "pixel: %s\n", p)
	}
	if s.commentHits > 0 {
		fmt.Fprintf(&sb, "note: %d comment(s) mention a tracker, which a rewriter cannot reach\n", s.commentHits)
	}
	return sb.String()
}

// newStripper builds a stripper with the default parameter set. The report
// comment is off by default so that a caller comparing output byte for byte is
// not comparing against a running total.
func newStripper() *stripper {
	s := &stripper{removePixels: true, quiet: true, params: map[string]bool{}}
	for _, p := range defaultParams {
		s.params[p] = true
	}
	return s
}

func stripString(in string, opts ...func(*stripper)) (string, *stripper, error) {
	s := newStripper()
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run(strings.NewReader(in), &out)
	return out.String(), s, err
}

// commentData makes text safe to sit between comment delimiters the caller
// wrote itself.
//
// [lolhtml.CheckComment] is the library's guard for exactly this position, and
// it reports rather than repairs - deliberately, because the repair is a choice
// about meaning. Nothing inside a comment is a character reference, so there is
// no escaping available: text a comment cannot hold has to be changed instead.
// "- -" for "--" is the replacement its own message suggests, and it keeps a
// report readable, which is the point of a report.
//
// The check runs after the replacement rather than instead of it, as an
// assertion: if a later edit adds a field this does not neutralise, it comes
// back as an error here instead of quietly reopening the hole.
func commentData(text string) (string, error) {
	safe := strings.ReplaceAll(text, "--", "- -")
	if err := lolhtml.CheckComment(safe); err != nil {
		return "", err
	}
	return safe, nil
}

// Command imgcdn points a page's images at an image CDN, with a width and a format.
//
//	<img src="/p/hero.jpg">
//	  ->  <img src="https://cdn/?url=%2Fp%2Fhero.jpg&amp;w=800&amp;fm=webp">
//
// It rewrites src on img, srcset on img and source, and the href of an image
// preload, which is the set of places a page names an image file. What it will not
// do is guess: a URL it cannot read exactly is left alone and counted, because an
// image that fails to load is worse than one that is a little too big.
//
// # Two spellings of the same URL
//
// An attribute value is reported as the document spelled it, references and all, so
// "?a=1&amp;b=2" and "?a=1&b=2" are the same URL written two ways. This program has
// to know which characters the URL really contains before it can percent-encode it
// into a query parameter, so it decodes every reference first, the way a browser
// does before it fetches: "&nbsp;" is the one character U+00A0 and goes into the
// query as "%C2%A0", not as six characters of URL, and "&#47;&#47;host/x" is the
// protocol-relative URL it is to a browser rather than a path to proxy. Writing
// the new value back, the separators go in as "&amp;", which is the spelling the
// library leaves alone: SetAttribute escapes the double quote and nothing else.
//
// # srcset is a list with commas inside its members
//
// A srcset entry is a URL followed by an optional descriptor, entries separated by
// commas - and a URL may contain a comma. The specification's parse is therefore
// not "split on comma": take characters up to whitespace, and a trailing comma on
// that run is the separator. So "/a.jpg,/b.jpg" is one candidate whose URL has a
// comma in it, and "/a.jpg, /b.jpg" is two. This program follows that, and a member
// it will not rewrite - one already off-site - leaves the whole attribute alone:
// half a rewritten list is a list of images that do not match each other.
//
// # What it costs to match the biggest tag on the page
//
// A srcset is often the longest attribute in a document, and this program's handlers
// match the elements that carry it. That is what decides the memory floor: measured,
// MaxMemory has to cover the largest token a handler is given when that token
// straddles two writes, and nothing else - a 2012-byte <img> tag needs 2012 bytes of
// limit under 64-byte writes, needs 5 in a single Write, and needs 5 either way if no
// handler matches it. So adding this rewrite to a pipeline can raise the limit the
// same pipeline needed before, without the document having changed. See
// [lolhtml.MemorySettings] and differential/memory_test.go.
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Options are the decisions a caller gets to make.
type Options struct {
	// Base is the CDN endpoint. Required.
	Base string
	// Param is the query parameter the original URL goes in.
	Param string
	// Width and Format are the transformations, left out when empty.
	Width  int
	Format string
	// Skip is a prefix that means "already on the CDN", so a second run is a
	// no-operation.
	Skip string
}

// Result is what happened.
type Result struct {
	Src      int // src attributes rewritten
	Srcset   int // srcset attributes rewritten
	Preload  int // preload hrefs rewritten
	Already  int // URLs already pointing at the CDN
	Refused  int // URLs this program would not read exactly
	Absolute int // URLs on another host, left alone
}

func (r Result) String() string {
	return fmt.Sprintf("imgcdn: rewrote %d src, %d srcset, %d preload; %d already on the cdn, %d refused, %d off-site",
		r.Src, r.Srcset, r.Preload, r.Already, r.Refused, r.Absolute)
}

// OK reports whether every image URL was accounted for.
func (r Result) OK() bool { return r.Refused == 0 }

type rewriter struct {
	opts Options
	res  Result
}

// cdn builds the CDN URL for one image URL, or reports that it will not.
func (rw *rewriter) cdn(raw string) (string, bool) {
	src, ok := decodeAmpersands(raw)
	if !ok {
		rw.res.Refused++
		return "", false
	}
	if rw.opts.Skip != "" && strings.HasPrefix(src, rw.opts.Skip) {
		rw.res.Already++
		return "", false
	}
	// A data: URL is already in the page and an off-site URL is somebody else's
	// bandwidth, not this rewrite's business.
	if strings.HasPrefix(strings.ToLower(src), "data:") {
		rw.res.Absolute++
		return "", false
	}
	if u, err := url.Parse(src); err == nil && u.Host != "" {
		rw.res.Absolute++
		return "", false
	}
	q := rw.opts.Param + "=" + url.QueryEscape(src)
	if rw.opts.Width > 0 {
		q += "&amp;w=" + strconv.Itoa(rw.opts.Width)
	}
	if rw.opts.Format != "" {
		q += "&amp;fm=" + url.QueryEscape(rw.opts.Format)
	}
	sep := "?"
	if strings.Contains(rw.opts.Base, "?") {
		sep = "&amp;"
	}
	return rw.opts.Base + sep + q, true
}

func (rw *rewriter) src(e *lolhtml.Element) error {
	raw, ok := e.Attribute("src")
	if !ok {
		return nil
	}
	next, ok := rw.cdn(raw)
	if !ok {
		return nil
	}
	rw.res.Src++
	return e.SetAttribute("src", next)
}

func (rw *rewriter) srcset(e *lolhtml.Element) error {
	raw, ok := e.Attribute("srcset")
	if !ok {
		return nil
	}
	members, ok := parseSrcset(raw)
	if !ok {
		rw.res.Refused++
		return nil
	}
	var out []string
	for _, m := range members {
		next, ok := rw.cdn(m.url)
		if !ok {
			return nil // the attribute is left exactly as it was
		}
		if m.descriptor != "" {
			next += " " + m.descriptor
		}
		out = append(out, next)
	}
	if len(out) == 0 {
		return nil
	}
	rw.res.Srcset++
	return e.SetAttribute("srcset", strings.Join(out, ", "))
}

func (rw *rewriter) preload(e *lolhtml.Element) error {
	if as, _ := e.Attribute("as"); !strings.EqualFold(as, "image") {
		return nil
	}
	raw, ok := e.Attribute("href")
	if !ok {
		return nil
	}
	next, ok := rw.cdn(raw)
	if !ok {
		return nil
	}
	rw.res.Preload++
	return e.SetAttribute("href", next)
}

func (rw *rewriter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("img[src]", rw.src),
		lolhtml.OnElement("img[srcset],source[srcset]", rw.srcset),
		lolhtml.OnElement("link[rel~=preload][href]", rw.preload),
	}
}

// member is one entry of a srcset.
type member struct{ url, descriptor string }

// parseSrcset follows the specification's parse rather than splitting on commas,
// because a URL may contain one. It reports false for anything it cannot read.
func parseSrcset(s string) ([]member, bool) {
	var out []member
	i := 0
	skip := func(set string) {
		for i < len(s) && strings.ContainsRune(set, rune(s[i])) {
			i++
		}
	}
	const space = " \t\n\f\r"
	for {
		skip(space + ",")
		if i >= len(s) {
			if len(out) == 0 {
				return nil, false
			}
			return out, true
		}
		start := i
		for i < len(s) && !strings.ContainsRune(space, rune(s[i])) {
			i++
		}
		raw := s[start:i]
		trailing := strings.HasSuffix(raw, ",")
		raw = strings.TrimRight(raw, ",")
		if raw == "" {
			return nil, false
		}
		m := member{url: raw}
		if !trailing {
			skip(space)
			start = i
			for i < len(s) && s[i] != ',' {
				i++
			}
			m.descriptor = strings.TrimRight(strings.TrimSpace(s[start:i]), ",")
			if strings.ContainsAny(m.descriptor, "\"'<>") {
				return nil, false
			}
		}
		out = append(out, m)
	}
}

// decodeAmpersands turns the attribute's source text into the URL a browser
// would fetch: every character reference decoded, not only the ones that spell
// "&". It used to read those alone and refuse any other reference as one it
// could not encode - but "&nbsp;" is one character, U+00A0, and QueryEscape
// encodes it as a browser would, "%C2%A0"; refusing it left the image off the
// CDN and the run reported as not OK for a URL that was perfectly readable.
// The bool is kept for the caller's shape; nothing is refused here now.
func decodeAmpersands(s string) (string, bool) {
	return html.UnescapeString(s), true
}

// continuesName reports whether s starts with a character that stops a
// semicolon-less named reference from being one: "=" or an ASCII alphanumeric.
func continuesName(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '=' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '#' || r == 'x') {
			return false
		}
	}
	return s != ""
}

// Rewrite copies src to dst, pointing image URLs at the CDN.
func Rewrite(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Param == "" {
		opts.Param = "url"
	}
	if opts.Skip == "" {
		opts.Skip = opts.Base
	}
	rw := &rewriter{opts: opts}
	w, err := lolhtml.NewWriter(dst, rw.options()...)
	if err != nil {
		return rw.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return rw.res, err
	}
	if err := w.Close(); err != nil {
		return rw.res, err
	}
	return rw.res, nil
}

func main() {
	var opts Options
	flag.StringVar(&opts.Base, "base", "", "CDN endpoint (required)")
	flag.StringVar(&opts.Param, "param", "url", "query parameter for the original URL")
	flag.IntVar(&opts.Width, "w", 0, "width parameter, omitted when zero")
	flag.StringVar(&opts.Format, "fm", "", "format parameter, omitted when empty")
	flag.Parse()

	if opts.Base == "" {
		fmt.Fprintln(os.Stderr, "imgcdn: -base is required")
		os.Exit(2)
	}
	res, err := Rewrite(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "imgcdn:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

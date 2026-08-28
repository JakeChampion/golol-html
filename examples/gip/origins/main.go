// Command origins reports every origin a page would contact, and what asked for it.
//
//	$ origins -page https://site.example/ < page.html
//	origin                        first  requests  triggers
//	https://site.example          yes    12        img script link
//	https://cdn.example           no     4         script link[preconnect]
//	https://analytics.example     no     1         script
//
// It writes no document. A reporting tool that also copies its input has two jobs and
// only one of them can fail usefully, so the destination is io.Discard and the report
// goes to stdout: the exit status is about the page, not about the rewrite.
//
// # What counts as contacting an origin
//
// A URL in the document is not the same thing as a request. An href on a link is a
// request only if somebody clicks, a preconnect is a request with no response, and a
// srcset is one request out of several candidates. So each origin is reported with the
// triggers that named it, and the triggers are grouped by what they mean:
//
//	fetched     img, script, link[stylesheet], iframe, video, audio, source, track,
//	            object, embed, and url() in CSS: the browser asks for these
//	hinted      link[preconnect], link[dns-prefetch], link[preload], link[prefetch]
//	navigated   a[href], area[href], form[action]: only if somebody acts
//
// The distinction is the point of the report. An origin that only ever appears as a
// navigation is not a third party the page loaded, and an origin that appears as a
// script is one that can do anything.
//
// # First party needs the page's URL
//
// A document does not contain its own address, so -page supplies it, and a <base
// href> in the document overrides it for relative URLs the way a browser would. Every
// relative URL therefore resolves to the first-party origin, which is what makes the
// third-party list the interesting one. Without -page, relative URLs are reported as
// "(relative)" rather than guessed at.
//
// # The shape of a report-only rewrite
//
// Measured rather than assumed: one handler with a selector list naming the elements
// that can hold a URL, rather than a handler on "*" that asks every element about
// every attribute. A selector list costs what one selector costs at NewWriter -
// registrations are per handler, not per clause - while a wide selector pays the
// per-element cost for every element in the document, and the gap grows with the page.
// See reportshape_test.go and [lolhtml.OnElement].
package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what a URL means for whether the page contacts the origin.
type Kind int

const (
	// Fetched is a request the browser makes on its own.
	Fetched Kind = iota
	// Hinted is a connection the page asked for in advance.
	Hinted
	// Navigated is a request that happens only if someone acts.
	Navigated
)

func (k Kind) String() string {
	switch k {
	case Fetched:
		return "fetched"
	case Hinted:
		return "hinted"
	}
	return "navigated"
}

// source is one place a URL can appear: an element, an attribute and what it means.
type source struct {
	attr string
	kind Kind
}

// Sources are the places this program looks, by element name.
var Sources = map[string][]source{
	"img":        {{"src", Fetched}, {"srcset", Fetched}},
	"script":     {{"src", Fetched}},
	"iframe":     {{"src", Fetched}},
	"embed":      {{"src", Fetched}},
	"object":     {{"data", Fetched}},
	"source":     {{"src", Fetched}, {"srcset", Fetched}},
	"track":      {{"src", Fetched}},
	"audio":      {{"src", Fetched}},
	"video":      {{"src", Fetched}, {"poster", Fetched}},
	"input":      {{"src", Fetched}, {"formaction", Navigated}},
	"button":     {{"formaction", Navigated}},
	"a":          {{"href", Navigated}},
	"area":       {{"href", Navigated}},
	"form":       {{"action", Navigated}},
	"use":        {{"xlink:href", Fetched}},
	"image":      {{"xlink:href", Fetched}},
	"blockquote": {{"cite", Navigated}},
	"q":          {{"cite", Navigated}},
}

// LinkRels maps a link element's rel to what the URL means. A rel this program does
// not know is reported as fetched, which is the cautious answer.
var LinkRels = map[string]Kind{
	"stylesheet": Fetched, "icon": Fetched, "apple-touch-icon": Fetched,
	"manifest": Fetched, "modulepreload": Fetched,
	"preconnect": Hinted, "dns-prefetch": Hinted, "preload": Hinted, "prefetch": Hinted,
	"canonical": Navigated, "alternate": Navigated, "author": Navigated,
	"help": Navigated, "license": Navigated, "next": Navigated, "prev": Navigated,
	"search": Navigated, "me": Navigated,
}

// Elements is the selector list, built once. One handler for all of it: see the file
// comment.
const Elements = `base,img,script[src],iframe[src],embed[src],object[data],source,` +
	`track[src],audio[src],video,input,button[formaction],a[href],area[href],` +
	`form[action],use[xlink\:href],image[xlink\:href],blockquote[cite],q[cite],` +
	`link[href],[style]`

// Origin is what the report prints one line of.
type Origin struct {
	Name     string // scheme://host[:port], or a pseudo-origin in brackets
	First    bool   // the page's own origin
	Requests int
	Triggers map[string]int // "img", "link[preconnect]", "css", …
	Kinds    map[Kind]int
}

// Report is the whole answer.
type Report struct {
	Page    string
	Base    string
	Origins []Origin
}

// ThirdParty returns the origins that are not the page's own and that the browser
// would fetch from without anybody acting.
func (r Report) ThirdParty() []Origin {
	var out []Origin
	for _, o := range r.Origins {
		if !o.First && o.Kinds[Fetched] > 0 {
			out = append(out, o)
		}
	}
	return out
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-40s %-6s %-9s %s\n", "origin", "first", "requests", "triggers")
	for _, o := range r.Origins {
		first := "no"
		if o.First {
			first = "yes"
		}
		fmt.Fprintf(&b, "%-40s %-6s %-9d %s\n", o.Name, first, o.Requests, triggerList(o.Triggers))
	}
	return b.String()
}

func triggerList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s(%d)", k, m[k]))
	}
	return strings.Join(out, " ")
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Page is the document's own URL. Without it, relative URLs are reported as
	// "(relative)" rather than attributed to a first-party origin.
	Page string
}

type reporter struct {
	opts    Options
	page    *url.URL
	base    *url.URL
	origins map[string]*Origin
	order   []string
	css     strings.Builder
}

func (r *reporter) note(rawURL, trigger string, kind Kind) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return
	}
	name := r.originOf(raw)
	if name == "" {
		return
	}
	o := r.origins[name]
	if o == nil {
		o = &Origin{Name: name, Triggers: map[string]int{}, Kinds: map[Kind]int{}}
		if r.page != nil && name == originString(r.page) {
			o.First = true
		}
		r.origins[name] = o
		r.order = append(r.order, name)
	}
	o.Requests++
	o.Triggers[trigger]++
	o.Kinds[kind]++
}

// originOf reduces a URL to its origin, or to a pseudo-origin for the shapes that do
// not have one. A relative URL with a known base is first-party; without one it is
// reported as relative rather than guessed at.
func (r *reporter) originOf(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "data:"):
		return "(data)"
	case strings.HasPrefix(lower, "blob:"):
		return "(blob)"
	case strings.HasPrefix(lower, "javascript:"):
		return "(javascript)"
	case strings.HasPrefix(lower, "mailto:"), strings.HasPrefix(lower, "tel:"):
		return "(" + lower[:strings.Index(lower, ":")] + ")"
	case strings.HasPrefix(lower, "about:"):
		return "(about)"
	}
	u, err := url.Parse(strings.ReplaceAll(raw, "&amp;", "&"))
	if err != nil {
		return "(unparsed)"
	}
	base := r.base
	if base == nil {
		base = r.page
	}
	if u.Host == "" {
		if base == nil {
			return "(relative)"
		}
		u = base.ResolveReference(u)
	} else if u.Scheme == "" && base != nil {
		// A protocol-relative URL takes the page's scheme.
		u.Scheme = base.Scheme
	}
	if u.Host == "" {
		return "(relative)"
	}
	return originString(u)
}

func originString(u *url.URL) string {
	if u.Scheme == "" {
		return "//" + u.Host
	}
	return u.Scheme + "://" + u.Host
}

func (r *reporter) element(e *lolhtml.Element) error {
	tag := e.TagName()

	if style, ok := e.Attribute("style"); ok && strings.Contains(style, "url(") {
		for _, u := range cssURLs(style) {
			r.note(u, "style", Fetched)
		}
	}

	switch tag {
	case "base":
		if href, ok := e.Attribute("href"); ok && r.base == nil && strings.TrimSpace(href) != "" {
			if u, err := url.Parse(strings.TrimSpace(href)); err == nil {
				if r.page != nil {
					u = r.page.ResolveReference(u)
				}
				r.base = u
			}
		}
		return nil
	case "link":
		return r.link(e)
	}

	for _, s := range Sources[tag] {
		v, ok := e.Attribute(s.attr)
		if !ok {
			continue
		}
		if s.attr == "srcset" {
			for _, m := range srcsetURLs(v) {
				r.note(m, tag+"[srcset]", s.kind)
			}
			continue
		}
		r.note(v, tag, s.kind)
	}
	return nil
}

// link needs the rel to know what the href means: a preconnect is a connection and a
// canonical is a claim.
func (r *reporter) link(e *lolhtml.Element) error {
	href, ok := e.Attribute("href")
	if !ok {
		return nil
	}
	rel, _ := e.Attribute("rel")
	kind := Fetched
	label := "link"
	for _, token := range strings.Fields(strings.ToLower(rel)) {
		if k, known := LinkRels[token]; known {
			kind = k
			label = "link[" + token + "]"
			break
		}
	}
	r.note(href, label, kind)
	// A preload of an image can carry a candidate list of its own.
	if set, ok := e.Attribute("imagesrcset"); ok {
		for _, m := range srcsetURLs(set) {
			r.note(m, label+"[imagesrcset]", kind)
		}
	}
	return nil
}

// styleText reads a stylesheet for url() references. The text is accumulated to the
// end of the node and put back unchanged - as HTML, because Text would escape the CSS
// - which for a reporter means "put back at all": the destination is io.Discard, and
// the point of writing it is that this program stays a rewrite rather than a parser.
func (r *reporter) styleText(c *lolhtml.TextChunk) error {
	r.css.WriteString(c.Text())
	if !c.IsLastInTextNode() {
		return nil
	}
	sheet := r.css.String()
	r.css.Reset()
	for _, u := range cssURLs(sheet) {
		r.note(u, "css", Fetched)
	}
	for _, u := range cssImports(sheet) {
		r.note(u, "css[@import]", Fetched)
	}
	return nil
}

// cssURLs returns every url(...) argument, unquoted.
func cssURLs(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "url(")
		if i < 0 {
			return out
		}
		s = s[i+len("url("):]
		j := strings.IndexByte(s, ')')
		if j < 0 {
			return out
		}
		out = append(out, unquote(strings.TrimSpace(s[:j])))
		s = s[j+1:]
	}
}

// cssImports returns the URLs of @import rules written as strings rather than url().
func cssImports(s string) []string {
	var out []string
	lower := strings.ToLower(s)
	for {
		i := strings.Index(lower, "@import")
		if i < 0 {
			return out
		}
		// Both strings are advanced together. i is an index into lower, so slicing s
		// with it is only right while the two are the same length: advancing lower
		// alone left every match after the first landing at the wrong offset in s,
		// which for a program that reports the origins a page contacts meant
		// silently reporting the first @import of a stylesheet and no others.
		s = s[i+len("@import"):]
		lower = lower[i+len("@import"):]
		rest := strings.TrimLeft(s, " \t\r\n")
		if len(rest) == 0 || (rest[0] != '"' && rest[0] != '\'') {
			continue
		}
		q := rest[0]
		if j := strings.IndexByte(rest[1:], q); j >= 0 {
			out = append(out, rest[1:1+j])
		}
	}
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// srcsetURLs returns the candidate URLs, by the specification's parse: characters up
// to whitespace, with a trailing comma as the separator.
func srcsetURLs(s string) []string {
	const space = " \t\n\f\r"
	var out []string
	i := 0
	skip := func(set string) {
		for i < len(s) && strings.ContainsRune(set, rune(s[i])) {
			i++
		}
	}
	for {
		skip(space + ",")
		if i >= len(s) {
			return out
		}
		start := i
		for i < len(s) && !strings.ContainsRune(space, rune(s[i])) {
			i++
		}
		raw := strings.TrimRight(s[start:i], ",")
		trailing := strings.HasSuffix(s[start:i], ",")
		if raw != "" {
			out = append(out, raw)
		}
		if !trailing {
			skip(space)
			for i < len(s) && s[i] != ',' {
				i++
			}
		}
	}
}

func (r *reporter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(Elements, r.element),
		lolhtml.OnText("style", r.styleText),
	}
}

// Origins reads src and reports the origins it would contact. Nothing is written: the
// document is not the output.
func Origins(src io.Reader, opts Options) (Report, error) {
	r := &reporter{opts: opts, origins: map[string]*Origin{}}
	if opts.Page != "" {
		if u, err := url.Parse(opts.Page); err == nil && u.Host != "" {
			r.page = u
		}
	}
	w, err := lolhtml.NewWriter(io.Discard, r.options()...)
	if err != nil {
		return Report{}, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return r.report(), err
	}
	if err := w.Close(); err != nil {
		return r.report(), err
	}
	return r.report(), nil
}

func (r *reporter) report() Report {
	rep := Report{Page: r.opts.Page}
	if r.base != nil {
		rep.Base = r.base.String()
	}
	sort.SliceStable(r.order, func(i, j int) bool {
		a, b := r.origins[r.order[i]], r.origins[r.order[j]]
		if a.First != b.First {
			return a.First
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Name < b.Name
	})
	for _, name := range r.order {
		rep.Origins = append(rep.Origins, *r.origins[name])
	}
	return rep
}

func main() {
	var opts Options
	flag.StringVar(&opts.Page, "page", "", "the document's own URL, which makes first-party visible")
	third := flag.Bool("third-party", false, "list only third-party origins the browser would fetch from")
	flag.Parse()

	rep, err := Origins(os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "origins:", err)
		os.Exit(2)
	}
	if *third {
		for _, o := range rep.ThirdParty() {
			fmt.Printf("%-40s %-9d %s\n", o.Name, o.Requests, triggerList(o.Triggers))
		}
		if len(rep.ThirdParty()) > 0 {
			os.Exit(1)
		}
		return
	}
	fmt.Print(rep)
}

// Command rebase rewrites a <base> element away by resolving the URLs it affected.
//
//	<base href="/assets/"><img src="a.png">  ->  <img src="/assets/a.png">
//
// A base element is two defaults in one tag: href changes what every relative URL in
// the document resolves against, and target changes where every link opens. Removing
// the tag without doing anything else changes both, silently, which is why "strip the
// base tag" is not a rewrite anyone should ship. This program does the work instead:
// it resolves relative URLs against the base, puts the base's target on the links
// that were relying on it, and then removes the element.
//
// # The base can arrive after what it affects
//
// A base element belongs in the head, before anything that names a URL, and that is
// where it usually is. It does not have to be: a document can carry one in the body,
// after images that a browser resolved against the page URL and after links it had
// already given a target. This program is a single pass, so a URL it has already
// written past cannot be changed - and it says so rather than pretending, counting
// each URL that went by before the base arrived and exiting non-zero. A caller who
// needs those too has the same answer as everywhere else the evidence arrives late:
// buffer the document and run it twice, the first pass only looking for the base.
//
// Two more shapes worth knowing, both from the specification: only the first href and
// the first target count, and they can come from different elements - so
// "<base target=_blank><base href=/x/>" has both defaults and neither element is
// redundant. A base with neither attribute does nothing at all.
//
// # Stylesheets are text, and their text needs the other content type
//
// A relative URL can also be inside a style attribute or a <style> element, and both
// are rewritten here. The element is the interesting one: its text is raw text, so
// [lolhtml.Text] would escape a CSS ">" into "&gt;" and break every child selector in
// the sheet. [lolhtml.HTML] is the content type that puts CSS back as CSS - and it is
// unguarded, because a text chunk cannot say which element it came from, so this
// program calls [lolhtml.CheckRawText] before writing. See the package documentation
// on inserting into a script or a style.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// URLAttributes are the attributes that hold a URL, by element. One handler and one
// selector list, because two handlers matching the same element would resolve the
// same attribute twice.
var URLAttributes = map[string][]string{
	"a": {"href"}, "area": {"href"}, "link": {"href"},
	"img": {"src", "srcset", "longdesc"}, "source": {"src", "srcset"},
	"script": {"src"}, "iframe": {"src"}, "embed": {"src"}, "track": {"src"},
	"audio": {"src"}, "video": {"src", "poster"}, "object": {"data"},
	"input": {"src", "formaction"}, "button": {"formaction"},
	"form": {"action"}, "blockquote": {"cite"}, "q": {"cite"},
	"del": {"cite"}, "ins": {"cite"}, "use": {"xlink:href"}, "image": {"xlink:href"},
}

// Elements is the selector list built from URLAttributes, plus the elements this
// program has other business with.
const Elements = `base,a[href],area[href],link[href],img,source,script[src],iframe[src],` +
	`embed[src],track[src],audio[src],video,object[data],input,button[formaction],` +
	`form[action],blockquote[cite],q[cite],del[cite],ins[cite],` +
	`use[xlink\:href],image[xlink\:href],[style]`

// Targeted are the elements a base target applies to.
var Targeted = map[string]bool{"a": true, "area": true, "form": true}

// Options are the decisions a caller gets to make.
type Options struct {
	// Page is the document's own URL, which the base href itself resolves against
	// and which relative URLs resolve against when there is no base. Required for a
	// relative base href.
	Page string
	// Keep leaves the base element in the document instead of removing it, for a
	// caller who wants the URLs resolved and the default kept.
	Keep bool
}

// Result is what happened.
type Result struct {
	URLs     int    // URLs resolved
	Targets  int    // elements given the base's target
	Styles   int    // style attributes and elements rewritten
	Early    int    // URLs that went past before the base arrived
	Removed  bool   // the base element was removed
	BaseHref string // the href that was in force
	BaseTgt  string // the target that was in force
}

func (r Result) String() string {
	base := "no base"
	if r.BaseHref != "" || r.BaseTgt != "" {
		base = fmt.Sprintf("base href=%q target=%q", r.BaseHref, r.BaseTgt)
	}
	return fmt.Sprintf("rebase: %s; resolved %d urls, %d styles, %d targets; %d urls went past first",
		base, r.URLs, r.Styles, r.Targets, r.Early)
}

// OK reports whether every URL in the document was resolved against the base that
// applied to it.
func (r Result) OK() bool { return r.Early == 0 }

type rebaser struct {
	opts Options
	res  Result
	base *url.URL        // the resolved base URL, nil until a base href arrives
	page *url.URL        // the document's own URL, for a relative base href
	css1 strings.Builder // a <style> element's text, accumulated to its end

	pendingU int // relative URLs seen before any base href
	pendingT int // targetable elements seen before any base target
}

func (r *rebaser) element(e *lolhtml.Element) error {
	tag := e.TagName()
	if tag == "base" {
		return r.baseElement(e)
	}
	// A style attribute can be anywhere, so it is checked for every match.
	if err := r.styleAttribute(e); err != nil {
		return err
	}
	if err := r.urls(e, tag); err != nil {
		return err
	}
	return r.target(e, tag)
}

// urls resolves the element's URL attributes, or counts what a base arriving later
// would have changed. Which of those it is cannot be known here - the base may be
// below - so the count is provisional and only becomes a number in the report if a
// base href does arrive.
func (r *rebaser) urls(e *lolhtml.Element, tag string) error {
	names := URLAttributes[tag]
	if len(names) == 0 {
		return nil
	}
	if r.base == nil {
		for _, name := range names {
			if v, ok := e.Attribute(name); ok && strings.TrimSpace(v) != "" && isRelative(v) {
				r.pendingU++
			}
		}
		return nil
	}
	for _, name := range names {
		raw, ok := e.Attribute(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		var next string
		if name == "srcset" {
			next = r.srcset(raw)
		} else {
			next = r.resolve(raw)
		}
		if next == "" || next == raw {
			continue
		}
		r.res.URLs++
		if err := e.SetAttribute(name, next); err != nil {
			return err
		}
	}
	return nil
}

// target is the other half of the tag, and it is independent of the href: a base with
// a target and no href changes where links open and nothing else.
func (r *rebaser) target(e *lolhtml.Element, tag string) error {
	if !Targeted[tag] {
		return nil
	}
	if has, _ := e.HasAttribute("target"); has {
		return nil
	}
	if r.res.BaseTgt == "" {
		r.pendingT++
		return nil
	}
	r.res.Targets++
	return e.SetAttribute("target", r.res.BaseTgt)
}

// baseElement reads the first href and the first target - which the specification
// takes independently, so they can come from different elements - and removes the tag.
func (r *rebaser) baseElement(e *lolhtml.Element) error {
	if href, ok := e.Attribute("href"); ok && r.res.BaseHref == "" && strings.TrimSpace(href) != "" {
		r.res.BaseHref = href
		if u, err := url.Parse(unescapeAmp(href)); err == nil {
			if r.page != nil {
				u = r.page.ResolveReference(u)
			}
			r.base = u
			// Now it is known that the URLs above this element were at stake.
			r.res.Early += r.pendingU
			r.pendingU = 0
		}
	}
	if tgt, ok := e.Attribute("target"); ok && r.res.BaseTgt == "" && strings.TrimSpace(tgt) != "" {
		r.res.BaseTgt = tgt
		r.res.Early += r.pendingT
		r.pendingT = 0
	}
	if !r.opts.Keep {
		e.Remove()
		r.res.Removed = true
	}
	return nil
}

// resolve turns one relative URL into an absolute one against the base. It returns ""
// for anything it will not touch: a URL with a scheme, a fragment-only reference -
// which the base does affect in a browser, but rewriting it changes what a
// same-document link means - and anything it cannot parse.
func (r *rebaser) resolve(raw string) string {
	if r.base == nil || !isRelative(raw) {
		return ""
	}
	u, err := url.Parse(unescapeAmp(raw))
	if err != nil {
		return ""
	}
	out := r.base.ResolveReference(u).String()
	return escapeAmp(out)
}

// srcset resolves every member, keeping the descriptors.
func (r *rebaser) srcset(raw string) string {
	members, ok := parseSrcset(raw)
	if !ok {
		return ""
	}
	out := make([]string, 0, len(members))
	changed := false
	for _, m := range members {
		next := r.resolve(m.url)
		if next == "" {
			next = m.url
		} else {
			changed = true
		}
		if m.descriptor != "" {
			next += " " + m.descriptor
		}
		out = append(out, next)
	}
	if !changed {
		return ""
	}
	return strings.Join(out, ", ")
}

// styleAttribute rewrites url(...) in an inline style, or counts what a base arriving
// later would have changed - the same provisional count urls() keeps, and for the same
// reason. Leaving CSS out of it was worse than not resolving it: the base element is
// removed all the same, so a url(x.png) above it silently starts resolving against a
// different base, and the report said no URL had gone past.
func (r *rebaser) styleAttribute(e *lolhtml.Element) error {
	raw, ok := e.Attribute("style")
	if !ok || !strings.Contains(raw, "url(") {
		return nil
	}
	next, relative := r.css(raw)
	if r.base == nil {
		r.pendingU += relative
		return nil
	}
	if next == raw {
		return nil
	}
	r.res.Styles++
	return e.SetAttribute("style", next)
}

// styleText rewrites a whole <style> element's text. It is accumulated to the end of
// the node, rewritten, checked, and written back as HTML rather than Text: raw text
// does not decode references, so Text would escape the CSS instead of quoting it.
func (r *rebaser) styleText(c *lolhtml.TextChunk) error {
	r.css1.WriteString(c.Text())
	if !c.IsLastInTextNode() {
		c.Remove()
		return nil
	}
	sheet := r.css1.String()
	r.css1.Reset()
	next, relative := r.css(sheet)
	if r.base == nil {
		// Counted rather than resolved, as in styleAttribute: the sheet goes back
		// unchanged, and the caller is told these URLs went past the base.
		r.pendingU += relative
		return c.Replace(sheet, lolhtml.HTML)
	}
	if next != sheet {
		r.res.Styles++
	}
	if err := lolhtml.CheckRawText("style", next); err != nil {
		return err
	}
	return c.Replace(next, lolhtml.HTML)
}

// css resolves every url(...) it can read, leaving the rest alone, and reports how many
// relative references it saw. The count is what the base-arriving-late case needs: with no
// base there is nothing to resolve, and the references still have to be counted.
func (r *rebaser) css(s string) (string, int) {
	var b strings.Builder
	relative := 0
	for {
		i := strings.Index(s, "url(")
		if i < 0 {
			b.WriteString(s)
			return b.String(), relative
		}
		b.WriteString(s[:i+len("url(")])
		s = s[i+len("url("):]
		j := strings.IndexByte(s, ')')
		if j < 0 {
			b.WriteString(s)
			return b.String(), relative
		}
		inner := s[:j]
		quote := ""
		trimmed := strings.TrimSpace(inner)
		if len(trimmed) >= 2 && (trimmed[0] == '"' || trimmed[0] == '\'') && trimmed[len(trimmed)-1] == trimmed[0] {
			quote = string(trimmed[0])
			trimmed = trimmed[1 : len(trimmed)-1]
		}
		if isRelative(trimmed) {
			relative++
		}
		if next := r.resolve(trimmed); next != "" {
			b.WriteString(quote + next + quote)
		} else {
			b.WriteString(inner)
		}
		b.WriteString(")")
		s = s[j+1:]
	}
}

// isRelative reports whether a URL is one the base applies to. A scheme, a
// protocol-relative URL and a bare fragment are all left alone.
func isRelative(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
		return false
	}
	if i := strings.IndexAny(s, ":/?#"); i >= 0 && s[i] == ':' {
		return false
	}
	return true
}

// unescapeAmp and escapeAmp move a URL between the document's spelling and its own.
// An attribute value arrives with its references still encoded, and "&amp;" is the
// one that matters in a URL.
func unescapeAmp(s string) string { return strings.ReplaceAll(s, "&amp;", "&") }
func escapeAmp(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&amp;", "&"), "&", "&amp;")
}

// member is one entry of a srcset.
type member struct{ url, descriptor string }

func parseSrcset(s string) ([]member, bool) {
	const space = " \t\n\f\r"
	var out []member
	i := 0
	skip := func(set string) {
		for i < len(s) && strings.ContainsRune(set, rune(s[i])) {
			i++
		}
	}
	for {
		skip(space + ",")
		if i >= len(s) {
			return out, len(out) > 0
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
			m.descriptor = strings.TrimSpace(s[start:i])
		}
		out = append(out, m)
	}
}

func (r *rebaser) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(Elements, r.element),
		lolhtml.OnText("style", r.styleText),
	}
}

// Rebase copies src to dst, resolving away the document's base element.
func Rebase(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	r := &rebaser{opts: opts}
	if opts.Page != "" {
		if u, err := url.Parse(opts.Page); err == nil {
			r.page = u
		}
	}
	w, err := lolhtml.NewWriter(dst, r.options()...)
	if err != nil {
		return r.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return r.res, err
	}
	if err := w.Close(); err != nil {
		return r.res, err
	}
	return r.res, nil
}

func main() {
	var opts Options
	flag.StringVar(&opts.Page, "page", "", "the document's own URL, which a relative base href resolves against")
	flag.BoolVar(&opts.Keep, "keep", false, "leave the base element in place")
	flag.Parse()

	res, err := Rebase(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rebase:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

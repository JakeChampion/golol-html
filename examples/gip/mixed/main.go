// Command mixed finds mixed content on an https page and, told to, refuses the page.
//
//	<script src="http://cdn/x.js">   blockable: a browser will not load it
//	<img src="http://cdn/x.png">     upgradeable: a browser will try https first
//	<a href="http://other/p">        neither: a link is not a subresource
//
// Mixed content is an http:// subresource on an https:// page. Browsers block the
// dangerous half outright and upgrade the rest, so the practical questions are which
// half a page has and what to do about it - and the three answers this program offers
// are upgrade, report and refuse.
//
// # Refusing is a decision about the sink, not about the handler
//
// -strict returns an error from the handler that finds the first blockable URL, which
// stops the rewrite. Measured, that is not atomic: everything before the failing
// element has already reached the destination, at every write size and including a
// single Write, and what it holds is well-formed HTML - so a client reading it sees a
// short page rather than a failure. The only way to fail closed is for the caller to
// hold the output until the rewrite finishes, which is what -strict does here: it
// writes into a buffer and forwards nothing unless the whole document came through.
// See [lolhtml.ErrPoisoned] and the package documentation on handler errors.
//
// The same measurement rules out the tempting shape: deciding at the end. A
// [lolhtml.OnDocumentEnd] handler can see the whole page and return an error, and by
// then every byte has gone out - the error surfaces from Close with nothing left to
// stop. A rewrite that must fail closed decides as early as it can and buffers
// anyway.
//
// # What counts
//
// The classification is the specification's, not a guess: script, stylesheet, iframe,
// object and embed are blockable, and images, media and tracks are upgradeable.
// Navigations - a link, a form, a cite - are not subresources at all, and reporting
// them as mixed content is how a checker gets ignored.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Class is what a browser does with an insecure subresource.
type Class int

const (
	// Blockable is refused outright: script, stylesheet, iframe, object, embed.
	Blockable Class = iota
	// Upgradeable is retried over https and blocked if that fails: images, media.
	Upgradeable
	// Navigation is not a subresource: a link, a form, a citation.
	Navigation
)

func (c Class) String() string {
	switch c {
	case Blockable:
		return "blockable"
	case Upgradeable:
		return "upgradeable"
	}
	return "navigation"
}

// where is one place an insecure URL can appear.
type where struct {
	attr  string
	class Class
}

// Sources are the places this program looks, by element name. The classes are the
// specification's split between blockable and optionally-blockable content.
var Sources = map[string][]where{
	"script": {{"src", Blockable}},
	"iframe": {{"src", Blockable}},
	"object": {{"data", Blockable}},
	"embed":  {{"src", Blockable}},
	"img":    {{"src", Upgradeable}, {"srcset", Upgradeable}},
	// <image> is a spelling of <img>: the parser renames it and a browser fetches
	// it, so a checker that matched only img would miss an insecure request. The SVG
	// element of the same name is a different thing, and is skipped by namespace.
	"image":     {{"src", Upgradeable}, {"srcset", Upgradeable}, {"xlink:href", Upgradeable}},
	"source":    {{"src", Upgradeable}, {"srcset", Upgradeable}},
	"video":     {{"src", Upgradeable}, {"poster", Upgradeable}},
	"audio":     {{"src", Upgradeable}},
	"track":     {{"src", Upgradeable}},
	"input":     {{"src", Upgradeable}},
	"use":       {{"xlink:href", Blockable}},
	"svg:image": {{"xlink:href", Upgradeable}},
	"a":         {{"href", Navigation}},
	"area":      {{"href", Navigation}},
	"form":      {{"action", Navigation}},
	"q":         {{"cite", Navigation}},
}

// Elements is the selector list: one handler, because two selectors matching the same
// element would see each other's edits and upgrade a URL twice.
const Elements = `script[src],iframe[src],object[data],embed[src],img,image,source,video,` +
	`audio[src],track[src],input[src],use[xlink\:href],` +
	`a[href],area[href],form[action],q[cite],link[href],[style]`

// LinkBlockable are the link rel tokens whose target is a subresource rather than a
// destination. A rel this program does not know is treated as a navigation, which is
// the quiet answer rather than the loud one.
var LinkBlockable = map[string]bool{
	"stylesheet": true, "modulepreload": true, "preload": true, "prefetch": true,
	"manifest": true, "import": true,
}

// Finding is one insecure URL.
type Finding struct {
	Element string
	Attr    string
	URL     string
	Class   Class
}

// Result is what happened.
type Result struct {
	Findings  []Finding
	Upgraded  int
	Refused   bool   // strict mode stopped the rewrite
	RefusedAt string // the URL it stopped on
}

// Blockable returns the findings a browser would refuse to load.
func (r Result) Blockable() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Class == Blockable {
			out = append(out, f)
		}
	}
	return out
}

func (r Result) String() string {
	counts := map[Class]int{}
	for _, f := range r.Findings {
		counts[f.Class]++
	}
	s := fmt.Sprintf("mixed: %d blockable, %d upgradeable, %d navigations; %d upgraded",
		counts[Blockable], counts[Upgradeable], counts[Navigation], r.Upgraded)
	if r.Refused {
		s += fmt.Sprintf("; refused at %s", r.RefusedAt)
	}
	return s
}

// OK reports whether the page has no blockable mixed content.
func (r Result) OK() bool { return len(r.Blockable()) == 0 }

// ErrBlockable is what strict mode fails with.
type ErrBlockable struct{ Finding Finding }

func (e ErrBlockable) Error() string {
	return fmt.Sprintf("blockable mixed content: <%s %s=%q>", e.Finding.Element, e.Finding.Attr, e.Finding.URL)
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Strict fails the rewrite on the first blockable URL. The caller has to hold
	// the output until the rewrite finishes for that to mean anything: see the file
	// comment.
	Strict bool
	// Upgrade rewrites http:// to https:// where a browser would have done it
	// anyway, which turns an upgradeable finding into no request at all.
	Upgrade bool
}

type finder struct {
	opts Options
	res  Result
	css  strings.Builder
}

func (f *finder) element(e *lolhtml.Element) error {
	tag := e.TagName()
	if tag == "image" && e.NamespaceURI() != lolhtml.NamespaceHTML {
		// An SVG image, which is its own element rather than a spelling of img.
		tag = "svg:image"
	}

	if style, ok := e.Attribute("style"); ok && strings.Contains(style, "url(") {
		for _, u := range cssURLs(style) {
			if err := f.note(Finding{tag, "style", u, Upgradeable}); err != nil {
				return err
			}
		}
	}

	if tag == "link" {
		return f.link(e)
	}
	for _, w := range Sources[tag] {
		raw, ok := e.Attribute(w.attr)
		if !ok {
			continue
		}
		if w.attr == "srcset" {
			next, changed, err := f.srcset(tag, raw, w.class)
			if err != nil {
				return err
			}
			if changed {
				if err := e.SetAttribute(w.attr, next); err != nil {
					return err
				}
			}
			continue
		}
		if !insecure(raw) {
			continue
		}
		if err := f.note(Finding{tag, w.attr, raw, w.class}); err != nil {
			return err
		}
		if f.opts.Upgrade && w.class != Navigation {
			f.res.Upgraded++
			if err := e.SetAttribute(w.attr, upgrade(raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *finder) link(e *lolhtml.Element) error {
	raw, ok := e.Attribute("href")
	if !ok || !insecure(raw) {
		return nil
	}
	rel, _ := e.Attribute("rel")
	class := Navigation
	for _, token := range strings.Fields(strings.ToLower(rel)) {
		if LinkBlockable[token] {
			class = Blockable
			break
		}
	}
	if err := f.note(Finding{"link", "href", raw, class}); err != nil {
		return err
	}
	if f.opts.Upgrade && class != Navigation {
		f.res.Upgraded++
		return e.SetAttribute("href", upgrade(raw))
	}
	return nil
}

// styleText reads a stylesheet, and rewrites it only if it has to: the text goes back
// as HTML because Text would escape the CSS, and it is checked first because the text
// path does not check itself.
func (f *finder) styleText(c *lolhtml.TextChunk) error {
	f.css.WriteString(c.Text())
	if !c.IsLastInTextNode() {
		c.Remove()
		return nil
	}
	sheet := f.css.String()
	f.css.Reset()
	changed := false
	for _, u := range cssURLs(sheet) {
		if !insecure(u) {
			continue
		}
		if err := f.note(Finding{"style", "url()", u, Upgradeable}); err != nil {
			return err
		}
		if f.opts.Upgrade {
			f.res.Upgraded++
			sheet = strings.ReplaceAll(sheet, u, upgrade(u))
			changed = true
		}
	}
	_ = changed
	if err := lolhtml.CheckRawText("style", sheet); err != nil {
		return err
	}
	return c.Replace(sheet, lolhtml.HTML)
}

// srcset checks and upgrades each candidate.
func (f *finder) srcset(tag, raw string, class Class) (string, bool, error) {
	members, ok := parseSrcset(raw)
	if !ok {
		return "", false, nil
	}
	out := make([]string, 0, len(members))
	changed := false
	for _, m := range members {
		u := m.url
		if insecure(u) {
			if err := f.note(Finding{tag, "srcset", u, class}); err != nil {
				return "", false, err
			}
			if f.opts.Upgrade {
				f.res.Upgraded++
				u = upgrade(u)
				changed = true
			}
		}
		if m.descriptor != "" {
			u += " " + m.descriptor
		}
		out = append(out, u)
	}
	return strings.Join(out, ", "), changed, nil
}

// note records a finding, and in strict mode stops the rewrite at the first blockable
// one. Stopping is the handler's part; holding the output is the caller's.
func (f *finder) note(fi Finding) error {
	f.res.Findings = append(f.res.Findings, fi)
	if f.opts.Strict && fi.Class == Blockable {
		f.res.Refused = true
		f.res.RefusedAt = fi.URL
		return ErrBlockable{fi}
	}
	return nil
}

func insecure(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "http://")
}

// upgrade turns http:// into https://, keeping everything else about the URL.
func upgrade(raw string) string {
	s := strings.TrimSpace(raw)
	return "https://" + s[len("http://"):]
}

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
		inner := strings.TrimSpace(s[:j])
		if len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') && inner[len(inner)-1] == inner[0] {
			inner = inner[1 : len(inner)-1]
		}
		out = append(out, inner)
		s = s[j+1:]
	}
}

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

func (f *finder) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(Elements, f.element),
		lolhtml.OnText("style", f.styleText),
	}
}

// Check copies src to dst, reporting mixed content and optionally upgrading it. In
// strict mode it writes nothing at all unless the whole document came through: the
// output is buffered here rather than streamed, because a rewrite that stops mid-page
// has already delivered a short page otherwise.
func Check(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	f := &finder{opts: opts}
	out := dst
	var held bytes.Buffer
	if opts.Strict {
		out = &held
	}
	w, err := lolhtml.NewWriter(out, f.options()...)
	if err != nil {
		return f.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return f.res, err
	}
	if err := w.Close(); err != nil {
		return f.res, err
	}
	if opts.Strict {
		if _, err := dst.Write(held.Bytes()); err != nil {
			return f.res, err
		}
	}
	return f.res, nil
}

func main() {
	var opts Options
	report := flag.Bool("report", false, "write no document, only the findings")
	flag.BoolVar(&opts.Strict, "strict", false, "refuse the page on blockable mixed content")
	flag.BoolVar(&opts.Upgrade, "upgrade", false, "rewrite http:// to https:// where a browser would")
	flag.Parse()

	var dst io.Writer = os.Stdout
	if *report {
		dst = io.Discard
	}
	res, err := Check(dst, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	for _, f := range sortFindings(res.Findings) {
		fmt.Fprintf(os.Stderr, "  %-11s <%s %s=%q>\n", f.Class, f.Element, f.Attr, f.URL)
	}
	if err != nil {
		// The library wraps a handler error with the selector that matched, which is
		// the whole list here. The refusal says it better.
		var blocked ErrBlockable
		if errors.As(err, &blocked) {
			fmt.Fprintln(os.Stderr, "mixed: refused:", blocked)
		} else {
			fmt.Fprintln(os.Stderr, "mixed:", err)
		}
		os.Exit(1)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

func sortFindings(in []Finding) []Finding {
	out := append([]Finding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

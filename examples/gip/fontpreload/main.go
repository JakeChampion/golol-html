// Command fontpreload injects preload hints for the fonts a stylesheet uses.
//
//	fontpreload -css site.css -base https://example.com/css/ page.html
//	<link rel="preload" as="font" type="font/woff2" href="https://example.com/fonts/a.woff2" crossorigin>
//
// The stylesheet is a separate input rather than something read out of the page,
// and that is the point of the exercise: the font URLs are in the CSS, the hints
// belong in the HTML, and nothing about the document says which stylesheet it
// will end up using. A tool that guessed would preload fonts the page does not
// want, which costs bandwidth on every request.
//
// Three things it will not do.
//
// It will not preload a font it cannot name a type for. A preload without the
// right type attribute is either ignored or fetched twice, so a URL with an
// unrecognised extension is reported rather than hinted.
//
// It will not omit crossorigin. A font fetch is always CORS, so a preload without
// it does not match the later request and the font is fetched twice - which is
// worse than not preloading at all.
//
// It will not preload more than a handful. Every preload competes with the
// document for bandwidth, and a stylesheet with thirty faces wants a look at its
// font strategy rather than thirty hints.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// fontTypes maps a file extension to the type a preload has to declare.
var fontTypes = map[string]string{
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
}

type injector struct {
	css  string // the stylesheet's contents
	base string // absolute base for resolving url() references
	max  int
	only string // if set, only this extension is hinted

	fonts   []font
	added   int
	skipped map[string]int
}

// A font is one resolved face to preload.
type font struct {
	href string
	kind string
}

func (in *injector) note(reason string) {
	if in.skipped == nil {
		in.skipped = map[string]int{}
	}
	in.skipped[reason]++
}

func defaults() *injector { return &injector{max: 4, only: ".woff2"} }

func (in *injector) validate() error {
	if in.css == "" {
		return fmt.Errorf("-css is required: the font urls are in the stylesheet")
	}
	if in.max < 1 {
		return fmt.Errorf("-max %d leaves no room for a hint", in.max)
	}
	if in.base != "" {
		u, err := url.Parse(in.base)
		if err != nil {
			return fmt.Errorf("-base %q is not a url: %w", in.base, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("-base %q is not absolute", in.base)
		}
	}
	if in.only != "" {
		if _, ok := fontTypes[strings.ToLower(in.only)]; !ok {
			return fmt.Errorf("-only %q is not a font extension this knows a type "+
				"for; a preload without the right type is ignored or fetched twice",
				in.only)
		}
	}
	return nil
}

// collect finds the font URLs in the stylesheet.
//
// A deliberately small CSS reader: it looks for url(...) inside @font-face
// blocks and nothing else. Parsing CSS properly is a different program, and the
// failure mode of getting it wrong here - preloading something that is not a
// font - is a wasted request on every page view.
func (in *injector) collect() {
	seen := map[string]bool{}

	for _, block := range fontFaceBlocks(in.css) {
		for _, raw := range urlsIn(block) {
			href, kind, ok := in.resolve(raw)
			if !ok {
				continue
			}
			if seen[href] {
				continue
			}
			seen[href] = true
			in.fonts = append(in.fonts, font{href: href, kind: kind})
		}
	}
}

// fontFaceBlocks returns the body of every @font-face rule. Nested braces are
// not a thing inside one, so matching to the first closing brace is enough.
func fontFaceBlocks(css string) []string {
	var out []string
	rest := css
	for {
		i := indexFold(rest, "@font-face")
		if i < 0 {
			return out
		}
		rest = rest[i+len("@font-face"):]
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			return out
		}
		close := strings.IndexByte(rest[open:], '}')
		if close < 0 {
			return out
		}
		out = append(out, rest[open+1:open+close])
		rest = rest[open+close:]
	}
}

// urlsIn returns the argument of every url() in a chunk of CSS, unquoted.
func urlsIn(css string) []string {
	var out []string
	rest := css
	for {
		i := indexFold(rest, "url(")
		if i < 0 {
			return out
		}
		rest = rest[i+len("url("):]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return out
		}
		arg := strings.TrimSpace(rest[:end])
		arg = strings.Trim(arg, `"'`)
		if arg != "" {
			out = append(out, arg)
		}
		rest = rest[end:]
	}
}

func indexFold(s, sub string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}

// resolve turns a url() argument into an absolute href and a font type, or
// reports why it cannot.
func (in *injector) resolve(raw string) (string, string, bool) {
	if strings.HasPrefix(strings.ToLower(raw), "data:") {
		// Already in the stylesheet; preloading it would fetch nothing.
		in.note("a data: url needs no preload")
		return "", "", false
	}

	u, err := url.Parse(raw)
	if err != nil {
		in.note("an unparseable url() was ignored")
		return "", "", false
	}

	ext := strings.ToLower(path.Ext(u.Path))
	kind, known := fontTypes[ext]
	if !known {
		in.note(fmt.Sprintf("no font type is known for %q, so it was not preloaded", ext))
		return "", "", false
	}
	if in.only != "" && ext != strings.ToLower(in.only) {
		in.note("a font outside -only was not preloaded")
		return "", "", false
	}

	if u.Scheme != "" && u.Host != "" {
		return raw, kind, true
	}
	if in.base == "" {
		in.note("a relative url() needs -base to become absolute")
		return "", "", false
	}
	base, err := url.Parse(in.base)
	if err != nil {
		return "", "", false
	}
	return base.ResolveReference(u).String(), kind, true
}

// hinted is the fonts to emit. Pure: the over-cap note is recorded by the caller.
func (in *injector) hinted() []font {
	if len(in.fonts) <= in.max {
		return in.fonts
	}
	return in.fonts[:in.max]
}

// markup is the links. Every value is escaped for an attribute, and crossorigin
// is not optional: a font fetch is CORS, so a preload without it does not match
// the request the stylesheet will make and the font is fetched twice.
func (in *injector) markup() string {
	var sb strings.Builder
	for _, f := range in.hinted() {
		sb.WriteString(`<link rel="preload" as="font" type="` +
			lolhtml.EscapeAttribute(f.kind) + `" href="` +
			lolhtml.EscapeAttribute(f.href) + `" crossorigin>`)
		in.added++
	}
	return sb.String()
}

func (in *injector) options() []lolhtml.Option {
	// A preload the page already has, matched on href so the same font linked
	// under a different rel is not mistaken for one.
	have := map[string]bool{}
	sawHead := false
	placed := false

	return []lolhtml.Option{
		lolhtml.OnElement(`link[rel~="preload"][href]`, func(e *lolhtml.Element) error {
			have[attr(e, "href")] = true
			return nil
		}),

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
				return in.insert(have, func(markup string) error {
					return end.Before(markup, lolhtml.HTML)
				})
			})
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if sawHead || placed {
				return nil
			}
			placed = true
			return in.insert(have, func(markup string) error {
				return e.Before(markup, lolhtml.HTML)
			})
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !placed && len(in.fonts) > 0 {
				in.note("no head and no body to put the hints in")
			}
			return nil
		}),
	}
}

// insert emits the hints the page does not already have. The already-present
// check happens here rather than at collection time because a preload in the head
// is seen before the insertion point, which is the one ordering this program gets
// for free.
func (in *injector) insert(have map[string]bool, write func(string) error) error {
	var wanted []font
	for _, f := range in.hinted() {
		if have[f.href] {
			in.note("the page already preloads a font")
			continue
		}
		wanted = append(wanted, f)
	}
	if len(wanted) == 0 {
		return nil
	}
	if over := len(in.fonts) - in.max; over > 0 {
		in.note(fmt.Sprintf("%d fonts beyond -max=%d were not preloaded", over, in.max))
	}

	saved := in.fonts
	in.fonts = wanted
	markup := in.markup()
	in.fonts = saved
	return write(markup)
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (in *injector) run(r io.Reader, w io.Writer) error {
	if err := in.validate(); err != nil {
		return err
	}
	in.collect()

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

func injectString(doc, css string, opts ...func(*injector)) (string, *injector, error) {
	in := defaults()
	in.css = css
	for _, o := range opts {
		o(in)
	}
	var out bytes.Buffer
	err := in.run(strings.NewReader(doc), &out)
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
	fmt.Fprintf(&sb, "fonts=%d preloaded=%d\n", len(in.fonts), in.added)
	reasons := make([]string, 0, len(in.skipped))
	for r := range in.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, in.skipped[r])
	}
	return sb.String()
}

func main() {
	in := defaults()
	cssPath := flag.String("css", "", "stylesheet to read font urls from")
	flag.StringVar(&in.base, "base", "", "absolute base for resolving relative url()")
	flag.IntVar(&in.max, "max", in.max, "most fonts to preload")
	flag.StringVar(&in.only, "only", in.only,
		"preload only this extension; empty for every known font type")
	flag.Parse()

	if *cssPath != "" {
		b, err := os.ReadFile(*cssPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fontpreload:", err)
			os.Exit(1)
		}
		in.css = string(b)
	}

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fontpreload:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: fontpreload -css FILE [file.html]")
		os.Exit(2)
	}

	if err := in.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fontpreload:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, in.report())
}

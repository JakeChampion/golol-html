// Command inlinesvg replaces small SVG images with the file's own markup, so that
// CSS can style them and the page can stop asking for them.
//
//	<img src="/i/save.svg" alt="Save">
//	  ->  <svg role="img" aria-label="Save" ...>…the file…</svg>
//
// The file comes from a resolver, which is the caller's: a directory, a cache, a
// build manifest. This program decides what to do with what it hands back, and
// there are three decisions worth spelling out.
//
// # Inlining is a privilege change
//
// An <img> cannot run script, whatever the file behind it contains. The same bytes
// inlined into the page can: an <svg> may hold <script>, an onload attribute, or a
// <use> pointing at another document. So the file is not passed through - it goes
// through a nested rewrite that drops script and style elements, drops every
// on* attribute, and drops href values that are not local fragments. A resolver
// that only ever returns trusted files loses nothing by that; one that can be
// pointed at user uploads needs it.
//
// # An HTML tag name inside an <svg> ends the svg
//
// The parser's foreign-content rules break out of SVG when they meet an HTML tag
// name - <b>, <p>, <img>, <table> and 35 others - and everything after it in the
// file is page content rather than image content. For an inliner that is the whole
// ballgame: a file containing one <p> puts the rest of itself in the document body,
// outside the element this program wrapped it in.
//
// The library's two views of that disagree. NamespaceURI follows the break-out and
// reports HTML for what comes after, while a selector does not: "svg > circle" still
// matches a circle that the tree puts outside the svg. So neither a selector nor a
// namespace check is a reliable "is this still inside my image", and this program
// counts the tag names instead: a file containing one is refused, with its name in
// the report. See differential/foreign_test.go.
//
// # Two copies of one file collide
//
// Ids are document-wide. The same file inlined twice gives two elements the same id,
// and a <use href="#a"> or a fill="url(#a)" then means whichever came first. So ids
// are prefixed per inline - i0-, i1- - and the references that can be rewritten are:
// href and xlink:href when they are local fragments, and url(#…) inside any
// attribute value. A reference this program does not understand is left alone and
// counted, because a rewrite that half-renames an id is worse than one that does not
// rename it at all.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// BreaksOutOfSVG are the HTML tag names that end an <svg> by starting. Measured
// against golang.org/x/net/html rather than copied: see differential/foreign_test.go.
// font is conditional - it breaks out only with a color, face or size attribute - and
// is handled separately.
var BreaksOutOfSVG = map[string]bool{
	"b": true, "big": true, "blockquote": true, "body": true, "br": true,
	"center": true, "code": true, "dd": true, "div": true, "dl": true,
	"dt": true, "em": true, "embed": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "head": true, "hr": true, "i": true,
	"img": true, "li": true, "listing": true, "menu": true, "meta": true,
	"nobr": true, "ol": true, "p": true, "pre": true, "ruby": true, "s": true,
	"small": true, "span": true, "strong": true, "strike": true, "sub": true,
	"sup": true, "table": true, "tt": true, "u": true, "ul": true, "var": true,
}

// FontBreaksOut are the attributes that make a <font> break out.
var FontBreaksOut = []string{"color", "face", "size"}

// Resolver turns the src of an image into the file behind it. Returning an error
// leaves the image alone.
type Resolver func(src string) ([]byte, error)

// Options are the decisions a caller gets to make.
type Options struct {
	// Resolve is required.
	Resolve Resolver
	// Max is the largest file to inline, in bytes. A big icon is not an icon.
	Max int
	// Prefix goes in front of each inlined file's ids, with a counter after it.
	Prefix string
}

// Result is what happened.
type Result struct {
	Inlined      int      // images replaced
	TooBig       int      // files over the limit
	Unresolved   int      // images the resolver would not or could not resolve
	Escaping     int      // files holding a tag name that would end the svg
	EscapingTags []string // which tag names those were, so the report is actionable
	Scripts      int      // script and style elements dropped from inlined files
	Handlers     int      // on* attributes dropped
	Renamed      int      // ids rewritten
	Opaque       int      // references left alone because they were not understood
	Skipped      int      // images that were not svg
}

func (r Result) String() string {
	escaping := strconv.Itoa(r.Escaping)
	if len(r.EscapingTags) > 0 {
		escaping += " (<" + strings.Join(r.EscapingTags, "> <") + ">)"
	}
	return fmt.Sprintf("inlinesvg: inlined %d (%d ids renamed, %d scripts and %d handlers dropped); %d too big, %s escaping, %d unresolved, %d opaque refs, %d not svg",
		r.Inlined, r.Renamed, r.Scripts, r.Handlers, r.TooBig, escaping, r.Unresolved, r.Opaque, r.Skipped)
}

// OK reports whether every SVG image was inlined.
func (r Result) OK() bool { return r.TooBig+r.Unresolved+r.Escaping == 0 }

type inliner struct {
	opts Options
	res  Result
	n    int // inlines so far, for the id prefix
}

func (in *inliner) img(e *lolhtml.Element) error {
	src, ok := e.Attribute("src")
	if !ok || !strings.EqualFold(path.Ext(strip(src)), ".svg") {
		in.res.Skipped++
		return nil
	}
	file, err := in.opts.Resolve(src)
	if err != nil {
		in.res.Unresolved++
		return nil
	}
	if in.opts.Max > 0 && len(file) > in.opts.Max {
		in.res.TooBig++
		return nil
	}
	prefix := in.opts.Prefix + strconv.Itoa(in.n)
	svg, err := in.clean(string(file), prefix)
	if err != nil {
		var esc escapes
		if !errors.As(err, &esc) {
			return err
		}
		in.res.Escaping++
		if !slices.Contains(in.res.EscapingTags, esc.tag) {
			in.res.EscapingTags = append(in.res.EscapingTags, esc.tag)
		}
		return nil
	}
	in.n++
	in.res.Inlined++

	// The accessible name stays in an attribute: an alt may hold a raw "<", which is
	// markup in an element's text. See the package documentation on building markup.
	var attrs string
	if alt, ok := e.Attribute("alt"); ok && alt != "" {
		attrs = ` role="img" aria-label="` + strings.ReplaceAll(alt, `"`, "&quot;") + `"`
	} else {
		attrs = ` aria-hidden="true"`
	}
	return e.Replace(open(svg, attrs), lolhtml.HTML)
}

// escapes reports the tag name in a file that would end the svg element, if there is
// one.
type escapes struct{ tag string }

func (e escapes) Error() string { return "the file contains <" + e.tag + ">, which ends the svg" }

// clean is a rewrite of the resolver's bytes: this library, used on its own input.
// The file is markup from outside the program, so it is treated the way a document
// is - matched and edited rather than searched and spliced.
func (in *inliner) clean(file, prefix string) (string, error) {
	var bad string
	var scripts, handlers, renamed, opaque int
	out, err := lolhtml.RewriteString(file,
		lolhtml.OnElement("script,style", func(e *lolhtml.Element) error {
			scripts++
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if BreaksOutOfSVG[tag] {
				bad = tag
				return nil
			}
			if tag == "font" {
				for _, a := range FontBreaksOut {
					if has, _ := e.HasAttribute(a); has {
						bad = tag
						return nil
					}
				}
			}
			for _, a := range e.AttributeList() {
				name := strings.ToLower(a.Name)
				switch {
				case strings.HasPrefix(name, "on"):
					handlers++
					if err := e.RemoveAttribute(a.Name); err != nil {
						return err
					}
				case name == "id":
					renamed++
					if err := e.SetAttribute(a.Name, prefix+"-"+a.Value); err != nil {
						return err
					}
				case name == "href" || name == "xlink:href":
					v, kind := rewriteRef(a.Value, prefix)
					switch kind {
					case refLocal:
						renamed++
						if err := e.SetAttribute(a.Name, v); err != nil {
							return err
						}
					case refOpaque:
						opaque++
						if err := e.RemoveAttribute(a.Name); err != nil {
							return err
						}
					}
				case strings.Contains(a.Value, "url(#"):
					renamed++
					if err := e.SetAttribute(a.Name, rewriteURLRefs(a.Value, prefix)); err != nil {
						return err
					}
				}
			}
			return nil
		}),
	)
	if err != nil {
		return "", err
	}
	if bad != "" {
		return "", escapes{bad}
	}
	in.res.Scripts += scripts
	in.res.Handlers += handlers
	in.res.Renamed += renamed
	in.res.Opaque += opaque
	return out, nil
}

type refKind int

const (
	refNone refKind = iota
	refLocal
	refOpaque
)

// rewriteRef renames a local fragment and refuses anything else: an href in an
// inlined file that points somewhere else is a request the <img> would not have made.
func rewriteRef(v, prefix string) (string, refKind) {
	if strings.HasPrefix(v, "#") && len(v) > 1 {
		return "#" + prefix + "-" + v[1:], refLocal
	}
	return v, refOpaque
}

// rewriteURLRefs renames every url(#id) in a value, which is how fill, stroke, mask
// and filter point at a definition.
func rewriteURLRefs(v, prefix string) string {
	var b strings.Builder
	for {
		i := strings.Index(v, "url(#")
		if i < 0 {
			b.WriteString(v)
			return b.String()
		}
		b.WriteString(v[:i+len("url(#")])
		b.WriteString(prefix + "-")
		v = v[i+len("url(#"):]
	}
}

// open adds the wrapper's attributes to the file's own <svg> tag, so that the
// element the page sees is the file's element rather than one wrapped around it.
func open(svg, attrs string) string {
	i := strings.Index(strings.ToLower(svg), "<svg")
	if i < 0 {
		return svg
	}
	return svg[:i+len("<svg")] + attrs + svg[i+len("<svg"):]
}

func strip(src string) string {
	if k := strings.IndexAny(src, "?#"); k >= 0 {
		return src[:k]
	}
	return src
}

// Inline copies src to dst, replacing small SVG images with their markup.
func Inline(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Prefix == "" {
		opts.Prefix = "i"
	}
	in := &inliner{opts: opts}
	w, err := lolhtml.NewWriter(dst, in.options()...)
	if err != nil {
		return in.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return in.res, err
	}
	if err := w.Close(); err != nil {
		return in.res, err
	}
	return in.res, nil
}

func (in *inliner) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("img[src]", in.img)}
}

// dirResolver is the resolver the command uses: files under one directory, with the
// URL's path taken as relative to it and anything climbing out of it refused.
func dirResolver(dir string) Resolver {
	return func(src string) ([]byte, error) {
		p := filepath.Clean(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(strip(src), "/"))))
		if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%s is outside %s", src, dir)
		}
		return os.ReadFile(p)
	}
}

func main() {
	dir := flag.String("dir", ".", "directory the image URLs are resolved against")
	max := flag.Int("max", 4096, "largest file to inline, in bytes")
	prefix := flag.String("prefix", "i", "prefix for the ids of each inlined file")
	flag.Parse()

	res, err := Inline(os.Stdout, os.Stdin, Options{
		Resolve: dirResolver(*dir), Max: *max, Prefix: *prefix,
	})
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inlinesvg:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

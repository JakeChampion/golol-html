// Command sprite injects an SVG sprite once and points the page's icons at it.
//
//	<img src="/icons/save.svg" alt="Save">
//	  ->  <svg class="icon" role="img" aria-label="Save"><use href="#i-save"></use></svg>
//
// An icon per <img> is an HTTP request per icon; one sprite holding every symbol is
// one request, and each icon becomes a <use> pointing into it. The rewrite is two
// jobs: turn the references into <use> elements, and get the sprite into the
// document exactly once.
//
// Where the sprite goes is decided by when the rewriter knows it is needed. At the
// top of the document that is not known yet - the first icon may be a screen down -
// so -at top injects the sprite whether the page has icons or not, and -at end
// (the default) injects it only when at least one reference was rewritten, at the
// one position still available once the evidence is in. A <use> is resolved after
// the document is parsed, so a sprite at the end is reachable from a reference
// above it.
//
// The accessible name is where this program is careful, and the reason is that a
// value is only source for the context it came from. An alt attribute may hold a
// raw "<": it is an attribute value, where "<" is an ordinary character. Moved into
// an element's text - the <title> inside an <svg>, which is the obvious place for a
// name - it is markup, and an alt of "<img src=x onerror=alert(1)>" becomes a live
// element that the document itself had made inert. So the name stays in an
// attribute, aria-label, and the only character escaped on the way in is the double
// quote, which is exactly what the library escapes for an attribute it writes
// itself. Escaping more would be wrong in the other direction: [lolhtml.EscapeAttribute]
// escapes "&" as well, which turns a document's "&amp;" into "&amp;amp;".
//
// See the package documentation on building markup, and
// differential/context_test.go for the measurements.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// InHead are the elements a document can have before its body content, so seeing one
// says nothing about where the body starts.
var InHead = map[string]bool{
	"html": true, "head": true, "title": true, "base": true, "link": true,
	"meta": true, "style": true, "script": true, "noscript": true,
	"template": true, "basefont": true, "bgsound": true,
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Sprite is the markup to inject: an <svg> holding one <symbol> per icon.
	Sprite string
	// Prefix goes in front of an icon's name to make the symbol id.
	Prefix string
	// Class goes on each generated <svg>.
	Class string
	// AtTop injects the sprite at the start of the body instead of the end of the
	// document, for a caller who would rather have it early than only when needed.
	AtTop bool
	// Marker is the attribute that says a sprite is already in the document.
	Marker string
}

// Result is what happened.
type Result struct {
	Icons    int  // references rewritten
	Named    int  // of those, ones carrying an accessible name
	Injected bool // the sprite was added
	Present  bool // a sprite was already in the document
	Doubled  bool // both: the document had one, and it was found too late
	Skipped  int  // references left alone
}

// OK reports whether the document came out with exactly one sprite in it.
func (r Result) OK() bool { return !r.Doubled }

func (r Result) String() string {
	where := "not injected"
	switch {
	case r.Doubled:
		where = "injected before the document's own was seen: two sprites, use -at end"
	case r.Present:
		where = "already present"
	case r.Injected:
		where = "injected"
	}
	return fmt.Sprintf("sprite: %d icons rewritten (%d named), %d skipped, sprite %s",
		r.Icons, r.Named, r.Skipped, where)
}

type injector struct {
	opts Options
	res  Result
	used []string
	seen map[string]bool
}

// icon rewrites one reference. The <img> is replaced rather than renamed: renaming a
// void element would leave the new element open, since the source has no end tag to
// close it.
func (i *injector) icon(e *lolhtml.Element) error {
	src, ok := e.Attribute("src")
	if !ok || !strings.EqualFold(path.Ext(strip(src)), ".svg") {
		i.res.Skipped++
		return nil
	}
	name := strings.TrimSuffix(path.Base(strip(src)), path.Ext(strip(src)))
	if name == "" {
		i.res.Skipped++
		return nil
	}
	if !i.seen[name] {
		i.seen[name] = true
		i.used = append(i.used, name)
	}

	var b strings.Builder
	b.WriteString(`<svg class="` + quoteOnly(i.opts.Class) + `"`)
	// An alt attribute is the accessible name, and an empty one says the icon is
	// decoration. Either way the value stays in an attribute: see the file comment.
	if alt, ok := e.Attribute("alt"); ok && alt != "" {
		b.WriteString(` role="img" aria-label="` + quoteOnly(alt) + `"`)
		i.res.Named++
	} else {
		b.WriteString(` aria-hidden="true"`)
	}
	b.WriteString(`><use href="#` + quoteOnly(i.opts.Prefix+name) + `"></use></svg>`)
	i.res.Icons++
	return e.Replace(b.String(), lolhtml.HTML)
}

// present notes a sprite the document already has, so that a second run over the
// output changes nothing.
func (i *injector) present(e *lolhtml.Element) error {
	i.res.Present = true
	if i.res.Injected {
		// The document had a sprite and this run put another one in before reaching
		// it. That is the ordering constraint rather than a bug to fix here: a
		// position at the top of the document is passed before anything below it has
		// been read. Say so, and leave -at end as the mode that can tell.
		i.res.Doubled = true
	}
	return nil
}

// top is the early injection point, taken before anything is known about the page.
// It cannot be a handler on <body>, because a document does not have to spell that
// tag - most do not - so the position is the first element that a browser would put
// in the body: the body tag itself when the source has one, and otherwise the first
// element that cannot be in the head.
func (i *injector) top(e *lolhtml.Element) error {
	if !i.opts.AtTop || i.res.Present || i.res.Injected {
		return nil
	}
	tag := e.TagName()
	if tag == "body" {
		i.res.Injected = true
		return e.Prepend(i.sprite(), lolhtml.HTML)
	}
	if InHead[tag] {
		return nil
	}
	i.res.Injected = true
	return e.Before(i.sprite(), lolhtml.HTML)
}

// end is the other one, taken when the evidence is in.
func (i *injector) end(d *lolhtml.DocumentEnd) error {
	if i.opts.AtTop || i.res.Present || i.res.Injected || len(i.used) == 0 {
		return nil
	}
	i.res.Injected = true
	return d.Append(i.sprite(), lolhtml.HTML)
}

// sprite is the caller's markup with the marker attribute added, so the next run
// recognises it. The markup is the caller's own and is not escaped: it is markup.
func (i *injector) sprite() string {
	s := strings.TrimSpace(i.opts.Sprite)
	if lower := strings.ToLower(s); strings.HasPrefix(lower, "<svg") && !strings.Contains(lower, i.opts.Marker) {
		return `<svg ` + i.opts.Marker + `="1"` + s[len("<svg"):]
	}
	return s
}

func (i *injector) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("["+i.opts.Marker+"]", i.present),
		lolhtml.OnElement("*", i.top),
		lolhtml.OnElement("img[src]", i.icon),
		lolhtml.OnDocumentEnd(i.end),
	}
}

// strip takes the query and fragment off a URL, which are not part of its name.
func strip(src string) string {
	if k := strings.IndexAny(src, "?#"); k >= 0 {
		return src[:k]
	}
	return src
}

// quoteOnly escapes the one character that could end a double-quoted attribute,
// which is the rule the library applies to an attribute it writes itself. A value
// that came from the document is already source: escaping "&" as well would turn its
// "&amp;" into "&amp;amp;".
func quoteOnly(s string) string { return strings.ReplaceAll(s, `"`, "&quot;") }

// Inject copies src to dst, rewriting icon references and injecting the sprite.
func Inject(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Prefix == "" {
		opts.Prefix = "i-"
	}
	if opts.Class == "" {
		opts.Class = "icon"
	}
	if opts.Marker == "" {
		opts.Marker = "data-sprite"
	}
	i := &injector{opts: opts, seen: map[string]bool{}}
	w, err := lolhtml.NewWriter(dst, i.options()...)
	if err != nil {
		return i.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return i.res, err
	}
	if err := w.Close(); err != nil {
		return i.res, err
	}
	return i.res, nil
}

func main() {
	var opts Options
	file := flag.String("sprite", "", "file holding the sprite markup (required)")
	at := flag.String("at", "end", `where to inject: "end" when needed, or "top" always`)
	flag.StringVar(&opts.Prefix, "prefix", "i-", "symbol id prefix")
	flag.StringVar(&opts.Class, "class", "icon", "class for each generated svg")
	flag.StringVar(&opts.Marker, "marker", "data-sprite", "attribute marking an injected sprite")
	flag.Parse()

	if *file == "" {
		fmt.Fprintln(os.Stderr, "sprite: -sprite is required")
		os.Exit(2)
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sprite:", err)
		os.Exit(2)
	}
	opts.Sprite = string(b)
	opts.AtTop = *at == "top"

	res, err := Inject(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sprite:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

// Command deprecated reports the obsolete elements and attributes a page still uses.
//
//	$ deprecated < page.html
//	element/attribute   uses  first at  class        instead
//	center              12    412       presentation CSS: text-align
//	font                31    455       presentation CSS: font-family, color, font-size
//	image               1     1204      parser-alias <img>: a browser builds img from it
//	@align              8     980       presentation CSS: text-align or vertical-align
//
// It writes no document: the destination is io.Discard and the report goes to stdout,
// so the exit status is about the page. Modernising these is a different program,
// because the replacement for most of them is a stylesheet rather than markup.
//
// # What "deprecated" means here, and why the classes differ
//
// Three kinds, and they are not equally fixable:
//
//	presentation   center, font, big, strike, tt, marquee, blink, nobr, basefont,
//	               spacer, and attributes like align and bgcolor. The replacement is
//	               CSS, so a rewrite cannot do it without knowing the stylesheet.
//	semantic       acronym, dir, isindex, keygen, menuitem, rb, rtc, plaintext,
//	               listing, xmp. Each has a modern element that means the same thing.
//	embedding      applet, frame, frameset, noframes. The replacement is an iframe or
//	               an object, and the layout around it usually has to change too.
//
// # One of them is not what the document says
//
// <image> is a spelling of <img>: the parser renames it, carrying the attributes
// over, so a browser loads the file and runs an onerror if there is one. Measured
// against x/net/html - and the rewriter reports what the document spelled, so a
// selector for img does not match it. That makes it the one entry on this list that
// is a live request rather than a styling problem, and the one that any other rewrite
// keyed on img is also missing. SVG's own image element keeps its name, so telling
// them apart is a namespace check rather than a rename. See
// differential/imagealias_test.go.
//
// # Where, not just what
//
// Each finding carries the byte offset of the element's start tag, from
// [lolhtml.Element.SourceLocation], because a count with no position is a number
// nobody can act on. The offsets are absolute and do not depend on how the input was
// chunked.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Class is how fixable a finding is.
type Class int

const (
	// Presentation is replaced by CSS.
	Presentation Class = iota
	// Semantic has a modern element that means the same thing.
	Semantic
	// Embedding is replaced by an iframe or an object.
	Embedding
	// ParserAlias is a name the parser rewrites into another element.
	ParserAlias
)

func (c Class) String() string {
	switch c {
	case Presentation:
		return "presentation"
	case Semantic:
		return "semantic"
	case Embedding:
		return "embedding"
	}
	return "parser-alias"
}

// advice is what to do about one name. on, when set, is the elements the attribute
// is obsolete on - the names that are ordinary words elsewhere.
type advice struct {
	class Class
	what  string
	on    []string
}

// Elements are the obsolete element names, with what replaces each.
var Elements = map[string]advice{
	"center":   {Presentation, "CSS: text-align", nil},
	"font":     {Presentation, "CSS: font-family, color, font-size", nil},
	"basefont": {Presentation, "CSS on a common ancestor", nil},
	"big":      {Presentation, "CSS: font-size", nil},
	"strike":   {Presentation, "<s> for no-longer-accurate, <del> for removed", nil},
	"tt":       {Presentation, "<code>, <kbd>, <samp> or CSS: font-family", nil},
	"marquee":  {Presentation, "CSS animation, or nothing", nil},
	"blink":    {Presentation, "nothing: it has not blinked since 2013", nil},
	"nobr":     {Presentation, "CSS: white-space: nowrap", nil},
	"spacer":   {Presentation, "CSS: margin or padding", nil},

	"acronym":   {Semantic, "<abbr>", nil},
	"dir":       {Semantic, "<ul>", nil},
	"isindex":   {Semantic, "<form> with an <input>", nil},
	"keygen":    {Semantic, "nothing: the feature was removed", nil},
	"menuitem":  {Semantic, "nothing: the feature was removed", nil},
	"rb":        {Semantic, "<ruby> without it: the content model changed", nil},
	"rtc":       {Semantic, "<rt> inside <ruby>", nil},
	"plaintext": {Semantic, "<pre>, and it cannot be closed", nil},
	"listing":   {Semantic, "<pre>", nil},
	"xmp":       {Semantic, "<pre> with the content escaped", nil},

	"applet":   {Embedding, "<object> or <embed>", nil},
	"frame":    {Embedding, "<iframe>", nil},
	"frameset": {Embedding, "<iframe>, with the layout in CSS", nil},
	"noframes": {Embedding, "nothing: no browser needs it", nil},

	"image": {ParserAlias, "<img>: a browser builds img from it", nil},
}

// Attributes are the obsolete presentational attributes, with the elements they
// appear on. The value is what replaces the attribute.
var Attributes = map[string]advice{
	"align":       {Presentation, "CSS: text-align or vertical-align", nil},
	"valign":      {Presentation, "CSS: vertical-align", nil},
	"bgcolor":     {Presentation, "CSS: background-color", nil},
	"background":  {Presentation, "CSS: background-image", nil},
	"border":      {Presentation, "CSS: border", nil},
	"cellpadding": {Presentation, "CSS: padding on the cells", nil},
	"cellspacing": {Presentation, "CSS: border-spacing", nil},
	"hspace":      {Presentation, "CSS: margin", nil},
	"vspace":      {Presentation, "CSS: margin", nil},
	"nowrap":      {Presentation, "CSS: white-space", nil},
	"clear":       {Presentation, "CSS: clear", nil},
	"compact":     {Presentation, "CSS: margin and line-height", nil},
	"frameborder": {Presentation, "CSS: border on the iframe", nil},
	"marginwidth": {Presentation, "CSS: padding inside the framed document", nil},
	"scrolling":   {Presentation, "CSS: overflow inside the framed document", nil},
	"language":    {Semantic, "nothing: the type attribute, or neither", []string{"script"}},
	"link":        {Presentation, "CSS: a:link", []string{"body"}},
	"vlink":       {Presentation, "CSS: a:visited", []string{"body"}},
	"alink":       {Presentation, "CSS: a:active", []string{"body"}},
	"text":        {Presentation, "CSS: color", []string{"body"}},
}

// Finding is one use of one obsolete name.
type Finding struct {
	Name    string // the element name, or "@attr" for an attribute
	Spelled string // what the document wrote, case and all
	On      string // for an attribute, the element it was on
	Class   Class
	Instead string
	Offset  int
}

// Result is the whole report.
type Result struct {
	Findings []Finding
	Elements int // findings that are elements
	Attrs    int // findings that are attributes
}

// OK reports whether the page uses nothing obsolete.
func (r Result) OK() bool { return len(r.Findings) == 0 }

// Uses groups the findings by name, in the order a report should print them: most
// used first, then by name.
func (r Result) Uses() []Use {
	byName := map[string]*Use{}
	var order []string
	for _, f := range r.Findings {
		u := byName[f.Name]
		if u == nil {
			u = &Use{Name: f.Name, Class: f.Class, Instead: f.Instead, First: f.Offset}
			byName[f.Name] = u
			order = append(order, f.Name)
		}
		u.Count++
		if f.Offset < u.First {
			u.First = f.Offset
		}
	}
	out := make([]Use, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Use is one name and how often the page used it.
type Use struct {
	Name    string
	Count   int
	Class   Class
	Instead string
	First   int
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-19s %-5s %-8s %-12s %s\n", "element/attribute", "uses", "first at", "class", "instead")
	for _, u := range r.Uses() {
		fmt.Fprintf(&b, "%-19s %-5d %-8d %-12s %s\n", u.Name, u.Count, u.First, u.Class, u.Instead)
	}
	return b.String()
}

// selector is the one selector list: every obsolete element, plus every element that
// can carry an obsolete attribute, which is any of them.
func selector() string {
	names := make([]string, 0, len(Elements))
	for name := range Elements {
		names = append(names, name)
	}
	sort.Strings(names)
	// The attribute half needs a wide match, so it is expressed as attribute
	// presence selectors rather than as "*": a document whose elements carry none of
	// them costs nothing per element.
	attrs := make([]string, 0, len(Attributes))
	for name := range Attributes {
		attrs = append(attrs, "["+name+"]")
	}
	sort.Strings(attrs)
	return strings.Join(append(names, attrs...), ",")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type reporter struct {
	res Result
}

func (r *reporter) element(e *lolhtml.Element) error {
	tag := e.TagName()
	at := e.SourceLocation().Start

	if a, obsolete := Elements[tag]; obsolete {
		// An SVG image is a different element with the same name, and it is not
		// obsolete.
		if !(tag == "image" && e.NamespaceURI() != lolhtml.NamespaceHTML) {
			r.res.Findings = append(r.res.Findings, Finding{
				Name: tag, Spelled: e.TagNamePreserveCase(), Class: a.class,
				Instead: a.what, Offset: at,
			})
			r.res.Elements++
		}
	}

	for _, attr := range e.AttributeList() {
		name := strings.ToLower(attr.Name)
		a, obsolete := Attributes[name]
		if !obsolete {
			continue
		}
		if len(a.on) > 0 && !contains(a.on, tag) {
			// A name that is only obsolete on one element - language on a script,
			// text on a body - is an ordinary word anywhere else.
			continue
		}
		r.res.Findings = append(r.res.Findings, Finding{
			Name: "@" + name, Spelled: attr.NamePreserveCase, On: tag,
			Class: a.class, Instead: a.what, Offset: at,
		})
		r.res.Attrs++
	}
	return nil
}

func (r *reporter) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(selector(), r.element)}
}

// Report reads src and reports what is obsolete in it. Nothing is written.
func Report(src io.Reader) (Result, error) {
	r := &reporter{}
	w, err := lolhtml.NewWriter(io.Discard, r.options()...)
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
	class := flag.String("class", "", "report only one class: presentation, semantic, embedding, parser-alias")
	list := flag.Bool("list", false, "one line per use, with its offset, rather than a summary")
	flag.Parse()

	res, err := Report(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "deprecated:", err)
		os.Exit(2)
	}
	if *class != "" {
		var kept []Finding
		for _, f := range res.Findings {
			if f.Class.String() == *class {
				kept = append(kept, f)
			}
		}
		res = Result{Findings: kept}
	}
	if *list {
		for _, f := range res.Findings {
			on := ""
			if f.On != "" {
				on = " on <" + f.On + ">"
			}
			fmt.Printf("%-8d %-12s %s%s -> %s\n", f.Offset, f.Class, f.Spelled, on, f.Instead)
		}
	} else {
		fmt.Print(res)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

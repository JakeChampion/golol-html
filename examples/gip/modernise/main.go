// Command modernise replaces obsolete elements with the markup that means the same
// thing, moving their presentation into classes.
//
//	<center>x</center>              ->  <div class="m-center">x</div>
//	<font color="red">x</font>      ->  <span class="m-color-red">x</span>
//	<acronym title="t">WWW</acronym> ->  <abbr title="t">WWW</abbr>
//	<image src="a.png">             ->  <img src="a.png">
//
// The classes are names, not styles: this program cannot know a page's stylesheet, so
// it prints the CSS its classes need on stderr and leaves adding it to the caller.
// That is the whole reason presentational markup is hard to modernise - the meaning
// is in the rendering - and a rewrite that invented inline styles instead would trade
// one problem for a worse one.
//
// # A rename changes how the element's content is parsed
//
// [lolhtml.Element.SetTagName] writes over the tag and nothing else. The content
// inside it is not re-parsed - the rewriter has already decided what those tokens are
// - but whoever reads the output parses it under the new name's content model, and
// that model can move the content or throw it away. Measured against x/net/html:
//
//	<div><p>x</p></div>       renamed to table    the p is fostered out of it
//	<div><p>x</p><span>y</span></div>  to select  both elements are gone, text merged
//	<xmp><b>x</b></xmp>       renamed to pre      the text becomes an element
//
// No error, nothing odd in the output. So a modernising rewrite is only safe where
// the new element's content model accepts what the old one held, and this program
// renames only within that set: center, marquee, big, strike, tt, nobr, blink, font
// to div or span, acronym to abbr, dir to ul, listing and xmp to pre - the last two
// carrying a warning, because their content was text and becomes markup. See
// differential/rename_test.go.
//
// # What it will not do
//
//   - frameset, frame and noframes: the replacement is an iframe and a layout, which
//     is a rewrite of the page rather than of an element.
//   - applet, keygen, isindex, menuitem, spacer, basefont: nothing modern means the
//     same thing, so they are reported and left.
//   - plaintext: nothing closes it, so a rename leaves the new element open to the
//     end of the document.
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

// rename is what one obsolete element becomes.
type rename struct {
	// to is the new tag name.
	to string
	// class, when set, is added to the new element - the presentation the old name
	// carried.
	class string
	// warn, when set, is why the caller should look at the result.
	warn string
}

// Renames are the elements this program rewrites. Every entry is a rename whose new
// content model accepts what the old element held, which is the condition that makes
// a rename safe: see the file comment.
var Renames = map[string]rename{
	"center":  {to: "div", class: "center"},
	"marquee": {to: "div", class: "marquee", warn: "the animation is gone; the class needs CSS or nothing"},
	"big":     {to: "span", class: "big"},
	"strike":  {to: "s", class: ""},
	"tt":      {to: "code", class: ""},
	"nobr":    {to: "span", class: "nowrap"},
	"blink":   {to: "span", class: "blink", warn: "no browser has blinked since 2013"},
	"acronym": {to: "abbr", class: ""},
	"dir":     {to: "ul", class: ""},
	"font":    {to: "span", class: ""}, // its classes come from its attributes
	"listing": {to: "pre", class: "", warn: "its content was text and is now markup"},
	"xmp":     {to: "pre", class: "", warn: "its content was text and is now markup"},
	"image":   {to: "img", class: ""}, // a spelling of img: the parser renames it anyway
}

// Left are the obsolete elements this program will not rename, with why.
var Left = map[string]string{
	"frameset":  "the replacement is an iframe and a layout",
	"frame":     "the replacement is an iframe and a layout",
	"noframes":  "no browser needs it",
	"applet":    "nothing modern means the same thing",
	"keygen":    "the feature was removed",
	"isindex":   "the feature was removed",
	"menuitem":  "the feature was removed",
	"spacer":    "CSS margin, which needs the stylesheet",
	"basefont":  "CSS on a common ancestor, which needs the stylesheet",
	"plaintext": "nothing closes it, so a rename would leave the new element open",
}

// FontAttributes are font's presentation attributes and the class each becomes. The
// value is part of the class name, because "red" and "blue" are different styles.
var FontAttributes = []string{"color", "face", "size"}

// AttributeClasses are the presentational attributes this program turns into classes,
// on any element. The value is part of the name for the same reason.
var AttributeClasses = []string{"align", "valign", "bgcolor", "nowrap", "clear"}

// Options are the decisions a caller gets to make.
type Options struct {
	// Prefix goes in front of every class this program writes.
	Prefix string
	// Attributes turns the presentational attributes into classes as well as the
	// elements. It is off by default because the attribute set is larger and the
	// CSS is more likely to collide with a page's own.
	Attributes bool
}

// Result is what happened.
type Result struct {
	Renamed  int
	Classes  int            // classes added
	Left     int            // obsolete elements left alone
	Warnings []string       // one per element that needs looking at
	CSS      map[string]int // class name to uses, for the stylesheet the caller needs
}

func (r Result) String() string {
	return fmt.Sprintf("modernise: renamed %d elements, added %d classes in %d rules; left %d alone, %d warnings",
		r.Renamed, r.Classes, len(r.CSS), r.Left, len(r.Warnings))
}

// Stylesheet is the CSS the classes need, in the order a caller should read it. The
// declarations are the obvious ones; a caller who disagrees has the class names.
func (r Result) Stylesheet(prefix string) string {
	names := make([]string, 0, len(r.CSS))
	for name := range r.CSS {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, ".%s { %s }  /* %d uses */\n", name, declaration(strings.TrimPrefix(name, prefix)), r.CSS[name])
	}
	return b.String()
}

// declaration is the CSS one class name is for. A class carrying a value - color-red,
// align-left - is split back into a property and a value, and a value that is all hex
// digits gets its "#" back, since that is what it was in the document.
func declaration(name string) string {
	if decl, ok := Declarations[name]; ok {
		return decl
	}
	prop, value, found := strings.Cut(name, "-")
	if !found {
		return "/* the page's own styling */"
	}
	if prop == "size" {
		// font's size was 1 to 7, or a relative "+2", and none of those is a CSS
		// length. The keyword scale is the closest thing, so it is named rather
		// than guessed at.
		if kw, ok := FontSizes[value]; ok {
			return "font-size: " + kw
		}
		return "font-size: /* was font size " + value + " */"
	}
	css, ok := Properties[prop]
	if !ok {
		return "/* the page's own styling */"
	}
	return css + ": " + cssValue(value)
}

// Properties are the CSS properties the value-carrying classes stand for.
var Properties = map[string]string{
	"align":   "text-align",
	"valign":  "vertical-align",
	"bgcolor": "background-color",
	"clear":   "clear",
	"color":   "color",
	"face":    "font-family",
	"size":    "font-size",
	"nowrap":  "white-space",
}

// FontSizes maps font's size attribute to the CSS keyword scale, which is what the
// specification says those numbers meant.
var FontSizes = map[string]string{
	"1": "x-small", "2": "small", "3": "medium", "4": "large",
	"5": "x-large", "6": "xx-large", "7": "xxx-large",
}

// cssValue puts back what slug took off: a hash in front of a hex colour, and a
// keyword otherwise. A font size was 1 to 7 and has no direct equivalent, so it is
// left for the caller to decide.
func cssValue(v string) string {
	if v == "" {
		return "/* the value the document had */"
	}
	hex := len(v) == 3 || len(v) == 6
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			hex = false
			break
		}
	}
	if hex {
		return "#" + v
	}
	return v
}

// Declarations are what each class this program writes is for. Keyed without the
// caller's prefix.
var Declarations = map[string]string{
	"center":  "text-align: center",
	"big":     "font-size: larger",
	"nowrap":  "white-space: nowrap",
	"blink":   "/* nothing: it has not blinked since 2013 */",
	"marquee": "/* an animation, if the page still wants one */",
}

type moderniser struct {
	opts Options
	res  Result
}

func (m *moderniser) element(e *lolhtml.Element) error {
	tag := e.TagName()

	if tag == "image" && e.NamespaceURI() != lolhtml.NamespaceHTML {
		return nil // an SVG image is its own element
	}

	if why, left := Left[tag]; left {
		m.res.Left++
		m.res.Warnings = append(m.res.Warnings, "<"+tag+"> left alone: "+why)
		return nil
	}

	var classes []string
	if m.opts.Attributes {
		for _, name := range AttributeClasses {
			v, ok := e.Attribute(name)
			if !ok {
				continue
			}
			classes = append(classes, m.class(name+"-"+slug(v)))
			if err := e.RemoveAttribute(name); err != nil {
				return err
			}
		}
	}

	r, renaming := Renames[tag]
	if renaming {
		if tag == "font" {
			for _, name := range FontAttributes {
				v, ok := e.Attribute(name)
				if !ok {
					continue
				}
				classes = append(classes, m.class(name+"-"+slug(v)))
				if err := e.RemoveAttribute(name); err != nil {
					return err
				}
			}
		}
		if r.class != "" {
			classes = append(classes, m.class(r.class))
		}
		if r.warn != "" {
			m.res.Warnings = append(m.res.Warnings, "<"+tag+"> -> <"+r.to+">: "+r.warn)
		}
	}

	if len(classes) > 0 {
		if err := m.addClasses(e, classes); err != nil {
			return err
		}
	}
	if renaming {
		m.res.Renamed++
		return e.SetTagName(r.to)
	}
	return nil
}

// addClasses adds to the element's class attribute rather than replacing it, since a
// page's own classes are the ones its stylesheet knows about.
func (m *moderniser) addClasses(e *lolhtml.Element, classes []string) error {
	existing, _ := e.Attribute("class")
	fields := strings.Fields(existing)
	for _, c := range classes {
		if !contains(fields, c) {
			fields = append(fields, c)
			m.res.Classes++
			if m.res.CSS == nil {
				m.res.CSS = map[string]int{}
			}
			m.res.CSS[c]++
		}
	}
	return e.SetAttribute("class", strings.Join(fields, " "))
}

func (m *moderniser) class(name string) string { return m.opts.Prefix + name }

// slug turns an attribute value into something that can be a class name, keeping
// enough of it to tell two values apart.
func slug(v string) string {
	v = strings.TrimSpace(v)
	// A relative font size is a different value from an absolute one, and the sign
	// is the only thing that says so.
	if strings.HasPrefix(v, "+") {
		v = "plus" + v[1:]
	} else if strings.HasPrefix(v, "-") {
		v = "minus" + v[1:]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '#':
			// A colour, which is the common case: keep the digits and drop the hash.
		case b.Len() > 0:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "unset"
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// selector is one selector list: every name this program acts on, plus the elements
// carrying an attribute it turns into a class.
func (m *moderniser) selector() string {
	names := make([]string, 0, len(Renames)+len(Left))
	for name := range Renames {
		names = append(names, name)
	}
	for name := range Left {
		names = append(names, name)
	}
	sort.Strings(names)
	if m.opts.Attributes {
		attrs := make([]string, 0, len(AttributeClasses))
		for _, name := range AttributeClasses {
			attrs = append(attrs, "["+name+"]")
		}
		sort.Strings(attrs)
		names = append(names, attrs...)
	}
	return strings.Join(names, ",")
}

func (m *moderniser) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(m.selector(), m.element)}
}

// Modernise copies src to dst, replacing obsolete elements with modern ones.
func Modernise(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Prefix == "" {
		opts.Prefix = "m-"
	}
	m := &moderniser{opts: opts}
	w, err := lolhtml.NewWriter(dst, m.options()...)
	if err != nil {
		return m.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return m.res, err
	}
	if err := w.Close(); err != nil {
		return m.res, err
	}
	return m.res, nil
}

func main() {
	var opts Options
	flag.StringVar(&opts.Prefix, "prefix", "m-", "prefix for every class written")
	flag.BoolVar(&opts.Attributes, "attributes", false, "turn presentational attributes into classes too")
	css := flag.Bool("css", false, "write the stylesheet the classes need to stdout instead of the document")
	flag.Parse()

	var dst io.Writer = os.Stdout
	if *css {
		dst = io.Discard
	}
	res, err := Modernise(dst, os.Stdin, opts)
	if *css {
		fmt.Print(res.Stylesheet(opts.Prefix))
	}
	fmt.Fprintln(os.Stderr, res)
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "  "+w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "modernise:", err)
		os.Exit(2)
	}
}

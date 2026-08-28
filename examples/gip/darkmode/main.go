// Command darkmode adds the two things a page needs to respect a reader's colour
// preference: a theme-color meta per scheme, and a stylesheet link that only
// loads in dark mode.
//
//	darkmode -dark '#101014' -light '#ffffff' -stylesheet /dark.css page.html
//	<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
//	<meta name="theme-color" content="#101014" media="(prefers-color-scheme: dark)">
//	<link rel="stylesheet" href="/dark.css" media="(prefers-color-scheme: dark)">
//
// A page that already declares a media-qualified theme-color keeps it, because
// the page has thought about this and the flags have not. A bare theme-color with
// no media is a different case: it applies to both schemes, which is what this
// program exists to replace, so it is reported and left for the operator to
// remove rather than silently overridden.
//
// The colours are checked before they go anywhere. A theme-color that a browser
// cannot parse is ignored, which looks identical to the meta being absent, so an
// unparseable one is refused rather than emitted.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type adder struct {
	dark       string
	light      string
	stylesheet string

	inserted []string
	skipped  map[string]int
}

func (a *adder) note(reason string) {
	if a.skipped == nil {
		a.skipped = map[string]int{}
	}
	a.skipped[reason]++
}

func defaults() *adder { return &adder{} }

func (a *adder) validate() error {
	if a.dark == "" && a.light == "" && a.stylesheet == "" {
		return fmt.Errorf("nothing to add: give -dark, -light or -stylesheet")
	}
	for name, colour := range map[string]string{"-dark": a.dark, "-light": a.light} {
		if colour == "" {
			continue
		}
		if !validColour(colour) {
			return fmt.Errorf("%s %q is not a colour a browser will parse: use a "+
				"hex value like #101014, or one of the CSS named colours", name, colour)
		}
	}
	if a.stylesheet != "" {
		if _, err := url.Parse(a.stylesheet); err != nil {
			return fmt.Errorf("-stylesheet %q is not a url: %w", a.stylesheet, err)
		}
	}
	return nil
}

// validColour accepts what a theme-color is allowed to be, narrowly: a hex
// value or a named colour. Not rgb() or colour functions - they are valid CSS
// and a needless amount of parsing for a flag, and refusing is honest about
// what has been checked.
func validColour(s string) bool {
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3, 4, 6, 8:
		default:
			return false
		}
		for i := 0; i < len(hex); i++ {
			c := hex[i] | 0x20 // lower-case
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return true
	}
	// A name has to be one a browser knows. Accepting any run of letters would put
	// a typo straight into the meta, and a theme-color a browser cannot parse is
	// ignored - which looks exactly like the meta being absent, so the tool would
	// report success for a page it had not changed the appearance of at all. That
	// is the failure the package comment says is refused rather than emitted, and
	// only the list refuses it.
	return namedColours[strings.ToLower(s)]
}

// namedColours is the CSS <named-color> keyword list, which is what a browser will
// parse and nothing else. Case does not matter in CSS, so lookups are lower-cased.
// "transparent" and "currentcolor" are deliberately absent: they are separate
// keywords rather than named colours, and neither is a useful theme-color.
var namedColours = map[string]bool{
	"aliceblue": true, "antiquewhite": true, "aqua": true, "aquamarine": true, "azure": true,
	"beige": true, "bisque": true, "black": true, "blanchedalmond": true, "blue": true,
	"blueviolet": true, "brown": true, "burlywood": true, "cadetblue": true, "chartreuse": true,
	"chocolate": true, "coral": true, "cornflowerblue": true, "cornsilk": true, "crimson": true,
	"cyan": true, "darkblue": true, "darkcyan": true, "darkgoldenrod": true, "darkgray": true,
	"darkgreen": true, "darkgrey": true, "darkkhaki": true, "darkmagenta": true,
	"darkolivegreen": true, "darkorange": true, "darkorchid": true, "darkred": true,
	"darksalmon": true, "darkseagreen": true, "darkslateblue": true, "darkslategray": true,
	"darkslategrey": true, "darkturquoise": true, "darkviolet": true, "deeppink": true,
	"deepskyblue": true, "dimgray": true, "dimgrey": true, "dodgerblue": true, "firebrick": true,
	"floralwhite": true, "forestgreen": true, "fuchsia": true, "gainsboro": true,
	"ghostwhite": true, "gold": true, "goldenrod": true, "gray": true, "green": true,
	"greenyellow": true, "grey": true, "honeydew": true, "hotpink": true, "indianred": true,
	"indigo": true, "ivory": true, "khaki": true, "lavender": true, "lavenderblush": true,
	"lawngreen": true, "lemonchiffon": true, "lightblue": true, "lightcoral": true,
	"lightcyan": true, "lightgoldenrodyellow": true, "lightgray": true, "lightgreen": true,
	"lightgrey": true, "lightpink": true, "lightsalmon": true, "lightseagreen": true,
	"lightskyblue": true, "lightslategray": true, "lightslategrey": true, "lightsteelblue": true,
	"lightyellow": true, "lime": true, "limegreen": true, "linen": true, "magenta": true,
	"maroon": true, "mediumaquamarine": true, "mediumblue": true, "mediumorchid": true,
	"mediumpurple": true, "mediumseagreen": true, "mediumslateblue": true,
	"mediumspringgreen": true, "mediumturquoise": true, "mediumvioletred": true,
	"midnightblue": true, "mintcream": true, "mistyrose": true, "moccasin": true,
	"navajowhite": true, "navy": true, "oldlace": true, "olive": true, "olivedrab": true,
	"orange": true, "orangered": true, "orchid": true, "palegoldenrod": true, "palegreen": true,
	"paleturquoise": true, "palevioletred": true, "papayawhip": true, "peachpuff": true,
	"peru": true, "pink": true, "plum": true, "powderblue": true, "purple": true,
	"rebeccapurple": true, "red": true, "rosybrown": true, "royalblue": true, "saddlebrown": true,
	"salmon": true, "sandybrown": true, "seagreen": true, "seashell": true, "sienna": true,
	"silver": true, "skyblue": true, "slateblue": true, "slategray": true, "slategrey": true,
	"snow": true, "springgreen": true, "steelblue": true, "tan": true, "teal": true,
	"thistle": true, "tomato": true, "turquoise": true, "violet": true, "wheat": true,
	"white": true, "whitesmoke": true, "yellow": true, "yellowgreen": true,
}

const (
	darkMedia  = "(prefers-color-scheme: dark)"
	lightMedia = "(prefers-color-scheme: light)"
)

func (a *adder) options() []lolhtml.Option {
	// What the page already says. A media-qualified theme-color means the page
	// has an opinion per scheme; a bare one applies to both.
	haveDark, haveLight, haveBare := false, false, false
	haveSheet := false

	placed := false

	return []lolhtml.Option{
		lolhtml.OnElement(`meta[name="theme-color"]`, func(e *lolhtml.Element) error {
			media := strings.ToLower(squash(decoded(attr(e, "media"))))
			switch {
			case strings.Contains(media, "dark"):
				haveDark = true
			case strings.Contains(media, "light"):
				haveLight = true
			default:
				haveBare = true
			}
			return nil
		}),

		// A stylesheet already qualified for dark mode. Matched on the media
		// attribute rather than the href, because the same file can be linked
		// under any name.
		lolhtml.OnElement(`link[rel~="stylesheet"][media]`, func(e *lolhtml.Element) error {
			if strings.Contains(strings.ToLower(decoded(attr(e, "media"))), "dark") {
				haveSheet = true
			}
			return nil
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if end.Name() != "head" {
					// OnEndTag fires on whatever token closed the
					// element, and </head> is optional: when it is left
					// out the head is closed by <body>, and this callback
					// runs against </body> or </html> instead. A position
					// taken from a tag with another name is not in the
					// head, so the tags would land after the body - where
					// a theme-color is read too late to matter and a
					// dark-mode stylesheet is a render-blocking surprise.
					// The <body> handler below is the insertion point for
					// that shape, and it runs first.
					return nil
				}
				if placed {
					return nil
				}
				placed = true
				markup := a.markup(haveDark, haveLight, haveBare, haveSheet)
				if markup == "" {
					return nil
				}
				return end.Before(markup, lolhtml.HTML)
			})
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			// The test is whether the tags have gone in, not whether a head
			// was seen: a document with a head but no </head> arrives here
			// with the head still open, and <body> is where that head ends.
			// Inserting before it lands in the head a parser builds.
			if placed {
				return nil
			}
			placed = true
			markup := a.markup(haveDark, haveLight, haveBare, haveSheet)
			if markup == "" {
				return nil
			}
			return e.Before(markup, lolhtml.HTML)
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !placed {
				a.note("no head and no body to put the tags in")
			}
			return nil
		}),
	}
}

// markup builds what is missing. Every value is escaped for an attribute: the
// element does not exist yet, so nothing else will do it.
func (a *adder) markup(haveDark, haveLight, haveBare, haveSheet bool) string {
	var sb strings.Builder

	add := func(key, markup string) {
		a.inserted = append(a.inserted, key)
		sb.WriteString(markup)
	}

	if haveBare {
		a.note("the page has a theme-color with no media, which applies to both " +
			"schemes; remove it or the browser may prefer it")
	}

	if a.light != "" {
		if haveLight {
			a.note("the page already has a light theme-color")
		} else {
			add("theme-color/light", `<meta name="theme-color" content="`+
				lolhtml.EscapeAttribute(a.light)+`" media="`+lightMedia+`">`)
		}
	}
	if a.dark != "" {
		if haveDark {
			a.note("the page already has a dark theme-color")
		} else {
			add("theme-color/dark", `<meta name="theme-color" content="`+
				lolhtml.EscapeAttribute(a.dark)+`" media="`+darkMedia+`">`)
		}
	}
	if a.stylesheet != "" {
		if haveSheet {
			a.note("the page already links a dark-mode stylesheet")
		} else {
			add("stylesheet", `<link rel="stylesheet" href="`+
				lolhtml.EscapeAttribute(a.stylesheet)+`" media="`+darkMedia+`">`)
		}
	}
	return sb.String()
}

func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }
func squash(s string) string  { return strings.Join(strings.Fields(s), " ") }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (a *adder) run(r io.Reader, w io.Writer) error {
	if err := a.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, a.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func addString(in string, opts ...func(*adder)) (string, *adder, error) {
	a := defaults()
	for _, o := range opts {
		o(a)
	}
	var out bytes.Buffer
	err := a.run(strings.NewReader(in), &out)
	return out.String(), a, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (a *adder) report() string {
	var sb strings.Builder
	if len(a.inserted) == 0 {
		sb.WriteString("inserted nothing\n")
	} else {
		fmt.Fprintf(&sb, "inserted %s\n", strings.Join(a.inserted, ", "))
	}
	reasons := make([]string, 0, len(a.skipped))
	for r := range a.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, a.skipped[r])
	}
	return sb.String()
}

func main() {
	a := defaults()
	flag.StringVar(&a.dark, "dark", "", "theme-color for dark mode")
	flag.StringVar(&a.light, "light", "", "theme-color for light mode")
	flag.StringVar(&a.stylesheet, "stylesheet", "",
		"stylesheet to link under a dark-mode media query")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "darkmode:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: darkmode [-dark C] [-light C] [file.html]")
		os.Exit(2)
	}

	if err := a.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "darkmode:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, a.report())
}

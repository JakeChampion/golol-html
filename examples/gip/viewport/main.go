// Command viewport fixes a missing or harmful viewport meta tag.
//
//	viewport page.html
//	<meta name="viewport" content="width=device-width, initial-scale=1">
//
// There are three cases and they need different treatment, which is the whole
// reason this is not a one-line rewrite.
//
// No viewport at all: a mobile browser assumes a desktop-width page and scales
// it down, so the tag is added.
//
// A viewport that stops the reader zooming: user-scalable=no, or a
// maximum-scale below 2, defeats pinch-to-zoom. That is an accessibility failure
// rather than a preference, so those directives are removed and the rest of the
// declaration is kept - the page may have said something about width that it
// meant.
//
// A viewport that is merely different: width=1024, say. That is a decision, and
// this program leaves it alone and reports it. Overriding a deliberate choice
// because it is unusual is how a tool becomes something people turn off.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type fixer struct {
	content string // what to insert when there is none
	// minScale is the smallest maximum-scale that still allows useful zooming.
	// The WCAG threshold is 200 per cent.
	minScale float64
	force    bool // replace any viewport, not only a zoom-blocking one

	added    int
	repaired int
	removed  []string
	skipped  map[string]int
}

func (f *fixer) note(reason string) {
	if f.skipped == nil {
		f.skipped = map[string]int{}
	}
	f.skipped[reason]++
}

func defaults() *fixer {
	return &fixer{content: "width=device-width, initial-scale=1", minScale: 2}
}

func (f *fixer) validate() error {
	if f.content == "" {
		return fmt.Errorf("-content cannot be empty: a viewport meta with no content " +
			"is worse than none, because it looks deliberate")
	}
	if f.minScale <= 0 {
		return fmt.Errorf("-min-scale %v is not a scale", f.minScale)
	}
	return nil
}

// A directive is one key-value pair of a viewport content attribute.
type directive struct {
	key   string
	value string
}

// parseContent splits a viewport declaration. Keys are lower-cased, because
// browsers accept "Width = device-width" and so must anything reading it.
//
// Three things separate directives, not one: a comma, a semicolon, and ASCII
// whitespace. Splitting on the comma alone is the natural reading of the syntax
// and it is not what a browser does - "width=device-width; user-scalable=no" is
// a spelling people write, and to a comma-only split it is a single directive
// whose key is "width". The zoom block then goes unseen, the tag is left as it
// is, and the report says the page's viewport does not block zooming: a wrong
// answer that reads like a considered one, which is the case this program exists
// to catch.
//
// Whitespace being a separator is also why this is a scan rather than a Split
// and a Cut: the spaces in "Width = device-width" surround the "=" rather than
// ending the directive, so they have to be skipped there and honoured elsewhere.
func parseContent(content string) []directive {
	var out []directive
	for i := 0; i < len(content); {
		for i < len(content) && isViewportSeparator(content[i]) {
			i++
		}
		if i >= len(content) {
			break
		}
		start := i
		for i < len(content) && content[i] != '=' && !isViewportSeparator(content[i]) {
			i++
		}
		key := strings.ToLower(strings.TrimSpace(content[start:i]))

		// An "=" after any run of spaces belongs to this directive; anything
		// else ends it with no value, which is how "user-scalable" alone reads.
		value := ""
		j := i
		for j < len(content) && isASCIISpace(content[j]) {
			j++
		}
		if j < len(content) && content[j] == '=' {
			j++
			for j < len(content) && isASCIISpace(content[j]) {
				j++
			}
			start = j
			for j < len(content) && !isViewportSeparator(content[j]) {
				j++
			}
			value = strings.TrimSpace(content[start:j])
			i = j
		}
		if key == "" {
			continue
		}
		out = append(out, directive{key: key, value: value})
	}
	return out
}

// isViewportSeparator reports whether a byte ends a directive.
func isViewportSeparator(b byte) bool {
	return b == ',' || b == ';' || isASCIISpace(b)
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return false
}

func formatContent(ds []directive) string {
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		if d.value == "" {
			parts = append(parts, d.key)
			continue
		}
		parts = append(parts, d.key+"="+d.value)
	}
	return strings.Join(parts, ", ")
}

// blocksZoom reports whether a directive stops a reader zooming, and why.
func (f *fixer) blocksZoom(d directive) (string, bool) {
	switch d.key {
	case "user-scalable":
		v := strings.ToLower(d.value)
		if v == "no" || v == "0" || v == "false" {
			return "user-scalable=" + d.value, true
		}
	case "maximum-scale":
		if scale, err := strconv.ParseFloat(d.value, 64); err == nil && scale < f.minScale {
			return "maximum-scale=" + d.value, true
		}
	}
	return "", false
}

func (f *fixer) options() []lolhtml.Option {
	seen := false
	sawHead := false
	placed := false

	return []lolhtml.Option{
		lolhtml.OnElement(`meta[name="viewport"]`, func(e *lolhtml.Element) error {
			if seen {
				// A second viewport is not twice the instruction: a browser
				// takes the first, so a later one is noise that reads as though
				// it applies.
				f.note("a second viewport meta was left alone; a browser uses the first")
				return nil
			}
			seen = true

			content, ok := e.Attribute("content")
			if !ok || strings.TrimSpace(decoded(content)) == "" {
				f.repaired++
				return e.SetAttribute("content", f.content)
			}

			directives := parseContent(decoded(content))
			var kept []directive
			var removed []string
			for _, d := range directives {
				if why, blocks := f.blocksZoom(d); blocks {
					removed = append(removed, why)
					continue
				}
				kept = append(kept, d)
			}

			switch {
			case f.force:
				f.repaired++
				return e.SetAttribute("content", f.content)
			case len(removed) == 0:
				f.note("the page has its own viewport and it does not block zooming")
				return nil
			}

			f.removed = append(f.removed, removed...)
			f.repaired++
			if len(kept) == 0 {
				// Everything it said was about blocking zoom.
				return e.SetAttribute("content", f.content)
			}
			return e.SetAttribute("content", formatContent(kept))
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if placed || seen {
					return nil
				}
				placed = true
				f.added++
				return end.Before(f.markup(), lolhtml.HTML)
			})
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if sawHead || placed || seen {
				return nil
			}
			placed = true
			f.added++
			return e.Before(f.markup(), lolhtml.HTML)
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !seen && !placed {
				f.note("no head and no body to add the viewport to")
			}
			return nil
		}),
	}
}

func (f *fixer) markup() string {
	return `<meta name="viewport" content="` + lolhtml.EscapeAttribute(f.content) + `">`
}

func decoded(s string) string { return stdhtml.UnescapeString(s) }

func (f *fixer) run(r io.Reader, w io.Writer) error {
	if err := f.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, f.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fixString(in string, opts ...func(*fixer)) (string, *fixer, error) {
	f := defaults()
	for _, o := range opts {
		o(f)
	}
	var out bytes.Buffer
	err := f.run(strings.NewReader(in), &out)
	return out.String(), f, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (f *fixer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "added=%d repaired=%d", f.added, f.repaired)
	if len(f.removed) > 0 {
		fmt.Fprintf(&sb, " removed=%s", strings.Join(f.removed, " "))
	}
	sb.WriteString("\n")
	reasons := make([]string, 0, len(f.skipped))
	for r := range f.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, f.skipped[r])
	}
	return sb.String()
}

func main() {
	f := defaults()
	flag.StringVar(&f.content, "content", f.content, "viewport content to add")
	flag.Float64Var(&f.minScale, "min-scale", f.minScale,
		"smallest maximum-scale left in place; below this it is removed")
	flag.BoolVar(&f.force, "force", false,
		"replace any existing viewport, not only one that blocks zooming")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		file, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "viewport:", err)
			os.Exit(1)
		}
		defer file.Close()
		r = file
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: viewport [-force] [file.html]")
		os.Exit(2)
	}

	if err := f.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "viewport:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, f.report())
}

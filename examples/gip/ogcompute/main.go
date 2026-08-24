// Command ogcompute fills in missing Open Graph tags from the page's own first
// heading and first image.
//
//	ogcompute -base https://example.com/p page.html
//	<meta property="og:title" content="Widget &amp; Co">
//	<meta property="og:image" content="https://example.com/w.png">
//
// It only fills gaps. A page that already declares og:title keeps it, because
// the page knows better than a heuristic: this program's guesses are for pages
// that said nothing.
//
// The tags belong in the head and the heading and image are in the body, so this
// cannot be one pass - see the package documentation on insertion positions. It
// reads the document, then rewrites it, and says so in its report. -title and
// -image skip the reading pass for a caller who already knows, which is the same
// arrangement examples/gip/pagenav uses and for the same reason.
//
// What it will not do is invent. An og:image has to be an absolute URL to be any
// use, so without -base a relative one is reported rather than emitted, and a
// heading that is only whitespace is not a title.
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

type filler struct {
	base  string // absolute base for resolving what the page provides
	title string // given rather than computed
	image string
	desc  string

	// found by the reading pass
	haveTitle string
	haveImage string
	present   map[string]bool

	inserted []string
	passes   int
	skipped  map[string]int
}

func (f *filler) note(reason string) {
	if f.skipped == nil {
		f.skipped = map[string]int{}
	}
	f.skipped[reason]++
}

func defaults() *filler { return &filler{present: map[string]bool{}} }

func (f *filler) validate() error {
	if f.base != "" {
		u, err := url.Parse(f.base)
		if err != nil {
			return fmt.Errorf("-base %q is not a url: %w", f.base, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("-base %q is not absolute", f.base)
		}
	}
	if f.image != "" {
		if err := f.checkAbsolute(f.image); err != nil {
			return fmt.Errorf("-image: %w", err)
		}
	}
	return nil
}

func (f *filler) checkAbsolute(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a url: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%q is not absolute; an og:image has to be", raw)
	}
	return nil
}

// given reports whether nothing needs reading from the document.
func (f *filler) given() bool { return f.title != "" && f.image != "" }

// readPass finds the first heading, the first image, and which og properties the
// page already declares. It writes nothing.
func (f *filler) readPass(doc []byte) error {
	f.passes++

	var heading strings.Builder
	collecting := false
	done := false

	_, err := lolhtml.Rewrite(doc,
		// Which og properties the page already has. Read through Attribute
		// rather than the iterator: a meta can carry property twice and the
		// first copy is the one a parser keeps.
		lolhtml.OnElement("meta[property], meta[name]", func(e *lolhtml.Element) error {
			key := strings.ToLower(decoded(attr(e, "property")))
			if key == "" {
				key = strings.ToLower(decoded(attr(e, "name")))
			}
			if strings.HasPrefix(key, "og:") && decoded(attr(e, "content")) != "" {
				f.present[key] = true
			}
			return nil
		}),

		// The first heading, whatever level. Its text arrives after the start
		// tag, so it is gathered and taken at the end tag.
		lolhtml.OnElement("h1, h2, h3", func(e *lolhtml.Element) error {
			if done || !e.CanHaveContent() {
				return nil
			}
			heading.Reset()
			collecting = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				collecting = false
				if text := squash(decoded(heading.String())); text != "" {
					f.haveTitle = text
					done = true
				}
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			if collecting {
				heading.WriteString(tc.Text())
			}
			return nil
		}),

		lolhtml.OnElement("img[src]", func(e *lolhtml.Element) error {
			if f.haveImage != "" {
				return nil
			}
			src := decoded(attr(e, "src"))
			if src == "" {
				return nil
			}
			f.haveImage = src
			return nil
		}),
	)
	return err
}

// resolve makes a URL absolute, or returns "" if it cannot.
func (f *filler) resolve(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "" && u.Host != "" {
		return raw
	}
	if f.base == "" {
		return ""
	}
	base, err := url.Parse(f.base)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

// markup builds the tags to insert, and records what was left out and why.
func (f *filler) markup() string {
	title := f.title
	if title == "" {
		title = f.haveTitle
	}
	image := f.image
	if image == "" {
		image = f.resolve(f.haveImage)
		if image == "" && f.haveImage != "" {
			f.note("the page's first image is a relative url and no -base was given")
		}
	}

	var sb strings.Builder
	add := func(key, value string) {
		if value == "" {
			return
		}
		if f.present[key] {
			f.note("the page already declares " + key)
			return
		}
		f.inserted = append(f.inserted, key)
		sb.WriteString(`<meta property="` + key + `" content="` +
			lolhtml.EscapeAttribute(value) + `">`)
	}

	add("og:title", title)
	add("og:image", image)
	add("og:description", f.desc)
	return sb.String()
}

func (f *filler) writePass(doc []byte, w io.Writer) error {
	f.passes++

	markup := f.markup()
	sawHead := false
	placed := markup == ""

	out, err := lolhtml.NewWriter(w,
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
				return end.Before(markup, lolhtml.HTML)
			})
		}),
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if sawHead || placed {
				return nil
			}
			placed = true
			return e.Before(markup, lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !placed {
				f.note("no head and no body to put the tags in")
			}
			return nil
		}),
	)
	if err != nil {
		return err
	}
	if _, err := out.Write(doc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }
func squash(s string) string  { return strings.Join(strings.Fields(s), " ") }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (f *filler) run(r io.Reader, w io.Writer) error {
	if err := f.validate(); err != nil {
		return err
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if !f.given() {
		if err := f.readPass(doc); err != nil {
			return err
		}
	}
	return f.writePass(doc, w)
}

func fillString(in string, opts ...func(*filler)) (string, *filler, error) {
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

func (f *filler) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "passes=%d inserted=%s", f.passes, strings.Join(f.inserted, ","))
	if len(f.inserted) == 0 {
		fmt.Fprintf(&sb, "none")
	}
	reasons := make([]string, 0, len(f.skipped))
	for r := range f.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	sb.WriteString("\n")
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, f.skipped[r])
	}
	return sb.String()
}

func main() {
	f := defaults()
	flag.StringVar(&f.base, "base", "", "absolute base url for resolving the page's image")
	flag.StringVar(&f.title, "title", "", "og:title, skipping the reading pass for it")
	flag.StringVar(&f.image, "image", "", "og:image, absolute; skips the reading pass for it")
	flag.StringVar(&f.desc, "description", "", "og:description, which cannot be computed")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		file, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "ogcompute:", err)
			os.Exit(1)
		}
		defer file.Close()
		r = file
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: ogcompute [-base URL] [file.html]")
		os.Exit(2)
	}

	if err := f.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ogcompute:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, f.report())
}

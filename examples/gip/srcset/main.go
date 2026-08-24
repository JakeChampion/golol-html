// Command srcset builds a responsive srcset for every image from its src, a
// width list and an image CDN template.
//
//	srcset -widths 320,640,1280 -cdn '/cdn?u={url}&w={w}' < page.html > out.html
//
// An image that already has a srcset is left alone. One with no width to size
// against gets a sizes attribute only if the caller supplied one, because
// guessing a layout is how a responsive image ends up loading the wrong file.
//
// ESI parsing is on by default. These pages are usually assembled at the edge,
// so they contain <esi:include> tags written without a self-closing slash, and
// without -esi=false those are treated as containers rather than void elements:
// replacing or removing one then swallows the enclosing element's end tag. The
// symptom is malformed output rather than an error, which is why this defaults
// the other way from the library.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	widths := flag.String("widths", "320,640,1024,1600", "comma-separated widths")
	cdn := flag.String("cdn", "/cdn?u={url}&w={w}", "CDN template with {url} and {w}")
	sizes := flag.String("sizes", "", "sizes attribute to add, empty to add none")
	esi := flag.Bool("esi", true, "parse ESI tags as void elements")
	flag.Parse()

	b := &builder{cdn: *cdn, sizes: *sizes, esi: *esi}
	for _, w := range strings.Split(*widths, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(w))
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "srcset: bad width %q\n", w)
			os.Exit(2)
		}
		b.widths = append(b.widths, n)
	}
	sort.Ints(b.widths)

	if !strings.Contains(b.cdn, "{url}") || !strings.Contains(b.cdn, "{w}") {
		fmt.Fprintln(os.Stderr, "srcset: -cdn must contain {url} and {w}")
		os.Exit(2)
	}

	if err := b.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "srcset:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, b.report())
}

type builder struct {
	widths []int
	cdn    string
	sizes  string
	esi    bool

	built     int
	kept      int
	skipped   []string
	esiVoided int
}

func (b *builder) run(src io.Reader, dst io.Writer) error {
	opts := b.options()
	if b.esi {
		opts = append(opts, lolhtml.WithESITags())
	}
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (b *builder) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("img[src]", func(e *lolhtml.Element) error {
			if _, ok := e.Attribute("srcset"); ok {
				b.kept++
				return nil
			}
			src, ok := e.Attribute("src")
			if !ok || strings.TrimSpace(src) == "" {
				return nil
			}
			if reason := unsuitable(src); reason != "" {
				b.skipped = append(b.skipped, src+" ("+reason+")")
				return nil
			}

			b.built++
			if err := e.SetAttribute("srcset", b.srcsetFor(src)); err != nil {
				return err
			}
			if b.sizes == "" {
				return nil
			}
			if _, ok := e.Attribute("sizes"); ok {
				return nil
			}
			return e.SetAttribute("sizes", b.sizes)
		}),

		// An esi:include is a void element only when ESI parsing is on. Counting
		// them makes the difference visible in the report rather than only in
		// malformed output, which is how this was noticed at all.
		lolhtml.OnElement("esi\\:include", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				b.esiVoided++
			}
			return nil
		}),
	}
}

// srcsetFor renders one candidate per width, smallest first, which is the order
// a browser reads most cheaply.
func (b *builder) srcsetFor(src string) string {
	parts := make([]string, 0, len(b.widths))
	for _, w := range b.widths {
		parts = append(parts, fmt.Sprintf("%s %dw", b.rendered(src, w), w))
	}
	return strings.Join(parts, ", ")
}

// rendered fills the template. The URL is percent-encoded because it becomes a
// query parameter of another URL, and a raw & in it would end the parameter.
func (b *builder) rendered(src string, w int) string {
	out := strings.ReplaceAll(b.cdn, "{url}", url.QueryEscape(src))
	return strings.ReplaceAll(out, "{w}", strconv.Itoa(w))
}

// unsuitable names why an image cannot be resized by a CDN, or "" if it can.
func unsuitable(src string) string {
	s := strings.TrimSpace(strings.ToLower(src))
	switch {
	case strings.HasPrefix(s, "data:"):
		return "already inline"
	case strings.HasSuffix(s, ".svg"):
		return "vector, resizing is pointless"
	case strings.HasSuffix(s, ".gif"):
		return "animation would be lost"
	}
	if _, err := url.Parse(strings.TrimSpace(src)); err != nil {
		return "unparseable"
	}
	return ""
}

func (b *builder) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "srcset built=%d kept=%d skipped=%d esi-void=%d\n",
		b.built, b.kept, len(b.skipped), b.esiVoided)
	for _, s := range b.skipped {
		fmt.Fprintf(&sb, "skipped: %s\n", s)
	}
	return sb.String()
}

func buildString(in string, opts ...func(*builder)) (string, *builder, error) {
	b := &builder{widths: []int{320, 640}, cdn: "/cdn?u={url}&w={w}", esi: true}
	for _, o := range opts {
		o(b)
	}
	var out bytes.Buffer
	err := b.run(strings.NewReader(in), &out)
	return out.String(), b, err
}

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
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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
			raw, ok := e.Attribute("src")
			if !ok || strings.TrimSpace(raw) == "" {
				return nil
			}
			// Decoded before anything is built from it. An attribute value is
			// reported as raw source with its character references still encoded,
			// so the src of <img src="/img.php?id=1&amp;size=2"> is the eleven
			// characters "...&amp;size", not "...&size". Percent-encoding that
			// straight into the CDN template asks the CDN for a URL nobody would
			// ever have fetched: u=%2Fimg.php%3Fid%3D1%26amp%3Bsize%3D2. Writing
			// an & as &amp; is the correct way to spell a query URL in HTML, so
			// this is the ordinary case rather than an exotic one.
			src := decodeAttribute(raw)
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

// decodeAttribute decodes the character references in an attribute value, by the
// rule that holds inside an attribute rather than the one html.UnescapeString
// applies everywhere.
//
// They differ by one clause, and it is the clause a URL runs into: a named
// reference written without its semicolon is not a reference at all when the
// character after it is "=" or ASCII alphanumeric. So "/img.php?id=1&copy=2" is a
// URL with a parameter called copy, and html.UnescapeString reads it as one with a
// copyright sign in it - which would be percent-encoded into the CDN request as a
// copyright sign, a different image from the one the page shows.
//
// Element.Attribute states the rule, examples/gip/references implements it in full
// including the text context and what to leave encoded, and examples/gip/modulesplit
// carries the same attribute half this does.
func decodeAttribute(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n := attributeReference(s[i:])
		if n == 0 {
			// Not a reference here: a bare ampersand, an unknown name, or a
			// name without its semicolon before "=" or an alphanumeric.
			b.WriteByte('&')
			i++
			continue
		}
		b.WriteString(stdhtml.UnescapeString(s[i : i+n]))
		i += n
	}
	return b.String()
}

// attributeReference returns the length of the character reference at the start
// of s, or 0 when what is there is not one in an attribute value.
func attributeReference(s string) int {
	if len(s) < 2 || s[0] != '&' {
		return 0
	}
	if s[1] == '#' {
		// A numeric reference is a reference with or without its semicolon in
		// both contexts; the clause below is about named ones only.
		end, start := 2, 2
		if end < len(s) && (s[end] == 'x' || s[end] == 'X') {
			end++
			start = end
			for end < len(s) && isHex(s[end]) {
				end++
			}
		} else {
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
		}
		if end == start {
			return 0 // "&#" or "&#x" with no digits
		}
		if end < len(s) && s[end] == ';' {
			end++
		}
		return end
	}
	run := 1
	for run < len(s) && isAlnum(s[run]) {
		run++
	}
	// Names are matched longest-first and the match can be a prefix of the run:
	// "&copy2" is the copyright sign followed by a "2", so the name ends where
	// the longest known one ends and the rule below looks at the character after
	// that rather than after the run.
	end := 0
	for k := run; k > 1; k-- {
		if known(s[1:k]) {
			end = k
			break
		}
	}
	if end == 0 {
		return 0 // a bare ampersand, or no name the table has
	}
	if end < len(s) && s[end] == ';' {
		return end + 1
	}
	if end < len(s) && (s[end] == '=' || isAlnum(s[end])) {
		return 0 // the attribute rule
	}
	return end
}

// known reports whether the table has exactly this name. The standard library's
// decoder is the table - asking it beats carrying a copy of 2231 names - but it
// matches the longest prefix and leaves the rest, so "&copy2;" comes back as the
// copyright sign followed by "2;". An exact name is one whose decoded form is the
// character alone, and no reference in the table stands for more than two code
// points, which is the test.
func known(name string) bool {
	in := "&" + name + ";"
	decoded := stdhtml.UnescapeString(in)
	return decoded != in && utf8.RuneCountInString(decoded) <= 2
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
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

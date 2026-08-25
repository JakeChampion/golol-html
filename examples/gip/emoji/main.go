// Command emoji expands :shortcodes: in a document's text.
//
// The interesting part is not the substitution, it is what the substitution puts
// into a document whose encoding cannot hold it.
//
// A handler always works in UTF-8, and content it inserts is encoded on the way
// out into the document's encoding. An emoji has no windows-1252 spelling, so it
// comes out as a numeric character reference - "&#128512;" - which is the same
// character to anything that decodes references and eight characters to anything
// that does not. This program reports which of the two happened, because a caller
// serving a legacy-encoded page should know that its emoji are references rather
// than characters.
//
// And in two positions there is no reference to emit, so the insertion is refused
// rather than encoded: a tag name and a comment's text. This program touches
// neither, which is worth saying because it is why it cannot fail that way - a
// shortcode inside a comment is left alone, not because comments are not prose but
// because expanding it in a legacy encoding would fail the rewrite.
//
// The rest is the discipline shared with the other text programs: match over the
// accumulated node because a shortcode can be split across chunks, skip the
// elements where a replacement would corrupt rather than help, and write the text
// back as Text so nothing inserted can become markup.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// shortcode matches :name: with the characters a shortcode may contain.
var shortcode = regexp.MustCompile(`:([a-z0-9_+-]{1,40}):`)

// table is the shortcodes this program knows. Small on purpose: the point is the
// substitution, not the vocabulary.
var table = map[string]string{
	"smile":       "\U0001F604",
	"grin":        "\U0001F600",
	"heart":       "❤️",
	"thumbsup":    "\U0001F44D",
	"wave":        "\U0001F44B",
	"tada":        "\U0001F389",
	"check":       "✅",
	"warning":     "⚠️",
	"coffee":      "☕",
	"family":      "\U0001F468‍\U0001F469‍\U0001F467",
	"thumbsup_t2": "\U0001F44D\U0001F3FC",
}

// skipped elements hold content that is not prose, and where a reference emitted
// for an unrepresentable character would be eight characters of nonsense rather
// than an emoji.
var skipped = map[string]bool{
	"script": true, "style": true, "title": true, "textarea": true,
	"template": true, "noscript": true, "iframe": true, "xmp": true,
	"noembed": true, "noframes": true, "option": true, "select": true,
	"code": true, "pre": true, "kbd": true, "samp": true,
}

// A Result says what was expanded and how it came out.
type Result struct {
	// Expanded counts the shortcodes replaced, by name.
	Expanded map[string]int
	// Unknown counts the :things: that looked like shortcodes and are not.
	Unknown int
	// AsReferences is set when the output encoding could not hold the
	// characters and the library emitted numeric references instead.
	AsReferences bool
}

// Total is every expansion.
func (r Result) Total() int {
	n := 0
	for _, c := range r.Expanded {
		n += c
	}
	return n
}

func (r Result) String() string {
	names := make([]string, 0, len(r.Expanded))
	for n := range r.Expanded {
		names = append(names, n)
	}
	sort.Strings(names)
	form := "characters"
	if r.AsReferences {
		form = "numeric references, because the document's encoding cannot hold them"
	}
	return fmt.Sprintf("%d expansions as %s (%s), %d unknown shortcodes",
		r.Total(), form, strings.Join(names, ", "), r.Unknown)
}

// Expand copies src to dst with shortcodes replaced. encoding is the document's,
// as WithEncoding takes it.
func Expand(dst io.Writer, src io.Reader, encoding string) (Result, error) {
	res := Result{Expanded: map[string]int{}}
	depth := 0
	var node strings.Builder

	opts := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if !skipped[tag] || !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != tag {
					return nil
				}
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if depth > 0 {
				return nil
			}
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			source := node.String()
			node.Reset()

			text, expanded, unknown := expand(stdhtml.UnescapeString(source))
			for name, n := range expanded {
				res.Expanded[name] += n
			}
			res.Unknown += unknown
			if len(expanded) == 0 {
				// Nothing expanded, so the node goes back as it came.
				return t.Replace(source, lolhtml.HTML)
			}
			// As Text: the library escapes what would be markup and encodes
			// what the document's encoding can hold, emitting a numeric
			// reference for what it cannot.
			return t.Replace(text, lolhtml.Text)
		}),
	}
	if encoding != "" {
		opts = append(opts, lolhtml.WithEncoding(encoding))
	}

	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}
	res.AsReferences = res.Total() > 0 && !representable(encoding)
	return res, nil
}

// representable reports whether the encoding can hold the characters this
// program inserts. Only a Unicode encoding can, and utf-8 is the only one the
// library's default and this program's vocabulary need.
func representable(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return true
	}
	return false
}

// expand replaces the shortcodes it knows and counts what it did.
func expand(text string) (string, map[string]int, int) {
	expanded := map[string]int{}
	unknown := 0
	out := shortcode.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		emoji, ok := table[name]
		if !ok {
			unknown++
			return m
		}
		expanded[name]++
		return emoji
	})
	return out, expanded, unknown
}

func main() {
	encoding := ""
	if len(os.Args) > 1 {
		encoding = os.Args[1]
	}
	res, err := Expand(os.Stdout, os.Stdin, encoding)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emoji:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "emoji:", res)
	// A sanity check worth making in the program rather than only in its tests:
	// every value in the table has to be valid UTF-8, since the library takes
	// inserted content as UTF-8 and would refuse it otherwise.
	for name, v := range table {
		if !utf8.ValidString(v) {
			fmt.Fprintf(os.Stderr, "emoji: the entry for %q is not valid UTF-8\n", name)
			os.Exit(1)
		}
	}
}

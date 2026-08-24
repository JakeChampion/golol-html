// Command toc builds a table of contents from a document's headings and inserts
// it at a marker.
//
//	toc -marker '#toc' < page.html > out.html
//
// It reads the document twice, and that is the point rather than an oversight.
// A table of contents at a marker depends on headings that come after the
// marker, and one streaming pass cannot insert content it has not seen yet: a
// StreamFunc is called at the position where its content belongs, not deferred
// until the end, so a sink registered at the marker runs before the first
// heading is parsed and would emit an empty list.
//
// So: pass one collects the headings and writes nothing, pass two inserts. The
// input has to be re-readable, which is why this takes a file rather than a
// pipe. If it cannot be re-read, the choices are to buffer the whole document,
// or to put the contents at the end with -at-end, which one pass can do.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	marker := flag.String("marker", "#toc", "selector for the element to fill")
	atEnd := flag.Bool("at-end", false, "append the contents at the document end, in one pass")
	minLevel := flag.Int("min", 2, "shallowest heading level to include")
	maxLevel := flag.Int("max", 4, "deepest heading level to include")
	flag.Parse()

	if *minLevel < 1 || *maxLevel > 6 || *minLevel > *maxLevel {
		fmt.Fprintln(os.Stderr, "toc: -min and -max must satisfy 1 <= min <= max <= 6")
		os.Exit(2)
	}

	b := &tocBuilder{marker: *marker, minLevel: *minLevel, maxLevel: *maxLevel}

	if *atEnd {
		if err := b.onePass(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "toc:", err)
			os.Exit(1)
		}
		fmt.Fprint(os.Stderr, b.report())
		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: toc [-marker sel] file.html > out.html")
		fmt.Fprintln(os.Stderr, "       toc -at-end < in.html > out.html")
		os.Exit(2)
	}

	if err := b.twoPass(flag.Arg(0), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "toc:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, b.report())
}

type entry struct {
	level int
	text  string
	id    string
	// start is the heading's offset in the input. Both passes read the same
	// bytes, so it is an exact key for matching a heading in pass two to the id
	// pass one computed for it - which ordinal position is not, because a
	// heading with no text is skipped in the contents but still seen here.
	start int
}

type tocBuilder struct {
	marker             string
	minLevel, maxLevel int

	entries []entry
	// open tracks the heading currently being read, so its text can be
	// accumulated across chunks and its id assigned at its end tag.
	open  *entry
	text  strings.Builder
	ids   map[string]int
	found bool
}

// headingSelector matches the levels the caller asked for. Building it from the
// range rather than matching all six and filtering keeps the selector doing the
// work.
func (b *tocBuilder) headingSelector() string {
	parts := make([]string, 0, 6)
	for l := b.minLevel; l <= b.maxLevel; l++ {
		parts = append(parts, "h"+strconv.Itoa(l))
	}
	return strings.Join(parts, ", ")
}

// collect registers the handlers that gather headings. Both passes use it: pass
// one to learn the headings, pass two to learn where they are so the ids match.
func (b *tocBuilder) collect() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(b.headingSelector(), func(e *lolhtml.Element) error {
			level, err := strconv.Atoi(strings.TrimPrefix(e.TagName(), "h"))
			if err != nil {
				return nil
			}
			b.text.Reset()
			b.open = &entry{level: level, start: e.SourceLocation().Start}
			if id, ok := e.Attribute("id"); ok && strings.TrimSpace(id) != "" {
				b.open.id = id
			}

			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				open := b.open
				b.open = nil
				if open == nil {
					return nil
				}
				open.text = strings.Join(strings.Fields(b.text.String()), " ")
				if open.text == "" {
					return nil
				}
				if open.id == "" {
					open.id = b.uniqueID(open.text)
				}
				b.entries = append(b.entries, *open)
				return nil
			})
		}),

		lolhtml.OnText(b.headingSelector(), func(t *lolhtml.TextChunk) error {
			if b.open != nil {
				b.text.WriteString(t.Text())
			}
			return nil
		}),
	}
}

// assign adds the id pass one computed to any heading that had none, so the
// contents links resolve.
//
// An end-tag handler would be the natural place - the text is complete by then -
// but an EndTag cannot set an attribute and the element wrapper is already
// detached. So the id has to be known when the start tag is seen, which is
// exactly why there are two passes.
func (b *tocBuilder) assign(collected []entry) lolhtml.Option {
	byStart := make(map[int]string, len(collected))
	for _, e := range collected {
		byStart[e.start] = e.id
	}

	return lolhtml.OnElement(b.headingSelector(), func(e *lolhtml.Element) error {
		if _, ok := e.Attribute("id"); ok {
			return nil
		}
		id, ok := byStart[e.SourceLocation().Start]
		if !ok {
			// A heading with no text: not in the contents, so nothing to link.
			return nil
		}
		return e.SetAttribute("id", id)
	})
}

// uniqueID slugs the heading text, disambiguating repeats, so two headings
// called "Overview" do not both claim the same anchor.
func (b *tocBuilder) uniqueID(text string) string {
	// Decoded first: the text is raw source, so "Configure &amp; run" would
	// otherwise slug as "configure-amp-run".
	var sb strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(stdhtml.UnescapeString(text)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				sb.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(sb.String(), "-")
	if slug == "" {
		slug = "section"
	}

	if b.ids == nil {
		b.ids = map[string]int{}
	}
	b.ids[slug]++
	if n := b.ids[slug]; n > 1 {
		slug = fmt.Sprintf("%s-%d", slug, n)
	}
	return slug
}

// twoPass reads the file once to collect headings and once to write the result.
func (b *tocBuilder) twoPass(path string, dst io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Pass one: collect, discard the output.
	w, err := lolhtml.NewWriter(io.Discard, b.collect()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, f); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	collected := b.entries
	b.entries = nil
	b.ids = nil

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("this input cannot be read twice, so -at-end is the only option: %w", err)
	}

	// Pass two: insert. The headings are collected again so the ids assigned to
	// the document are the ones the contents links to.
	opts := append(b.collect(), b.assign(collected))
	opts = append(opts, lolhtml.OnElement(b.marker, func(e *lolhtml.Element) error {
		b.found = true
		return e.SetInnerContent(renderList(collected), lolhtml.HTML)
	}))

	w, err = lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, f); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// onePass appends the contents at the document end, which is the one placement a
// single pass can manage: by then every heading has been seen.
func (b *tocBuilder) onePass(src io.Reader, dst io.Writer) error {
	opts := append(b.collect(), lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		if len(b.entries) == 0 {
			return nil
		}
		b.found = true
		return d.Append("\n<nav class=\"toc\">"+renderList(b.entries)+"</nav>\n", lolhtml.HTML)
	}))

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

// renderList builds a nested list, with each sublist inside the <li> it belongs
// to rather than beside it.
//
// The heading text is written through unescaped, and that is deliberate: text
// read from the document arrives as raw source, with character references still
// encoded. Escaping it again turns "Configure &amp; run" into
// "Configure &amp;amp; run", which is what an earlier version of this did.
// Re-emitting source text as HTML round-trips, because a literal < could not
// have been text in the first place.
//
// The id cannot be written through the same way, because it is being put inside
// quotes this function chose. See the comment where it is written.
func renderList(entries []entry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	base := entries[0].level
	depth := 0 // open sublists beyond the outermost
	openLi := false

	sb.WriteString("<ul>")
	for _, e := range entries {
		want := e.level - base
		if want < 0 {
			want = 0
		}

		for depth < want {
			// A sublist belongs inside a list item. When the document skips a
			// level - an h2 followed by an h4 - there is no item to put it in,
			// so an empty one holds it rather than emitting a <ul> inside a <ul>.
			if !openLi {
				sb.WriteString("<li>")
			}
			sb.WriteString("<ul>")
			depth++
			openLi = false
		}
		for depth > want {
			if openLi {
				sb.WriteString("</li>")
			}
			sb.WriteString("</ul></li>")
			depth--
			openLi = false
		}
		if openLi {
			sb.WriteString("</li>")
		}

		// The id is escaped and the text is not, and the difference is the
		// point. Both arrive as raw source, but the id is going into an
		// attribute this function is quoting itself, and a single-quoted id in
		// the document may hold a bare double quote: a heading with
		// id='a" onmouseover="alert(1)' put a working event handler in the
		// table of contents. Decoded first and escaped after, so an id of
		// "a&amp;b" does not come out as "a&amp;amp;b".
		fmt.Fprintf(&sb, `<li><a href="#%s">%s</a>`,
			lolhtml.EscapeAttribute(stdhtml.UnescapeString(e.id)), e.text)
		openLi = true
	}

	if openLi {
		sb.WriteString("</li>")
	}
	for depth > 0 {
		sb.WriteString("</ul></li>")
		depth--
	}
	sb.WriteString("</ul>")
	return sb.String()
}

func (b *tocBuilder) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "headings=%d marker-found=%v\n", len(b.entries), b.found)
	if len(b.entries) > 0 && !b.found {
		fmt.Fprintf(&sb, "note: no element matched %q, so the contents were not inserted\n", b.marker)
	}
	return sb.String()
}

func onePassString(in string, opts ...func(*tocBuilder)) (string, *tocBuilder, error) {
	b := &tocBuilder{marker: "#toc", minLevel: 2, maxLevel: 4}
	for _, o := range opts {
		o(b)
	}
	var out bytes.Buffer
	err := b.onePass(strings.NewReader(in), &out)
	return out.String(), b, err
}

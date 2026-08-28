// Command breadcrumb emits schema.org BreadcrumbList JSON-LD derived from a
// page's own breadcrumb nav.
//
//	breadcrumb -base https://example.com/ page.html
//	<script type="application/ld+json">{"@context":"https://schema.org",...}</script>
//
// The script goes immediately after the nav it was built from, which is the one
// position that needs no second pass: JSON-LD is honoured anywhere in the
// document, and by the nav's end tag every item has been seen. A rewrite that
// wanted it in the head would have to buffer, for the reason the package
// documentation gives under insertion positions.
//
// The JSON is built by hand rather than with encoding/json, and that is the
// interesting part of the program. Two escaping problems apply at once:
//
// The content lands inside a <script>, which is raw text, so a "</script>" in it
// would close the element. The answer is JSON's own escaping: "/" written as
// "\/" inside a string. That is why encoding/json is not used here - it does not
// escape the slash.
//
// Only one of this program's two paths has the library behind it, and which one
// turns on where the insertion is written rather than on what it contains.
// -placeholder fills a page-supplied <script> with SetInnerContent, which is an
// insertion into the element's own content and so is checked: bad JSON would be
// refused with ErrRawTextBreakout rather than rendered. The default path builds
// the whole element as a string and writes it with EndTag.After, outside the
// nav, where a "</script>" is ordinary markup and the library deliberately does
// not look - what is checked is the position, not the type. So on that path the
// escaping below is the only thing standing between a crumb name and a broken
// document, and a program less sure of its own escaping would run
// lolhtml.CheckRawText over the JSON itself, which is what that function is
// exported for.
//
// The values are also document-derived, so each needs escaping for JSON itself -
// quotes, backslashes and control characters - before the slash rule is applied.
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
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// An item is one crumb: its visible text and, except usually for the last, a
// link.
type item struct {
	name string
	href string
}

type builder struct {
	selector    string // where the breadcrumb links are
	base        string // resolved against, so relative hrefs become absolute
	maxItems    int
	placeholder bool // fill a page-supplied placeholder instead of inserting

	items   []item
	emitted int
	skipped map[string]int
}

func (b *builder) note(reason string) {
	if b.skipped == nil {
		b.skipped = map[string]int{}
	}
	b.skipped[reason]++
}

func defaults() *builder {
	return &builder{
		selector: "nav.breadcrumb, nav[aria-label=breadcrumb], .breadcrumbs",
		maxItems: 20,
	}
}

func (b *builder) validate() error {
	if b.selector == "" {
		return fmt.Errorf("-selector cannot be empty")
	}
	if b.maxItems < 1 {
		return fmt.Errorf("-max %d is not a useful limit", b.maxItems)
	}
	if b.base != "" {
		u, err := url.Parse(b.base)
		if err != nil {
			return fmt.Errorf("-base %q is not a url: %w", b.base, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("-base %q is not absolute", b.base)
		}
	}
	return nil
}

func (b *builder) options() []lolhtml.Option {
	// One breadcrumb nav per document: a second one is more likely a footer
	// duplicate than a second trail, and two BreadcrumbLists contradict.
	done := false
	inNav := 0

	// The current crumb's text, gathered between its start and end tags.
	var text strings.Builder
	var href string
	collecting := false
	filled := false

	opts := []lolhtml.Option{}

	// -placeholder chooses the target up front, and it has to be chosen up
	// front: the nav's end tag comes before a placeholder that follows it, so a
	// program that inserted there and then found a placeholder would have
	// emitted two. Which target to use is something the caller knows and the
	// document cannot be asked about in one pass.
	if b.placeholder {
		opts = append(opts, lolhtml.OnElement(placeholderSelector, func(e *lolhtml.Element) error {
			if filled || !e.CanHaveContent() {
				return nil
			}
			if len(b.items) == 0 {
				b.note("the placeholder appears before the breadcrumb nav, so it " +
					"could not be filled in one pass")
				return nil
			}
			filled = true
			b.emitted++
			return e.SetInnerContent(b.json(), lolhtml.HTML)
		}))
	}

	opts = append(opts,

		lolhtml.OnElement(b.selector, func(e *lolhtml.Element) error {
			if done {
				b.note("a second breadcrumb nav was ignored")
				return nil
			}
			if !e.CanHaveContent() {
				return nil
			}
			// The script is emitted after the nav's end tag, which is after the
			// only chance to notice that a previous run already emitted one -
			// the script is further on again. So the mark goes on the nav
			// itself, at its start tag, where a second run sees it in time.
			//
			// It is set whether or not a script follows: a nav with no crumbs
			// produces none on either run, so skipping it later changes nothing.
			if _, marked := e.Attribute(markAttr); marked {
				done = true
				b.note("this nav was already processed")
				return nil
			}
			if err := e.SetAttribute(markAttr, ""); err != nil {
				return err
			}
			inNav++
			b.items = b.items[:0]

			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				inNav--
				done = true
				if len(b.items) == 0 {
					b.note("the breadcrumb nav had no items")
					return nil
				}
				if b.placeholder {
					// The placeholder handler owns the emission.
					return nil
				}
				b.emitted++
				return end.After(b.script(), lolhtml.HTML)
			})
		}),

		// Each link inside the nav is a crumb. The text is the name and arrives
		// after the start tag, so the href waits for the end tag to pair them.
		//
		// The selector is built with descendants of every alternative, not by
		// appending to the string. -selector is a selector list, and
		// b.selector+" a" would attach the " a" to its last alternative only -
		// which silently made the nav itself match this handler and produced a
		// one-item breadcrumb containing the whole trail as one name.
		lolhtml.OnElement(descendants(b.selector, "a", "span"), func(e *lolhtml.Element) error {
			if inNav == 0 || !e.CanHaveContent() {
				return nil
			}
			// A crumb is usually spelled with one element inside another -
			// <a href="/"><span>Home</span></a> is the schema.org shape - so
			// this selector matches twice for a single crumb. The state below
			// is shared by every match, so starting a new crumb here would
			// reset the text the enclosing element had begun and overwrite its
			// href with the inner element's absent one: the name is emitted
			// once by the span's end tag and again, unlinked, by the a's.
			// A match inside a crumb joins it instead. Its text is already
			// being collected, and its href fills in for an enclosing element
			// that had none, which is the <span><a href=...> spelling.
			if collecting {
				if href == "" {
					href = stdhtml.UnescapeString(strings.TrimSpace(attr(e, "href")))
				}
				return nil
			}
			href = stdhtml.UnescapeString(strings.TrimSpace(attr(e, "href")))
			text.Reset()
			collecting = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				collecting = false
				name := squash(stdhtml.UnescapeString(text.String()))
				if name == "" {
					return nil
				}
				if len(b.items) >= b.maxItems {
					b.note("the breadcrumb had more items than -max")
					return nil
				}
				b.items = append(b.items, item{name: name, href: b.resolve(href)})
				return nil
			})
		}),

		lolhtml.OnText(b.selector, func(tc *lolhtml.TextChunk) error {
			if collecting {
				text.WriteString(tc.Text())
			}
			return nil
		}),
	)
	return opts
}

// descendants builds a selector matching the named children of every alternative
// in a selector list. Appending to the list as a string only extends its last
// alternative, which is a quiet way to match the wrong elements.
func descendants(list string, tags ...string) string {
	var out []string
	for _, alt := range strings.Split(list, ",") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		for _, tag := range tags {
			out = append(out, alt+" "+tag)
		}
	}
	return strings.Join(out, ", ")
}

// resolve makes a crumb's href absolute when a base was given. An href that
// cannot be resolved is dropped rather than emitted relative: a BreadcrumbList
// with a relative item is worse than one with the item's position only.
func (b *builder) resolve(href string) string {
	if href == "" {
		return ""
	}
	if b.base == "" {
		return href
	}
	base, err := url.Parse(b.base)
	if err != nil {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

// squash collapses runs of whitespace, because breadcrumb markup is usually
// indented and the name would otherwise carry the indentation.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// markAttr is set on the nav this program has processed, so a second run over
// its own output adds nothing. It has to be on the nav rather than on the script
// for the reason given where it is set.
const markAttr = "data-breadcrumb-json"

// placeholderSelector is the element this program prefers to fill: a page that
// emits one gets the library's own check on the content, since filling it is an
// insertion into a script rather than the insertion of a script.
const placeholderSelector = `script[type="application/ld+json"][data-breadcrumb]`

// script builds the whole element, for a page with no placeholder.
func (b *builder) script() string {
	return `<script type="application/ld+json">` + b.json() + `</script>`
}

// json builds the BreadcrumbList. It is assembled by hand: see the note at the
// top of the file for why encoding/json is not used.
func (b *builder) json() string {
	var sb strings.Builder
	sb.WriteString(`{"@context":"https://schema.org","@type":"BreadcrumbList",`)
	sb.WriteString(`"itemListElement":[`)
	for i, it := range b.items {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"@type":"ListItem","position":%d,"name":`, i+1)
		sb.WriteString(jsonString(it.name))
		if it.href != "" {
			sb.WriteString(`,"item":`)
			sb.WriteString(jsonString(it.href))
		}
		sb.WriteByte('}')
	}
	sb.WriteString(`]}`)
	return sb.String()
}

// jsonString writes a JSON string literal, escaping "/" as "\/" as well as the
// characters JSON requires.
//
// The slash is the one that matters here and it is not JSON's idea: it is what
// keeps a "</script>" in a value from closing the element the JSON is being put
// inside. encoding/json does not do it, which is why this function exists.
func jsonString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			sb.WriteString(`\"`)
			i++
		case c == '\\':
			sb.WriteString(`\\`)
			i++
		case c == '/':
			// Not required by JSON, required by the element this lands in.
			sb.WriteString(`\/`)
			i++
		case c == '\n':
			sb.WriteString(`\n`)
			i++
		case c == '\r':
			sb.WriteString(`\r`)
			i++
		case c == '\t':
			sb.WriteString(`\t`)
			i++
		case c < 0x20:
			fmt.Fprintf(&sb, `\u%04x`, c)
			i++
		case c < utf8.RuneSelf:
			sb.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				// Not valid UTF-8. A JSON string has to be, so the byte becomes
				// the replacement character rather than corrupting the document.
				sb.WriteString(`�`)
				i++
				continue
			}
			sb.WriteString(s[i : i+size])
			i += size
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (b *builder) run(r io.Reader, w io.Writer) error {
	if err := b.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, b.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func buildString(in string, opts ...func(*builder)) (string, *builder, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	var out bytes.Buffer
	err := b.run(strings.NewReader(in), &out)
	return out.String(), b, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (b *builder) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "emitted=%d items=%d", b.emitted, len(b.items))
	reasons := make([]string, 0, len(b.skipped))
	for r := range b.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, b.skipped[r])
	}
	return sb.String()
}

func main() {
	b := defaults()
	flag.StringVar(&b.selector, "selector", b.selector, "selector for the breadcrumb nav")
	flag.StringVar(&b.base, "base", "", "absolute base url for resolving crumb hrefs")
	flag.IntVar(&b.maxItems, "max", b.maxItems, "maximum number of crumbs")
	flag.BoolVar(&b.placeholder, "placeholder", false,
		"fill a page-supplied "+placeholderSelector+" instead of inserting a script")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "breadcrumb:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: breadcrumb [-base URL] [file.html]")
		os.Exit(2)
	}

	if err := b.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "breadcrumb:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, b.report())
}

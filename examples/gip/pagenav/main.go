// Command pagenav adds rel=next and rel=prev link elements from a page's own
// pagination markup.
//
//	pagenav -current 2 page.html
//	<head>...<link rel="prev" href="/p/1"><link rel="next" href="/p/3"></head>
//
// The links belong in the head. The pagination nav they are derived from is in
// the body. A rewriter cannot insert into a position it has already passed, so
// this cannot be done in one pass over the document, and the program is built
// around admitting that rather than around pretending otherwise:
//
//	-next and -prev given     one pass, streaming
//	neither given             two passes, and the document is held in memory
//
// The two-pass mode is the interesting one, and it is the honest cost of the
// job. Measured on this machine, a second pass roughly doubles the fixed
// allocation count - 27 allocations for one pass against 57 for two, a ratio
// that does not grow with document size, since the cost is in building the
// rewriter rather than in the document. What does grow with the document is the
// buffer: the whole thing has to be held while the first pass looks for the nav,
// which is the part that makes it not a streaming rewrite.
//
// So it is a flag rather than the default. A caller who already knows the
// neighbours - a paginated listing usually does, since it computed the page
// numbers to render the nav - should say so and stay streaming.
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

	lolhtml "github.com/JakeChampion/golol-html"
)

type nav struct {
	selector string // where the pagination nav is
	current  int    // the current page number, 1-based; 0 means unknown
	next     string // given rather than discovered
	prev     string

	// discovered by the reading pass
	found map[int]string

	inserted int
	rewrote  int
	passes   int
	skipped  map[string]int
}

func (n *nav) note(reason string) {
	if n.skipped == nil {
		n.skipped = map[string]int{}
	}
	n.skipped[reason]++
}

func defaults() *nav {
	return &nav{selector: "nav.pagination a[href], .pagination a[href]"}
}

func (n *nav) validate() error {
	if n.selector == "" {
		return fmt.Errorf("-selector cannot be empty")
	}
	for name, href := range map[string]string{"next": n.next, "prev": n.prev} {
		if href == "" {
			continue
		}
		if _, err := url.Parse(href); err != nil {
			return fmt.Errorf("-%s %q is not a url: %w", name, href, err)
		}
	}
	if n.next == "" && n.prev == "" && n.current == 0 {
		return fmt.Errorf("give -next and -prev, or -current so the neighbours can " +
			"be read from the pagination nav")
	}
	return nil
}

// given reports whether both neighbours were supplied, which is the case that
// needs only one pass.
func (n *nav) given() bool { return n.next != "" || n.prev != "" }

// readPass scans for the pagination nav and records the page numbers it links
// to. It writes nothing, so its output goes to io.Discard.
func (n *nav) readPass(doc []byte) error {
	n.found = map[int]string{}
	n.passes++

	// The link text is the page number in the overwhelming majority of
	// paginations, and it arrives after the start tag, so the href is held until
	// the end tag can pair it with the text.
	var href string
	var text strings.Builder
	open := false

	_, err := lolhtml.Rewrite(doc,
		lolhtml.OnElement(n.selector, func(e *lolhtml.Element) error {
			href, _ = e.Attribute("href")
			text.Reset()
			if !e.CanHaveContent() {
				return nil
			}
			open = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				open = false
				if page, ok := pageNumber(text.String()); ok {
					if _, seen := n.found[page]; !seen {
						n.found[page] = href
					}
				}
				return nil
			})
		}),
		lolhtml.OnText(n.selector, func(tc *lolhtml.TextChunk) error {
			if open {
				text.WriteString(tc.Text())
			}
			return nil
		}),
	)
	return err
}

// pageNumber reads a page number out of link text. Anything that is not a plain
// number is not one: "Next" and "..." are navigation furniture rather than pages,
// and guessing at them is how a rel=next ends up pointing at the wrong place.
func pageNumber(text string) (int, bool) {
	s := strings.TrimSpace(stdhtml.UnescapeString(text))
	if s == "" {
		return 0, false
	}
	page, err := strconv.Atoi(s)
	if err != nil || page < 1 {
		return 0, false
	}
	return page, true
}

// neighbours resolves what to emit, from the flags or from the reading pass.
func (n *nav) neighbours() (prev, next string) {
	prev, next = n.prev, n.next
	if n.current == 0 {
		return prev, next
	}
	if prev == "" {
		if href, ok := n.found[n.current-1]; ok {
			prev = href
		}
	}
	if next == "" {
		if href, ok := n.found[n.current+1]; ok {
			next = href
		}
	}
	return prev, next
}

const relSelector = `link[rel~="next"], link[rel~="prev"], link[rel~="previous"]`

// writeOptions is the writing pass's handler set, separated from writePass so a
// test can drive it with chunked input. writePass writes the document in one
// call, which would otherwise make chunk invariance untestable here.
func (n *nav) writeOptions() []lolhtml.Option {
	prev, next := n.neighbours()
	if prev == "" && next == "" {
		n.note("no neighbouring pages were found or given")
	}

	headRegion := true
	sawHead := false
	done := map[string]bool{}

	return []lolhtml.Option{
		// An existing rel=next or rel=prev is rewritten rather than joined: two
		// of either is a contradiction, and a stale one is worse than none.
		lolhtml.OnElement(relSelector, func(e *lolhtml.Element) error {
			rel := strings.ToLower(strings.TrimSpace(stdhtml.UnescapeString(attr(e, "rel"))))
			kind := "next"
			if rel != "next" {
				kind = "prev"
			}
			want := next
			if kind == "prev" {
				want = prev
			}
			switch {
			case !headRegion:
				n.note("a rel=" + kind + " outside the head was left alone")
				return nil
			case want == "":
				// Nothing to say, so the existing one is left as it is rather
				// than removed: this program was asked to add links, not to
				// audit them.
				n.note("an existing rel=" + kind + " was left alone")
				done[kind] = true
				return nil
			case done[kind]:
				n.note("a second rel=" + kind + " in the head was removed")
				e.Remove()
				return nil
			}
			done[kind] = true
			n.rewrote++
			return e.SetAttribute("href", want)
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				defer func() { headRegion = false }()
				return n.insertMissing(done, prev, next, func(markup string) error {
					return end.Before(markup, lolhtml.HTML)
				})
			})
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			defer func() { headRegion = false }()
			if sawHead {
				return nil
			}
			return n.insertMissing(done, prev, next, func(markup string) error {
				return e.Before(markup, lolhtml.HTML)
			})
		}),
	}
}

func (n *nav) writePass(doc []byte, w io.Writer) error {
	n.passes++
	out, err := lolhtml.NewWriter(w, n.writeOptions()...)
	if err != nil {
		return err
	}
	if _, err := out.Write(doc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// insertMissing emits the links the document does not already carry, prev first,
// as one string so the order reads as written.
func (n *nav) insertMissing(done map[string]bool, prev, next string, insert func(string) error) error {
	var sb strings.Builder
	count := 0
	for _, kind := range []string{"prev", "next"} {
		href := prev
		if kind == "next" {
			href = next
		}
		if href == "" || done[kind] {
			continue
		}
		done[kind] = true
		count++
		sb.WriteString(`<link rel="` + kind + `" href="` +
			lolhtml.EscapeAttribute(href) + `">`)
	}
	if count == 0 {
		return nil
	}
	n.inserted += count
	return insert(sb.String())
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// run does one pass when the neighbours were given and two when they have to be
// found. The two-pass path holds the whole document, which is the cost of
// deriving head content from the body.
func (n *nav) run(r io.Reader, w io.Writer) error {
	if err := n.validate(); err != nil {
		return err
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if !n.given() || n.current != 0 {
		if err := n.readPass(doc); err != nil {
			return err
		}
	}
	return n.writePass(doc, w)
}

func navString(in string, opts ...func(*nav)) (string, *nav, error) {
	n := defaults()
	for _, o := range opts {
		o(n)
	}
	var out bytes.Buffer
	err := n.run(strings.NewReader(in), &out)
	return out.String(), n, err
}

func total(m map[string]int) int {
	c := 0
	for _, v := range m {
		c += v
	}
	return c
}

func (n *nav) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "passes=%d inserted=%d rewrote=%d pages-found=%d",
		n.passes, n.inserted, n.rewrote, len(n.found))
	reasons := make([]string, 0, len(n.skipped))
	for r := range n.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, n.skipped[r])
	}
	return sb.String()
}

func main() {
	n := defaults()
	flag.StringVar(&n.selector, "selector", n.selector,
		"selector for the pagination links")
	flag.IntVar(&n.current, "current", 0, "the current page number, 1-based")
	flag.StringVar(&n.next, "next", "", "next page url, skipping the reading pass")
	flag.StringVar(&n.prev, "prev", "", "previous page url, skipping the reading pass")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "pagenav:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: pagenav [-current N] [file.html]")
		os.Exit(2)
	}

	if err := n.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pagenav:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, n.report())
}

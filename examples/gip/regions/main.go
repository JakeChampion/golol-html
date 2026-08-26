// Command regions applies a different set of handlers to each region of one document, split at
// offsets the caller gives, and refuses a split that would change what the document means.
//
//	$ regions -at 214 -at 8192 page.html
//	3 regions
//	  0..214       head rules, 214 bytes in, 231 out
//	  214..8192    body rules, 7978 bytes in, 8104 out
//	  8192..end    footer rules, 4110 bytes in, 4110 out
//
//	$ regions -at 25 wrapped.html
//	regions: the boundary at 25 is not a place to cut: something is still open there - an
//	  element whose end tag is on the other side, or an unfinished tag, comment or raw-text
//	  element - so the two regions would not rewrite as the document does
//
// # Cut where nothing is open
//
// A rewriter is a tokenizer rather than a tree builder, so most of what a region sees does not
// depend on what encloses it: a start tag, a text chunk and a comment are the same tokens alone as
// they are inside the document. A region beginning in the middle of a table reports the same `td`
// that the whole document reports, and a tree builder would disagree.
//
// End tags are the exception, and they are the reason the rule is what it is. An end tag pairs with
// a start tag, so an element that spans a boundary is split: the region before the boundary never
// meets the end tag, and the region after meets an end tag with nothing open to match. Any handler
// registered through [lolhtml.Element.OnEndTag] on such an element runs in neither half.
//
// So a boundary is safe when nothing is open at it. Measured with a probe that acts on every kind
// of unit - elements, end tags, text, comments and the doctype - and comparing the two halves
// against the whole:
//
//	boundary                                          safe
//	between two top-level divs                        yes
//	before the second <td> of a table row             no - the tr and the table are open
//	before the second <option> of a select            no - the select is open
//	before the second <li> of a list                  no - the ul is open
//	inside a script, style, textarea or title          no
//	inside a comment, a tag, or a doctype              no
//
// The second, third and fourth rows are safe for handlers that never touch an end tag and unsafe
// for handlers that do, which is why the check uses a probe that touches everything: the answer is
// then about the boundary rather than about one caller's handlers, and each region here has its
// own.
//
// # The cheap test answers a different question
//
// The test the package documentation gives for joining fragments - append a sentinel element to the
// prefix and see whether a handler for it runs - answers whether the prefix *swallows* what
// follows. That is necessary and not sufficient. A prefix ending in a bare "<" or "</" swallows
// nothing, because "<" followed by "<" is text, and is still a bad place to split: the tail then
// begins with what should have been a tag name and reads as text. Measured over every offset of a
// document holding a script, a comment and an ordinary element, four offsets pass the sentinel test
// and change the document, and every one of them is a prefix ending in "<" or "</".
//
// So [Absorbs] is the cheap pre-filter and [SafeBoundary] is the answer.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Region is a span of the document and the handlers to apply to it.
type Region struct {
	Name       string
	Start, End int
	Opts       []lolhtml.Option
	// In and Out are the sizes, filled in by Rewrite.
	In, Out int
}

// sentinelTag is a name no document will hold, and sentinel the markup appended to test whether a
// prefix ends with the tokenizer in its ordinary state.
const (
	sentinelTag = "x-boundary-9f3"
	sentinel    = "<" + sentinelTag + "></" + sentinelTag + ">"
)

// ErrBoundary says a split would change what the document means.
type ErrBoundary struct {
	At int
}

func (e *ErrBoundary) Error() string {
	return fmt.Sprintf("the boundary at %d is not a place to cut: something is still open "+
		"there - an element whose end tag is on the other side, or an unfinished tag, "+
		"comment or raw-text element - so the two regions would not rewrite as the "+
		"document does", e.At)
}

// Absorbs reports whether the prefix of doc up to offset would swallow what follows it, by the test
// the package documentation gives for joining fragments: append a sentinel element and see whether
// a handler for it runs.
//
// It is necessary for a safe boundary and not sufficient, which is worth knowing because the two
// questions look like one. A prefix ending in a bare "<" or "</" swallows nothing - the sentinel
// is recognised, because "<" followed by "<" is text - and is still not a place to split, since the
// tail then begins with what should have been a tag name and reads as text. Measured over every
// offset of a document holding a script, a comment and an ordinary element, four offsets pass this
// test and change the document, and all four are a prefix ending in "<" or "</".
//
// So this is the cheap pre-filter and [SafeBoundary] is the answer.
func Absorbs(doc string, offset int) bool {
	if offset <= 0 || offset >= len(doc) {
		return false
	}
	seen := false
	if _, err := lolhtml.RewriteString(doc[:offset]+sentinel,
		lolhtml.OnElement(sentinelTag, func(*lolhtml.Element) error {
			seen = true
			return nil
		})); err != nil {
		return true
	}
	return !seen
}

// probe acts on every kind of unit a handler can be registered for, so that a boundary which
// changes the tokenising changes the output. A weaker probe answers a narrower question and calls
// boundaries safe that are not: one that ignores end tags calls a split inside `</div` safe, because
// an end tag has nothing a probe on elements alone would alter.
//
// The point of covering every kind is that the answer is then about the boundary rather than about
// the caller's handlers - which is what the regions need, since each region has its own.
func probe() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if err := e.SetAttribute(sentinelTag, "1"); err != nil {
				return err
			}
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.Before("<!--e-->", lolhtml.HTML)
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				return c.After("<!--t-->", lolhtml.HTML)
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText(c.Text() + "~")
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			d.Remove()
			return nil
		}),
	}
}

// SafeBoundary reports whether doc can be split at offset without changing what either side means.
//
// It answers exactly, by rewriting both ways with a probe that touches everything and comparing:
// if the two halves produce what the whole produces, the boundary is a token boundary. That costs
// two extra parses of the document per boundary, which is the price of an answer rather than an
// estimate, and a boundary is decided once.
func SafeBoundary(doc string, offset int) bool {
	if offset <= 0 || offset >= len(doc) {
		return true
	}
	whole, err := lolhtml.RewriteString(doc, probe()...)
	if err != nil {
		return false
	}
	head, err := lolhtml.RewriteString(doc[:offset], probe()...)
	if err != nil {
		return false
	}
	tail, err := lolhtml.RewriteString(doc[offset:], probe()...)
	if err != nil {
		return false
	}
	return head+tail == whole
}

// Rewrite applies each region's handlers to its own span and writes the result.
func Rewrite(doc string, regions []Region, dst io.Writer) ([]Region, error) {
	if len(regions) == 0 {
		return nil, errors.New("regions: none")
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Start < regions[j].Start })

	// Every boundary is checked before anything is written, because a document half written
	// with the wrong parse is worse than one not written.
	for _, r := range regions {
		if !SafeBoundary(doc, r.Start) {
			return nil, &ErrBoundary{At: r.Start}
		}
	}

	out := make([]Region, len(regions))
	for i, r := range regions {
		end := r.End
		if end <= 0 || end > len(doc) {
			end = len(doc)
		}
		if r.Start > end {
			return out, fmt.Errorf("regions: %s starts at %d and ends at %d",
				r.Name, r.Start, end)
		}
		piece := doc[r.Start:end]

		counted := &counting{w: dst}
		w, err := lolhtml.NewWriter(counted, r.Opts...)
		if err != nil {
			return out, err
		}
		if _, err := w.Write([]byte(piece)); err != nil {
			w.Close()
			return out, fmt.Errorf("rewriting %s: %w", r.Name, err)
		}
		if err := w.Close(); err != nil {
			return out, fmt.Errorf("closing %s: %w", r.Name, err)
		}
		r.In, r.Out = len(piece), counted.n
		out[i] = r
	}
	return out, nil
}

// counting records how many bytes went through.
type counting struct {
	w io.Writer
	n int
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// Report describes what each region did.
func Report(regions []Region, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d region%s\n", len(regions), plural(len(regions)))
	for _, r := range regions {
		end := fmt.Sprint(r.End)
		if r.End <= 0 || r.End >= total {
			end = "end"
		}
		fmt.Fprintf(&b, "  %-14s %s, %d bytes in, %d out\n",
			fmt.Sprintf("%d..%s", r.Start, end), r.Name, r.In, r.Out)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// The three handler sets, which do visibly different things so the regions are told apart in the
// output rather than only in the report.
func headRules() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-region", "head")
	})}
}

func bodyRules() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})}
}

func footerRules() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-region", "footer")
	})}
}

type offsets []int

func (o *offsets) String() string { return "" }

func (o *offsets) Set(s string) error {
	// ParseInt rather than Sscanf, which stops at the first character it does not
	// understand and reports success - so "1.5" would split at byte 1 and "12x" at 12. A
	// boundary somewhere nobody asked for is the one thing this program must not do.
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fmt.Errorf("%q is not an offset", s)
	}
	*o = append(*o, n)
	return nil
}

func main() {
	var at offsets
	flag.Var(&at, "at", "a byte offset to split at, repeatable")
	report := flag.Bool("report", true, "print the regions to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "regions:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}
	doc, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "regions:", err)
		os.Exit(1)
	}

	rules := [][]lolhtml.Option{headRules(), bodyRules(), footerRules()}
	names := []string{"head rules", "body rules", "footer rules"}
	sort.Ints(at)
	starts := append([]int{0}, at...)

	var regions []Region
	for i, start := range starts {
		end := len(doc)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		regions = append(regions, Region{
			Name:  names[min(i, len(names)-1)],
			Start: start,
			End:   end,
			Opts:  rules[min(i, len(rules)-1)],
		})
	}

	done, err := Rewrite(string(doc), regions, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "regions:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, Report(done, len(doc)))
	}
}

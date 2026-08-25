// Command corpus reports which of the rewriter's documented hazards a document actually
// contains.
//
//	$ corpus < html-parsing-spec.html
//	713903 bytes
//	construct                              uses   first at  what it costs
//	implied end tag                        3247   7829      a position at the element's end lands elsewhere
//	element nothing closes                 5      16        no end-tag handler runs for it at all
//	p containing a block element           194    245839    a block wrapper inside it takes the content out
//
// That is the HTML parsing specification itself, and the element nothing closes at offset 16
// is its <html>. -v prints every occurrence rather than the summary.
//
// Every construct here is one the documentation names as a place where a streaming rewrite
// and a browser see different documents. None of them is a defect in a page: they are
// ordinary HTML, and most pages have several. The point is to know which ones a particular
// corpus has before writing a rewrite that assumes it does not.
//
// # Why sample rather than reason
//
// Because the answer is per corpus. A rewrite that positions content at an element's end
// is safe on a document whose end tags are all spelled and wrong on one that omits them,
// and which of those a site produces is a fact about its templates, not about HTML. The
// same goes for the rest: an <image> spelling, a table with fostered text, a raw-text
// element holding something that looks like its own end tag.
//
// # What each detector can see
//
// All of it from the rewriter itself, with no parser to compare against: an implied end
// tag is an end-tag callback whose name is not the element's, fostered content is text
// arriving while a table is open and no cell is, a self-closing HTML tag is
// IsSelfClosing on an element that can have content.
//
// The scan runs with strict mode off, because it has to survive the documents it reports
// on: strict mode refuses a raw-text tag inside a select, and that is one of the
// constructs being counted. The cost is the documented one - the content inside such a tag
// is text rather than markup - so a construct nested inside one is not seen. A document
// that reports "raw text inside a select or frameset" is a document this scan has not
// fully seen, and the count is a floor rather than a total.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Construct is one thing worth knowing about a document.
type Construct struct {
	Name string
	// Costs is what it does to a rewrite that does not expect it.
	Costs string
	// Behaviour is the entry in docs/gip/known-behaviours.md, where there is one.
	Behaviour string
}

// Constructs are the ones this program looks for.
var Constructs = []Construct{
	{"implied end tag", "a position at the element's end lands elsewhere", "the end-tag rule"},
	{"element nothing closes", "no end-tag handler runs for it at all", "the end-tag rule"},
	{"<image>", "a selector for img does not match it", "B155"},
	{"fostered content in a table", "the bytes say it is in the table, the tree says beside it", "B141"},
	{"template holding table rows", "prepending an element into it deletes the rows", "B144"},
	{"raw text inside a select or frameset", "strict mode refuses the document", "B22"},
	{"duplicate attribute", "selectors and Attribute see the first, AttributeList sees both", "B57"},
	{"self-closing HTML tag", "the slash is not a close: the element has content", "IsSelfClosing"},
	{"raw text holding its own end sequence", "an insertion there is refused, and a rename releases it", "B117"},
	{"base after a URL", "the URLs above it cannot be resolved in one pass", "the ordering constraint"},
	{"U+FFFD in text", "the declared encoding could not decode some bytes", "B158"},
	{"p containing a block element", "a block wrapper inside it takes the content out", "B146"},
}

// Finding is one occurrence.
type Finding struct {
	Construct string
	Offset    int
	Detail    string
}

// Report is what the document holds.
type Report struct {
	Bytes    int
	Findings []Finding
}

// Counts groups the findings by construct.
func (r Report) Counts() map[string]int {
	m := map[string]int{}
	for _, f := range r.Findings {
		m[f.Construct]++
	}
	return m
}

// First returns the earliest offset per construct.
func (r Report) First() map[string]int {
	m := map[string]int{}
	for _, f := range r.Findings {
		if o, seen := m[f.Construct]; !seen || f.Offset < o {
			m[f.Construct] = f.Offset
		}
	}
	return m
}

func (r Report) String() string {
	counts, first := r.Counts(), r.First()
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes\n", r.Bytes)
	fmt.Fprintf(&b, "%-38s %-6s %-9s %s\n", "construct", "uses", "first at", "what it costs")
	var any bool
	for _, c := range Constructs {
		n := counts[c.Name]
		if n == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "%-38s %-6d %-9d %s\n", c.Name, n, first[c.Name], c.Costs)
	}
	if !any {
		b.WriteString("none of the documented constructs\n")
	}
	return b.String()
}

// scanner finds the constructs. Everything it knows comes from the rewriter: there is no
// parser here to compare against.
type scanner struct {
	report Report

	// open is the stack of element names as the tokens describe it, which is what an
	// implied end tag is measured against.
	open []string
	// pending holds, per element name, the start offsets of the elements whose end-tag
	// handler has not fired. Offsets rather than a count, so an element nothing closes
	// can be reported where it opened: the document end is when it is known, not where
	// it is.
	pending map[string][]int
	// tables, cells, selects, framesets and templates are depth counters.
	tables, cells, selects, framesets, templates int
	// rowsInTemplate remembers whether the current template held a row.
	rowsInTemplate []bool
	// urlSeen is set once an element naming a URL has gone past, for the base check.
	urlSeen bool
}

// URLElements are the elements whose presence before a <base> matters.
var URLElements = map[string]bool{
	"img": true, "script": true, "link": true, "a": true, "iframe": true,
	"source": true, "video": true, "audio": true, "embed": true, "object": true,
}

// Blocks are the elements whose presence inside a <p> makes a wrapper unsafe there.
var Blocks = map[string]bool{
	"div": true, "table": true, "ul": true, "ol": true, "dl": true, "pre": true,
	"blockquote": true, "figure": true, "section": true, "article": true, "form": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

func (s *scanner) note(name string, offset int, detail string) {
	s.report.Findings = append(s.report.Findings, Finding{Construct: name, Offset: offset, Detail: detail})
}

func (s *scanner) element(e *lolhtml.Element) error {
	tag := e.TagName()
	at := e.SourceLocation().Start

	if tag == "image" && e.NamespaceURI() == lolhtml.NamespaceHTML {
		s.note("<image>", at, "a spelling of img")
	}
	if e.IsSelfClosing() && e.CanHaveContent() && e.NamespaceURI() == lolhtml.NamespaceHTML {
		s.note("self-closing HTML tag", at, "<"+tag+"/>")
	}
	if names := duplicates(e); len(names) > 0 {
		s.note("duplicate attribute", at, strings.Join(names, " "))
	}
	if tag == "base" && s.urlSeen {
		if _, ok := e.Attribute("href"); ok {
			s.note("base after a URL", at, "URLs above it are already past")
		}
	}
	if URLElements[tag] {
		s.urlSeen = true
	}
	if lolhtml.IsRawText(tag) && (s.selects > 0 || s.framesets > 0) {
		s.note("raw text inside a select or frameset", at, "<"+tag+">")
	}
	if s.templates > 0 && (tag == "tr" || tag == "td" || tag == "th" || tag == "tbody") {
		if n := len(s.rowsInTemplate); n > 0 && !s.rowsInTemplate[n-1] {
			s.rowsInTemplate[n-1] = true
			s.note("template holding table rows", at, "<"+tag+">")
		}
	}
	if Blocks[tag] && s.inParagraph() {
		s.note("p containing a block element", at, "<"+tag+"> inside a <p>")
	}

	switch tag {
	case "table":
		s.tables++
	case "td", "th", "caption":
		s.cells++
	case "select":
		s.selects++
	case "frameset":
		s.framesets++
	case "template":
		s.templates++
		s.rowsInTemplate = append(s.rowsInTemplate, false)
	}

	if !e.CanHaveContent() {
		return nil
	}
	s.open = append(s.open, tag)
	if s.pending == nil {
		s.pending = map[string][]int{}
	}
	s.pending[tag] = append(s.pending[tag], e.SourceLocation().Start)
	return e.OnEndTag(func(x *lolhtml.EndTag) error {
		if n := len(s.pending[tag]); n > 0 {
			s.pending[tag] = s.pending[tag][:n-1]
		}
		if x.Name() != tag {
			s.note("implied end tag", x.SourceLocation().Start,
				"<"+tag+"> closed by </"+x.Name()+">")
		}
		// Pop this element and anything the same end tag closed with it.
		for i := len(s.open) - 1; i >= 0; i-- {
			if s.open[i] == tag {
				s.open = s.open[:i]
				break
			}
		}
		switch tag {
		case "table":
			s.tables--
		case "td", "th", "caption":
			s.cells--
		case "select":
			s.selects--
		case "frameset":
			s.framesets--
		case "template":
			s.templates--
			if n := len(s.rowsInTemplate); n > 0 {
				s.rowsInTemplate = s.rowsInTemplate[:n-1]
			}
		}
		return nil
	})
}

// inParagraph reports whether a paragraph is open with nothing but inline elements
// between, which is the shape a block element inside a paragraph takes.
func (s *scanner) inParagraph() bool {
	for i := len(s.open) - 1; i >= 0; i-- {
		if s.open[i] == "p" {
			return true
		}
		if Blocks[s.open[i]] || s.open[i] == "td" || s.open[i] == "li" || s.open[i] == "body" {
			return false
		}
	}
	return false
}

func (s *scanner) text(c *lolhtml.TextChunk) error {
	t := c.Text()
	at := c.SourceLocation().Start
	if strings.TrimSpace(t) != "" && s.tables > 0 && s.cells == 0 && s.templates == 0 {
		s.note("fostered content in a table", at, "text inside a table and outside a cell")
	}
	if strings.Contains(t, "�") {
		s.note("U+FFFD in text", at, "a byte the encoding could not decode, or the character itself")
	}
	if n := len(s.open); n > 0 && lolhtml.IsRawText(s.open[n-1]) {
		if i := strings.Index(strings.ToLower(t), "</"+s.open[n-1]); i >= 0 {
			s.note("raw text holding its own end sequence", at+i, "inside <"+s.open[n-1]+">")
		}
	}
	return nil
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		// Permissive parsing, because a scanner has to survive the documents it is
		// meant to report on: strict mode refuses a raw-text tag inside a select, and
		// that is one of the constructs being counted. The cost is that the content
		// inside such a tag is text rather than markup, so anything nested in it is
		// not seen - which the report says.
		lolhtml.WithStrict(false),
		lolhtml.OnElement("*", s.element),
		lolhtml.OnDocumentText(s.text),
	}
}

// Scan reads a document and reports the constructs in it. Nothing is written.
func Scan(doc []byte) (Report, error) {
	s := &scanner{}
	s.report.Bytes = len(doc)
	w, err := lolhtml.NewWriter(io.Discard, s.options()...)
	if err != nil {
		return s.report, err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return s.report, err
	}
	if err := w.Close(); err != nil {
		return s.report, err
	}
	// Whatever is still pending was never closed, which is its own construct. Each is
	// reported at its own start offset, in document order, so the report points at the
	// element rather than at the end of the scan.
	type unclosed struct {
		name string
		at   int
	}
	var left []unclosed
	for name, offsets := range s.pending {
		for _, at := range offsets {
			left = append(left, unclosed{name, at})
		}
	}
	sort.Slice(left, func(i, j int) bool {
		if left[i].at != left[j].at {
			return left[i].at < left[j].at
		}
		return left[i].name < left[j].name
	})
	for _, u := range left {
		s.note("element nothing closes", u.at, "<"+u.name+"> is still open at the document end")
	}
	return s.report, nil
}

// duplicates returns the attribute names an element has more than once.
func duplicates(e *lolhtml.Element) []string {
	seen := map[string]int{}
	for _, a := range e.AttributeList() {
		seen[a.Name]++
	}
	var out []string
	for name, n := range seen {
		if n > 1 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func main() {
	list := flag.Bool("list", false, "list the constructs this program looks for")
	verbose := flag.Bool("v", false, "print every occurrence rather than a summary")
	flag.Parse()

	if *list {
		for _, c := range Constructs {
			fmt.Printf("%-38s %-24s %s\n", c.Name, c.Behaviour, c.Costs)
		}
		return
	}

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(2)
	}
	r, err := Scan(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(2)
	}
	if *verbose {
		for _, f := range r.Findings {
			fmt.Printf("%-38s %-9d %s\n", f.Construct, f.Offset, f.Detail)
		}
		return
	}
	fmt.Print(r)
}

// Command microdata extracts HTML microdata into a flat key-value report.
//
//	microdata page.html
//	1 Product
//	  Product.name = Widget
//	  Product.offers.price = 9.99
//	  Product.offers.priceCurrency = GBP
//
// It changes nothing: the document goes to standard output byte for byte and the
// report to standard error.
//
// Microdata is a tree - an itemprop belongs to the nearest enclosing itemscope,
// and an itemprop can itself open a new scope - and a rewriter has no tree. The
// shape is recovered with a stack, and the stack has to be of open *elements*
// rather than of open scopes, because the question is where an element ended and
// an end-tag callback does not answer it. HTML lets an element be closed by the
// next start tag - <p itemprop=a>x<p itemprop=b>y is two properties - and there
// is no end tag for the first one: its callback runs at the enclosing element's
// end tag instead, by which time the second property's text has already gone by.
// Popping there gave "a" the value "xy" and made every following property a child
// of a scope that had closed.
//
// So this keeps every open element and applies the specification's implied end
// tags on each start tag, the way examples/gip/markdown and examples/gip/depth
// do, and closes a scope or a property when its element leaves that stack. That
// costs an end-tag registration per element, which is the price of being exact
// about where content ends; a program that only needs positions can register far
// fewer. An element that cannot have content, such as the <meta itemprop content>
// that most microdata is written with, is never pushed: it has no end tag to pop
// it, and the stack would never come back down.
//
// One thing this program has to decide that a rewriting program does not. An
// element can carry the same attribute twice, and the API is split about it:
// selectors, Attribute and SetAttribute act on the first copy, while iterating
// the attributes yields every copy. A parser drops all but the first, so the
// first is what a browser acts on, and this program follows that - which means
// reading through Attribute rather than through the iterator, even where the
// iterator would be more convenient.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A scope is one open itemscope.
type scope struct {
	typ   string // the last path segment of itemtype, or "item"
	path  string // the dotted path from the outermost scope
	depth int    // open-element depth it was pushed at, so it closes with its element
}

// A pair is one line of the report.
type pair struct {
	item  int    // which top-level item it belongs to
	key   string // dotted path
	value string
}

type reader struct {
	maxDepth int
	maxPairs int

	pairs   []pair
	items   int
	skipped map[string]int
}

func (r *reader) note(reason string) {
	if r.skipped == nil {
		r.skipped = map[string]int{}
	}
	r.skipped[reason]++
}

func defaults() *reader { return &reader{maxDepth: 12, maxPairs: 1000} }

func (r *reader) validate() error {
	if r.maxDepth < 1 {
		return fmt.Errorf("-max-depth %d leaves no room for an item", r.maxDepth)
	}
	if r.maxPairs < 1 {
		return fmt.Errorf("-max-pairs %d leaves no room for a value", r.maxPairs)
	}
	return nil
}

func (r *reader) options() []lolhtml.Option {
	// open is every element the source has open at this point. It is what makes an
	// implicitly closed element close where it actually ended rather than where its
	// callback happens to arrive; see the package comment.
	var open []string
	// seen is the source offset of every end-tag token already applied. One token
	// reaches this program once per element it closes, and the stack must move once.
	seen := map[int]bool{}

	var stack []scope

	// Text properties being gathered. A stack rather than one, because an
	// itemprop can contain another and an itemprop's value is all of its text:
	// given <div itemprop=outer>before <span itemprop=inner>in</span> after</div>
	// the outer value is "before in after" and the inner one is "in". A single
	// variable produced "in" twice and lost the outer entirely.
	type pending struct {
		key   string
		depth int // open-element depth, as for a scope
		text  strings.Builder
	}
	var props []*pending

	// popTo unwinds the open elements to n deep, finishing whatever microdata was
	// opened inside them: a property's value is complete when its element ends,
	// wherever that turns out to be.
	popTo := func(n int) {
		for len(open) > n {
			open = open[:len(open)-1]
			d := len(open)
			for len(props) > 0 && props[len(props)-1].depth > d {
				p := props[len(props)-1]
				props = props[:len(props)-1]
				r.add(p.key, squash(decoded(p.text.String())))
			}
			for len(stack) > 0 && stack[len(stack)-1].depth > d {
				stack = stack[:len(stack)-1]
			}
		}
	}

	return []lolhtml.Option{
		// The open-element stack is kept for every element, not only for the ones
		// carrying microdata: what closes an itemprop is the next start tag, and
		// that tag need not carry anything itself.
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			popTo(impliedEnd(open, tag))
			if !e.CanHaveContent() {
				// Nothing can be inside it, so it never goes on the stack.
				return nil
			}
			open = append(open, tag)
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				at := t.SourceLocation().Start
				if seen[at] {
					return nil
				}
				seen[at] = true
				// Pop to and including the innermost open element of that name.
				// An end tag that matches nothing open is stray and changes
				// nothing.
				for i := len(open) - 1; i >= 0; i-- {
					if open[i] == t.Name() {
						popTo(i)
						return nil
					}
				}
				return nil
			})
		}),

		lolhtml.OnElement("[itemscope], [itemprop]", func(e *lolhtml.Element) error {
			// Read through Attribute, not through the iterator: an element can
			// carry itemprop twice, and the first copy is the one a parser
			// keeps.
			prop := decoded(attr(e, "itemprop"))
			_, isScope := e.Attribute("itemscope")

			key := ""
			if prop != "" {
				key = r.pathFor(stack, prop)
			}

			if isScope {
				if len(stack) >= r.maxDepth {
					r.note("an item was nested deeper than -max-depth")
					return nil
				}
				typ := typeName(decoded(attr(e, "itemtype")))
				path := typ
				if len(stack) > 0 {
					path = key
					if path == "" {
						// A nested scope with no itemprop has nothing to hang
						// its values from.
						r.note("a nested itemscope had no itemprop to name it")
						return nil
					}
				} else {
					r.items++
				}
				if !e.CanHaveContent() {
					// It is not on the open-element stack, so nothing would
					// ever close it. A scope with no content has no properties
					// either, so nothing is lost.
					r.note("an itemscope on an element with no content was skipped")
					return nil
				}
				// len(open) is this element's own depth: the handler above ran
				// first and pushed it.
				stack = append(stack, scope{typ: typ, path: path, depth: len(open)})
				return nil
			}

			if key == "" {
				return nil
			}

			// A value can come from an attribute rather than from text, and
			// which attribute depends on the element. This is the list the
			// microdata specification gives.
			if v, ok := valueAttribute(e); ok {
				r.add(key, decoded(v))
				return nil
			}

			if !e.CanHaveContent() {
				r.note("an itemprop with no value attribute and no content")
				return nil
			}
			props = append(props, &pending{key: key, depth: len(open)})
			return nil
		}),

		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			// Every open property gets the text: it belongs to all of them.
			for _, p := range props {
				p.text.WriteString(tc.Text())
			}
			return nil
		}),
	}
}

// impliedEnd applies the specification's implied end tags for a start tag named
// next, and returns how many of the open elements are left: the element next
// closes, and everything still open inside it.
//
// Each rule has a barrier, because these only reach within their own structure. A
// <li> closes an open list item in the same list and not one in a list two levels
// out, which is what stops a malformed document from unwinding the whole stack.
// Written this way rather than as a test of the innermost element alone, because
// the element being closed need not be innermost: the second <li> of
// <ul><li><em>a<li>b</ul> ends the <em> as well as the item.
func impliedEnd(open []string, next string) int {
	n := len(open)
	through := func(want, barrier map[string]bool) {
		for i := n - 1; i >= 0; i-- {
			if want[open[i]] {
				n = i
				return
			}
			if barrier[open[i]] {
				return
			}
		}
	}
	switch next {
	case "li":
		through(set("li"), set("ul", "ol", "menu"))
	case "dd", "dt":
		through(set("dd", "dt"), set("dl"))
	case "td", "th":
		through(set("td", "th"), set("tr", "table"))
	case "tr":
		through(set("tr"), set("table", "tbody", "thead", "tfoot"))
	case "option":
		through(set("option"), set("select", "datalist"))
	case "optgroup":
		through(set("option", "optgroup"), set("select"))
	case "rt", "rp":
		through(set("rt", "rp"), set("ruby"))
	}
	// A paragraph is closed by any of the block elements that cannot be inside
	// one. Nothing above a <p> on the stack is a block, so there is no barrier to
	// respect.
	if closesAParagraph[next] {
		through(set("p"), nil)
	}
	return n
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// closesAParagraph is the set of start tags that end an open <p>. A paragraph
// cannot contain flow content that is itself a block, so the parser closes it
// rather than nesting.
var closesAParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "details": true, "dialog": true, "dir": true, "div": true,
	"dl": true, "dt": true, "dd": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "hr": true, "li": true, "listing": true, "main": true,
	"menu": true, "nav": true, "ol": true, "p": true, "plaintext": true,
	"pre": true, "search": true, "section": true, "summary": true,
	"table": true, "ul": true, "xmp": true,
}

// pathFor builds the dotted key for a property in the current scope.
func (r *reader) pathFor(stack []scope, prop string) string {
	if len(stack) == 0 {
		// A property outside any itemscope is not microdata, but it is common
		// enough in the wild to be worth reporting rather than dropping.
		return prop
	}
	return stack[len(stack)-1].path + "." + prop
}

func (r *reader) add(key, value string) {
	if value == "" {
		return
	}
	if len(r.pairs) >= r.maxPairs {
		r.note("more values than -max-pairs")
		return
	}
	item := r.items
	if item == 0 {
		item = 1
	}
	r.pairs = append(r.pairs, pair{item: item, key: key, value: value})
}

// valueAttribute is the microdata specification's mapping from element to the
// attribute that holds its value. Everything else takes its text.
func valueAttribute(e *lolhtml.Element) (string, bool) {
	var name string
	switch strings.ToLower(e.TagName()) {
	case "meta":
		name = "content"
	case "audio", "embed", "iframe", "img", "source", "track", "video":
		name = "src"
	case "a", "area", "link":
		name = "href"
	case "object":
		name = "data"
	case "data", "meter":
		name = "value"
	case "time":
		name = "datetime"
	default:
		return "", false
	}
	v, ok := e.Attribute(name)
	if !ok {
		return "", false
	}
	return v, true
}

// typeName is the last path segment of an itemtype URL, which is what a report
// wants: "https://schema.org/Product" reads as "Product".
func typeName(itemtype string) string {
	if itemtype == "" {
		return "item"
	}
	s := strings.TrimRight(strings.Fields(itemtype)[0], "/")
	if i := strings.LastIndexAny(s, "/#"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "item"
	}
	return s
}

// decoded turns raw source into text. Everything the library reports is raw
// source with references still encoded, and a report is read by a person.
func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (r *reader) run(in io.Reader, w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func readString(in string, opts ...func(*reader)) (string, *reader, error) {
	r := defaults()
	for _, o := range opts {
		o(r)
	}
	var out bytes.Buffer
	err := r.run(strings.NewReader(in), &out)
	return out.String(), r, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (r *reader) report() string {
	var sb strings.Builder
	if len(r.pairs) == 0 {
		sb.WriteString("no microdata\n")
	}
	item := 0
	for _, p := range r.pairs {
		if p.item != item {
			item = p.item
			fmt.Fprintf(&sb, "%d\n", item)
		}
		fmt.Fprintf(&sb, "  %s = %s\n", p.key, p.value)
	}
	reasons := make([]string, 0, len(r.skipped))
	for reason := range r.skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", reason, r.skipped[reason])
	}
	fmt.Fprintf(&sb, "items=%d values=%d\n", r.items, len(r.pairs))
	return sb.String()
}

func main() {
	r := defaults()
	flag.IntVar(&r.maxDepth, "max-depth", r.maxDepth, "deepest itemscope nesting to follow")
	flag.IntVar(&r.maxPairs, "max-pairs", r.maxPairs, "most values to report")
	flag.Parse()

	var in io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "microdata:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: microdata [file.html]")
		os.Exit(2)
	}

	if err := r.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "microdata:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, r.report())
}

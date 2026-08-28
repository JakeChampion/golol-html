// Command ids reports duplicate id attributes, and the references they make
// ambiguous.
//
//	page.html:14:3: id="submit" is used 3 times (also at 22:5, 31:5)
//	page.html:14:3:   1 label for=, 1 aria-controls and 1 fragment link name it
//	page.html:40:3: for="gone" names no id in this document
//
// A duplicate id is not one broken thing, it is a list of them, and which ones
// depends on what points at the id. So the report names them: every reference in
// the document is collected along with every id, and the two are matched at the
// end. That is the shape a report can have and a rewrite cannot - see
// examples/gip/labels, where the same join is what makes the finding possible.
//
// The reason duplicates survive in a codebase is worth stating, because it is the
// one mechanism that does not break: a CSS "#id" selector matches every element
// with that id, so the page looks right. Everything else takes the first and
// silently ignores the rest - getElementById, a fragment link, a label's for, and
// every ARIA reference. So the visual check passes and the keyboard user is the
// one who finds it.
//
// The attributes that name an id are a list rather than a rule, and this is the
// list: for, form, list, headers, aria-activedescendant, aria-controls,
// aria-describedby, aria-details, aria-errormessage, aria-flowto, aria-labelledby
// and aria-owns, plus an href or an xlink:href whose value is a fragment. Six of
// them - headers, aria-controls, aria-describedby, aria-flowto, aria-labelledby
// and aria-owns - hold several ids separated by spaces rather than one, which is a
// detail a program has to get right to report on them at all.
//
// Every one of those values is read decoded, because an id is what it decodes to:
// id="caf&eacute;" and id="café" are one id spelled two ways, and a fragment link
// to either names both. The library reports attributes as raw source, so comparing
// what it hands back would miss the duplicate and report the link as broken in the
// same document.
//
// Two more findings fall out of having the index:
//
// A reference naming no id at all - a for pointing at a control that was renamed,
// an aria-labelledby left behind by a refactor. It is the same defect as a
// duplicate seen from the other side.
//
// An id that is not a valid id: one with whitespace in it, or an empty one. HTML
// says an id must have at least one character and no whitespace, and the reason to
// report it is concrete rather than pedantic: a fragment link cannot address it,
// and a CSS "#id" selector cannot name it.
//
// One thing reported as advice rather than as a defect: two ids differing only in
// case. That is legal - ids are case-sensitive, and so is a "#id" selector, which
// is why "#Main" does not match id="main" - and it is the kind of legal that costs
// somebody an afternoon.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what is wrong.
type Kind string

const (
	Duplicate Kind = "duplicate"
	Broken    Kind = "broken-reference"
	Invalid   Kind = "invalid-id"
	CaseOnly  Kind = "case-only"
)

// Single are the attributes whose value is one id.
var Single = []string{
	"for", "form", "list", "aria-activedescendant", "aria-details", "aria-errormessage",
}

// Multiple are the attributes whose value is several ids separated by whitespace.
var Multiple = []string{
	"headers", "aria-controls", "aria-describedby", "aria-flowto", "aria-labelledby",
	"aria-owns",
}

// A Finding is one thing worth reporting.
type Finding struct {
	Kind         Kind
	At           int
	Line, Column int
	Message      string
}

func (f Finding) String() string {
	return fmt.Sprintf("%d:%d: %s", f.Line, f.Column, f.Message)
}

// A Result is the report.
type Result struct {
	Findings []Finding
	// Ids seen, Unique ones, and References collected.
	Ids, Unique, References int
}

// OK reports whether there was nothing to say.
func (r Result) OK() bool { return len(r.Findings) == 0 }

func (r Result) String() string {
	return fmt.Sprintf("ids: %d id attributes, %d distinct; %d references; %d findings",
		r.Ids, r.Unique, r.References, len(r.Findings))
}

// an occurrence of an id, and a reference to one.
type occurrence struct {
	at  int
	tag string
}

type reference struct {
	at   int
	tag  string
	attr string
	id   string
}

// Check reads doc and reports on its ids.
func Check(doc []byte) (Result, error) {
	c := &checker{ids: map[string][]occurrence{}}
	w, err := lolhtml.NewWriter(io.Discard, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.report(doc), nil
}

type checker struct {
	res  Result
	ids  map[string][]occurrence
	refs []reference
	// order is the ids in the order they were first seen, so the report is in
	// document order without sorting strings.
	order []string
}

func (c *checker) options() []lolhtml.Option {
	// One handler on every element: this program's rule is about every element,
	// which is the case a wide selector is for.
	return []lolhtml.Option{lolhtml.OnElement("*", c.element)}
}

func (c *checker) element(e *lolhtml.Element) error {
	at := e.SourceLocation().Start
	tag := e.TagName()

	if id, ok := decoded(e, "id"); ok {
		c.res.Ids++
		if _, seen := c.ids[id]; !seen {
			c.order = append(c.order, id)
		}
		c.ids[id] = append(c.ids[id], occurrence{at: at, tag: tag})
	}

	for _, attr := range Single {
		if v, ok := decoded(e, attr); ok && strings.TrimSpace(v) != "" {
			c.refs = append(c.refs, reference{at: at, tag: tag, attr: attr, id: strings.TrimSpace(v)})
			c.res.References++
		}
	}
	for _, attr := range Multiple {
		v, ok := decoded(e, attr)
		if !ok {
			continue
		}
		// Decoded first and split after, which is the order a browser uses: a
		// value spelling its separator as "&#32;" is two ids, not one.
		for _, id := range strings.Fields(v) {
			c.refs = append(c.refs, reference{at: at, tag: tag, attr: attr, id: id})
			c.res.References++
		}
	}
	// A fragment in an href names an id, and is the reference a reader uses.
	for _, attr := range []string{"href", "xlink:href"} {
		if v, ok := decoded(e, attr); ok && strings.HasPrefix(v, "#") && len(v) > 1 {
			c.refs = append(c.refs, reference{at: at, tag: tag, attr: "fragment link", id: v[1:]})
			c.res.References++
		}
	}
	return nil
}

// decoded reads an attribute and decodes its character references, which is the
// only comparable form. [lolhtml.Element.Attribute] reports raw source, so
// id="caf&eacute;" and id="café" arrive as two different strings and name
// the same id, and href="#café" names it too. Comparing raw source misses
// the duplicate and invents a broken reference at the same time. Nothing here is
// written back, so there is no need to keep the raw form; a program that rewrote
// the value would decide on the decoded form and write the raw one.
func decoded(e *lolhtml.Element, name string) (string, bool) {
	v, ok := e.Attribute(name)
	if !ok {
		return "", false
	}
	return stdhtml.UnescapeString(v), true
}

func (c *checker) report(doc []byte) Result {
	lines := newlines(doc)
	pos := func(at int) string {
		line, col := position(lines, doc, at)
		return fmt.Sprintf("%d:%d", line, col)
	}
	add := func(kind Kind, at int, format string, args ...any) {
		line, col := position(lines, doc, at)
		c.res.Findings = append(c.res.Findings, Finding{
			Kind: kind, At: at, Line: line, Column: col,
			Message: fmt.Sprintf(format, args...),
		})
	}
	c.res.Unique = len(c.ids)

	// References by id, so a duplicate can say what it broke.
	byID := map[string][]reference{}
	for _, r := range c.refs {
		byID[r.id] = append(byID[r.id], r)
	}

	for _, id := range c.order {
		where := c.ids[id]
		if len(where) < 2 {
			continue
		}
		others := make([]string, 0, len(where)-1)
		for _, o := range where[1:] {
			others = append(others, pos(o.at))
		}
		add(Duplicate, where[0].at, "id=%q is used %d times (also at %s)",
			id, len(where), strings.Join(others, ", "))
		if named := byID[id]; len(named) > 0 {
			add(Duplicate, where[0].at, "  %s name it, and each takes the first; "+
				"a CSS %q selector matches all of them, which is why this looks fine",
				describe(named), "#"+id)
		}
	}

	for _, r := range c.refs {
		if len(c.ids[r.id]) == 0 {
			add(Broken, r.at, "<%s %s=%q> names no id in this document", r.tag, r.attr, r.id)
		}
	}

	for _, id := range c.order {
		switch {
		case id == "":
			add(Invalid, c.ids[id][0].at, `id="" has no characters in it, so nothing can address it`)
		case strings.ContainsAny(id, " \t\r\n\f"):
			add(Invalid, c.ids[id][0].at, "id=%q contains whitespace, so a fragment link "+
				"and a CSS selector cannot address it", id)
		}
	}

	// Ids differing only in case: legal, and the kind of legal that costs an
	// afternoon.
	lower := map[string][]string{}
	for _, id := range c.order {
		key := strings.ToLower(id)
		lower[key] = append(lower[key], id)
	}
	for _, id := range c.order {
		group := lower[strings.ToLower(id)]
		if len(group) < 2 || group[0] != id {
			continue
		}
		add(CaseOnly, c.ids[id][0].at, "id=%q and %s differ only in case, which is legal "+
			"and means a %q selector matches only one of them",
			id, strings.Join(quoteAll(group[1:]), " and "), "#"+id)
	}

	sort.SliceStable(c.res.Findings, func(i, j int) bool {
		return c.res.Findings[i].At < c.res.Findings[j].At
	})
	return c.res
}

// describe counts the references by attribute, so the message says what kind
// rather than listing every one.
func describe(refs []reference) string {
	counts := map[string]int{}
	var order []string
	for _, r := range refs {
		if counts[r.attr] == 0 {
			order = append(order, r.attr)
		}
		counts[r.attr]++
	}
	parts := make([]string, 0, len(order))
	for _, attr := range order {
		name := attr
		if attr != "fragment link" {
			name = attr + "="
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[attr], name))
	}
	return strings.Join(parts, ", ")
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func newlines(doc []byte) []int {
	var at []int
	for i, b := range doc {
		if b == '\n' {
			at = append(at, i)
		}
	}
	return at
}

func position(lines []int, doc []byte, at int) (line, column int) {
	line = 1
	start := 0
	for _, nl := range lines {
		if nl >= at {
			break
		}
		line++
		start = nl + 1
	}
	if at > len(doc) {
		at = len(doc)
	}
	return line, utf8.RuneCount(doc[start:at]) + 1
}

func main() {
	name := "-"
	var doc []byte
	var err error
	switch len(os.Args) {
	case 1:
		doc, err = io.ReadAll(os.Stdin)
	case 2:
		name = os.Args[1]
		doc, err = os.ReadFile(name)
	default:
		fmt.Fprintln(os.Stderr, "usage: ids [file] < page")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ids:", err)
		os.Exit(1)
	}
	res, err := Check(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ids:", err)
		os.Exit(1)
	}
	for _, f := range res.Findings {
		fmt.Printf("%s:%s\n", name, f)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

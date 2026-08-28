// Command transitions gives view-transition names to the elements that appear on both of two
// pages, so a browser can animate between them.
//
//	$ transitions before.html after.html
//	14 elements on both pages, 3 named
//	  header > nav               -> vt-header-nav
//	  main > h1                  -> vt-main-h1
//	  main > div.card:nth(2)     -> vt-main-div-card-2
//	  skipped 11: no id, no class, and not one of the elements worth animating
//
// A view transition needs the same name on the same thing in both documents. "The same thing" is
// the problem: two pages are two documents, and nothing in either says which element in one
// corresponds to which in the other.
//
// # Why a source offset cannot be the identity
//
// [lolhtml.SourceLocation] offsets are absolute and stable, which is what makes them an identity
// *within* one document - examples/gip/article leans on exactly that. Across two documents they
// are meaningless: the same header is at byte 210 in one page and byte 1804 in the other.
//
// So the identity has to come from the content. This program builds a path - the chain of open
// elements, each with its tag, its id or first class, and its position among its siblings of the
// same shape - which is the same string in both documents when the element is the same element,
// and is computable while streaming because it only ever needs what is already open.
//
//	header > nav
//	main > div.card:nth(2) > h2
//
// The path is a fact about the source rather than about the tree: a rewriter reports the elements
// the document contains, not the ones a tree builder would add, so a fragment beginning with
// <body> has a path starting at "body" and a full page has one starting at "html". That is what
// makes the comparison work between two pages written by the same templates, and it is also the
// limit - a page that gains a wrapping <div> between versions has different paths for everything
// inside it, and this cannot tell that from a page that replaced its content.
//
// Two elements collide when a page has genuinely identical structure in two places, and the :nth
// part is what separates them. A page that reorders its cards between the two versions defeats
// this, and the report says how many paths matched rather than claiming they are the right ones -
// which is the honest limit of doing it without a tree.
//
// # What the rewrite has to be careful about
//
// A view-transition name goes in a style attribute, and an element may already have one. The
// declaration is prepended, because the cascade takes the last declaration for a property and an
// element's own style should keep beating what this program adds - the same rule
// examples/gip/email arrived at.
//
// The name has to be a CSS custom-ident: it cannot start with a digit, cannot be "none", and can
// hold only letters, digits, hyphens and underscores. So a class of "3-col" or "none" is
// sanitised rather than trusted, and the result is made unique per document, because two elements
// with the same name animate as one thing.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// worthAnimating are the elements a transition is usually about. Naming everything is worse than
// naming nothing: a browser animating four hundred elements is a browser dropping frames.
var worthAnimating = map[string]bool{
	"header": true, "nav": true, "main": true, "footer": true, "aside": true,
	"h1": true, "h2": true, "h3": true, "img": true, "figure": true,
	"article": true, "section": true, "table": true,
}

// namableWithClass are elements that are worth animating only when the page has distinguished
// them. A div is not interesting; a div the page called "card" is the thing a reader watches move.
var namableWithClass = map[string]bool{"div": true, "li": true, "tr": true, "span": false}

// namable reports whether an element is one to give a name to. An id is enough on its own: a page
// that gave an element an id has said it is a particular thing.
func namable(el Element) bool {
	switch {
	case worthAnimating[el.Tag]:
		return true
	case el.ID != "":
		return true
	case el.Class != "" && namableWithClass[el.Tag]:
		return true
	default:
		return false
	}
}

// Element is one element's identity and where it was.
type Element struct {
	// Path is the identity: the chain of ancestors with each one's shape and position.
	Path  string
	Tag   string
	ID    string
	Class string
	// Location is where it was in its own document, which is useful for a report and
	// useless as an identity across two.
	Location lolhtml.SourceLocation
}

// Document is what one page contains, keyed by path.
type Document struct {
	Name     string
	Elements map[string]Element
	// Order is the paths in document order, so a report reads like the page.
	Order []string
}

// Scan reads a document and records the identity of every element.
func Scan(name string, r io.Reader) (*Document, error) {
	doc := &Document{Name: name, Elements: map[string]Element{}}

	// open is the stack of ancestors, each with the counts of the shapes seen inside it, so
	// a sibling's position among its own shape can be numbered.
	type frame struct {
		segment string
		counts  map[string]int
	}
	var open []frame
	// roots counts the shapes at the top level of the input, which have no parent frame
	// to be counted in. A fragment can begin at the top level - this program says so - and
	// without this two sibling roots of the same shape both number 1, so they share a path:
	// the second is dropped from the map here and Apply stamps the same name on both.
	roots := map[string]int{}

	w, err := lolhtml.NewWriter(io.Discard, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		tag := e.TagName()
		id, _ := e.Attribute("id")
		class, _ := e.Attribute("class")
		shape := shapeOf(tag, id, class)

		// The position among siblings of the same shape, counted in the parent - or at
		// the top level, where there is no parent.
		counts := roots
		if len(open) > 0 {
			counts = open[len(open)-1].counts
		}
		counts[shape]++
		nth := counts[shape]
		segment := shape
		if nth > 1 {
			segment = fmt.Sprintf("%s:nth(%d)", shape, nth)
		}

		var path strings.Builder
		for _, f := range open {
			path.WriteString(f.segment)
			path.WriteString(" > ")
		}
		path.WriteString(segment)

		if _, seen := doc.Elements[path.String()]; !seen {
			doc.Elements[path.String()] = Element{
				Path: path.String(), Tag: tag, ID: id, Class: class,
				Location: e.SourceLocation(),
			}
			doc.Order = append(doc.Order, path.String())
		}

		if !e.CanHaveContent() {
			return nil
		}
		open = append(open, frame{segment: segment, counts: map[string]int{}})
		depth := len(open)
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			// The end tag may close more than this element, so the stack is unwound to
			// this depth rather than popped once.
			if len(open) >= depth {
				open = open[:depth-1]
			}
			return nil
		})
	}))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return doc, nil
}

// shapeOf is an element's identity within its parent: its tag, plus its id or its first class if
// it has one. The first class only, because a page that adds "is-active" to an element between two
// versions has not changed the element.
func shapeOf(tag, id, class string) string {
	switch {
	case id != "":
		return tag + "#" + id
	case class != "":
		return tag + "." + firstClass(class)
	default:
		return tag
	}
}

func firstClass(class string) string {
	fields := strings.Fields(class)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Pairing is the elements two documents have in common.
type Pairing struct {
	Before, After *Document
	// Shared are the paths in both, in the first document's order.
	Shared []string
	// Names maps a path to the view-transition name it was given.
	Names map[string]string
	// Skipped counts shared elements that were not named because they are not worth
	// animating.
	Skipped int
}

// Pair works out which elements are on both pages and names them.
func Pair(before, after *Document) *Pairing {
	p := &Pairing{Before: before, After: after, Names: map[string]string{}}

	used := map[string]bool{}
	for _, path := range before.Order {
		el, ok := after.Elements[path]
		if !ok {
			continue
		}
		p.Shared = append(p.Shared, path)

		if !namable(el) {
			p.Skipped++
			continue
		}
		name := uniqueName(nameFor(before.Elements[path]), used)
		used[name] = true
		p.Names[path] = name
	}
	return p
}

// nameFor builds a candidate name from an element's own words, which makes a stylesheet that
// wants to target one readable.
func nameFor(el Element) string {
	parts := []string{"vt"}
	for _, segment := range strings.Split(el.Path, " > ") {
		parts = append(parts, identSegment(segment))
	}
	name := strings.Join(parts, "-")
	name = strings.Trim(name, "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return name
}

// identSegment turns one path segment into something a CSS custom-ident can hold.
func identSegment(segment string) string {
	var b strings.Builder
	for _, r := range segment {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// uniqueName makes a name that is not taken, and that a browser will accept.
//
// A custom-ident cannot begin with a digit and cannot be one of the CSS-wide keywords, so a name
// derived from a class of "3-col" or "none" is not a name. Prefixing is enough for the first and
// a suffix settles the second.
func uniqueName(base string, used map[string]bool) string {
	if base == "" {
		base = "vt"
	}
	if r := rune(base[0]); unicode.IsDigit(r) || r == '-' {
		base = "vt-" + base
	}
	switch strings.ToLower(base) {
	case "none", "auto", "initial", "inherit", "unset", "revert", "revert-layer":
		base = "vt-" + base
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !used[candidate] {
			return candidate
		}
	}
}

// Apply rewrites a document, adding the names the pairing decided.
//
// The paths are recomputed here rather than carried over, because a path is a function of the
// document and recomputing it is what makes this pass independent of the scan - the same code
// walking the same document reaches the same names.
func Apply(r io.Reader, w io.Writer, p *Pairing) (int, error) {
	applied := 0

	type frame struct {
		segment string
		counts  map[string]int
	}
	var open []frame
	// The same top-level counter the scan keeps: the two passes have to number siblings
	// identically or the recomputed path is not the one that was recorded.
	roots := map[string]int{}

	writer, err := lolhtml.NewWriter(w, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		tag := e.TagName()
		id, _ := e.Attribute("id")
		class, _ := e.Attribute("class")
		shape := shapeOf(tag, id, class)

		counts := roots
		if len(open) > 0 {
			counts = open[len(open)-1].counts
		}
		counts[shape]++
		nth := counts[shape]
		segment := shape
		if nth > 1 {
			segment = fmt.Sprintf("%s:nth(%d)", shape, nth)
		}

		var path strings.Builder
		for _, f := range open {
			path.WriteString(f.segment)
			path.WriteString(" > ")
		}
		path.WriteString(segment)

		if name, ok := p.Names[path.String()]; ok {
			applied++
			if err := prependStyle(e, "view-transition-name: "+name); err != nil {
				return err
			}
		}

		if !e.CanHaveContent() {
			return nil
		}
		open = append(open, frame{segment: segment, counts: map[string]int{}})
		depth := len(open)
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			if len(open) >= depth {
				open = open[:depth-1]
			}
			return nil
		})
	}))
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return applied, err
	}
	if err := writer.Close(); err != nil {
		return applied, err
	}
	return applied, nil
}

// prependStyle puts a declaration before whatever the element's style attribute already says.
//
// Before, not after: the cascade takes the last declaration for a property, so an element's own
// style has to stay last and keep winning. The value is written back raw apart from the addition,
// because an attribute value is source text - "a &amp; b" is those characters and escaping it
// again would change it.
func prependStyle(e *lolhtml.Element, declaration string) error {
	existing, _ := e.Attribute("style")
	existing = strings.TrimSpace(existing)

	if strings.Contains(existing, "view-transition-name") {
		// Already named by the page itself: leave it, since the page knows something this
		// program does not.
		return nil
	}
	if existing == "" {
		return e.SetAttribute("style", declaration+";")
	}
	return e.SetAttribute("style", declaration+"; "+existing)
}

// Report describes the pairing.
func Report(p *Pairing) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d elements on both pages, %d named\n", len(p.Shared), len(p.Names))

	paths := make([]string, 0, len(p.Names))
	for path := range p.Names {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fmt.Fprintf(&b, "  %-40s -> %s\n", path, p.Names[path])
	}
	if p.Skipped > 0 {
		fmt.Fprintf(&b, "  skipped %d: on both pages and not one of the elements worth animating\n",
			p.Skipped)
	}
	if len(p.Shared) == 0 {
		fmt.Fprintf(&b, "  the two pages have no element with the same path: either they are "+
			"very different, or their structure moved and this cannot tell those apart\n")
	}
	return b.String()
}

func main() {
	quiet := flag.Bool("quiet", false, "do not print the report on stderr")
	which := flag.String("write", "after", `which document to write with the names: "before" or "after"`)
	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "transitions: give it two files, before and after")
		os.Exit(2)
	}

	beforeBytes, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitions:", err)
		os.Exit(2)
	}
	afterBytes, err := os.ReadFile(flag.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitions:", err)
		os.Exit(2)
	}

	before, err := Scan(flag.Arg(0), strings.NewReader(string(beforeBytes)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitions:", err)
		os.Exit(1)
	}
	after, err := Scan(flag.Arg(1), strings.NewReader(string(afterBytes)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitions:", err)
		os.Exit(1)
	}

	p := Pair(before, after)

	source := string(afterBytes)
	if *which == "before" {
		source = string(beforeBytes)
	}
	if _, err := Apply(strings.NewReader(source), os.Stdout, p); err != nil {
		fmt.Fprintln(os.Stderr, "transitions:", err)
		os.Exit(1)
	}
	if !*quiet {
		fmt.Fprint(os.Stderr, Report(p))
	}
}

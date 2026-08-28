// Command histogram counts the elements in a document by tag name and prints
// them as a bar chart.
//
// Counting is the simplest thing a rewriter can do, which makes the two
// questions it raises the whole of the program.
//
// What is a tag name? TagName is lower-cased, which is what a count wants: a
// page that writes <DIV> and <div> has one kind of element and should have one
// row. TagNamePreserveCase is the source spelling, which matters for foreign
// content, where <linearGradient> is a different element from <lineargradient>
// to everything except an HTML parser. The histogram keys on the lower-case name
// and records the spellings, so a page that is inconsistent shows it rather than
// having it averaged away.
//
// And what is the same element? Selectors here do not consider namespaces, so
// "a" matches both an HTML link and an SVG <a>. They are different elements with
// the same name, and adding their counts together produces a number that is not
// about anything.
//
// NamespaceURI does not answer that question by itself, and the way it does not
// is worth knowing. It reports the namespace an element's *children* are parsed
// in, which is the element's own namespace everywhere except the integration
// points - <svg><title>, <math><mi> and the rest - where foreign content
// switches back to HTML. Asking <mi> for its namespace gives HTML.
//
// But it is exactly the right answer one level up. An element's own namespace is
// the namespace its parent's children are parsed in, so the program keeps a
// stack of NamespaceURI values and reads the top. That is the documented meaning
// used as it is rather than worked around, and it puts <math><mi> in MathML and
// an HTML <p> inside <svg><foreignObject> back in HTML.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// unknown labels a namespace the library does not name, which cannot happen
// today and would otherwise be silently folded into HTML.
const unknown = "?"

// A Kind is one row of the histogram: a tag name in a namespace.
type Kind struct {
	// Name is the lower-cased tag name.
	Name string
	// Prefix is "svg:", "math:" or empty, so a row reads as what it is.
	Prefix string
	// Count is how many start tags were seen.
	Count int
	// Spellings are the distinct source spellings, in the order first seen, and
	// only when there is more than one or it differs from Name.
	Spellings []string
}

// Label is what the chart puts at the start of a row.
func (k Kind) Label() string { return k.Prefix + k.Name }

// A Report is the whole count.
type Report struct {
	// Kinds are the rows, most frequent first, ties broken by name.
	Kinds []Kind
	// Total is every start tag counted.
	Total int
}

// Count reads the document and counts its elements.
func Count(r io.Reader) (Report, error) {
	type key struct{ prefix, name string }
	counts := map[key]int{}
	spellings := map[key][]string{}
	var order []key

	// ns is the namespace an element's children are parsed in, one entry per
	// enclosing element that changed it. The top is what the element being
	// reported is itself in. Each entry remembers the tag that pushed it, because
	// an end tag closes the nearest open element of that name and everything
	// opened after it - see the unwind below.
	type frame struct{ uri, tag string }
	ns := []frame{{uri: lolhtml.NamespaceHTML}}

	handler := lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		top := ns[len(ns)-1].uri
		own := top
		// <svg> and <math> are the two tags that enter foreign content, so they
		// are themselves foreign - taking the parent's answer would put them in
		// HTML, which is where they were written and not where they are. For
		// those two the children's namespace is also their own.
		if tag := e.TagName(); tag == "svg" || tag == "math" {
			if child := e.NamespaceURI(); child != "" {
				own = child
			}
		}
		k := key{prefix(own), e.TagName()}

		// Push only where the namespace changes - at <svg> and <math>, and back
		// at an integration point - so the stack holds a handful of entries on
		// any real document rather than one per element.
		if child := e.NamespaceURI(); child != "" && child != top && e.CanHaveContent() {
			tag := e.TagName()
			ns = append(ns, frame{uri: child, tag: tag})
			if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
				// An element whose end tag the source left out is handed the
				// enclosing one; unwinding on that would drop the wrong entry.
				if t.Name() != tag {
					return nil
				}
				// This element's entry is not necessarily the top one. An
				// HTML tag name inside an <svg> takes the parser out of
				// foreign content - 44 names do it - and the tags that follow
				// it, still inside the source <svg>, report the HTML namespace
				// and get pushed here. Nothing closes those by name, so
				// </svg> would pop the last of them and leave the svg entry
				// on top, labelling the whole rest of the document svg:.
				// An end tag closes the nearest open element of that name and
				// everything opened after it, so that is what this unwinds to.
				for i := len(ns) - 1; i > 0; i-- {
					if ns[i].tag == tag {
						ns = ns[:i]
						break
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
		if s := e.TagNamePreserveCase(); !contains(spellings[k], s) {
			spellings[k] = append(spellings[k], s)
		}
		return nil
	})

	// Nothing is rewritten, so the output goes nowhere.
	rw, err := lolhtml.NewWriter(io.Discard, handler)
	if err != nil {
		return Report{}, err
	}
	if _, err := io.Copy(rw, r); err != nil {
		rw.Close()
		return Report{}, err
	}
	if err := rw.Close(); err != nil {
		return Report{}, err
	}

	rep := Report{Kinds: make([]Kind, 0, len(order))}
	for _, k := range order {
		s := spellings[k]
		// One spelling that is already the lower-case name says nothing.
		if len(s) == 1 && s[0] == k.name {
			s = nil
		}
		rep.Kinds = append(rep.Kinds, Kind{
			Name:      k.name,
			Prefix:    k.prefix,
			Count:     counts[k],
			Spellings: s,
		})
		rep.Total += counts[k]
	}
	sort.SliceStable(rep.Kinds, func(i, j int) bool {
		if rep.Kinds[i].Count != rep.Kinds[j].Count {
			return rep.Kinds[i].Count > rep.Kinds[j].Count
		}
		return rep.Kinds[i].Label() < rep.Kinds[j].Label()
	})
	return rep, nil
}

// prefix turns a namespace URI into something short enough for a chart. An
// empty URI is what a detached or unreported element gives, and is kept
// distinct rather than folded into HTML.
func prefix(uri string) string {
	switch uri {
	case lolhtml.NamespaceHTML, "":
		return ""
	case lolhtml.NamespaceSVG:
		return "svg:"
	case lolhtml.NamespaceMathML:
		return "math:"
	default:
		return unknown + ":"
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Chart renders the report as text, with bars scaled so the largest row fills
// width columns.
func Chart(rep Report, width int) string {
	if len(rep.Kinds) == 0 {
		return "no elements\n"
	}
	// The total row is a row, so its label and count set the widths too.
	labelWidth, countWidth, top := len("total"), len(strconv.Itoa(rep.Total)), rep.Kinds[0].Count
	for _, k := range rep.Kinds {
		labelWidth = max(labelWidth, len(k.Label()))
		countWidth = max(countWidth, len(strconv.Itoa(k.Count)))
	}

	var b strings.Builder
	for _, k := range rep.Kinds {
		// At least one column for anything that occurred at all: a row that
		// rounds to nothing reads as a row that did not happen.
		bar := 1
		if top > 0 && width > 0 {
			bar = max(1, k.Count*width/top)
		}
		fmt.Fprintf(&b, "%-*s %*d %s", labelWidth, k.Label(), countWidth, k.Count,
			strings.Repeat("#", bar))
		if len(k.Spellings) > 0 {
			fmt.Fprintf(&b, "  (%s)", strings.Join(k.Spellings, ", "))
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%-*s %*d  in %d kinds\n", labelWidth, "total", countWidth,
		rep.Total, len(rep.Kinds))
	return b.String()
}

func main() {
	width := 40
	if len(os.Args) > 1 {
		n, err := strconv.Atoi(os.Args[1])
		if err != nil || n < 1 {
			fmt.Fprintln(os.Stderr, "usage: histogram [bar-width]")
			os.Exit(2)
		}
		width = n
	}
	rep, err := Count(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "histogram:", err)
		os.Exit(1)
	}
	fmt.Print(Chart(rep, width))
}

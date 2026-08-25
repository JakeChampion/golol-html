// Command idmerge concatenates several documents into one and keeps every id unique, rewriting
// the references as well as the ids.
//
//	$ idmerge one.html two.html three.html > merged.html
//	merged 3 documents: 14 ids kept, 6 renamed, 9 references rewritten
//	  two.html      #intro -> #intro-2, #toc -> #toc-2
//	  three.html    #intro -> #intro-3
//	  1 reference points at an id no document defines: #missing in two.html
//
// # Why this is two passes per document
//
// A reference can come before the id it points to. A table of contents at the top of a page links
// to headings further down, so a rewrite that renames an id has to know the mapping before it
// meets the first reference - and it cannot, because the id has not arrived. That is the ordering
// constraint the library documents, and here it is not a matter of choosing a better position:
// the evidence is genuinely later than the first place it is needed.
//
// So each document is read once to collect its ids, and rewritten once with the mapping decided.
// The mapping is shared across documents, which is what makes the ids unique in the
// concatenation rather than merely inside each part.
//
// # What counts as a reference
//
// More than href. The attributes that name an id are a fixed list, and a document that uses one
// of them and is renamed without it becomes a document whose labels point at nothing:
//
//	href="#id"                a link, and the only one most programs remember
//	for                       a label's field
//	form                      a field's form, from outside it
//	list                      an input's datalist
//	headers                   a cell's header cells, space-separated
//	aria-labelledby           space-separated
//	aria-describedby          space-separated
//	aria-controls, -owns      space-separated
//	usemap="#name"            an image's map, matched by name rather than id
//
// The space-separated ones are the reason this is not a search and replace: a value of
// "intro summary" is two references, and renaming one of them means rewriting the value rather
// than replacing it.
//
// # What it does not do
//
// It does not feed several documents into one rewriter. Two documents written into one Writer are
// one document to the parser: the second one's <html> lands inside the first one's body, and its
// <head> content with it. Each document is rewritten on its own and the outputs are joined, which
// is the only way to keep the parts parseable - and the join is the caller's problem, so this one
// wraps each part in a <section> and says so.
package main

import (
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// referenceAttributes are the attributes whose value names an id. The bool is whether the value
// is a space-separated list of them.
var referenceAttributes = map[string]bool{
	"for":                   false,
	"form":                  false,
	"list":                  false,
	"headers":               true,
	"aria-labelledby":       true,
	"aria-describedby":      true,
	"aria-controls":         true,
	"aria-owns":             true,
	"aria-flowto":           true,
	"aria-activedescendant": false,
}

// Document is one input and what was found in it.
type Document struct {
	Name string
	// IDs are the ids it defines, in document order.
	IDs []string
	// Renames maps an id to what it became, for the ones that collided.
	Renames map[string]string
	// Dangling are references to ids that no document defines. They are reported rather
	// than rewritten: a reference to a missing id is a fact about the input.
	Dangling []string
}

// Merger holds the namespace across documents.
type Merger struct {
	// taken is every id already used in the output.
	taken map[string]bool
	// defined is every id any input defines, which is how a dangling reference is told from
	// one that points into another part.
	defined map[string]bool

	Documents  []*Document
	References int
}

func NewMerger() *Merger {
	return &Merger{taken: map[string]bool{}, defined: map[string]bool{}}
}

// Collect reads a document and records the ids it defines, deciding a rename for each collision.
// It writes nothing: this is the pass that only looks.
func (m *Merger) Collect(name string, doc string) (*Document, error) {
	d := &Document{Name: name, Renames: map[string]string{}}

	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("[id]", func(e *lolhtml.Element) error {
			id := attrDecoded(e, "id")
			if id == "" {
				return nil
			}
			d.IDs = append(d.IDs, id)
			m.defined[id] = true

			if !m.taken[id] {
				m.taken[id] = true
				return nil
			}
			// A collision: the id keeps its shape and gains a suffix, so a reader can
			// still see where it came from.
			renamed := m.unique(id)
			d.Renames[id] = renamed
			m.taken[renamed] = true
			m.defined[renamed] = true
			return nil
		}),
		// A map is matched by name rather than by id, and usemap points at it - so a name
		// is part of the same namespace for this purpose.
		lolhtml.OnElement("map[name]", func(e *lolhtml.Element) error {
			name := attrDecoded(e, "name")
			if name == "" {
				return nil
			}
			m.defined[name] = true
			if !m.taken[name] {
				m.taken[name] = true
				return nil
			}
			renamed := m.unique(name)
			d.Renames[name] = renamed
			m.taken[renamed] = true
			m.defined[renamed] = true
			return nil
		}),
	); err != nil {
		return nil, err
	}
	m.Documents = append(m.Documents, d)
	return d, nil
}

// unique returns an unused id built from base.
func (m *Merger) unique(base string) string {
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !m.taken[candidate] {
			return candidate
		}
	}
}

// Rewrite writes a document with its renames applied, to ids and to every reference.
//
// It takes a reader rather than the string the collect pass was given, because this is the pass
// that streams: the mapping is already decided, so the bytes can arrive however they like.
func (m *Merger) Rewrite(d *Document, r io.Reader, w io.Writer) error {
	rename := func(id string) (string, bool) {
		to, ok := d.Renames[id]
		return to, ok
	}

	handlers := []lolhtml.Option{
		lolhtml.OnElement("[id]", func(e *lolhtml.Element) error {
			id := attrDecoded(e, "id")
			if to, ok := rename(id); ok {
				return e.SetAttribute("id", to)
			}
			return nil
		}),
		lolhtml.OnElement("map[name]", func(e *lolhtml.Element) error {
			name := attrDecoded(e, "name")
			if to, ok := rename(name); ok {
				return e.SetAttribute("name", to)
			}
			return nil
		}),
		lolhtml.OnElement("[href]", func(e *lolhtml.Element) error {
			href := attrRaw(e, "href")
			if !strings.HasPrefix(href, "#") {
				return nil
			}
			target := stdhtml.UnescapeString(href[1:])
			m.References++
			if to, ok := rename(target); ok {
				return e.SetAttribute("href", "#"+to)
			}
			if !m.defined[target] && target != "" {
				d.Dangling = append(d.Dangling, "#"+target)
			}
			return nil
		}),
		lolhtml.OnElement("[usemap]", func(e *lolhtml.Element) error {
			usemap := attrRaw(e, "usemap")
			if !strings.HasPrefix(usemap, "#") {
				return nil
			}
			target := stdhtml.UnescapeString(usemap[1:])
			m.References++
			if to, ok := rename(target); ok {
				return e.SetAttribute("usemap", "#"+to)
			}
			if !m.defined[target] {
				d.Dangling = append(d.Dangling, "usemap #"+target)
			}
			return nil
		}),
	}

	// One handler per reference attribute, because a selector list would not say which
	// attribute matched - and the rewriting differs between the single-valued ones and the
	// space-separated ones.
	for attr, list := range referenceAttributes {
		attr, list := attr, list
		handlers = append(handlers, lolhtml.OnElement("["+attr+"]", func(e *lolhtml.Element) error {
			value := attrRaw(e, attr)
			if value == "" {
				return nil
			}
			if !list {
				m.References++
				target := stdhtml.UnescapeString(value)
				if to, ok := rename(target); ok {
					return e.SetAttribute(attr, to)
				}
				if !m.defined[target] {
					d.Dangling = append(d.Dangling, attr+"="+target)
				}
				return nil
			}

			// A space-separated list: each entry is its own reference, so the value is
			// rebuilt rather than replaced.
			parts := strings.Fields(stdhtml.UnescapeString(value))
			changed := false
			for i, part := range parts {
				m.References++
				if to, ok := rename(part); ok {
					parts[i] = to
					changed = true
					continue
				}
				if !m.defined[part] {
					d.Dangling = append(d.Dangling, attr+"="+part)
				}
			}
			if !changed {
				return nil
			}
			return e.SetAttribute(attr, strings.Join(parts, " "))
		}))
	}

	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return err
	}
	return writer.Close()
}

// Merge collects and rewrites every document, wrapping each in a section so that the parts of the
// result are still recognisable - joining documents is the caller's decision and this is the
// simplest defensible one.
func (m *Merger) Merge(inputs []Input, w io.Writer) error {
	for _, in := range inputs {
		if _, err := m.Collect(in.Name, in.Doc); err != nil {
			return fmt.Errorf("collecting %s: %w", in.Name, err)
		}
	}
	for i, in := range inputs {
		if _, err := fmt.Fprintf(w, "<section data-source=%q>", in.Name); err != nil {
			return err
		}
		if err := m.Rewrite(m.Documents[i], strings.NewReader(in.Doc), w); err != nil {
			return fmt.Errorf("rewriting %s: %w", in.Name, err)
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}
	}
	return nil
}

// Input is one document to merge.
type Input struct {
	Name string
	Doc  string
}

// Report describes what the merge did.
func (m *Merger) Report() string {
	kept, renamed := 0, 0
	for _, d := range m.Documents {
		kept += len(d.IDs) - len(d.Renames)
		renamed += len(d.Renames)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "merged %d documents: %d ids kept, %d renamed, %d references seen\n",
		len(m.Documents), kept, renamed, m.References)
	for _, d := range m.Documents {
		if len(d.Renames) == 0 {
			continue
		}
		names := make([]string, 0, len(d.Renames))
		for from, to := range d.Renames {
			names = append(names, "#"+from+" -> #"+to)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "  %-14s %s\n", d.Name, strings.Join(names, ", "))
	}
	for _, d := range m.Documents {
		if len(d.Dangling) == 0 {
			continue
		}
		sort.Strings(d.Dangling)
		fmt.Fprintf(&b, "  %-14s %d reference(s) point at an id no document defines: %s\n",
			d.Name, len(d.Dangling), strings.Join(unique(d.Dangling), ", "))
	}
	return b.String()
}

// unique removes repeats while keeping order, so a report of the same dangling reference in five
// places says it once.
func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// attrDecoded reads an attribute and decodes it, which is right for an id: two ids are the same id
// when they are the same after decoding, and a document can spell one with a character reference.
func attrDecoded(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return stdhtml.UnescapeString(v)
}

// attrRaw reads an attribute as it was written, for the cases where the value is rewritten rather
// than compared.
func attrRaw(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func main() {
	quiet := flag.Bool("quiet", false, "do not print the report on stderr")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "idmerge: give it some files")
		os.Exit(2)
	}

	inputs := make([]Input, 0, flag.NArg())
	for _, name := range flag.Args() {
		doc, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "idmerge:", err)
			os.Exit(2)
		}
		inputs = append(inputs, Input{Name: name, Doc: string(doc)})
	}

	m := NewMerger()
	if err := m.Merge(inputs, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "idmerge:", err)
		os.Exit(1)
	}
	if !*quiet {
		fmt.Fprint(os.Stderr, m.Report())
	}
}

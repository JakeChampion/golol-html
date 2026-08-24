// Command classmap renames classes through a mapping, the way a CSS-modules
// build does.
//
//	classmap -map classes.json < page.html > out.html
//
// The mapping is JSON, original name to generated name. A class with no entry is
// left alone and reported, because a page referring to a class the stylesheet no
// longer defines is a bug worth seeing rather than a silent no-op.
//
// One pass is enough, and the reason is worth naming: selectors are matched
// against the document as it arrived, so renaming a class cannot trigger a rule
// keyed on the new name. There is no cascade to worry about, and no chance of a
// rename renaming itself.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	mapPath := flag.String("map", "", "JSON file of original-to-generated class names (required)")
	strict := flag.Bool("strict", false, "exit nonzero if any class had no mapping")
	alsoAttrs := flag.String("also", "", "comma-separated attributes to rewrite as well, such as data-class")
	flag.Parse()

	if *mapPath == "" {
		fmt.Fprintln(os.Stderr, "usage: classmap -map classes.json < in.html > out.html")
		os.Exit(2)
	}

	m, err := loadMap(*mapPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "classmap:", err)
		os.Exit(2)
	}

	r := &renamer{mapping: m}
	for _, a := range strings.Split(*alsoAttrs, ",") {
		if a = strings.TrimSpace(a); a != "" {
			r.also = append(r.also, a)
		}
	}

	if err := r.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "classmap:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, r.report())
	if *strict && len(r.unmapped) > 0 {
		os.Exit(1)
	}
}

func loadMap(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	for from, to := range m {
		if from == "" || to == "" {
			return nil, fmt.Errorf("%s: an empty class name in %q -> %q", path, from, to)
		}
		if strings.ContainsAny(to, ` "'<>&`) {
			return nil, fmt.Errorf("%s: %q maps to %q, which is not a single class name", path, from, to)
		}
	}
	return m, nil
}

type renamer struct {
	mapping map[string]string
	also    []string

	renamed  map[string]int
	unmapped map[string]int
	elements int
}

func (r *renamer) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (r *renamer) options() []lolhtml.Option {
	attrs := append([]string{"class"}, r.also...)

	opts := make([]lolhtml.Option, 0, len(attrs))
	for _, attr := range attrs {
		attr := attr
		opts = append(opts, lolhtml.OnElement("["+attr+"]", func(e *lolhtml.Element) error {
			raw, ok := e.Attribute(attr)
			if !ok {
				return nil
			}
			if attr == "class" {
				r.elements++
			}

			// The token list is decoded to be looked up - a class written
			// "caf&eacute;" is the class "café" - and the replacement is written
			// as the mapping gives it, which loadMap has already checked cannot
			// need escaping.
			out, changed := r.rewriteTokens(stdhtml.UnescapeString(raw))
			if !changed {
				return nil
			}
			return e.SetAttribute(attr, out)
		}))
	}
	return opts
}

// rewriteTokens maps each class in a space-separated list, preserving order and
// leaving unmapped names in place. Order matters: it is what a stylesheet's
// author sees in the DOM, and reordering makes a diff unreadable for no gain.
func (r *renamer) rewriteTokens(list string) (string, bool) {
	tokens := strings.Fields(list)
	if len(tokens) == 0 {
		return list, false
	}

	changed := false
	for i, tok := range tokens {
		to, ok := r.mapping[tok]
		if !ok {
			r.note(&r.unmapped, tok)
			continue
		}
		if to != tok {
			changed = true
		}
		r.note(&r.renamed, tok)
		tokens[i] = to
	}
	return strings.Join(tokens, " "), changed
}

func (r *renamer) note(m *map[string]int, key string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}

func (r *renamer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "elements=%d renamed=%d distinct=%d unmapped=%d\n",
		r.elements, total(r.renamed), len(r.renamed), len(r.unmapped))

	if len(r.unmapped) > 0 {
		names := make([]string, 0, len(r.unmapped))
		for n, c := range r.unmapped {
			names = append(names, fmt.Sprintf("%s=%d", n, c))
		}
		sort.Strings(names)
		fmt.Fprintf(&sb, "no mapping for: %s\n", strings.Join(names, " "))
	}
	return sb.String()
}

func total(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

func renameString(in string, mapping map[string]string, also ...string) (string, *renamer, error) {
	r := &renamer{mapping: mapping, also: also}
	var out bytes.Buffer
	err := r.run(strings.NewReader(in), &out)
	return out.String(), r, err
}

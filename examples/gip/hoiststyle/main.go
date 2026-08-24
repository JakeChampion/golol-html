// Command hoiststyle moves inline style attributes into a stylesheet.
//
//	hoiststyle < page.html > out.html
//	hoiststyle -prefix h -at head < page.html
//
// Each distinct declaration block becomes one class, so a style repeated across
// fifty elements is written once. The stylesheet is emitted at the document end,
// which is the only place a single pass can put it: the set of rules is not known
// until the last element has been seen.
//
// Two things this does not do, both because they need information a rewriter does
// not have. It does not merge or reorder declarations, since specificity and
// order decide what wins and a rewriter cannot see the other stylesheets. And it
// does not remove a style attribute it could not hoist, which is anything whose
// value it cannot parse as a declaration block.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
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
	prefix := flag.String("prefix", "s", "class name prefix")
	report := flag.Bool("report", false, "summarise on stderr")
	flag.Parse()

	if !validClassPrefix(*prefix) {
		fmt.Fprintf(os.Stderr, "hoiststyle: prefix %q is not a valid class name start\n", *prefix)
		os.Exit(2)
	}

	h := &hoister{prefix: *prefix, verbose: *report}
	if err := h.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hoiststyle:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, h.report())
}

// validClassPrefix keeps the generated class name a class name: it ends up in a
// class attribute and in a CSS selector, so anything that could close either is
// refused rather than escaped.
func validClassPrefix(p string) bool {
	if p == "" {
		return false
	}
	for i, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case (r >= '0' && r <= '9') || r == '-':
			if i == 0 {
				return false // a class may not start with a digit or a hyphen
			}
		default:
			return false
		}
	}
	return true
}

type rule struct {
	class string
	decls string
	uses  int
}

type hoister struct {
	prefix  string
	verbose bool

	// byDecls maps a normalised declaration block to its rule, so a repeated
	// style is written once.
	byDecls map[string]*rule
	order   []string
	skipped []string
}

func (h *hoister) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, h.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (h *hoister) options() []lolhtml.Option {
	return []lolhtml.Option{
		// [style] matches a present-but-empty attribute too, which is worth
		// having: an empty style is worth removing and not worth a rule.
		lolhtml.OnElement("[style]", func(e *lolhtml.Element) error {
			raw, ok := e.Attribute("style")
			if !ok {
				return nil
			}

			// Decoded to decide, because a declaration block written with
			// character references is the same block; the raw form is never
			// written back, since the value is leaving the document entirely.
			decls := normalise(stdhtml.UnescapeString(raw))
			if decls == "" {
				return e.RemoveAttribute("style")
			}
			if !plausibleDeclarations(decls) {
				h.skipped = append(h.skipped, truncate(decls))
				return nil
			}

			r := h.ruleFor(decls)
			r.uses++

			if err := e.RemoveAttribute("style"); err != nil {
				return err
			}
			existing, _ := e.Attribute("class")
			return e.SetAttribute("class", addClass(existing, r.class))
		}),

		// The document end is the only place that has seen every rule.
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if len(h.order) == 0 {
				return nil
			}
			// HTML: this is an element being constructed, and its tags have to
			// survive as markup. The declarations are the document's own bytes,
			// filtered by plausibleDeclarations so they cannot contain "<".
			return d.Append("\n<style>"+h.stylesheet()+"</style>\n", lolhtml.HTML)
		}),
	}
}

// ruleFor returns the rule for a declaration block, creating it on first use.
// The class name is derived from the declarations, so the same input always
// produces the same stylesheet - which matters for caching.
func (h *hoister) ruleFor(decls string) *rule {
	if h.byDecls == nil {
		h.byDecls = map[string]*rule{}
	}
	if r, ok := h.byDecls[decls]; ok {
		return r
	}

	sum := sha256.Sum256([]byte(decls))
	// Base32 without padding: lower-cased it is all letters and digits, so it is
	// a valid class name with no escaping.
	name := h.prefix + "-" + strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:5]))

	r := &rule{class: name, decls: decls}
	h.byDecls[decls] = r
	h.order = append(h.order, decls)
	return r
}

func (h *hoister) stylesheet() string {
	var sb strings.Builder
	for _, decls := range h.order {
		r := h.byDecls[decls]
		fmt.Fprintf(&sb, ".%s{%s}", r.class, r.decls)
	}
	return sb.String()
}

// normalise puts a declaration block into a canonical form, so that two styles
// differing only in spacing, a trailing semicolon or the case of a property name
// share one rule rather than two.
//
// Property names are lower-cased because CSS matches them case-insensitively.
// Values are not: a font family, a content string or a custom property value can
// all be case-sensitive.
func normalise(decls string) string {
	parts := strings.Split(decls, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, value, ok := strings.Cut(p, ":")
		if !ok {
			out = append(out, strings.Join(strings.Fields(p), " "))
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(name))+":"+
			strings.Join(strings.Fields(value), " "))
	}
	return strings.Join(out, ";")
}

// plausibleDeclarations refuses anything that would not survive being written
// into a stylesheet: a declaration block cannot contain the characters that end
// a rule or a <style> element, and one without a colon is not a declaration.
func plausibleDeclarations(decls string) bool {
	if !strings.Contains(decls, ":") {
		return false
	}
	return !strings.ContainsAny(decls, "<>{}\"")
}

// addClass appends a class to a class attribute, keeping what was there and not
// repeating itself.
func addClass(existing, add string) string {
	for _, c := range strings.Fields(existing) {
		if c == add {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return add
	}
	return strings.Join(append(strings.Fields(existing), add), " ")
}

func (h *hoister) report() string {
	rules := make([]*rule, 0, len(h.byDecls))
	uses := 0
	for _, r := range h.byDecls {
		rules = append(rules, r)
		uses += r.uses
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].uses > rules[j].uses })

	var sb strings.Builder
	fmt.Fprintf(&sb, "hoisted=%d rules=%d skipped=%d\n", uses, len(rules), len(h.skipped))
	if h.verbose {
		for _, r := range rules {
			fmt.Fprintf(&sb, "  %s used %d time(s): %s\n", r.class, r.uses, truncate(r.decls))
		}
	}
	for _, s := range h.skipped {
		fmt.Fprintf(&sb, "left inline: %s\n", s)
	}
	return sb.String()
}

func truncate(s string) string {
	if len(s) <= 50 {
		return s
	}
	return s[:50] + "..."
}

func hoistString(in string, opts ...func(*hoister)) (string, *hoister, error) {
	h := &hoister{prefix: "s"}
	for _, o := range opts {
		o(h)
	}
	var out bytes.Buffer
	err := h.run(strings.NewReader(in), &out)
	return out.String(), h, err
}

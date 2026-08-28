// Command preserve runs a set of rewrites over a document and says which of them left it
// alone.
//
//	$ preserve < page.html
//	rewrite                                          tokens      first difference
//	set an attribute on every element                preserved
//	add a class to every element                     preserved
//	rename b to strong                               preserved
//	comment before every element                     preserved
//	span after every element, guarded                preserved
//	replace each list item's content                 CHANGED     token 4: end li became end ul
//
// The rewrites are fixed and deliberately mixed: five are meaning-preserving on any
// document, and one is the documented hazard - replacing an element's content, which on a
// document that omits an end tag replaces everything up to the *enclosing* element's end
// and so deletes the items after it. Running them over a caller's own document says which
// shapes that document actually contains: the hazard is only a hazard where an end tag is
// missing.
//
// # What a token-level check can and cannot see
//
// This program compares what the rewriter itself reports: the sequence of element names,
// their nesting as the tokens describe it, their attribute names, and the spans of text
// and comments. That catches an element that ended early, a tag that moved, a name that
// changed, content that vanished.
//
// It cannot see the tree. Foster parenting moves content out of a table without changing
// a single token, and a content model that rejects what it is given deletes nodes the
// token stream still has - so a rewrite can pass here and still change the document a
// browser builds. Nor can it judge a wrapper: an insertion that means "put this around
// that" has no expression in a token sequence, so there is nothing to subtract before
// comparing. Both of those live in differential/preserving_test.go, where
// golang.org/x/net/html does the parsing. This program is the part that can run against a
// live document without a dependency.
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

// Rewrite is one rewrite to try, with what to ignore when comparing.
type Rewrite struct {
	Name string
	// Opts builds the handlers.
	Opts func() []lolhtml.Option
	// IgnoreAttrs are the attribute names the rewrite is allowed to add.
	IgnoreAttrs map[string]bool
	// IgnoreText drops text from the comparison, for a rewrite whose intended change is
	// to the text.
	IgnoreText bool
	// IgnoreElements are the element names the rewrite is allowed to insert. Only
	// insertions this program can subtract are worth trying: a wrapper cannot be
	// subtracted at all, since the token stream has no way to say "this element is
	// around that one" - which is why the wrapper case lives in the tree-level check.
	IgnoreElements map[string]bool
	// IgnoreComments drops comments from the comparison.
	IgnoreComments bool
	// Equate maps a new element name back to the old one.
	Equate map[string]string
	// Hazard says this rewrite is expected to change some documents: it is here to be
	// seen failing, not as a suggestion.
	Hazard bool
}

// Rewrites are the fixed set, safe ones first.
func Rewrites() []Rewrite {
	return []Rewrite{
		{
			Name: "set an attribute on every element",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					return e.SetAttribute("data-seen", "1")
				})}
			},
			IgnoreAttrs: map[string]bool{"data-seen": true},
		},
		{
			Name: "add a class to every element",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					c, _ := e.Attribute("class")
					return e.SetAttribute("class", strings.TrimSpace(c+" m"))
				})}
			},
			IgnoreAttrs: map[string]bool{"class": true},
		},
		{
			Name: "rename b to strong",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
					return e.SetTagName("strong")
				})}
			},
			Equate: map[string]string{"strong": "b"},
		},
		{
			Name: "comment before every element",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					return e.Before("<!--m-->", lolhtml.HTML)
				})}
			},
			IgnoreComments: true,
		},
		{
			Name: "span after every element, guarded",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					if !e.CanHaveContent() {
						return e.After(`<span data-m=""></span>`, lolhtml.HTML)
					}
					tag := e.TagName()
					return e.OnEndTag(func(x *lolhtml.EndTag) error {
						if x.Name() != tag {
							return nil // not this element's end: see the end-tag rule
						}
						return x.After(`<span data-m=""></span>`, lolhtml.HTML)
					})
				})}
			},
			IgnoreElements: map[string]bool{"span": true},
			IgnoreAttrs:    map[string]bool{"data-m": true},
		},
		{
			Name: "replace each list item's content",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("li", func(e *lolhtml.Element) error {
					return e.SetInnerContent("x", lolhtml.Text)
				})}
			},
			IgnoreText: true,
			Hazard:     true,
		},
	}
}

// token is one thing the rewriter reported, reduced to what should not change.
type token struct {
	Kind  string // "el", "end", "text", "comment"
	Name  string
	Attrs string
	Depth int
}

func (t token) String() string {
	if t.Attrs != "" {
		return fmt.Sprintf("%s %s[%s] at depth %d", t.Kind, t.Name, t.Attrs, t.Depth)
	}
	if t.Name != "" {
		return fmt.Sprintf("%s %s at depth %d", t.Kind, t.Name, t.Depth)
	}
	return fmt.Sprintf("%s at depth %d", t.Kind, t.Depth)
}

// structure reads a document through the rewriter and returns the tokens, with the
// rewrite's intended change taken out.
func structure(doc []byte, r Rewrite) ([]token, error) {
	var out []token
	depth := 0
	opts := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			name := e.TagName()
			if to, ok := r.Equate[name]; ok {
				name = to
			}
			if r.IgnoreElements[name] {
				// An element the rewrite is allowed to insert, and one the document
				// may have had of its own: both are dropped, so the comparison is
				// about everything else.
				if e.CanHaveContent() {
					return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
				}
				return nil
			}
			var names []string
			for _, a := range e.AttributeList() {
				if r.IgnoreAttrs[a.Name] {
					continue
				}
				names = append(names, a.Name)
			}
			sort.Strings(names)
			out = append(out, token{Kind: "el", Name: name, Attrs: strings.Join(names, ","), Depth: depth})
			if e.CanHaveContent() {
				depth++
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					depth--
					end := x.Name()
					if to, ok := r.Equate[end]; ok {
						end = to
					}
					out = append(out, token{Kind: "end", Name: end, Depth: depth})
					return nil
				})
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if r.IgnoreText {
				return nil
			}
			if s := strings.TrimSpace(c.Text()); s != "" {
				out = append(out, token{Kind: "text", Name: s, Depth: depth})
			}
			return nil
		}),
	}
	if !r.IgnoreComments {
		opts = append(opts, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			out = append(out, token{Kind: "comment", Name: c.Text(), Depth: depth})
			return nil
		}))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

// Outcome is what one rewrite did to the document.
type Outcome struct {
	Rewrite   string
	Hazard    bool
	Preserved bool
	Diff      string
	Err       error
}

// Check runs every rewrite over the document.
func Check(doc []byte) ([]Outcome, error) {
	var out []Outcome
	for _, r := range Rewrites() {
		before, err := structure(doc, r)
		if err != nil {
			return nil, err
		}
		rewritten, err := apply(doc, r)
		if err != nil {
			out = append(out, Outcome{Rewrite: r.Name, Hazard: r.Hazard, Err: err})
			continue
		}
		after, err := structure(rewritten, r)
		if err != nil {
			return nil, err
		}
		o := Outcome{Rewrite: r.Name, Hazard: r.Hazard, Preserved: true}
		if d := difference(before, after); d != "" {
			o.Preserved, o.Diff = false, d
		}
		out = append(out, o)
	}
	return out, nil
}

// apply runs the rewrite and returns the new document.
func apply(doc []byte, r Rewrite) ([]byte, error) {
	var b strings.Builder
	w, err := lolhtml.NewWriter(&b, r.Opts()...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// difference names the first token that differs, in the terms a caller can act on.
func difference(before, after []token) string {
	n := len(before)
	if len(after) < n {
		n = len(after)
	}
	for i := 0; i < n; i++ {
		if before[i] != after[i] {
			if before[i].Kind == "end" && after[i].Kind != "end" {
				return fmt.Sprintf("token %d: %s ended early", i, before[i].Name)
			}
			return fmt.Sprintf("token %d: %s became %s", i, before[i], after[i])
		}
	}
	switch {
	case len(after) > len(before):
		return fmt.Sprintf("token %d: %s appeared", n, after[n])
	case len(after) < len(before):
		return fmt.Sprintf("token %d: %s disappeared", n, before[n])
	}
	return ""
}

func report(outcomes []Outcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-46s %-11s %s\n", "rewrite", "tokens", "first difference")
	for _, o := range outcomes {
		state := "preserved"
		if o.Err != nil {
			state = "failed"
		} else if !o.Preserved {
			state = "CHANGED"
		}
		fmt.Fprintf(&b, "%-46s %-11s %s\n", o.Rewrite, state, o.Diff)
	}
	return b.String()
}

func main() {
	list := flag.Bool("list", false, "list the rewrites and which are hazards")
	flag.Parse()

	if *list {
		for _, r := range Rewrites() {
			kind := "preserving"
			if r.Hazard {
				kind = "hazard"
			}
			fmt.Printf("%-46s %s\n", r.Name, kind)
		}
		return
	}

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "preserve:", err)
		os.Exit(2)
	}
	outcomes, err := Check(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "preserve:", err)
		os.Exit(2)
	}
	fmt.Print(report(outcomes))

	// A preserving rewrite that changed the tokens is the interesting failure; a hazard
	// that changed them is the point.
	for _, o := range outcomes {
		if !o.Hazard && (!o.Preserved || o.Err != nil) {
			os.Exit(1)
		}
	}
}

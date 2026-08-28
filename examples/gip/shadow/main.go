// Command shadow gives every custom element a declarative shadow root, and gives it exactly
// once, so the same page can go through twice without gaining two.
//
//	$ shadow -t my-card=card.html -t my-badge=badge.html page.html
//	4 hosts, 1 already had a shadow root, 3 given one
//	  my-card      2 given
//	  my-badge     1 given, 1 already had one
//
// A declarative shadow root is a <template shadowrootmode="open"> child of its host, and the
// parser attaches it when the template's end tag arrives. So inserting one is an insertion into
// the host - and the question is where in the host, which turns out to matter more than it
// looks.
//
// # Why the end tag and not the start tag
//
// Two reasons, and both are measurements rather than preferences.
//
// The first is that a rewrite that inserts at the start tag cannot know whether it is needed. A
// host that already has a shadow root must be left alone, and whether it has one is only known
// once its children have gone past - by which time the start tag is behind the rewriter and
// nothing can be inserted there. Inserting at the end tag puts the decision after the evidence:
// the detecting handler for `my-card > template[shadowrootmode]` has already run or it has not.
//
// The direct-child part of that selector is load-bearing. A declarative shadow root is a child
// of its host, and a template deeper inside is an ordinary template:
//
//	<my-card><div><template shadowrootmode="open">…</template></div></my-card>
//
//	my-card > template[shadowrootmode]     matches 0
//	template[shadowrootmode]               matches 1
//
// The second reason is that the two ways of inserting at the end of an element are not the same
// when the source omits the end tag. On `<ul><li>a<li>b<li>c</ul>`, where the rewriter has each
// item still open inside the last:
//
//	source                      <ul><li>a<li>b<li>c</ul>
//	Append per item             <ul><li>a<li>b<li>c[A1]</ul>
//	EndTag.Before per item      <ul><li>a<li>b<li>c[B3][B2][B1]</ul>
//	After per item              <ul><li>a<li>b<li>c</ul>[C1]
//
// Append keeps the outermost item's insertion and silently discards the other two. So does
// After, outside the list. EndTag.Before keeps all three, innermost first, because all three
// handlers run at the one `</ul>`. Neither position is right - the content belongs at each
// item's own end, and there is no such position in the source - but one of them loses content
// and the other does not, and a rewrite whose whole job is to add something should be the kind
// that does not lose it. Every one of these is silent: no error either way.
//
// # What it cannot do
//
// A host whose end tag never arrives gets nothing, because an end-tag handler for an element
// nothing closes never runs. `<my-card/>` is the case that costs someone an afternoon: HTML
// ignores the slash on an element that is neither void nor foreign, so the host opens and runs
// to the end of the document. [lolhtml.Element.IsSelfClosing] reports true and
// [lolhtml.Element.CanHaveContent] also reports true, so neither is a test for it. The report
// counts those hosts rather than pretending they were done.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Templates maps a host tag name to the shadow root markup for it.
type Templates map[string]string

// tagList is the -t flag: repeatable, tag=path.
type tagList struct {
	t Templates
}

func (l *tagList) String() string { return "" }

func (l *tagList) Set(v string) error {
	tag, path, ok := strings.Cut(v, "=")
	if !ok || tag == "" || path == "" {
		return errors.New("want tag=path")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if l.t == nil {
		l.t = Templates{}
	}
	l.t[strings.ToLower(tag)] = strings.TrimRight(string(b), "\n")
	return nil
}

// Count is what happened to one host tag.
type Count struct {
	Tag string
	// Given is the number of hosts that got a shadow root, Had the number that already had
	// one, and Unclosed the number whose end tag never arrived, which get nothing.
	Given    int
	Had      int
	Unclosed int
}

// Result is what a run did.
type Result struct {
	Doc    string
	Counts map[string]*Count
}

// Total adds a field across the tags.
func (r Result) Total(f func(*Count) int) int {
	n := 0
	for _, c := range r.Counts {
		n += f(c)
	}
	return n
}

// Tags lists the host tags that appeared, in order.
func (r Result) Tags() []string {
	out := make([]string, 0, len(r.Counts))
	for tag := range r.Counts {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// Insert gives every host in src a declarative shadow root unless it already has one.
//
// The mode is the shadowrootmode of the templates it inserts, and it also decides what counts as
// an existing shadow root: a host with a template of either mode is left alone, because two
// shadow roots on one host is not a thing.
func Insert(src io.Reader, templates Templates, mode string) (Result, error) {
	if len(templates) == 0 {
		return Result{}, errors.New("shadow: no templates")
	}
	if mode != "open" && mode != "closed" {
		return Result{}, fmt.Errorf("shadow: mode %q is not open or closed", mode)
	}

	res := Result{Counts: map[string]*Count{}}
	count := func(tag string) *Count {
		c, ok := res.Counts[tag]
		if !ok {
			c = &Count{Tag: tag}
			res.Counts[tag] = c
		}
		return c
	}

	// hasRoot[n] records that the host opened as the n-th host has a shadow root already.
	// The detecting handler cannot ask what its parent is, so the depth of open hosts is
	// what connects the two.
	hasRoot := map[int]bool{}
	type host struct {
		id  int
		tag string
	}
	var open []host
	seq := 0

	var opts []lolhtml.Option
	for tag, markup := range templates {
		tag, markup := tag, markup

		// A declarative shadow root is a direct child, so the selector says so: a
		// template deeper inside the host is an ordinary template.
		opts = append(opts, lolhtml.OnElement(tag+" > template[shadowrootmode]",
			func(*lolhtml.Element) error {
				if len(open) > 0 {
					hasRoot[open[len(open)-1].id] = true
				}
				return nil
			}))

		opts = append(opts, lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
			seq++
			id := seq
			c := count(tag)

			// A host that cannot hold content cannot hold a shadow root either.
			// Nothing in HTML makes a custom element void, so this is here for the
			// foreign-content case rather than for a hyphenated tag.
			if !e.CanHaveContent() {
				c.Unclosed++
				return nil
			}

			open = append(open, host{id: id, tag: tag})
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				open = open[:len(open)-1]
				had := hasRoot[id]
				delete(hasRoot, id)
				if had {
					c.Had++
					return nil
				}
				if end.Name() != tag {
					// Not this host's end tag. The source left it out, so the
					// token belongs to an enclosing element and the position is
					// outside the host: on <my-card><my-badge>x</my-card> the
					// badge's handler runs at </my-card>, and inserting there
					// gives the badge a second shadow root - or the card's root
					// to the badge - while the report claims both were done. A
					// host with no end tag of its own is the case counted below,
					// so it is counted the same way here.
					c.Unclosed++
					return nil
				}
				c.Given++
				return end.Before(shadowRoot(markup, mode), lolhtml.HTML)
			})
		}))
	}

	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}

	// Whatever is still open never had an end tag, so it never got a shadow root. This is
	// the only signal available: an end-tag handler for an element nothing closes does not
	// run, and nothing reports that it did not.
	for _, h := range open {
		count(h.tag).Unclosed++
	}
	res.Doc = out.String()
	return res, nil
}

// shadowRoot wraps the markup in the template that makes it a declarative shadow root.
func shadowRoot(markup, mode string) string {
	return `<template shadowrootmode="` + mode + `">` + markup + `</template>`
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d hosts, %d already had a shadow root, %d given one",
		r.Total(func(c *Count) int { return c.Given + c.Had + c.Unclosed }),
		r.Total(func(c *Count) int { return c.Had }),
		r.Total(func(c *Count) int { return c.Given }))
	if n := r.Total(func(c *Count) int { return c.Unclosed }); n > 0 {
		fmt.Fprintf(&b, ", %d with no end tag and so left alone", n)
	}
	b.WriteString("\n")
	for _, tag := range r.Tags() {
		c := r.Counts[tag]
		parts := []string{fmt.Sprintf("%d given", c.Given)}
		if c.Had > 0 {
			parts = append(parts, fmt.Sprintf("%d already had one", c.Had))
		}
		if c.Unclosed > 0 {
			parts = append(parts, fmt.Sprintf("%d with no end tag", c.Unclosed))
		}
		fmt.Fprintf(&b, "  %-14s %s\n", tag, strings.Join(parts, ", "))
	}
	return b.String()
}

func main() {
	var list tagList
	flag.Var(&list, "t", "tag=path of a template to insert, repeatable")
	mode := flag.String("mode", "open", "shadowrootmode of the templates inserted")
	report := flag.Bool("report", false, "print the counts instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "shadow:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	res, err := Insert(src, list.t, *mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shadow:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(res)
		return
	}
	fmt.Print(res.Doc)
}

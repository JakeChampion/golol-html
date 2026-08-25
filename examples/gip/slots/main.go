// Command slots fills named slots in a template with supplied fragments.
//
//	<slot name="title">Untitled</slot>
//
// A slot with a fragment for its name gets the fragment; a slot without one keeps
// what is inside it, which is the default. That part is easy, and it is easy for
// a reason worth naming: the decision needs only the start tag. The name is an
// attribute, the fragments are known before the document starts, and nothing
// about the answer depends on what comes later. Where a rewrite can be decided at
// the start tag, a stream is as good as a tree.
//
// The program then does the thing that is not easy, because a template language
// wants it: a fragment can be defined by the document.
//
//	<template data-fill="title">The <em>real</em> title</template>
//
// That runs into the ordering constraint. An insertion can only go where the
// rewriter has not been, so a definition can only fill slots that come after it.
// A definition below the slot it was meant for cannot be used, and this program
// does not pretend otherwise: it counts those as [Result.Late] and reports them,
// with the default content left in place. The answer for a document written that
// way is to read it twice, and the point of the count is to say which documents
// need it.
//
// Collecting a definition means holding its markup, which means rebuilding it
// from tokens: start tags with their attributes, text as the document spelled it,
// comments, end tags. Two things about that are measured rather than assumed.
//
// The attribute names come from AttributeList and not from the Attributes
// iterator, because the iterator lower-cases them. In HTML that is harmless. In
// SVG it is not: viewbox is not viewBox and a browser ignores it, so a definition
// holding a chart would come out as a chart-shaped nothing. See
// [lolhtml.Element.SetAttribute], which has the same rule from the other side -
// adding an attribute lower-cases its name, so an SVG attribute can be updated
// and cannot be introduced.
//
// Taking an element's tags away takes the token that closed it, and where a
// document leaves a tag out that token belongs to something else:
// <div><template data-fill=x>a</div> would lose its </div>. The repair is the
// name guard on [lolhtml.Element.OnEndTag] - a callback whose name is not this
// element's is standing on a borrowed token - and this program uses it in both
// places it unwraps something.
//
// Fragments can hold slots of their own. Inserted content is not re-parsed, so a
// slot inside a fragment would reach the page as a slot; instead each fragment is
// run through a filler of its own before it goes in. Fragments are strings that
// are already in memory, so this buffers without apology - the include program,
// which fetches its fragments, cannot afford to and does not. A depth limit stops
// the recursion and a path stops a cycle, and a slot that hits either keeps its
// default rather than disappearing.
//
// The -unwrap flag takes the <slot> tags away and leaves the content, which is
// what a server-side template usually wants. Without it the tags stay, which is
// valid HTML and is what a browser's own slot mechanism expects.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Options are the flags and the limits.
type Options struct {
	// Unwrap removes the <slot> tags, leaving the filled content.
	Unwrap bool
	// MaxDepth is how many levels of slot-inside-a-fragment to fill.
	MaxDepth int
	// MaxDefinition is how many bytes of an in-document definition to hold. A
	// definition is markup from the document, so a number is needed.
	MaxDefinition int
}

// DefaultOptions are what main uses.
var DefaultOptions = Options{MaxDepth: 3, MaxDefinition: 64 << 10}

// A Result counts what happened.
type Result struct {
	// Slots seen, Filled from a fragment or a definition, and Defaults left as
	// the template wrote them.
	Slots, Filled, Defaults int
	// Unnamed slots, which cannot be filled and are left alone.
	Unnamed int
	// Definitions collected from the document, and Late ones that arrived after
	// a slot had already asked for them.
	Definitions, Late int
	// TooBig definitions, dropped rather than held.
	TooBig int
	// TooDeep and Cycles are slots inside fragments that were not filled.
	TooDeep, Cycles int
}

func (r Result) String() string {
	return fmt.Sprintf("slots: %d slots: %d filled, %d defaults, %d unnamed; "+
		"%d definitions (%d late, %d too big); %d too deep, %d cycles",
		r.Slots, r.Filled, r.Defaults, r.Unnamed, r.Definitions, r.Late, r.TooBig,
		r.TooDeep, r.Cycles)
}

// Fill copies src to dst with every slot filled.
func Fill(dst io.Writer, src io.Reader, fragments map[string]string, opts Options) (Result, error) {
	f := newFiller(fragments, opts)
	w, err := lolhtml.NewWriter(dst, f.options()...)
	if err != nil {
		return *f.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return *f.res, err
	}
	if err := w.Close(); err != nil {
		return *f.res, err
	}
	return *f.res, nil
}

type filler struct {
	frags map[string]string
	opts  Options
	res   *Result

	// defs are the definitions collected from the document so far, and wanted is
	// the names a slot has already asked for and not got - which is how a
	// definition finds out it is late.
	defs   map[string]string
	wanted map[string]bool

	// depth and path are the fragment recursion: how deep, and how it got here.
	depth int
	path  []string

	// collector is set while a definition is being collected, and everything
	// inside it is buffered rather than filled.
	collector *collector
	// gen changes when a definition ends, so a callback from an abandoned one
	// does nothing.
	gen int
}

func newFiller(fragments map[string]string, opts Options) *filler {
	return &filler{
		frags:  fragments,
		opts:   opts,
		res:    &Result{},
		defs:   map[string]string{},
		wanted: map[string]bool{},
	}
}

// nested is the filler for a fragment: one level deeper, with the slot's name on
// the path so a fragment that fills a slot with itself is a cycle. It shares the
// counts and the definitions, because those belong to the document.
func (f *filler) nested(name string) *filler {
	return &filler{
		frags:  f.frags,
		opts:   f.opts,
		res:    f.res,
		defs:   f.defs,
		wanted: f.wanted,
		depth:  f.depth + 1,
		path:   append(append([]string{}, f.path...), name),
	}
}

func (f *filler) options() []lolhtml.Option {
	// Built here, per rewriter: the handlers close over this filler.
	return []lolhtml.Option{
		lolhtml.OnElement("*", f.element),
		lolhtml.OnDocumentText(f.text),
		lolhtml.OnDocumentComment(f.comment),
	}
}

func (f *filler) element(e *lolhtml.Element) error {
	if f.collector != nil {
		return f.collect(e)
	}
	name := e.TagName()
	if name == "template" {
		if fill, ok := e.Attribute("data-fill"); ok && fill != "" {
			return f.startDefinition(e, fill)
		}
		return nil
	}
	if name != "slot" {
		return nil
	}
	return f.fill(e)
}

func (f *filler) text(t *lolhtml.TextChunk) error {
	if f.collector == nil {
		return nil
	}
	if s := t.Text(); s != "" {
		if !f.collector.add(piece{text: s}) {
			return nil
		}
	}
	t.Remove()
	return nil
}

func (f *filler) comment(c *lolhtml.Comment) error {
	if f.collector == nil {
		return nil
	}
	if !f.collector.add(piece{markup: "<!--" + c.Text() + "-->"}) {
		return nil
	}
	c.Remove()
	return nil
}

// fill is the whole of the easy half: the name is on the start tag, so the
// decision needs nothing the rewriter has not seen.
func (f *filler) fill(e *lolhtml.Element) error {
	f.res.Slots++
	name, ok := e.Attribute("name")
	if !ok || name == "" {
		f.res.Unnamed++
		return nil
	}

	content, ok := f.frags[name]
	if !ok {
		content, ok = f.defs[name]
	}
	if !ok {
		// Nothing to put in it, so the default content stays. Remembering the
		// name is what lets a definition further down the document report that it
		// was too late to be used.
		f.wanted[name] = true
		f.res.Defaults++
		return nil
	}

	if f.depth > f.opts.MaxDepth {
		f.res.TooDeep++
		f.res.Defaults++
		return nil
	}
	for _, seen := range f.path {
		if seen == name {
			f.res.Cycles++
			f.res.Defaults++
			return nil
		}
	}

	// A fragment can hold slots, and inserted content is not re-parsed, so it is
	// filled before it goes in. Fragments are already strings in memory: nothing
	// is lost by buffering one.
	expanded, err := f.expand(name, content)
	if err != nil {
		return err
	}
	if err := e.SetInnerContent(expanded, lolhtml.HTML); err != nil {
		return err
	}
	f.res.Filled++
	if !f.opts.Unwrap {
		return nil
	}
	return unwrap(e)
}

func (f *filler) expand(name, content string) (string, error) {
	nested := f.nested(name)
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, nested.options()...)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, content); err != nil {
		w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// startDefinition begins collecting a <template data-fill="name">. Everything
// inside it is buffered instead of filled: the slots in a definition belong to
// whoever uses the definition.
func (f *filler) startDefinition(e *lolhtml.Element, name string) error {
	f.collector = &collector{name: name, limit: f.opts.MaxDefinition}
	gen := f.gen
	if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
		if f.gen != gen || f.collector == nil {
			return nil
		}
		c := f.collector
		f.collector = nil
		f.gen++

		if c.tooBig {
			f.res.TooBig++
		} else {
			f.defs[c.name] = c.render()
			f.res.Definitions++
			if f.wanted[c.name] {
				// A slot above this one asked for this name and got its default.
				// Nothing here can go back and fill it.
				f.res.Late++
			}
		}
		if t.Name() != c.name && t.Name() != "template" {
			// The token that closed the template belongs to an enclosing element,
			// and taking the template's tags away took it. Put it back.
			return t.Before("</"+t.Name()+">", lolhtml.HTML)
		}
		return nil
	}); err != nil {
		return err
	}
	e.RemoveAndKeepContent()
	return nil
}

// collect buffers one element of a definition and takes its tags out of the
// output.
func (f *filler) collect(e *lolhtml.Element) error {
	name := e.TagName()
	if lolhtml.IsRawText(name) {
		// A script or a style inside a definition holds content that is not
		// markup, and rebuilding it as markup would turn its text into elements.
		// The definition is dropped rather than mangled.
		f.collector.tooBig = true
		return nil
	}
	if !f.collector.add(piece{markup: startTag(e)}) {
		return nil
	}
	if e.IsSelfClosing() || !e.CanHaveContent() {
		e.Remove()
		return nil
	}
	gen := f.gen
	c := f.collector
	if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
		if f.gen != gen {
			return nil
		}
		if t.Name() != name {
			// Not this element's end tag: the token belongs to an enclosing
			// element, which writes it itself.
			return nil
		}
		c.add(piece{markup: "</" + name + ">"})
		return nil
	}); err != nil {
		return err
	}
	e.RemoveAndKeepContent()
	return nil
}

// unwrap takes an element's tags away and writes back the closing token if the
// token that closed it belonged to something else.
func unwrap(e *lolhtml.Element) error {
	name := e.TagName()
	if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
		if t.Name() == name {
			return nil
		}
		return t.Before("</"+t.Name()+">", lolhtml.HTML)
	}); err != nil {
		return err
	}
	e.RemoveAndKeepContent()
	return nil
}

// A piece is one part of a collected definition: markup that was rebuilt, or text
// as the document spelled it.
type piece struct {
	markup string
	text   string
}

// A collector holds a definition while it arrives.
type collector struct {
	name   string
	limit  int
	size   int
	pieces []piece
	// tooBig says the definition is being abandoned - it outgrew the limit, or it
	// holds something that cannot be rebuilt as markup.
	tooBig bool
}

// add buffers a piece and reports whether it was taken. False means the
// definition is being abandoned, so the caller should leave the token where it
// is: half a definition in the output is worse than the whole one.
func (c *collector) add(p piece) bool {
	if c.tooBig {
		return false
	}
	c.size += len(p.markup) + len(p.text)
	if c.size > c.limit {
		c.tooBig = true
		return false
	}
	c.pieces = append(c.pieces, p)
	return true
}

func (c *collector) render() string {
	var b strings.Builder
	for _, p := range c.pieces {
		b.WriteString(p.markup)
		b.WriteString(p.text)
	}
	return b.String()
}

// startTag rebuilds a start tag.
//
// The names come from AttributeList rather than the Attributes iterator, which
// lower-cases them: in SVG the case is part of the name. The values are the
// document's own source, so writing them back needs no escaping - except for the
// double quote, which is the one character that could end an attribute this
// program is writing with double quotes.
//
// What a rebuild cannot keep is the quoting: an attribute's value is reported
// without it, so <img src=x> comes back as <img src="x">. An attribute with no
// value at all is still written with no value, which is the part that would change
// what a parser sees.
func startTag(e *lolhtml.Element) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(e.TagName())
	for _, a := range e.AttributeList() {
		b.WriteByte(' ')
		b.WriteString(a.NamePreserveCase)
		if a.Value != "" {
			b.WriteString(`="`)
			b.WriteString(strings.ReplaceAll(a.Value, `"`, "&quot;"))
			b.WriteByte('"')
		}
	}
	if e.IsSelfClosing() {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteByte('>')
	return b.String()
}

func main() {
	opts := DefaultOptions
	fragments := map[string]string{}
	for _, arg := range os.Args[1:] {
		if arg == "-unwrap" {
			opts.Unwrap = true
			continue
		}
		name, path, ok := strings.Cut(arg, "=")
		if !ok || name == "" {
			fmt.Fprintln(os.Stderr, "usage: slots [-unwrap] [name=file ...] < template")
			os.Exit(2)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "slots:", err)
			os.Exit(1)
		}
		fragments[name] = string(body)
	}
	res, err := Fill(os.Stdout, os.Stdin, fragments, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slots:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

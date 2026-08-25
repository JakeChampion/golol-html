// Command handlerstats reports how many times each registered handler fired.
//
// Throughput here tracks the number of crossings into Go rather than the size of
// the document, so the number that explains a slow rewrite is how often each
// handler ran. The library reports nothing about that, and it cannot be recovered
// afterwards: an Option is opaque once built, so there is nothing to wrap and
// nothing to ask.
//
// What can be done is to wrap the function before the Option is built, which is
// what this package does - one constructor per handler kind, each returning the
// Option the library would have returned and counting on the way through.
//
// Three things it measures that are easy to guess wrong.
//
// A text handler fires per chunk, not per text node, and the last chunk of every
// node is usually empty. So a page with one paragraph can report three text
// calls, two of which carry nothing, and a handler that does work per call does
// it on empty strings.
//
// An element handler fires per start tag, so a selector that matches a common
// element is the cost of the rewrite whatever the handler does. Comparing calls
// against the element count says whether a selector is doing any narrowing.
//
// An end-tag handler is registered from inside an element handler, so it cannot
// be wrapped from out here. Route it through Counter.EndTag and it is counted;
// call e.OnEndTag directly and it is not. That is a limitation of instrumenting
// from outside, and it is reported rather than hidden: the report says how many
// end-tag handlers were registered through this package.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Counter builds instrumented handlers. The zero value is ready to use.
//
// It is not safe to share one across Writers running concurrently. Within one
// Writer the rewriter runs handlers one at a time, which is what makes a plain
// counter enough.
type Counter struct {
	rows  map[string]*Row
	order []string
	// built is where a caller can keep the options it made, so that building
	// and running are two steps rather than one expression. The Counter does
	// not use it.
	built []lolhtml.Option
}

// A Row is one handler's tally.
type Row struct {
	// Name is the kind and selector, as it would be written in the source.
	Name string
	// Calls is how many times the handler ran.
	Calls int
	// Empty is how many of those carried nothing: a text chunk with no bytes.
	// Always zero for handlers that are not text handlers.
	Empty int
	// Bytes is the total length of the text a text handler was handed, so a
	// caller can see the difference between many small chunks and few large
	// ones.
	Bytes int
	// Errors is how many calls returned an error. At most one matters, since
	// the first stops the rewrite, but a handler that returns errors it expects
	// to be ignored is worth seeing.
	Errors int
}

func (c *Counter) row(name string) *Row {
	if c.rows == nil {
		c.rows = map[string]*Row{}
	}
	r, ok := c.rows[name]
	if !ok {
		r = &Row{Name: name}
		c.rows[name] = r
		c.order = append(c.order, name)
	}
	return r
}

// OnElement counts an element handler.
func (c *Counter) OnElement(selector string, fn func(*lolhtml.Element) error) lolhtml.Option {
	r := c.row(`OnElement("` + selector + `")`)
	return lolhtml.OnElement(selector, func(e *lolhtml.Element) error {
		r.Calls++
		return c.record(r, fn(e))
	})
}

// OnText counts a text handler, and separates the chunks that carry nothing.
func (c *Counter) OnText(selector string, fn func(*lolhtml.TextChunk) error) lolhtml.Option {
	r := c.row(`OnText("` + selector + `")`)
	return lolhtml.OnText(selector, func(t *lolhtml.TextChunk) error {
		r.Calls++
		if n := len(t.Bytes()); n == 0 {
			r.Empty++
		} else {
			r.Bytes += n
		}
		return c.record(r, fn(t))
	})
}

// OnComment counts a comment handler.
func (c *Counter) OnComment(selector string, fn func(*lolhtml.Comment) error) lolhtml.Option {
	r := c.row(`OnComment("` + selector + `")`)
	return lolhtml.OnComment(selector, func(cm *lolhtml.Comment) error {
		r.Calls++
		return c.record(r, fn(cm))
	})
}

// OnDoctype counts a doctype handler.
func (c *Counter) OnDoctype(fn func(*lolhtml.Doctype) error) lolhtml.Option {
	r := c.row("OnDoctype()")
	return lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
		r.Calls++
		return c.record(r, fn(d))
	})
}

// OnDocumentText counts a document-level text handler.
func (c *Counter) OnDocumentText(fn func(*lolhtml.TextChunk) error) lolhtml.Option {
	r := c.row("OnDocumentText()")
	return lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
		r.Calls++
		if n := len(t.Bytes()); n == 0 {
			r.Empty++
		} else {
			r.Bytes += n
		}
		return c.record(r, fn(t))
	})
}

// OnDocumentComment counts a document-level comment handler.
func (c *Counter) OnDocumentComment(fn func(*lolhtml.Comment) error) lolhtml.Option {
	r := c.row("OnDocumentComment()")
	return lolhtml.OnDocumentComment(func(cm *lolhtml.Comment) error {
		r.Calls++
		return c.record(r, fn(cm))
	})
}

// OnDocumentEnd counts a document-end handler.
func (c *Counter) OnDocumentEnd(fn func(*lolhtml.DocumentEnd) error) lolhtml.Option {
	r := c.row("OnDocumentEnd()")
	return lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		r.Calls++
		return c.record(r, fn(d))
	})
}

// EndTag registers an end-tag handler on e and counts it, under a name that
// records which element handler registered it.
//
// This exists because an end-tag handler is registered from inside a running
// handler rather than when the rewriter is built, so there is no Option to wrap.
// Anything registered with e.OnEndTag directly is invisible here.
func (c *Counter) EndTag(from string, e *lolhtml.Element, fn func(*lolhtml.EndTag) error) error {
	r := c.row(`OnEndTag(from ` + from + `)`)
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		r.Calls++
		return c.record(r, fn(t))
	})
}

func (c *Counter) record(r *Row, err error) error {
	if err != nil {
		r.Errors++
	}
	return err
}

// Rows returns the tallies, most-called first, ties broken by registration
// order so a report of a document with no matches still lists every handler.
func (c *Counter) Rows() []Row {
	out := make([]Row, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, *c.rows[name])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}

// Total is the number of handler calls, which is the number of crossings into
// Go and the thing that tracks the cost of the rewrite.
func (c *Counter) Total() int {
	n := 0
	for _, r := range c.rows {
		n += r.Calls
	}
	return n
}

// Report renders the tallies as a table.
func (c *Counter) Report() string {
	rows := c.Rows()
	if len(rows) == 0 {
		return "no handlers registered\n"
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Name))
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s %6d calls", width, r.Name, r.Calls)
		if r.Empty > 0 {
			fmt.Fprintf(&b, ", %d empty", r.Empty)
		}
		if r.Bytes > 0 {
			fmt.Fprintf(&b, ", %d bytes", r.Bytes)
		}
		if r.Errors > 0 {
			fmt.Fprintf(&b, ", %d errors", r.Errors)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%-*s %6d calls\n", width, "total", c.Total())
	return b.String()
}

func main() {
	var c Counter
	opts := []lolhtml.Option{
		c.OnElement("a[href]", func(*lolhtml.Element) error { return nil }),
		c.OnElement("img", func(*lolhtml.Element) error { return nil }),
		c.OnElement("*", func(*lolhtml.Element) error { return nil }),
		c.OnText("p", func(*lolhtml.TextChunk) error { return nil }),
		c.OnComment("*", func(*lolhtml.Comment) error { return nil }),
		c.OnDoctype(func(*lolhtml.Doctype) error { return nil }),
		c.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return nil }),
	}

	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "handlerstats:", err)
		os.Exit(1)
	}
	if _, err := io.Copy(w, os.Stdin); err != nil {
		w.Close()
		fmt.Fprintln(os.Stderr, "handlerstats:", err)
		os.Exit(1)
	}
	if err := w.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "handlerstats:", err)
		os.Exit(1)
	}
	fmt.Print(c.Report())
}

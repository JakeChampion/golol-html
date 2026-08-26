// Command worstshape finds the document shape a handler set is slowest on, by running the same
// handlers over documents of the same size in different shapes and ranking them.
//
// The point is that document size is the wrong axis. Held at 200 KB, the shapes below span a
// factor of about 1900 in time per byte, and what they span is handler calls per byte - not
// nesting depth, not markup validity, not how much of the document matches. Measured on an M3 Pro,
// fastest of seven passes, with a handler set of two element selectors and a document-level text
// and comment handler:
//
//	shape                            ns/byte   alloc B/KB    calls
//	implied end tags (li)          103.565     19,667.8  120,000
//	malformed: unclosed tags        50.974      6,561.0   40,000
//	attribute-heavy anchors         42.233      3,284.0   17,646
//	many text nodes                 41.267      7,290.0   44,444
//	tables                          35.122      5,180.9   31,578
//	many siblings                   29.497      2,986.3   18,181
//	deep nesting                    25.376      2,986.9   18,183
//	many comments                   16.926      2,055.7   25,000
//	bogus comments                  13.478      1,646.1   20,000
//	malformed: stray end tags        3.144          8.5        0
//	one element, many attributes     1.601          8.6        1
//	one long comment                 0.481          7.7        1
//	one long text node               0.076          8.8        2
//	raw text (script)                0.067          8.8        2
//	entities                         0.065          8.0        2
//	one long attribute value         0.055          8.6        1
//
// Three things worth taking from that.
//
// The worst shape is a list of <li> with no closing tags, and it is worst because it produces
// three handler calls per element: the element itself, its text, and the empty final chunk that
// ends the text node. Forty thousand list items in 200 KB is 120,000 calls. It is not a
// pathological document - it is what a navigation menu looks like.
//
// A document of stray end tags is nearly free, and that is B194 showing up as a cost: no handler
// ever sees a stray end tag, so 200 KB of them produce zero calls and allocate 8.5 bytes per KB,
// which is the floor. The same floor holds for one long text node, one long comment, a script, and
// one element with a 200 KB attribute value - all of them one or two calls.
//
// So a rewrite's cost is set by how finely the document is divided, and a handler set that looks
// cheap on prose can be twenty times more expensive on markup of the same size. The allocation
// column is the same story and does not depend on the machine: 19.7 KB allocated per KB of input
// at the top, about 8.5 bytes per KB at the bottom.
//
// The ranking is a property of the handler set, not of the library, which is why this is a harness
// rather than a table: point it at your own handlers.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Shape builds a document of about size bytes.
type Shape struct {
	Name  string
	Build func(size int) string
}

// Shapes is the catalogue. Each one divides a document differently at the same byte count, which
// is the axis that matters.
var Shapes = []Shape{
	{"implied end tags (li)", func(n int) string {
		return "<ul>" + strings.Repeat("<li>a", n/5) + "</ul>"
	}},
	{"malformed: unclosed tags", func(n int) string { return strings.Repeat("<div>", n/5) }},
	{"attribute-heavy anchors", func(n int) string {
		return strings.Repeat(`<a href="/x" class="c" id="i">t</a>`, n/34)
	}},
	{"many text nodes", func(n int) string { return strings.Repeat("<p>ab</p>", n/9) }},
	{"tables", func(n int) string {
		return "<table>" + strings.Repeat("<tr><td>a</td></tr>", n/19) + "</table>"
	}},
	{"many siblings", func(n int) string { return strings.Repeat("<div></div>", n/11) }},
	{"deep nesting", func(n int) string {
		d := n / 11
		return strings.Repeat("<div>", d) + "x" + strings.Repeat("</div>", d)
	}},
	{"many comments", func(n int) string { return strings.Repeat("<!--c-->", n/8) }},
	{"bogus comments", func(n int) string { return strings.Repeat("<?php x ?>", n/10) }},
	{"malformed: stray end tags", func(n int) string { return strings.Repeat("</div>", n/6) }},
	{"one element, many attributes", func(n int) string {
		var b strings.Builder
		b.WriteString("<div")
		for i := 0; b.Len() < n; i++ {
			fmt.Fprintf(&b, ` a%d="v"`, i)
		}
		b.WriteString("></div>")
		return b.String()
	}},
	{"one long comment", func(n int) string { return "<!--" + strings.Repeat("a", n) + "-->" }},
	{"one long text node", func(n int) string { return "<p>" + strings.Repeat("a", n) + "</p>" }},
	{"raw text (script)", func(n int) string { return "<script>" + strings.Repeat("a", n) + "</script>" }},
	{"entities", func(n int) string { return "<p>" + strings.Repeat("&amp;", n/5) + "</p>" }},
	{"one long attribute value", func(n int) string {
		return `<div title="` + strings.Repeat("a", n) + `"></div>`
	}},
}

// A Result is one shape's cost.
type Result struct {
	Shape       string
	Bytes       int
	Calls       int
	Nanoseconds int64
	AllocBytes  uint64
}

// NsPerByte is the ranking metric, and the one that depends on the machine.
func (r Result) NsPerByte() float64 { return float64(r.Nanoseconds) / float64(r.Bytes) }

// AllocPerByte is the same story without the clock in it.
func (r Result) AllocPerByte() float64 { return float64(r.AllocBytes) / float64(r.Bytes) }

// CallsPerByte is what the other two track.
func (r Result) CallsPerByte() float64 { return float64(r.Calls) / float64(r.Bytes) }

// Handlers builds a handler set. It takes a counter so the harness can report calls per byte,
// which is the number the cost turns out to follow.
type Handlers func(calls *int) []lolhtml.Option

// Rank runs handlers over every shape at the given size and returns the results, most expensive
// first. Each shape is run repeatedly and the fastest pass kept, which is the usual way to measure
// something whose slow runs are the machine's fault rather than the code's.
func Rank(handlers Handlers, size, passes int) ([]Result, error) {
	if passes < 1 {
		passes = 7
	}
	results := make([]Result, 0, len(Shapes))
	for _, shape := range Shapes {
		doc := []byte(shape.Build(size))

		// One warm pass, so the first measured one is not paying for lazy setup.
		var warm int
		if _, err := lolhtml.Rewrite(doc, handlers(&warm)...); err != nil {
			return nil, fmt.Errorf("%s: %w", shape.Name, err)
		}

		best := Result{Shape: shape.Name, Bytes: len(doc), Nanoseconds: -1}
		for i := 0; i < passes; i++ {
			var calls int
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			start := time.Now()
			w, err := lolhtml.NewWriter(io.Discard, handlers(&calls)...)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", shape.Name, err)
			}
			if _, err := w.Write(doc); err != nil {
				return nil, fmt.Errorf("%s: %w", shape.Name, err)
			}
			if err := w.Close(); err != nil {
				return nil, fmt.Errorf("%s: %w", shape.Name, err)
			}
			elapsed := time.Since(start).Nanoseconds()
			runtime.ReadMemStats(&m1)
			if best.Nanoseconds < 0 || elapsed < best.Nanoseconds {
				best.Nanoseconds = elapsed
				best.AllocBytes = m1.TotalAlloc - m0.TotalAlloc
				best.Calls = calls
			}
		}
		results = append(results, best)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].NsPerByte() > results[j].NsPerByte()
	})
	return results, nil
}

// DefaultHandlers is a realistic pass: two element selectors and the document-level text and
// comment handlers, which is roughly what a sanitiser or a link rewriter registers.
func DefaultHandlers(calls *int) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			*calls++
			return e.SetAttribute("rel", "noopener")
		}),
		lolhtml.OnElement("div, li, td", func(e *lolhtml.Element) error {
			*calls++
			_, _ = e.Attribute("class")
			return nil
		}),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { *calls++; return nil }),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { *calls++; return nil }),
	}
}

func main() {
	size := 200000
	if len(os.Args) > 1 {
		if _, err := fmt.Sscan(os.Args[1], &size); err != nil {
			fmt.Fprintln(os.Stderr, "worstshape: size must be a number")
			os.Exit(1)
		}
	}
	results, err := Rank(DefaultHandlers, size, 7)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worstshape:", err)
		os.Exit(1)
	}
	fmt.Printf("%-30s %10s %13s %10s %8s\n", "shape", "ns/byte", "alloc B/KB", "calls", "bytes")
	for _, r := range results {
		fmt.Printf("%-30s %10.3f %13.1f %10d %8d\n",
			r.Shape, r.NsPerByte(), r.AllocPerByte()*1024, r.Calls, r.Bytes)
	}
	if len(results) > 1 {
		worst, best := results[0], results[len(results)-1]
		fmt.Printf("\n%s costs %.0fx per byte what %s does, on %.0fx the handler calls\n",
			worst.Shape, worst.NsPerByte()/best.NsPerByte(), best.Shape,
			worst.CallsPerByte()/max(best.CallsPerByte(), 1e-9))
	}
}

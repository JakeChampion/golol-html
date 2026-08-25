// Command bytewise measures what a write size costs.
//
//	$ bytewise -shape ordinary -size 65520
//	65541 bytes
//	write size     allocations   per byte         time    ns/byte  against whole
//	1                     3143      0.048   7.495862ms      114.4           7.6x
//	64                    3143      0.048   1.145043ms       17.5           1.2x
//	256                   3143      0.048   1.042116ms       15.9           1.1x
//	4096                  3143      0.048   1.004781ms       15.3           1.0x
//	whole                 3143      0.048    989.185µs       15.1           1.0x
//
// Two things are worth reading off that table. The allocation column does not move: a
// rewrite allocates what the document costs, and feeding it in smaller pieces adds nothing,
// because the write path allocates nothing of its own. The time column moves by a factor of
// about eight, and all of that is the crossing into C - roughly 100 ns per write on this
// machine, whatever the write contains. So the number of writes is what a caller pays for.
//
// The allocation numbers are the ones to rely on: they are counted, not sampled, and the
// same on any machine. The timings are this machine's, and are printed for the ratio.
//
// # Where the cost goes
//
// Each write is a crossing: the rewriter takes the bytes, tokenises what it can, and hands
// back what it produced. A one-byte write pays that crossing for one byte of progress, and
// pays it again for the next. Nothing rescans, so nothing is quadratic - which is worth
// saying because it is easy to assume otherwise of a parser that has to hold an unclosed
// tag, and because this repository documented the opposite for several releases. Point the
// program at the shapes that were supposed to be pathological with -shape: an unclosed tag,
// an unclosed comment, an unclosed quoted value, a raw-text element that never ends, one
// enormous text node. All of them are linear, and all of them are cheaper than ordinary
// markup at any write size, because a construct the rewriter is still holding produces
// nothing to hand back.
//
//	$ bytewise -shape unclosed-tag -size 4096 -check
//	...
//	one-byte writes: 0.005 allocations per byte at 4101 bytes, 0.001 at 16404 - linear
//
// -check is the assertion, not the table: it re-measures a document four times the size and
// exits non-zero if the cost per byte grew.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Shapes are the documents -shape can generate, each a way of making the rewriter hold
// something across writes.
var Shapes = map[string]func(int) string{
	"ordinary":         func(n int) string { return strings.Repeat(`<p class="a">text</p>`, n/21+1) },
	"unclosed-tag":     func(n int) string { return "<div " + strings.Repeat("a", n) },
	"many-attributes":  func(n int) string { return "<div " + strings.Repeat(`a="b" `, n/6) },
	"unclosed-comment": func(n int) string { return "<!--" + strings.Repeat("x", n) },
	"unclosed-value":   func(n int) string { return `<div a="` + strings.Repeat("x", n) },
	"unclosed-script":  func(n int) string { return "<script>" + strings.Repeat("x", n) },
	"one-text-node":    func(n int) string { return "<p>" + strings.Repeat("x", n) + "</p>" },
}

// Measurement is one write size over one document.
type Measurement struct {
	Chunk    int
	Bytes    int
	Runs     int
	Allocs   float64
	Duration time.Duration
}

// PerByte is the allocation count divided by the document length, which is the figure that
// does not depend on the machine.
func (m Measurement) PerByte() float64 {
	if m.Bytes == 0 {
		return 0
	}
	return m.Allocs / float64(m.Bytes)
}

// NsPerByte is this machine's time per byte.
func (m Measurement) NsPerByte() float64 {
	if m.Bytes == 0 {
		return 0
	}
	return float64(m.Duration.Nanoseconds()) / float64(m.Bytes)
}

// rewrite runs one whole rewrite of doc, chunk bytes at a time. A chunk of zero means one
// write of everything.
//
// Close can legitimately fail here: several of the shapes end inside a construct and strict
// mode reports that. The cost is what is being measured, so the error is not the subject.
func rewrite(doc []byte, chunk int) error {
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		return err
	}
	step := chunk
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, err := w.Write(doc[i:min(i+step, len(doc))]); err != nil {
			_ = w.Close()
			return err
		}
	}
	_ = w.Close()
	return nil
}

// measure runs the rewrite at one write size, repeated runs times.
//
// Allocations are counted from the runtime's own totals rather than sampled, so the figure
// is exact for the work done between the two reads: the sole approximation is that a
// concurrent goroutine allocating at the same time would be counted too, and this program
// has none.
func measure(doc []byte, chunk, runs int) (Measurement, error) {
	if runs < 1 {
		runs = 1
	}
	// One rewrite outside the measurement, so first-call initialisation is not attributed
	// to the run.
	if err := rewrite(doc, chunk); err != nil {
		return Measurement{}, err
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i := 0; i < runs; i++ {
		if err := rewrite(doc, chunk); err != nil {
			return Measurement{}, err
		}
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	return Measurement{
		Chunk:    chunk,
		Bytes:    len(doc),
		Runs:     runs,
		Allocs:   float64(after.Mallocs-before.Mallocs) / float64(runs),
		Duration: elapsed / time.Duration(runs),
	}, nil
}

// Curve is the measurements for one document at several write sizes.
type Curve struct {
	Bytes        int
	Measurements []Measurement
}

// Measure runs every write size over one document, repeating each enough times to get a
// timing worth printing without making the whole run slow.
func Measure(doc []byte, chunks []int, runs int) (Curve, error) {
	c := Curve{Bytes: len(doc)}
	for _, chunk := range chunks {
		m, err := measure(doc, chunk, runs)
		if err != nil {
			return c, err
		}
		c.Measurements = append(c.Measurements, m)
	}
	return c, nil
}

// Baseline is the measurement for the largest write size, which everything else is printed
// against.
func (c Curve) Baseline() Measurement {
	var out Measurement
	for _, m := range c.Measurements {
		if m.Chunk == 0 || m.Chunk > out.Chunk {
			out = m
		}
		if m.Chunk == 0 {
			break
		}
	}
	return out
}

func (c Curve) String() string {
	base := c.Baseline()
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes\n", c.Bytes)
	fmt.Fprintf(&b, "%-12s %13s %10s %12s %10s %14s\n",
		"write size", "allocations", "per byte", "time", "ns/byte", "against "+label(base.Chunk))
	for _, m := range c.Measurements {
		ratio := 0.0
		if base.Duration > 0 {
			ratio = float64(m.Duration) / float64(base.Duration)
		}
		fmt.Fprintf(&b, "%-12s %13.0f %10.3f %12s %10.1f %13.1fx\n",
			label(m.Chunk), m.Allocs, m.PerByte(), m.Duration, m.NsPerByte(), ratio)
	}
	return b.String()
}

func label(chunk int) string {
	if chunk == 0 {
		return "whole"
	}
	return fmt.Sprint(chunk)
}

// Linear reports whether the cost per byte held between two documents of different sizes at
// the same write size, which is what says the cost is linear rather than quadratic. The
// comparison is in allocations, which are deterministic; a timing comparison on a shared
// machine would report the machine's load.
func Linear(small, large Curve, chunk int) (bool, float64, float64) {
	find := func(c Curve) (Measurement, bool) {
		for _, m := range c.Measurements {
			if m.Chunk == chunk {
				return m, true
			}
		}
		return Measurement{}, false
	}
	a, okA := find(small)
	b, okB := find(large)
	if !okA || !okB {
		return false, 0, 0
	}
	// A quadratic cost would show as a per-byte figure that grows with the document; the
	// tolerance is for the fixed cost of a rewrite, which the smaller document divides by
	// fewer bytes.
	return b.PerByte() <= a.PerByte()*1.5, a.PerByte(), b.PerByte()
}

// grow repeats a document, which keeps its shape while multiplying its size.
func grow(doc []byte, times int) []byte {
	out := make([]byte, 0, len(doc)*times)
	for i := 0; i < times; i++ {
		out = append(out, doc...)
	}
	return out
}

func main() {
	shape := flag.String("shape", "", "generate a document instead of reading stdin: "+shapeNames())
	size := flag.Int("size", 16<<10, "size of the generated document, in bytes")
	runs := flag.Int("runs", 20, "how many times to repeat each measurement")
	check := flag.Bool("check", false, "also measure a document four times the size, and say whether the cost per byte held")
	flag.Parse()

	doc, err := document(*shape, *size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bytewise:", err)
		os.Exit(2)
	}

	chunks := []int{1, 64, 256, 4096, 0}
	curve, err := Measure(doc, chunks, *runs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bytewise:", err)
		os.Exit(1)
	}
	fmt.Print(curve)

	if !*check {
		return
	}

	bigger, err := Measure(grow(doc, 4), chunks, *runs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bytewise:", err)
		os.Exit(1)
	}
	fmt.Printf("\nfour times the document:\n")
	fmt.Print(bigger)

	linear, a, b := Linear(curve, bigger, 1)
	fmt.Printf("\none-byte writes: %.3f allocations per byte at %d bytes, %.3f at %d - %s\n",
		a, curve.Bytes, b, bigger.Bytes, verdict(linear))
	if !linear {
		os.Exit(1)
	}
}

func document(shape string, size int) ([]byte, error) {
	if shape != "" {
		gen, ok := Shapes[shape]
		if !ok {
			return nil, fmt.Errorf("no shape %q; have %s", shape, shapeNames())
		}
		if size < 1 {
			return nil, fmt.Errorf("a document of %d bytes is not something to measure", size)
		}
		return []byte(gen(size)), nil
	}
	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("nothing on stdin to measure; -shape generates a document instead")
	}
	return doc, nil
}

func verdict(linear bool) string {
	if linear {
		return "linear"
	}
	return "NOT linear: the per-byte cost grew with the document"
}

func shapeNames() string {
	names := make([]string, 0, len(Shapes))
	for name := range Shapes {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return strings.Join(names, ", ")
}

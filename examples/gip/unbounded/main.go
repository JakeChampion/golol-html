// Command unbounded rewrites a document larger than any buffer worth holding, and says which
// handler patterns keep the memory flat.
//
//	$ unbounded -size 64
//	64 MB, fed in 4096-byte writes, three times the size for the growth column
//
//	pattern                        peak heap   at 3x    growth  bounded
//	no handlers                       1.15 MB  1.15 MB    1.00x  yes
//	element handler                   3.72 MB  3.72 MB    1.00x  yes
//	element + SetAttribute            3.71 MB  3.71 MB    1.00x  yes
//	text, per chunk                   3.73 MB  3.73 MB    1.00x  yes
//	element + SetUserData(nil)        3.72 MB  3.72 MB    1.00x  yes
//	element + OnEndTag              665.67 MB  1.95 GB    3.00x  NO
//	element + SetUserData           523.95 MB  1.53 GB    2.99x  NO
//
// The document never exists in memory: it is generated as it is written and the output goes
// to io.Discard, so what the peak measures is the rewriter and the handler, not the caller's
// buffers.
//
// # The rule
//
// A rewrite is bounded unless a handler asks the library to remember something per unit.
// Reading is free, editing is free, and two calls are not: [Element.OnEndTag] registers a
// callback that lives until the rewrite ends rather than until the end tag arrives, and
// SetUserData attaches a value with the same lifetime. Both cost a cgo handle per unit, both
// are invisible in the output, and neither is bounded by MemorySettings.MaxMemory - that
// limit is lol-html's parse buffer, and these live in the binding's handle table.
//
// Per *unit* is worth reading carefully for text: a chunk is the unit, and how many chunks a
// text node arrives in depends on the caller's write sizes. One 2 KB text node written whole
// is two chunks; the same node written a byte at a time is two thousand. So user data on text
// chunks is the one cost in this library that scales with how the document was fed rather
// than with what it says.
//
// # The mitigation
//
// SetUserData(nil) releases the handle immediately, so a handler that needs to pass a value
// to a later handler on the same unit can hand it over and clear it, and the rewrite stays
// bounded. The -pattern flag runs one pattern on its own if you want to see that in
// isolation. ClearEndTagHandlers is not the equivalent for OnEndTag: it stops the callback
// running and keeps the handle, so the only way to bound that one is to register it on fewer
// elements.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Pattern is a way of writing a handler, and whether it is expected to hold memory.
type Pattern struct {
	Name string
	// Bounded is what this program checks rather than assumes: it is the claim, and the
	// growth column is the measurement.
	Bounded bool
	Options func() []lolhtml.Option
}

// Patterns are ordered so the bounded ones come first, since that is the answer a reader
// wants and the list of exceptions is the short one.
var Patterns = []Pattern{
	{"no handlers", true, func() []lolhtml.Option { return nil }},
	{"element handler", true, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil })}
	}},
	{"element + SetAttribute", true, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "nofollow")
		})}
	}},
	{"text, per chunk", true, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			_ = t.Text()
			return nil
		})}
	}},
	{"element + SetUserData(nil)", true, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			if err := e.SetUserData("x"); err != nil {
				return err
			}
			return e.SetUserData(nil)
		})}
	}},
	{"element + OnEndTag", false, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		})}
	}},
	{"element + OnEndTag, cleared", false, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			if err := e.OnEndTag(func(*lolhtml.EndTag) error { return nil }); err != nil {
				return err
			}
			// Clearing stops the callback and keeps the handle, which is the point of
			// including this row.
			e.ClearEndTagHandlers()
			return nil
		})}
	}},
	{"element + SetUserData", false, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetUserData("x")
		})}
	}},
	{"text + SetUserData", false, func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			return t.SetUserData("x")
		})}
	}},
}

// unit is the document this program generates, repeated. It has an element with an
// attribute, an end tag and a text node, so every pattern above has something to act on.
const unit = `<a href="/x">t</a> `

// Result is one pattern measured at one size.
type Result struct {
	Pattern  string
	Bytes    int
	PeakHeap uint64
}

// PerMB is the peak heap divided by the document size, which is the figure that should be
// falling as the document grows if the pattern is bounded.
func (r Result) PerMB() float64 {
	if r.Bytes == 0 {
		return 0
	}
	return float64(r.PeakHeap) / (float64(r.Bytes) / (1 << 20))
}

// Rewrite feeds a generated document of about size bytes through one pattern, in writes of
// writeSize, and reports the highest heap seen while it ran, over what was already there.
//
// The figure is a delta from a post-collection baseline, not an absolute: HeapAlloc counts
// the whole process, so the absolute number says as much about whatever else is running as
// about the rewrite. The delta is what the rewrite added.
//
// The heap is sampled rather than integrated: sampling every write is too expensive to be
// worth it, so it is sampled every 64. A pattern that holds memory holds it for the whole
// rewrite, so the high-water mark is what matters and a coarse sample finds it.
func Rewrite(p Pattern, size, writeSize int) (Result, error) {
	res := Result{Pattern: p.Name}

	var base runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&base)

	w, err := lolhtml.NewWriter(io.Discard, p.Options()...)
	if err != nil {
		return res, err
	}

	chunk := strings.Repeat(unit, writeSize/len(unit)+1)[:writeSize]
	var m runtime.MemStats
	for written, writes := 0, 0; written < size; writes++ {
		n := min(writeSize, size-written)
		if _, err := w.Write([]byte(chunk[:n])); err != nil {
			w.Close()
			return res, err
		}
		written += n
		res.Bytes = written
		if writes%64 == 0 {
			runtime.ReadMemStats(&m)
			res.PeakHeap = max(res.PeakHeap, added(m.HeapAlloc, base.HeapAlloc))
		}
	}
	runtime.ReadMemStats(&m)
	res.PeakHeap = max(res.PeakHeap, added(m.HeapAlloc, base.HeapAlloc))
	if err := w.Close(); err != nil {
		return res, err
	}
	return res, nil
}

// added is the heap above the baseline, floored at zero: a collection during the rewrite can
// leave the current figure below where it started, and a negative peak is not a measurement.
func added(now, base uint64) uint64 {
	if now < base {
		return 0
	}
	return now - base
}

// Growth is one pattern measured at two sizes, which is what says whether it is bounded.
type Growth struct {
	Pattern  string
	Bounded  bool
	Small    Result
	Large    Result
	Multiple int
}

// Ratio is how much the peak grew when the document grew by Multiple. A bounded pattern
// stays near one however large the multiple is.
func (g Growth) Ratio() float64 {
	if g.Small.PeakHeap == 0 {
		return 0
	}
	return float64(g.Large.PeakHeap) / float64(g.Small.PeakHeap)
}

// visible is the smallest peak worth drawing a conclusion from. Below it there is nothing to
// see: a bounded rewrite's working set is a couple of megabytes whatever the document, and the
// ratio between two small numbers is the sampler's noise rather than a trend. An unbounded
// pattern is far above this - a handle per element is tens of megabytes by the time a document
// is a megabyte of anchors.
const visible = 8 << 20

// Held reports whether the peak grew with the document. Two conditions, because either alone
// misreads: the peak has to be large enough to mean something, and it has to have grown with
// the document. The ratio threshold is halfway between the two answers this measurement gives
// - a bounded pattern measures 1.0 and an unbounded one measures the multiple - so it does
// not need to be precise to be right.
func (g Growth) Held() bool {
	return g.Large.PeakHeap > visible && g.Ratio() > 1+float64(g.Multiple-1)/2
}

// Measure runs every pattern at both sizes.
func Measure(patterns []Pattern, size, writeSize, multiple int) ([]Growth, error) {
	var out []Growth
	for _, p := range patterns {
		small, err := Rewrite(p, size, writeSize)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		large, err := Rewrite(p, size*multiple, writeSize)
		if err != nil {
			return nil, fmt.Errorf("%s at %dx: %w", p.Name, multiple, err)
		}
		out = append(out, Growth{p.Name, p.Bounded, small, large, multiple})
	}
	return out, nil
}

// Disagreements returns the patterns whose measurement contradicted the claim in the table.
// This is the program's assertion: it is not here to print numbers but to check them.
func Disagreements(gs []Growth) []string {
	var out []string
	for _, g := range gs {
		if g.Held() == g.Bounded {
			out = append(out, fmt.Sprintf("%s is listed as %s and measured %s (%.2fx over %dx the document)",
				g.Pattern, claim(g.Bounded), claim(!g.Held()), g.Ratio(), g.Multiple))
		}
	}
	sort.Strings(out)
	return out
}

func claim(bounded bool) string {
	if bounded {
		return "bounded"
	}
	return "unbounded"
}

func report(gs []Growth, size, writeSize, multiple int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, fed in %d-byte writes, %dx the size for the growth column\n\n",
		human(uint64(size)), writeSize, multiple)
	fmt.Fprintf(&b, "%-30s %10s %10s %8s  %s\n", "pattern", "peak heap", "at "+fmt.Sprint(multiple)+"x", "growth", "bounded")
	for _, g := range gs {
		bounded := "yes"
		if g.Held() {
			bounded = "NO"
		}
		fmt.Fprintf(&b, "%-30s %10s %10s %7.2fx  %s\n", g.Pattern,
			human(g.Small.PeakHeap), human(g.Large.PeakHeap), g.Ratio(), bounded)
	}
	if d := Disagreements(gs); len(d) > 0 {
		b.WriteString("\n")
		for _, s := range d {
			fmt.Fprintf(&b, "%s\n", s)
		}
	}
	return b.String()
}

func human(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

func main() {
	size := flag.Int("size", 16, "document size in MB")
	writeSize := flag.Int("write", 4096, "write size in bytes")
	multiple := flag.Int("multiple", 3, "how much larger the second measurement is")
	pattern := flag.String("pattern", "", "measure only the pattern whose name contains this")
	flag.Parse()

	if *size < 1 || *writeSize < 1 || *multiple < 2 {
		fmt.Fprintln(os.Stderr, "unbounded: a size and write size of at least one, and a multiple of at least two")
		os.Exit(2)
	}

	patterns := Patterns
	if *pattern != "" {
		patterns = nil
		for _, p := range Patterns {
			if strings.Contains(p.Name, *pattern) {
				patterns = append(patterns, p)
			}
		}
		if len(patterns) == 0 {
			fmt.Fprintf(os.Stderr, "unbounded: no pattern matching %q\n", *pattern)
			os.Exit(2)
		}
	}

	gs, err := Measure(patterns, *size<<20, *writeSize, *multiple)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unbounded:", err)
		os.Exit(1)
	}
	fmt.Print(report(gs, *size<<20, *writeSize, *multiple))

	if len(Disagreements(gs)) > 0 {
		os.Exit(1)
	}
}

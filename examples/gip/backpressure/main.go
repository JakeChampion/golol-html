// Command backpressure measures what a rewrite costs a slow destination.
//
//	$ backpressure -latency 100us
//	6200 bytes, 200 anchors, destination latency 100µs per write
//
//	rewrite                     writes  per match  median  unbuffered  buffered  writes buffered
//	passthrough                      1        0.0    6200     100µs     101µs                1
//	a handler that does nothing    400        2.0       8      40ms     201µs                2
//	reading an attribute           400        2.0       8      40ms     202µs                2
//	an end-tag handler             600        3.0       4      60ms     203µs                2
//	setting an attribute          2600       13.0       1     260ms     303µs                3
//	removing an attribute         1200        6.0       1     120ms     202µs                2
//	inserting before              600        3.0       8      60ms     205µs                2
//
// Nothing here is buffered by the library on purpose - output goes to the destination as it
// becomes available, so a caller streaming to a client sees the first bytes early. The cost
// of that choice is that the destination's per-write cost is paid per write, and the number of
// writes is not the one a reader would guess from the document's size.
//
// # What decides the number of writes
//
// Matching, not editing. A handler that does nothing at all still splits the output around
// every element it matched: 200 anchors become 400 writes of a median 8 bytes, where the same
// document with no handlers is one write of 6200. A selector that matches nothing costs
// nothing, and neither does a comment handler on a document with no comments - so it is the
// matches that count, not the registrations.
//
// Editing multiplies it again, because a mutated start tag is re-serialised piece by piece:
// setting one attribute on each of 200 anchors turns one write into 2600, of median size one
// byte.
//
// That matters for a read-only instrumentation pass, which is the case where nobody expects a
// cost: adding a counter to a rewrite that streams to an unbuffered net.Conn turns one write
// per document into two per element.
//
// # The fix, and what it costs
//
// A bufio.Writer collapses all of it to the number of buffer-fulls: the same rewrites become
// two or three writes. The cost is latency to the first byte, since a buffer is a promise not
// to write yet - which is the tradeoff the library declines to make for the caller, because
// only the caller knows whether the client is a browser waiting for a page or a file.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Unit is the anchor this program repeats to make its page.
const Unit = `<a href="/x" class="c">link</a>`

// SlowSink counts writes and sleeps for Latency on each, which is what a destination with a
// per-write cost looks like - a syscall, a network hop, a lock.
type SlowSink struct {
	Latency time.Duration

	Calls int
	Bytes int
	Sizes []int
}

func (s *SlowSink) Write(p []byte) (int, error) {
	s.Calls++
	s.Bytes += len(p)
	s.Sizes = append(s.Sizes, len(p))
	if s.Latency > 0 {
		time.Sleep(s.Latency)
	}
	return len(p), nil
}

// Median is the median write size, which says more than the mean when the distribution is
// one enormous write or thousands of single bytes.
func (s *SlowSink) Median() int {
	if len(s.Sizes) == 0 {
		return 0
	}
	sizes := make([]int, len(s.Sizes))
	copy(sizes, s.Sizes)
	sort.Ints(sizes)
	return sizes[len(sizes)/2]
}

// Rewrite is one way of writing a handler, named for what it does rather than for what it
// costs, since the cost is what the program is for.
type Rewrite struct {
	Name    string
	Options func() []lolhtml.Option
}

// Rewrites are ordered from cheapest to most expensive, which is also from least to most
// useful - that being the tradeoff.
var Rewrites = []Rewrite{
	{"passthrough", func() []lolhtml.Option { return nil }},
	{"a selector matching nothing", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("span.nope", func(*lolhtml.Element) error { return nil })}
	}},
	{"a handler that does nothing", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil })}
	}},
	{"reading an attribute", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("href")
			return nil
		})}
	}},
	{"an end-tag handler", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		})}
	}},
	{"inserting before", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.Before("<!--b-->", lolhtml.HTML)
		})}
	}},
	{"removing an attribute", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.RemoveAttribute("class")
		})}
	}},
	{"setting an attribute", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "nofollow")
		})}
	}},
}

// Result is one rewrite measured with and without a buffer in front of the destination.
type Result struct {
	Name string

	Matches int

	Writes   int
	Median   int
	Bytes    int
	Duration time.Duration

	BufferedWrites   int
	BufferedDuration time.Duration
}

// PerMatch is the number of destination writes each matched element cost, which is the figure
// that does not depend on the size of the page.
func (r Result) PerMatch() float64 {
	if r.Matches == 0 {
		return 0
	}
	return float64(r.Writes) / float64(r.Matches)
}

// Amplification is how much the buffer saved, in writes.
func (r Result) Amplification() float64 {
	if r.BufferedWrites == 0 {
		return 0
	}
	return float64(r.Writes) / float64(r.BufferedWrites)
}

// run feeds doc through one rewrite, to dst, and returns how long it took.
func run(doc string, dst io.Writer, opts []lolhtml.Option, flush func() error) (time.Duration, error) {
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := w.Write([]byte(doc)); err != nil {
		w.Close()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	if flush != nil {
		if err := flush(); err != nil {
			return 0, err
		}
	}
	return time.Since(start), nil
}

// Measure runs one rewrite twice: straight to a slow destination, and through a buffer.
func Measure(r Rewrite, doc string, latency time.Duration, bufSize int) (Result, error) {
	res := Result{Name: r.Name, Matches: strings.Count(doc, "<a ")}

	direct := &SlowSink{Latency: latency}
	d, err := run(doc, direct, r.Options(), nil)
	if err != nil {
		return res, err
	}
	res.Writes, res.Median, res.Bytes, res.Duration = direct.Calls, direct.Median(), direct.Bytes, d

	buffered := &SlowSink{Latency: latency}
	bw := bufio.NewWriterSize(buffered, bufSize)
	bd, err := run(doc, bw, r.Options(), bw.Flush)
	if err != nil {
		return res, err
	}
	res.BufferedWrites, res.BufferedDuration = buffered.Calls, bd

	return res, nil
}

func report(results []Result, doc string, latency time.Duration, bufSize int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes, %d anchors, destination latency %v per write, buffer %d bytes\n\n",
		len(doc), strings.Count(doc, "<a "), latency, bufSize)
	fmt.Fprintf(&b, "%-28s %8s %10s %8s %11s %10s %8s\n",
		"rewrite", "writes", "per match", "median", "unbuffered", "buffered", "saved")
	for _, r := range results {
		fmt.Fprintf(&b, "%-28s %8d %10.1f %8d %11s %10s %7.0fx\n",
			r.Name, r.Writes, r.PerMatch(), r.Median,
			round(r.Duration), round(r.BufferedDuration), r.Amplification())
	}
	return b.String()
}

// round trims a duration to something readable: the interesting part is the order of
// magnitude, not the nanoseconds.
func round(d time.Duration) time.Duration {
	switch {
	case d > time.Second:
		return d.Round(10 * time.Millisecond)
	case d > time.Millisecond:
		return d.Round(100 * time.Microsecond)
	}
	return d.Round(time.Microsecond)
}

func main() {
	latency := flag.Duration("latency", 100*time.Microsecond, "how long each destination write takes")
	anchors := flag.Int("anchors", 200, "how many anchors the page has")
	bufSize := flag.Int("buffer", 4096, "size of the bufio.Writer to compare against")
	flag.Parse()

	if *anchors < 1 || *bufSize < 1 {
		fmt.Fprintln(os.Stderr, "backpressure: -anchors and -buffer are counts, not zero")
		os.Exit(2)
	}

	doc := strings.Repeat(Unit, *anchors)
	var results []Result
	for _, r := range Rewrites {
		res, err := Measure(r, doc, *latency, *bufSize)
		if err != nil {
			fmt.Fprintln(os.Stderr, "backpressure:", err)
			os.Exit(1)
		}
		results = append(results, res)
	}
	fmt.Print(report(results, doc, *latency, *bufSize))
}

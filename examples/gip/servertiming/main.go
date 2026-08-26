// Command servertiming times a rewrite and writes what it measured into the document it
// rewrote, as a Server-Timing comment at the end.
//
//	$ servertiming -per-handler page.html
//	<!-- Server-Timing: rewrite;dur=1.031, handlers;dur=0.412;desc="2001 calls" -->
//
//	$ servertiming -report page.html
//	49806 bytes, 2001 handler calls
//	  rewrite            1.031ms, which is 25146 clock ticks
//	  in handlers        412.3µs, 206ns per call
//	  clock tick         41ns
//	  the comment        appended at the document end, where the duration is known
//
// # The duration is only knowable at the end, so the comment can only go there
//
// A Server-Timing header would be better and it is not available: a header is sent before the
// body, and how long the body took to rewrite is known after it. An HTTP trailer is the protocol's
// answer and few clients read one. So this writes a comment, and a comment can only go where the
// rewriter has not been yet - which, for a value that is not known until the end, is the end.
// [lolhtml.DocumentEnd] is that position.
//
// The duration it reports therefore excludes writing the comment and closing the writer, which is
// unavoidable: a measurement cannot include the act of recording it.
//
// # What timing it costs
//
// Two clock reads per handler call, against a handler call that is a crossing into C. Measured on
// an M3 Pro, fastest of fifty runs, rewriting a page of paragraphs with one matching anchor each:
//
//	page              plain      one clock read   a read per handler call
//	486 bytes         12.9µs     12.8µs           13.1µs
//	4806 bytes         105µs     105.9µs          110.6µs
//	49806 bytes      1.030ms     1.017ms          1.032ms
//
// About six per cent of a handler call at the middle size, and inside the noise at the other two.
// Allocations do not change per call: two more for the whole rewrite, not two per handler. So
// instrumenting every handler is affordable, which is worth knowing because the alternative -
// timing the whole rewrite and dividing - cannot tell a slow selector from a slow document.
//
// # A duration below the clock tick is not a measurement
//
// The tick is a property of the platform, not of the library: 41ns on this machine, and coarser
// than a whole small rewrite on some. So the tick is measured, reported, and used - a rewrite that
// did not last a useful multiple of it gets a comment saying so rather than a number. The project
// learned this the hard way in examples/gip/queue, where a per-item figure read as exactly zero on
// the Windows runner.
//
// # The comment does not always survive
//
// An input that ends inside a construct swallows what is appended at the document end, and a
// comment payload has a failure mode of its own. Measured, appending a comment and separately an
// element to inputs that stop where they say:
//
//	input ends inside      the comment       an element
//	nothing (complete)     survives          survives
//	a script or style      swallowed         swallowed
//	a textarea or title    swallowed         swallowed
//	a doctype              swallowed         swallowed
//	a start or end tag     swallowed         swallowed
//	a comment              *merges*          swallowed
//
// The last row is the one to watch: appending a comment to a document that ends inside a comment
// produces one comment, so counting comments says it worked. It did not - the timing text is
// inside the page's own unfinished comment, carrying whatever the page had written before it. So a
// caller checking that the marker arrived has to look at the comment's text and not at how many
// there are.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Measurement is what one rewrite recorded.
type Measurement struct {
	Bytes int
	// Rewrite is the whole rewrite, and Handlers the time spent inside handler callbacks,
	// which is zero unless PerHandler was asked for.
	Rewrite  time.Duration
	Handlers time.Duration
	Calls    int
	// Tick is this platform's clock resolution, measured rather than assumed.
	Tick time.Duration
	// PerHandler says whether the handler figure was collected.
	PerHandler bool
}

// Resolvable reports whether the rewrite lasted long enough for the clock to describe it. Twenty
// ticks is the same bar examples/gip/queue uses, for the same reason.
func (m Measurement) Resolvable() bool {
	return m.Tick > 0 && m.Rewrite > 20*m.Tick
}

// Ticks is how many clock ticks the rewrite lasted, which is the honest way to say whether the
// figure means anything.
func (m Measurement) Ticks() int {
	if m.Tick <= 0 {
		return 0
	}
	return int(m.Rewrite / m.Tick)
}

// PerCall is the average time inside a handler, or zero when nothing was collected.
func (m Measurement) PerCall() time.Duration {
	if m.Calls == 0 {
		return 0
	}
	return m.Handlers / time.Duration(m.Calls)
}

// Comment is the Server-Timing comment for this measurement, or a comment saying why there is no
// figure. It is a comment rather than a header because the value is not known until the body has
// been written.
func (m Measurement) Comment() string {
	if !m.Resolvable() {
		return fmt.Sprintf("<!-- Server-Timing: not measured, the rewrite lasted %v "+
			"against a clock tick of %v -->", m.Rewrite.Round(time.Nanosecond), m.Tick)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Server-Timing: rewrite;dur=%.3f", ms(m.Rewrite))
	if m.PerHandler {
		fmt.Fprintf(&b, `, handlers;dur=%.3f;desc="%d calls"`, ms(m.Handlers), m.Calls)
	}
	b.WriteString(" -->")
	return b.String()
}

// ms is a duration in milliseconds, which is the unit Server-Timing uses.
func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// clockTick measures the smallest interval this platform's clock reports, by reading it until the
// reading changes, and takes the smallest of several attempts.
func clockTick() time.Duration {
	best := time.Duration(0)
	for range 5 {
		start := time.Now()
		var d time.Duration
		for d == 0 {
			d = time.Since(start)
		}
		if best == 0 || d < best {
			best = d
		}
	}
	return best
}

// Rewrite copies src to dst through a rewriter that adds rel=noopener to external links, times
// itself, and appends what it measured.
//
// The work it does is deliberately ordinary: the point is the timing, and a rewrite that did
// nothing would measure the cost of the parser alone.
func Rewrite(src io.Reader, dst io.Writer, perHandler bool) (Measurement, error) {
	m := Measurement{Tick: clockTick(), PerHandler: perHandler}

	// The comment has to be built when the document ends, because that is when the duration
	// exists. The measurement is captured by the closure rather than returned, so the handler
	// reads whatever has been accumulated by then.
	start := time.Now()

	counting := &countingWriter{w: dst}
	w, err := lolhtml.NewWriter(counting,
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			if !perHandler {
				return annotate(e)
			}
			callStart := time.Now()
			err := annotate(e)
			m.Handlers += time.Since(callStart)
			m.Calls++
			return err
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			// Everything before this point is the rewrite; the append and the
			// close are not, and cannot be.
			m.Rewrite = time.Since(start)
			return d.Append(m.Comment(), lolhtml.HTML)
		}))
	if err != nil {
		return m, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return m, err
	}
	if err := w.Close(); err != nil {
		return m, err
	}
	m.Bytes = counting.n
	return m, nil
}

// annotate is the work being timed.
func annotate(e *lolhtml.Element) error {
	href, _ := e.Attribute("href")
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return e.SetAttribute("rel", "noopener")
	}
	return nil
}

// countingWriter counts the bytes that reached the destination, which is the figure a
// Server-Timing consumer wants beside a duration.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

func (m Measurement) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes", m.Bytes)
	if m.PerHandler {
		fmt.Fprintf(&b, ", %d handler calls", m.Calls)
	}
	b.WriteString("\n")
	if m.Resolvable() {
		fmt.Fprintf(&b, "  %-18s %v, which is %d clock ticks\n", "rewrite",
			m.Rewrite.Round(100*time.Nanosecond), m.Ticks())
	} else {
		fmt.Fprintf(&b, "  %-18s %v, which is %d clock ticks - too few to mean "+
			"anything\n", "rewrite", m.Rewrite.Round(time.Nanosecond), m.Ticks())
	}
	if m.PerHandler && m.Calls > 0 {
		fmt.Fprintf(&b, "  %-18s %v, %v per call\n", "in handlers",
			m.Handlers.Round(100*time.Nanosecond), m.PerCall().Round(time.Nanosecond))
	}
	fmt.Fprintf(&b, "  %-18s %v\n", "clock tick", m.Tick)
	fmt.Fprintf(&b, "  %-18s appended at the document end, where the duration is known\n",
		"the comment")
	return b.String()
}

func main() {
	perHandler := flag.Bool("per-handler", false, "time each handler call as well as the whole rewrite")
	report := flag.Bool("report", false, "print what was measured instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "servertiming:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	dst := io.Writer(os.Stdout)
	var held strings.Builder
	if *report {
		dst = &held
	}
	m, err := Rewrite(src, dst, *perHandler)
	if err != nil {
		fmt.Fprintln(os.Stderr, "servertiming:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(m)
	}
}

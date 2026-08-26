// Command tee streams a document to two destinations at once - one rewritten, one exactly as it
// arrived - from a single read of the input, and reports how far apart they ran.
//
//	$ tee -rewritten out.html -verbatim raw.html page.html
//	7400 bytes in, 10400 out rewritten, 7400 out verbatim
//	  widest gap        12 bytes, with the verbatim copy ahead
//	  held by           a start tag no selector had ruled out
//	  sink calls        4400 rewritten, 200 verbatim
//
// The verbatim copy is the input, so there is nothing to compute for it: tee the reader and give
// one branch to the rewriter. What is worth knowing is the gap, because it is the only view from
// outside of what the rewriter is holding.
//
// # A start tag is held until every selector has been ruled out
//
// The verbatim copy runs ahead by whatever the rewriter has buffered, and that is bounded by one
// start tag rather than by the document. Which tag depends on the selectors, and not on whether
// any handler ran. Feeding a 5513-byte document one byte at a time and watching the widest gap:
//
//	document                            selector             widest gap
//	<div data-x="1" … >x</div>          none                          5
//	                                    a[href]                       5
//	                                    span[data-x]                  5
//	                                    div[data-x]                5505
//	                                    div[data-absent]           5505
//	                                    div.absent                 5505
//	                                    div#absent                 5505
//	                                    [data-absent]              5505
//	                                    *                          5505
//
// A tag name rules a selector out at the name and the rest of the tag streams. An attribute, a
// class or an id cannot be ruled out until the tag ends, so the tag is held whether it matches or
// not - div[data-absent] holds as much as div[data-x] does. A selector with no tag-name component
// holds every tag.
//
// So the bound is the longest start tag whose name some selector does not exclude. Ordinary markup
// gives a gap of a few bytes; a tag with five hundred attributes gives five thousand, and only if
// a selector could still be interested in it. Text is never held: a 10 KB text node ran a gap of
// three bytes with a text handler registered and without one.
//
// The one way to make the gap the size of the document is to do it yourself - accumulating a text
// node to [lolhtml.TextChunk.IsLastInTextNode] and removing the chunks along the way held 10003 of
// 10007 bytes. That is the caller's buffer, not the rewriter's, and it is worth knowing which is
// which when a memory figure looks wrong.
//
// # The two destinations fail differently
//
// The verbatim copy is written first, so a rewrite that fails has already put bytes in it. There is
// no ordering that avoids this: whichever is written first is ahead when the other breaks. What a
// caller can do is know which, and this reports the counts so a partial pair is recognisable
// rather than surprising.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Split is what a run did.
type Split struct {
	In, Rewritten, Verbatim int64
	// RewrittenCalls and VerbatimCalls are how many times each destination was written to,
	// which is the other half of what a slow sink cares about.
	RewrittenCalls, VerbatimCalls int
	// WidestGap is the largest number of bytes the verbatim copy was ahead by, which is what
	// the rewriter was holding.
	WidestGap int64
	// Held names what the gap was waiting for, as far as it can be told from outside.
	Held string
}

func (s Split) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes in, %d out rewritten, %d out verbatim\n",
		s.In, s.Rewritten, s.Verbatim)
	fmt.Fprintf(&b, "  %-18s %d bytes, with the verbatim copy ahead\n", "widest gap", s.WidestGap)
	if s.Held != "" {
		fmt.Fprintf(&b, "  %-18s %s\n", "held by", s.Held)
	}
	fmt.Fprintf(&b, "  %-18s %d rewritten, %d verbatim\n", "sink calls",
		s.RewrittenCalls, s.VerbatimCalls)
	return b.String()
}

// counting wraps a destination and records what went through it.
type counting struct {
	w     io.Writer
	n     int64
	calls int
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.calls++
	return n, err
}

// Tee reads src once and writes it to verbatim exactly as it arrived and to rewritten with the
// handlers applied.
//
// chunk is how much to read at a time, which decides how finely the gap can be observed: the gap
// is a property of the rewriter, and a read of the whole document at once cannot show it.
func Tee(src io.Reader, rewritten, verbatim io.Writer, chunk int, opts ...lolhtml.Option) (Split, error) {
	if chunk <= 0 {
		chunk = 4096
	}
	var s Split
	rw := &counting{w: rewritten}
	vb := &counting{w: verbatim}

	w, err := lolhtml.NewWriter(rw, opts...)
	if err != nil {
		return s, err
	}

	buf := make([]byte, chunk)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			s.In += int64(n)
			// The verbatim copy first, because it cannot fail for a reason the
			// rewrite caused - and because something has to be first, which is the
			// asymmetry the package comment describes.
			if _, err := vb.Write(buf[:n]); err != nil {
				w.Close()
				return s.finish(rw, vb), err
			}
			if _, err := w.Write(buf[:n]); err != nil {
				w.Close()
				return s.finish(rw, vb), err
			}
			if gap := vb.n - rw.n; gap > s.WidestGap {
				s.WidestGap = gap
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			w.Close()
			return s.finish(rw, vb), readErr
		}
	}
	if err := w.Close(); err != nil {
		return s.finish(rw, vb), err
	}
	return s.finish(rw, vb), nil
}

// finish copies the counters in and describes the gap.
func (s Split) finish(rw, vb *counting) Split {
	s.Rewritten, s.RewrittenCalls = rw.n, rw.calls
	s.Verbatim, s.VerbatimCalls = vb.n, vb.calls
	switch {
	case s.WidestGap == 0:
		s.Held = "nothing: the reads were larger than anything the rewriter held"
	case s.WidestGap < 64:
		s.Held = "a token in progress, which is what ordinary markup costs"
	default:
		s.Held = "a start tag no selector had ruled out"
	}
	return s
}

func main() {
	rewrittenPath := flag.String("rewritten", "", "where the rewritten copy goes, or stdout")
	verbatimPath := flag.String("verbatim", "", "where the verbatim copy goes, or nowhere")
	chunk := flag.Int("chunk", 64, "how many bytes to read at a time")
	report := flag.Bool("report", true, "print the counts to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "tee:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	rewritten := io.Writer(os.Stdout)
	if *rewrittenPath != "" {
		f, err := os.Create(*rewrittenPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tee:", err)
			os.Exit(1)
		}
		defer f.Close()
		rewritten = f
	}
	verbatim := io.Writer(io.Discard)
	if *verbatimPath != "" {
		f, err := os.Create(*verbatimPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tee:", err)
			os.Exit(1)
		}
		defer f.Close()
		verbatim = f
	}

	s, err := Tee(src, rewritten, verbatim, *chunk, annotate())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tee:", err)
		fmt.Fprint(os.Stderr, s)
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, s)
	}
}

// annotate is the rewrite, kept simple because the subject is the plumbing.
func annotate() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})
}

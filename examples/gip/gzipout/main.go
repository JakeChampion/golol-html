// Command gzipout rewrites a document into a gzip writer and checks the round trip, because two
// Closers in a chain is a thing people get wrong in a way that costs bytes.
//
//	$ gzipout page.html
//	13806 bytes in, 16806 rewritten, 1188 gzipped
//	  round trip        exact
//	  the rewrite cost  3000 bytes, and 51 compressed
//	  ratio             14.1x on the rewritten output
//
// # The order of the two Closes decides whether the tail arrives
//
// A rewriter can write during its own Close: [lolhtml.OnDocumentEnd] always runs there, and so
// does a text handler for the last chunk of a text node the document left open. If the compressor
// has already been closed by then, those bytes have nowhere to go.
//
// Measured, closing each way round:
//
//	document and handlers                       rewriter first   gzip first
//	a complete document, no append                     34 bytes     34 bytes
//	an append at the document end                       71 bytes     34 bytes
//	an append, document ends inside a tag               58 bytes     21 bytes
//	text held, document ends inside the text             7 bytes      3 bytes
//
// The rewrite is not silent about it: closing the compressor first makes the rewriter's Close
// return "flate: closed writer". So the mistake is detectable, and the way it escapes detection is
// the shape people write:
//
//	defer w.Close()     // runs second
//	defer zw.Close()    // runs first, and the tail is lost
//
// Deferred calls run last in, first out, so the defer written *second* runs *first* - and a defer
// discards the error, which is the only thing that would have said so. Close both in order and
// check both errors, which is what this program does and why it is worth having as an example.
//
// # The write pattern costs nothing in compressed size
//
// A rewriter writes in many small pieces, which looks like it should hurt compression. It does not,
// because a gzip writer buffers and nothing here flushes. Measured, the same document fed to the
// rewriter at four sizes and the output gzipped:
//
//	input chunk       gzipped
//	1                    1188
//	16                   1188
//	512                  1188
//	1048576              1188
//	one write of the whole output   1188
//
// Identical, and identical to compressing the finished output in one go. So a rewrite can be
// piped into a compressor without thinking about buffer sizes, which is worth knowing because the
// obvious defensive move - wrapping the compressor in a bufio.Writer - buys nothing here.
//
// # What a rewrite costs on the wire
//
// Much less than it costs in bytes, when what it adds repeats. Adding rel="noopener" to two hundred
// links added 3000 bytes to the document and 51 to the gzipped stream: a 22 per cent increase
// becomes 4.5 per cent. A rewrite that adds the same attribute everywhere is nearly free
// compressed, and one that adds distinct content - an id per element, a nonce per script - is not.
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Sizes is what a run measured.
type Sizes struct {
	In, Rewritten, Gzipped int
	// InGzipped is the input compressed on its own, so the rewrite's cost on the wire can be
	// stated rather than guessed.
	InGzipped int
	// RoundTrip is true when decompressing gave back exactly what the rewriter wrote.
	RoundTrip bool
}

// Ratio is the compression ratio on the rewritten output.
func (s Sizes) Ratio() float64 {
	if s.Gzipped == 0 {
		return 0
	}
	return float64(s.Rewritten) / float64(s.Gzipped)
}

func (s Sizes) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes in, %d rewritten, %d gzipped\n", s.In, s.Rewritten, s.Gzipped)
	if s.RoundTrip {
		fmt.Fprintf(&b, "  %-18s exact\n", "round trip")
	} else {
		fmt.Fprintf(&b, "  %-18s FAILED: what came back is not what went in\n", "round trip")
	}
	fmt.Fprintf(&b, "  %-18s %d bytes, and %d compressed\n", "the rewrite cost",
		s.Rewritten-s.In, s.Gzipped-s.InGzipped)
	fmt.Fprintf(&b, "  %-18s %.1fx on the rewritten output\n", "ratio", s.Ratio())
	return b.String()
}

// Compress rewrites src into dst through a gzip writer, and returns what it measured.
//
// The two Closes are in order and both errors are checked, which is the whole point: a deferred
// Close in the wrong order loses whatever the rewriter writes at Close, and a deferred Close
// discards the error that would have said so.
func Compress(src io.Reader, dst io.Writer, opts ...lolhtml.Option) (Sizes, error) {
	var s Sizes

	// The input is held only to measure it compressed on its own, which is what makes the
	// rewrite's cost on the wire a figure rather than a guess. A caller who does not want
	// that can stream and skip it.
	input, err := io.ReadAll(src)
	if err != nil {
		return s, err
	}
	s.In = len(input)

	counted := &counting{w: dst}
	zw := gzip.NewWriter(counted)
	rewritten := &counting{w: zw}

	w, err := lolhtml.NewWriter(rewritten, opts...)
	if err != nil {
		return s, err
	}
	if _, err := w.Write(input); err != nil {
		w.Close()
		zw.Close()
		return s, err
	}
	// The rewriter first, because it writes during Close. Then the compressor, because it
	// writes its trailer during Close. Neither error is dropped.
	if err := w.Close(); err != nil {
		zw.Close()
		return s, fmt.Errorf("closing the rewriter: %w", err)
	}
	if err := zw.Close(); err != nil {
		return s, fmt.Errorf("closing the compressor: %w", err)
	}
	s.Rewritten, s.Gzipped = rewritten.n, counted.n

	var inZ bytes.Buffer
	iz := gzip.NewWriter(&inZ)
	if _, err := iz.Write(input); err != nil {
		return s, err
	}
	if err := iz.Close(); err != nil {
		return s, err
	}
	s.InGzipped = inZ.Len()
	return s, nil
}

// counting records how many bytes went through.
type counting struct {
	w io.Writer
	n int
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// RoundTrip rewrites src, compresses it, decompresses it again and reports whether what came back
// is what the rewriter wrote. It holds both copies, which is what a check costs.
func RoundTrip(src io.Reader, opts ...lolhtml.Option) (Sizes, error) {
	input, err := io.ReadAll(src)
	if err != nil {
		return Sizes{}, err
	}

	var gzipped bytes.Buffer
	s, err := Compress(bytes.NewReader(input), &gzipped, opts...)
	if err != nil {
		return s, err
	}

	// What the rewriter wrote, without a compressor in the way.
	want, err := lolhtml.RewriteString(string(input), opts...)
	if err != nil {
		return s, err
	}

	zr, err := gzip.NewReader(bytes.NewReader(gzipped.Bytes()))
	if err != nil {
		return s, fmt.Errorf("reading the gzip stream: %w", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		return s, fmt.Errorf("decompressing: %w", err)
	}
	if err := zr.Close(); err != nil {
		return s, fmt.Errorf("closing the gzip reader: %w", err)
	}
	s.RoundTrip = string(got) == want
	return s, nil
}

// annotate is the rewrite, chosen because what it adds repeats - which is what makes the
// compressed cost interesting.
func annotate() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})
}

func main() {
	out := flag.String("out", "", "where the gzip stream goes, or nowhere")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "gzipout:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	input, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gzipout:", err)
		os.Exit(1)
	}

	s, err := RoundTrip(bytes.NewReader(input), annotate())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gzipout:", err)
		os.Exit(1)
	}
	fmt.Print(s)

	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gzipout:", err)
			os.Exit(1)
		}
		defer f.Close()
		if _, err := Compress(bytes.NewReader(input), f, annotate()); err != nil {
			fmt.Fprintln(os.Stderr, "gzipout:", err)
			os.Exit(1)
		}
	}

	if !s.RoundTrip {
		os.Exit(1)
	}
}

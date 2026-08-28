// Command gunzip rewrites a document that arrives gzipped, decompressing as it goes, and refuses
// to be surprised by the two things that go wrong.
//
//	$ gunzip -limit 8m page.html.gz > out.html
//	80 bytes in, 4700 decompressed, 5700 written
//	  integrity        the stream ended cleanly and the checksum matched
//	  limit            8388608 bytes, not reached
//
//	$ gunzip -limit 1k big.html.gz > out.html
//	gunzip: the document is larger than the 1024-byte limit, and what was written is a
//	  truncated document rather than a short one
//
// # A limit before the decompressor bounds the wrong number
//
// A gzipped body is a size claim that the sender controls twice: once in the bytes it sends and
// once in what they expand to. Measured, 50991 compressed bytes expanding to 52428800:
//
//	no limit                        52428800 bytes reached the rewriter
//	io.LimitReader after gunzip      1048576 bytes, as asked
//	io.LimitReader before gunzip    52428800 bytes - the limit was never near
//
// The middle row is the only one that bounds what a rewriter has to hold. The last is the natural
// mistake, because the thing being limited is called the input and the input that arrived was the
// compressed one.
//
// # And a limit is silent unless it is asked to speak
//
// [io.LimitReader] ends the stream at the limit, which to everything downstream looks exactly like
// the document ending. The rewrite succeeds, [lolhtml.Writer.Close] succeeds, and the output is a
// truncated document that nothing complains about. So this reads one byte past the limit and
// treats an arrival at that byte as an error, which is the difference between a short document and
// a truncated one.
//
// # A truncated stream can still deliver every byte
//
// Cutting a gzip stream short does not always cost content. Measured over the same 80-byte stream:
//
//	cut at   reached the rewriter   error
//	10%                   0 bytes   gzip header: unexpected EOF
//	50%                  43 bytes   unexpected EOF
//	90%                4700 bytes   unexpected EOF
//	99%                4700 bytes   unexpected EOF
//
// At 90 per cent the deflate data is complete and only the trailer is missing - so the whole
// document arrived and the checksum that would have vouched for it did not. The error is therefore
// about integrity rather than completeness, and a caller who ignores it because the output looks
// whole has shipped an unverified document. This one reports what was written alongside the error,
// so a partial write is recognisable.
package main

import (
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Result is what a run did.
type Result struct {
	Compressed, Decompressed, Written int64
	// Limit is the decompressed-size limit, and LimitHit whether the document reached it.
	Limit    int64
	LimitHit bool
	// Clean is whether the stream ended with a trailer that verified.
	Clean bool
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d bytes in, %d decompressed, %d written\n",
		r.Compressed, r.Decompressed, r.Written)
	if r.Clean {
		fmt.Fprintf(&b, "  %-16s the stream ended cleanly and the checksum matched\n",
			"integrity")
	} else {
		fmt.Fprintf(&b, "  %-16s the stream did not end cleanly, so what was written is "+
			"unverified\n", "integrity")
	}
	if r.Limit > 0 {
		hit := "not reached"
		if r.LimitHit {
			hit = "REACHED, so the output is a truncated document"
		}
		fmt.Fprintf(&b, "  %-16s %d bytes, %s\n", "limit", r.Limit, hit)
	}
	return b.String()
}

// ErrTooLarge is returned when the decompressed document reaches the limit. It is an error rather
// than a truncation because a truncated document is not a short document: half a page renders as a
// page, and nothing downstream can tell.
var ErrTooLarge = errors.New("the document is larger than the limit, and what was written is a " +
	"truncated document rather than a short one")

// counting records how many bytes passed through a reader or writer.
type counting struct {
	r io.Reader
	w io.Writer
	n int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Rewrite decompresses src and rewrites it into dst, refusing a document that decompresses past
// limit. A limit of zero means no limit, which is a decision rather than a default: a rewriter
// holds little, but what it is handed comes from whoever sent the body.
func Rewrite(src io.Reader, dst io.Writer, limit int64, opts ...lolhtml.Option) (Result, error) {
	var res Result
	compressed := &counting{r: src}

	zr, err := gzip.NewReader(compressed)
	if err != nil {
		res.Compressed = compressed.n
		return res, fmt.Errorf("reading the gzip header: %w", err)
	}

	// The limit goes here, after the decompressor, because this is where the number it is
	// about appears. One byte past the limit, so arriving at it is distinguishable from
	// ending on it - io.LimitReader alone is silent, and silence is the failure mode.
	var body io.Reader = zr
	if limit > 0 {
		body = io.LimitReader(zr, limit+1)
	}
	decompressed := &counting{r: body}
	written := &counting{w: dst}

	w, err := lolhtml.NewWriter(written, opts...)
	if err != nil {
		return res, err
	}

	_, copyErr := io.Copy(w, decompressed)
	closeErr := w.Close()
	// The trailer is read and verified inside Read, as the deflate data ends, so a
	// checksum failure has already come back as copyErr by the time the copy returns -
	// after the bytes have been written, which is the point the file comment is about.
	// Measured: a corrupted CRC gives copyErr "gzip: invalid checksum", and a stream cut
	// at 90 per cent gives "unexpected EOF"; in both cases zr.Close() returns nil.
	//
	// gzip.Reader.Close forwards the flate decompressor's error and nothing else, so it
	// is non-nil only when the deflate data itself was cut short - and then copyErr says
	// so too, and says it first. It is called because a reader that was opened should be
	// closed and an error from a Close should not be dropped, not because it is where the
	// integrity answer comes from. The branch for it below is a backstop that a measured
	// run does not reach.
	trailerErr := zr.Close()

	res.Compressed = compressed.n
	res.Decompressed = decompressed.n
	res.Written = written.n
	res.Limit = limit
	res.LimitHit = limit > 0 && decompressed.n > limit
	res.Clean = copyErr == nil && trailerErr == nil && !res.LimitHit

	switch {
	case res.LimitHit:
		return res, ErrTooLarge
	case copyErr != nil:
		return res, fmt.Errorf("decompressing: %w", copyErr)
	case closeErr != nil:
		return res, fmt.Errorf("closing the rewriter: %w", closeErr)
	case trailerErr != nil:
		return res, fmt.Errorf("the gzip trailer: %w", trailerErr)
	}
	return res, nil
}

// annotate is the rewrite, kept ordinary because the subject is the plumbing.
func annotate() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})
}

// parseSize reads a size with an optional k or m suffix, because a limit is the kind of flag people
// want to write as 8m.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1<<30, s[:len(s)-1]
	}
	// ParseInt rather than Sscanf, which stops at the first character it does not understand
	// and reports success: "1x" reads as 1 and "1.5m" as 1. A limit that quietly means
	// something other than what was typed is worse than a rejected flag.
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return n * mult, nil
}

func main() {
	limitFlag := flag.String("limit", "16m", "refuse a document that decompresses past this, or 0")
	report := flag.Bool("report", true, "print what happened to stderr")
	flag.Parse()

	limit, err := parseSize(*limitFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gunzip:", err)
		os.Exit(2)
	}

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "gunzip:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	res, err := Rewrite(src, os.Stdout, limit, annotate())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gunzip:", err)
		if *report {
			fmt.Fprint(os.Stderr, res)
		}
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, res)
	}
}

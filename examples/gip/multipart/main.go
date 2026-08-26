// Command multipart rewrites the HTML parts of a multipart body and passes everything else through
// byte for byte.
//
//	$ multipart -boundary B body.txt
//	4 parts: 2 rewritten, 2 copied
//	  text/html                2 parts, 3400 bytes in, 4900 out
//	  application/json         1 part, 210 bytes copied
//	  image/png                1 part, 8192 bytes copied
//
// # Multipart is the join that is safe
//
// The package documentation warns that rewriting fragments separately and concatenating the
// outputs is not the same as rewriting the whole, because a fragment that ends inside a tag
// swallows whatever follows it. Multipart is the exception, and the reason is worth saying: the
// delimiter is out of band. A boundary is written by the multipart writer rather than by the
// rewriter, so a part that ends mid-tag keeps its truncated tag and the next part is untouched.
// Measured, a part ending in `<div attr="` followed by another part:
//
//	part 0: "<p>a</p><div attr=\""
//	part 1: "<p>second part</p>"
//
// So each part is a document and the parts cannot contaminate each other. That is what makes one
// rewriter per part correct rather than merely convenient.
//
// # A rewriter per part is free until the parts are small
//
// A [lolhtml.Writer] cannot be reused, so N parts means N rewriters and N selector registrations.
// That is a fixed cost per part, and whether it matters depends entirely on the part size.
// Measured over 100000 bytes total, fastest of ten runs:
//
//	parts   bytes each   a rewriter per part   one rewriter   ratio
//	    1        99990              5.338ms        5.473ms    0.98x
//	   10         9990              5.336ms        5.368ms    0.99x
//	  100          990              5.668ms         5.58ms    1.02x
//	 1000           90              7.661ms        4.984ms    1.54x
//
// Free above a kilobyte a part and half again as expensive at ninety bytes, where the registration
// costs more than the content. A body of many tiny HTML parts is the shape to watch, and the
// answer is fewer selectors rather than fewer rewriters, because there is no reusing one.
//
// # Closing the rewriter before the next part begins
//
// A rewriter writes during its own Close, and a multipart part stops accepting writes once the
// next one starts. Closing them out of order gives "multipart: can't write to finished part" -
// loud, and lost if the Close is deferred. This closes each rewriter before creating the next
// part, and checks the error.
package main

import (
	"flag"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Counts is what a run did, by media type.
type Counts struct {
	Parts     int
	In, Out   int64
	Rewritten bool
}

// Report is the whole run.
type Report struct {
	ByType map[string]*Counts
	Total  int
}

// Rewritten and Copied are how many parts took each path.
func (r Report) Rewritten() int {
	n := 0
	for _, c := range r.ByType {
		if c.Rewritten {
			n += c.Parts
		}
	}
	return n
}

func (r Report) Copied() int { return r.Total - r.Rewritten() }

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d part%s: %d rewritten, %d copied\n",
		r.Total, plural(r.Total), r.Rewritten(), r.Copied())
	types := make([]string, 0, len(r.ByType))
	for t := range r.ByType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		c := r.ByType[t]
		if c.Rewritten {
			fmt.Fprintf(&b, "  %-24s %d part%s, %d bytes in, %d out\n",
				t, c.Parts, plural(c.Parts), c.In, c.Out)
		} else {
			fmt.Fprintf(&b, "  %-24s %d part%s, %d bytes copied\n",
				t, c.Parts, plural(c.Parts), c.In)
		}
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// counting records how many bytes went through.
type counting struct {
	w io.Writer
	n int64
}

func (c *counting) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Rewrite reads a multipart body from src and writes one to dst, rewriting the parts whose media
// type is html and copying the rest.
//
// The parts are written in the order they arrive, and each rewriter is closed before the next part
// is created - a part stops accepting writes once the next one starts, and a rewriter writes at
// Close.
func Rewrite(src io.Reader, dst io.Writer, boundary string, opts ...lolhtml.Option) (Report, error) {
	r := Report{ByType: map[string]*Counts{}}
	if boundary == "" {
		return r, fmt.Errorf("multipart: no boundary")
	}

	mr := multipart.NewReader(src, boundary)
	mw := multipart.NewWriter(dst)
	if err := mw.SetBoundary(boundary); err != nil {
		return r, fmt.Errorf("multipart: %w", err)
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return r, fmt.Errorf("reading part %d: %w", r.Total+1, err)
		}
		r.Total++

		media := mediaType(part.Header)
		counts := r.ByType[media]
		if counts == nil {
			counts = &Counts{}
			r.ByType[media] = counts
		}
		counts.Parts++

		out, err := mw.CreatePart(part.Header)
		if err != nil {
			part.Close()
			return r, fmt.Errorf("writing part %d: %w", r.Total, err)
		}

		if media != "text/html" {
			// Everything else goes through untouched. A rewriter would corrupt a
			// PNG and would make a JSON body longer without changing what it says.
			n, err := io.Copy(out, part)
			counts.In += n
			counts.Out += n
			part.Close()
			if err != nil {
				return r, fmt.Errorf("copying part %d: %w", r.Total, err)
			}
			continue
		}

		counts.Rewritten = true
		in := &counting{w: io.Discard}
		written := &counting{w: out}
		w, err := lolhtml.NewWriter(written, opts...)
		if err != nil {
			part.Close()
			return r, err
		}
		// The input is counted by teeing it, so the report can say what the rewrite
		// cost per part rather than only in total.
		_, copyErr := io.Copy(w, io.TeeReader(part, in))
		closeErr := w.Close()
		part.Close()
		counts.In += in.n
		counts.Out += written.n
		if copyErr != nil {
			return r, fmt.Errorf("rewriting part %d: %w", r.Total, copyErr)
		}
		if closeErr != nil {
			return r, fmt.Errorf("closing part %d: %w", r.Total, closeErr)
		}
	}

	if err := mw.Close(); err != nil {
		return r, fmt.Errorf("closing the multipart body: %w", err)
	}
	return r, nil
}

// mediaType reads the part's type, lower-cased and without parameters, or the empty string when
// the part does not say. A part with no Content-Type is not HTML: guessing would rewrite whatever
// happened to look like markup.
func mediaType(h textproto.MIMEHeader) string {
	v := h.Get("Content-Type")
	if v == "" {
		return "(none)"
	}
	t, _, err := mime.ParseMediaType(v)
	if err != nil {
		return "(unparseable)"
	}
	return strings.ToLower(t)
}

// annotate is the rewrite, kept ordinary because the subject is the plumbing.
func annotate() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})
}

func main() {
	boundary := flag.String("boundary", "", "the multipart boundary")
	report := flag.Bool("report", true, "print the counts to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "multipart:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	r, err := Rewrite(src, os.Stdout, *boundary, annotate())
	if err != nil {
		fmt.Fprintln(os.Stderr, "multipart:", err)
		if *report {
			fmt.Fprint(os.Stderr, r)
		}
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, r)
	}
}

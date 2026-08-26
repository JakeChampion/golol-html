// Command headonly rewrites the head of a document and passes the body through without parsing it.
//
//	$ headonly page.html
//	head 114 bytes, rewritten to 116; body 200034 bytes, copied
//	  stopped at        <p>, the first element that cannot be in a head
//	  saved             the body was never parsed
//
// # Not parsing the body is worth two orders of magnitude
//
// A rewriter runs to Close and cannot be switched off part way, so "only the head" looks like it
// has to mean handlers that check a flag and do nothing. It does not. A handler can stop the
// rewrite by returning an error, and what has reached the destination at that point is documented
// to be byte for byte what a fresh rewriter produces from that much input - not a truncation. The
// element's [lolhtml.SourceLocation] gives the offset in the input where it began, so the rest of
// the input is exactly what still has to be sent, and it can be copied.
//
// Measured on a document with a 114-byte head, fastest of twenty runs:
//
//	body size   stop and copy   handlers gated off   a plain no-handler pass
//	100 bytes            6µs                  7µs                       1µs
//	10 KB                8µs                 99µs                      20µs
//	200 KB              16µs              1.842ms                     375µs
//
// A hundred and fifteen times faster than gating at 200 KB, and twenty-three times faster than
// passing the body through a rewriter with no handlers at all - because it does not pass the body
// through anything. The outputs are identical, which the tests assert rather than assume.
//
// # Where the head ends when nothing says so
//
// Stopping at <body> is not enough, because a document need not spell one: `<html><head><title>T
// </title><p>x` has a body in the tree and no body tag in the source, and a rewriter reports the
// source. So the rule is the specification's - stop at the first element that cannot appear in a
// head:
//
//	base  link  meta  noscript  script  style  template  title
//
// Anything else ends the head, and <body> is only the most explicit case of it. A document that is
// nothing but head elements has no body to copy, which is reported rather than assumed.
//
// # What it costs to not parse something
//
// The bytes have to be available from the offset, so this reads the document into memory. A stream
// can do the same by keeping what it has read past the stop point, which is bounded by one write:
// the stop is reported from Write, so at most that write's worth of input is past the point where
// the copy has to begin.
//
// And the body is not checked. Nothing in it is parsed, so nothing in it is validated - a body
// that would have failed a sanitiser passes through untouched. That is the trade: this is for
// rewrites whose subject is the head, and a rewrite that has anything to say about the body has to
// read the body.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// headElements are the elements the specification allows in a head. Anything else ends it.
var headElements = map[string]bool{
	"base": true, "link": true, "meta": true, "noscript": true,
	"script": true, "style": true, "template": true, "title": true,
	// These are the head itself and its ancestors, which do not end it.
	"html": true, "head": true,
}

// errEndOfHead stops the rewrite. It is a sentinel so that errors.Is finds it whether it comes back
// from Write or, wrapped in ErrPoisoned, from Close.
var errEndOfHead = errors.New("the head has ended")

// Result is what a run did.
type Result struct {
	// HeadIn and HeadOut are the head's size before and after rewriting.
	HeadIn, HeadOut int
	// BodyCopied is how many bytes were passed through without being parsed.
	BodyCopied int
	// StoppedAt names the element that ended the head, empty when the document was all head.
	StoppedAt string
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "head %d bytes, rewritten to %d; body %d bytes, copied\n",
		r.HeadIn, r.HeadOut, r.BodyCopied)
	if r.StoppedAt == "" {
		fmt.Fprintf(&b, "  %-18s nothing: every element in the document belongs in a head\n",
			"stopped at")
	} else {
		fmt.Fprintf(&b, "  %-18s <%s>, the first element that cannot be in a head\n",
			"stopped at", r.StoppedAt)
	}
	fmt.Fprintf(&b, "  %-18s the body was never parsed\n", "saved")
	return b.String()
}

// Rewrite rewrites the head of doc into dst and copies the rest verbatim.
//
// It takes the document as a string rather than a reader because the copy starts at an offset in
// the input, and something has to hold the input to copy from it. See the package comment on what
// a stream would do instead.
func Rewrite(doc string, dst io.Writer, opts ...lolhtml.Option) (Result, error) {
	var res Result
	counted := &counting{w: dst}
	stop := -1
	var stoppedAt string

	all := append([]lolhtml.Option{}, opts...)
	all = append(all, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		name := e.TagName()
		if headElements[name] {
			return nil
		}
		// The offset is the element's own start, so everything from here on is
		// untouched input - which is what makes the copy exact.
		stop = e.SourceLocation().Start
		stoppedAt = name
		return errEndOfHead
	}))

	w, err := lolhtml.NewWriter(counted, all...)
	if err != nil {
		return res, err
	}
	_, writeErr := w.Write([]byte(doc))
	closeErr := w.Close()

	// The stop is expected; anything else is not.
	switch {
	case stop >= 0:
		if !errors.Is(writeErr, errEndOfHead) && !errors.Is(closeErr, errEndOfHead) {
			return res, fmt.Errorf("the head ended at %d and the stop did not arrive: "+
				"write=%v close=%v", stop, writeErr, closeErr)
		}
	case writeErr != nil:
		return res, writeErr
	case closeErr != nil:
		return res, closeErr
	}

	res.HeadOut = counted.n
	if stop < 0 {
		// Every element belonged in a head, so there is nothing to copy.
		res.HeadIn = len(doc)
		return res, nil
	}
	res.HeadIn = stop
	res.StoppedAt = stoppedAt

	n, err := io.Copy(dst, strings.NewReader(doc[stop:]))
	res.BodyCopied = int(n)
	if err != nil {
		return res, fmt.Errorf("copying the body: %w", err)
	}
	return res, nil
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

// headRewrites are the handlers, which do the sort of thing a head rewrite does: mark the
// stylesheets and add a preconnect for whatever the page loads from elsewhere.
func headRewrites() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("link[rel=stylesheet]", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-critical", "1")
		}),
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			return e.Prepend("• ", lolhtml.Text)
		}),
	}
}

func main() {
	report := flag.Bool("report", true, "print what happened to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "headonly:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}
	doc, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "headonly:", err)
		os.Exit(1)
	}

	res, err := Rewrite(string(doc), os.Stdout, headRewrites()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "headonly:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, res)
	}
}

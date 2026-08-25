// Command clientgone rewrites a page to a destination that stops accepting bytes partway
// through, which is what a browser closing a connection looks like from inside a handler.
//
//	$ clientgone -budget 100
//	the destination accepted 99 of 760 bytes and then failed
//	  reported by          Write
//	  errors.Is(gone)      true       from Write
//	  errors.Is(gone)      true       from Close, under ErrPoisoned=true
//	  links rewritten      2          the count when the rewrite stopped, not the page's total
//	  comments seen        2
//	  text chunks seen     6
//	  document end ran     false      <- a summary written there would not have run
//	  writes to the sink   30
//
// # What stops
//
// Everything. Once the destination has refused a write, no further handler runs: the
// rewriter is abandoned, later output chunks are dropped, and the error surfaces from the
// call that was running. That includes the document-end handler, which is where a rewrite
// naturally puts its summary - so a program that logs its totals there logs nothing at all
// on the run where the client went away.
//
// The accounting has to live outside the handlers to survive, which is what this program
// does: the counters are fields, readable after the failure, and they say what the rewrite
// had reached rather than what the page contains. Those are different numbers and only one
// of them is available.
//
// # Which call reports it
//
// The one that was running. A destination failure during Write surfaces there; the Writer is
// poisoned, so every later Write and the Close report it again, wrapped - errors.Is finds the
// destination's own error through all of it.
//
// Close can be the call that fails, but only when Close is the call that writes. Measured:
//
//	document                     written during Close
//	<p>text</p>                  nothing
//	<p>unclosed text             nothing
//	<script>var a =              nothing
//	<p>a</p                      the unfinished end tag
//	<div a="x                    the unfinished attribute
//	<!--unclosed                 the unfinished comment
//	<p>text</p><                 the bare less-than
//	any document, with a handler appending at the document end
//
// -closes prints that table from a live measurement rather than from this comment.
//
// So a document that ends cleanly, or in the middle of text, has already been handed over
// entirely by the time Close is called, and Close cannot discover a broken destination. A
// document that ends inside a token has not.
//
// # What the destination keeps
//
// What it accepted. The rewriter does not retract anything, so a client that disconnected
// after 100 bytes has 100 bytes of a rewritten page - well-formed as far as it goes, and a
// short version of a document it asked for. Nothing can be done about that from inside the
// rewrite; it is an argument for buffering the output when a partial response is worse than
// no response, which is what examples/gip/mixed does.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// ErrGone is what the fake destination returns, standing in for a closed connection.
var ErrGone = errors.New("clientgone: the destination stopped accepting")

// Unit is repeated to make the page.
const Unit = `<a href="/x">link</a><!--c--><p>text</p>`

// BudgetSink accepts up to Budget bytes in total and fails on the write that would exceed it.
// A connection that closes behaves this way: some of the response was accepted, the rest
// cannot be.
type BudgetSink struct {
	Budget int

	Written int
	Calls   int

	// InClose is set by the caller around Close, so the sink can record whether it was
	// written to during the final flush.
	InClose    bool
	CloseCalls int
}

func (b *BudgetSink) Write(p []byte) (int, error) {
	b.Calls++
	if b.InClose {
		b.CloseCalls++
	}
	if b.Written+len(p) > b.Budget {
		return 0, ErrGone
	}
	b.Written += len(p)
	return len(p), nil
}

// Run is one rewrite against a destination with a budget.
type Run struct {
	Budget    int
	WriteSize int

	// The counters are fields rather than closures over a summary, because a summary
	// written in the document-end handler never runs when the destination fails.
	Links, Comments, TextChunks int
	DocumentEndRan              bool

	Accepted, Offered  int
	SinkCalls          int
	WriteErr, CloseErr error
}

// Rewrite feeds a page of size bytes to a rewriter whose destination fails after budget
// bytes.
func Rewrite(budget, writeSize, size int) (*Run, error) {
	r := &Run{Budget: budget, WriteSize: writeSize}
	doc := strings.Repeat(Unit, size/len(Unit)+1)
	dst := &BudgetSink{Budget: budget}

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			r.Links++
			return e.SetAttribute("rel", "nofollow")
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { r.Comments++; return nil }),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { r.TextChunks++; return nil }),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			// The place a summary belongs, and the place it does not survive.
			r.DocumentEndRan = true
			return nil
		}))
	if err != nil {
		return nil, err
	}

	step := writeSize
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		chunk := doc[i:min(i+step, len(doc))]
		r.Offered += len(chunk)
		if _, r.WriteErr = w.Write([]byte(chunk)); r.WriteErr != nil {
			break
		}
	}
	dst.InClose = true
	r.CloseErr = w.Close()

	r.Accepted = dst.Written
	r.SinkCalls = dst.Calls
	return r, nil
}

// ReportedBy names the call that first reported the failure, which is the one that was
// running when the destination refused.
func (r *Run) ReportedBy() string {
	switch {
	case r.WriteErr != nil:
		return "Write"
	case r.CloseErr != nil:
		return "Close"
	}
	return "nothing: the destination never refused"
}

func (r *Run) String() string {
	var b strings.Builder
	if r.WriteErr == nil && r.CloseErr == nil {
		fmt.Fprintf(&b, "the destination accepted all %d bytes\n", r.Accepted)
	} else {
		fmt.Fprintf(&b, "the destination accepted %d of %d bytes and then failed\n",
			r.Accepted, r.Offered)
	}
	fmt.Fprintf(&b, "  %-20s %s\n", "reported by", r.ReportedBy())
	fmt.Fprintf(&b, "  %-20s %-10v %s\n", "errors.Is(gone)", errors.Is(r.WriteErr, ErrGone), "from Write")
	fmt.Fprintf(&b, "  %-20s %-10v %s\n", "errors.Is(gone)", errors.Is(r.CloseErr, ErrGone),
		"from Close, under ErrPoisoned="+fmt.Sprint(errors.Is(r.CloseErr, lolhtml.ErrPoisoned)))
	fmt.Fprintf(&b, "  %-20s %-10d %s\n", "links rewritten", r.Links,
		"the count when the rewrite stopped, not the page's total")
	fmt.Fprintf(&b, "  %-20s %d\n", "comments seen", r.Comments)
	fmt.Fprintf(&b, "  %-20s %d\n", "text chunks seen", r.TextChunks)
	fmt.Fprintf(&b, "  %-20s %-10v %s\n", "document end ran", r.DocumentEndRan,
		summaryNote(r.DocumentEndRan))
	fmt.Fprintf(&b, "  %-20s %d\n", "writes to the sink", r.SinkCalls)
	return b.String()
}

func summaryNote(ran bool) string {
	if ran {
		return ""
	}
	return "<- a summary written there would not have run"
}

// CloseWrites reports, for one document, whether anything reached the destination during
// Close. It is the table in the documentation above, measurable rather than asserted.
func CloseWrites(doc string, appendAtEnd bool) (before, during int, closeErr error, err error) {
	dst := &BudgetSink{Budget: 1 << 30}
	opts := []lolhtml.Option{
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if appendAtEnd {
				return d.Append("<!--end-->", lolhtml.HTML)
			}
			return nil
		}),
	}
	w, nerr := lolhtml.NewWriter(dst, opts...)
	if nerr != nil {
		return 0, 0, nil, nerr
	}
	if _, werr := w.Write([]byte(doc)); werr != nil {
		return 0, 0, nil, werr
	}
	before = dst.Calls
	// Nothing more is affordable from here, so a write during Close fails and Close has to
	// report it.
	dst.InClose = true
	dst.Budget = dst.Written
	closeErr = w.Close()
	return before, dst.CloseCalls, closeErr, nil
}

func main() {
	budget := flag.Int("budget", 100, "bytes the destination will accept")
	writeSize := flag.Int("write", 0, "write size in bytes, or 0 for one write")
	size := flag.Int("size", 740, "page size in bytes")
	closes := flag.Bool("closes", false, "instead, show which documents write to the destination during Close")
	flag.Parse()

	if *closes {
		fmt.Printf("%-22s %-8s %-8s %s\n", "document", "before", "during", "close error")
		for _, doc := range []string{
			`<p>text</p>`, `<p>unclosed text`, `<script>var a =`,
			`<p>a</p`, `<div a="x`, `<!--unclosed`, `<p>text</p><`,
		} {
			before, during, cerr, err := CloseWrites(doc, false)
			if err != nil {
				fmt.Fprintln(os.Stderr, "clientgone:", err)
				os.Exit(1)
			}
			fmt.Printf("%-22q %-8d %-8d %v\n", doc, before, during, cerr)
		}
		before, during, cerr, err := CloseWrites(`<p>text</p>`, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "clientgone:", err)
			os.Exit(1)
		}
		fmt.Printf("%-22s %-8d %-8d %v\n", "the same, appending", before, during, cerr)
		return
	}

	if *budget < 0 || *size < 1 {
		fmt.Fprintln(os.Stderr, "clientgone: a budget of at least zero and a size of at least one")
		os.Exit(2)
	}

	run, err := Rewrite(*budget, *writeSize, *size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clientgone:", err)
		os.Exit(1)
	}
	fmt.Print(run)
}

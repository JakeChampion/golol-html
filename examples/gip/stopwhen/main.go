// Command stopwhen rewrites a stream that never ends and stops when it has what it came for.
//
//	$ stopwhen -want 3
//	wanted 3 headings, saw 3
//	  mechanism        a sentinel error from the handler
//	  bytes accepted   0 of 4096 written - a refused write reports none, though 72 were rewritten
//	  bytes out        72
//	  the sink holds   a rewrite of the first 72 bytes of the input
//	  write error      lolhtml: element handler for "h2": found heading 3: stopwhen: done
//	  errors.Is(done)  true
//	  close error      lolhtml: writer is poisoned by an earlier error: … stopwhen: done
//	  errors.Is(done)  true
//	  errors.Is(poison) true
//
//	$ stopwhen -want 3 -how quiet
//	wanted 3 headings, saw 114
//	  mechanism        stopped writing and closed
//	  bytes accepted   4096 of 4096 written
//	  bytes out        4096
//	  the sink holds   a rewrite of the first 4096 bytes of the input
//	  close error      <nil>
//
// There are two ways to stop, and they answer different questions. The two runs above are the
// difference: one stopped at the third heading and one stopped after the write that contained
// it, having seen 114.
//
// # A sentinel error from the handler
//
// Return an error and the rewrite ends where the handler said. The error identity survives:
// the handler's error is wrapped, so errors.Is finds the caller's sentinel both in what Write
// returns and in what Close returns - Close reports it under ErrPoisoned. Use this when the
// condition is discovered inside a handler and the caller has to know why it stopped.
//
// What reaches the sink is the useful part, and it is a stronger guarantee than it looks: the
// output is a rewrite of a prefix of the input, byte for byte the same as feeding that prefix
// to a fresh rewriter. Not a truncation mid-token, and not a partially serialised element -
// the unit whose handler stopped is not emitted at all. So a caller can keep or serve what it
// has.
//
// Where the prefix ends depends on which handler stopped:
//
//	an element handler   the bytes before that element's start tag
//	an end-tag handler   the bytes before that end tag
//	a text handler       the bytes before that chunk - and which chunk is the nth depends
//	                     on the write sizes, so this position is not a property of the
//	                     document
//
// The last row is the reason to count text nodes rather than chunks: a chunk is a fact about
// how the input arrived. Accumulate to IsLastInTextNode and stop there, and the position is
// the document's again.
//
// # Stop writing and close
//
// If the condition is about the caller rather than the document - enough data, long enough,
// bored - the mechanism is to stop feeding it. Close returns nil, the output is a rewrite of
// what was fed, and nothing is poisoned.
//
// The cost is granularity. The condition is checked between writes, so the rewrite overshoots
// by up to one write's worth of document: with 4 KB reads and this generated stream, asking to
// stop at the third heading stops after the 114th. That is fine when the point is to bound the
// work and wrong when the point is to stop at a place in the document, which is what the
// sentinel is for.
//
// # Either way
//
// Close still has to be called, and it releases everything: the handles a rewrite held are
// gone afterwards whether it ended at the document's end, at a handler's error, or in the
// middle of a stream nobody intends to finish reading.
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

// ErrDone is the sentinel a handler returns when the condition is met. A caller's own error
// type works the same way: what matters is that the handler wraps something it can recognise
// afterwards, since the rewriter's error will be wrapped around it.
var ErrDone = errors.New("stopwhen: done")

// Unit is what the generated stream repeats. The stream is infinite in the sense that
// matters: it is produced as it is consumed and never held.
const Unit = `<h2 id="s">head</h2><p>body text</p>`

// stream writes Unit over and over into w, in writes of size bytes, until w refuses or stop
// returns true. It reports how many bytes w accepted, how many were offered, and the error.
//
// The writes continue the repetition rather than restarting it, which is what makes the stream
// a document rather than a sequence of fragments: a write size that is not a multiple of the
// unit still produces `<h2 …>head</h2><p>body text</p>` over and over. The first version of
// this restarted the pattern each write and the program's own prefix check caught it.
//
// A refused write is not a partial one to the caller: Write reports 0 on failure even when the
// rewriter consumed part of what it was given, so what this counts is bytes accepted.
func stream(w io.Writer, size int, stop func() bool) (fed, written int, err error) {
	src := generate(size + len(Unit))
	at := 0
	for !stop() {
		chunk := src[at : at+size]
		at = (at + size) % len(Unit)
		n, werr := w.Write([]byte(chunk))
		fed += n
		written += size
		if werr != nil {
			return fed, written, werr
		}
	}
	return fed, written, nil
}

// generate returns at least n bytes of the repeating stream. Reading a window of it at any
// offset that is a multiple of the unit length gives the same bytes the stream would have
// produced at that point, which is what lets stream keep its place with an index rather than
// with a buffer of everything written so far.
func generate(n int) string {
	return strings.Repeat(Unit, n/len(Unit)+2)
}

// Stop is how a run ended.
type Stop struct {
	// How is the mechanism: a sentinel error, or ceasing to write.
	How string
	// Seen is how many times the condition's counter advanced.
	Seen int
	// Want is what was asked for, and Seen above is what the condition actually reached -
	// they differ when the check happens between writes.
	Want int
	// Fed is bytes the rewriter accepted, Written is bytes offered to it, and Out is bytes
	// that reached the sink. Fed and Written differ on the write that failed: Write reports
	// zero when it fails, though the rewriter consumed part of what it was given.
	Fed, Written, Out int
	// WriteErr and CloseErr are what the two calls reported.
	WriteErr, CloseErr error
	// PrefixRewrite is whether the output is byte-for-byte a rewrite of the first Out
	// bytes of the input, which is the guarantee worth checking rather than assuming.
	PrefixRewrite bool
}

func (s Stop) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "wanted %d headings, saw %d\n", s.Want, s.Seen)
	fmt.Fprintf(&b, "  %-16s %s\n", "mechanism", s.How)
	if s.Fed != s.Written {
		fmt.Fprintf(&b, "  %-16s %d of %d written - a refused write reports none, though %d were rewritten\n",
			"bytes accepted", s.Fed, s.Written, s.Out)
	} else {
		fmt.Fprintf(&b, "  %-16s %d of %d written\n", "bytes accepted", s.Fed, s.Written)
	}
	fmt.Fprintf(&b, "  %-16s %d\n", "bytes out", s.Out)
	fmt.Fprintf(&b, "  %-16s %v\n", "the sink holds",
		holds(s.PrefixRewrite, s.Out))
	if s.WriteErr != nil {
		fmt.Fprintf(&b, "  %-16s %v\n", "write error", s.WriteErr)
		fmt.Fprintf(&b, "  %-16s %v\n", "errors.Is(done)", errors.Is(s.WriteErr, ErrDone))
	}
	fmt.Fprintf(&b, "  %-16s %v\n", "close error", s.CloseErr)
	if s.CloseErr != nil {
		fmt.Fprintf(&b, "  %-16s %v\n", "errors.Is(done)", errors.Is(s.CloseErr, ErrDone))
		fmt.Fprintf(&b, "  %-16s %v\n", "errors.Is(poison)",
			errors.Is(s.CloseErr, lolhtml.ErrPoisoned))
	}
	return b.String()
}

func holds(prefix bool, n int) string {
	if prefix {
		return fmt.Sprintf("a rewrite of the first %d bytes of the input", n)
	}
	return "something that is not a rewrite of any prefix of the input"
}

// RunSentinel stops by returning ErrDone from the handler on the nth heading.
func RunSentinel(want, writeSize int) (Stop, error) {
	s := Stop{How: "a sentinel error from the handler", Want: want}
	var out strings.Builder

	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
		s.Seen++
		if s.Seen == want {
			return fmt.Errorf("found heading %d: %w", want, ErrDone)
		}
		return nil
	}))
	if err != nil {
		return s, err
	}

	// The condition is discovered inside the handler, so the writing loop has no reason
	// to stop on its own: it writes until the rewriter refuses.
	s.Fed, s.Written, s.WriteErr = stream(w, writeSize, func() bool { return false })
	s.CloseErr = w.Close()
	s.Out = out.Len()

	ok, err := isPrefixRewrite(out.String())
	if err != nil {
		return s, err
	}
	s.PrefixRewrite = ok
	return s, nil
}

// RunQuiet stops by ceasing to write once the condition is met, then closing.
func RunQuiet(want, writeSize int) (Stop, error) {
	s := Stop{How: "stopped writing and closed", Want: want}
	var out strings.Builder

	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
		s.Seen++
		return nil
	}))
	if err != nil {
		return s, err
	}

	s.Fed, s.Written, s.WriteErr = stream(w, writeSize, func() bool { return s.Seen >= want })
	s.CloseErr = w.Close()
	s.Out = out.Len()

	ok, err := isPrefixRewrite(out.String())
	if err != nil {
		return s, err
	}
	s.PrefixRewrite = ok
	return s, nil
}

// isPrefixRewrite reports whether got is what a fresh rewriter produces from the first
// len(got) bytes of the stream. The handler here does nothing, so the comparison is against
// the input rather than against another edit.
func isPrefixRewrite(got string) (bool, error) {
	prefix := generate(len(got))[:len(got)]
	again, err := lolhtml.RewriteString(prefix,
		lolhtml.OnElement("h2", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		return false, err
	}
	return got == again, nil
}

func main() {
	want := flag.Int("want", 3, "stop at this many headings")
	how := flag.String("how", "sentinel", `how to stop: "sentinel" or "quiet"`)
	writeSize := flag.Int("write", 4096, "write size in bytes")
	flag.Parse()

	if *want < 1 || *writeSize < 1 {
		fmt.Fprintln(os.Stderr, "stopwhen: -want and -write are counts, not zero")
		os.Exit(2)
	}

	var (
		s   Stop
		err error
	)
	switch *how {
	case "sentinel":
		s, err = RunSentinel(*want, *writeSize)
	case "quiet":
		s, err = RunQuiet(*want, *writeSize)
	default:
		fmt.Fprintf(os.Stderr, "stopwhen: no mechanism %q; sentinel or quiet\n", *how)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stopwhen:", err)
		os.Exit(1)
	}
	fmt.Print(s)

	// The guarantee is the reason a caller can keep what it has, so failing it is worth an
	// exit status rather than a line of output.
	if !s.PrefixRewrite {
		os.Exit(1)
	}
}

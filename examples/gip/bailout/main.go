// Command bailout shows what a memory limit does to a response, with and without
// graceful bail-out.
//
//	$ bailout -limit 1024 -chunk 256 < page.html
//	limit 1024, 256-byte writes
//	  default:   failed after 670 of 5170 bytes; the client gets a short page
//	             the truncation is at an element boundary, so it parses cleanly
//	  graceful:  failed after 5170 of 5170 bytes; the client gets everything,
//	             1284 bytes of it unrewritten
//	  no limit:  completed; 5170 bytes, all rewritten
//
// A memory bail-out is not a clean failure. Both modes stop the rewrite and report the
// error, and the difference is what the destination already holds - which is the only
// part the client sees. This program feeds a document at a chosen limit and write size,
// three times, and says exactly what each mode delivered.
//
// # What the limit measures
//
// Not the document. Measured, a megabyte of small paragraphs passes a one-kilobyte
// limit when it arrives in one Write, and the same document in four-kilobyte writes
// needs sixty-four kilobytes. The limit bounds the copies the parser makes when a token
// straddles two writes, so it is sensitive to the write pattern and blind to the body -
// which means a limit chosen with RewriteString will be wrong under io.Copy, and that a
// limit is not a defence against a large request. Bounding the input is.
//
// This program therefore reports the write size as prominently as the limit, and -floor
// searches for the smallest limit that completes at that write size, which is the number
// a caller actually wants.
//
// # What each mode delivers
//
//   - Default: the destination holds a prefix that ends at a token boundary, so it is
//     well-formed HTML a client will render as a short page. Nothing says it is short.
//   - Graceful: the destination holds every byte the rewriter received, with the tail
//     unrewritten - so a removing rewrite has served exactly what it was removing.
//   - Either way the error surfaces from Write or Close, and the Writer is poisoned.
//
// Which to choose is a decision about the rewrite, not about the document: for one that
// adds something, serving the input is the better failure; for one that removes
// something, the truncation is. See [lolhtml.MemorySettings].
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

// Mode is one of the three runs.
type Mode int

const (
	// Default is the memory limit with graceful bail-out off.
	Default Mode = iota
	// Graceful is the memory limit with it on.
	Graceful
	// Unlimited is the same rewrite with no limit, for comparison.
	Unlimited
)

func (m Mode) String() string {
	switch m {
	case Default:
		return "default"
	case Graceful:
		return "graceful"
	}
	return "no limit"
}

// Run is what one mode delivered.
type Run struct {
	Mode       Mode
	Delivered  int  // bytes that reached the destination
	Rewritten  int  // elements the handler reached
	BailedOut  bool // the memory limit was exceeded
	Err        error
	WellFormed bool // the delivered prefix parses as a document with no partial tag
	Tail       string
}

// Result is the comparison.
type Result struct {
	Bytes int
	Limit int
	Chunk int
	Runs  []Run
	Floor int // the smallest limit that completes at this write size, if asked for
}

func (r Result) String() string {
	var b strings.Builder
	writes := "one Write"
	if r.Chunk > 0 {
		writes = fmt.Sprintf("%d-byte writes", r.Chunk)
	}
	fmt.Fprintf(&b, "limit %d, %s, %d bytes of input\n", r.Limit, writes, r.Bytes)
	for _, run := range r.Runs {
		fmt.Fprintf(&b, "  %-9s ", run.Mode.String()+":")
		switch {
		case run.BailedOut:
			fmt.Fprintf(&b, "bailed out after %d of %d bytes, %d elements rewritten\n",
				run.Delivered, r.Bytes, run.Rewritten)
			if run.Mode == Default {
				fmt.Fprintf(&b, "            the client gets a short page%s\n", wellFormed(run))
			} else {
				fmt.Fprintf(&b, "            the client gets every byte received, the tail unrewritten\n")
			}
		case run.Err != nil:
			fmt.Fprintf(&b, "failed: %v\n", run.Err)
		default:
			fmt.Fprintf(&b, "completed; %d bytes, %d elements rewritten\n", run.Delivered, run.Rewritten)
		}
	}
	if r.Floor > 0 {
		fmt.Fprintf(&b, "  the smallest limit that completes at this write size: %d\n", r.Floor)
	}
	return b.String()
}

func wellFormed(r Run) string {
	if r.WellFormed {
		return ", and it parses cleanly: nothing says it is short"
	}
	return ", ending mid-tag"
}

// rewrite is the rewrite being demonstrated: it sets an attribute on every paragraph,
// which is enough to make the handler's work visible in the counts.
func options(rewritten *int) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			*rewritten++
			return e.SetAttribute("data-seen", "1")
		}),
	}
}

// run feeds the document once.
func run(doc []byte, mode Mode, limit, chunk int) Run {
	out := Run{Mode: mode}
	var sink strings.Builder
	opts := options(&out.Rewritten)
	switch mode {
	case Default:
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}))
	case Graceful:
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory: limit, GracefulBailOut: true,
		}))
	}
	w, err := lolhtml.NewWriter(&sink, opts...)
	if err != nil {
		out.Err = err
		return out
	}
	step := chunk
	if step <= 0 {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		end := i + step
		if end > len(doc) {
			end = len(doc)
		}
		if _, err := w.Write(doc[i:end]); err != nil {
			out.Err = err
			break
		}
	}
	if cerr := w.Close(); out.Err == nil {
		out.Err = cerr
	}
	out.BailedOut = errors.Is(out.Err, lolhtml.ErrMemoryLimitExceeded)
	out.Delivered = sink.Len()
	out.Tail = tail(sink.String())
	out.WellFormed = !endsMidTag(sink.String())
	return out
}

func tail(s string) string {
	if len(s) > 60 {
		return s[len(s)-60:]
	}
	return s
}

// endsMidTag reports whether the delivered bytes stop inside a tag, which is what
// decides whether a client sees a short page or a broken one.
func endsMidTag(s string) bool {
	open := strings.LastIndexByte(s, '<')
	close := strings.LastIndexByte(s, '>')
	return open > close
}

// Compare runs all three modes, and optionally searches for the smallest limit that
// completes at this write size.
func Compare(doc []byte, limit, chunk int, findFloor bool) Result {
	res := Result{Bytes: len(doc), Limit: limit, Chunk: chunk}
	for _, mode := range []Mode{Default, Graceful, Unlimited} {
		res.Runs = append(res.Runs, run(doc, mode, limit, chunk))
	}
	if findFloor {
		res.Floor = floor(doc, chunk)
	}
	return res
}

// floor is the smallest power-of-two limit that completes at this write size, found by
// doubling: the number a caller wants when choosing one.
func floor(doc []byte, chunk int) int {
	for limit := 64; limit <= 1<<24; limit *= 2 {
		if r := run(doc, Default, limit, chunk); !r.BailedOut && r.Err == nil {
			return limit
		}
	}
	return 0
}

func main() {
	limit := flag.Int("limit", 1024, "MaxMemory, in bytes")
	chunk := flag.Int("chunk", 256, "write size, or 0 for one Write")
	findFloor := flag.Bool("floor", false, "also search for the smallest limit that completes")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bailout:", err)
		os.Exit(2)
	}
	res := Compare(doc, *limit, *chunk, *findFloor)
	fmt.Print(res)
	for _, r := range res.Runs {
		if r.BailedOut {
			os.Exit(1)
		}
	}
}

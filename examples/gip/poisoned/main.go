// Command poisoned walks the ways a rewrite can fail and prints what each call returns
// afterwards.
//
//	$ poisoned
//	failure                first Write            later Write    Close                  delivered
//	handler error          the handler's error    ErrPoisoned     ErrPoisoned + cause    prefix
//	destination error      the sink's error       ErrPoisoned     ErrPoisoned + cause    nothing
//	memory limit           ErrMemoryLimitExceeded ErrPoisoned     ErrPoisoned + cause    nothing
//	ambiguous tag          ErrAmbiguousTag        ErrPoisoned     ErrPoisoned + cause    prefix
//	handler panic          re-raised on this goroutine            ErrPoisoned            prefix
//	after Close            ErrClosed              ErrClosed       nil                    everything
//
// One rule underneath all of it: lol-html cannot resume after an error, so the first
// failure is the last thing that happens to that Writer. Every later call reports
// [lolhtml.ErrPoisoned] wrapped around the original cause, so errors.Is and errors.As
// still reach it however late they are asked - and Close is still worth calling, because
// it is the deterministic release of the native handles.
//
// What the destination already holds is the part that decides what a client sees, and it
// is usually not nothing. Everything the rewriter had flushed before the failure has been
// written, and it ends at a token boundary, so it reads as a short page rather than as an
// error. A rewrite that has to refuse a document must hold its own output rather than
// relying on the failure.
//
// "Usually", because the prefix can be empty, and the memory-limit row is the case that
// shows it. The buffer requirement is decided early, so a limit too small for the document
// fails on the first write - before any output has been flushed to the destination - and
// the default bail-out discards what it was holding. Whether a client gets a short page or
// an empty one is a fact about when the failure happens, not about the kind of failure, so
// the row above is what this configuration produces rather than a rule about memory limits.
//
// This program is a demonstration rather than a discovery: everything it prints is
// documented behaviour, and it exists so that the whole state machine can be seen in one
// place, and so that a change to any part of it fails a test.
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

// Failure is one way a rewrite can stop.
type Failure struct {
	Name string
	// Build makes a Writer that will fail in this way, writing to dst.
	Build func(dst io.Writer) (*lolhtml.Writer, error)
	// Doc is the document to feed it.
	Doc string
	// Chunk is the write size, for the failures that need one.
	Chunk int
	// Panics says the failure arrives as a panic rather than an error.
	Panics bool
}

// Outcome is what happened.
type Outcome struct {
	Failure   string
	FirstErr  error
	LaterErr  error
	CloseErr  error
	Recovered any
	Delivered string
	Poisoned  bool // the later error reports ErrPoisoned
	CauseKept bool // the later error still reaches the original cause
	MidTag    bool // the delivered prefix stops inside a tag
}

// ErrHandler is the error the handler failure uses, so the report can show that the
// cause survives.
var ErrHandler = errors.New("the handler refused this document")

// ErrSink is the destination failure.
var ErrSink = errors.New("the destination refused this write")

type brokenSink struct{}

func (brokenSink) Write(p []byte) (int, error) { return 0, ErrSink }

// Failures are the ways to stop, in the order the report prints them.
func Failures() []Failure {
	body := strings.Repeat("<p>paragraph with some text in it</p>", 60)
	return []Failure{
		{
			Name: "handler error",
			Doc:  "<p>one</p><p>two</p><img src=\"x\"><p>three</p>",
			Build: func(dst io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(dst, lolhtml.OnElement("img", func(*lolhtml.Element) error {
					return ErrHandler
				}))
			},
		},
		{
			Name: "destination error",
			Doc:  "<p>one</p><p>two</p>",
			Build: func(io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(brokenSink{}, lolhtml.OnElement("p", func(*lolhtml.Element) error {
					return nil
				}))
			},
		},
		{
			Name:  "memory limit",
			Doc:   body,
			Chunk: 256,
			Build: func(dst io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(dst,
					lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: 512}),
					lolhtml.OnElement("p", func(e *lolhtml.Element) error {
						return e.SetAttribute("data-x", "1")
					}))
			},
		},
		{
			Name: "ambiguous tag",
			Doc:  "<p>one</p><select><xmp>x</xmp></select><p>two</p>",
			Build: func(dst io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(dst, lolhtml.WithStrict(true),
					lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil }))
			},
		},
		{
			Name:   "handler panic",
			Doc:    "<p>one</p><img src=\"x\">",
			Panics: true,
			Build: func(dst io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(dst, lolhtml.OnElement("img", func(*lolhtml.Element) error {
					panic("the handler panicked")
				}))
			},
		},
		{
			Name: "after Close",
			Doc:  "<p>one</p>",
			Build: func(dst io.Writer) (*lolhtml.Writer, error) {
				return lolhtml.NewWriter(dst, lolhtml.OnElement("p", func(*lolhtml.Element) error {
					return nil
				}))
			},
		},
	}
}

// Run drives one failure and records what every call returned.
func Run(f Failure) Outcome {
	out := Outcome{Failure: f.Name}
	var sink strings.Builder
	w, err := f.Build(&sink)
	if err != nil {
		out.FirstErr = err
		return out
	}

	feed := func(doc string) error {
		step := f.Chunk
		if step <= 0 {
			step = len(doc)
		}
		for i := 0; i < len(doc); i += step {
			end := i + step
			if end > len(doc) {
				end = len(doc)
			}
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				return err
			}
		}
		return nil
	}

	if f.Panics {
		func() {
			defer func() { out.Recovered = recover() }()
			out.FirstErr = feed(f.Doc)
		}()
	} else {
		out.FirstErr = feed(f.Doc)
	}

	if f.Name == "after Close" {
		// The one case where the first thing that happens is success.
		out.CloseErr = w.Close()
		_, out.LaterErr = w.Write([]byte("<p>late</p>"))
		out.Delivered = sink.String()
		out.Poisoned = errors.Is(out.LaterErr, lolhtml.ErrClosed)
		return out
	}

	_, out.LaterErr = w.Write([]byte("<p>late</p>"))
	out.CloseErr = w.Close()
	out.Delivered = sink.String()
	out.Poisoned = errors.Is(out.LaterErr, lolhtml.ErrPoisoned) && errors.Is(out.CloseErr, lolhtml.ErrPoisoned)
	out.CauseKept = sameCause(out.FirstErr, out.CloseErr) || (f.Panics && out.Recovered != nil)
	out.MidTag = endsMidTag(out.Delivered)
	return out
}

// sameCause reports whether the late error still reaches the first one's cause, which is
// what makes errors.Is worth calling on it.
func sameCause(first, late error) bool {
	if first == nil || late == nil {
		return false
	}
	for _, target := range []error{ErrHandler, ErrSink, lolhtml.ErrMemoryLimitExceeded, lolhtml.ErrAmbiguousTag} {
		if errors.Is(first, target) {
			return errors.Is(late, target)
		}
	}
	// A native error with no sentinel: compare the messages, which is what a caller
	// would have to do too.
	return strings.Contains(late.Error(), first.Error())
}

func endsMidTag(s string) bool {
	open := strings.LastIndexByte(s, '<')
	close := strings.LastIndexByte(s, '>')
	return open > close
}

// Report is every outcome.
type Report struct{ Outcomes []Outcome }

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-18s %-27s %-20s %-20s %s\n",
		"failure", "first Write", "later Write", "Close", "delivered")
	for _, o := range r.Outcomes {
		fmt.Fprintf(&b, "%-18s %-27s %-20s %-20s %s\n",
			o.Failure, describe(o.FirstErr, o.Recovered), describe(o.LaterErr, nil),
			describe(o.CloseErr, nil), delivered(o))
	}
	return b.String()
}

func describe(err error, recovered any) string {
	if recovered != nil {
		return fmt.Sprintf("panic: %v", recovered)
	}
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, lolhtml.ErrClosed):
		return "ErrClosed"
	case errors.Is(err, lolhtml.ErrPoisoned):
		return "ErrPoisoned + cause"
	case errors.Is(err, lolhtml.ErrMemoryLimitExceeded):
		return "ErrMemoryLimitExceeded"
	case errors.Is(err, lolhtml.ErrAmbiguousTag):
		return "ErrAmbiguousTag"
	case errors.Is(err, ErrHandler):
		return "the handler's error"
	case errors.Is(err, ErrSink):
		return "the destination's error"
	}
	return firstLine(err)
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 24 {
		s = s[:21] + "..."
	}
	return s
}

func delivered(o Outcome) string {
	switch {
	case o.Delivered == "":
		return "nothing"
	case o.MidTag:
		return fmt.Sprintf("%d bytes, ending mid-tag", len(o.Delivered))
	default:
		return fmt.Sprintf("%d bytes, whole tokens", len(o.Delivered))
	}
}

// All runs every failure.
func All() Report {
	var r Report
	for _, f := range Failures() {
		r.Outcomes = append(r.Outcomes, Run(f))
	}
	return r
}

func main() {
	verbose := flag.Bool("v", false, "also print what each destination received")
	flag.Parse()

	r := All()
	fmt.Print(r)
	if *verbose {
		fmt.Println()
		for _, o := range r.Outcomes {
			fmt.Printf("%s:\n  %q\n", o.Failure, o.Delivered)
		}
	}
	// Every failure has to poison the writer, or the demonstration is wrong.
	for _, o := range r.Outcomes {
		if !o.Poisoned {
			fmt.Fprintf(os.Stderr, "poisoned: %s did not poison the writer\n", o.Failure)
			os.Exit(1)
		}
	}
}

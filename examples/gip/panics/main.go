// Command panics shows where a handler panic comes out, and what is left afterwards.
//
//	$ panics
//	handler                panics from   writer afterwards      library afterwards
//	element                Write         ErrPoisoned (bare)     usable
//	text                   Write         ErrPoisoned (bare)     usable
//	comment                Write         ErrPoisoned (bare)     usable
//	doctype                Write         ErrPoisoned (bare)     usable
//	end tag                Write         ErrPoisoned (bare)     usable
//	stream func            Write         ErrPoisoned (bare)     usable
//	document end           Close         ErrClosed              usable
//	text, unclosed node    Close         ErrClosed              usable
//
// A panic in a handler does not unwind through Rust. It is caught at the boundary, the
// native resources are released, and it is re-raised on the goroutine that called Write
// or Close - which one depends on when the handler ran, and that is the part worth a
// table rather than a sentence.
//
// Two handlers can run inside Close: [lolhtml.OnDocumentEnd] always, and a text handler
// for the final chunk of a text node the document left open. So a caller that recovers
// around Write and not around Close has a gap, and a rewrite whose text handler can fail
// has to check Close's error as well - which is the ordinary advice for a different
// reason.
//
// Afterwards the Writer is poisoned with the bare sentinel - the panic went to the caller
// instead of becoming an error, so there is no cause to wrap - unless the panic came out
// of Close, in which case the Writer is *closed* rather than poisoned: Close marks it
// closed before it does anything, so a later Write reports [lolhtml.ErrClosed] and a
// later Close reports nil. Either way the library is unaffected, a new Writer works, and
// nothing leaks: the release happens on the way out of the boundary rather than in a
// deferred Close the panic skipped.
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

// Where a panic came out.
type Where int

const (
	FromWrite Where = iota
	FromClose
	NotAtAll
)

func (w Where) String() string {
	switch w {
	case FromWrite:
		return "Write"
	case FromClose:
		return "Close"
	}
	return "not at all"
}

// Case is one handler kind that panics.
type Case struct {
	Name string
	Doc  string
	Opts func() []lolhtml.Option
}

// Outcome is what happened to one case.
type Outcome struct {
	Name         string
	Where        Where
	Value        any
	Poisoned     bool
	Closed       bool
	BareSentinel bool
	StillUsable  bool
}

// Cases are the handler kinds, in the order the table prints them.
func Cases() []Case {
	return []Case{
		{"element", "<p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(*lolhtml.Element) error {
				panic("the element handler panicked")
			})}
		}},
		{"text", "<p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error {
				panic("the text handler panicked")
			})}
		}},
		{"comment", "<!--c-->", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				panic("the comment handler panicked")
			})}
		}},
		{"doctype", "<!DOCTYPE html><p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
				panic("the doctype handler panicked")
			})}
		}},
		{"end tag", "<p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					panic("the end-tag handler panicked")
				})
			})}
		}},
		{"stream func", "<p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(*lolhtml.Sink) error {
					panic("the stream function panicked")
				})
			})}
		}},
		{"document end", "<p>a</p>", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
				panic("the document-end handler panicked")
			})}
		}},
		{"text, unclosed node", "<p>a", func() []lolhtml.Option {
			// The final chunk of a text node the document left open arrives during
			// Close, so this handler panics from there rather than from Write.
			return []lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				if c.IsLastInTextNode() {
					panic("the text handler panicked at the end of the node")
				}
				return nil
			})}
		}},
	}
}

// Run drives one case, recovering around Write and around Close separately so the table
// can say which one it came out of.
func Run(c Case) Outcome {
	out := Outcome{Name: c.Name, Where: NotAtAll}
	w, err := lolhtml.NewWriter(io.Discard, c.Opts()...)
	if err != nil {
		return out
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				out.Where, out.Value = FromWrite, r
			}
		}()
		_, _ = w.Write([]byte(c.Doc))
	}()

	if out.Where == NotAtAll {
		func() {
			defer func() {
				if r := recover(); r != nil {
					out.Where, out.Value = FromClose, r
				}
			}()
			_ = w.Close()
		}()
	}

	// What the writer says now. A panic from Write poisons it; a panic from Close
	// leaves it closed, because Close marks the writer closed before it does anything
	// - so the two failures are told apart by ErrPoisoned against ErrClosed rather
	// than by the panic value.
	_, werr := w.Write([]byte("<p>late</p>"))
	cerr := w.Close()
	out.Poisoned = errors.Is(werr, lolhtml.ErrPoisoned) && errors.Is(cerr, lolhtml.ErrPoisoned)
	out.Closed = errors.Is(werr, lolhtml.ErrClosed)
	out.BareSentinel = werr == lolhtml.ErrPoisoned

	// And whether the library is still usable afterwards, which is the question a
	// caller who recovers actually has.
	if s, err := lolhtml.RewriteString("<p>after</p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetAttribute("ok", "1")
	})); err == nil && strings.Contains(s, `ok="1"`) {
		out.StillUsable = true
	}
	return out
}

// Report is the table.
type Report struct{ Outcomes []Outcome }

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %-13s %-22s %s\n", "handler", "panics from", "writer afterwards", "library afterwards")
	for _, o := range r.Outcomes {
		poison := "neither"
		switch {
		case o.Poisoned:
			poison = "ErrPoisoned"
			if o.BareSentinel {
				poison += " (bare)"
			}
		case o.Closed:
			poison = "ErrClosed"
		}
		usable := "unusable"
		if o.StillUsable {
			usable = "usable"
		}
		fmt.Fprintf(&b, "%-22s %-13s %-22s %s\n", o.Name, o.Where, poison, usable)
	}
	return b.String()
}

// All runs every case.
func All() Report {
	var r Report
	for _, c := range Cases() {
		r.Outcomes = append(r.Outcomes, Run(c))
	}
	return r
}

func main() {
	verbose := flag.Bool("v", false, "also print the panic value from each case")
	flag.Parse()

	r := All()
	fmt.Print(r)
	if *verbose {
		fmt.Println()
		for _, o := range r.Outcomes {
			fmt.Printf("%-22s %v\n", o.Name, o.Value)
		}
	}
	for _, o := range r.Outcomes {
		if o.Where == NotAtAll || !(o.Poisoned || o.Closed) || !o.StillUsable {
			fmt.Fprintf(os.Stderr, "panics: %s behaved unexpectedly: %+v\n", o.Name, o)
			os.Exit(1)
		}
	}
}

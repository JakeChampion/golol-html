// Command pipeline runs a document through two rewriters, the output of the first being the
// input of the second, and shows what that buys over doing both in one pass.
//
//	$ pipeline
//	input        <p>Hello world</p>
//	one pass     <p><span class="new">x</span>Hello world</p>
//	piped        <p><span class="new" data-seen="yes">x</span>Hello world</p>
//
//	the second stage matched markup the first stage produced, which one pass cannot
//
// A [lolhtml.Writer] is an io.Writer, so a rewriter can be another rewriter's destination.
// Nothing else is needed: no buffer, no temporary file, no second read of the input.
//
// # Why bother
//
// Because selectors match the document as it arrived, not as a handler left it. A handler that
// inserts <span class="new"> does not make a ".new" selector fire in the same pass - that is
// deliberate, and it is what stops a rewrite triggering itself. So acting on produced markup
// takes a second pass, and a pipeline is the cheapest kind: the stages run at the same time,
// each holding only what a rewriter holds.
//
//	                          allocations       document held
//	one pass, both handlers            831       no
//	piped, one handler each           1645       no
//	buffered, one handler each        1655       yes, all of it
//
// Two stages cost about twice one, which is the price of parsing twice and is the same
// whichever way the second pass is arranged. What the pipeline saves is the document: peak
// heap measured 2.8 MB piping 1 MB, 3.5 MB piping 4 MB and 3.5 MB piping 16 MB - flat, where
// buffering grows with the input.
//
// A pipeline is not a substitute for the buffered kind when the second pass needs something
// the first pass has to finish to know - a table of contents, a canonical URL derived from the
// body. Those need the whole document before the second pass starts, and that is what holding
// it is for.
//
// # Closing
//
// Close upstream first. Each stage's Close flushes what it still holds into the next one, so
// closing the downstream stage first shuts the door on the upstream's tail: measured on
// <p>a</p, the wrong order loses "</p" and the upstream's Close reports "writer is closed".
// The right order reports nil from both.
//
// # Errors
//
// A handler error in any stage reaches the caller through every stage above it, with its
// identity intact: errors.Is finds the downstream sentinel in what the upstream Write
// returned, and in what either Close returns. So a pipeline does not need its own error
// plumbing.
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

// Stage is one rewriter's worth of work, named for what it does.
type Stage struct {
	Name    string
	Options func() []lolhtml.Option
}

// Insert is the first stage: it puts a marked-up span at the start of every paragraph.
var Insert = Stage{"insert a span", func() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.Prepend(`<span class="new">x</span>`, lolhtml.HTML)
	})}
}}

// Annotate is the second stage: it acts on the markup the first stage produced, which is the
// thing one pass cannot do.
var Annotate = Stage{"annotate .new", func() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(".new", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-seen", "yes")
	})}
}}

// Pipe runs doc through stages in order, each stage's output feeding the next, and returns the
// final output. Nothing is buffered between them: the writers are chained, so a byte written
// at the top can reach the bottom before the next byte is read.
//
// The writers are closed in pipeline order, upstream first, because each Close flushes into
// the next stage. Closing the other way round shuts the door on the tail.
func Pipe(doc string, stages ...Stage) (string, error) {
	if len(stages) == 0 {
		return "", errors.New("pipeline: no stages")
	}

	var out strings.Builder
	// Build from the bottom up: the last stage writes to out, and each earlier stage
	// writes to the one after it.
	writers := make([]*lolhtml.Writer, len(stages))
	var dst io.Writer = &out
	for i := len(stages) - 1; i >= 0; i-- {
		w, err := lolhtml.NewWriter(dst, stages[i].Options()...)
		if err != nil {
			closeAll(writers)
			return "", fmt.Errorf("pipeline: stage %q: %w", stages[i].Name, err)
		}
		writers[i] = w
		dst = w
	}

	if _, err := writers[0].Write([]byte(doc)); err != nil {
		closeAll(writers)
		return out.String(), err
	}
	if err := closeAll(writers); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// closeAll closes the stages upstream first and returns the first error, having closed every
// stage regardless. A stage left open leaks until it is collected, and the tail of a pipeline
// half-closed is worse than either.
func closeAll(writers []*lolhtml.Writer) error {
	var first error
	for _, w := range writers {
		if w == nil {
			continue
		}
		if err := w.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OnePass runs every stage's handlers in a single rewriter, which is what a caller reaches for
// first and what does not work when one stage acts on another's output.
func OnePass(doc string, stages ...Stage) (string, error) {
	var opts []lolhtml.Option
	for _, s := range stages {
		opts = append(opts, s.Options()...)
	}
	return lolhtml.RewriteString(doc, opts...)
}

// ClosedInTheWrongOrder runs a two-stage pipeline closing the downstream stage first, which is
// the mistake this program exists partly to demonstrate. It returns the output and the error
// the upstream Close reported.
func ClosedInTheWrongOrder(doc string) (string, error) {
	var out strings.Builder
	down, err := lolhtml.NewWriter(&out, Annotate.Options()...)
	if err != nil {
		return "", err
	}
	up, err := lolhtml.NewWriter(down, Insert.Options()...)
	if err != nil {
		down.Close()
		return "", err
	}
	if _, err := up.Write([]byte(doc)); err != nil {
		up.Close()
		down.Close()
		return out.String(), err
	}
	// Backwards on purpose.
	if err := down.Close(); err != nil {
		up.Close()
		return out.String(), err
	}
	return out.String(), up.Close()
}

func main() {
	doc := flag.String("doc", `<p>Hello world</p>`, "the document to rewrite")
	wrong := flag.Bool("wrong-order", false, "also close the stages in the wrong order, and report it")
	flag.Parse()

	one, err := OnePass(*doc, Insert, Annotate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		os.Exit(1)
	}
	piped, err := Pipe(*doc, Insert, Annotate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		os.Exit(1)
	}

	fmt.Printf("%-12s %s\n", "input", *doc)
	fmt.Printf("%-12s %s\n", "one pass", one)
	fmt.Printf("%-12s %s\n", "piped", piped)
	fmt.Println()
	if piped == one {
		fmt.Println("the two agree, so this document has nothing the second stage could only see afterwards")
	} else {
		fmt.Println("the second stage matched markup the first stage produced, which one pass cannot")
	}

	if !*wrong {
		return
	}
	fmt.Println()
	for _, d := range []string{*doc, `<p>a</p`} {
		got, err := ClosedInTheWrongOrder(d)
		right, rerr := Pipe(d, Insert, Annotate)
		fmt.Printf("%-14q closed downstream first: %q (%v)\n", d, got, err)
		fmt.Printf("%-14s closed upstream first:   %q (%v)\n", "", right, rerr)
	}
}

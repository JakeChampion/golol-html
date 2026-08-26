// Command stopafter rewrites a document until it meets a marker and copies everything after it.
//
//	$ stopafter -marker no-rewrite-below page.html
//	stopped at a comment, 214 bytes rewritten, 200034 copied
//	  marker           <!-- no-rewrite-below -->
//	  resumed at       byte 214, the comment's own start
//
// examples/gip/headonly does this at the head/body boundary and B189 has the measurement. What is
// different here is that the marker can be any kind of unit, and where the rewrite resumes is not
// the same for all of them.
//
// # The resume offset is not the same for every kind of marker
//
// A handler that returns an error stops the rewrite, and what has reached the destination is what a
// fresh rewriter produces from that much input. Where that prefix ends is documented per handler
// kind, and the offset to resume from follows from it - except for text, where it does not follow
// the way it looks like it should:
//
//	marker            the prefix ends            resume at
//	a comment         before the comment         the comment's SourceLocation().Start
//	an element        before its start tag       the element's SourceLocation().Start
//	an end tag        before the end tag         the end tag's SourceLocation().Start
//	text              before that chunk          the last chunk's SourceLocation().End
//
// The first three read the same way: the stopping unit was not emitted, so resume where it begins.
// Text is the exception, because a text node arrives in several chunks and the earlier ones have
// already been written by the time the marker is recognised. A marker in text is only a position in
// the document once the node has been accumulated to
// [lolhtml.TextChunk.IsLastInTextNode] - and by then the prefix holds the whole node, so the
// resume point is its end. Using the node's start instead duplicates it: measured, on
// `<p>before STOP here</p><p>after</p>`, resuming at 3 rather than 19 emits the text twice.
//
// # A marker inside raw text is not a marker, and one path has to be told
//
// The bytes `<!-- no-rewrite-below -->` inside a script, a style, a textarea or a title are that
// element's text and not a comment, so a comment handler never sees them. Measured for all four.
// That is the right answer: a marker in a script is a string in a program.
//
// The text path does not get it for free. A document-level text handler is handed raw text like any
// other text - a text chunk does not name its element - so a marker written as a bare word inside a
// script stops the rewrite unless something says otherwise. What says otherwise is the handler
// order: a selector-associated text handler runs before the document-level one for the same chunk,
// whatever order they were registered in, so a handler on `script, style, textarea, title` can mark
// the chunk before the general one decides about it. This program does that, and counts those
// places so the report can say the marker was seen somewhere it does not count.
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

// Kind is what sort of unit carried the marker.
type Kind int

const (
	// NotFound means the rewrite ran to the end.
	NotFound Kind = iota
	Comment
	Element
	Text
)

func (k Kind) String() string {
	switch k {
	case Comment:
		return "a comment"
	case Element:
		return "an element"
	case Text:
		return "text"
	}
	return "nothing"
}

// errMarker stops the rewrite. A sentinel, so errors.Is finds it whether it comes back from Write
// or, wrapped in ErrPoisoned, from Close.
var errMarker = errors.New("the marker was reached")

// Result is what a run did.
type Result struct {
	Kind Kind
	// ResumeAt is the input offset the copy began at, and Rewritten and Copied the two halves
	// of the output.
	ResumeAt          int
	Rewritten, Copied int
	// InRawText counts places the marker's text appeared inside a raw-text element, where it
	// is not a marker at all.
	InRawText int
}

func (r Result) String() string {
	var b strings.Builder
	if r.Kind == NotFound {
		fmt.Fprintf(&b, "no marker, %d bytes rewritten\n", r.Rewritten)
	} else {
		fmt.Fprintf(&b, "stopped at %v, %d bytes rewritten, %d copied\n",
			r.Kind, r.Rewritten, r.Copied)
		where := "its own start"
		if r.Kind == Text {
			where = "the end of the text node, because the earlier chunks were " +
				"already written"
		}
		fmt.Fprintf(&b, "  %-16s byte %d, %s\n", "resumed at", r.ResumeAt, where)
	}
	if r.InRawText > 0 {
		fmt.Fprintf(&b, "  %-16s %d place%s inside a script, style, textarea or title, "+
			"where it is that element's text rather than a marker\n",
			"also seen", r.InRawText, plural(r.InRawText))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Rewrite rewrites doc into dst until it meets the marker, then copies the rest.
//
// The marker is looked for in a comment, in an element's attributes as data-<marker>, and in text.
// Whichever arrives first stops the rewrite.
func Rewrite(doc, marker string, dst io.Writer, opts ...lolhtml.Option) (Result, error) {
	var res Result
	if marker == "" {
		return res, errors.New("stopafter: no marker")
	}

	counted := &counting{w: dst}
	resume := -1
	kind := NotFound

	// One accumulator for the current text node, because a marker in text is only a position
	// in the document once the node is whole.
	var text strings.Builder
	// And whether that node is raw text, which a document-level text handler cannot tell on
	// its own: a text chunk does not name its element. A selector-associated text handler
	// runs before the document-level one for the same chunk, whatever order they were
	// registered in, so the one below can say where this chunk is.
	rawText := false

	all := append([]lolhtml.Option{}, opts...)
	all = append(all,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if !strings.Contains(c.Text(), marker) {
				return nil
			}
			// The prefix ends before the comment, so the comment's own bytes are
			// still to come.
			resume, kind = c.SourceLocation().Start, Comment
			return errMarker
		}),
		lolhtml.OnElement("[data-"+marker+"]", func(e *lolhtml.Element) error {
			resume, kind = e.SourceLocation().Start, Element
			return errMarker
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			raw := rawText
			text.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				return nil
			}
			whole := text.String()
			text.Reset()
			rawText = false
			// Raw text is that element's own text rather than the document's, so a
			// marker in it is a string in a program and not a position in a page.
			if raw || !strings.Contains(whole, marker) {
				return nil
			}
			// The prefix ends after this node, not before it: the earlier chunks
			// have been written already. So resume at the node's end.
			resume, kind = t.SourceLocation().End, Text
			return errMarker
		}),
		// Raw text is not markup, so a marker in it is not a marker. Counted rather than
		// acted on, because a document that hides its marker in a script would otherwise
		// be rewritten throughout with no complaint.
		lolhtml.OnText("script, style, textarea, title", func(t *lolhtml.TextChunk) error {
			rawText = true
			if strings.Contains(t.Text(), marker) {
				res.InRawText++
			}
			return nil
		}),
	)

	w, err := lolhtml.NewWriter(counted, all...)
	if err != nil {
		return res, err
	}
	_, writeErr := w.Write([]byte(doc))
	closeErr := w.Close()

	res.Kind = kind
	res.Rewritten = counted.n
	if kind == NotFound {
		if writeErr != nil {
			return res, writeErr
		}
		if closeErr != nil {
			return res, closeErr
		}
		return res, nil
	}
	if !errors.Is(writeErr, errMarker) && !errors.Is(closeErr, errMarker) {
		return res, fmt.Errorf("the marker was found at %d and the stop did not arrive: "+
			"write=%v close=%v", resume, writeErr, closeErr)
	}

	res.ResumeAt = resume
	n, err := io.Copy(dst, strings.NewReader(doc[resume:]))
	res.Copied = int(n)
	if err != nil {
		return res, fmt.Errorf("copying the rest: %w", err)
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

// mark is the rewrite, chosen so its effect is visible either side of the marker.
func mark() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	})
}

func main() {
	marker := flag.String("marker", "no-rewrite-below", "the marker to stop at")
	report := flag.Bool("report", true, "print what happened to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "stopafter:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}
	doc, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stopafter:", err)
		os.Exit(1)
	}

	res, err := Rewrite(string(doc), *marker, os.Stdout, mark())
	if err != nil {
		fmt.Fprintln(os.Stderr, "stopafter:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, res)
	}
}

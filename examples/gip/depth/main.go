// Command depth enforces a maximum element nesting depth and reports the
// deepest path it found.
//
// A depth budget is a tree question, and this library does not build a tree. It
// reports tokens: start tags, end tags, text. So the program keeps the stack of
// open elements itself, and the whole difficulty is that a stack driven by
// tokens is not the same as a stack driven by elements.
//
// HTML lets a document leave end tags out. In <ul><li>a<li>b</ul> the first item
// is closed by the second item's start tag, and there is no token that says so.
// A counter that only decrements on end tags reports that list as three deep
// where a browser has two, and the error accumulates: a page of forty implicit
// list items reports a depth of forty-one.
//
// So the stack pops on a start tag as well, following the specification's
// implied end tags. That is the part of a parser this program has to be, and it
// is written out in impliedlyClosedBy below rather than hidden, because the list
// is the program.
//
// End tags are the other half, and they arrive here through a detour. There is
// no top-level end-tag handler; the only way to see one is Element.OnEndTag,
// which fires against the tag that closed the element - so for <ul><li>a<li>b</ul>
// three handlers fire at the single </ul> token. The program registers one on
// every element and then deduplicates by source location, because what it wants
// is the token, and the token is what the source location identifies.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// ErrTooDeep is returned when the document exceeds the budget. The report is
// still filled in, up to the point where the rewrite stopped.
var ErrTooDeep = errors.New("depth: nesting budget exceeded")

// A Report is what the walk found.
type Report struct {
	// MaxDepth is the greatest number of simultaneously open elements.
	MaxDepth int
	// DeepestPath is the tag names of those elements, outermost first.
	DeepestPath []string
	// Elements is how many start tags were seen, which is the work done.
	Elements int
}

// Check reads the document and reports its nesting. If max is greater than zero
// and the document exceeds it, Check stops at the first element past the budget
// and returns ErrTooDeep along with the report describing that element's path.
//
// Depth counts the elements the source contains. It does not count the html,
// head and body a browser inserts around a fragment, because those are not in
// the document and a budget is usually about what the document says.
func Check(r io.Reader, max int) (Report, error) {
	var rep Report
	var open []string
	// seen holds the source offset of every end-tag token already applied. One
	// token reaches this program once per element it closes, and the stack must
	// move once.
	seen := map[int]bool{}
	// stop holds the error the handler returned, so the caller gets the message
	// with the path in it rather than the bare sentinel. The rewrite's own error
	// wraps this one, but it also wraps it in a description of the handler,
	// which is noise here: the program knows why it stopped.
	var stop error

	record := func() {
		if len(open) > rep.MaxDepth {
			rep.MaxDepth = len(open)
			rep.DeepestPath = append(rep.DeepestPath[:0:0], open...)
		}
	}

	handler := lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		tag := e.TagName()
		rep.Elements++

		// A start tag can close elements before it opens one.
		for len(open) > 0 && impliedlyClosedBy(open[len(open)-1], tag) {
			open = open[:len(open)-1]
		}

		// Void elements and self-closing foreign elements never go on the
		// stack: they have no content, so nothing can be inside them. They
		// still count towards the depth at their own position.
		if !e.CanHaveContent() {
			open = append(open, tag)
			record()
			depth := len(open)
			open = open[:len(open)-1]
			if max > 0 && depth > max {
				stop = tooDeep(&rep, max)
				return stop
			}
			return nil
		}

		open = append(open, tag)
		record()
		if max > 0 && len(open) > max {
			stop = tooDeep(&rep, max)
			return stop
		}

		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			at := t.SourceLocation().Start
			if seen[at] {
				return nil
			}
			seen[at] = true
			name := t.Name()
			// Pop to and including the innermost element of that name. An end
			// tag that matches nothing open is a stray tag and changes nothing.
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] == name {
					open = open[:i]
					return nil
				}
			}
			return nil
		})
	})

	rw, err := lolhtml.NewWriter(io.Discard, handler)
	if err != nil {
		return rep, err
	}
	if _, err := io.Copy(rw, r); err != nil {
		rw.Close()
		if stop != nil {
			return rep, stop
		}
		return rep, err
	}
	if err := rw.Close(); err != nil {
		if stop != nil {
			return rep, stop
		}
		return rep, err
	}
	return rep, nil
}

func tooDeep(rep *Report, max int) error {
	return fmt.Errorf("%w: %d deep at %s, budget is %d",
		ErrTooDeep, rep.MaxDepth, strings.Join(rep.DeepestPath, " > "), max)
}

// impliedlyClosedBy reports whether an open element named open is closed by a
// start tag named next, with no end tag in the source.
//
// These are the specification's rules, restricted to the ones that fire on a
// start tag. They are what makes the difference between counting elements and
// counting tags.
func impliedlyClosedBy(open, next string) bool {
	switch open {
	case "li":
		return next == "li"
	case "dd", "dt":
		return next == "dd" || next == "dt"
	case "td", "th":
		return next == "td" || next == "th" || next == "tr" ||
			isTableSection(next)
	case "tr":
		return next == "tr" || isTableSection(next)
	case "thead", "tbody", "tfoot":
		return isTableSection(next)
	case "option":
		return next == "option" || next == "optgroup"
	case "optgroup":
		return next == "optgroup"
	case "rt", "rp":
		return next == "rt" || next == "rp"
	case "p":
		return closesAParagraph[next]
	case "caption", "colgroup":
		return next == "tr" || isTableSection(next) || next == "caption" ||
			next == "colgroup"
	}
	return false
}

func isTableSection(tag string) bool {
	return tag == "thead" || tag == "tbody" || tag == "tfoot"
}

// closesAParagraph is the set of start tags that end an open <p>. A paragraph
// cannot contain flow content that is itself a block, so the parser closes it
// rather than nesting.
var closesAParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "details": true, "dialog": true, "dir": true, "div": true,
	"dl": true, "dt": true, "dd": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "hr": true, "li": true, "listing": true, "main": true,
	"menu": true, "nav": true, "ol": true, "p": true, "plaintext": true,
	"pre": true, "search": true, "section": true, "summary": true,
	"table": true, "ul": true, "xmp": true,
}

func main() {
	max := 0
	if len(os.Args) > 1 {
		n, err := strconv.Atoi(os.Args[1])
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "usage: depth [max-depth]")
			os.Exit(2)
		}
		max = n
	}
	rep, err := Check(os.Stdin, max)
	fmt.Printf("%d elements, deepest %d: %s\n",
		rep.Elements, rep.MaxDepth, strings.Join(rep.DeepestPath, " > "))
	if err != nil {
		fmt.Fprintln(os.Stderr, "depth:", err)
		os.Exit(1)
	}
}

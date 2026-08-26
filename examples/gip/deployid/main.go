// Command deployid echoes a deploy identifier from the environment into a meta tag, and says when
// it could not put it somewhere a browser will read.
//
//	$ DEPLOY_ID=d7f3a91 deployid page.html
//	<!doctype html><meta name="deploy-id" content="d7f3a91"><html><head>…
//
//	$ DEPLOY_ID=d7f3a91 deployid -report page.html
//	the meta is in the head
//	  anchor            before the first element, <html>
//	  head reached      yes
//
// A comment can go anywhere. A meta has to be in the head or a browser ignores it, and a rewriter
// cannot see the head that a parser will build - see examples/gip/buildinfo and B182. So the
// question is not where to put the tag but whether the place available is a place that counts.
//
// # The parser builds the head around the insertion, unless text got there first
//
// Inserting a bare meta before the first element is enough: a parser in its "before head" mode
// meets the meta, creates the head, and puts the meta in it. Wrapping the meta in <head> of your
// own is unnecessary, and harmless where the source has a head already, because a second head is
// a parse error and dropped. Measured against x/net/html:
//
//	document                                    the meta lands
//	<!doctype html><html><head>…                in the head
//	<!doctype html><p>x</p>                     in the head - the parser creates one
//	<!doctype html><html><body><p>x</p>…        in the head
//	<!doctype html><body><p>x</p>               in the head
//	<!doctype html><title>t</title><p>x</p>     in the head, before the title
//	<!doctype html><!-- c --><p>x</p>           in the head
//	<!doctype html>\n  <p>x</p>                 in the head
//	<!doctype html>text<p>x</p>                 in the *body*, where it is ignored
//	<!doctype html>&nbsp;<p>x</p>               in the body: nbsp is not whitespace
//	just text                                   in the body
//
// So the rule is: the meta reaches the head if nothing but the doctype, comments and whitespace
// precedes the insertion point. The two failures are the same failure - text before the first
// element ends the head, and a document that is only text has no first element at all.
//
// The non-breaking space is worth its own line. It looks like whitespace, it is not one of the
// five characters a parser skips before the head, and a template that indents its output with one
// by accident moves every meta tag into the body.
//
// # And that is detectable, in one pass, before the insertion
//
// Text arrives before the element that follows it, so a text handler knows whether the head is
// still open by the time the element handler runs. This program uses that: it counts
// non-whitespace text seen before the first element, inserts the meta anyway - a meta in the body
// is inert rather than harmful - and reports that a browser will ignore it. Reporting beats
// silence: a deploy id that is present in the source and invisible to the tooling that reads it is
// worse than one that is missing.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Placement is where the meta ended up, as far as a parser will be concerned.
type Placement int

const (
	// InHead is the only one that counts.
	InHead Placement = iota
	// InBody means text preceded the first element, so the head was closed before the
	// insertion.
	InBody
	// NoElement means the document has no element to insert before.
	NoElement
)

func (p Placement) String() string {
	switch p {
	case InHead:
		return "in the head"
	case InBody:
		return "in the body, where a browser ignores it"
	}
	return "at the end of a document with no elements, where a browser ignores it"
}

// Result is what a run did.
type Result struct {
	ID    string
	Name  string
	Where Placement
	// Anchor is the element the meta went before, empty when there was none.
	Anchor string
	// TextBefore is the non-whitespace text that closed the head, empty when none did.
	TextBefore string
}

// Meta is the tag this writes. The content is attribute-value source, so a value from the
// environment is escaped rather than trusted: a deploy id is not markup.
func (r Result) Meta() string {
	return fmt.Sprintf(`<meta name=%q content=%q>`, r.Name, lolhtml.EscapeAttribute(r.ID))
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the meta is %v\n", r.Where)
	switch r.Where {
	case InHead:
		fmt.Fprintf(&b, "  %-18s before the first element, <%s>\n", "anchor", r.Anchor)
		fmt.Fprintf(&b, "  %-18s yes\n", "head reached")
	case InBody:
		fmt.Fprintf(&b, "  %-18s before the first element, <%s>\n", "anchor", r.Anchor)
		fmt.Fprintf(&b, "  %-18s no: %q came before it, and text ends the head\n",
			"head reached", trim(r.TextBefore))
	default:
		fmt.Fprintf(&b, "  %-18s the document end: there is no element to be before\n",
			"anchor")
		fmt.Fprintf(&b, "  %-18s no\n", "head reached")
	}
	return b.String()
}

func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 30 {
		return s[:27] + "..."
	}
	return s
}

// isHTMLSpace reports whether r is one of the five characters an HTML parser treats as whitespace
// before the head. A non-breaking space is not one of them, which is the trap.
func isHTMLSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// blank reports whether s is whitespace as the parser counts it.
func blank(s string) bool {
	return strings.TrimFunc(s, isHTMLSpace) == ""
}

// Echo writes the deploy id into src as a meta tag.
func Echo(src io.Reader, dst io.Writer, id, name string) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("deployid: no deploy id")
	}
	if name == "" {
		return Result{}, fmt.Errorf("deployid: no meta name")
	}
	res := Result{ID: id, Name: name}

	placed := false
	w, err := lolhtml.NewWriter(dst,
		// Text arrives before the element that follows it, so this runs first and the
		// element handler can read what it recorded. A text chunk that is only
		// whitespace does not end the head; anything else does.
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if placed || res.TextBefore != "" {
				return nil
			}
			if s := t.Text(); !blank(s) {
				res.TextBefore = s
			}
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if placed {
				return nil
			}
			placed = true
			res.Anchor = e.TagName()
			res.Where = InHead
			if res.TextBefore != "" {
				res.Where = InBody
			}
			// A bare meta is enough: the parser creates the head around it when the
			// head is still open, and a <head> of our own would be dropped as a
			// duplicate when it is not.
			return e.Before(res.Meta(), lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if placed {
				return nil
			}
			res.Where = NoElement
			return d.Append(res.Meta(), lolhtml.HTML)
		}))
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	return res, w.Close()
}

func main() {
	id := flag.String("id", os.Getenv("DEPLOY_ID"), "the deploy id, defaulting to $DEPLOY_ID")
	name := flag.String("name", "deploy-id", "the meta name")
	report := flag.Bool("report", false, "print where the meta went instead of the document")
	strict := flag.Bool("strict", false, "exit non-zero when the meta could not reach the head")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "deployid:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	dst := io.Writer(os.Stdout)
	var held strings.Builder
	if *report {
		dst = &held
	}
	res, err := Echo(src, dst, *id, *name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(res)
	}
	if *strict && res.Where != InHead {
		os.Exit(1)
	}
}

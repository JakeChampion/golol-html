// Command strictmode shows what each of the two parsing modes makes of a document.
//
//	$ strictmode < page.html
//	strict:      failed at byte 812: ambiguous <xmp> inside <select>
//	permissive:  completed; 41 elements, 6 text runs holding markup
//	  the missed region: bytes 812-864, "<script>alert(1)</script>"
//	  elements the permissive pass saw that the strict pass never reached: 12
//
// Strict mode is on by default and should stay on. It fails on a handful of
// non-conforming shapes - a text-content tag opening inside a <select>, or inside a
// <frameset> - where a streaming parser cannot tell whether what follows is markup or
// text. This program runs a document through both modes and prints the difference,
// which is the only honest way to decide what turning it off would cost on a
// particular corpus.
//
// # What permissive mode actually does
//
// Not silence, which is the thing worth measuring rather than assuming. Measured on
// <select><xmp><script>alert(1)</script></xmp></select><p>after</p>:
//
//	element handlers    select, xmp and p all fire; script does not
//	text handlers       the script's source arrives as text, in chunks
//	the output          identical to the input
//
// So the ambiguous element itself is an element, the document after a closed ambiguous
// tag is markup as usual, and what is missed is markup that arrives as text. That is
// what this program reports: not "handlers did not run" but which elements the two
// passes disagree about, and which runs of text hold something that looks like markup.
//
// # The practical answer for a rewrite that cannot use strict mode
//
// Refuse on the text. A run of text holding "<script" or "<img" is the signal, and
// returning an error from a text handler stops the document - with the caveat that
// stopping is not atomic, so the caller has to hold its output. -refuse does that here
// and reports what it would have refused.
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

// Suspicious are the sequences that mean a run of text is markup somebody meant.
var Suspicious = []string{"<script", "<img", "<iframe", "<svg", "<object", "<embed",
	"<link", "<style", "<form", "<input", "onerror=", "onload=", "javascript:"}

// Pass is what one mode made of the document.
type Pass struct {
	Strict   bool
	Err      error
	Elements []string
	TextRuns []Run
	Output   int // bytes that reached the destination
}

// Run is one span of text, with where it came from.
type Run struct {
	Text  string
	Start int
	End   int
}

// Suspicious reports the runs that hold something that looks like markup.
func (p Pass) Suspicious() []Run {
	var out []Run
	for _, r := range p.TextRuns {
		lower := strings.ToLower(r.Text)
		for _, s := range Suspicious {
			if strings.Contains(lower, s) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// Result is the comparison.
type Result struct {
	Strict     Pass
	Permissive Pass
	Ambiguous  bool     // strict mode refused the document
	OnlySeenBy []string // elements the permissive pass saw and the strict pass did not
	Refused    error    // what -refuse would have failed with
}

// OK reports whether the document is one both modes handle the same way.
func (r Result) OK() bool { return !r.Ambiguous && len(r.Permissive.Suspicious()) == 0 }

func (r Result) String() string {
	var b strings.Builder
	if r.Ambiguous {
		fmt.Fprintf(&b, "strict:      failed after %d bytes: %s\n", r.Strict.Output, firstLine(r.Strict.Err))
	} else if r.Strict.Err != nil {
		fmt.Fprintf(&b, "strict:      failed: %s\n", firstLine(r.Strict.Err))
	} else {
		fmt.Fprintf(&b, "strict:      completed; %d elements\n", len(r.Strict.Elements))
	}
	fmt.Fprintf(&b, "permissive:  ")
	if r.Permissive.Err != nil {
		fmt.Fprintf(&b, "failed: %s\n", firstLine(r.Permissive.Err))
	} else {
		fmt.Fprintf(&b, "completed; %d elements, %d text runs holding markup\n",
			len(r.Permissive.Elements), len(r.Permissive.Suspicious()))
	}
	for _, run := range r.Permissive.Suspicious() {
		fmt.Fprintf(&b, "  markup as text at bytes %d-%d: %q\n", run.Start, run.End, clip(run.Text))
	}
	if len(r.OnlySeenBy) > 0 {
		fmt.Fprintf(&b, "  elements only the permissive pass saw: %s\n", strings.Join(r.OnlySeenBy, " "))
	}
	if r.Refused != nil {
		fmt.Fprintf(&b, "  a text handler would refuse this document: %s\n", r.Refused)
	}
	return b.String()
}

func firstLine(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

// run makes one pass over the document.
func run(doc []byte, strict bool) Pass {
	p := Pass{Strict: strict}
	counted := &counter{}
	// A text node arrives in chunks, and the tokenizer splits it around a "<" that
	// does not begin a tag - which is exactly the character this program is looking
	// for. So the run is accumulated to the end of the node before it is examined:
	// per chunk, "<script" is never in one piece.
	var acc strings.Builder
	var start = -1
	w, err := lolhtml.NewWriter(counted,
		lolhtml.WithStrict(strict),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			p.Elements = append(p.Elements, e.TagName())
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			if start < 0 && l.Start != l.End {
				start = l.Start
			}
			acc.WriteString(c.Text())
			if !c.IsLastInTextNode() {
				return nil
			}
			text := acc.String()
			acc.Reset()
			from := start
			start = -1
			if strings.TrimSpace(text) != "" {
				p.TextRuns = append(p.TextRuns, Run{Text: text, Start: from, End: l.End})
			}
			return nil
		}))
	if err != nil {
		p.Err = err
		return p
	}
	if _, err := w.Write(doc); err != nil {
		p.Err = err
		w.Close()
		p.Output = counted.n
		return p
	}
	if err := w.Close(); err != nil {
		p.Err = err
	}
	p.Output = counted.n
	return p
}

type counter struct{ n int }

func (c *counter) Write(b []byte) (int, error) { c.n += len(b); return len(b), nil }

// Compare runs both modes and reports the difference. Neither pass writes a document:
// this program is a report, and the caller's choice of mode is the output.
func Compare(doc []byte, refuse bool) Result {
	var res Result
	res.Strict = run(doc, true)
	res.Permissive = run(doc, false)
	res.Ambiguous = errors.Is(res.Strict.Err, lolhtml.ErrAmbiguousTag)

	seen := map[string]bool{}
	for _, e := range res.Strict.Elements {
		seen[e] = true
	}
	for _, e := range res.Permissive.Elements {
		if !seen[e] {
			seen[e] = true
			res.OnlySeenBy = append(res.OnlySeenBy, e)
		}
	}

	if refuse {
		res.Refused = refuseOnText(doc)
	}
	return res
}

// ErrMarkupAsText is what the refusing pass fails with.
type ErrMarkupAsText struct {
	Text  string
	Start int
}

func (e ErrMarkupAsText) Error() string {
	return fmt.Sprintf("markup in a text run at byte %d: %q", e.Start, clip(e.Text))
}

// refuseOnText is the answer for a rewrite that cannot use strict mode: permissive
// parsing, and a text handler that refuses a run holding markup.
func refuseOnText(doc []byte) error {
	var acc strings.Builder
	start := -1
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.WithStrict(false),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			if start < 0 && l.Start != l.End {
				start = l.Start
			}
			acc.WriteString(c.Text())
			if !c.IsLastInTextNode() {
				return nil
			}
			text := acc.String()
			acc.Reset()
			from := start
			start = -1
			lower := strings.ToLower(text)
			for _, s := range Suspicious {
				if strings.Contains(lower, s) {
					return ErrMarkupAsText{Text: text, Start: from}
				}
			}
			return nil
		}))
	if err != nil {
		return err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return unwrapHandler(err)
	}
	if err := w.Close(); err != nil {
		return unwrapHandler(err)
	}
	return nil
}

// unwrapHandler digs the refusal out of the library's wrapping, which names the
// selector rather than the reason.
func unwrapHandler(err error) error {
	var e ErrMarkupAsText
	if errors.As(err, &e) {
		return e
	}
	return err
}

func main() {
	refuse := flag.Bool("refuse", false, "also try the text-handler refusal and report what it would stop")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "strictmode:", err)
		os.Exit(2)
	}
	res := Compare(doc, *refuse)
	fmt.Print(res)
	if !res.OK() {
		os.Exit(1)
	}
}

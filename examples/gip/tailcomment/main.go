// Command tailcomment emits a summary of what a rewrite changed as a trailing HTML comment, which
// is the shape a build stamp or a debug trace usually takes: invisible in the page, there in the
// source.
//
// A comment is the one place a summary cannot be escaped into. Comment.SetText refuses text that
// would end the comment early, and there is no escaping that would work - nothing inside a comment
// is a character reference, so "-->" has no spelling a comment can hold. But SetText is not the
// path this program uses. A trailing comment is appended, and DocumentEnd.Append takes markup, so
// the comment is built by hand and nothing guards it. That is what lolhtml.CheckComment is for.
//
// What happens without the check is not an error, which is the point:
//
//	summary          appended as <!--summary-->      what the document gets
//	3 changed        <!--3 changed-->                one comment
//	a-->b            <!--a-->b-->                    a comment "a", then "b-->" as text
//	>                <!-->-->                        an empty comment, then "-->" as text
//
// So a summary holding text from the document - a tag name, an attribute value, a URL - can put
// markup into the page it was describing. Nothing errors and the output parses.
//
// Since there is no escape, a caller has to choose, and this program makes the choice explicit
// rather than picking silently: Safe rewrites the offending sequences into ones that read the same
// to a person ("-->" becomes "- ->"), and Strict refuses to emit a comment at all and says why.
// Both are honest; a program that quietly dropped the summary would not be.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Summary is what the rewrite has to report.
type Summary struct {
	Changed map[string]int // attribute set, by element name
	Notes   []string       // free text, which is where document content gets in
}

// Text renders the summary as comment data. It is deliberately not comment-safe: what it renders
// depends on the document, and making it safe is the caller's decision.
func (s Summary) Text() string {
	names := make([]string, 0, len(s.Changed))
	for name := range s.Changed {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(" rewritten:")
	for _, name := range names {
		fmt.Fprintf(&b, " %s=%d", name, s.Changed[name])
	}
	for _, note := range s.Notes {
		fmt.Fprintf(&b, "; %s", note)
	}
	b.WriteString(" ")
	return b.String()
}

// Safe returns text that can be comment data, having changed the sequences that cannot appear in
// one. The result reads the same to a person and is not the same string, which is the trade a
// comment forces: "-->" becomes "- ->", "--!>" becomes "- -!>", and a leading ">" or "->" gets a
// space in front of it.
func Safe(text string) string {
	text = strings.ReplaceAll(text, "-->", "- ->")
	text = strings.ReplaceAll(text, "--!>", "- -!>")
	if strings.HasPrefix(text, ">") || strings.HasPrefix(text, "->") {
		text = " " + text
	}
	return text
}

// Mode is what to do when the summary cannot be comment data as written.
type Mode int

const (
	// ModeSafe rewrites the offending sequences and emits the comment.
	ModeSafe Mode = iota
	// ModeStrict emits no comment and returns the error, for a caller that would rather
	// know than have its summary altered.
	ModeStrict
)

// Run rewrites r into w, adding rel="noopener" to every target="_blank" anchor, and appends a
// summary of what it did as a trailing comment.
func Run(r io.Reader, w io.Writer, mode Mode) (Summary, error) {
	summary := Summary{Changed: map[string]int{}}

	rw, err := lolhtml.NewWriter(w, lolhtml.OnElement(`a[target="_blank"]`, func(e *lolhtml.Element) error {
		if _, ok := e.Attribute("rel"); ok {
			return nil
		}
		summary.Changed[e.TagName()]++
		// The href goes into the summary, which is how document content reaches comment
		// data. This is the line that makes the check necessary.
		if href, ok := e.Attribute("href"); ok && len(summary.Notes) < 3 {
			summary.Notes = append(summary.Notes, "first: "+href)
		}
		return e.SetAttribute("rel", "noopener")
	}))
	if err != nil {
		return summary, err
	}
	if _, err := io.Copy(rw, r); err != nil {
		return summary, err
	}
	// Close first: an error means the output is the early-stop prefix, and a summary of a
	// rewrite that did not finish would be describing a truncated document.
	if err := rw.Close(); err != nil {
		return summary, err
	}

	text := summary.Text()
	if err := lolhtml.CheckComment(text); err != nil {
		if mode == ModeStrict {
			return summary, fmt.Errorf("summary cannot be a comment: %w", err)
		}
		text = Safe(text)
		if err := lolhtml.CheckComment(text); err != nil {
			// Safe is supposed to fix every case the check refuses; if it ever does
			// not, emitting the comment anyway would put markup in the page.
			return summary, fmt.Errorf("summary still cannot be a comment after "+
				"rewriting it: %w", err)
		}
	}
	_, err = io.WriteString(w, "<!--"+text+"-->")
	return summary, err
}

func main() {
	mode := ModeSafe
	if len(os.Args) > 1 && os.Args[1] == "-strict" {
		mode = ModeStrict
	}
	summary, err := Run(os.Stdin, os.Stdout, mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tailcomment:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "tailcomment: %d anchors given rel=noopener\n", summary.Changed["a"])
}

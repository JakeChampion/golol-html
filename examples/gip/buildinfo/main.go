// Command buildinfo stamps a page with the commit that produced it, and reports where it managed
// to put the stamp.
//
//	$ buildinfo -commit 9f3a1c2 -built 2026-08-26T02:00:00Z page.html
//	<!doctype html><!-- build 9f3a1c2 2026-08-26T02:00:00Z --><html>…
//
//	$ buildinfo -report -commit 9f3a1c2 page.html
//	stamped at the top, before <html>
//	  already stamped   no
//	  anchor            the first element
//
// The value is known before the rewrite starts, which is the opposite of
// examples/gip/servertiming and makes the top of the document available. Available is not the
// same as guaranteed, and the difference is the whole program.
//
// # There is no guaranteed anchor at the top
//
// A rewriter reports the elements the source contains, not the ones a tree builder would add. So
// `<!doctype html><p>x</p>` reports one element, `p` - no html, no head, no body - and a rewrite
// that prepends to <head> does nothing at all on it. HTML permits all three tags to be left out,
// so this is not an edge case, it is most fragments and plenty of whole pages.
//
// [lolhtml.Doctype] cannot help either: it has Remove and no Before, After or Replace, because
// lol-html offers none. So the anchors, in the order this tries them:
//
//	the first element of any kind      Before it, which lands after any doctype
//	nothing else worked                the document end
//
// The second is not a fallback so much as the other half: a document with no elements at all -
// empty, only text, only a comment - has nowhere at the top to be.
//
// # The top costs idempotence, and the end buys it
//
// A page that goes through twice should not collect two stamps, and deciding that needs to know
// whether one is already there. A stamp already in the document could be anywhere in it, so at
// the first element the answer is not known yet - the evidence is later than the position.
//
// The end of the document is the other trade: by then every comment has gone past, so the stamp
// can be skipped if one was found, and the position is always available. What it costs is the
// stamp being at the bottom, where a human reading source will not see it and a byte-range fetch
// of the first kilobyte will not either.
//
// So -at=top is the default and it is not idempotent; -at=end is idempotent and puts the stamp
// where fewer readers look. The report says which happened, and running with -at=top twice says
// "already stamped: yes" and stamps it again anyway, because by the time it knew, the position was
// gone.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// marker is what makes a stamp recognisable as one on a later pass.
const marker = "build "

// Placement is where the stamp went.
type Placement int

const (
	// NotPlaced means nothing was written, which happens only when -at=end found an
	// existing stamp.
	NotPlaced Placement = iota
	// BeforeFirstElement is the top of the document, after any doctype.
	BeforeFirstElement
	// AtDocumentEnd is the end of the output.
	AtDocumentEnd
)

func (p Placement) String() string {
	switch p {
	case BeforeFirstElement:
		return "at the top, before the first element"
	case AtDocumentEnd:
		return "at the document end"
	}
	return "nowhere"
}

// Stamp is what a run did.
type Stamp struct {
	// Commit and Built are what the stamp says.
	Commit, Built string
	// Where it went, and whether the document already had one.
	Where   Placement
	Already bool
	// FirstElement is the element the stamp went before, empty when there was none.
	FirstElement string
}

// Comment is the stamp's markup. It carries no markup of its own, so nothing needs escaping - and
// what would need it is refused rather than escaped, because a commit hash containing a
// comment-closing sequence is not a commit hash.
func (s Stamp) Comment() string {
	text := marker + s.Commit
	if s.Built != "" {
		text += " " + s.Built
	}
	return "<!-- " + text + " -->"
}

// Valid reports whether the values can go in a comment at all.
func (s Stamp) Valid() error {
	for _, v := range []string{s.Commit, s.Built} {
		if strings.Contains(v, "--") || strings.Contains(v, ">") ||
			strings.Contains(v, "<") {
			return fmt.Errorf("buildinfo: %q cannot go in a comment", v)
		}
	}
	if s.Commit == "" {
		return fmt.Errorf("buildinfo: no commit")
	}
	return nil
}

func (s Stamp) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "stamped %v\n", s.Where)
	fmt.Fprintf(&b, "  %-18s %s\n", "already stamped", yesNo(s.Already))
	switch s.Where {
	case BeforeFirstElement:
		fmt.Fprintf(&b, "  %-18s the first element, <%s>\n", "anchor", s.FirstElement)
	case AtDocumentEnd:
		fmt.Fprintf(&b, "  %-18s the document end, which is always available\n", "anchor")
	default:
		fmt.Fprintf(&b, "  %-18s none needed: the document was already stamped\n", "anchor")
	}
	return b.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// AtTop stamps before the first element, which is where a reader looks and where the answer to
// "is it already stamped" is not yet known.
func AtTop(src io.Reader, dst io.Writer, s Stamp) (Stamp, error) {
	if err := s.Valid(); err != nil {
		return s, err
	}
	done := false
	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			// Recorded rather than acted on: by the time this runs on an existing
			// stamp, the position at the top may already be behind the rewriter.
			if strings.Contains(c.Text(), marker) {
				s.Already = true
			}
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if done {
				return nil
			}
			done = true
			s.Where = BeforeFirstElement
			s.FirstElement = e.TagName()
			return e.Before(s.Comment(), lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if done {
				return nil
			}
			// No element in the source, so there is no top to be at.
			s.Where = AtDocumentEnd
			return d.Append(s.Comment(), lolhtml.HTML)
		}))
	if err != nil {
		return s, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return s, err
	}
	return s, w.Close()
}

// AtEnd stamps at the document end, which is always available and is late enough to know whether
// the document was already stamped.
func AtEnd(src io.Reader, dst io.Writer, s Stamp) (Stamp, error) {
	if err := s.Valid(); err != nil {
		return s, err
	}
	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if strings.Contains(c.Text(), marker) {
				s.Already = true
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if s.Already {
				s.Where = NotPlaced
				return nil
			}
			s.Where = AtDocumentEnd
			return d.Append(s.Comment(), lolhtml.HTML)
		}))
	if err != nil {
		return s, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return s, err
	}
	return s, w.Close()
}

func main() {
	commit := flag.String("commit", os.Getenv("GIT_COMMIT"), "the commit that produced the page")
	built := flag.String("built", os.Getenv("BUILD_TIME"), "when it was built")
	at := flag.String("at", "top", `"top" or "end"`)
	report := flag.Bool("report", false, "print where the stamp went instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "buildinfo:", err)
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

	stamp := Stamp{Commit: *commit, Built: *built}
	var err error
	switch *at {
	case "top":
		stamp, err = AtTop(src, dst, stamp)
	case "end":
		stamp, err = AtEnd(src, dst, stamp)
	default:
		err = fmt.Errorf("buildinfo: -at is %q, want top or end", *at)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(stamp)
	}
}

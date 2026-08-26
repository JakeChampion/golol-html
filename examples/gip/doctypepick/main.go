// Command doctypepick chooses what a rewrite does from the document's doctype, and only from a
// doctype the document's own parser will honour.
//
//	$ doctypepick page.html
//	standards mode: <!doctype html>
//	  handlers          the standards set
//	  the doctype       counted: nothing but whitespace and comments came before it
//
//	$ doctypepick legacy.html
//	quirks mode: a doctype arrived after text, which a parser ignores
//	  handlers          the quirks set
//	  the doctype       not counted: "some text" came first
//
// # A doctype the rewriter reports is not always a doctype
//
// [lolhtml.OnDoctype] fires for every doctype token in the source, and a parser honours only the
// first one, and only when nothing but whitespace and comments has come before it. Everything else
// is a parse error and dropped. Measured against x/net/html, using the one behaviour that differs
// between the modes - a table start tag closes an open paragraph in standards mode and not in
// quirks:
//
//	document begins with          OnDoctype reports   the parser is in
//	<!doctype html>                          "html"   standards mode
//	whitespace, then a doctype               "html"   standards mode
//	a comment, then a doctype                "html"   standards mode
//	text, then a doctype                     "html"   *quirks* mode
//	an element, then a doctype               "html"   *quirks* mode
//	a non-breaking space, then one           "html"   *quirks* mode
//	nothing                                      ""   quirks mode
//
// Three rows where the handler fires and the mode is not what it says. So a rewrite that chooses
// standards-mode behaviour on the strength of the handler alone is wrong on those three, silently -
// and the shapes are not exotic: a stray byte-order mark, a templating comment that emitted a
// newline as an entity, a header written before the doctype.
//
// The condition is the same one examples/gip/deployid measures for a meta tag reaching the head:
// only the five ASCII whitespace characters count as whitespace. A non-breaking space is text, and
// text ends the chance of a doctype counting.
//
// # Choosing handlers needs no peek
//
// The handler set is fixed when the [lolhtml.Writer] is built, which is before any input has been
// read, so choosing by doctype looks like it needs a first look at the input. Two ways, and the
// obvious one is not better:
//
//	peek 512 bytes, then build the writer     3.002ms
//	register both sets and gate them          2.959ms
//
// Over a 87 KB document, fastest of twenty. The peek parses its prefix twice and buys nothing - and
// it can miss the doctype altogether, because a doctype can be arbitrarily far in: after a
// 5000-byte comment it ended at byte 5022, and after two hundred comments at byte 2015. A peek is a
// bound on correctness for no gain in speed.
//
// So this registers both sets and gates them on the doctype, which is one pass and cannot miss.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Mode is what the document's parser will be in.
type Mode int

const (
	// Quirks is the default: no doctype, a legacy one, or one a parser ignores.
	Quirks Mode = iota
	// Standards is <!doctype html> with nothing but whitespace and comments before it.
	Standards
)

func (m Mode) String() string {
	if m == Standards {
		return "standards mode"
	}
	return "quirks mode"
}

// Result is what a run decided and why.
type Result struct {
	Mode Mode
	// Doctype is the doctype the rewriter reported, empty when there was none.
	Doctype string
	// Counted is whether a parser will honour it.
	Counted bool
	// Disqualifier is what came before it, empty when nothing did.
	Disqualifier string
	// Matches is how many elements the chosen handlers acted on, so the report says the
	// choice had an effect.
	Matches int
}

func (r Result) String() string {
	var b strings.Builder
	switch {
	case r.Doctype == "":
		fmt.Fprintf(&b, "%v: no doctype\n", r.Mode)
	case r.Counted:
		fmt.Fprintf(&b, "%v: <!doctype %s>\n", r.Mode, r.Doctype)
	default:
		fmt.Fprintf(&b, "%v: a doctype arrived after %s, which a parser ignores\n",
			r.Mode, r.Disqualifier)
	}
	set := "the quirks set"
	if r.Mode == Standards {
		set = "the standards set"
	}
	fmt.Fprintf(&b, "  %-18s %s, %d match%s\n", "handlers", set, r.Matches,
		map[bool]string{true: "", false: "es"}[r.Matches == 1])
	switch {
	case r.Doctype == "":
		fmt.Fprintf(&b, "  %-18s none, so quirks mode\n", "the doctype")
	case r.Counted:
		fmt.Fprintf(&b, "  %-18s counted: nothing but whitespace and comments came "+
			"before it\n", "the doctype")
	default:
		fmt.Fprintf(&b, "  %-18s not counted: %s came first\n", "the doctype",
			r.Disqualifier)
	}
	return b.String()
}

// isHTMLSpace reports whether r is one of the five characters a parser skips before a doctype. A
// non-breaking space is not one of them.
func isHTMLSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// blank reports whether s is whitespace as a parser counts it.
func blank(s string) bool { return strings.TrimFunc(s, isHTMLSpace) == "" }

// Rewrite rewrites src into dst, choosing between two handler sets by the document's mode.
//
// Both sets are registered and gated rather than chosen after a peek: the peek is no faster, and a
// doctype can be far enough into a document that a bounded peek misses it.
func Rewrite(src io.Reader, dst io.Writer) (Result, error) {
	var res Result
	decided := false

	// What disqualifies a later doctype. Whitespace and comments do not; anything else does,
	// and once something has, no doctype counts.
	disqualify := func(what string) {
		if res.Disqualifier == "" {
			res.Disqualifier = what
		}
	}

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			if decided {
				// A second doctype is a parse error and dropped, so the first
				// answer stands.
				return nil
			}
			decided = true
			name, _ := d.Name()
			res.Doctype = name
			public, _ := d.PublicID()
			system, _ := d.SystemID()
			res.Counted = res.Disqualifier == ""
			// Standards mode is <!doctype html> and nothing else: a public or
			// system identifier makes it a legacy declaration.
			if res.Counted && strings.EqualFold(name, "html") && public == "" && system == "" {
				res.Mode = Standards
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if !blank(t.Text()) {
				disqualify(fmt.Sprintf("%q", trim(t.Text())))
			}
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			disqualify("<" + e.TagName() + ">")

			// The handlers themselves. Both sets are registered here and the mode
			// decides which runs, which is the whole design: one pass, no peek.
			if !applies(e) {
				return nil
			}
			res.Matches++
			if res.Mode == Standards {
				return standards(e)
			}
			return quirks(e)
		}),
	)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	return res, w.Close()
}

// trim shortens a disqualifying string for the report.
func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 24 {
		return s[:21] + "..."
	}
	return s
}

// applies says which elements either set acts on, so the two are comparable and the match count
// means the same thing whichever ran. B174 is why a table is the interesting element: a table start
// tag closes an open paragraph in standards mode and not in quirks, so a wrapper inserted inside a
// paragraph lands beside it in one mode and inside it in the other.
func applies(e *lolhtml.Element) bool { return e.TagName() == "table" }

// standards is the rewrite for a document a parser will read in standards mode.
func standards(e *lolhtml.Element) error { return e.SetAttribute("data-mode", "standards") }

// quirks is the rewrite for quirks mode, where the same wrapper would land inside the paragraph.
func quirks(e *lolhtml.Element) error { return e.SetAttribute("data-mode", "quirks") }

func main() {
	report := flag.Bool("report", true, "print the decision to stderr")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "doctypepick:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	res, err := Rewrite(src, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doctypepick:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Fprint(os.Stderr, res)
	}
}

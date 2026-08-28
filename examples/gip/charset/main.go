// Command charset makes sure a document declares the encoding it is actually in.
//
//	charset -encoding windows-1252 page.html
//	<meta charset="windows-1252">
//
// The declaration has to name the encoding of the bytes, and this program cannot
// discover that: nothing is sniffed, the rewriter is told an encoding and takes
// it as fact, and a document's own meta is ordinary markup to it. So -encoding is
// both what the rewriter is told and what gets declared, which keeps the two from
// disagreeing - the failure this program exists to avoid is a page whose bytes are
// one encoding and whose meta says another, because every reader believes the
// meta.
//
// An existing declaration is honoured rather than replaced. If it already names
// the right encoding there is nothing to do; if it names a different one, that is
// a contradiction this program will not resolve by guessing, so it reports it and
// changes nothing. Deciding which of the two is right needs to know where the
// bytes came from, which is the caller's knowledge.
//
// The declaration goes as early as the head allows. A browser stops looking after
// the first 1024 bytes, so a charset meta after a long block of inline CSS is not
// a declaration, it is decoration - and this program reports the distance rather
// than pretending the position does not matter.
//
// Wanting that position is what makes this two passes. The earliest place to
// insert is the start of the head, and whether to insert at all depends on
// whether a declaration appears later in that same head. A rewriter cannot know
// that yet: an insertion can only go where it has not been. So the document is
// read once to find any existing declaration and rewritten once to add one - the
// first version of this prepended at the head's start tag and then met the
// page's own meta, producing two declarations, which is the exact failure it
// exists to prevent.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// prescanLimit is how far a browser reads looking for a charset declaration.
const prescanLimit = 1024

type fixer struct {
	encoding string // what the bytes are in, and what to declare
	force    bool   // replace a declaration that names something else

	added    int
	replaced int
	passes   int
	skipped  map[string]int

	// found is the declaration the document already had, and where.
	found    string
	foundAt  int
	haveMeta bool
}

func (f *fixer) note(reason string) {
	if f.skipped == nil {
		f.skipped = map[string]int{}
	}
	f.skipped[reason]++
}

func defaults() *fixer { return &fixer{encoding: "utf-8"} }

func (f *fixer) validate() error {
	if f.encoding == "" {
		return fmt.Errorf("-encoding cannot be empty: the declaration has to name " +
			"the encoding the bytes are in")
	}
	// The label has to be one the rewriter accepts, or the document would be
	// read as something else and the declaration would be a second lie.
	//
	// The Writer built to ask the question is closed, even though nothing is
	// written to it: NewWriter has already allocated the native rewriter by the
	// time it returns, and the library asks that every Writer be closed, including
	// one being abandoned. The runtime cleanup that would otherwise free it is a
	// leak guard rather than the supported path, and it stops working entirely for
	// a Writer any handler has captured. A server validating a label per request
	// would hold one unfreed rewriter per request without this line.
	w, err := lolhtml.NewWriter(io.Discard, lolhtml.WithEncoding(f.encoding))
	if err != nil {
		return fmt.Errorf("-encoding %q: %w", f.encoding, err)
	}
	w.Close()
	return nil
}

// readPass finds any existing charset declaration and where it is. It writes
// nothing.
func (f *fixer) readPass(doc []byte) error {
	f.passes++
	_, err := lolhtml.Rewrite(doc,
		lolhtml.WithEncoding(f.encoding),
		// Both spellings: <meta charset> and the http-equiv form, which is what
		// older pages use and what a rewrite has to recognise to avoid adding a
		// second declaration beside it.
		lolhtml.OnElement(`meta[charset], meta[http-equiv]`, func(e *lolhtml.Element) error {
			label, ok := charsetOf(e)
			if !ok {
				return nil
			}
			if f.haveMeta {
				f.note("a second charset declaration was left alone; a browser uses the first")
				return nil
			}
			f.haveMeta = true
			f.found = label
			f.foundAt = e.SourceLocation().Start
			return nil
		}))
	return err
}

// writeOptions is the writing pass, decided from what the reading pass found.
func (f *fixer) writeOptions() []lolhtml.Option {
	sawHead := false
	placed := false

	opts := []lolhtml.Option{lolhtml.WithEncoding(f.encoding)}

	if f.haveMeta {
		switch {
		case strings.EqualFold(f.found, f.encoding):
			f.note("the page already declares " + f.encoding)
		case f.force:
			opts = append(opts, lolhtml.OnElement(`meta[charset], meta[http-equiv]`,
				func(e *lolhtml.Element) error {
					if placed {
						return nil
					}
					if _, ok := charsetOf(e); !ok {
						return nil
					}
					placed = true
					f.replaced++
					return f.rewrite(e)
				}))
		default:
			f.note(fmt.Sprintf("the page declares %q and the bytes are %q; one of "+
				"the two is wrong and this program will not guess - use -force to "+
				"declare %s", f.found, f.encoding, f.encoding))
		}
		if f.foundAt >= prescanLimit {
			f.note(fmt.Sprintf("the declaration is at byte %d, past the %d a browser "+
				"reads, so it may not be seen at all", f.foundAt, prescanLimit))
		}
		return opts
	}

	return append(opts,
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if placed || !e.CanHaveContent() {
				return nil
			}
			// Prepend, not append: the declaration has to be inside the first
			// 1024 bytes a browser reads, and the start of the head is the
			// earliest position available.
			placed = true
			f.added++
			return e.Prepend(f.markup(), lolhtml.HTML)
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if sawHead || placed {
				return nil
			}
			placed = true
			f.added++
			return e.Before(f.markup(), lolhtml.HTML)
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if !placed {
				f.note("no head and no body to declare the encoding in")
			}
			return nil
		}))
}

// charsetOf reads the encoding a meta declares, in either spelling. The second
// result is false for a meta that is not a charset declaration at all.
func charsetOf(e *lolhtml.Element) (string, bool) {
	if v, ok := e.Attribute("charset"); ok {
		if label := strings.TrimSpace(decoded(v)); label != "" {
			return label, true
		}
		return "", false
	}
	equiv := strings.ToLower(strings.TrimSpace(decoded(attr(e, "http-equiv"))))
	if equiv != "content-type" {
		return "", false
	}
	// content="text/html; charset=windows-1252"
	content := decoded(attr(e, "content"))
	_, after, ok := strings.Cut(strings.ToLower(content), "charset=")
	if !ok {
		return "", false
	}
	label := strings.TrimSpace(after)
	if i := strings.IndexAny(label, "; \t"); i >= 0 {
		label = label[:i]
	}
	if label == "" {
		return "", false
	}
	return label, true
}

// rewrite replaces an existing declaration in place, in whichever spelling it
// used: turning an http-equiv into a short meta would change more than asked.
func (f *fixer) rewrite(e *lolhtml.Element) error {
	if _, ok := e.Attribute("charset"); ok {
		return e.SetAttribute("charset", f.encoding)
	}
	return e.SetAttribute("content", "text/html; charset="+f.encoding)
}

func (f *fixer) markup() string {
	return `<meta charset="` + lolhtml.EscapeAttribute(f.encoding) + `">`
}

func decoded(s string) string { return stdhtml.UnescapeString(s) }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (f *fixer) run(r io.Reader, w io.Writer) error {
	if err := f.validate(); err != nil {
		return err
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err := f.readPass(doc); err != nil {
		return err
	}

	// The document is read in the encoding it is declared to be in, which is the
	// same label that gets written. Telling the rewriter one thing and the reader
	// another is the bug this program is about.
	f.passes++
	out, err := lolhtml.NewWriter(w, f.writeOptions()...)
	if err != nil {
		return err
	}
	if _, err := out.Write(doc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fixString(in string, opts ...func(*fixer)) (string, *fixer, error) {
	f := defaults()
	for _, o := range opts {
		o(f)
	}
	var out bytes.Buffer
	err := f.run(strings.NewReader(in), &out)
	return out.String(), f, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (f *fixer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "passes=%d added=%d replaced=%d", f.passes, f.added, f.replaced)
	if f.haveMeta {
		fmt.Fprintf(&sb, " found=%q at=%d", f.found, f.foundAt)
	}
	sb.WriteString("\n")
	reasons := make([]string, 0, len(f.skipped))
	for r := range f.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, f.skipped[r])
	}
	return sb.String()
}

func main() {
	f := defaults()
	flag.StringVar(&f.encoding, "encoding", f.encoding,
		"the encoding the bytes are in; used to read the document and to declare it")
	flag.BoolVar(&f.force, "force", false,
		"replace a declaration that names a different encoding")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		file, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "charset:", err)
			os.Exit(1)
		}
		defer file.Close()
		r = file
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: charset [-encoding LABEL] [file.html]")
		os.Exit(2)
	}

	if err := f.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "charset:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, f.report())
}

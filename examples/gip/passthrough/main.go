// Command passthrough checks that a rewrite which changes nothing changes
// nothing.
//
// It sounds like a tautology and it is not. The rewriter does not copy bytes
// from input to output: it parses the document into tokens and writes them back
// out, so byte-identity is a claim about the serialiser agreeing with the parser
// on every shape of input - unusual attribute quoting, a bogus comment, a
// duplicate attribute, an unclosed tag at the end of the document. Anything the
// serialiser normalises would show up here as a difference, and a caller who
// registered a handler for one element would be silently rewriting the rest of
// the page.
//
// The check runs each document through several modes and several write patterns.
// The modes matter because "no handlers" is the easy case: with nothing
// registered the rewriter has less to do, and the interesting claim is that
// registering a handler that does nothing also changes nothing.
//
// Two known exceptions, both measured rather than assumed:
//
// A text handler decodes and re-encodes text. A byte that is not valid in the
// declared encoding then becomes U+FFFD on the way out, whether or not the
// handler touches it. With no text handler those bytes pass through untouched.
//
// Strict mode refuses a document whose parse is ambiguous rather than producing
// output at all, so those documents are checked with strict mode off, where the
// ambiguous region is passed through as text.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Case is one document in the corpus.
type Case struct {
	Name string
	Doc  string
	// TextHandlerChanges says this document is one of the exceptions: its bytes
	// are not valid in the declared encoding, so any text handler rewrites them.
	TextHandlerChanges bool
	// NeedsLenientMode says the document's parse is ambiguous, so strict mode
	// refuses it rather than passing it through.
	NeedsLenientMode bool
}

// A Mode is one way of running a document through the rewriter while asking it
// to change nothing.
type Mode struct {
	Name string
	Opts func() []lolhtml.Option
	// UsesTextHandler marks the modes that decode and re-encode text.
	UsesTextHandler bool
}

// Modes are the ways of doing nothing, from least to most registered.
func Modes() []Mode {
	return []Mode{
		{Name: "no handlers", Opts: func() []lolhtml.Option { return nil }},
		{Name: "a handler that never matches", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("no-such-element", func(*lolhtml.Element) error { return nil })}
		}},
		{Name: "an element handler", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(*lolhtml.Element) error { return nil })}
		}},
		{Name: "an element handler that reads", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				_ = e.TagName()
				for range e.Attributes() {
				}
				return nil
			})}
		}},
		{Name: "a comment handler", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return nil })}
		}},
		{Name: "a doctype handler", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDoctype(func(*lolhtml.Doctype) error { return nil })}
		}},
		{Name: "a document-end handler", Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return nil })}
		}},
		{Name: "a text handler", UsesTextHandler: true, Opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil })}
		}},
		{Name: "every handler", UsesTextHandler: true, Opts: func() []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("*", func(*lolhtml.Element) error { return nil }),
				lolhtml.OnText("*", func(*lolhtml.TextChunk) error { return nil }),
				lolhtml.OnComment("*", func(*lolhtml.Comment) error { return nil }),
				lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }),
				lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return nil }),
				lolhtml.OnDoctype(func(*lolhtml.Doctype) error { return nil }),
				lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return nil }),
			}
		}},
	}
}

// writeSizes are the input write patterns to try. Zero means one write.
var writeSizes = []int{0, 1, 3, 64, 4096}

// A Failure is one document that came out different from how it went in.
type Failure struct {
	Case  string
	Mode  string
	Write int
	Got   string
}

func (f Failure) String() string {
	return fmt.Sprintf("%s / %s / writes of %d: output differs (%q)",
		f.Case, f.Mode, f.Write, elide(f.Got))
}

// Check runs every case through every mode and write pattern and returns the
// differences, along with how many comparisons were made.
func Check(cases []Case) ([]Failure, int) {
	var failures []Failure
	checks := 0
	for _, c := range cases {
		for _, m := range Modes() {
			// The exception: a text handler rewrites bytes that are not valid
			// in the declared encoding, so this pairing is expected to differ
			// and is checked the other way round below.
			if c.TextHandlerChanges && m.UsesTextHandler {
				continue
			}
			for _, n := range writeSizes {
				opts := m.Opts()
				if c.NeedsLenientMode {
					opts = append(opts, lolhtml.WithStrict(false))
				}
				got, err := rewrite(c.Doc, n, opts)
				checks++
				if err != nil {
					failures = append(failures, Failure{c.Name, m.Name, n, "error: " + err.Error()})
					continue
				}
				if got != c.Doc {
					failures = append(failures, Failure{c.Name, m.Name, n, got})
				}
			}
		}
	}
	return failures, checks
}

// CheckExceptions runs the documents that are expected to change and reports the
// ones that did not - because an exception nobody can reproduce is a stale
// exception, and carrying it forward hides whatever it was protecting.
func CheckExceptions(cases []Case) []string {
	var stale []string
	for _, c := range cases {
		if !c.TextHandlerChanges {
			continue
		}
		got, err := rewrite(c.Doc, 0, []lolhtml.Option{
			lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }),
		})
		if err == nil && got == c.Doc {
			stale = append(stale, c.Name)
		}
	}
	return stale
}

func rewrite(doc string, writeSize int, opts []lolhtml.Option) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		return "", err
	}
	if writeSize <= 0 {
		if _, err := io.WriteString(w, doc); err != nil {
			w.Close()
			return "", err
		}
	} else {
		for i := 0; i < len(doc); i += writeSize {
			if _, err := io.WriteString(w, doc[i:min(i+writeSize, len(doc))]); err != nil {
				w.Close()
				return "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func elide(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[:60] + "..." + s[len(s)-60:]
}

func main() {
	cases := Corpus()
	failures, checks := Check(cases)
	stale := CheckExceptions(cases)

	fmt.Printf("%d documents, %d comparisons\n", len(cases), checks)
	for _, f := range failures {
		fmt.Println(" ", f)
	}
	for _, name := range stale {
		fmt.Printf("  %s is listed as an exception and did not differ\n", name)
	}
	if len(failures) > 0 || len(stale) > 0 {
		os.Exit(1)
	}
	fmt.Println("passthrough is byte-identical")
}

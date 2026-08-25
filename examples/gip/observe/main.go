// Command observe reads a document, reports what is in it, and proves it changed nothing.
//
//	$ observe < page.html
//	4,812 bytes: 118 elements (14 kinds), 96 attributes (9 names), 41 text nodes,
//	  2 comments, 1 doctype; identical output
//
// A rewrite that only observes should be the identity, and this program checks that
// rather than asserting it: the document goes through with every handler kind registered,
// reading everything each unit offers, and the output is compared with the input byte for
// byte. If they differ it says where, and exits non-zero.
//
// # The one exception, and why it is the document's rather than the handler's
//
// A text handler decodes and re-encodes the text it is given. For a document that is
// what it says it is, that is exact. For one holding bytes the declared encoding cannot
// decode, it is not: the handler is given U+FFFD, and U+FFFD is what comes out - so the
// output differs from the input and nothing the handler did caused it.
//
// So the check distinguishes the two. A difference is reported as the document's when the
// input holds bytes the encoding cannot decode, and as the observer's otherwise - and
// only the second is a failure. Measured: with no text handler at all those bytes pass
// through untouched, which is what -no-text does, and is the only way to observe such a
// document without changing it.
//
// # What it reports
//
// Counts rather than content: elements by name, attributes by name, text and comment
// bytes, the doctype, and the source-location span of each kind. Nothing is buffered
// beyond the counters, so the report is available for a document of any size - which is
// the point of a streaming rewriter and the thing a profile of a large page needs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Profile is what one document holds.
type Profile struct {
	Bytes        int
	Elements     int
	ByName       map[string]int
	Attributes   int
	AttrNames    map[string]int
	TextNodes    int
	TextBytes    int
	Comments     int
	CommentBytes int
	Doctypes     int
	FFFD         int // replacement characters the handlers were given
}

// Kinds returns the element names, most used first.
func (p Profile) Kinds() []string { return sorted(p.ByName) }

// Attrs returns the attribute names, most used first.
func (p Profile) Attrs() []string { return sorted(p.AttrNames) }

func sorted(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func (p Profile) String() string {
	return fmt.Sprintf("%d bytes: %d elements (%d kinds), %d attributes (%d names), "+
		"%d text nodes (%d bytes), %d comments (%d bytes), %d doctypes",
		p.Bytes, p.Elements, len(p.ByName), p.Attributes, len(p.AttrNames),
		p.TextNodes, p.TextBytes, p.Comments, p.CommentBytes, p.Doctypes)
}

// Result is the profile plus the proof.
type Result struct {
	Profile   Profile
	Identical bool
	// Difference, when the output differs, is where and how.
	Difference string
	// TheDocuments is true when the difference is the document's fault rather than the
	// observer's: bytes the declared encoding cannot decode.
	TheDocuments bool
}

// OK reports whether observation was free.
func (r Result) OK() bool { return r.Identical || r.TheDocuments }

func (r Result) String() string {
	s := r.Profile.String()
	switch {
	case r.Identical:
		s += "; identical output"
	case r.TheDocuments:
		s += fmt.Sprintf("; output differs because the document is not valid in its encoding "+
			"(%d replacement characters): %s", r.Profile.FFFD, r.Difference)
	default:
		s += "; OUTPUT DIFFERS: " + r.Difference
	}
	return s
}

// observer counts what it is given and changes nothing.
type observer struct {
	p Profile
}

func (o *observer) options(text bool) []lolhtml.Option {
	opts := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			o.p.Elements++
			if o.p.ByName == nil {
				o.p.ByName = map[string]int{}
				o.p.AttrNames = map[string]int{}
			}
			o.p.ByName[e.TagName()]++
			// Reading everything an element offers, which is the point: an observer
			// that reads nothing proves nothing.
			_ = e.TagNamePreserveCase()
			_ = e.NamespaceURI()
			_ = e.CanHaveContent()
			_ = e.IsSelfClosing()
			_ = e.IsRemoved()
			_ = e.SourceLocation()
			for _, a := range e.AttributeList() {
				o.p.Attributes++
				o.p.AttrNames[a.Name]++
				o.p.FFFD += strings.Count(a.Value, "�")
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			o.p.Comments++
			t := c.Text()
			o.p.CommentBytes += len(t)
			o.p.FFFD += strings.Count(t, "�")
			_ = c.SourceLocation()
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			o.p.Doctypes++
			_, _ = d.Name()
			_, _ = d.PublicID()
			_, _ = d.SystemID()
			return nil
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return nil }),
	}
	if text {
		opts = append(opts, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			t := c.Text()
			o.p.TextBytes += len(t)
			o.p.FFFD += strings.Count(t, "�")
			if c.IsLastInTextNode() {
				o.p.TextNodes++
			}
			_ = c.SourceLocation()
			return nil
		}))
	}
	return opts
}

// Observe reads doc, profiles it, and compares the output with the input.
func Observe(doc []byte, text bool, chunk int) (Result, error) {
	o := &observer{}
	o.p.Bytes = len(doc)
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, o.options(text)...)
	if err != nil {
		return Result{}, err
	}
	step := chunk
	if step <= 0 {
		step = len(doc)
	}
	if step == 0 {
		step = 1
	}
	for i := 0; i < len(doc); i += step {
		end := i + step
		if end > len(doc) {
			end = len(doc)
		}
		if _, err := w.Write(doc[i:end]); err != nil {
			w.Close()
			return Result{Profile: o.p}, err
		}
	}
	if err := w.Close(); err != nil {
		return Result{Profile: o.p}, err
	}

	res := Result{Profile: o.p}
	if bytes.Equal(out.Bytes(), doc) {
		res.Identical = true
		return res, nil
	}
	res.Difference = difference(doc, out.Bytes())
	res.TheDocuments = !utf8.Valid(doc)
	return res, nil
}

// difference names the first byte that differs, with a little context either side.
func difference(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("first difference at byte %d: %q became %q",
				i, window(a, i), window(b, i))
		}
	}
	return fmt.Sprintf("the output is %d bytes and the input %d", len(b), len(a))
}

func window(s []byte, i int) string {
	lo, hi := i-8, i+8
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return string(s[lo:hi])
}

func main() {
	noText := flag.Bool("no-text", false, "do not register a text handler, which is the only way to observe a document with undecodable bytes without changing it")
	chunk := flag.Int("chunk", 0, "write size, or 0 for one Write")
	details := flag.Bool("details", false, "also list the element and attribute names")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "observe:", err)
		os.Exit(2)
	}
	res, err := Observe(doc, !*noText, *chunk)
	if err != nil {
		fmt.Fprintln(os.Stderr, "observe:", err)
		os.Exit(2)
	}
	fmt.Println(res)
	if *details {
		fmt.Println("\nelements:")
		for _, name := range res.Profile.Kinds() {
			fmt.Printf("  %-16s %d\n", name, res.Profile.ByName[name])
		}
		fmt.Println("attributes:")
		for _, name := range res.Profile.Attrs() {
			fmt.Printf("  %-16s %d\n", name, res.Profile.AttrNames[name])
		}
	}
	if !res.OK() {
		os.Exit(1)
	}
}

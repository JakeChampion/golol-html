// Command split cuts a document into parts at a chosen heading level, and makes each part stand
// on its own.
//
//	$ split -level 2 -o out < book.html
//	3 parts, cut at <h2>
//	  out/part-1.html   1284 bytes   "Introduction"      reopened <html><body><article>
//	  out/part-2.html   4218 bytes   "The middle part"   reopened <html><body><article>
//	  out/part-3.html    902 bytes   "Afterwards"        reopened <html><body><article>
//
// # One rewriter, several destinations
//
// A rewriter writes to one destination, and a split needs several - so the destination is a
// writer that forwards to whichever part is current, and the handler that meets a heading tells
// it to move on. The rewriter never knows: from its side this is one document, which is what
// keeps the parse and the offsets right.
//
// # What makes a part stand on its own
//
// The tags that were open when the cut happened. A heading three levels inside
// <html><body><article> starts a part whose text is inside nothing at all unless those three are
// written again - so each part begins with the ancestors' start tags, as they appeared in the
// source, and ends with their end tags in reverse.
//
// The rewriter is what makes that possible: an element handler knows the tag and its attributes,
// and its end-tag handler is where the ancestor leaves the stack. Nothing else is needed - no
// tree, and no second pass.
//
// The reopened tags are the source's own, attributes included, because a part whose <article> has
// lost its class is a part that styles differently. What is not reproduced is anything the
// ancestors' *content* said before the cut - a part does not get the previous part's paragraphs -
// which is the point of cutting.
//
// # Where a cut is allowed
//
// At a heading of the chosen level, and nowhere else. A byte budget with -max moves the cut to
// the next heading after the budget is exceeded rather than cutting mid-element: a part that is
// larger than asked for is a nuisance, and a part that ends inside a tag is not a part.
//
// The budget is counted at the destination rather than at the input, because the output is what a
// caller is sizing - and the two differ whenever a handler edits anything.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Part is one piece of the split.
type Part struct {
	// Index is 1-based, which is what a filename wants.
	Index int
	// Title is the text of the heading that started the part, empty for the first part when
	// the document begins before any heading.
	Title string
	// Reopened is the ancestor chain written at the start of the part.
	Reopened []string
	// Bytes is how much reached this part, including the reopened tags.
	Bytes int

	buf strings.Builder
	// prefix is how many bytes the reopened ancestors took.
	prefix int
	// sawContent is set when text or a completed element has been written to this part.
	sawContent bool
}

// HasContent reports whether the part holds anything worth cutting off: some text, or an element
// that opened and closed inside it. A part holding only the tags that are still open around it is
// not a part, which is what stops a document beginning with a heading from producing an empty
// first one.
//
// Counting bytes instead does not work, and that is worth saying: the wrapper's own start tags
// are written to the part before the first heading arrives, so a byte count says a part holding
// "<body>" and nothing else has content.
func (p *Part) HasContent() bool { return p.sawContent }

// Content is what the part holds.
func (p *Part) Content() string { return p.buf.String() }

// Splitter routes a rewriter's output into parts.
type Splitter struct {
	// MaxBytes is a soft budget: a cut happens at the first allowed boundary after it is
	// exceeded. Zero means cut at every heading.
	MaxBytes int

	parts   []*Part
	current *Part
	// open is the stack of open element tags, as they appeared in the source, so a part can
	// reopen them.
	open []string
}

// NewSplitter starts a splitter with one part waiting.
func NewSplitter(maxBytes int) *Splitter {
	s := &Splitter{MaxBytes: maxBytes}
	s.start(nil)
	return s
}

// Write is the rewriter's destination: it forwards to the current part.
func (s *Splitter) Write(p []byte) (int, error) {
	s.current.buf.Write(p)
	s.current.Bytes += len(p)
	return len(p), nil
}

// start begins a new part, reopening the tags that are currently open.
func (s *Splitter) start(reopen []string) {
	p := &Part{Index: len(s.parts) + 1, Reopened: append([]string(nil), reopen...)}
	for _, tag := range reopen {
		p.buf.WriteString(tag)
		p.Bytes += len(tag)
	}
	p.prefix = p.Bytes
	s.parts = append(s.parts, p)
	s.current = p
}

// finish closes the current part by writing the end tags of everything still open, innermost
// first.
func (s *Splitter) finish() {
	for i := len(s.open) - 1; i >= 0; i-- {
		name := tagName(s.open[i])
		if name == "" {
			continue
		}
		s.current.buf.WriteString("</" + name + ">")
		s.current.Bytes += len(name) + 3
	}
}

// cut ends the current part and starts the next one with the same ancestors.
func (s *Splitter) cut() {
	s.finish()
	s.start(s.open)
}

// ready reports whether a cut is wanted here: always at a heading unless a budget says the part
// is not big enough yet.
func (s *Splitter) ready() bool {
	if s.MaxBytes <= 0 {
		return true
	}
	return s.current.Bytes >= s.MaxBytes
}

// Parts returns the parts, with the last one closed.
func (s *Splitter) Parts() []*Part {
	return s.parts
}

// tagName returns the element name from a start tag, or "" if it cannot be read. It is written by
// hand because the tag is one this program built from a name it was given, not something parsed
// out of a document.
func tagName(startTag string) string {
	if !strings.HasPrefix(startTag, "<") {
		return ""
	}
	end := strings.IndexAny(startTag[1:], " \t\n>/")
	if end < 0 {
		return ""
	}
	return startTag[1 : 1+end]
}

// Split cuts a document at headings of the given level.
func Split(r io.Reader, level int, maxBytes int) (*Splitter, error) {
	s := NewSplitter(maxBytes)
	heading := fmt.Sprintf("h%d", level)

	// title accumulates the heading's text, which is the part's name. A heading's text
	// arrives in chunks like any other, so it is accumulated to IsLastInTextNode.
	var title strings.Builder
	inHeading := 0

	handlers := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()

			if tag == heading {
				// The cut happens before the heading is written, so the heading
				// starts the next part rather than ending the previous one - and
				// only if the current part has something of its own, so a document
				// that begins with a heading does not produce a part holding
				// nothing but <html><body>.
				if s.current.HasContent() && s.ready() {
					s.cut()
				}
				inHeading++
				title.Reset()
				if e.CanHaveContent() {
					if err := e.OnEndTag(func(*lolhtml.EndTag) error {
						inHeading--
						s.current.Title = strings.TrimSpace(title.String())
						return nil
					}); err != nil {
						return err
					}
				}
				return nil
			}

			if !e.CanHaveContent() {
				return nil
			}
			// The source's own start tag, attributes included: a part whose article has
			// lost its class styles differently.
			s.open = append(s.open, sourceTag(e))
			depth := len(s.open)
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				if len(s.open) >= depth {
					s.open = s.open[:depth-1]
				}
				// An element that opened and closed inside this part is content,
				// which is how an image or a horizontal rule between headings keeps
				// its own part.
				s.current.sawContent = true
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if inHeading > 0 {
				title.WriteString(c.Text())
			}
			if strings.TrimSpace(c.Text()) != "" {
				s.current.sawContent = true
			}
			return nil
		}),
	}

	w, err := lolhtml.NewWriter(s, handlers...)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	s.finish()
	return s, nil
}

// sourceTag rebuilds an element's start tag from what the handler can see, so a part can reopen
// it.
//
// The attribute values are written with only the double quote escaped, which is the rule
// SetAttribute follows and the reason it is documented: a value from AttributeList is raw source,
// so "a&amp;b" is those seven characters and writing them back unchanged round-trips. Escaping it
// properly - which is what the first version of this did, with EscapeAttribute - produces
// class="a&amp;amp;b" in the reopened tag, and a part whose class is not the class it had.
//
// The quote is escaped because a value can contain one: an attribute written in single quotes in
// the source, as in title='say "hi"', has a raw double quote in its value, and that would end the
// attribute this function is writing.
//
// So a reopened tag preserves the attribute *values* rather than their spelling, and cannot do
// better: there is no way to write a literal double quote inside double quotes. A stylesheet and a
// parser see the same attributes either way, which is what the part needs.
func sourceTag(e *lolhtml.Element) string {
	var b strings.Builder
	b.WriteString("<" + e.TagName())
	for _, attr := range e.AttributeList() {
		b.WriteString(" " + attr.Name)
		if attr.Value == "" {
			continue
		}
		b.WriteString(`="` + strings.ReplaceAll(attr.Value, `"`, "&quot;") + `"`)
	}
	b.WriteString(">")
	return b.String()
}

// Report describes the split.
func Report(s *Splitter, level int, dir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d parts, cut at <h%d>\n", len(s.Parts()), level)
	for _, p := range s.Parts() {
		name := filepath.Join(dir, fmt.Sprintf("part-%d.html", p.Index))
		title := p.Title
		if title == "" {
			title = "(no heading)"
		}
		fmt.Fprintf(&b, "  %-22s %6d bytes   %-24q reopened %s\n",
			name, p.Bytes, title, strings.Join(p.Reopened, ""))
	}
	return b.String()
}

func main() {
	level := flag.Int("level", 2, "the heading level to cut at")
	maxBytes := flag.Int("max", 0, "cut only after a part reaches this many bytes")
	dir := flag.String("o", "", "write the parts to this directory instead of standard output")
	flag.Parse()

	if *level < 1 || *level > 6 {
		fmt.Fprintln(os.Stderr, "split: -level is a heading level, 1 to 6")
		os.Exit(2)
	}

	s, err := Split(os.Stdin, *level, *maxBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}

	if *dir == "" {
		for _, p := range s.Parts() {
			fmt.Printf("<!-- part %d: %s -->\n%s\n", p.Index, p.Title, p.Content())
		}
		fmt.Fprint(os.Stderr, Report(s, *level, "."))
		return
	}

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}
	for _, p := range s.Parts() {
		name := filepath.Join(*dir, fmt.Sprintf("part-%d.html", p.Index))
		if err := os.WriteFile(name, []byte(p.Content()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "split:", err)
			os.Exit(1)
		}
	}
	fmt.Fprint(os.Stderr, Report(s, *level, *dir))
}

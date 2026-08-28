// Command summary extracts a page's summary and stops reading as soon as it has
// one.
//
// A summary is the meta description if the page has one, and otherwise the first
// paragraph of its own content - not the cookie notice in the header and not the
// strapline in the nav. Both of those are ordinary work. The interesting part is
// stopping.
//
// A streaming rewriter has no "that is enough". The only way to stop it is to
// fail: a handler that returns an error ends the rewrite, and the error surfaces
// from the Write that was running. So the program returns a sentinel from the
// handler that completes the summary and treats it as success, which is worth
// being explicit about because it means the Writer is left poisoned and the
// output is truncated. For an extractor that writes to io.Discard and wants the
// text rather than the document, that is exactly right.
//
// What it buys is measurable, and it is not "reads only the summary". The
// rewriter is fed by a copy loop with a buffer, and it stops at the first write
// after the summary is complete - so the bytes read are one buffer more than the
// summary needed, not one byte. On a 200 KB page whose first paragraph ends at
// byte 400: 32 KB read with io.Copy's default buffer, 4 KB with a 4 KB one, and
// 200 KB if the abort is left out. The buffer size is the knob, and the program
// takes it as a parameter for that reason.
package main

import (
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// errEnough is the sentinel a handler returns to stop the rewrite. It never
// reaches the caller of Extract.
var errEnough = errors.New("summary: enough")

// ErrNoSummary is returned when the document has neither a meta description nor
// a paragraph of its own.
var ErrNoSummary = errors.New("summary: no description and no paragraph")

// boilerplate regions hold text that belongs to the site rather than the page.
var boilerplate = map[string]bool{
	"nav": true, "header": true, "footer": true, "aside": true,
}

// skipped regions hold content that is not prose.
var skipped = map[string]bool{
	"script": true, "style": true, "template": true, "noscript": true,
	"select": true, "option": true, "iframe": true, "title": true,
}

// A Result is the summary and what it cost to find it.
type Result struct {
	// Text is the summary.
	Text string
	// From says where it came from: "meta description" or "first paragraph".
	From string
	// Read is how many input bytes were consumed before the extractor stopped.
	//
	// There is no companion field for the size of the document, because this
	// program never learns it: it stops at the first write after the summary is
	// complete and the rest of the input is never read. Comparing Read against a
	// total is a job for whoever holds the document.
	Read int64
}

// String renders the result.
func (r Result) String() string {
	return fmt.Sprintf("%s (%s, read %d bytes)", r.Text, r.From, r.Read)
}

type extractor struct {
	summary string
	from    string

	para strings.Builder
	node strings.Builder

	inPara     bool
	skipDepth  int
	blockDepth int
}

// Extract reads a document and returns its summary, stopping as soon as it has
// one. bufSize is the copy buffer, which bounds how much more than the summary
// gets read; zero uses io.Copy's default.
func Extract(r io.Reader, bufSize int) (Result, error) {
	e := &extractor{}
	var read int64

	w, err := lolhtml.NewWriter(io.Discard, e.options()...)
	if err != nil {
		return Result{}, err
	}

	buf := make([]byte, 32*1024)
	if bufSize > 0 {
		buf = make([]byte, bufSize)
	}
	var stopped bool
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			read += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				if !errors.Is(werr, errEnough) {
					w.Close()
					return Result{}, werr
				}
				stopped = true
				break
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				w.Close()
				return Result{}, rerr
			}
			break
		}
	}
	// Close reports the poisoning from the deliberate stop, which is not a
	// failure here. Any other error is.
	if cerr := w.Close(); cerr != nil && !errors.Is(cerr, errEnough) && !stopped {
		return Result{}, cerr
	}

	if e.summary == "" {
		return Result{Read: read}, ErrNoSummary
	}
	return Result{Text: e.summary, From: e.from, Read: read}, nil
}

func (e *extractor) options() []lolhtml.Option {
	return []lolhtml.Option{
		// The meta description, if there is one. name is not an attribute whose
		// values a selector matches case-insensitively, so the comparison is in
		// Go.
		lolhtml.OnElement("meta[name][content]", func(el *lolhtml.Element) error {
			name, _ := el.Attribute("name")
			if !strings.EqualFold(strings.TrimSpace(name), "description") {
				return nil
			}
			content, _ := el.Attribute("content")
			text := collapse(stdhtml.UnescapeString(content))
			if text == "" {
				return nil
			}
			e.summary, e.from = text, "meta description"
			return errEnough
		}),

		lolhtml.OnElement("*", func(el *lolhtml.Element) error {
			tag := el.TagName()
			e.flushNode()

			if boilerplate[tag] || skipped[tag] {
				if !el.CanHaveContent() {
					return nil
				}
				e.skipDepth++
				return el.OnEndTag(func(*lolhtml.EndTag) error {
					// Lowered whatever the token is named. The name guard is
					// the right test for a handler writing at an end tag's
					// position and the wrong one for a counter: <option> is on
					// the skip list and its end tag is omissible, so
					// <option>One<option>Two</select> runs both handlers at
					// </select>, and comparing names there would leave the
					// counter raised and skip the rest of the document. The
					// handler runs once per element, so this stays balanced.
					e.skipDepth--
					return nil
				})
			}

			// A paragraph is closed by any element that cannot be inside one,
			// and there is no end tag in the source when that happens - so the
			// pending paragraph has to be finished here rather than waited for.
			// Without this, <p>One.<p>Two. reports the second paragraph.
			if e.inPara && closesAParagraph[tag] {
				if err := e.finishPara(); err != nil {
					return err
				}
			}

			if tag != "p" || e.skipDepth > 0 || !el.CanHaveContent() {
				return nil
			}
			e.inPara = true
			e.para.Reset()
			return el.OnEndTag(func(t *lolhtml.EndTag) error {
				e.flushNode()
				return e.finishPara()
			})
		}),

		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if e.skipDepth > 0 || !e.inPara {
				return nil
			}
			e.node.WriteString(t.Text())
			if t.IsLastInTextNode() {
				e.flushNode()
			}
			return nil
		}),

		// A paragraph that is never closed still ends at the document end, and
		// so does the search for one.
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			e.flushNode()
			if e.inPara {
				// Ignore the sentinel here: there is nothing left to stop.
				if err := e.finishPara(); err != nil && !errors.Is(err, errEnough) {
					return err
				}
			}
			return nil
		}),
	}
}

// flushNode decodes the accumulated text node and adds it to the paragraph.
// Decoding happens after accumulating, because a character reference can be
// split across chunks.
func (e *extractor) flushNode() {
	if e.node.Len() == 0 {
		return
	}
	if e.inPara {
		e.para.WriteString(stdhtml.UnescapeString(e.node.String()))
	}
	e.node.Reset()
}

// finishPara takes the accumulated paragraph if it has any text in it, and stops
// the rewrite if it does.
func (e *extractor) finishPara() error {
	text := collapse(e.para.String())
	e.para.Reset()
	e.inPara = false
	if text == "" {
		// An empty paragraph is not a summary; keep looking.
		return nil
	}
	e.summary, e.from = text, "first paragraph"
	return errEnough
}

// closesAParagraph is the set of start tags that end an open <p>, which is how a
// paragraph ends when the source does not close it.
var closesAParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"details": true, "div": true, "dl": true, "dt": true, "dd": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "header": true, "hgroup": true, "hr": true, "li": true,
	"main": true, "menu": true, "nav": true, "ol": true, "p": true, "pre": true,
	"search": true, "section": true, "summary": true, "table": true, "ul": true,
}

// collapse turns any run of whitespace into one space and trims the ends, which
// is what a renderer does and what a summary wants.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func main() {
	bufSize := 0
	if len(os.Args) > 1 {
		if _, err := fmt.Sscan(os.Args[1], &bufSize); err != nil || bufSize < 1 {
			fmt.Fprintln(os.Stderr, "usage: summary [read-buffer-bytes]")
			os.Exit(2)
		}
	}
	res, err := Extract(os.Stdin, bufSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, "summary:", err)
		os.Exit(1)
	}
	fmt.Println(res.Text)
	fmt.Fprintf(os.Stderr, "summary: from the %s, read %d bytes\n", res.From, res.Read)
}

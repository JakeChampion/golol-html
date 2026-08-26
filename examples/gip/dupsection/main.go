// Command dupsection duplicates a section of a document, renaming the ids in the copy, without
// ever holding the whole document. It is the streaming version of what a tree API does with a
// deep clone: a template block repeated per row, an A/B variant emitted beside the original, a
// print copy of an article.
//
// The trick is that the caller already has the bytes. A rewriter cannot hand back an element's
// source - it reports units, and reconstructing a section from those loses things: a stray end
// tag inside it reaches no handler at all, and touching an attribute re-serialises a start tag.
// But an element's SourceLocation is a byte range into the input, and that range does not move
// with the write pattern, so a caller feeding the rewriter can slice its own copy of the input at
// it. What it has to retain is the section, not the document:
//
//	on the section's start tag   remember Start, begin retaining input from there
//	on the section's end tag     the copy is retained[Start:End]
//	after the end tag            stop retaining, drop the buffer
//
// With one correction that a first attempt got wrong and the tests caught: a caller cannot drop
// its buffer just because no section is open. A start tag spans writes, and by the time the
// handler for it fires, its own first bytes are in an earlier write - fed three bytes at a time,
// the handler for `<div id=a>` at offset 0 fires while the caller is holding input from offset 9,
// and the section it is asked to copy begins before anything it kept. What is safe to drop is
// everything up to the end of the last unit any handler reported: tokens do not overlap, so no
// future unit can begin before that point. So retention between sections is bounded by the
// largest single token rather than by nothing.
//
// The end tag has to be checked by name before its End is used. An omitted end tag hands the
// handler an enclosing element's tag, and the arithmetic then measures to the end of that one -
// documented on Element.SourceLocation, and the reason both items of <ul><li>a<li>b</ul> would
// otherwise measure as reaching the end of the list.
//
// Peak retention is measured rather than asserted. With 512-byte reads and one 1016-byte section,
// documents of 5 KB, 41 KB and 801 KB retained 1072, 1504 and 1408 bytes at peak: the section
// plus a read or two, moving with where the read boundaries fell around it and not with the
// document. A section whose end tag never arrives is never duplicated, and its buffer goes at
// Close.
//
// The copy is rewritten in a second pass over those bytes, which is the two-pass pattern the
// SourceLocation documentation recommends: ids get a suffix, and same-document references to them
// - href="#id", and the for, aria-labelledby, aria-describedby and aria-controls attributes -
// are renamed with them, so the copy is self-consistent rather than a second element with the
// same id.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// idRefAttrs are the attributes whose value is an id or a list of them, so the copy's references
// point at the copy. href is handled separately, because only a same-document fragment counts.
var idRefAttrs = []string{"for", "aria-labelledby", "aria-describedby", "aria-controls"}

// Stats is what a caller wants to know about the run: how many sections were copied, and how much
// input had to be held to do it.
type Stats struct {
	Sections     int
	PeakRetained int
	Unfinished   int // sections whose end tag never arrived
}

// Duplicate copies r to w, following each element matching selector with a copy of itself whose
// ids have suffix appended. It retains only the section being copied.
func Duplicate(r io.Reader, w io.Writer, selector, suffix string, chunk int) (Stats, error) {
	var st Stats

	// retained holds input from base onwards, and is only fed while a section is open.
	var retained bytes.Buffer
	base := -1
	// open is the section in progress: its name, to guard the end tag, and its start offset.
	var openName string
	openStart := -1
	// consumed counts every byte handed to the rewriter, which is what SourceLocation is
	// relative to.
	consumed := 0
	// lastEnd is the end of the last unit any handler reported. Nothing later can begin
	// before it, so input before it can be dropped - and input after it cannot, because a
	// start tag still being parsed lives there.
	lastEnd := 0
	note := func(l lolhtml.SourceLocation) {
		if l.End > lastEnd {
			lastEnd = l.End
		}
	}

	rw, err := lolhtml.NewWriter(w,
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			note(tc.SourceLocation())
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			note(c.SourceLocation())
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			note(d.SourceLocation())
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			note(e.SourceLocation())
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				note(t.SourceLocation())
				return nil
			})
		}),
		lolhtml.OnElement(selector, func(e *lolhtml.Element) error {
			if openName != "" {
				// A match inside a match. Copying both would copy the inner one twice, so the
				// outer one wins and this is left alone.
				return nil
			}
			if !e.CanHaveContent() {
				// A void element has no end tag, so there is no extent to take and nothing
				// this program can copy.
				return nil
			}
			name := e.TagName()
			start := e.SourceLocation().Start
			openName, openStart = name, start
			st.Unfinished++

			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != name {
					// Not this element's end tag: an omitted end tag handed us an enclosing
					// element's, so this element ended implicitly and there is no extent to
					// take. Clearing the open state matters as much as not copying: leaving
					// it set would make every later match look nested and skip it too.
					openName, openStart = "", -1
					return nil
				}
				end := t.SourceLocation().End
				openName, openStart = "", -1
				st.Unfinished--

				if base < 0 || start < base || end > base+retained.Len() {
					// The section began before retention did, which happens only if a
					// handler fired for input this program never buffered. Refusing is
					// better than emitting a partial copy.
					return fmt.Errorf("dupsection: section %d..%d is outside the retained "+
						"range %d..%d", start, end, base, base+retained.Len())
				}
				section := retained.Bytes()[start-base : end-base]

				copied, err := renameIDs(section, suffix)
				if err != nil {
					return err
				}
				st.Sections++
				return t.After(string(copied), lolhtml.HTML)
			})
		}))
	if err != nil {
		return st, err
	}

	if chunk <= 0 {
		chunk = 32 * 1024
	}
	buf := make([]byte, chunk)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			// Retention has to cover this write before it is handed over, because the
			// handlers for it run inside Write and a section can start anywhere in it.
			if base < 0 {
				base = consumed
			}
			retained.Write(buf[:n])
			consumed += n

			if _, err := rw.Write(buf[:n]); err != nil {
				return st, err
			}
			if retained.Len() > st.PeakRetained {
				st.PeakRetained = retained.Len()
			}
			// Trim to the earliest offset that could still be needed: the open
			// section's start, or the end of the last unit reported, since a start tag
			// being parsed lives after that and its bytes have to survive.
			keepFrom := lastEnd
			if openName != "" && openStart < keepFrom {
				keepFrom = openStart
			}
			if keepFrom > base {
				kept := append([]byte(nil), retained.Bytes()[keepFrom-base:]...)
				retained.Reset()
				retained.Write(kept)
				base = keepFrom
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return st, readErr
		}
	}
	return st, rw.Close()
}

// renameIDs is the second pass, over the section's own bytes: every id gets the suffix, and every
// same-document reference to an id is renamed with it.
func renameIDs(section []byte, suffix string) ([]byte, error) {
	return lolhtml.Rewrite(section, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if id, ok := e.Attribute("id"); ok {
			if err := e.SetAttribute("id", id+suffix); err != nil {
				return err
			}
		}
		for _, name := range idRefAttrs {
			v, ok := e.Attribute(name)
			if !ok || strings.TrimSpace(v) == "" {
				continue
			}
			// These hold one id or a space-separated list of them.
			fields := strings.Fields(v)
			for i, f := range fields {
				fields[i] = f + suffix
			}
			if err := e.SetAttribute(name, strings.Join(fields, " ")); err != nil {
				return err
			}
		}
		// A same-document fragment, and only that: "#x" is renamed, "/page#x" is not,
		// because it points into another document where the copy does not exist.
		if href, ok := e.Attribute("href"); ok && strings.HasPrefix(href, "#") && len(href) > 1 {
			if err := e.SetAttribute("href", href+suffix); err != nil {
				return err
			}
		}
		return nil
	}))
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "dupsection: give a selector and a suffix, document on stdin")
		os.Exit(1)
	}
	st, err := Duplicate(os.Stdin, os.Stdout, os.Args[1], os.Args[2], 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dupsection:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%d sections copied, %d bytes retained at peak", st.Sections, st.PeakRetained)
	if st.Unfinished > 0 {
		fmt.Fprintf(os.Stderr, ", %d never finished", st.Unfinished)
	}
	fmt.Fprintln(os.Stderr)
}

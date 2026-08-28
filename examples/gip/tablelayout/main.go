// Command tablelayout converts a div-based page into the table markup an email client will
// render, and refuses the conversions whose result would depend on the document's doctype.
//
//	$ tablelayout < page.html
//	converted 6 rows and 14 columns
//	  quirks mode      no doctype: a table wrapper inside a <p> would stay in the paragraph
//	  refused          1 rows and 1 columns inside a <p>: converting them depends on the doctype
//	  spacers          3 empty cells given a non-breaking space
//
// Mail clients render tables and disagree about everything else, so a page laid out with divs
// has to become one laid out with tables. The conversion is a wrapper: a <table><tr><td> before
// the div, a </td></tr></table> after it, and the div itself removed with its content kept.
//
// # The one conversion this refuses
//
// A row inside a paragraph. Whether the wrapper ends up inside the paragraph or beside it
// depends on the document's mode, and the mode depends on the doctype - a table start tag closes
// an open <p> in a standards-mode document and not in a quirks-mode one. Measured, with
// x/net/html doing the parsing:
//
//	wrapper     no doctype (quirks)   <!doctype html>
//	<table>     stays in the <p>      leaves the <p>
//	<div>       leaves                leaves
//	<span>      stays                 stays
//
// So the same input produces two different trees depending on a doctype that email templates
// frequently lack, and a converter that silently picks one is a converter that works on the
// author's machine. This one reports the document's mode, refuses the conversion inside a
// paragraph, and says how many it refused. The differential suite has the matrix as a test.
//
// "Inside a paragraph" is a question about the wrapper's position, which is why the answer is
// not simply "was there a <p> start tag earlier". The wrapper goes in immediately before the
// row's start tag, so what matters is whether the paragraph is still open *there* - and most
// of the paragraphs in real markup are never closed by a </p> at all, but by the next block
// element starting. ClosesParagraph below is that list. Getting it wrong in either direction
// is a real cost: refuse too little and the wrapper moves under one of the two doctypes,
// refuse too much and a page whose first paragraph was unclosed converts nothing at all.
//
// # The empty-cell spacer
//
// An empty <td> collapses in several clients, so an empty cell gets a non-breaking space. It goes
// in with [lolhtml.Text] rather than as "&nbsp;", because the escaping is the library's job: a
// literal U+00A0 is what the client needs, and writing the entity would produce "&amp;nbsp;" if
// this program ever had to write it through a path that escapes.
//
// # What the report is for
//
// A conversion nobody can check is a conversion nobody should ship. The report says how many
// rows and columns were converted, how many were refused and why, how many spacers went in, and
// what the document's mode is - which is the fact that decides whether the refusals mattered.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Report is what the conversion did.
type Report struct {
	Rows    int
	Columns int
	// RefusedRows counts the rows that were left as they were because a table wrapper
	// inside a paragraph lands in a different place depending on the doctype.
	RefusedRows int
	// RefusedColumns counts the columns left alone for the same reason, and the ones
	// inside a refused row: a <td> whose <table> was never written is dropped by the
	// parser, so converting a column whose row was refused loses the cell.
	RefusedColumns int
	// Spacers counts the empty cells given a non-breaking space.
	Spacers int
	// Doctype is what the document declared, empty if it declared nothing.
	Doctype string
}

// Quirks reports whether the document has no doctype at all, which is the case where a table
// wrapper stays inside a paragraph. A legacy doctype is quirks too, but naming that reliably
// means implementing the specification's doctype table, and a converter that guesses about it
// would be worse than one that says it only checks for a missing one.
func (r Report) Quirks() bool { return r.Doctype == "" }

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "converted %d rows and %d columns\n", r.Rows, r.Columns)
	switch {
	case r.Doctype == "":
		fmt.Fprintf(&b, "  %-16s no doctype: a table wrapper inside a <p> would stay in the paragraph\n",
			"quirks mode")
	default:
		fmt.Fprintf(&b, "  %-16s %s\n", "doctype", r.Doctype)
	}
	if r.RefusedRows+r.RefusedColumns > 0 {
		fmt.Fprintf(&b, "  %-16s %d rows and %d columns inside a <p>: converting them depends on the doctype\n",
			"refused", r.RefusedRows, r.RefusedColumns)
	}
	if r.Spacers > 0 {
		fmt.Fprintf(&b, "  %-16s %d empty cells given a non-breaking space\n", "spacers", r.Spacers)
	}
	return b.String()
}

// NonBreakingSpace is what an empty cell gets. It is the character rather than the "&nbsp;"
// entity because the insertion goes in with lolhtml.Text, which escapes what it is given: the
// entity would arrive at the client as the six characters "&nbsp;".
const NonBreakingSpace = "\u00a0"

// ClosesParagraph are the start tags that close an open <p> without any end tag being
// written, which is the tree construction spec's "if the stack of open elements has a p
// element in button scope, then close a p element" step. A <p> and a <div> are both on it,
// which is why <p>text<div class="row"> has no paragraph open by the time the row's content
// is reached - and why a converter that only lowered its counter at </p> stopped converting
// for the rest of the document.
var ClosesParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "details": true, "dialog": true, "dir": true, "div": true,
	"dl": true, "dd": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "hr": true, "li": true, "listing": true, "main": true,
	"menu": true, "nav": true, "ol": true, "p": true, "plaintext": true,
	"pre": true, "search": true, "section": true, "summary": true,
	"table": true, "ul": true, "xmp": true,
}

// Selectors are what counts as a row and a column. They are flags because every framework spells
// them differently, and a converter that only knew one would be useless on the next page.
type Selectors struct {
	Row    string
	Column string
}

// DefaultSelectors are the Bootstrap-ish spellings, which is what most hand-written templates
// use.
var DefaultSelectors = Selectors{Row: "div.row", Column: `div[class^="col"]`}

// Convert rewrites r into w, turning rows and columns into tables and cells.
//
// The streaming path: NewWriter and io.Copy, with Close checked, because a mail-sending service
// converting templates on the way out is the case this is for.
func Convert(r io.Reader, w io.Writer, sel Selectors) (Report, error) {
	var report Report

	// inParagraph says whether a <p> is open here, because there is no selector for "not
	// inside a paragraph".
	//
	// A flag rather than a depth counter, and cleared by ClosesParagraph below as well as by
	// the end tag, because </p> is one of the end tags HTML lets a document leave out. A
	// counter raised at <p> and lowered in OnEndTag gets stuck raised on the first paragraph
	// that is closed implicitly - OnEndTag does not fire for an end tag that is not in the
	// source - and every conversion in the rest of the document is then refused for a
	// paragraph that a parser closed long ago.
	inParagraph := false
	// refusedRows is the nesting depth of rows that were refused, so their columns are
	// refused too. The <td> this program writes is only markup inside the <table> the row
	// would have opened; without it the parser drops the cell and its content merges into
	// whatever was around it.
	refusedRows := 0
	// cells is the stack of cells being built, so an empty one can be given a spacer at its
	// end tag - the only place where "was there any text" is a settled question.
	type cell struct{ sawText bool }
	var cells []*cell

	handlers := []lolhtml.Option{
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			name, _ := d.Name()
			if name == "" {
				return nil
			}
			report.Doctype = name
			if pub, ok := d.PublicID(); ok && pub != "" {
				report.Doctype += " PUBLIC " + pub
			}
			return nil
		}),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			inParagraph = true
			// Fires at this paragraph's end tag, whatever its name: an unclosed <p>
			// ends at its parent's end tag and reports that name. Either way the
			// paragraph is over.
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				inParagraph = false
				return nil
			})
		}),
		lolhtml.OnElement(sel.Row, func(e *lolhtml.Element) error {
			if inParagraph {
				// Refused: see the package comment. The div stays as it is, which at
				// least renders the same in every client that understands divs.
				//
				// The paragraph is still open at this point even when this element is
				// one that closes it - the wrapper goes in *before* this start tag,
				// where the <p> has not ended yet, and whether a <table> there closes
				// it is exactly the doctype question. That is why ClosesParagraph is
				// applied by a handler registered after this one.
				report.RefusedRows++
				refusedRows++
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					refusedRows--
					return nil
				})
			}
			report.Rows++
			return wrap(e, `<table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%"><tr>`,
				`</tr></table>`)
		}),
		lolhtml.OnElement(sel.Column, func(e *lolhtml.Element) error {
			if inParagraph || refusedRows > 0 {
				report.RefusedColumns++
				return nil
			}
			report.Columns++
			c := &cell{}
			cells = append(cells, c)
			if err := e.Before(`<td valign="top">`, lolhtml.HTML); err != nil {
				return err
			}
			if err := e.OnEndTag(func(end *lolhtml.EndTag) error {
				if len(cells) > 0 {
					cells = cells[:len(cells)-1]
				}
				// An empty cell collapses in several clients, so it gets a
				// non-breaking space - as text, which is the library's escaping
				// rather than an entity this program spells itself.
				if !c.sawText {
					report.Spacers++
					if err := end.Before(" ", lolhtml.Text); err != nil {
						return err
					}
				}
				return end.After(`</td>`, lolhtml.HTML)
			}); err != nil {
				return err
			}
			e.RemoveAndKeepContent()
			return nil
		}),
		// The implied </p>. Registered after the two handlers above so that for one
		// element they see the paragraph as it was before this start tag closed it,
		// which is where their insertions go.
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if tag == "p" {
				// On the list, but it closes a paragraph and opens one in the same
				// breath, and the handler above has already recorded the new one.
				return nil
			}
			if ClosesParagraph[tag] {
				inParagraph = false
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if strings.TrimSpace(c.Text()) == "" {
				return nil
			}
			// Any non-blank text marks every open cell as non-empty: a cell holding a
			// nested column that holds text is not empty either.
			for _, open := range cells {
				open.sawText = true
			}
			return nil
		}),
	}

	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return report, err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return report, err
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	return report, nil
}

// ConvertString is Convert over a string, which is what the tests use.
func ConvertString(doc string, sel Selectors) (string, Report, error) {
	var out strings.Builder
	report, err := Convert(strings.NewReader(doc), &out, sel)
	return out.String(), report, err
}

// wrap puts open before an element and close after it, and removes the element itself while
// keeping its content - which is the whole of "convert this div into that table".
//
// The two insertions are one call each rather than several: repeated Before calls come out in
// order and repeated After calls come out reversed, so assembling a wrapper from several calls
// on the same side produces it backwards. See the package documentation on two insertions of the
// same kind.
func wrap(e *lolhtml.Element, open, close string) error {
	if err := e.Before(open, lolhtml.HTML); err != nil {
		return err
	}
	if err := e.After(close, lolhtml.HTML); err != nil {
		return err
	}
	e.RemoveAndKeepContent()
	return nil
}

func main() {
	row := flag.String("row", DefaultSelectors.Row, "the selector for a row")
	column := flag.String("column", DefaultSelectors.Column, "the selector for a column")
	flag.Parse()

	report, err := Convert(os.Stdin, os.Stdout, Selectors{Row: *row, Column: *column})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tablelayout:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, report)

	// A refusal is not a failure - the document is still valid and still renders - but it is
	// the one thing a caller has to decide about, so it earns a non-zero status.
	if report.RefusedRows+report.RefusedColumns > 0 {
		os.Exit(1)
	}
}

// Command tablelayout converts a div-based page into the table markup an email client will
// render, and refuses the conversions whose result would depend on the document's doctype.
//
//	$ tablelayout < page.html
//	converted 6 rows and 14 columns
//	  quirks mode      no doctype: a table wrapper inside a <p> would stay in the paragraph
//	  refused          2 rows inside a <p>: converting them depends on the doctype
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
	// RefusedInParagraph counts the rows that were left as they were because a table
	// wrapper inside a paragraph lands in a different place depending on the doctype.
	RefusedInParagraph int
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
	if r.RefusedInParagraph > 0 {
		fmt.Fprintf(&b, "  %-16s %d rows inside a <p>: converting them depends on the doctype\n",
			"refused", r.RefusedInParagraph)
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

	// paragraphs counts the open <p> elements, because there is no selector for "not inside a
	// paragraph" and the depth is the only way to know.
	paragraphs := 0
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
			paragraphs++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				paragraphs--
				return nil
			})
		}),
		lolhtml.OnElement(sel.Row, func(e *lolhtml.Element) error {
			if paragraphs > 0 {
				// Refused: see the package comment. The div stays as it is, which at
				// least renders the same in every client that understands divs.
				report.RefusedInParagraph++
				return nil
			}
			report.Rows++
			return wrap(e, `<table role="presentation" border="0" cellpadding="0" cellspacing="0" width="100%"><tr>`,
				`</tr></table>`)
		}),
		lolhtml.OnElement(sel.Column, func(e *lolhtml.Element) error {
			if paragraphs > 0 {
				report.RefusedInParagraph++
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
	if report.RefusedInParagraph > 0 {
		os.Exit(1)
	}
}

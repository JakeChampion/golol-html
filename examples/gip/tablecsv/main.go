// Command tablecsv extracts every table in a document as CSV, expanding colspan
// and rowspan so every row has the same number of fields.
//
// A table is the hardest structure in HTML to read from a stream, for three
// reasons that have nothing to do with CSV.
//
// Cells and rows are usually written without end tags. <tr><td>a<td>b is two
// cells, and the second start tag is what ends the first - so a cell's content
// ends at a token the cell's own end-tag handler will not see. The program keeps
// the stack of open elements and applies the implied end tags, the same way
// examples/gip/markdown does.
//
// A table can contain things that are not in it. A parser moves content that
// cannot be inside a table to just before the table; there is no tree here, so
// that content is reported inside. Text between <table> and the first <tr> is the
// common case, and a program that collected "the table's text" would collect it.
// This one takes cell content from cells, which is the shape that avoids the
// question.
//
// And a grid is not a list of rows. colspan and rowspan mean a row's cells do not
// line up with its columns, and a rowspan from an earlier row occupies a column
// in this one without any cell being written for it. So the program fills a grid
// rather than appending fields, and carries the outstanding row spans forward.
package main

import (
	"encoding/csv"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Table is one table's grid, already expanded: every row has Columns fields.
type Table struct {
	Rows    [][]string
	Columns int
}

// spans records a rowspan still to be paid out, one entry per column it covers.
type span struct {
	rowsLeft int
	text     string
}

type extractor struct {
	tables []Table

	// open is the stack of element names, because the library reports tokens and
	// a grid needs structure.
	open []string

	// depth of the table being read, or 0 for none. Nested tables are counted
	// and the inner one is read as its own table.
	stack []*builder

	node strings.Builder
}

// A builder accumulates one table.
type builder struct {
	rows [][]string
	// row is the row being built, and col the next column to fill in it.
	row []string
	col int
	// carried holds the rowspans still outstanding, indexed by column.
	carried map[int]*span
	// cell is the text of the cell being read, and inCell whether there is one.
	cell    strings.Builder
	inCell  bool
	cellCol int
	// colspan and rowspan of the cell being read.
	colspan, rowspan int
	columns          int
}

// Extract reads a document and returns its tables.
func Extract(r io.Reader) ([]Table, error) {
	e := &extractor{}
	w, err := lolhtml.NewWriter(io.Discard, e.options()...)
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
	return e.tables, nil
}

func (e *extractor) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", e.element),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			e.node.WriteString(t.Text())
			if t.IsLastInTextNode() {
				e.flushNode()
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			e.flushNode()
			e.popTo(0)
			return nil
		}),
	}
}

func (e *extractor) element(el *lolhtml.Element) error {
	tag := el.TagName()
	e.flushNode()
	e.closeImplied(tag)

	switch tag {
	case "table":
		e.stack = append(e.stack, newBuilder())
	case "tr":
		if b := e.current(); b != nil {
			b.startRow()
		}
	case "td", "th":
		if b := e.current(); b != nil {
			b.startCell(spanOf(el, "colspan"), spanOf(el, "rowspan"))
		}
	}

	if !el.CanHaveContent() {
		return nil
	}
	e.open = append(e.open, tag)
	depth := len(e.open)
	return el.OnEndTag(func(*lolhtml.EndTag) error {
		e.flushNode()
		// The callback may be firing at an ancestor's end tag, so everything
		// from this depth in is ending.
		e.popTo(depth - 1)
		return nil
	})
}

// spanOf reads a colspan or rowspan, clamped the way browsers clamp them: a
// missing, unparseable or zero value is 1, and the maxima keep a malformed
// document from asking for a billion columns.
func spanOf(el *lolhtml.Element, name string) int {
	v, ok := el.Attribute(name)
	if !ok {
		return 1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 1
	}
	limit := 1000
	if name == "rowspan" {
		limit = 65534
	}
	return min(n, limit)
}

func (e *extractor) current() *builder {
	if len(e.stack) == 0 {
		return nil
	}
	return e.stack[len(e.stack)-1]
}

// closeImplied applies the implied end tags a start tag generates. A cell is
// closed by the next cell or row, a row by the next row or section.
func (e *extractor) closeImplied(next string) {
	switch next {
	case "td", "th":
		e.popThrough(set("td", "th"), set("tr", "table"))
	case "tr":
		e.popThrough(set("tr"), set("table", "tbody", "thead", "tfoot"))
	case "tbody", "thead", "tfoot":
		e.popThrough(set("tbody", "thead", "tfoot"), set("table"))
	}
}

func (e *extractor) popThrough(want, barrier map[string]bool) {
	for i := len(e.open) - 1; i >= 0; i-- {
		if want[e.open[i]] {
			e.popTo(i)
			return
		}
		if barrier[e.open[i]] {
			return
		}
	}
}

// popTo closes elements until the stack is n deep, finishing whatever each one
// was accumulating.
func (e *extractor) popTo(n int) {
	for len(e.open) > n {
		tag := e.open[len(e.open)-1]
		e.open = e.open[:len(e.open)-1]
		switch tag {
		case "td", "th":
			if b := e.current(); b != nil {
				b.endCell()
			}
		case "tr":
			if b := e.current(); b != nil {
				b.endRow()
			}
		case "table":
			if b := e.current(); b != nil {
				b.endCell()
				b.endRow()
				e.tables = append(e.tables, b.table())
				e.stack = e.stack[:len(e.stack)-1]
			}
		}
	}
}

// flushNode adds the accumulated text node to the cell being read, if any. Text
// outside a cell is dropped, which is where the fostered content goes.
func (e *extractor) flushNode() {
	if e.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(e.node.String())
	e.node.Reset()
	if b := e.current(); b != nil && b.inCell {
		b.cell.WriteString(s)
	}
}

func newBuilder() *builder {
	return &builder{carried: map[int]*span{}}
}

// startRow begins a row and pays out any rowspans that cover it.
func (b *builder) startRow() {
	b.endCell()
	b.endRow()
	b.row = nil
	b.col = 0
	b.fillCarried()
}

// fillCarried writes the cells that earlier rows reserved in this one.
func (b *builder) fillCarried() {
	for {
		s, ok := b.carried[b.col]
		if !ok {
			return
		}
		b.set(b.col, s.text)
		s.rowsLeft--
		if s.rowsLeft == 0 {
			delete(b.carried, b.col)
		}
		b.col++
	}
}

func (b *builder) startCell(colspan, rowspan int) {
	b.endCell()
	b.fillCarried()
	b.inCell = true
	b.cell.Reset()
	b.cellCol = b.col
	b.colspan, b.rowspan = colspan, rowspan
}

// endCell writes the cell being read across its colspan, and reserves its
// rowspan in later rows.
func (b *builder) endCell() {
	if !b.inCell {
		return
	}
	text := collapse(b.cell.String())
	b.cell.Reset()
	b.inCell = false

	for i := range b.colspan {
		col := b.cellCol + i
		b.set(col, text)
		if b.rowspan > 1 {
			b.carried[col] = &span{rowsLeft: b.rowspan - 1, text: text}
		}
	}
	b.col = b.cellCol + b.colspan
	b.fillCarried()
}

// set writes one field, growing the row as needed.
func (b *builder) set(col int, text string) {
	for len(b.row) <= col {
		b.row = append(b.row, "")
	}
	b.row[col] = text
	if col+1 > b.columns {
		b.columns = col + 1
	}
}

func (b *builder) endRow() {
	if b.row == nil {
		return
	}
	b.rows = append(b.rows, b.row)
	b.row = nil
	b.col = 0
}

// table pads every row to the widest, so the result is a rectangle.
func (b *builder) table() Table {
	for i := range b.rows {
		for len(b.rows[i]) < b.columns {
			b.rows[i] = append(b.rows[i], "")
		}
	}
	return Table{Rows: b.rows, Columns: b.columns}
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// collapse turns runs of whitespace into single spaces and trims the ends, which
// is what a cell's text means.
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
	tables, err := Extract(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tablecsv:", err)
		os.Exit(1)
	}
	w := csv.NewWriter(os.Stdout)
	for i, tbl := range tables {
		if i > 0 {
			// A blank line between tables, which csv.Writer will not write.
			w.Flush()
			fmt.Println()
		}
		if err := w.WriteAll(tbl.Rows); err != nil {
			fmt.Fprintln(os.Stderr, "tablecsv:", err)
			os.Exit(1)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintln(os.Stderr, "tablecsv:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "tablecsv: %d tables\n", len(tables))
}

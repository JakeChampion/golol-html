// Command tablejson converts each table in a document to JSON, one object per
// row, keyed by the header cells.
//
// Reading the grid is the same work as examples/gip/tablecsv - implied end tags
// for cells and rows, a grid rather than a list of rows so colspan and rowspan
// line up, and cell content taken from cells so that content a parser fosters
// out of the table does not become data. What is new is that JSON needs *names*,
// and a table is not obliged to provide usable ones.
//
// Which row is the header is a guess, and the program says which guess it made:
// the last row of a <thead> if there is one, otherwise the first row if every
// cell in it is a <th>, otherwise none - and with no header there are no keys, so
// the rows come out as arrays instead of objects. Inventing "column 1" for a
// table that has no header would produce JSON that looks authoritative and says
// nothing.
//
// Names collide, and that is the part worth writing carefully. A header cell with
// colspan=2 gives two columns the same name. Two <th> can hold the same text. An
// empty <th> gives no name at all. JSON has no answer for a duplicate key - one
// of them wins, silently, and which one depends on the reader - so the program
// makes the keys unique, keeps the original name in the report, and counts what
// it had to rename. A caller can then decide whether the table was worth reading.
package main

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Table is one table, converted.
type Table struct {
	// Keys are the column names, in order, after disambiguation. Empty when the
	// table has no header row.
	Keys []string `json:"keys,omitempty"`
	// Rows are the body rows as objects, when there are keys.
	Rows []map[string]string `json:"rows,omitempty"`
	// Arrays are the body rows as arrays, when there are not.
	Arrays [][]string `json:"arrays,omitempty"`
	// HeaderFrom says which guess produced the keys: "thead", "first row" or
	// "none".
	HeaderFrom string `json:"headerFrom"`
	// Renamed are the columns whose names had to be made unique, as
	// original -> final.
	Renamed map[string]string `json:"renamed,omitempty"`
}

// Convert reads a document and returns its tables.
func Convert(r io.Reader) ([]Table, error) {
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

// A grid is a table as read: rows of cells, each knowing whether it was a <th>.
type cell struct {
	text   string
	header bool
}

type grid struct {
	rows [][]cell
	// theadRows is how many of the leading rows came from a <thead>.
	theadRows int

	row     []cell
	col     int
	carried map[int]*span
	columns int

	buf              strings.Builder
	inCell           bool
	cellCol          int
	colspan, rowspan int
	isHeader         bool
	inHead           bool
}

type span struct {
	rowsLeft int
	c        cell
}

type extractor struct {
	tables []Table
	open   []string
	stack  []*grid
	node   strings.Builder
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
		e.stack = append(e.stack, &grid{carried: map[int]*span{}})
	case "thead":
		if g := e.current(); g != nil {
			g.inHead = true
		}
	case "tbody", "tfoot":
		if g := e.current(); g != nil {
			g.inHead = false
		}
	case "tr":
		if g := e.current(); g != nil {
			g.startRow()
		}
	case "td", "th":
		if g := e.current(); g != nil {
			g.startCell(spanOf(el, "colspan"), spanOf(el, "rowspan"), tag == "th")
		}
	}

	if !el.CanHaveContent() {
		return nil
	}
	e.open = append(e.open, tag)
	depth := len(e.open)
	return el.OnEndTag(func(*lolhtml.EndTag) error {
		e.flushNode()
		e.popTo(depth - 1)
		return nil
	})
}

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

func (e *extractor) current() *grid {
	if len(e.stack) == 0 {
		return nil
	}
	return e.stack[len(e.stack)-1]
}

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

func (e *extractor) popTo(n int) {
	for len(e.open) > n {
		tag := e.open[len(e.open)-1]
		e.open = e.open[:len(e.open)-1]
		g := e.current()
		if g == nil {
			continue
		}
		switch tag {
		case "td", "th":
			g.endCell()
		case "tr":
			g.endRow()
		case "thead":
			g.inHead = false
		case "table":
			g.endCell()
			g.endRow()
			e.tables = append(e.tables, g.convert())
			e.stack = e.stack[:len(e.stack)-1]
		}
	}
}

func (e *extractor) flushNode() {
	if e.node.Len() == 0 {
		return
	}
	s := stdhtml.UnescapeString(e.node.String())
	e.node.Reset()
	if g := e.current(); g != nil && g.inCell {
		g.buf.WriteString(s)
	}
}

func (g *grid) startRow() {
	g.endCell()
	g.endRow()
	g.row = nil
	g.col = 0
	g.fillCarried()
}

func (g *grid) fillCarried() {
	for {
		s, ok := g.carried[g.col]
		if !ok {
			return
		}
		g.set(g.col, s.c)
		s.rowsLeft--
		if s.rowsLeft == 0 {
			delete(g.carried, g.col)
		}
		g.col++
	}
}

func (g *grid) startCell(colspan, rowspan int, header bool) {
	g.endCell()
	g.fillCarried()
	g.inCell = true
	g.buf.Reset()
	g.cellCol = g.col
	g.colspan, g.rowspan, g.isHeader = colspan, rowspan, header
}

func (g *grid) endCell() {
	if !g.inCell {
		return
	}
	c := cell{text: collapse(g.buf.String()), header: g.isHeader}
	g.buf.Reset()
	g.inCell = false
	for i := range g.colspan {
		col := g.cellCol + i
		g.set(col, c)
		if g.rowspan > 1 {
			g.carried[col] = &span{rowsLeft: g.rowspan - 1, c: c}
		}
	}
	g.col = g.cellCol + g.colspan
	g.fillCarried()
}

func (g *grid) set(col int, c cell) {
	for len(g.row) <= col {
		g.row = append(g.row, cell{})
	}
	g.row[col] = c
	if col+1 > g.columns {
		g.columns = col + 1
	}
}

func (g *grid) endRow() {
	if g.row == nil {
		return
	}
	g.rows = append(g.rows, g.row)
	if g.inHead {
		g.theadRows = len(g.rows)
	}
	g.row = nil
	g.col = 0
}

// convert turns the grid into a Table, choosing the header row and naming the
// columns.
func (g *grid) convert() Table {
	for i := range g.rows {
		for len(g.rows[i]) < g.columns {
			g.rows[i] = append(g.rows[i], cell{})
		}
	}

	headerAt, from := g.headerRow()
	if headerAt < 0 {
		t := Table{HeaderFrom: from}
		for _, row := range g.rows {
			t.Arrays = append(t.Arrays, texts(row))
		}
		return t
	}

	keys, renamed := names(g.rows[headerAt])
	t := Table{Keys: keys, HeaderFrom: from, Renamed: renamed}
	for i, row := range g.rows {
		if i <= headerAt {
			continue
		}
		obj := make(map[string]string, len(keys))
		for c, k := range keys {
			if c < len(row) {
				obj[k] = row[c].text
			}
		}
		t.Rows = append(t.Rows, obj)
	}
	return t
}

// headerRow picks the header, and says which guess it was.
func (g *grid) headerRow() (int, string) {
	if g.theadRows > 0 {
		// The last row of the thead: a multi-row thead is column groups above
		// the names, and the names are the row nearest the data.
		return g.theadRows - 1, "thead"
	}
	if len(g.rows) > 0 && allHeaders(g.rows[0]) {
		return 0, "first row"
	}
	return -1, "none"
}

func allHeaders(row []cell) bool {
	if len(row) == 0 {
		return false
	}
	for _, c := range row {
		if !c.header {
			return false
		}
	}
	return true
}

// names turns a header row into unique keys, reporting what it had to rename.
//
// JSON has no answer for a duplicate key: one of them wins and which one depends
// on the reader. A colspan in the header is the usual source, and two cells with
// the same text is the other.
func names(row []cell) ([]string, map[string]string) {
	keys := make([]string, 0, len(row))
	seen := map[string]int{}
	var renamed map[string]string

	for i, c := range row {
		name := c.text
		if name == "" {
			name = "column " + strconv.Itoa(i+1)
		}
		original := name
		if n := seen[name]; n > 0 {
			for {
				n++
				candidate := original + " " + strconv.Itoa(n)
				if seen[candidate] == 0 {
					seen[original] = n
					name = candidate
					break
				}
			}
			if renamed == nil {
				renamed = map[string]string{}
			}
			renamed[original] = name
		}
		seen[name]++
		keys = append(keys, name)
	}
	return keys, renamed
}

func texts(row []cell) []string {
	out := make([]string, 0, len(row))
	for _, c := range row {
		out = append(out, c.text)
	}
	return out
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

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
	tables, err := Convert(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tablejson:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tables); err != nil {
		fmt.Fprintln(os.Stderr, "tablejson:", err)
		os.Exit(1)
	}
	for i, t := range tables {
		if len(t.Renamed) > 0 {
			fmt.Fprintf(os.Stderr, "tablejson: table %d had %d duplicate column name(s)\n",
				i+1, len(t.Renamed))
		}
		if t.HeaderFrom == "none" {
			fmt.Fprintf(os.Stderr, "tablejson: table %d has no header row; rows are arrays\n", i+1)
		}
	}
}

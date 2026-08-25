package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func extract(t *testing.T, doc string) []Table {
	t.Helper()
	tables, err := Extract(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Extract(%q): %v", doc, err)
	}
	return tables
}

// one returns the single table in doc, failing if there is not exactly one.
func one(t *testing.T, doc string) Table {
	t.Helper()
	tables := extract(t, doc)
	if len(tables) != 1 {
		t.Fatalf("%q gave %d tables, want 1", doc, len(tables))
	}
	return tables[0]
}

func TestGrid(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want [][]string
	}{
		{"one cell", `<table><tr><td>a</table>`, [][]string{{"a"}}},
		{"one row", `<table><tr><td>a<td>b</table>`, [][]string{{"a", "b"}}},
		{"two rows", `<table><tr><td>a<tr><td>b</table>`, [][]string{{"a"}, {"b"}}},
		{"headers", `<table><tr><th>h<tr><td>a</table>`, [][]string{{"h"}, {"a"}}},
		{"tbody", `<table><tbody><tr><td>a</tbody></table>`, [][]string{{"a"}}},
		{"thead and tbody", `<table><thead><tr><th>h<tbody><tr><td>a</table>`,
			[][]string{{"h"}, {"a"}}},

		// colspan repeats the value across the columns it covers.
		{"colspan", `<table><tr><td colspan=2>a<td>b</table>`, [][]string{{"a", "a", "b"}}},
		{"colspan only", `<table><tr><td colspan=3>a</table>`, [][]string{{"a", "a", "a"}}},

		// rowspan reserves the column in later rows, where no cell is written.
		{"rowspan", `<table><tr><td rowspan=2>a<td>b<tr><td>c</table>`,
			[][]string{{"a", "b"}, {"a", "c"}}},
		{"rowspan of three", `<table><tr><td rowspan=3>a<td>b<tr><td>c<tr><td>d</table>`,
			[][]string{{"a", "b"}, {"a", "c"}, {"a", "d"}}},
		{"both spans", `<table><tr><td colspan=2 rowspan=2>a<td>b<tr><td>c</table>`,
			[][]string{{"a", "a", "b"}, {"a", "a", "c"}}},
		{"rowspan in the middle", `<table><tr><td>a<td rowspan=2>b<td>c<tr><td>d<td>e</table>`,
			[][]string{{"a", "b", "c"}, {"d", "b", "e"}}},

		// Ragged rows are padded, so the result is a rectangle.
		{"ragged", `<table><tr><td>a<td>b<tr><td>c</table>`, [][]string{{"a", "b"}, {"c", ""}}},

		// Cell content.
		{"entities", `<table><tr><td>caf&eacute; &amp; more</table>`,
			[][]string{{"café & more"}}},
		{"whitespace collapsed", "<table><tr><td>  a\n  b  </table>", [][]string{{"a b"}}},
		{"inline markup", `<table><tr><td>a <b>b</b> c</table>`, [][]string{{"a b c"}}},
		{"empty cell", `<table><tr><td><td>b</table>`, [][]string{{"", "b"}}},

		// Malformed spans are clamped rather than trusted.
		{"zero colspan", `<table><tr><td colspan=0>a</table>`, [][]string{{"a"}}},
		{"negative rowspan", `<table><tr><td rowspan=-3>a</table>`, [][]string{{"a"}}},
		{"unparseable colspan", `<table><tr><td colspan=lots>a</table>`, [][]string{{"a"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := one(t, tt.doc)
			if !reflect.DeepEqual(got.Rows, tt.want) {
				t.Errorf("rows = %q, want %q", got.Rows, tt.want)
			}
			for i, row := range got.Rows {
				if len(row) != got.Columns {
					t.Errorf("row %d has %d fields, Columns is %d", i, len(row), got.Columns)
				}
			}
		})
	}
}

// Content a parser moves out of the table is reported inside it, so a table
// extractor has to take cell content from cells rather than text from the table.
func TestFosteredContentIsNotTableData(t *testing.T) {
	tests := []struct {
		doc  string
		want [][]string
	}{
		{`<table>stray<tr><td>a</table>`, [][]string{{"a"}}},
		{`<table><tr>stray<td>a</table>`, [][]string{{"a"}}},
		{`<table><b>stray</b><tr><td>a</table>`, [][]string{{"a"}}},
		{`<table><tr><td>a</td>stray</tr></table>`, [][]string{{"a"}}},
		{`<table><div>stray</div><tr><td>a<td>b</table>`, [][]string{{"a", "b"}}},
	}
	for _, tt := range tests {
		got := one(t, tt.doc)
		if !reflect.DeepEqual(got.Rows, tt.want) {
			t.Errorf("%q: rows = %q, want %q", tt.doc, got.Rows, tt.want)
		}
	}
}

// The two ways of writing the same table have to give the same grid, which is
// what the implied end tags are for.
func TestImplicitAndExplicitEndTagsAgree(t *testing.T) {
	pairs := []struct{ implicit, explicit string }{
		{`<table><tr><td>a<td>b</table>`,
			`<table><tr><td>a</td><td>b</td></tr></table>`},
		{`<table><tr><td>a<tr><td>b</table>`,
			`<table><tr><td>a</td></tr><tr><td>b</td></tr></table>`},
		{`<table><thead><tr><th>h<tbody><tr><td>a</table>`,
			`<table><thead><tr><th>h</th></tr></thead><tbody><tr><td>a</td></tr></tbody></table>`},
		{`<table><tr><td colspan=2>a<td>b<tr><td>c<td>d<td>e</table>`,
			`<table><tr><td colspan="2">a</td><td>b</td></tr><tr><td>c</td><td>d</td><td>e</td></tr></table>`},
	}
	for _, p := range pairs {
		a, b := one(t, p.implicit), one(t, p.explicit)
		if !reflect.DeepEqual(a.Rows, b.Rows) {
			t.Errorf("implicit %q gave %q\nexplicit %q gave %q",
				p.implicit, a.Rows, p.explicit, b.Rows)
		}
	}
}

func TestSeveralTables(t *testing.T) {
	tables := extract(t, `<table><tr><td>a</table><p>between</p><table><tr><td>b<td>c</table>`)
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2", len(tables))
	}
	if !reflect.DeepEqual(tables[0].Rows, [][]string{{"a"}}) {
		t.Errorf("first = %q", tables[0].Rows)
	}
	if !reflect.DeepEqual(tables[1].Rows, [][]string{{"b", "c"}}) {
		t.Errorf("second = %q", tables[1].Rows)
	}
}

// A table inside a cell is its own table, and the outer one keeps its shape.
func TestNestedTables(t *testing.T) {
	tables := extract(t, `<table><tr><td><table><tr><td>inner</table><td>outer</table>`)
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2: %q", len(tables), tables)
	}
	// The inner table closes first.
	if !reflect.DeepEqual(tables[0].Rows, [][]string{{"inner"}}) {
		t.Errorf("inner = %q, want [[inner]]", tables[0].Rows)
	}
	if got := tables[1].Rows; len(got) != 1 || len(got[0]) != 2 || got[0][1] != "outer" {
		t.Errorf("outer = %q, want one row ending in \"outer\"", got)
	}
}

func TestNoTables(t *testing.T) {
	for _, doc := range []string{``, `<p>no tables here</p>`, `<div><span>x</span></div>`} {
		if got := extract(t, doc); len(got) != 0 {
			t.Errorf("%q gave %d tables", doc, len(got))
		}
	}
}

// An unclosed table still ends at the document end.
func TestAnUnclosedTable(t *testing.T) {
	got := one(t, `<table><tr><td>a<td>b`)
	if !reflect.DeepEqual(got.Rows, [][]string{{"a", "b"}}) {
		t.Errorf("rows = %q", got.Rows)
	}
}

// The grid must not depend on how the input was written.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<table>stray<tr><th>a<th colspan=2>b<tr><td rowspan=2>c<td>d<td>e<tr><td>f<td>g</table>`,
		`<table><tr><td>caf&eacute;<td>b</table>`,
		`<table><tr><td><table><tr><td>inner</table><td>outer</table>`,
		`<table><thead><tr><th>h<tbody><tr><td>a</table>`,
	}
	for _, doc := range docs {
		want := extract(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			got, err := Extract(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q at writes of %d:\n got %q\nwant %q", doc, n, got, want)
			}
		}
	}
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

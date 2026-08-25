package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func convert(t *testing.T, doc string) []Table {
	t.Helper()
	tables, err := Convert(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Convert(%q): %v", doc, err)
	}
	return tables
}

func one(t *testing.T, doc string) Table {
	t.Helper()
	tables := convert(t, doc)
	if len(tables) != 1 {
		t.Fatalf("%q gave %d tables, want 1", doc, len(tables))
	}
	return tables[0]
}

// Which row is the header is a guess, and the program says which one it made.
func TestHeaderDetection(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		from string
		keys []string
	}{
		{"thead", `<table><thead><tr><th>a<th>b<tbody><tr><td>1<td>2</table>`,
			"thead", []string{"a", "b"}},
		{"first row of th", `<table><tr><th>a<th>b<tr><td>1<td>2</table>`,
			"first row", []string{"a", "b"}},
		// A multi-row thead is column groups above the names, so the row
		// nearest the data is the one with the names in it.
		{"multi-row thead", `<table><thead><tr><th colspan=2>group<tr><th>a<th>b<tbody><tr><td>1<td>2</table>`,
			"thead", []string{"a", "b"}},
		// A first row of ordinary cells is data, not a header.
		{"no header", `<table><tr><td>1<td>2<tr><td>3<td>4</table>`, "none", nil},
		{"mixed first row", `<table><tr><th>a<td>b<tr><td>1<td>2</table>`, "none", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := one(t, tt.doc)
			if got.HeaderFrom != tt.from {
				t.Errorf("HeaderFrom = %q, want %q", got.HeaderFrom, tt.from)
			}
			if !reflect.DeepEqual(got.Keys, tt.keys) {
				t.Errorf("Keys = %q, want %q", got.Keys, tt.keys)
			}
		})
	}
}

// With no header the rows are arrays, because inventing "column 1" for a table
// that has no names produces JSON that looks authoritative and says nothing.
func TestNoHeaderMeansArrays(t *testing.T) {
	got := one(t, `<table><tr><td>1<td>2<tr><td>3<td>4</table>`)
	if got.Rows != nil {
		t.Errorf("Rows = %v, want none", got.Rows)
	}
	want := [][]string{{"1", "2"}, {"3", "4"}}
	if !reflect.DeepEqual(got.Arrays, want) {
		t.Errorf("Arrays = %q, want %q", got.Arrays, want)
	}
}

// Duplicate names are the part that needs care: JSON has no answer for a
// duplicate key, so the keys are made unique and the renaming is reported.
func TestDuplicateNames(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		keys    []string
		renamed map[string]string
	}{
		{
			name:    "colspan in the header",
			doc:     `<table><tr><th>a<th colspan=2>b<tr><td>1<td>2<td>3</table>`,
			keys:    []string{"a", "b", "b 2"},
			renamed: map[string]string{"b": "b 2"},
		},
		{
			name:    "two cells with the same text",
			doc:     `<table><tr><th>a<th>a<tr><td>1<td>2</table>`,
			keys:    []string{"a", "a 2"},
			renamed: map[string]string{"a": "a 2"},
		},
		{
			name:    "three the same",
			doc:     `<table><tr><th>a<th>a<th>a<tr><td>1<td>2<td>3</table>`,
			keys:    []string{"a", "a 2", "a 3"},
			renamed: map[string]string{"a": "a 3"},
		},
		{
			name: "an empty header cell is named by position",
			doc:  `<table><tr><th>a<th><tr><td>1<td>2</table>`,
			keys: []string{"a", "column 2"},
		},
		{
			name: "two empty header cells",
			doc:  `<table><tr><th><th><tr><td>1<td>2</table>`,
			keys: []string{"column 1", "column 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := one(t, tt.doc)
			if !reflect.DeepEqual(got.Keys, tt.keys) {
				t.Errorf("Keys = %q, want %q", got.Keys, tt.keys)
			}
			if !reflect.DeepEqual(got.Renamed, tt.renamed) {
				t.Errorf("Renamed = %v, want %v", got.Renamed, tt.renamed)
			}
			// Every key is distinct, which is the invariant the renaming
			// exists for.
			seen := map[string]bool{}
			for _, k := range got.Keys {
				if seen[k] {
					t.Errorf("key %q appears twice in %q", k, got.Keys)
				}
				seen[k] = true
			}
			// And every row has one field per key.
			for i, row := range got.Rows {
				if len(row) != len(got.Keys) {
					t.Errorf("row %d has %d fields, want %d", i, len(row), len(got.Keys))
				}
			}
		})
	}
}

// The grid work from the CSV converter still has to hold: spans, implied end
// tags, and content a parser fosters out of the table.
func TestTheGridUnderneath(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		rows []map[string]string
	}{
		{"rowspan carries down", `<table><tr><th>a<th>b<tr><td rowspan=2>x<td>1<tr><td>2</table>`,
			[]map[string]string{{"a": "x", "b": "1"}, {"a": "x", "b": "2"}}},
		{"implicit end tags", `<table><tr><th>a<th>b<tr><td>1<td>2</table>`,
			[]map[string]string{{"a": "1", "b": "2"}}},
		{"fostered text is not data", `<table>stray<tr><th>a<tr><td>1</table>`,
			[]map[string]string{{"a": "1"}}},
		{"entities decoded", `<table><tr><th>a<tr><td>caf&eacute;</table>`,
			[]map[string]string{{"a": "café"}}},
		{"ragged rows padded", `<table><tr><th>a<th>b<tr><td>1</table>`,
			[]map[string]string{{"a": "1", "b": ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := one(t, tt.doc).Rows; !reflect.DeepEqual(got, tt.rows) {
				t.Errorf("rows = %v, want %v", got, tt.rows)
			}
		})
	}
}

func TestSeveralAndNestedTables(t *testing.T) {
	tables := convert(t, `<table><tr><th>a<tr><td>1</table><table><tr><th>b<tr><td>2</table>`)
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2", len(tables))
	}
	if !reflect.DeepEqual(tables[0].Keys, []string{"a"}) || !reflect.DeepEqual(tables[1].Keys, []string{"b"}) {
		t.Errorf("keys = %q and %q", tables[0].Keys, tables[1].Keys)
	}

	nested := convert(t, `<table><tr><th>outer<tr><td><table><tr><th>inner<tr><td>x</table></table>`)
	if len(nested) != 2 {
		t.Fatalf("nested gave %d tables, want 2", len(nested))
	}
	// The inner table closes first.
	if !reflect.DeepEqual(nested[0].Keys, []string{"inner"}) {
		t.Errorf("first closed table has keys %q, want [inner]", nested[0].Keys)
	}
}

// The output must not depend on how the input was written.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<table><thead><tr><th>Name<th colspan=2>Score<tbody><tr><td>a<td>1<td>2</table>`,
		`<table>stray<tr><th>a<th>a<tr><td>caf&eacute;<td>2</table>`,
		`<table><tr><td>1<td>2</table>`,
		`<table><tr><th>a<tr><td rowspan=2>x<tr></table>`,
	}
	for _, doc := range docs {
		want := convert(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			got, err := Convert(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q at writes of %d:\n got %+v\nwant %+v", doc, n, got, want)
			}
		}
	}
}

func TestNoTables(t *testing.T) {
	for _, doc := range []string{``, `<p>none</p>`} {
		if got := convert(t, doc); len(got) != 0 {
			t.Errorf("%q gave %d tables", doc, len(got))
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

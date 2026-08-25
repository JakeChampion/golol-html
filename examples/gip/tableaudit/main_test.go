package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func audit(t *testing.T, doc string) Report {
	t.Helper()
	rep, err := Audit(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Audit(%q): %v", doc, err)
	}
	return rep
}

// kinds returns the finding kinds, in report order.
func kinds(rep Report) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func TestWhatIsClean(t *testing.T) {
	clean := []string{
		// scope on every header cell.
		`<table><tr><th scope=col>a<th scope=col>b<tr><td>1<td>2</table>`,
		`<table><tr><th scope=row>a<td>1</table>`,
		`<table><tr><th SCOPE=COL>a<tr><td>1</table>`,
		`<table><tr><th scope=colgroup>a<tr><td>1</table>`,
		// Every data cell naming its headers.
		`<table><tr><th id=h>a<tr><td headers=h>1</table>`,
		// A table with no header cells and no data cells is not a data table.
		`<table></table>`,
		// No tables at all.
		`<p>nothing</p>`,
	}
	for _, doc := range clean {
		if rep := audit(t, doc); len(rep.Findings) != 0 {
			t.Errorf("%q: %v", doc, rep.Findings)
		}
	}
}

func TestFindings(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		kinds []string
	}{
		{"no scope at all", `<table><tr><th>a<th>b<tr><td>1<td>2</table>`,
			[]string{"no scope and no headers"}},
		{"scope on some", `<table><tr><th scope=col>a<th>b<tr><td>1<td>2</table>`,
			[]string{"scope on some header cells but not all"}},
		{"invalid scope", `<table><tr><th scope=column>a<tr><td>1</table>`,
			[]string{"invalid scope", "no scope and no headers"}},
		{"spans without association", `<table><tr><th colspan=2>a<tr><td>1<td>2</table>`,
			[]string{"spans without scope or headers"}},
		{"no header cells", `<table><tr><td>1<td>2</table>`,
			[]string{"no header cells"}},
		{"empty headers attribute", `<table><tr><th scope=col>a<tr><td headers="  ">1</table>`,
			[]string{"empty headers attribute"}},
		{"nested table", `<table><tr><th scope=col>a<tr><td><table><tr><th>b<tr><td>1</table></table>`,
			[]string{"nested table without scope or headers"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kinds(audit(t, tt.doc)); !reflect.DeepEqual(got, tt.kinds) {
				t.Errorf("kinds = %q, want %q", got, tt.kinds)
			}
		})
	}
}

// A headers attribute names ids, and an id can be defined after the reference.
// This is the check that cannot be made as the document streams, and these are
// the two directions it has to get right.
func TestHeadersAreCheckedAgainstTheWholeDocument(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		kinds []string
	}{
		{
			// The id is on the header before the reference: the easy direction.
			name: "id before the reference",
			doc:  `<table><tr><th id=h scope=col>a<tr><td headers=h>1</table>`,
		},
		{
			// The id is defined after the reference, and further on in the
			// document than the table. Nothing at the moment of the reference
			// could have known.
			name: "id after the reference",
			doc:  `<table><tr><th scope=col>a<tr><td headers=later>1</table><p id=later>x</p>`,
		},
		{
			name:  "id nowhere",
			doc:   `<table><tr><th scope=col>a<tr><td headers=missing>1</table>`,
			kinds: []string{"headers names an id that is not in the document"},
		},
		{
			// A duplicate id breaks the association, and the duplicate can be
			// anywhere - including after the table.
			name:  "duplicated id after the table",
			doc:   `<table><tr><th id=h scope=col>a<tr><td headers=h>1</table><p id=h>x</p>`,
			kinds: []string{"headers names a duplicated id"},
		},
		{
			name:  "several missing ids in one attribute",
			doc:   `<table><tr><th scope=col>a<tr><td headers="one two">1</table>`,
			kinds: []string{"headers names an id that is not in the document"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := audit(t, tt.doc)
			if got := kinds(rep); !reflect.DeepEqual(got, tt.kinds) {
				t.Errorf("kinds = %q, want %q", got, tt.kinds)
			}
		})
	}
}

// The detail names what is wrong, because a report that says only "bad table" is
// a report nobody can act on.
func TestFindingsNameTheProblem(t *testing.T) {
	rep := audit(t, `<table><tr><th scope=col>a<tr><td headers="one two">1</table>`)
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %v", rep.Findings)
	}
	f := rep.Findings[0]
	if !strings.Contains(f.Detail, "one") || !strings.Contains(f.Detail, "two") {
		t.Errorf("detail %q does not name the missing ids", f.Detail)
	}
	if !strings.Contains(f.String(), "table 1") {
		t.Errorf("%q does not name the table", f.String())
	}
}

// Several tables are numbered in document order, and a finding names its table.
func TestTablesAreNumbered(t *testing.T) {
	rep := audit(t, `<table><tr><th scope=col>a<tr><td>1</table>`+
		`<table><tr><th>b<tr><td>2</table>`+
		`<table><tr><th>c<tr><td>3</table>`)
	if rep.Tables != 3 {
		t.Errorf("Tables = %d, want 3", rep.Tables)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("findings = %v", rep.Findings)
	}
	if rep.Findings[0].Table != 2 || rep.Findings[1].Table != 3 {
		t.Errorf("findings are for tables %d and %d, want 2 and 3",
			rep.Findings[0].Table, rep.Findings[1].Table)
	}
}

// The report must not depend on how the input was written.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<table><tr><th>a<th scope=col>b<tr><td>1<td>2</table>`,
		`<table><tr><th scope=col>a<tr><td headers="later missing">1</table><p id=later>x</p>`,
		`<table><tr><th colspan=2>a<tr><td>1<td>2</table>`,
		`<table><tr><th scope=col>a<tr><td><table><tr><th>b<tr><td>1</table></table>`,
	}
	for _, doc := range docs {
		want := audit(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			got, err := Audit(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q at writes of %d:\n got %+v\nwant %+v", doc, n, got, want)
			}
		}
	}
}

// Implied end tags again: the two spellings of one table have to be audited the
// same way.
func TestImplicitAndExplicitEndTagsAgree(t *testing.T) {
	pairs := []struct{ implicit, explicit string }{
		{`<table><tr><th>a<th>b<tr><td>1<td>2</table>`,
			`<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>`},
		{`<table><tr><th scope=col>a<tr><td>1</table>`,
			`<table><tr><th scope="col">a</th></tr><tr><td>1</td></tr></table>`},
	}
	for _, p := range pairs {
		a, b := audit(t, p.implicit), audit(t, p.explicit)
		if !reflect.DeepEqual(kinds(a), kinds(b)) {
			t.Errorf("implicit %q gave %q, explicit %q gave %q",
				p.implicit, kinds(a), p.explicit, kinds(b))
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

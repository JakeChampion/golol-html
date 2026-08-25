// Command tableaudit reports tables whose header cells cannot be associated with
// their data.
//
// A screen reader announces a data cell together with its headers, and it can
// only do that if the association is discoverable: a scope attribute on each
// <th>, or a headers attribute on each data cell naming the ids of its headers,
// or a table simple enough that the implicit algorithm works - one header row or
// column, no spans, no nesting.
//
// The reason this is a good fit for a streaming rewriter, where the table
// converters next door are a fight, is that a report has no position. Everything
// awkward about tables here comes from having to write something at a place the
// rewriter has already passed; an audit writes nothing, so the awkwardness
// disappears and one pass is enough - including for the part that cannot be
// decided locally.
//
// A headers attribute names ids, and an id can be defined after the reference
// that uses it, anywhere in the document. So "does this headers attribute point
// at anything" is a question with no answer at the moment it is asked. The
// program collects both sides and reconciles them in an OnDocumentEnd handler,
// which is the one position that has seen everything - and the same reason a
// rewrite that needed this would have to read the document twice.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Finding is one problem with one table.
type Finding struct {
	// Table is the 1-based index of the table in the document.
	Table int
	// Kind is the problem, as a short stable string a caller can group by.
	Kind string
	// Detail says which cell or which id.
	Detail string
}

func (f Finding) String() string {
	if f.Detail == "" {
		return fmt.Sprintf("table %d: %s", f.Table, f.Kind)
	}
	return fmt.Sprintf("table %d: %s (%s)", f.Table, f.Kind, f.Detail)
}

// A Report is everything the audit found.
type Report struct {
	Findings []Finding
	// Tables is how many tables were seen, so a clean report can say so.
	Tables int
	// Layout counts the tables with no header cells at all, which are usually
	// layout tables rather than data tables and are reported separately.
	Layout int
}

// validScopes are the values scope may take. Compared folded, because scope is
// not an attribute whose values a selector matches case-insensitively.
var validScopes = map[string]bool{
	"col": true, "row": true, "colgroup": true, "rowgroup": true,
}

type auditor struct {
	report Report

	// open is this program's own stack, because the association rules are about
	// structure and the library reports tokens.
	open []string

	// tables are the tables being read, innermost last: a table inside a cell is
	// audited separately.
	tables []*tableState

	// ids counts every id in the document, so a headers attribute can be
	// checked and duplicates reported. Both need the whole document, which is
	// why they are reconciled at the end.
	ids map[string]int
	// references are the headers attributes seen, with where they were.
	references []reference
}

type reference struct {
	table int
	cell  string
	ids   []string
}

type tableState struct {
	index int
	// headerCells counts the <th> in this table, and scoped how many have a
	// usable scope.
	headerCells, scoped int
	// headerIDs counts the <th> carrying an id, which is what a headers
	// attribute can point at.
	headerIDs int
	// dataCells counts the <td>, and withHeaders how many carry a headers
	// attribute.
	dataCells, withHeaders int
	// spanned is set when any cell spans rows or columns, which is what makes
	// the implicit algorithm unreliable.
	spanned bool
	// nested is set when this table is inside another, where the implicit
	// algorithm does not apply either.
	nested bool
}

// Audit reads a document and returns what it found.
func Audit(r io.Reader) (Report, error) {
	a := &auditor{ids: map[string]int{}}
	w, err := lolhtml.NewWriter(io.Discard, a.options()...)
	if err != nil {
		return Report{}, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return Report{}, err
	}
	if err := w.Close(); err != nil {
		return Report{}, err
	}
	sort.SliceStable(a.report.Findings, func(i, j int) bool {
		if a.report.Findings[i].Table != a.report.Findings[j].Table {
			return a.report.Findings[i].Table < a.report.Findings[j].Table
		}
		return a.report.Findings[i].Kind < a.report.Findings[j].Kind
	})
	return a.report, nil
}

func (a *auditor) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", a.element),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			a.popTo(0)
			a.reconcile()
			return nil
		}),
	}
}

func (a *auditor) element(el *lolhtml.Element) error {
	tag := el.TagName()
	a.closeImplied(tag)

	// Every id in the document, wherever it is: a headers attribute may name one
	// on an element that is not a cell, and a duplicate id breaks the
	// association whether or not the duplicate is in a table.
	if id, ok := el.Attribute("id"); ok {
		if id = strings.TrimSpace(id); id != "" {
			a.ids[id]++
		}
	}

	switch tag {
	case "table":
		t := &tableState{index: a.report.Tables + 1, nested: len(a.tables) > 0}
		a.report.Tables++
		a.tables = append(a.tables, t)
	case "th", "td":
		if t := a.current(); t != nil {
			a.cell(el, t, tag == "th")
		}
	}

	if !el.CanHaveContent() {
		return nil
	}
	a.open = append(a.open, tag)
	depth := len(a.open)
	return el.OnEndTag(func(*lolhtml.EndTag) error {
		a.popTo(depth - 1)
		return nil
	})
}

func (a *auditor) cell(el *lolhtml.Element, t *tableState, header bool) {
	if spans(el, "colspan") > 1 || spans(el, "rowspan") > 1 {
		t.spanned = true
	}

	if header {
		t.headerCells++
		if scope, ok := el.Attribute("scope"); ok {
			if validScopes[strings.ToLower(strings.TrimSpace(scope))] {
				t.scoped++
			} else {
				a.add(t.index, "invalid scope", strings.TrimSpace(scope))
			}
		}
		if id, ok := el.Attribute("id"); ok && strings.TrimSpace(id) != "" {
			t.headerIDs++
		}
		return
	}

	t.dataCells++
	if h, ok := el.Attribute("headers"); ok {
		ids := strings.Fields(h)
		if len(ids) == 0 {
			a.add(t.index, "empty headers attribute", "")
			return
		}
		t.withHeaders++
		// Whether these ids exist cannot be known yet: an id may be defined
		// later in the document. Collected now, checked at the end.
		a.references = append(a.references, reference{table: t.index, cell: h, ids: ids})
	}
}

func spans(el *lolhtml.Element, name string) int {
	v, ok := el.Attribute(name)
	if !ok {
		return 1
	}
	n := 0
	for _, r := range strings.TrimSpace(v) {
		if r < '0' || r > '9' {
			return 1
		}
		n = n*10 + int(r-'0')
		if n > 1000 {
			return 1000
		}
	}
	if n < 1 {
		return 1
	}
	return n
}

func (a *auditor) current() *tableState {
	if len(a.tables) == 0 {
		return nil
	}
	return a.tables[len(a.tables)-1]
}

func (a *auditor) closeImplied(next string) {
	switch next {
	case "td", "th":
		a.popThrough(set("td", "th"), set("tr", "table"))
	case "tr":
		a.popThrough(set("tr"), set("table", "tbody", "thead", "tfoot"))
	case "tbody", "thead", "tfoot":
		a.popThrough(set("tbody", "thead", "tfoot"), set("table"))
	}
}

func (a *auditor) popThrough(want, barrier map[string]bool) {
	for i := len(a.open) - 1; i >= 0; i-- {
		if want[a.open[i]] {
			a.popTo(i)
			return
		}
		if barrier[a.open[i]] {
			return
		}
	}
}

func (a *auditor) popTo(n int) {
	for len(a.open) > n {
		tag := a.open[len(a.open)-1]
		a.open = a.open[:len(a.open)-1]
		if tag == "table" {
			if t := a.current(); t != nil {
				a.finish(t)
				a.tables = a.tables[:len(a.tables)-1]
			}
		}
	}
}

// finish judges one table, now that all of it has been seen.
func (a *auditor) finish(t *tableState) {
	if t.headerCells == 0 {
		a.report.Layout++
		if t.dataCells > 0 {
			a.add(t.index, "no header cells", "")
		}
		return
	}

	// Every data cell naming its headers is association enough on its own.
	if t.dataCells > 0 && t.withHeaders == t.dataCells {
		return
	}

	if t.scoped == t.headerCells {
		return
	}

	// Without scope on every header, the implicit algorithm has to carry it, and
	// it only does for a simple table.
	switch {
	case t.spanned:
		a.add(t.index, "spans without scope or headers", fmt.Sprintf(
			"%d of %d header cells are scoped", t.scoped, t.headerCells))
	case t.nested:
		a.add(t.index, "nested table without scope or headers", "")
	case t.scoped > 0:
		a.add(t.index, "scope on some header cells but not all", fmt.Sprintf(
			"%d of %d", t.scoped, t.headerCells))
	default:
		a.add(t.index, "no scope and no headers", fmt.Sprintf(
			"%d header cells", t.headerCells))
	}
}

// reconcile checks the collected headers references against the collected ids.
// This is the part that cannot be done as the document streams, because an id
// may be defined after the reference that names it.
func (a *auditor) reconcile() {
	for _, ref := range a.references {
		var missing, duplicated []string
		for _, id := range ref.ids {
			switch n := a.ids[id]; {
			case n == 0:
				missing = append(missing, id)
			case n > 1:
				duplicated = append(duplicated, id)
			}
		}
		if len(missing) > 0 {
			a.add(ref.table, "headers names an id that is not in the document",
				strings.Join(missing, " "))
		}
		if len(duplicated) > 0 {
			a.add(ref.table, "headers names a duplicated id", strings.Join(duplicated, " "))
		}
	}
}

func (a *auditor) add(table int, kind, detail string) {
	a.report.Findings = append(a.report.Findings, Finding{Table: table, Kind: kind, Detail: detail})
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// String renders the report.
func (r Report) String() string {
	var b strings.Builder
	for _, f := range r.Findings {
		fmt.Fprintln(&b, f)
	}
	fmt.Fprintf(&b, "%d tables, %d findings", r.Tables, len(r.Findings))
	if r.Layout > 0 {
		fmt.Fprintf(&b, ", %d with no header cells at all", r.Layout)
	}
	b.WriteByte('\n')
	return b.String()
}

func main() {
	rep, err := Audit(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tableaudit:", err)
		os.Exit(1)
	}
	fmt.Print(rep)
	if len(rep.Findings) > 0 {
		os.Exit(1)
	}
}

package main

import (
	"io"
	"strings"
	"testing"
)

const page = `<!doctype html><body>` +
	`<div class="row"><div class="col-6">left</div><div class="col-6">right</div></div>` +
	`<div class="row"><div class="col-12"></div></div>` +
	`</body>`

// TestARowBecomesATableAndAColumnBecomesACell, with the content kept.
func TestARowBecomesATableAndAColumnBecomesACell(t *testing.T) {
	out, report, err := ConvertString(page, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}

	if report.Rows != 2 || report.Columns != 3 {
		t.Errorf("converted %d rows and %d columns, want 2 and 3", report.Rows, report.Columns)
	}
	if strings.Contains(out, `class="row"`) || strings.Contains(out, `class="col`) {
		t.Errorf("a div survived the conversion:\n%s", out)
	}
	for _, want := range []string{
		`<table role="presentation"`, `<tr>`, `<td valign="top">left</td>`,
		`<td valign="top">right</td>`, `</tr></table>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
}

// TestAnEmptyCellGetsANonBreakingSpace, as the character rather than the entity - which is what
// lolhtml.Text guarantees and what the client needs.
func TestAnEmptyCellGetsANonBreakingSpace(t *testing.T) {
	out, report, err := ConvertString(page, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if report.Spacers != 1 {
		t.Errorf("gave %d cells a spacer, want 1", report.Spacers)
	}
	if !strings.Contains(out, "<td valign=\"top\"> </td>") {
		t.Errorf("the empty cell has no spacer:\n%q", out)
	}
	if strings.Contains(out, "&nbsp;") || strings.Contains(out, "&amp;nbsp;") {
		t.Errorf("the spacer arrived as an entity:\n%s", out)
	}

	// A cell with text does not get one, and neither does one whose text is inside a nested
	// element.
	nested := `<!doctype html><body><div class="row"><div class="col"><b>x</b></div></div></body>`
	_, r, err := ConvertString(nested, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if r.Spacers != 0 {
		t.Errorf("a cell holding <b>x</b> was given %d spacers", r.Spacers)
	}
}

// TestARowInsideAParagraphIsRefused, because where the wrapper lands depends on the document's
// mode - which is the finding this app was built around.
func TestARowInsideAParagraphIsRefused(t *testing.T) {
	doc := `<!doctype html><body><p>text <div class="row"><div class="col">c</div></div></p></body>`
	out, report, err := ConvertString(doc, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}

	if report.RefusedInParagraph != 2 {
		t.Errorf("refused %d, want the row and its column", report.RefusedInParagraph)
	}
	if report.Rows != 0 || report.Columns != 0 {
		t.Errorf("converted %d rows and %d columns inside a paragraph", report.Rows, report.Columns)
	}
	if !strings.Contains(out, `<div class="row">`) {
		t.Errorf("the refused row was changed anyway:\n%s", out)
	}
	if !strings.Contains(report.String(), "depends on the doctype") {
		t.Errorf("the report does not say why:\n%s", report.String())
	}
}

// TestTheParagraphDepthIsTrackedAcrossTheDocument: a row after a paragraph has closed is
// converted, which is what makes the refusal a position rather than a mood.
func TestTheParagraphDepthIsTrackedAcrossTheDocument(t *testing.T) {
	doc := `<!doctype html><body>` +
		`<p>first <div class="row">refused</div></p>` +
		`<div class="row">converted</div>` +
		`<p>second</p>` +
		`<div class="row">converted too</div>` +
		`</body>`
	out, report, err := ConvertString(doc, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows != 2 || report.RefusedInParagraph != 1 {
		t.Errorf("converted %d and refused %d, want 2 and 1", report.Rows, report.RefusedInParagraph)
	}
	if strings.Count(out, "<table") != 2 {
		t.Errorf("%d tables in the output:\n%s", strings.Count(out, "<table"), out)
	}
}

// TestTheDoctypeIsReported, since it is the fact that decides whether a refusal mattered.
func TestTheDoctypeIsReported(t *testing.T) {
	withDoctype := `<!doctype html><body><div class="row">c</div></body>`
	_, r, err := ConvertString(withDoctype, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if r.Doctype != "html" {
		t.Errorf("doctype reported as %q", r.Doctype)
	}
	if r.Quirks() {
		t.Error("a document with a doctype was called quirks")
	}

	without := `<body><div class="row">c</div></body>`
	_, r2, err := ConvertString(without, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Doctype != "" || !r2.Quirks() {
		t.Errorf("a document with no doctype reported %q", r2.Doctype)
	}
	if !strings.Contains(r2.String(), "quirks mode") {
		t.Errorf("the report does not mention the mode:\n%s", r2.String())
	}
}

// TestNestedRowsAreConverted, innermost and outermost, since email templates nest tables for
// exactly this reason.
func TestNestedRowsAreConverted(t *testing.T) {
	doc := `<!doctype html><body><div class="row"><div class="col">` +
		`<div class="row"><div class="col">deep</div></div>` +
		`</div></div></body>`
	out, report, err := ConvertString(doc, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows != 2 || report.Columns != 2 {
		t.Errorf("converted %d rows and %d columns, want 2 and 2", report.Rows, report.Columns)
	}
	if strings.Count(out, "<table") != 2 || strings.Count(out, "</table>") != 2 {
		t.Errorf("the tables do not balance:\n%s", out)
	}
	if !strings.Contains(out, "deep") {
		t.Errorf("the content was lost:\n%s", out)
	}
}

// TestTheOutputDoesNotDependOnHowTheInputWasChunked - the property, over the streaming path.
func TestTheOutputDoesNotDependOnHowTheInputWasChunked(t *testing.T) {
	var whole strings.Builder
	if _, err := Convert(strings.NewReader(page), &whole, DefaultSelectors); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{1, 2, 3, 7, 64, 4096} {
		var got strings.Builder
		report, err := Convert(&chunkedReader{s: page, size: size}, &got, DefaultSelectors)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if got.String() != whole.String() {
			t.Errorf("read in %d-byte chunks gave:\n got  %s\n want %s", size, got.String(), whole.String())
		}
		if report.Rows != 2 || report.Spacers != 1 {
			t.Errorf("read size %d reported %d rows and %d spacers", size, report.Rows, report.Spacers)
		}
	}
}

// chunkedReader hands out at most size bytes per Read, which is what a socket does.
type chunkedReader struct {
	s    string
	size int
	at   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestConvertingTwiceConvertsNothingTheSecondTime - the second property. The first pass leaves no
// rows or columns behind, so the second has nothing to do and changes nothing.
func TestConvertingTwiceConvertsNothingTheSecondTime(t *testing.T) {
	once, first, err := ConvertString(page, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}
	twice, second, err := ConvertString(once, DefaultSelectors)
	if err != nil {
		t.Fatal(err)
	}

	if second.Rows != 0 || second.Columns != 0 || second.Spacers != 0 {
		t.Errorf("the second pass converted %d rows, %d columns and added %d spacers",
			second.Rows, second.Columns, second.Spacers)
	}
	if twice != once {
		t.Errorf("the second pass changed the document:\n once:  %s\n twice: %s", once, twice)
	}
	if first.Rows == 0 {
		t.Error("the first pass converted nothing, so this test proves nothing")
	}
}

// TestTheSelectorsAreTheCallersChoice, since every framework spells a row differently.
func TestTheSelectorsAreTheCallersChoice(t *testing.T) {
	doc := `<!doctype html><body><section data-row><span data-col>x</span></section></body>`
	out, report, err := ConvertString(doc, Selectors{Row: "section[data-row]", Column: "span[data-col]"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows != 1 || report.Columns != 1 {
		t.Errorf("converted %d rows and %d columns", report.Rows, report.Columns)
	}
	if !strings.Contains(out, `<td valign="top">x</td>`) {
		t.Errorf("the cell is missing:\n%s", out)
	}

	// A selector the library refuses is an error from Convert rather than a silent
	// skipping of every row.
	if _, _, err := ConvertString(doc, Selectors{Row: "section:has(span)", Column: "span"}); err == nil {
		t.Error("an unsupported selector was accepted")
	}
}

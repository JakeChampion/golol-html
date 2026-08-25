package main

import (
	"strings"
	"testing"
)

func counts(t *testing.T, doc string) map[string]int {
	t.Helper()
	r, err := Scan([]byte(doc))
	if err != nil {
		t.Fatalf("Scan(%q): %v", doc, err)
	}
	return r.Counts()
}

// TestEachConstructIsFoundInADocumentThatHasIt, one document per detector.
func TestEachConstructIsFoundInADocumentThatHasIt(t *testing.T) {
	for _, tc := range []struct {
		construct string
		doc       string
	}{
		{"implied end tag", `<ul><li>a<li>b</ul>`},
		{"element nothing closes", `<div><p>a`},
		{"<image>", `<image src="x">`},
		{"fostered content in a table", `<table>stray<tr><td>a</td></tr></table>`},
		{"template holding table rows", `<template><tr><td>x</td></tr></template>`},
		{"raw text inside a select or frameset", `<select><xmp>y</xmp></select>`},
		{"duplicate attribute", `<div id="a" id="b"></div>`},
		{"self-closing HTML tag", `<div/>`},
		{"raw text holding its own end sequence", `<script>var a = "</script";</script>`},
		{"base after a URL", `<img src="1"><base href="/x/">`},
		{"U+FFFD in text", "<p>a�b</p>"},
		{"p containing a block element", `<p><div>x</div></p>`},
	} {
		if n := counts(t, tc.doc)[tc.construct]; n == 0 {
			t.Errorf("%q: %q was not found", tc.doc, tc.construct)
		}
	}
	// Every construct in the table has a case above.
	covered := map[string]bool{}
	for _, tc := range []string{"implied end tag", "element nothing closes", "<image>",
		"fostered content in a table", "template holding table rows",
		"raw text inside a select or frameset", "duplicate attribute", "self-closing HTML tag",
		"raw text holding its own end sequence", "base after a URL", "U+FFFD in text",
		"p containing a block element"} {
		covered[tc] = true
	}
	for _, c := range Constructs {
		if !covered[c.Name] {
			t.Errorf("%q is in the table and has no test", c.Name)
		}
	}
}

// TestATidyDocumentHasNoneOfThem, which is what makes the report worth reading.
func TestATidyDocumentHasNoneOfThem(t *testing.T) {
	const tidy = `<!DOCTYPE html><html><head><title>t</title></head><body>` +
		`<div class="a"><p>text</p><ul><li>one</li><li>two</li></ul>` +
		`<table><tbody><tr><td>cell</td></tr></tbody></table>` +
		`<img src="/i.png" alt="x"><script src="/s.js"></script></div></body></html>`
	r, err := Scan([]byte(tidy))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 0 {
		t.Errorf("a tidy document reported %d constructs: %v", len(r.Findings), r.Findings)
	}
	if !strings.Contains(r.String(), "none of the documented constructs") {
		t.Errorf("the report is %q", r.String())
	}
}

// TestABaseBeforeEveryURLIsNotReported: the construct is the order, not the element.
func TestABaseBeforeEveryURLIsNotReported(t *testing.T) {
	if n := counts(t, `<base href="/x/"><img src="1">`)["base after a URL"]; n != 0 {
		t.Errorf("a base before the URLs was reported %d times", n)
	}
	if n := counts(t, `<img src="1"><base href="/x/">`)["base after a URL"]; n != 1 {
		t.Errorf("a base after a URL was reported %d times, want 1", n)
	}
	// A base with no href does not resolve anything, so it is not the construct.
	if n := counts(t, `<img src="1"><base target="_blank">`)["base after a URL"]; n != 0 {
		t.Errorf("a base with no href was reported %d times", n)
	}
}

// TestAClosedListHasNoImpliedEndTags, and an open one has them.
func TestAClosedListHasNoImpliedEndTags(t *testing.T) {
	if n := counts(t, `<ul><li>a</li><li>b</li></ul>`)["implied end tag"]; n != 0 {
		t.Errorf("a closed list reported %d implied end tags", n)
	}
	n := counts(t, `<ul><li>a<li>b</ul>`)["implied end tag"]
	if n == 0 {
		t.Error("an open list reported none")
	}
}

// TestFosteredContentNeedsToBeOutsideACell.
func TestFosteredContentNeedsToBeOutsideACell(t *testing.T) {
	if n := counts(t, `<table><tr><td>text</td></tr></table>`)["fostered content in a table"]; n != 0 {
		t.Errorf("text in a cell was reported %d times", n)
	}
	if n := counts(t, `<table>text<tr><td>a</td></tr></table>`)["fostered content in a table"]; n != 1 {
		t.Errorf("text outside a cell was reported %d times, want 1", n)
	}
	// A caption is a cell for this purpose: its text is in the table.
	if n := counts(t, `<table><caption>c</caption><tr><td>a</td></tr></table>`)["fostered content in a table"]; n != 0 {
		t.Errorf("a caption's text was reported %d times", n)
	}
}

// TestASelfClosingVoidElementIsNotTheConstruct: the slash is meaningless there, and every
// document has some.
func TestASelfClosingVoidElementIsNotTheConstruct(t *testing.T) {
	for _, doc := range []string{`<img src="x"/>`, `<br/>`, `<input/>`, `<svg><circle r="1"/></svg>`} {
		if n := counts(t, doc)["self-closing HTML tag"]; n != 0 {
			t.Errorf("%q was reported %d times", doc, n)
		}
	}
	if n := counts(t, `<div/>`)["self-closing HTML tag"]; n != 1 {
		t.Errorf("<div/> was reported %d times, want 1", n)
	}
}

// TestTheReportOrdersByTheTableAndCountsUses.
func TestTheReportOrdersByTheTableAndCountsUses(t *testing.T) {
	doc := strings.Repeat(`<ul><li>a<li>b</ul>`, 3) + `<image src="x">`
	r, err := Scan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Counts()["implied end tag"]; got < 6 {
		t.Errorf("%d implied end tags, want at least six", got)
	}
	s := r.String()
	if i, j := strings.Index(s, "implied end tag"), strings.Index(s, "<image>"); i < 0 || j < 0 || i > j {
		t.Errorf("the report is not in table order:\n%s", s)
	}
	// The first offset is where the construct first appears, not where it last does.
	if first := r.First()["<image>"]; first != strings.Index(doc, "<image") {
		t.Errorf("the image is reported at %d, want %d", first, strings.Index(doc, "<image"))
	}
}

// TestScanningIsReadOnly: the scan writes nothing, so it can be pointed at anything.
func TestScanningIsReadOnly(t *testing.T) {
	for _, doc := range []string{
		`<select><xmp>y</xmp></select>`, // strict mode would refuse this
		"<p>caf\xe9</p>",                // not valid UTF-8
		`<p>unclosed`,
		``,
	} {
		if _, err := Scan([]byte(doc)); err != nil {
			t.Errorf("%q: %v", doc, err)
		}
	}
}

// TestAnUnclosedElementIsReportedWhereItOpens, not where the scan found out. The document
// end is when an element nothing closes becomes knowable; the offset a reader wants is the
// one they can look at.
func TestAnUnclosedElementIsReportedWhereItOpens(t *testing.T) {
	doc := `<div><p>text`
	r, err := Scan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}

	var got []Finding
	for _, f := range r.Findings {
		if f.Construct == "element nothing closes" {
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("found %d elements nothing closes in %q, want the div and the p: %+v",
			len(got), doc, got)
	}
	// In document order, at the offsets of the two start tags, and neither at the end.
	for i, want := range []struct {
		at   int
		name string
	}{{0, "<div>"}, {5, "<p>"}} {
		if got[i].Offset != want.at {
			t.Errorf("%s reported at %d, want %d", want.name, got[i].Offset, want.at)
		}
		if !strings.Contains(got[i].Detail, want.name) {
			t.Errorf("finding %d says %q, which does not name %s", i, got[i].Detail, want.name)
		}
		if got[i].Offset == len(doc) {
			t.Errorf("%s was reported at the document end", want.name)
		}
	}
}

// TestAClosedElementLeavesNothingPending, so the offsets above are not just every element.
func TestAClosedElementLeavesNothingPending(t *testing.T) {
	r, err := Scan([]byte(`<div><p>text</p></div>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if f.Construct == "element nothing closes" {
			t.Errorf("a fully closed document reported %+v", f)
		}
	}
}

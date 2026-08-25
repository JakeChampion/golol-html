package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func report(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Report(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Report(%q): %v", doc, err)
	}
	return res
}

func findingFor(res Result, name string) (Finding, bool) {
	for _, f := range res.Findings {
		if f.Name == name {
			return f, true
		}
	}
	return Finding{}, false
}

// TestEveryObsoleteElementIsReportedWithItsClass.
func TestEveryObsoleteElementIsReportedWithItsClass(t *testing.T) {
	for tag, want := range Elements {
		doc := "<" + tag + "></" + tag + ">"
		if tag == "image" || tag == "frame" || tag == "spacer" || tag == "basefont" ||
			tag == "isindex" || tag == "keygen" {
			doc = "<" + tag + ">"
		}
		res := report(t, doc)
		f, ok := findingFor(res, tag)
		if !ok {
			t.Errorf("<%s> was not reported: %v", tag, res.Findings)
			continue
		}
		if f.Class != want.class {
			t.Errorf("<%s> is %v, want %v", tag, f.Class, want.class)
		}
		if f.Instead != want.what {
			t.Errorf("<%s> advises %q, want %q", tag, f.Instead, want.what)
		}
		if f.Offset != 0 {
			t.Errorf("<%s> is at %d, want 0", tag, f.Offset)
		}
	}
}

// TestModernMarkupIsNotReported, which is the half a checker gets wrong: a page that
// uses <s>, <small>, <b>, <i> or <u> is using elements the specification kept.
func TestModernMarkupIsNotReported(t *testing.T) {
	for _, doc := range []string{
		`<s>gone</s>`, `<small>fine print</small>`, `<b>bold</b>`, `<i>idiom</i>`,
		`<u>misspelt</u>`, `<abbr>WWW</abbr>`, `<pre>code</pre>`, `<code>x</code>`,
		`<kbd>k</kbd>`, `<samp>s</samp>`, `<ruby><rt>r</rt></ruby>`, `<iframe src="x"></iframe>`,
		`<object data="x"></object>`, `<embed src="x">`, `<img src="x.png">`,
		`<table><tr><td>x</td></tr></table>`, `<ul><li>x</li></ul>`,
	} {
		if res := report(t, doc); !res.OK() {
			t.Errorf("%q was reported: %v", doc, res.Findings)
		}
	}
}

// TestAnSVGImageIsNotObsolete, which the namespace answers: the HTML one is a spelling
// of img and the SVG one is an element of its own.
func TestAnSVGImageIsNotObsolete(t *testing.T) {
	res := report(t, `<svg><image xlink:href="x.png"/></svg>`)
	if !res.OK() {
		t.Errorf("the SVG image was reported: %v", res.Findings)
	}
	res = report(t, `<image src="x.png">`)
	f, ok := findingFor(res, "image")
	if !ok || f.Class != ParserAlias {
		t.Errorf("%v", res.Findings)
	}
	// Both in one document: one finding, from the HTML one.
	res = report(t, `<svg><image xlink:href="a.png"/></svg><image src="b.png">`)
	if len(res.Findings) != 1 || res.Findings[0].Class != ParserAlias {
		t.Errorf("%v", res.Findings)
	}
}

// TestObsoleteAttributesAreReportedWhereTheyAreObsolete, and not where the name is an
// ordinary word.
func TestObsoleteAttributesAreReportedWhereTheyAreObsolete(t *testing.T) {
	for _, tc := range []struct {
		doc, want string
	}{
		{`<td align="left">x</td>`, "@align"},
		{`<table bgcolor="#fff"></table>`, "@bgcolor"},
		{`<table cellpadding="2"></table>`, "@cellpadding"},
		{`<img src="x" hspace="4">`, "@hspace"},
		{`<iframe src="x" frameborder="0"></iframe>`, "@frameborder"},
		{`<body text="#000"></body>`, "@text"},
		{`<script language="JavaScript"></script>`, "@language"},
	} {
		res := report(t, tc.doc)
		if _, ok := findingFor(res, tc.want); !ok {
			t.Errorf("%q: %v, want %s", tc.doc, res.Findings, tc.want)
		}
	}
	// The same names elsewhere are not reported: they are only obsolete on the
	// elements that had them.
	for _, doc := range []string{
		`<div text="hello">x</div>`,
		`<my-widget link="/p"></my-widget>`,
		`<div language="en">x</div>`,
	} {
		if res := report(t, doc); !res.OK() {
			t.Errorf("%q was reported: %v", doc, res.Findings)
		}
	}
	// Case does not matter, and the document's spelling is kept for the report.
	res := report(t, `<TD ALIGN="LEFT">x</TD>`)
	f, ok := findingFor(res, "@align")
	if !ok {
		t.Fatalf("%v", res.Findings)
	}
	if f.Spelled != "ALIGN" {
		t.Errorf("Spelled = %q, want the document's spelling", f.Spelled)
	}
	if f.On != "td" {
		t.Errorf("On = %q, want td", f.On)
	}
}

// TestTheOffsetIsWhereTheTagIs, because a count with no position is a number nobody
// can act on.
func TestTheOffsetIsWhereTheTagIs(t *testing.T) {
	const doc = `<p>text</p><center>x</center><p>more</p><font>y</font>`
	res := report(t, doc)
	if len(res.Findings) != 2 {
		t.Fatalf("%v", res.Findings)
	}
	for _, f := range res.Findings {
		if got := doc[f.Offset : f.Offset+len(f.Name)+2]; got != "<"+f.Name+">" {
			t.Errorf("%s claims offset %d, where the document has %q", f.Name, f.Offset, got)
		}
	}
	// And the offsets do not depend on the chunking.
	for _, size := range []int{1, 3, 7, 64} {
		r := &reporter{}
		w, err := lolhtml.NewWriter(discard{}, r.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if len(r.res.Findings) != len(res.Findings) {
			t.Fatalf("chunks of %d: %v", size, r.res.Findings)
		}
		for i := range r.res.Findings {
			if r.res.Findings[i] != res.Findings[i] {
				t.Errorf("chunks of %d: %v, want %v", size, r.res.Findings[i], res.Findings[i])
			}
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestUsesGroupsAndCounts, since a page with forty fonts and one marquee needs them in
// that order.
func TestUsesGroupsAndCounts(t *testing.T) {
	doc := strings.Repeat(`<font>x</font>`, 5) + `<center>y</center>` + strings.Repeat(`<font>z</font>`, 2)
	res := report(t, doc)
	uses := res.Uses()
	if len(uses) != 2 {
		t.Fatalf("%v", uses)
	}
	if uses[0].Name != "font" || uses[0].Count != 7 {
		t.Errorf("%v", uses[0])
	}
	if uses[0].First != 0 {
		t.Errorf("font first at %d, want 0", uses[0].First)
	}
	if uses[1].Name != "center" || uses[1].Count != 1 {
		t.Errorf("%v", uses[1])
	}
}

// TestRawTextObsoletesAreStillFound, which is the one place a reporter could miss an
// element: xmp, listing and plaintext hold text rather than markup, and the elements
// themselves are still elements.
func TestRawTextObsoletesAreStillFound(t *testing.T) {
	for _, tag := range []string{"xmp", "listing", "plaintext"} {
		doc := `<p>a</p><` + tag + `><b>not markup</b>`
		res := report(t, doc)
		f, ok := findingFor(res, tag)
		if !ok {
			t.Errorf("<%s> was not reported: %v", tag, res.Findings)
			continue
		}
		if f.Class != Semantic {
			t.Errorf("<%s> is %v", tag, f.Class)
		}
		// The <b> inside is text, so it is not an element and not a finding.
		if len(res.Findings) != 1 {
			t.Errorf("<%s>: %v, want one finding", tag, res.Findings)
		}
	}
}

// TestTheSelectorNamesEverythingItLooksFor, so nothing in the tables is unreachable.
func TestTheSelectorNamesEverythingItLooksFor(t *testing.T) {
	sel := selector()
	for name := range Elements {
		if !strings.Contains(sel, name) {
			t.Errorf("%q is in Elements and not in the selector", name)
		}
	}
	for name := range Attributes {
		if !strings.Contains(sel, "["+name+"]") {
			t.Errorf("%q is in Attributes and not in the selector", name)
		}
	}
	// It is one handler: registration is per handler, not per clause.
	r := &reporter{}
	if got := len(r.options()); got != 1 {
		t.Errorf("the reporter registers %d handlers, want 1", got)
	}
	// And the selector is valid, which a table typo would break.
	if _, err := lolhtml.RewriteString(`<p>x</p>`, r.options()...); err != nil {
		t.Errorf("the selector does not parse: %v", err)
	}
}

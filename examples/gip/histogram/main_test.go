package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func count(t *testing.T, doc string) Report {
	t.Helper()
	rep, err := Count(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Count(%q): %v", doc, err)
	}
	return rep
}

// labels returns "label=count" for each row, in report order.
func labels(rep Report) []string {
	var out []string
	for _, k := range rep.Kinds {
		out = append(out, k.Label()+"="+itoa(k.Count))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestCounting(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		want  []string
		total int
	}{
		{"empty", ``, nil, 0},
		{"text only", `hello`, nil, 0},
		{"one", `<p>a</p>`, []string{"p=1"}, 1},
		{"repeated", `<p>a</p><p>b</p><p>c</p>`, []string{"p=3"}, 3},
		{"nested counts both", `<div><p>a</p></div>`, []string{"div=1", "p=1"}, 2},
		// Most frequent first, ties broken by name.
		{"ordering", `<b>1</b><b>2</b><a>x</a><c>y</c>`, []string{"b=2", "a=1", "c=1"}, 4},

		// Void elements are elements.
		{"void", `<br><br><img src=x>`, []string{"br=2", "img=1"}, 3},

		// A comment and a doctype are not.
		{"comments and doctype", `<!DOCTYPE html><!-- c --><p>a</p>`, []string{"p=1"}, 1},

		// Template content is parsed, so it counts.
		{"template", `<template><p>a</p></template>`, []string{"p=1", "template=1"}, 2},

		// Raw text is not markup: the <div> inside a script is characters.
		{"raw text", `<script>var x = "<div></div>"</script>`, []string{"script=1"}, 1},
		{"style", `<style>p{}</style><p>a</p>`, []string{"p=1", "style=1"}, 2},

		// An end tag is not a new element.
		{"end tags", `<p>a</p></p>`, []string{"p=1"}, 1},

		// An element with its end tag left out is still one element.
		{"implicit end tags", `<ul><li>a<li>b</ul>`, []string{"li=2", "ul=1"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := count(t, tt.doc)
			if got := labels(rep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
			if rep.Total != tt.total {
				t.Errorf("Total = %d, want %d", rep.Total, tt.total)
			}
		})
	}
}

// Tag names are matched without regard to case, so a page that writes both
// spellings has one kind of element - and the histogram says which spellings it
// found rather than hiding the inconsistency.
func TestCaseFoldingKeepsTheSpellings(t *testing.T) {
	rep := count(t, `<DIV></DIV><div></div><Div></Div>`)
	if len(rep.Kinds) != 1 {
		t.Fatalf("rows = %q, want one", labels(rep))
	}
	k := rep.Kinds[0]
	if k.Name != "div" || k.Count != 3 {
		t.Errorf("row = %+v, want div=3", k)
	}
	if want := []string{"DIV", "div", "Div"}; !reflect.DeepEqual(k.Spellings, want) {
		t.Errorf("Spellings = %q, want %q", k.Spellings, want)
	}

	// A single spelling that is already the name says nothing, so it is not
	// reported.
	rep = count(t, `<p>a</p><p>b</p>`)
	if rep.Kinds[0].Spellings != nil {
		t.Errorf("Spellings = %q for a consistently lower-case page, want none", rep.Kinds[0].Spellings)
	}
}

// An HTML <a> and an SVG <a> are different elements that a selector cannot tell
// apart. Adding their counts together produces a number that is not about
// anything, so the histogram keeps them in separate rows.
func TestNamespacesAreSeparateRows(t *testing.T) {
	rep := count(t, `<a href=x>one</a><svg><a href=y></a><rect/></svg><math><mi>x</mi></math>`)
	got := labels(rep)
	want := []string{"a=1", "math:math=1", "math:mi=1", "svg:a=1", "svg:rect=1", "svg:svg=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// Foreign content keeps the spelling a page used, and that spelling is the one
// that matters there: <linearGradient> and <lineargradient> are two different
// elements to anything but an HTML parser.
func TestForeignSpellingIsReported(t *testing.T) {
	rep := count(t, `<svg><linearGradient/><clipPath/></svg>`)
	for _, k := range rep.Kinds {
		switch k.Name {
		case "lineargradient":
			if !reflect.DeepEqual(k.Spellings, []string{"linearGradient"}) {
				t.Errorf("linearGradient spellings = %q", k.Spellings)
			}
		case "clippath":
			if !reflect.DeepEqual(k.Spellings, []string{"clipPath"}) {
				t.Errorf("clipPath spellings = %q", k.Spellings)
			}
		}
	}
}

func TestChart(t *testing.T) {
	rep := count(t, `<p>1</p><p>2</p><p>3</p><p>4</p><div></div>`)
	got := Chart(rep, 8)
	want := "p     4 ########\ndiv   1 ##\ntotal 5  in 2 kinds\n"
	if got != want {
		t.Errorf("Chart =\n%q\nwant\n%q", got, want)
	}
}

// A row that occurred cannot render as nothing: rounding one occurrence in a
// thousand down to zero columns reads as a row that did not happen.
func TestASingleOccurrenceStillDrawsABar(t *testing.T) {
	doc := strings.Repeat(`<p>x</p>`, 1000) + `<blink></blink>`
	out := Chart(count(t, doc), 10)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "blink") {
			if !strings.Contains(line, "#") {
				t.Errorf("the one blink drew no bar: %q", line)
			}
			return
		}
	}
	t.Error("no blink row")
}

func TestChartOfNothing(t *testing.T) {
	if got := Chart(count(t, ``), 10); got != "no elements\n" {
		t.Errorf("Chart = %q", got)
	}
}

// Where the reader breaks must not change the count, and a break inside a tag
// name is the case that would.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><DIV><p>a<p>b</DIV><svg><linearGradient/></svg>` +
		`<a href=x>y</a><script>var s = "<p>";</script></body></html>`
	want := count(t, doc)
	for n := 1; n <= len(doc); n++ {
		got, err := Count(&chunked{s: doc, n: n})
		if err != nil {
			t.Fatalf("chunk %d: %v", n, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d changed the report:\n got %+v\nwant %+v", n, got, want)
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

// An integration point switches back to HTML, so an HTML element inside one is
// an HTML element. This is the case that a program reading NamespaceURI off the
// element itself gets wrong in both directions: it puts <mi> in HTML and, if it
// then trusted the enclosing <svg>, would put this <p> in SVG.
func TestIntegrationPointsSwitchBack(t *testing.T) {
	tests := []struct {
		doc  string
		want []string
	}{
		{`<math><mi>x</mi></math>`, []string{"math:math=1", "math:mi=1"}},
		{`<svg><foreignObject><p>a</p></foreignObject></svg>`,
			[]string{"p=1", "svg:foreignobject=1", "svg:svg=1"}},
		{`<svg><title>t</title></svg>`, []string{"svg:svg=1", "svg:title=1"}},
		{`<svg><desc><div></div></desc></svg>`,
			[]string{"div=1", "svg:desc=1", "svg:svg=1"}},
		{`<math><mtext><b>x</b></mtext></math>`,
			[]string{"b=1", "math:math=1", "math:mtext=1"}},
		// Out again: the namespace is restored after the foreign root closes.
		{`<svg><rect/></svg><rect></rect>`, []string{"rect=1", "svg:rect=1", "svg:svg=1"}},
		{`<div><svg><g/></svg><g></g></div>`,
			[]string{"div=1", "g=1", "svg:g=1", "svg:svg=1"}},
		// Nested foreign content.
		{`<svg><foreignObject><svg><rect/></svg></foreignObject></svg>`,
			[]string{"svg:svg=2", "svg:foreignobject=1", "svg:rect=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.doc, func(t *testing.T) {
			if got := labels(count(t, tt.doc)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
		})
	}
}

// A breakout does not corrupt the stack. An HTML tag name inside an <svg> takes
// the parser out of foreign content, so the tags after it - still inside the source
// <svg> - report the HTML namespace and get pushed. Nothing closes those by name, so
// an unwind that popped the top entry would leave the svg one on the stack and label
// the whole rest of the document svg:.
func TestAForeignContentBreakoutDoesNotStrandTheStack(t *testing.T) {
	got := labels(count(t, `<svg><circle/><p>a</p><rect/></svg><div>x</div>`))
	want := []string{"div=1", "svg:circle=1", "svg:p=1", "svg:rect=1", "svg:svg=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// An unclosed foreign root keeps everything after it foreign, which is what a
// parser does too.
func TestAnUnclosedForeignRootStaysForeign(t *testing.T) {
	got := labels(count(t, `<svg><rect/><p>a</p>`))
	want := []string{"svg:p=1", "svg:rect=1", "svg:svg=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

package main

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func check(t *testing.T, doc string, max int) (Report, error) {
	t.Helper()
	rep, err := Check(strings.NewReader(doc), max)
	if err != nil && !errors.Is(err, ErrTooDeep) {
		t.Fatalf("Check(%q): %v", doc, err)
	}
	return rep, err
}

func TestDepth(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		depth int
		path  []string
	}{
		{"empty", ``, 0, nil},
		{"text only", `hello`, 0, nil},
		{"one element", `<p>a</p>`, 1, []string{"p"}},
		{"nested", `<div><span>a</span></div>`, 2, []string{"div", "span"}},
		{"siblings do not add", `<div>a</div><div>b</div>`, 1, []string{"div"}},
		{"deep", `<a><b><c><d>x</d></c></b></a>`, 4, []string{"a", "b", "c", "d"}},

		// The whole reason this program keeps its own stack: these two
		// documents describe the same tree and a token counter reports
		// different depths for them.
		{"explicit items", `<ul><li>a</li><li>b</li></ul>`, 2, []string{"ul", "li"}},
		{"implicit items", `<ul><li>a<li>b</ul>`, 2, []string{"ul", "li"}},
		{"many implicit items", `<ul>` + strings.Repeat(`<li>x`, 40) + `</ul>`, 2, []string{"ul", "li"}},

		{"implicit cells", `<table><tr><td>a<td>b</table>`, 3, []string{"table", "tr", "td"}},
		{"implicit rows", `<table><tr><td>a<tr><td>b</table>`, 3, []string{"table", "tr", "td"}},
		{"table sections", `<table><thead><tr><td>a<tbody><tr><td>b</table>`, 4,
			[]string{"table", "thead", "tr", "td"}},
		{"implicit options", `<select><option>a<option>b</select>`, 2, []string{"select", "option"}},
		{"optgroup", `<select><optgroup><option>a<optgroup><option>b</select>`, 3,
			[]string{"select", "optgroup", "option"}},
		{"implicit paragraphs", `<p>a<p>b`, 1, []string{"p"}},
		{"paragraph closed by div", `<p>a<div>b</div>`, 1, []string{"p"}},
		{"paragraph inside div", `<div><p>a<p>b</div>`, 2, []string{"div", "p"}},
		{"definition list", `<dl><dt>a<dd>b<dt>c<dd>d</dl>`, 2, []string{"dl", "dt"}},
		{"ruby", `<ruby>a<rt>b<rt>c</ruby>`, 2, []string{"ruby", "rt"}},

		// A void element has no content, so it cannot nest - but it is an
		// element at its own position and counts there.
		{"void", `<div><br></div>`, 2, []string{"div", "br"}},
		{"void alone", `<br>`, 1, []string{"br"}},
		{"image in a link", `<a><img src=x></a>`, 2, []string{"a", "img"}},

		// A self-closing element in foreign content really is closed.
		{"foreign self closing", `<svg><rect/></svg>`, 2, []string{"svg", "rect"}},
		{"foreign nested", `<svg><g><rect/></g></svg>`, 3, []string{"svg", "g", "rect"}},

		// A stray end tag closes nothing.
		{"stray end tag", `</div><p>a</p>`, 1, []string{"p"}},
		{"unmatched end tag", `<div><span>a</b></span></div>`, 2, []string{"div", "span"}},

		// An unclosed element stays open, which is what a browser does too.
		{"unclosed", `<div><span>a`, 2, []string{"div", "span"}},

		{"raw text", `<div><script>var x = "<div>"</script></div>`, 2, []string{"div", "script"}},
		{"template", `<template><div>a</div></template>`, 2, []string{"template", "div"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := check(t, tt.doc, 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rep.MaxDepth != tt.depth {
				t.Errorf("MaxDepth = %d, want %d (path %q)", rep.MaxDepth, tt.depth, rep.DeepestPath)
			}
			if !reflect.DeepEqual(rep.DeepestPath, tt.path) {
				t.Errorf("DeepestPath = %q, want %q", rep.DeepestPath, tt.path)
			}
		})
	}
}

// TestATokenCounterDrifts is the measurement behind the design. Counting start
// tags up and end tags down is the obvious implementation, and it is wrong by
// one per implicitly closed element - which on a list is one per item.
func TestATokenCounterDrifts(t *testing.T) {
	for _, n := range []int{1, 2, 5, 40} {
		doc := `<ul>` + strings.Repeat(`<li>x`, n) + `</ul>`
		rep, err := check(t, doc, 0)
		if err != nil {
			t.Fatal(err)
		}
		if rep.MaxDepth != 2 {
			t.Errorf("%d items: MaxDepth = %d, want 2", n, rep.MaxDepth)
		}
		if got, want := naiveDepth(t, doc), n+1; got != want {
			t.Errorf("%d items: the naive counter reports %d, and this test exists "+
				"because it reports %d", n, got, want)
		}
	}
}

// naiveDepth counts start tags up and end tags down, which is what a program
// written against the token stream does before it meets an implicit end tag.
func naiveDepth(t *testing.T, doc string) int {
	t.Helper()
	depth, max := 0, 0
	seen := map[int]bool{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		depth++
		if depth > max {
			max = depth
		}
		if !e.CanHaveContent() {
			depth--
			return nil
		}
		return e.OnEndTag(func(x *lolhtml.EndTag) error {
			at := x.SourceLocation().Start
			if seen[at] {
				return nil
			}
			seen[at] = true
			depth--
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return max
}

func TestBudget(t *testing.T) {
	const doc = `<div><div><div><span>x</span></div></div></div>`
	for _, tt := range []struct {
		max      int
		exceeded bool
	}{{0, false}, {10, false}, {4, false}, {3, true}, {1, true}} {
		rep, err := check(t, doc, tt.max)
		if got := errors.Is(err, ErrTooDeep); got != tt.exceeded {
			t.Errorf("max=%d: exceeded = %v (err %v), want %v", tt.max, got, err, tt.exceeded)
		}
		if tt.exceeded && rep.MaxDepth != tt.max+1 {
			t.Errorf("max=%d: stopped at depth %d, want %d", tt.max, rep.MaxDepth, tt.max+1)
		}
	}
}

// The budget stops the walk at the first element past it, so the report names
// the path that broke it rather than the deepest path in the document.
func TestTheReportNamesThePathThatBrokeTheBudget(t *testing.T) {
	const doc = `<div><p>shallow</p></div><section><article><h1><span><em>deep</em></span></h1></article></section>`
	rep, err := check(t, doc, 3)
	if !errors.Is(err, ErrTooDeep) {
		t.Fatalf("budget of 3 was not exceeded: %v", err)
	}
	want := []string{"section", "article", "h1", "span"}
	if !reflect.DeepEqual(rep.DeepestPath, want) {
		t.Errorf("DeepestPath = %q, want %q", rep.DeepestPath, want)
	}
	if !strings.Contains(err.Error(), "section > article > h1 > span") {
		t.Errorf("the error does not name the path: %v", err)
	}
}

// A budget that is not exceeded costs nothing extra and reports the real
// deepest path.
func TestAPassingDocumentIsStillMeasured(t *testing.T) {
	rep, err := check(t, `<div><span><em>x</em></span></div>`, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.MaxDepth != 3 || rep.Elements != 3 {
		t.Errorf("MaxDepth = %d, Elements = %d, want 3 and 3", rep.MaxDepth, rep.Elements)
	}
}

// The rewriter is a stream, so where the reader happens to break must not change
// the answer.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><ul><li>a<li>b</ul><table><tr><td>x<td>y</table>` +
		`<div><div><span>deep</span></div></div></body></html>`
	want, err := Check(strings.NewReader(doc), 0)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= len(doc); n++ {
		got, err := Check(&chunked{s: doc, n: n}, 0)
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

// Depth counts what the source contains. A browser wraps a fragment in html,
// head and body; those are not in the document and are not counted, which is a
// decision rather than an oversight.
func TestDepthCountsWhatTheSourceContains(t *testing.T) {
	bare, err := Check(strings.NewReader(`<p>a</p>`), 0)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Check(strings.NewReader(`<html><head></head><body><p>a</p></body></html>`), 0)
	if err != nil {
		t.Fatal(err)
	}
	if bare.MaxDepth != 1 {
		t.Errorf("bare fragment depth = %d, want 1", bare.MaxDepth)
	}
	if full.MaxDepth != 3 {
		t.Errorf("full document depth = %d, want 3", full.MaxDepth)
	}
}

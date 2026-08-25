package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func convert(t *testing.T, doc string) string {
	t.Helper()
	md, _, err := Convert(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Convert(%q): %v", doc, err)
	}
	return md
}

func TestConvert(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"empty", ``, ``},
		{"paragraph", `<p>text</p>`, `text`},
		{"two paragraphs", `<p>one</p><p>two</p>`, "one\n\ntwo"},
		{"headings", `<h1>a</h1><h2>b</h2><h6>c</h6>`, "# a\n\n## b\n\n###### c"},
		{"emphasis", `<p>a <em>b</em> c</p>`, `a *b* c`},
		{"strong", `<p>a <strong>b</strong> c</p>`, `a **b** c`},
		{"strikethrough", `<p><del>gone</del></p>`, `~~gone~~`},
		{"nested emphasis", `<p><strong>a <em>b</em></strong></p>`, `**a *b***`},
		{"code", "<p>call <code>f(x)</code></p>", "call `f(x)`"},
		{"link", `<p><a href="/x">text</a></p>`, `[text](/x)`},
		{"link with entity in href", `<p><a href="/a?b=1&amp;c=2">t</a></p>`, `[t](/a?b=1&c=2)`},
		{"br", `<p>a<br>b</p>`, "a\nb"},
		{"hr", `<p>a</p><hr><p>b</p>`, "a\n\n---\n\nb"},
		{"blockquote", `<blockquote>quoted</blockquote>`, `> quoted`},

		{"unordered list", `<ul><li>one</li><li>two</li></ul>`, "- one\n- two"},
		{"ordered list", `<ol><li>one</li><li>two</li></ol>`, "1. one\n2. two"},
		{"nested list", `<ul><li>a<ul><li>b</li></ul></li></ul>`, "- a\n  - b"},
		{"pre", "<pre>a *b*\n  c</pre>", "```\na *b*\n  c\n```"},

		// Escaping, in the opposite direction from the library's.
		{"markdown metacharacters", `<p>a*b_c[d]e</p>`, `a\*b\_c\[d\]e`},
		{"a leading hash", `<p># not a heading</p>`, `\# not a heading`},
		{"backslash", `<p>a\b</p>`, `a\\b`},
		{"no escaping in code", "<p><code>a*b_c</code></p>", "`a*b_c`"},
		{"no escaping in pre", "<pre>a*b</pre>", "```\na*b\n```"},

		// References are decoded, then escaped for Markdown if they need it.
		{"entity that needs escaping", `<p>2 &lt; 3</p>`, `2 \< 3`},
		{"entity that does not", `<p>a &amp; b</p>`, `a & b`},
		{"accented entity", `<p>caf&eacute;</p>`, `café`},

		// Content that is not prose.
		{"script", `<p>a</p><script>var x = 1</script><p>b</p>`, "a\n\nb"},
		{"style", `<style>p{}</style><p>a</p>`, `a`},
		{"title", `<html><head><title>t</title></head><body><p>a</p></body></html>`, `a`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convert(t, tt.doc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The three ways an inline element can end, which is the reason this program
// keeps its own stack. Only the first is what an end-tag callback alone would
// get right.
func TestWhereAnInlineElementEnds(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		// Its own end tag: the easy case.
		{"own end tag", `<p><em>a</em> b</p>`, `*a* b`},

		// An ancestor's end tag, which is exactly where the element ends.
		{"closed by an ancestor", `<p><em>a</p><p>b</p>`, "*a*\n\nb"},
		// A parser's adoption agency would keep the <em> open around "b" and
		// reopen it after the </strong>. This converter closes everything
		// inside the tag that arrived instead, which is a simplification it
		// makes deliberately: "a" is bold and italic, and "b" is plain.
		{"closed by an outer emphasis", `<p><strong><em>a</strong>b</p>`, `***a***b`},

		// A sibling's start tag, which ends the element two tokens before any
		// callback arrives. Emphasis must not reach the next item.
		{"closed by the next list item", `<ul><li><em>a<li>b</ul>`, "- *a*\n- b"},
		{"closed by the next paragraph", `<p><em>a<p>b`, "*a*\n\nb"},
		{"closed by a block", `<p><em>a<div>b</div>`, "*a*\n\nb"},

		// Never closed at all, so the document end has to close it.
		{"never closed", `<p><em>a`, `*a*`},
		{"nothing closed", `<p><strong><em>a`, `**​*a*​**`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convert(t, tt.doc)
			// The zero-width spaces in the last expectation are only there to
			// keep the literal readable; strip them before comparing.
			want := strings.ReplaceAll(tt.want, "​", "")
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// Emphasis that leaks past the element it belongs to is the failure this design
// exists to prevent, so it gets its own test with the count spelled out.
func TestEmphasisDoesNotLeak(t *testing.T) {
	got := convert(t, `<ul><li><em>first<li>second<li>third</ul>`)
	want := "- *first*\n- second\n- third"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if n := strings.Count(got, "*"); n != 2 {
		t.Errorf("%d asterisks in %q; the emphasis is meant to close after the "+
			"first item", n, got)
	}
}

// Unsupported tags keep their text and are reported, because silently dropping
// a table is worse than saying so.
func TestDroppedTagsAreReported(t *testing.T) {
	md, dropped, err := Convert(strings.NewReader(
		`<p>a</p><table><tr><td>cell</table><figure><img src=x><figcaption>cap</figcaption></figure>`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "cell") || !strings.Contains(md, "cap") {
		t.Errorf("the text of the dropped elements is missing: %q", md)
	}
	want := []string{"figcaption", "figure", "img", "table", "td", "tr"}
	sorted := append([]string(nil), dropped...)
	sortStrings(sorted)
	if !reflect.DeepEqual(sorted, want) {
		t.Errorf("dropped = %v, want %v", sorted, want)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// The output must not depend on how the input was written.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<h1>Title</h1><p>Some <em>emph</em> and <code>a*b</code>.</p>`,
		`<ul><li>one<li><a href="/x?a=1&amp;b=2">two</a></ul>`,
		"<pre>raw *text*\nkept</pre><p>2 &lt; 3</p>",
		`<p><strong><em>deep</em></strong></p>`,
		`<ol><li>a<ol><li>b</ol><li>c</ol>`,
	}
	for _, doc := range docs {
		want := convert(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			md, _, err := Convert(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if md != want {
				t.Fatalf("writes of %d changed the output:\n got %q\nwant %q", n, md, want)
			}
		}
	}
}

// Every delimiter opened has to be closed, whatever the document does. This is
// the invariant behind the stack, checked by counting rather than by comparing
// against an expected string.
func TestDelimitersBalance(t *testing.T) {
	docs := []string{
		`<p><em>a`,
		`<p><strong>a`,
		`<p><em><strong>a`,
		`<ul><li><em>a<li><strong>b`,
		`<p><code>a`,
		`<p><del>a`,
		`<p><em>a</p><p><em>b`,
		`<div><em>a</div><em>b`,
	}
	for _, doc := range docs {
		got := convert(t, doc)
		for _, d := range []string{"*", "`", "~"} {
			if n := strings.Count(got, d); n%2 != 0 {
				t.Errorf("%q gave %q: an odd number of %q", doc, got, d)
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

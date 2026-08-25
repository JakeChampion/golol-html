package main

import (
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func convert(t *testing.T, doc string) string {
	t.Helper()
	out, err := Convert(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Convert(%q): %v", doc, err)
	}
	return out
}

func TestConvert(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"empty", ``, ``},
		{"bare text", `hello`, `hello`},
		{"one paragraph", `<p>hello</p>`, `hello`},
		{"two paragraphs", `<p>one</p><p>two</p>`, "one\n\ntwo"},
		{"inline markup stays on the line", `<p>one <b>two</b> three</p>`, `one two three`},
		{"br", `a<br>b`, "a\nb"},
		{"div", `<div>a</div><div>b</div>`, "a\nb"},
		{"heading then paragraph", `<h1>Title</h1><p>Body</p>`, "Title\n\nBody"},

		// Whitespace is collapsed the way a renderer collapses it.
		{"runs of spaces", `<p>a    b</p>`, `a b`},
		{"newlines in source", "<p>a\n  b</p>", `a b`},
		{"leading and trailing", `<p>   a   </p>`, `a`},
		{"tabs", "<p>a\t\tb</p>", `a b`},

		// References are decoded, including one that is not a reference.
		{"entity", `<p>caf&eacute; &amp; more</p>`, `café & more`},
		{"numeric entity", `<p>&#233; &#x2603;</p>`, "é ☃"},
		// Not a reference at all, and left alone.
		{"not a reference", `<p>a &zzz; b</p>`, `a &zzz; b`},
		// This one looks like it is not a reference and is: "&not" is on the
		// list HTML decodes without a semicolon, so a browser shows the same
		// thing. Worth a case because it reads as a bug in the decoder.
		{"legacy reference without a semicolon", `<p>a &notanentity; b</p>`, `a ¬anentity; b`},

		// Content that is not prose is skipped.
		{"script", `<p>a</p><script>var x = 1</script><p>b</p>`, "a\n\nb"},
		{"style", `<style>p{color:red}</style><p>a</p>`, `a`},
		{"head", `<html><head><title>t</title><meta charset="utf-8"></head><body>a</body></html>`, `a`},
		{"template", `<template><p>hidden</p></template><p>shown</p>`, `shown`},
		{"noscript", `<noscript>fallback</noscript><p>a</p>`, `a`},
		{"select", `<select><option>x</option></select><p>a</p>`, `a`},

		// pre keeps its whitespace.
		{"pre", "<pre>a  b\n  c</pre>", "a  b\n  c"},
		{"pre inside a div", "<div><pre>  x  </pre></div>", "  x  "},

		// The list items that have no end tag in the source.
		{"implicit list items", `<ul><li>one<li>two<li>three</ul>`, "one\ntwo\nthree"},
		{"explicit list items", `<ul><li>one</li><li>two</li></ul>`, "one\ntwo"},
		{"implicit table cells", `<table><tr><td>a<td>b<tr><td>c</table>`, "a\nb\nc"},
		{"implicit paragraphs", `<p>a<p>b`, "a\n\nb"},

		// No leading or trailing blank lines, whatever the document starts with.
		{"leading block", `<div><p>a</p></div>`, `a`},
		{"trailing blocks", `<p>a</p><div></div><p></p>`, `a`},
		{"nothing but blocks", `<div></div><p></p><ul><li></li></ul>`, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convert(t, tt.doc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The output must not depend on how the input was written. Every mechanism in
// this program that carries state between chunks is here to make that true.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<h1>Hello &amp; welcome</h1><p>First   para with  wrapping.</p>`,
		`<ul><li>one<li>two</ul><pre>  kept
  as is</pre><p>Last.</p>`,
		`<p>caf&eacute; and &#233; and &amp;amp;</p>`,
		`<p>` + strings.Repeat("word ", 50) + `</p>`,
		`<p>a</p><script>var s = "</p>"</script><p>b</p>`,
		`<div><p>a</p><table><tr><td>x<td>y</table></div>`,
	}
	for _, doc := range docs {
		want := convert(t, doc)
		for _, n := range []int{1, 2, 3, 5, 7, 64} {
			got, err := Convert(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if got != want {
				t.Fatalf("writes of %d changed the output:\n got %q\nwant %q", n, got, want)
			}
		}
	}
}

// A chunk never splits a character, but it splits everything larger - including
// a character reference. This is the measurement behind decoding per node
// instead of per chunk: html.UnescapeString applied to each chunk leaves the
// pieces of "&amp;" alone.
func TestAReferenceSplitAcrossChunksNeedsAccumulating(t *testing.T) {
	const doc = `<p>a &amp; b</p>`

	// Per chunk, one byte at a time: the reference arrives in pieces and
	// decoding each one does nothing.
	var perChunk strings.Builder
	w, err := lolhtml.NewWriter(io.Discard, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		perChunk.WriteString(stdhtml.UnescapeString(c.Text()))
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i++ {
		if _, err := w.Write([]byte(doc[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := perChunk.String(); got != `a &amp; b` {
		t.Errorf("decoding per chunk gave %q; this test records that it does not "+
			"decode a split reference", got)
	}

	// This program accumulates the node first, so it gets the same answer at
	// every write size.
	for _, n := range []int{1, 2, 3, len(doc)} {
		got, err := Convert(&chunked{s: doc, n: n})
		if err != nil {
			t.Fatal(err)
		}
		if got != `a & b` {
			t.Errorf("writes of %d: got %q, want %q", n, got, `a & b`)
		}
	}
}

// Breaks are emitted before a block begins rather than when it ends, because an
// element whose end tag the source leaves out has no end tag token of its own.
// This is the test that fails if that decision is reversed.
func TestBreaksDoNotDependOnEndTags(t *testing.T) {
	pairs := []struct{ implicit, explicit string }{
		{`<ul><li>one<li>two</ul>`, `<ul><li>one</li><li>two</li></ul>`},
		{`<table><tr><td>a<td>b</table>`, `<table><tr><td>a</td><td>b</td></tr></table>`},
		{`<p>a<p>b`, `<p>a</p><p>b</p>`},
		{`<dl><dt>a<dd>b`, `<dl><dt>a</dt><dd>b</dd></dl>`},
	}
	for _, p := range pairs {
		implicit, explicit := convert(t, p.implicit), convert(t, p.explicit)
		if implicit != explicit {
			t.Errorf("%q gave %q but %q gave %q; the two describe the same document",
				p.implicit, implicit, p.explicit, explicit)
		}
	}
}

// A word broken across two chunks must not gain a space, and two spaces broken
// across two chunks must not become two spaces. Both are the collapsing state
// surviving the boundary.
func TestCollapsingSurvivesAChunkBoundary(t *testing.T) {
	for _, doc := range []string{`<p>hello world</p>`, `<p>hello  world</p>`, "<p>hello \n world</p>"} {
		want := `hello world`
		for _, n := range []int{1, 2, 3, 6, 7} {
			got, err := Convert(&chunked{s: doc, n: n})
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%q at writes of %d: got %q, want %q", doc, n, got, want)
			}
		}
	}
}

// Nested skipped elements have to count in and out, or a script inside a
// template turns the rest of the document off.
func TestSkippingNests(t *testing.T) {
	tests := []struct{ doc, want string }{
		{`<template><script>x</script></template><p>a</p>`, `a`},
		{`<head><style>x</style></head><body><p>a</p></body>`, `a`},
		{`<noscript><noscript>x</noscript></noscript><p>a</p>`, `a`},
		{`<p>a</p><script>x</script><p>b</p><style>y</style><p>c</p>`, "a\n\nb\n\nc"},
	}
	for _, tt := range tests {
		if got := convert(t, tt.doc); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.doc, got, tt.want)
		}
	}
}

// An unclosed script swallows the rest of the document, which is what a parser
// does too - so the converter agreeing with it is the correct answer rather than
// a bug to work around.
func TestAnUnclosedScriptSwallowsTheRest(t *testing.T) {
	if got := convert(t, `<p>a</p><script>var x = 1`); got != `a` {
		t.Errorf("got %q, want %q", got, `a`)
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

package main

import (
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func collect(t *testing.T, doc string) []string {
	t.Helper()
	g, err := Collect(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Collect(%q): %v", doc, err)
	}
	var out []string
	for _, term := range g {
		out = append(out, term.Text)
	}
	sort.Strings(out)
	return out
}

func rewrite(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Rewrite(&b, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", doc, err)
	}
	return b.String(), res
}

func TestCollect(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{"one term", `<dl><dt>alpha<dd>first</dl>`, []string{"alpha"}},
		{"two terms", `<dl><dt>alpha<dd>a<dt>beta<dd>b</dl>`, []string{"alpha", "beta"}},
		{"explicit end tags", `<dl><dt>alpha</dt><dd>a</dd></dl>`, []string{"alpha"}},
		{"inline markup in a term", `<dl><dt>the <b>alpha</b><dd>a</dl>`, []string{"the alpha"}},
		{"entities decoded", `<dl><dt>caf&eacute;<dd>a</dl>`, []string{"café"}},
		{"whitespace collapsed", "<dl><dt>  two   words  <dd>a</dl>", []string{"two words"}},
		{"empty term ignored", `<dl><dt><dd>a<dt>beta<dd>b</dl>`, []string{"beta"}},
		{"duplicate terms once", `<dl><dt>alpha<dd>a<dt>alpha<dd>b</dl>`, []string{"alpha"}},
		{"no definition list", `<p>nothing</p>`, nil},
		{"unclosed list", `<dl><dt>alpha<dd>a`, []string{"alpha"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collect(t, tt.doc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("terms = %q, want %q", got, tt.want)
			}
		})
	}
}

// A term defined at the end of the document has to link mentions that came
// before it, which is the whole reason for two passes.
func TestATermDefinedLastLinksMentionsBefore(t *testing.T) {
	out, res := rewrite(t, `<p>alpha here</p><dl><dt>alpha<dd>a</dl>`)
	if res.Linked != 1 {
		t.Fatalf("linked %d mentions, want 1: %q", res.Linked, out)
	}
	if !strings.Contains(out, `<p><a href="#term-alpha" class="glossary">alpha</a> here</p>`) {
		t.Errorf("the mention before the list was not linked: %q", out)
	}
	if !res.SecondPass {
		t.Error("SecondPass = false")
	}
}

// With no terms there is nothing to link and no second pass to pay for, and the
// document has to come through untouched.
func TestNoTermsMeansOnePass(t *testing.T) {
	const doc = `<p>text with <a href="/x">a link</a> and <code>code</code></p>`
	out, res := rewrite(t, doc)
	if res.SecondPass {
		t.Error("SecondPass = true for a document with no glossary")
	}
	if out != doc {
		t.Errorf("the document changed:\n got %q\nwant %q", out, doc)
	}
}

func TestWhereTermsAreNotLinked(t *testing.T) {
	// Every one of these contains "alpha" somewhere it must not be linked.
	tests := []struct{ name, doc string }{
		{"inside an existing link", `<p><a href="/x">alpha</a></p><dl><dt>alpha<dd>a</dl>`},
		{"inside the definition list", `<dl><dt>alpha<dd>alpha again</dl>`},
		{"inside code", `<p><code>alpha</code></p><dl><dt>alpha<dd>a</dl>`},
		{"inside pre", `<pre>alpha</pre><dl><dt>alpha<dd>a</dl>`},
		{"inside a heading", `<h2>alpha</h2><dl><dt>alpha<dd>a</dl>`},
		{"inside a script", `<script>var alpha = 1</script><dl><dt>alpha<dd>a</dl>`},
		{"inside a textarea", `<textarea>alpha</textarea><dl><dt>alpha<dd>a</dl>`},
		{"inside a title", `<title>alpha</title><dl><dt>alpha<dd>a</dl>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _ := rewrite(t, tt.doc)
			if strings.Contains(out, "class=\"glossary\"") {
				t.Errorf("a link was inserted: %q", out)
			}
		})
	}
}

func TestMatching(t *testing.T) {
	tests := []struct {
		name   string
		doc    string
		linked int
	}{
		{"whole word only", `<p>alphabet</p><dl><dt>alpha<dd>a</dl>`, 0},
		{"word after punctuation", `<p>(alpha)</p><dl><dt>alpha<dd>a</dl>`, 1},
		{"case insensitive", `<p>Alpha ALPHA</p><dl><dt>alpha<dd>a</dl>`, 2},
		{"every occurrence", `<p>alpha alpha alpha</p><dl><dt>alpha<dd>a</dl>`, 3},
		{"longest term wins", `<p>streaming rewrite here</p><dl><dt>streaming<dd>a<dt>streaming rewrite<dd>b</dl>`, 1},
		{"hyphen is part of a word", `<p>alpha-beta</p><dl><dt>alpha<dd>a</dl>`, 0},
		{"multi-word term", `<p>a streaming rewrite</p><dl><dt>streaming rewrite<dd>a</dl>`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := rewrite(t, tt.doc)
			if res.Linked != tt.linked {
				t.Errorf("linked %d, want %d: %q", res.Linked, tt.linked, out)
			}
		})
	}
}

// Text with no term in it must come through unchanged. This is the case the
// first version of this program got wrong: it removed the earlier chunks of a
// node to accumulate it and then returned without writing them back.
func TestTextWithNoTermIsUntouched(t *testing.T) {
	out, _ := rewrite(t, `<p>See also <b>this</b> and that.</p><dl><dt>alpha<dd>a</dl>`)
	if !strings.Contains(out, `<p>See also <b>this</b> and that.</p>`) {
		t.Errorf("text was lost or altered: %q", out)
	}
}

// A character reference in the text must not be escaped again, which is what
// keeps the round trip through the linker honest.
func TestReferencesSurviveTheRewrite(t *testing.T) {
	out, res := rewrite(t, `<p>caf&eacute; and alpha &amp; more</p><dl><dt>alpha<dd>a</dl>`)
	if res.Linked != 1 {
		t.Fatalf("linked %d, want 1: %q", res.Linked, out)
	}
	if !strings.Contains(out, `caf&eacute;`) || !strings.Contains(out, `&amp; more`) {
		t.Errorf("references were altered: %q", out)
	}
	if strings.Contains(out, `&amp;eacute;`) || strings.Contains(out, `&amp;amp;`) {
		t.Errorf("references were escaped twice: %q", out)
	}
}

// The output must not depend on how the input was written, including when a term
// is split across chunks.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>A streaming rewrite is not a DOM.</p><dl><dt>streaming<dd>a<dt>DOM<dd>b</dl>`,
		`<p>See also <a href=/x>streaming</a> and <code>streaming</code>.</p><dl><dt>streaming<dd>a</dl>`,
		`<p>caf&eacute; and alpha</p><dl><dt>alpha<dd>a</dl>`,
		`<p>no terms at all</p>`,
	}
	for _, doc := range docs {
		want, wantRes := rewrite(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			got, err := Rewrite(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || got.Linked != wantRes.Linked {
				t.Fatalf("%q at writes of %d:\n got %q (%d linked)\nwant %q (%d linked)",
					doc, n, b.String(), got.Linked, want, wantRes.Linked)
			}
		}
	}
}

// Running it twice must not double the links, which is what the "not inside an
// existing link" rule buys.
func TestRewritingTwiceIsStable(t *testing.T) {
	const doc = `<p>alpha here</p><dl><dt>alpha<dd>a</dl>`
	once, _ := rewrite(t, doc)
	twice, res := rewrite(t, once)
	if twice != once {
		t.Errorf("the second run changed it:\n once  %q\n twice %q", once, twice)
	}
	if res.Linked != 0 {
		t.Errorf("the second run linked %d more mentions", res.Linked)
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

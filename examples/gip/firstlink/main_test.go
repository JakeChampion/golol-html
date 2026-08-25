package main

import (
	"io"
	"strings"
	"testing"
)

func rewrite(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Rewrite(&b, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", doc, err)
	}
	return b.String(), res
}

func links(s string) int { return strings.Count(s, `class="glossary"`) }

// One link per term, however many times it is mentioned.
func TestOnlyTheFirstMentionIsLinked(t *testing.T) {
	tests := []struct {
		name, doc string
		want      int
	}{
		{"one mention", `<p>alpha</p><dl><dt>alpha<dd>a</dl>`, 1},
		{"three mentions", `<p>alpha alpha alpha</p><dl><dt>alpha<dd>a</dl>`, 1},
		{"mentions in different paragraphs", `<p>alpha</p><p>alpha</p><dl><dt>alpha<dd>a</dl>`, 1},
		{"two terms", `<p>alpha beta alpha beta</p><dl><dt>alpha<dd>a<dt>beta<dd>b</dl>`, 2},
		{"no mention", `<p>nothing</p><dl><dt>alpha<dd>a</dl>`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := rewrite(t, tt.doc)
			if got := links(out); got != tt.want {
				t.Errorf("%d links, want %d: %q", got, tt.want, out)
			}
			if res.Linked != tt.want {
				t.Errorf("Result.Linked = %d, want %d", res.Linked, tt.want)
			}
		})
	}
}

// The one that needs two passes: the page's own link may come after the mention
// this program would otherwise take, and it still suppresses it.
func TestATermThePageLinksIsLeftAlone(t *testing.T) {
	tests := []struct{ name, doc string }{
		{"the page links it later", `<p>alpha here</p><p><a href="#x">alpha</a></p><dl><dt>alpha<dd>a</dl>`},
		{"the page links it earlier", `<p><a href="#x">alpha</a></p><p>alpha here</p><dl><dt>alpha<dd>a</dl>`},
		{"the link text contains the term", `<p>alpha here</p><p><a href="#x">see alpha now</a></p><dl><dt>alpha<dd>a</dl>`},
		{"the link is after the glossary", `<p>alpha here</p><dl><dt>alpha<dd>a</dl><p><a href="#x">alpha</a></p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := rewrite(t, tt.doc)
			if got := links(out); got != 0 {
				t.Errorf("%d links inserted: %q", got, out)
			}
			if res.Skipped != 1 {
				t.Errorf("Skipped = %d, want 1", res.Skipped)
			}
		})
	}

	// A link whose text merely contains the letters is not the term.
	out, res := rewrite(t, `<p>alpha here</p><p><a href="#x">alphabet</a></p><dl><dt>alpha<dd>a</dl>`)
	if links(out) != 1 || res.Linked != 1 {
		t.Errorf("a link to \"alphabet\" suppressed \"alpha\": %q", out)
	}
}

// Every term ends up in exactly one of the three outcomes.
func TestTheOutcomesAccountForEveryTerm(t *testing.T) {
	_, res := rewrite(t, `<p>alpha</p><p><a href="#x">beta</a></p>`+
		`<dl><dt>alpha<dd>a<dt>beta<dd>b<dt>gamma<dd>c</dl>`)
	if len(res.Terms) != 3 {
		t.Fatalf("%d terms, want 3", len(res.Terms))
	}
	if res.Linked != 1 || res.Skipped != 1 || res.Absent != 1 {
		t.Errorf("linked=%d skipped=%d absent=%d, want one of each",
			res.Linked, res.Skipped, res.Absent)
	}
	if res.Linked+res.Skipped+res.Absent != len(res.Terms) {
		t.Error("the outcomes do not add up to the number of terms")
	}
	summary := res.Summary()
	for _, want := range []string{"alpha", "beta", "gamma", "1 linked"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary does not mention %q: %s", want, summary)
		}
	}
}

func TestWhereNothingIsLinked(t *testing.T) {
	for _, doc := range []string{
		`<p><code>alpha</code></p><dl><dt>alpha<dd>a</dl>`,
		`<h2>alpha</h2><dl><dt>alpha<dd>a</dl>`,
		`<pre>alpha</pre><dl><dt>alpha<dd>a</dl>`,
		`<script>var alpha = 1</script><dl><dt>alpha<dd>a</dl>`,
		`<dl><dt>alpha<dd>alpha again</dl>`,
	} {
		out, _ := rewrite(t, doc)
		if links(out) != 0 {
			t.Errorf("%q gave %q", doc, out)
		}
	}
}

// Text with no term must come through unchanged, which is what makes the
// write-back-either-way rule necessary.
func TestUnmatchedTextIsUntouched(t *testing.T) {
	const doc = `<p>See also <b>this</b> and that.</p><dl><dt>alpha<dd>a</dl>`
	out, _ := rewrite(t, doc)
	if !strings.Contains(out, `<p>See also <b>this</b> and that.</p>`) {
		t.Errorf("text was lost or altered: %q", out)
	}
	// And a document with no glossary at all comes through byte for byte.
	const plain = `<p>text with <a href="/x">a link</a></p>`
	out, res := rewrite(t, plain)
	if out != plain {
		t.Errorf("got %q, want %q", out, plain)
	}
	if len(res.Terms) != 0 {
		t.Errorf("%d terms found in a document with no list", len(res.Terms))
	}
}

func TestReferencesSurvive(t *testing.T) {
	out, res := rewrite(t, `<p>caf&eacute; and alpha &amp; more</p><dl><dt>alpha<dd>a</dl>`)
	if res.Linked != 1 {
		t.Fatalf("linked %d, want 1: %q", res.Linked, out)
	}
	if !strings.Contains(out, `caf&eacute;`) || !strings.Contains(out, `&amp; more`) {
		t.Errorf("references were altered: %q", out)
	}
	if strings.Contains(out, `&amp;amp;`) {
		t.Errorf("a reference was escaped twice: %q", out)
	}
}

func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>A streaming rewrite streams. A DOM is a DOM.</p><p>See <a href="#term-dom">DOM</a>.</p>` +
			`<dl><dt>streaming<dd>a<dt>DOM<dd>b<dt>absent<dd>c</dl>`,
		`<p>alpha alpha</p><dl><dt>alpha<dd>a</dl>`,
		`<p>caf&eacute; alpha</p><dl><dt>alpha<dd>a</dl>`,
		`<p>no terms here</p>`,
	}
	for _, doc := range docs {
		want, wantRes := rewrite(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Rewrite(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Linked != wantRes.Linked {
				t.Fatalf("%q at writes of %d:\n got %q (%d linked)\nwant %q (%d linked)",
					doc, n, b.String(), res.Linked, want, wantRes.Linked)
			}
		}
	}
}

// Running it again must change nothing: the link it added the first time is now
// a link the page has, which is exactly what the second pass looks for.
func TestRunningItTwiceChangesNothing(t *testing.T) {
	const doc = `<p>alpha here and alpha again</p><dl><dt>alpha<dd>a</dl>`
	once, res1 := rewrite(t, doc)
	if res1.Linked != 1 {
		t.Fatalf("first run linked %d, want 1", res1.Linked)
	}
	twice, res2 := rewrite(t, once)
	if twice != once {
		t.Errorf("the second run changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Linked != 0 || res2.Skipped != 1 {
		t.Errorf("second run: linked=%d skipped=%d, want 0 and 1", res2.Linked, res2.Skipped)
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

package main

import (
	"bytes"
	"strings"
	"testing"
)

const base = "https://example.com/blog/"

// corpus is shared by the tests that make a claim about every document rather
// than about one case.
var corpus = []string{
	`<a href="/x">t</a>`,
	`<!DOCTYPE html><html><head><link href="a.css"></head><body><img src="i.png"></body></html>`,
	`<img srcset="a.png 1x, b.png 2x, c.png 3x" src="a.png">`,
	`<base href="/deep/"><a href="x">t</a>`,
	`<form action="s"><input formaction="t"><button formaction="u">g</button></form>`,
	`<a href="mailto:a@b.c">m</a><a href="javascript:void(0)">j</a><a href="data:,x">d</a>`,
	`<a href="/a?x=1&amp;y=2">amp</a>`,
	`<a href="/a%zz">bad</a>`,
	`<!--[if IE]><a href="/ie">i</a><![endif]--><a href="/ok">o</a>`,
	`<div><div><div><a href="deep">d</a>`,
	`<video poster="p.jpg"><source srcset="s.png 1x"><track src="t.vtt"></video>`,
	`<blockquote cite="c.html">q</blockquote>`,
	`<a href="">empty</a><a>none</a><a href="   ">blank</a>`,
	`<p>caf&eacute; text with no urls at all</p>`,
	`<a href="/x">t</a` + strings.Repeat("y", 100),
	``,
}

// TestChunkInvariance is the property that makes the streaming API usable: the
// output must not depend on how the input was split. A handler that accumulates
// state across chunks, or an attribute read that assumes the whole tag arrived
// in one write, breaks here and nowhere else.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := rewriteString(doc, base, true, false)
		if err != nil {
			t.Fatalf("whole write of %q: %v", doc, err)
		}

		for _, size := range []int{1, 2, 3, 7, 64} {
			var out bytes.Buffer
			rep, err := runChunked(doc, &out, size)
			if err != nil {
				t.Fatalf("chunked(%d) write of %q: %v", size, doc, err)
			}
			if got := out.String(); got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					size, doc, whole, got)
			}
			_ = rep
		}
	}
}

// TestIdempotent pins the property the whole program rests on: an absolute URL
// is a fixed point. If a second pass changes anything, the first pass produced a
// URL this program does not consider absolute, which would compound over a site
// build that runs twice.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := rewriteString(doc, base, true, false)
		if err != nil {
			t.Fatalf("first pass of %q: %v", doc, err)
		}
		twice, rep, err := rewriteString(once, base, true, false)
		if err != nil {
			t.Fatalf("second pass of %q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if rep.Rewritten != 0 {
			t.Errorf("second pass of %q rewrote %d URL(s); all should be absolute already",
				doc, rep.Rewritten)
		}
	}
}

// TestNonFetchSchemesAreLeftAlone: resolving mailto: or javascript: against an
// http base would silently break every such link in the document.
func TestNonFetchSchemesAreLeftAlone(t *testing.T) {
	for _, href := range []string{
		"mailto:someone@example.org",
		"tel:+441234567890",
		"javascript:void(0)",
		"data:text/plain,hello",
		"about:blank",
		"https://other.example/already",
		"HTTPS://other.example/upper",
	} {
		in := `<a href="` + href + `">t</a>`
		out, rep, err := rewriteString(in, base, true, false)
		if err != nil {
			t.Fatalf("%s: %v", href, err)
		}
		if out != in {
			t.Errorf("%s was rewritten: %q", href, out)
		}
		if rep.Rewritten != 0 || rep.AlreadyAbs != 1 {
			t.Errorf("%s: rewritten=%d absolute=%d, want 0 and 1",
				href, rep.Rewritten, rep.AlreadyAbs)
		}
	}
}

// TestFragmentAndProtocolRelative are the two shapes most easily got wrong: one
// looks like it has no URL in it, the other looks absolute but is not.
func TestFragmentAndProtocolRelative(t *testing.T) {
	tests := []struct{ in, want string }{
		{`<a href="#top">t</a>`, `<a href="https://example.com/blog/#top">t</a>`},
		{`<a href="//cdn.example/x">t</a>`, `<a href="https://cdn.example/x">t</a>`},
		{`<a href="?q=1">t</a>`, `<a href="https://example.com/blog/?q=1">t</a>`},
		{`<a href="../up">t</a>`, `<a href="https://example.com/up">t</a>`},
	}
	for _, tt := range tests {
		got, _, err := rewriteString(tt.in, base, true, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("%s\n got: %s\nwant: %s", tt.in, got, tt.want)
		}
	}
}

// TestSrcsetKeepsDescriptors: a srcset holds a list, and dropping a descriptor
// silently changes which image the browser picks.
func TestSrcsetKeepsDescriptors(t *testing.T) {
	in := `<img srcset="a.png 1x,  b.png 2x ,c.png 640w" src="a.png">`
	want := `<img srcset="https://example.com/blog/a.png 1x,  https://example.com/blog/b.png 2x ,https://example.com/blog/c.png 640w" src="https://example.com/blog/a.png">`
	got, rep, err := rewriteString(in, base, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if rep.Rewritten != 4 {
		t.Errorf("rewritten=%d, want 4 (three candidates and the src)", rep.Rewritten)
	}
}

// TestUnresolvedAnnotationCannotInjectMarkup: the annotation carries a URL from
// the input document, so it is untrusted. Inserted with lolhtml.Text it must be
// escaped no matter what it says.
func TestUnresolvedAnnotationCannotInjectMarkup(t *testing.T) {
	in := `<a href="/a%zz<script>alert(1)</script>">t</a>`
	got, rep, err := rewriteString(in, base, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unresolved != 1 {
		t.Fatalf("unresolved=%d, want 1", rep.Unresolved)
	}

	after := got[strings.Index(got, "</a>")+len("</a>"):]
	if strings.Contains(after, "<script>") {
		t.Errorf("annotation injected markup: %q", after)
	}
	if !strings.Contains(after, "&lt;script&gt;") {
		t.Errorf("annotation is not escaped as expected: %q", after)
	}
}

// TestCharacterReferencesSurvive: attribute values arrive as raw source, so an
// href holding &amp; must come back out holding &amp; and not a bare &, which
// would change the query the browser sends.
func TestCharacterReferencesSurvive(t *testing.T) {
	in := `<a href="/a?x=1&amp;y=2">t</a>`
	want := `<a href="https://example.com/a?x=1&amp;y=2">t</a>`
	got, _, err := rewriteString(in, base, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

// TestBaseHrefAppliesToLaterElementsOnly records what streaming costs: a <base>
// cannot reach back to URLs already emitted, and the program has to say so
// rather than quietly get them wrong.
func TestBaseHrefAppliesToLaterElementsOnly(t *testing.T) {
	in := `<link href="/early.css"><base href="/deep/dir/"><a href="late.html">t</a>`
	got, rep, err := rewriteString(in, base, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `href="https://example.com/early.css"`) {
		t.Errorf("early URL should use the original base: %s", got)
	}
	if !strings.Contains(got, `href="https://example.com/deep/dir/late.html"`) {
		t.Errorf("late URL should use the base element: %s", got)
	}
	if rep.BaseAfter != 1 {
		t.Errorf("BaseAfter=%d, want 1", rep.BaseAfter)
	}
}

// TestEmptyAndMissingAttributes: an empty href means the document itself, and
// rewriting it to the base would change what it points at.
func TestEmptyAndMissingAttributes(t *testing.T) {
	in := `<a href="">e</a><a>n</a><a href="  ">b</a>`
	got, rep, err := rewriteString(in, base, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("changed: %s", got)
	}
	if rep.Rewritten != 0 || rep.Unresolved != 0 {
		t.Errorf("rewritten=%d unresolved=%d, want 0 and 0", rep.Rewritten, rep.Unresolved)
	}
}

func TestBadBaseIsReported(t *testing.T) {
	for _, b := range []string{"", "/relative/only", "::"} {
		if _, _, err := rewriteString(`<a href="/x">t</a>`, b, true, false); err == nil {
			t.Errorf("base %q was accepted", b)
		}
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestAStartTagSlicesExactly is the claim the library's own sourceloc_test.go stops short of.
// It checks a prefix - that the slice opens `<name` - which every one of these would pass while
// ending in the wrong place. The forms here are the ones a scanner looking for the next `>`
// gets wrong.
func TestAStartTagSlicesExactly(t *testing.T) {
	for _, tt := range []struct{ doc, want string }{
		{`<p>x</p>`, `<p>`},
		{`<p >x`, `<p >`},
		{`<img src=a/>`, `<img src=a/>`},
		{`<p/>x`, `<p/>`},
		{`<p/ >x`, `<p/ >`},
		{`<a title="a<b">x</a>`, `<a title="a<b">`},
		{"<p\nclass=y>x", "<p\nclass=y>"},
		{`<p class = "y" >x`, `<p class = "y" >`},
		{`<p attr>x`, `<p attr>`},
		{`<p attr=>x`, `<p attr=>`},
		{`<p a="1"b="2">x`, `<p a="1"b="2">`},
	} {
		spans, err := Locate([]byte(tt.doc), []string{"*"})
		if err != nil {
			t.Fatalf("%q: %v", tt.doc, err)
		}
		var starts []Span
		for _, s := range spans {
			if s.Kind == "start" {
				starts = append(starts, s)
			}
		}
		if len(starts) != 1 {
			t.Fatalf("%q: %d start tags, want 1: %v", tt.doc, len(starts), starts)
		}
		if starts[0].Raw != tt.want {
			t.Errorf("%q: start tag slices to %q, want %q", tt.doc, starts[0].Raw, tt.want)
		}
	}
}

// TestTheSliceKeepsTheSpellingTagNameLoses. TagName is normalised, so a program that wants to
// report what the author wrote - a linter, a codemod producing a diff - has to go back to the
// input. The offsets are how.
func TestTheSliceKeepsTheSpellingTagNameLoses(t *testing.T) {
	const doc = `<P CLASS=x>hi</P>`
	spans, err := Locate([]byte(doc), []string{"p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) == 0 {
		t.Fatal("no spans: the selector is lowercase and the tag is not, so this needs the " +
			"selector match to be case-insensitive")
	}
	if got := spans[0].Raw; got != `<P CLASS=x>` {
		t.Errorf("slice is %q, want the author's own bytes %q", got, `<P CLASS=x>`)
	}
}

// TestOffsetsDoNotMoveWithTheChunking. B56 for this program's own reporting: the same spans
// have to come back however the caller fed the input, or an offset is only good for the write
// pattern that produced it.
func TestOffsetsDoNotMoveWithTheChunking(t *testing.T) {
	const doc = `<!doctype html><ul><li>a<li>b</ul><!--c--><p>tail</p>`
	// The baseline is the same handler set fed in one piece, not Locate - comparing against a
	// different set of handlers would have compared span counts that were never meant to
	// match, which is how the first draft of this test failed.
	whole, err := locateChunked([]byte(doc), len(doc)+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) == 0 {
		t.Fatal("no spans")
	}
	for _, size := range []int{1, 3, 7} {
		got, err := locateChunked([]byte(doc), size)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(whole) {
			t.Fatalf("chunk %d: %d spans, want %d", size, len(got), len(whole))
		}
		for i := range got {
			if got[i] != whole[i] {
				t.Errorf("chunk %d: span %d is %v, want %v", size, i, got[i], whole[i])
			}
		}
	}
}

// TestOnlyAStrayEndTagIsUnnamed is the finding. Every byte of an ordinary document is named by
// some handler; a stray end tag is named by none, in every shape it takes.
func TestOnlyAStrayEndTagIsUnnamed(t *testing.T) {
	for _, tt := range []struct {
		doc     string
		unnamed []string
	}{
		{`<!doctype html><p>hi</p><!--c--><ul><li>a<li>b</ul>`, nil},
		{"<p>a</p> \n <p>b</p>", nil},
		{`<script>var x = 1</script><style>a{}</style>`, nil},
		{`<textarea><b>x</b></textarea>`, nil},
		{`<p>unclosed`, nil},
		{`text before <p>x`, nil},
		{`<p></p >`, nil},

		{`</p>stray`, []string{`</p>`}},
		{`<div></span></div>`, []string{`</span>`}},
		{`</br>x`, []string{`</br>`}},
		{`</img>x`, []string{`</img>`}},
		{`</p class=x>y`, []string{`</p class=x>`}},
		{`</>x`, []string{`</>`}},
		{`<svg></circle></svg>`, []string{`</circle>`}},
		{`<ul><li>a</li></ul></li>`, []string{`</li>`}},
		{`<p>a</p></p><b>c</b>`, []string{`</p>`}},
	} {
		cov, err := Cover([]byte(tt.doc))
		if err != nil {
			t.Fatalf("%q: %v", tt.doc, err)
		}
		var got []string
		for _, u := range cov.Unnamed {
			got = append(got, u.Raw)
		}
		if strings.Join(got, "|") != strings.Join(tt.unnamed, "|") {
			t.Errorf("%q: unnamed %q, want %q", tt.doc, got, tt.unnamed)
			continue
		}
		for _, u := range got {
			if !strings.HasPrefix(u, "</") {
				t.Errorf("%q: unnamed %q is not an end tag, so the rule this documents "+
					"is wrong", tt.doc, u)
			}
		}
	}
}

// TestOneSpaceDecidesWhetherAStrayTagIsVisible. `</x>` is an end tag and unobservable; `</ x>`
// is not a tag but a bogus comment, so a comment handler sees it. A tool reporting what it
// could not account for has to survive both.
func TestOneSpaceDecidesWhetherAStrayTagIsVisible(t *testing.T) {
	invisible, err := Cover([]byte(`</x>y`))
	if err != nil {
		t.Fatal(err)
	}
	if len(invisible.Unnamed) != 1 || invisible.Unnamed[0].Raw != `</x>` {
		t.Errorf("`</x>y`: unnamed %v, want the end tag", invisible.Unnamed)
	}

	visible, err := Cover([]byte(`</ x>y`))
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Unnamed) != 0 {
		t.Errorf("`</ x>y`: unnamed %v, want none - the space makes it a comment",
			visible.Unnamed)
	}
	var kinds []string
	for _, s := range visible.Spans {
		kinds = append(kinds, s.Kind)
	}
	if len(visible.Spans) == 0 || visible.Spans[0].Kind != "comment" {
		t.Errorf("`</ x>y`: spans are %v, want a comment first", kinds)
	}
}

// TestReconstructingFromSpansAloneDropsTheStrayTag. This is why Unnamed exists rather than
// being left to the caller to notice. The spans look complete - they are contiguous, in order,
// and each slices to itself - and the document they rebuild is missing four bytes that the
// rewriter itself passed through to the output.
func TestReconstructingFromSpansAloneDropsTheStrayTag(t *testing.T) {
	const doc = `<p>a</p></p><b>c</b>`

	if out, err := rewriteUntouched(doc); err != nil {
		t.Fatal(err)
	} else if out != doc {
		t.Fatalf("a no-handler pass changed the document: %q, want %q", out, doc)
	}

	cov, err := Cover([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := Reconstruct(cov.Spans); got == doc {
		t.Fatalf("the spans alone rebuilt the whole document, so there is nothing "+
			"unnamed here and this test proves nothing: %q", got)
	} else if got != `<p>a</p><b>c</b>` {
		t.Errorf("spans alone rebuilt %q, want the document minus the stray end tag", got)
	}

	if got := Reconstruct(append(cov.Spans, cov.Unnamed...)); got != doc {
		t.Errorf("spans plus unnamed rebuilt %q, want %q", got, doc)
	}
}

// TestSpansPlusUnnamedIsAlwaysTheDocument over a wider set, including the documents with no
// unnamed bytes at all, so the recipe is one recipe rather than a special case per document.
func TestSpansPlusUnnamedIsAlwaysTheDocument(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t</title></head><body><p>hi</p></body></html>`,
		`<ul><li>a<li>b</ul>`,
		`<!--c--><?php x ?><!weird>`,
		`</p>stray</div>more`,
		`<script>if (a<b) {}</script>`,
		`<p>&amp;&#65;</p>`,
		"\xef\xbb\xbf<p>bom</p>",
		`<p title="&quot;">x</p>`,
		``,
		`plain text only`,
	} {
		cov, err := Cover([]byte(doc))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if got := Reconstruct(append(cov.Spans, cov.Unnamed...)); got != doc {
			t.Errorf("%q: spans plus unnamed rebuilt %q", doc, got)
		}
		for _, s := range append(cov.Spans, cov.Unnamed...) {
			if s.Raw != doc[s.Start:s.End] {
				t.Errorf("%q: span %v does not slice to itself", doc, s)
			}
		}
	}
}

// locateChunked is Locate with the input fed in fixed-size pieces, which is what makes the
// chunk-invariance test a test of the library rather than of one Write call.
func locateChunked(doc []byte, size int) ([]Span, error) {
	var spans []Span
	add := func(kind string, l lolhtml.SourceLocation) {
		spans = append(spans, Span{kind, l.Start, l.End, string(doc[l.Start:l.End])})
	}
	w, err := lolhtml.NewWriter(&bytes.Buffer{},
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { add("doctype", d.SourceLocation()); return nil }),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { add("comment", c.SourceLocation()); return nil }),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			add("start", e.SourceLocation())
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(et *lolhtml.EndTag) error { add("end", et.SourceLocation()); return nil })
		}),
	)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write(doc[i:end]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return dedupe(spans), nil
}

// rewriteUntouched is the passthrough baseline: whatever a no-handler rewrite emits is what the
// stray end tag's bytes are being compared against, so the test is not resting on my reading of
// the input.
func rewriteUntouched(doc string) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out)
	if err != nil {
		return "", err
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

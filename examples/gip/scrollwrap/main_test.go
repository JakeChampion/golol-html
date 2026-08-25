package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var std = Options{Selector: Wide, Class: "scroll"}

func wrap(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Wrap(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Wrap(%q): %v", doc, err)
	}
	return out.String(), res
}

const open = `<div class="scroll" tabindex="0">`
const openSpan = `<span class="scroll" tabindex="0">`

// TestAWideElementGetsAContainer.
func TestAWideElementGetsAContainer(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<table><tr><td>x</table>`, open + `<table><tr><td>x</table></div>`},
		{`<pre>code</pre>`, open + `<pre>code</pre></div>`},
		{`<iframe src="a"></iframe>`, open + `<iframe src="a"></iframe></div>`},
		{`<p>before</p><table></table>`, `<p>before</p>` + open + `<table></table></div>`},
		// Nothing else is touched.
		{`<div>x</div>`, `<div>x</div>`},
		{`<img src="a">`, `<img src="a">`},
	} {
		got, res := wrap(t, tc.doc, std)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if !res.OK() {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestInsideAParagraphTheWrapperIsASpan, because a div would take the element out of
// the paragraph, orphan the text after it and leave an empty paragraph behind.
func TestInsideAParagraphTheWrapperIsASpan(t *testing.T) {
	for _, tc := range []struct {
		doc, want   string
		spans, divs int
	}{
		{`<p>a <iframe src="x"></iframe> b</p>`,
			`<p>a ` + openSpan + `<iframe src="x"></iframe></span> b</p>`, 1, 0},
		{`<p>a <video src="x"></video> b</p>`,
			`<p>a ` + openSpan + `<video src="x"></video></span> b</p>`, 1, 0},
		// A pre closes the paragraph by starting, so the span could not hold it and
		// the div is right.
		{`<p>a<pre>code</pre></p>`, `<p>a` + open + `<pre>code</pre></div></p>`, 0, 1},
		// The paragraph is over here, so the container is a div again.
		{`<p>a</p><iframe src="x"></iframe>`,
			`<p>a</p>` + open + `<iframe src="x"></iframe></div>`, 0, 1},
		// So is it here: the div start tag ended the paragraph before the iframe.
		{`<p>a<div>b<iframe src="x"></iframe></div>`,
			`<p>a<div>b` + open + `<iframe src="x"></iframe></div></div>`, 0, 1},
		// And here, where an ancestor's end tag ended it.
		{`<section><p>a</section><iframe src="x"></iframe>`,
			`<section><p>a</section>` + open + `<iframe src="x"></iframe></div>`, 0, 1},
	} {
		got, res := wrap(t, tc.doc, std)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if res.Spans != tc.spans || res.Divs != tc.divs {
			t.Errorf("%q: Spans=%d Divs=%d, want %d and %d", tc.doc, res.Spans, res.Divs, tc.spans, tc.divs)
		}
	}
}

// TestTheDoctypeDecidesForATable, which is the one decision that cannot be made from
// the element alone: without a doctype a table does not close a paragraph.
func TestTheDoctypeDecidesForATable(t *testing.T) {
	const body = `<p>a<table><tr><td>x</table></p>`
	got, res := wrap(t, body, std)
	if want := `<p>a` + openSpan + `<table><tr><td>x</table></span></p>`; got != want {
		t.Errorf("quirks mode\n got %q\nwant %q", got, want)
	}
	if res.Spans != 1 {
		t.Errorf("quirks mode: %v", res)
	}
	got, res = wrap(t, `<!DOCTYPE html>`+body, std)
	if want := `<!DOCTYPE html><p>a` + open + `<table><tr><td>x</table></div></p>`; got != want {
		t.Errorf("standards mode\n got %q\nwant %q", got, want)
	}
	if res.Divs != 1 {
		t.Errorf("standards mode: %v", res)
	}
	// A doctype with a public identifier is quirks mode too, so the old pages that
	// have one get the span.
	got, _ = wrap(t, `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`+body, std)
	if !strings.Contains(got, openSpan) {
		t.Errorf("a transitional doctype got %q", got)
	}
	// Case does not matter in the name.
	got, _ = wrap(t, `<!doctype HTML>`+body, std)
	if !strings.Contains(got, open) {
		t.Errorf("a lower-case doctype got %q", got)
	}
}

// TestAnElementWhoseEndTagIsNotItsOwnIsReported, and the container is still closed:
// an unclosed div would swallow the rest of the document.
func TestAnElementWhoseEndTagIsNotItsOwnIsReported(t *testing.T) {
	const doc = `<ul><li><pre>code<li>next</ul>`
	got, res := wrap(t, doc, std)
	if res.Displaced != 1 || res.Divs != 0 {
		t.Errorf("%v", res)
	}
	if !strings.Contains(got, "</div>") || strings.Count(got, "</div>") != 1 {
		t.Errorf("the container was not closed exactly once: %q", got)
	}
	if res.OK() {
		t.Error("OK() is true with a displaced container")
	}
}

// TestAnElementNothingClosesIsCounted, since its container was opened and there is
// no position left to close it at.
func TestAnElementNothingClosesIsCounted(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		unclosed int
	}{
		{`<pre>code`, 1},
		{`<table><tr><td>x`, 1},
		{`<pre>a</pre><pre>b`, 1},
		{`<pre>a</pre>`, 0},
	} {
		_, res := wrap(t, tc.doc, std)
		if res.Unclosed != tc.unclosed {
			t.Errorf("%q: Unclosed = %d, want %d", tc.doc, res.Unclosed, tc.unclosed)
		}
	}
}

// TestWrappingTwiceChangesNothing, which is what the class is for.
func TestWrappingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<table><tr><td>x</table>`,
		`<p>a <iframe src="x"></iframe> b</p>`,
		`<!DOCTYPE html><p>a<table><tr><td>x</table></p>`,
		`<blockquote><pre>code</pre></blockquote>`,
		`<div class="scroll" tabindex="0"><table></table></div>`,
	} {
		once, _ := wrap(t, doc, std)
		twice, res := wrap(t, once, std)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Divs+res.Spans != 0 {
			t.Errorf("%q: the second pass wrapped %d", doc, res.Divs+res.Spans)
		}
		if res.Wrapped == 0 {
			t.Errorf("%q: the second pass did not recognise the container", doc)
		}
	}
	// A nested wide element inside an existing container is left alone too.
	got, res := wrap(t, `<div class="scroll"><table><tr><td><pre>x</pre></table></div>`, std)
	if strings.Contains(got, "tabindex") {
		t.Errorf("got %q", got)
	}
	if res.Wrapped != 2 {
		t.Errorf("%v", res)
	}
}

// TestTheLabelIsOptionalAndEscaped: a role="region" with no accessible name is worse
// than no role, so the role only appears with a label.
func TestTheLabelIsOptionalAndEscaped(t *testing.T) {
	got, _ := wrap(t, `<table></table>`, std)
	if strings.Contains(got, "role=") {
		t.Errorf("a container with no label got a role: %q", got)
	}
	opts := std
	opts.Label = `Sales "2024" & more`
	got, _ = wrap(t, `<table></table>`, opts)
	if !strings.Contains(got, `role="region"`) {
		t.Errorf("got %q", got)
	}
	var label string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		label, _ = e.Attribute("aria-label")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	// The value reads back as the source spells it, references and all.
	if label != `Sales &quot;2024&quot; &amp; more` {
		t.Errorf("aria-label reads back as %q", label)
	}
}

// TestTheSelectorAndClassAreTheCallers, because what is wide is a fact about a
// page's CSS rather than about HTML.
func TestTheSelectorAndClassAreTheCallers(t *testing.T) {
	got, res := wrap(t, `<table></table><figure></figure>`, Options{Selector: "figure", Class: "pan"})
	if want := `<table></table><div class="pan" tabindex="0"><figure></figure></div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Divs != 1 {
		t.Errorf("%v", res)
	}
	// An empty selector or class falls back to the defaults.
	got, _ = wrap(t, `<pre>x</pre>`, Options{})
	if want := open + `<pre>x</pre></div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<!DOCTYPE html><p>a<table><tr><td>x</table></p><p>b <iframe src="i"></iframe></p><pre>c</pre>`
	want, wantRes := wrap(t, doc, std)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		w := &wrapper{opts: std, quirks: true}
		var out strings.Builder
		rw, err := lolhtml.NewWriter(&out, w.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := rw.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := rw.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if w.res != wantRes {
			t.Errorf("chunks of %d: %v, want %v", size, w.res, wantRes)
		}
	}
}

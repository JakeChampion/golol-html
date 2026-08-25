package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func mark(t *testing.T, doc, elements string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Mark(&out, strings.NewReader(doc), elements)
	if err != nil {
		t.Fatalf("Mark(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheNamedElementsAreMarked, and nothing else is.
func TestTheNamedElementsAreMarked(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>a <code>b</code></p>", `<p>a <code translate="no">b</code></p>`},
		{"<kbd>a</kbd>", `<kbd translate="no">a</kbd>`},
		{"<samp>a</samp>", `<samp translate="no">a</samp>`},
		{"<var>a</var>", `<var translate="no">a</var>`},
		{"<p>a</p>", "<p>a</p>"},
		{"<pre>a</pre>", "<pre>a</pre>"},
		// The attribute goes on the element, and its content is untouched.
		{"<code>a &amp; <b>b</b></code>", `<code translate="no">a &amp; <b>b</b></code>`},
	} {
		if got, _ := mark(t, tc.in, ""); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestTheElementListIsTheCallers, since which elements are prose is a decision
// about a document rather than about HTML.
func TestTheElementListIsTheCallers(t *testing.T) {
	got, res := mark(t, "<pre>a</pre><code>b</code>", "pre")
	if want := `<pre translate="no">a</pre><code>b</code>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Marked["pre"] != 1 || res.Marked["code"] != 0 {
		t.Errorf("%v", res)
	}
}

// TestWhatTheDocumentAlreadySaidIsLeftAlone, in both directions: a page that says
// translate="yes" on a code sample means it.
func TestWhatTheDocumentAlreadySaidIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		in      string
		already int
	}{
		{`<code translate="no">a</code>`, 1},
		{`<code translate="yes">a</code>`, 1},
		{`<code TRANSLATE="YES">a</code>`, 1},
		{`<code translate="">a</code>`, 1},
	} {
		got, res := mark(t, tc.in, "")
		if got != tc.in {
			t.Errorf("%q was rewritten to %q", tc.in, got)
		}
		if res.Already != tc.already || len(res.Marked) != 0 {
			t.Errorf("%q: %v", tc.in, res)
		}
	}
}

// TestAnAttributeInsideOneSaysNothing, because translate is inherited - and a diff
// that adds an attribute which changes nothing is still a diff somebody has to
// read.
func TestAnAttributeInsideOneSaysNothing(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		nested   int
	}{
		{`<pre translate="no"><code>a</code></pre>`, `<pre translate="no"><code>a</code></pre>`, 1},
		{`<div translate="no"><code>a</code><kbd>b</kbd></div>`,
			`<div translate="no"><code>a</code><kbd>b</kbd></div>`, 2},
		// The program's own attribute counts as one: a code inside a code needs
		// nothing.
		{`<code>a<kbd>b</kbd></code>`, `<code translate="no">a<kbd>b</kbd></code>`, 1},
		// And an explicit yes turns inheritance back on, so what is inside it is
		// worth marking again.
		{`<div translate="no"><span translate="yes"><code>a</code></span></div>`,
			`<div translate="no"><span translate="yes"><code translate="no">a</code></span></div>`, 0},
	} {
		got, res := mark(t, tc.in, "")
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Nested != tc.nested {
			t.Errorf("%q: Nested = %d, want %d", tc.in, res.Nested, tc.nested)
		}
	}
}

// TestTheRegionEndsWithItsElement, so a code after a marked block is marked.
func TestTheRegionEndsWithItsElement(t *testing.T) {
	got, res := mark(t, `<pre translate="no"><code>a</code></pre><code>b</code>`, "")
	want := `<pre translate="no"><code>a</code></pre><code translate="no">b</code>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Nested != 1 || res.Marked["code"] != 1 {
		t.Errorf("%v", res)
	}
	// An element the document left unclosed keeps its region to the end of
	// whatever closed it, which is the end-tag rule rather than this program's.
	got, _ = mark(t, `<div translate="no"><code>a</code></div><code>b</code>`, "")
	if !strings.Contains(got, `<code translate="no">b`) {
		t.Errorf("got %q", got)
	}
}

// TestTheClassConventionIsHonoured, matched as a word and case-insensitively -
// neither of which a class selector does on its own.
func TestTheClassConventionIsHonoured(t *testing.T) {
	for _, tc := range []struct {
		in      string
		marked  bool
		byClass int
	}{
		{`<p class="notranslate">a</p>`, true, 1},
		{`<p class="a notranslate b">a</p>`, true, 1},
		{`<p class="noTranslate">a</p>`, true, 1},
		{`<p class="NOTRANSLATE">a</p>`, true, 1},
		{`<p class="notranslated">a</p>`, false, 0},
		{`<p class="a">a</p>`, false, 0},
	} {
		got, res := mark(t, tc.in, "")
		if marked := strings.Contains(got, `translate="no"`); marked != tc.marked {
			t.Errorf("%q -> %q, want marked = %v", tc.in, got, tc.marked)
		}
		if res.ByClass != tc.byClass {
			t.Errorf("%q: ByClass = %d, want %d", tc.in, res.ByClass, tc.byClass)
		}
	}
	// A plain class selector would miss two of those, which is why the selector is
	// what it is.
	missed := 0
	for _, doc := range []string{`<p class="noTranslate">a</p>`, `<p class="NOTRANSLATE">a</p>`} {
		if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(".notranslate", func(*lolhtml.Element) error {
			missed--
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		missed++
	}
	if missed != 2 {
		t.Errorf(".notranslate matched a spelling it should not have; the selector " +
			"in this program could be simpler")
	}
}

// TestMarkingTwiceChangesNothing, which falls out of leaving alone what already
// says something.
func TestMarkingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		"<p>a <code>b</code> c <kbd>d</kbd></p>",
		`<pre translate="no"><code>a</code></pre>`,
		`<p class="notranslate">a</p>`,
		`<code translate="yes">a</code>`,
	} {
		once, _ := mark(t, doc, "")
		twice, res := mark(t, once, "")
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if len(res.Marked) != 0 {
			t.Errorf("%q: the second pass marked %v", doc, res.Marked)
		}
	}
}

// TestChunkInvariance.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><p>Run <code>git push</code> then <kbd>Ctrl</kbd>.</p>` +
		`<pre translate="no"><code>inner</code></pre><p class="noTranslate">brand</p>` +
		`<code translate="yes">t</code><samp>out</samp></body></html>`
	want, _ := mark(t, doc, "")
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		m := &marker{res: Result{Marked: map[string]int{}}}
		w, err := lolhtml.NewWriter(&out, m.options(Elements)...)
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
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// TestASelectorListCostsLessThanABroadHandler, which is why this program is
// written the way it is. The library's alloc_test.go holds the general comparison;
// this is the same measurement on this program's own shape, so the choice cannot
// quietly stop being the cheap one.
func TestASelectorListCostsLessThanABroadHandler(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := range 500 {
		switch i % 20 {
		case 0:
			b.WriteString("<code>x</code>")
		case 1:
			b.WriteString("<kbd>y</kbd>")
		default:
			b.WriteString(`<p class="a b">some text here</p>`)
		}
	}
	b.WriteString("</body></html>")
	doc := b.String()

	measure := func(opts func() []lolhtml.Option) float64 {
		return testing.AllocsPerRun(20, func() {
			w, err := lolhtml.NewWriter(io.Discard, opts()...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, doc); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	narrow := measure(func() []lolhtml.Option {
		return (&marker{res: Result{Marked: map[string]int{}}}).options(Elements)
	})
	broad := measure(func() []lolhtml.Option {
		m := &marker{res: Result{Marked: map[string]int{}}}
		return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			switch e.TagName() {
			case "code", "kbd", "samp", "var":
				return m.byTag(e)
			}
			return nil
		})}
	})
	if broad < 2*narrow {
		t.Errorf("this program cost %.0f allocations and the broad version %.0f; "+
			"the reason for the selector list was that it wins by a lot", narrow, broad)
	}
}

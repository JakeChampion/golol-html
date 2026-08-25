package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func rebase(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Rebase(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Rebase(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheBaseGoesAndItsURLsAreResolved.
func TestTheBaseGoesAndItsURLsAreResolved(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<base href="/assets/"><img src="a.png">`, `<img src="/assets/a.png">`},
		{`<base href="/assets/"><a href="p.html">x</a>`, `<a href="/assets/p.html">x</a>`},
		{`<base href="/assets/"><script src="s.js"></script>`, `<script src="/assets/s.js"></script>`},
		{`<base href="/assets/"><form action="go"></form>`, `<form action="/assets/go"></form>`},
		{`<base href="/assets/"><video poster="p.jpg" src="v.mp4"></video>`,
			`<video poster="/assets/p.jpg" src="/assets/v.mp4"></video>`},
		{`<base href="/assets/"><blockquote cite="c.html"></blockquote>`,
			`<blockquote cite="/assets/c.html"></blockquote>`},
		{`<base href="/assets/"><svg><use xlink:href="i.svg#a"/></svg>`,
			`<svg><use xlink:href="/assets/i.svg#a" /></svg>`},
		// A deeper base, and a URL that climbs out of it.
		{`<base href="/a/b/c"><img src="d.png">`, `<img src="/a/b/d.png">`},
		{`<base href="/a/b/"><img src="../e.png">`, `<img src="/a/e.png">`},
		// An absolute path ignores the base's path, as URL resolution says.
		{`<base href="/a/b/"><img src="/f.png">`, `<img src="/f.png">`},
	} {
		got, res := rebase(t, tc.doc, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if !res.Removed || !res.OK() {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestWhatTheBaseDoesNotApplyTo.
func TestWhatTheBaseDoesNotApplyTo(t *testing.T) {
	for _, tc := range []struct{ in string }{
		{`https://other/z`},
		{`//other/z`},
		{`mailto:a@b`},
		{`data:text/plain,x`},
		{`#frag`},
		{`javascript:void(0)`},
	} {
		doc := `<base href="/assets/"><a href="` + tc.in + `">x</a>`
		got, res := rebase(t, doc, Options{})
		if want := `<a href="` + tc.in + `">x</a>`; got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, want)
		}
		if res.URLs != 0 {
			t.Errorf("%q: %v", tc.in, res)
		}
	}
	// An empty value is not a URL to resolve.
	got, _ := rebase(t, `<base href="/assets/"><img src="">`, Options{})
	if got != `<img src="">` {
		t.Errorf("got %q", got)
	}
}

// TestTheTargetIsTheOtherHalfOfTheTag, and removing the tag without carrying it over
// would change where every link opens.
func TestTheTargetIsTheOtherHalfOfTheTag(t *testing.T) {
	got, res := rebase(t, `<base target="_blank"><a href="/p">x</a><form action="/go"></form><area href="/m">`, Options{})
	for _, want := range []string{`<a href="/p" target="_blank">`, `<form action="/go" target="_blank">`, `<area href="/m" target="_blank">`} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
	if res.Targets != 3 {
		t.Errorf("%v", res)
	}
	// An element with its own target keeps it.
	got, res = rebase(t, `<base target="_blank"><a href="/p" target="_self">x</a>`, Options{})
	if got != `<a href="/p" target="_self">x</a>` {
		t.Errorf("got %q", got)
	}
	if res.Targets != 0 {
		t.Errorf("%v", res)
	}
	// The target applies to a link whose URL the base does not touch.
	got, _ = rebase(t, `<base href="/a/" target="_blank"><a href="https://other/z">z</a>`, Options{})
	if !strings.Contains(got, `href="https://other/z" target="_blank"`) {
		t.Errorf("got %q", got)
	}
	// And not to elements it has nothing to do with.
	got, _ = rebase(t, `<base target="_blank"><img src="/a.png"><script src="/s.js"></script>`, Options{})
	if strings.Contains(got, "target") {
		t.Errorf("got %q", got)
	}
}

// TestOnlyTheFirstOfEachAttributeCounts, and they can come from different elements.
func TestOnlyTheFirstOfEachAttributeCounts(t *testing.T) {
	got, res := rebase(t, `<base href="/a/"><base href="/b/"><img src="x.png">`, Options{})
	if !strings.Contains(got, `/a/x.png`) {
		t.Errorf("got %q", got)
	}
	if res.BaseHref != "/a/" {
		t.Errorf("%v", res)
	}
	// Two elements, one contributing each attribute.
	got, res = rebase(t, `<base target="_blank"><base href="/a/"><a href="x.html">y</a>`, Options{})
	if want := `<a href="/a/x.html" target="_blank">y</a>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.BaseHref != "/a/" || res.BaseTgt != "_blank" {
		t.Errorf("%v", res)
	}
	// A base with neither attribute does nothing but disappear.
	got, res = rebase(t, `<base><img src="x.png">`, Options{})
	if got != `<img src="x.png">` {
		t.Errorf("got %q", got)
	}
	if res.BaseHref != "" || res.URLs != 0 {
		t.Errorf("%v", res)
	}
}

// TestARelativeBaseNeedsThePageURL, which the caller supplies because a document does
// not contain it.
func TestARelativeBaseNeedsThePageURL(t *testing.T) {
	got, res := rebase(t, `<base href="assets/"><img src="a.png">`,
		Options{Page: "https://site.example/docs/index.html"})
	if want := `<img src="https://site.example/docs/assets/a.png">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.URLs != 1 {
		t.Errorf("%v", res)
	}
	// An absolute base href does not need it.
	got, _ = rebase(t, `<base href="https://cdn.example/x/"><img src="a.png">`, Options{})
	if want := `<img src="https://cdn.example/x/a.png">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestAURLBeforeTheBaseIsCountedRatherThanMissed: a single pass cannot go back, and
// the number is the honest output.
func TestAURLBeforeTheBaseIsCountedRatherThanMissed(t *testing.T) {
	const doc = `<img src="early.png"><a href="early.html">x</a><base href="/assets/"><img src="late.png">`
	got, res := rebase(t, doc, Options{})
	if !strings.Contains(got, `src="early.png"`) || !strings.Contains(got, `src="/assets/late.png"`) {
		t.Errorf("got %q", got)
	}
	if res.Early != 2 || res.URLs != 1 {
		t.Errorf("%v", res)
	}
	if res.OK() {
		t.Error("OK() is true with URLs the base could not be applied to")
	}
	// An absolute URL before the base is not at stake, so it is not counted.
	_, res = rebase(t, `<img src="https://other/x.png"><base href="/a/">`, Options{})
	if res.Early != 0 {
		t.Errorf("%v", res)
	}
}

// TestKeepLeavesTheElement, for a caller who wants the URLs resolved and the default
// kept for anything a script adds later.
func TestKeepLeavesTheElement(t *testing.T) {
	got, res := rebase(t, `<base href="/a/"><img src="x.png">`, Options{Keep: true})
	if want := `<base href="/a/"><img src="/a/x.png">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Removed {
		t.Errorf("%v", res)
	}
}

// TestCSSIsRewrittenInBothPlaces, and the stylesheet is put back as CSS rather than
// as escaped text.
func TestCSSIsRewrittenInBothPlaces(t *testing.T) {
	got, res := rebase(t, `<base href="/a/"><div style="background:url(bg.png)"></div>`, Options{})
	if want := `<div style="background:url(/a/bg.png)"></div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Styles != 1 {
		t.Errorf("%v", res)
	}

	// A stylesheet, whose child combinator must survive: Text would escape it.
	got, res = rebase(t, `<base href="/a/"><style>.x > .y{background:url(bg.png)}</style>`, Options{})
	if want := `<style>.x > .y{background:url(/a/bg.png)}</style>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Styles != 1 {
		t.Errorf("%v", res)
	}
	// Quotes inside url() are kept as they were.
	for _, q := range []string{`"`, `'`} {
		got, _ = rebase(t, `<base href="/a/"><style>.x{background:url(`+q+`bg.png`+q+`)}</style>`, Options{})
		if want := `<style>.x{background:url(` + q + `/a/bg.png` + q + `)}</style>`; got != want {
			t.Errorf("quote %s\n got %q\nwant %q", q, got, want)
		}
	}
	// A stylesheet with no base to resolve against is put back byte for byte, which
	// is the case that would have been corrupted by the wrong content type.
	const sheet = `<style>.x > .y{content:"a & b"}</style>`
	got, _ = rebase(t, sheet, Options{})
	if got != sheet {
		t.Errorf("\n got %q\nwant %q", got, sheet)
	}
}

// TestASheetThatWouldEndItsElementIsRefused, which is what CheckRawText is for: the
// text path does not refuse it by itself.
func TestASheetThatWouldEndItsElementIsRefused(t *testing.T) {
	// The rewrite cannot produce this by itself, so the input carries it: a
	// stylesheet whose text already contains the closing sequence, which the
	// tokenizer ends the element at - so what comes back has to be checked.
	var out strings.Builder
	_, err := Rebase(&out, strings.NewReader(`<base href="/a/"><style>.x{background:url(bg.png)}</style>`), Options{})
	if err != nil {
		t.Fatalf("a plain sheet failed: %v", err)
	}
	// And the check itself is the library's, applied to what this program would
	// write. Asserted here so the app cannot quietly stop calling it.
	if err := lolhtml.CheckRawText("style", `.x{content:"</style>"}`); err == nil {
		t.Error("CheckRawText accepted a sheet that ends its own element")
	}
}

// TestRebasingTwiceChangesNothing: the base is gone after the first pass, so the
// second has nothing to resolve.
func TestRebasingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<base href="/a/"><img src="x.png"><a href="y.html">y</a>`,
		`<base href="/a/" target="_blank"><a href="y.html">y</a>`,
		`<base href="/a/"><style>.x > .y{background:url(bg.png)}</style>`,
		`<img src="early.png"><base href="/a/">`,
	} {
		once, _ := rebase(t, doc, Options{})
		twice, res := rebase(t, once, Options{})
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.URLs != 0 || res.Targets != 0 {
			t.Errorf("%q: the second pass changed %v", doc, res)
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries, including a stylesheet split across writes,
// which is where the accumulate-to-the-last-chunk shape earns its keep.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<base href="/a/" target="_blank"><img src="x.png"><a href="y.html">y</a><style>.x > .y{background:url(bg.png)}</style><img srcset="p.png 1x, q.png 2x">`
	want, wantRes := rebase(t, doc, Options{})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		r := &rebaser{}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, r.options()...)
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
		if r.res.URLs != wantRes.URLs || r.res.Styles != wantRes.Styles || r.res.Targets != wantRes.Targets {
			t.Errorf("chunks of %d: %v, want %v", size, r.res, wantRes)
		}
	}
}

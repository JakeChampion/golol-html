package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var manifest = map[string]string{
	"/js/app.js":     "9f2a1c",
	"/css/site.css":  "41b0e7",
	"/i/a.png":       "aaa1",
	"/i/b.png":       "bbb2",
	"/i/c,d.png":     "ccc3",
	"/f/font.woff2":  "fff4",
	"/js/nested.mjs": "nnn5",
}

func std() Options { return Options{Manifest: manifest, Param: "v"} }

func bust(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Bust(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Bust(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestEveryPlaceAnAssetIsNamed.
func TestEveryPlaceAnAssetIsNamed(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<script src="/js/app.js"></script>`, `<script src="/js/app.js?v=9f2a1c"></script>`},
		{`<link href="/css/site.css">`, `<link href="/css/site.css?v=41b0e7">`},
		{`<img src="/i/a.png">`, `<img src="/i/a.png?v=aaa1">`},
		{`<source src="/i/a.png">`, `<source src="/i/a.png?v=aaa1">`},
		{`<video poster="/i/a.png"></video>`, `<video poster="/i/a.png?v=aaa1"></video>`},
		{`<object data="/i/a.png"></object>`, `<object data="/i/a.png?v=aaa1"></object>`},
		{`<embed src="/i/a.png">`, `<embed src="/i/a.png?v=aaa1">`},
		{`<track src="/i/a.png">`, `<track src="/i/a.png?v=aaa1">`},
		{`<iframe src="/i/a.png"></iframe>`, `<iframe src="/i/a.png?v=aaa1"></iframe>`},
		{`<svg><use xlink:href="/i/a.png#s"/></svg>`, `<svg><use xlink:href="/i/a.png?v=aaa1#s" /></svg>`},
		// An element that names no asset is left alone.
		{`<a href="/css/site.css">x</a>`, `<a href="/css/site.css">x</a>`},
		{`<div data-src="/i/a.png"></div>`, `<div data-src="/i/a.png"></div>`},
	} {
		got, _ := bust(t, tc.doc, std())
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
}

// TestAVideoWithBothAttributesGetsBothHashedOnce, which is the reason for one
// handler: two selectors that both matched would hash the first attribute twice.
func TestAVideoWithBothAttributesGetsBothHashedOnce(t *testing.T) {
	got, res := bust(t, `<video src="/i/a.png" poster="/i/b.png"></video>`, std())
	if want := `<video src="/i/a.png?v=aaa1" poster="/i/b.png?v=bbb2"></video>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Busted != 2 || res.Attrs != 2 {
		t.Errorf("%v", res)
	}
	if strings.Count(got, "v=aaa1") != 1 {
		t.Errorf("the hash went in more than once: %q", got)
	}
}

// TestTheRestOfTheURLIsKept: a query, a fragment, and both.
func TestTheRestOfTheURLIsKept(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`/i/a.png`, `/i/a.png?v=aaa1`},
		{`/i/a.png?x=1`, `/i/a.png?x=1&amp;v=aaa1`},
		{`/i/a.png#frag`, `/i/a.png?v=aaa1#frag`},
		{`/i/a.png?x=1#frag`, `/i/a.png?x=1&amp;v=aaa1#frag`},
		{`/i/a.png?x=1&amp;y=2`, `/i/a.png?x=1&amp;y=2&amp;v=aaa1`},
		// A relative path resolves to the same manifest key.
		{`./i/a.png`, `./i/a.png?v=aaa1`},
		{`i/a.png`, `i/a.png?v=aaa1`},
	} {
		got, _ := bust(t, `<img src="`+tc.in+`">`, std())
		if want := `<img src="` + tc.want + `">`; got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, want)
		}
	}
	// The value reads back as one attribute with the separators spelled as
	// references, which is what the document should say.
	got, _ := bust(t, `<img src="/i/a.png?x=1">`, std())
	var value string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("img", func(e *lolhtml.Element) error {
		value, _ = e.Attribute("src")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if value != `/i/a.png?x=1&amp;v=aaa1` {
		t.Errorf("the value reads back as %q", value)
	}
}

// TestTheNameStyleputsTheHashInTheFileName.
func TestTheNameStyleputsTheHashInTheFileName(t *testing.T) {
	opts := std()
	opts.Style = Name
	for _, tc := range []struct{ in, want string }{
		{`/js/app.js`, `/js/app.9f2a1c.js`},
		{`/i/a.png?x=1`, `/i/a.aaa1.png?x=1`},
		{`/i/a.png#f`, `/i/a.aaa1.png#f`},
		{`/f/font.woff2`, `/f/font.fff4.woff2`},
	} {
		got, _ := bust(t, `<img src="`+tc.in+`">`, opts)
		if want := `<img src="` + tc.want + `">`; got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, want)
		}
	}
}

// TestASrcsetIsHashedMemberByMember, with the specification's parse so a comma inside
// a URL stays in it.
func TestASrcsetIsHashedMemberByMember(t *testing.T) {
	got, res := bust(t, `<img srcset="/i/a.png 1x, /i/b.png 2x">`, std())
	if want := `<img srcset="/i/a.png?v=aaa1 1x, /i/b.png?v=bbb2 2x">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Busted != 2 {
		t.Errorf("%v", res)
	}
	got, _ = bust(t, `<img srcset="/i/c,d.png 2x">`, std())
	if want := `<img srcset="/i/c,d.png?v=ccc3 2x">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// A member the manifest does not know keeps its own URL while the others are
	// hashed, so the list is still a list of the same images.
	got, res = bust(t, `<img srcset="/i/a.png 1x, /i/unknown.png 2x">`, std())
	if want := `<img srcset="/i/a.png?v=aaa1 1x, /i/unknown.png 2x">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Unknown != 1 || res.Busted != 1 {
		t.Errorf("%v", res)
	}
}

// TestSomeURLsAreNotThisBuildsBusiness.
func TestSomeURLsAreNotThisBuildsBusiness(t *testing.T) {
	for _, tc := range []struct {
		doc    string
		reason func(Result) int
	}{
		{`<img src="https://cdn/i/a.png">`, func(r Result) int { return r.OffSite }},
		{`<img src="//cdn/i/a.png">`, func(r Result) int { return r.OffSite }},
		{`<img src="data:image/gif;base64,R0lGOD">`, func(r Result) int { return r.OffSite }},
		{`<img src="/i/unknown.png">`, func(r Result) int { return r.Unknown }},
		{`<img src="/i/a.png?v=aaa1">`, func(r Result) int { return r.Already }},
	} {
		got, res := bust(t, tc.doc, std())
		if got != tc.doc {
			t.Errorf("%q was rewritten to %q", tc.doc, got)
		}
		if tc.reason(res) != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
	// An empty or blank value is not a URL and is not counted as one.
	for _, doc := range []string{`<img src="">`, `<img src="   ">`} {
		got, res := bust(t, doc, std())
		if got != doc || res.Attrs != 0 {
			t.Errorf("%q gave %q, %v", doc, got, res)
		}
	}
}

// TestStrictModeFailsTheRewrite, which is what a build step wants: a page naming an
// asset nobody hashed is a page that will serve a stale file.
func TestStrictModeFailsTheRewrite(t *testing.T) {
	opts := std()
	opts.Strict = true
	var out strings.Builder
	res, err := Bust(&out, strings.NewReader(`<img src="/i/a.png"><img src="/i/gone.png">`), opts)
	if err == nil {
		t.Fatal("Bust succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "/i/gone.png") {
		t.Errorf("the error is %v, want it to name the asset", err)
	}
	if res.Unknown != 1 {
		t.Errorf("%v", res)
	}
	// Without strict mode the same document completes and reports.
	opts.Strict = false
	got, res := bust(t, `<img src="/i/a.png"><img src="/i/gone.png">`, opts)
	if !strings.Contains(got, "v=aaa1") || !strings.Contains(got, `src="/i/gone.png"`) {
		t.Errorf("got %q", got)
	}
	if res.OK() {
		t.Error("OK() is true with an unknown asset")
	}
}

// TestBustingTwiceChangesNothing.
func TestBustingTwiceChangesNothing(t *testing.T) {
	for _, style := range []Style{Query, Name} {
		opts := std()
		opts.Style = style
		for _, doc := range []string{
			`<script src="/js/app.js"></script>`,
			`<img src="/i/a.png?x=1#f">`,
			`<img srcset="/i/a.png 1x, /i/b.png 2x">`,
			`<img src="/i/unknown.png">`,
			`<img src="https://cdn/i/a.png">`,
		} {
			once, _ := bust(t, doc, opts)
			twice, res := bust(t, once, opts)
			if twice != once {
				t.Errorf("%q (style %d)\n once %q\ntwice %q", doc, style, once, twice)
			}
			if res.Busted != 0 {
				t.Errorf("%q (style %d): the second pass hashed %d", doc, style, res.Busted)
			}
		}
	}
}

// TestTheManifestIsReadAsPathAndHash.
func TestTheManifestIsReadAsPathAndHash(t *testing.T) {
	m, err := ReadManifest(strings.NewReader("# a build\n/js/app.js 9f2a1c\n\ncss/site.css 41b0e7\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["/js/app.js"] != "9f2a1c" {
		t.Errorf("%v", m)
	}
	// A path without a leading slash is the same key, so a manifest written either
	// way drives the same rewrite.
	if m["/css/site.css"] != "41b0e7" {
		t.Errorf("%v", m)
	}
	if _, err := ReadManifest(strings.NewReader("/js/app.js\n")); err == nil {
		t.Error("a line with one field was accepted")
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<script src="/js/app.js"></script><img srcset="/i/a.png 1x, /i/c,d.png 2x"><img src="/i/unknown.png"><link href="/css/site.css?x=1">`
	want, wantRes := bust(t, doc, std())
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		b := &buster{opts: std()}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, b.options()...)
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
		if b.res.Busted != wantRes.Busted || b.res.Unknown != wantRes.Unknown {
			t.Errorf("chunks of %d: %v, want %v", size, b.res, wantRes)
		}
	}
}

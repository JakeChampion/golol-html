package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func std() Options {
	return Options{Base: "https://cdn.test/i", Param: "url", Width: 800, Format: "webp"}
}

func rewrite(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", doc, err)
	}
	return out.String(), res
}

func cdn(u string) string {
	return `https://cdn.test/i?url=` + u + `&amp;w=800&amp;fm=webp`
}

// TestAnImageURLGoesThroughTheCDN, in each of the places a page names one.
func TestAnImageURLGoesThroughTheCDN(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<img src="/p/hero.jpg">`, `<img src="` + cdn("%2Fp%2Fhero.jpg") + `">`},
		{`<img srcset="/a.jpg">`, `<img srcset="` + cdn("%2Fa.jpg") + `">`},
		{`<source srcset="/a.avif">`, `<source srcset="` + cdn("%2Fa.avif") + `">`},
		{`<link rel="preload" as="image" href="/p.jpg">`,
			`<link rel="preload" as="image" href="` + cdn("%2Fp.jpg") + `">`},
		// A preload of something else is not an image.
		{`<link rel="preload" as="font" href="/f.woff2">`, `<link rel="preload" as="font" href="/f.woff2">`},
		{`<link rel="stylesheet" href="/s.css">`, `<link rel="stylesheet" href="/s.css">`},
	} {
		got, _ := rewrite(t, tc.doc, std())
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
	// The parameters are the caller's, and an empty one is left out rather than
	// sent empty.
	got, _ := rewrite(t, `<img src="/a.jpg">`, Options{Base: "https://cdn.test/i"})
	if want := `<img src="https://cdn.test/i?url=%2Fa.jpg">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	got, _ = rewrite(t, `<img src="/a.jpg">`, Options{Base: "https://cdn.test/i?v=2", Width: 100})
	if want := `<img src="https://cdn.test/i?v=2&amp;url=%2Fa.jpg&amp;w=100">`; got != want {
		t.Errorf("a base that already has a query\n got %q\nwant %q", got, want)
	}
}

// TestTheSeparatorsAreWrittenAsReferences, because a bare "&" in an attribute value
// is a reference waiting to happen and SetAttribute escapes the quote alone.
func TestTheSeparatorsAreWrittenAsReferences(t *testing.T) {
	got, _ := rewrite(t, `<img src="/a.jpg">`, std())
	if strings.Contains(got, "&w=") || strings.Contains(got, "&fm=") {
		t.Errorf("a bare ampersand separator: %q", got)
	}
	// It reads back as one attribute whose value spells the references, which is
	// what the document is supposed to say.
	var value string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("img", func(e *lolhtml.Element) error {
		value, _ = e.Attribute("src")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, "&amp;w=800") {
		t.Errorf("the value reads back as %q", value)
	}
}

// TestAnAmpersandInTheOriginalIsDecodedBeforeItIsEncoded: the two spellings are the
// same URL, and percent-encoding the reference instead of the character would send
// the CDN a different one.
func TestAnAmpersandInTheOriginalIsDecodedBeforeItIsEncoded(t *testing.T) {
	for _, spelling := range []string{
		`/q.jpg?a=1&amp;b=2`,
		`/q.jpg?a=1&b=2`,
		`/q.jpg?a=1&#38;b=2`,
		`/q.jpg?a=1&#x26;b=2`,
	} {
		got, res := rewrite(t, `<img src="`+spelling+`">`, std())
		if want := `<img src="` + cdn("%2Fq.jpg%3Fa%3D1%26b%3D2") + `">`; got != want {
			t.Errorf("%q\n got %q\nwant %q", spelling, got, want)
		}
		if res.Src != 1 {
			t.Errorf("%q: %v", spelling, res)
		}
	}
	// A reference this program cannot read is refused rather than encoded
	// literally: "&nbsp;" is one character, not six.
	for _, spelling := range []string{`/n.jpg?x=&nbsp;`, `/n.jpg?x=&#8212;`, `/n.jpg?x=&lt;`} {
		doc := `<img src="` + spelling + `">`
		got, res := rewrite(t, doc, std())
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Refused != 1 || res.OK() {
			t.Errorf("%q: %v", spelling, res)
		}
	}
}

// TestSrcsetIsParsedTheWayTheSpecificationSaysAndNotBySplittingOnCommas, because a
// URL may contain a comma.
func TestSrcsetIsParsedTheWayTheSpecificationSaysAndNotBySplittingOnCommas(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []member
	}{
		{`/a.jpg`, []member{{"/a.jpg", ""}}},
		{`/a.jpg 2x`, []member{{"/a.jpg", "2x"}}},
		{`/a.jpg 100w`, []member{{"/a.jpg", "100w"}}},
		{`/a.jpg, /b.jpg 2x`, []member{{"/a.jpg", ""}, {"/b.jpg", "2x"}}},
		// A comma inside a URL, which splitting would break.
		{`/b,c.jpg 2x`, []member{{"/b,c.jpg", "2x"}}},
		{`/b,c.jpg 1x, /d,e.jpg 2x`, []member{{"/b,c.jpg", "1x"}, {"/d,e.jpg", "2x"}}},
		// A comma with no space after it is part of the URL, because the
		// specification takes characters up to whitespace first: this is one
		// candidate, not two, which is the classic srcset trap.
		{`/a.jpg,/b.jpg`, []member{{"/a.jpg,/b.jpg", ""}}},
		// A trailing comma on that run is the separator.
		{`/a.jpg, /b.jpg`, []member{{"/a.jpg", ""}, {"/b.jpg", ""}}},
		{`/a.jpg 1x,/b.jpg 2x`, []member{{"/a.jpg", "1x"}, {"/b.jpg", "2x"}}},
		// Whitespace of every kind, and repeated separators.
		{"  /a.jpg\t1x ,\n/b.jpg  2x  ", []member{{"/a.jpg", "1x"}, {"/b.jpg", "2x"}}},
		{`/a.jpg 1x,, /b.jpg`, []member{{"/a.jpg", "1x"}, {"/b.jpg", ""}}},
	} {
		got, ok := parseSrcset(tc.in)
		if !ok {
			t.Errorf("parseSrcset(%q) refused it", tc.in)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("parseSrcset(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseSrcset(%q)[%d] = %v, want %v", tc.in, i, got[i], tc.want[i])
			}
		}
	}
	// Nothing to read is refused rather than turned into an empty list.
	for _, in := range []string{``, `   `, `,`, ` , `} {
		if _, ok := parseSrcset(in); ok {
			t.Errorf("parseSrcset(%q) was accepted", in)
		}
	}
}

// TestASrcsetIsRewrittenWholeOrNotAtAll: half a list is a list of images that do not
// match each other.
func TestASrcsetIsRewrittenWholeOrNotAtAll(t *testing.T) {
	got, res := rewrite(t, `<img srcset="/a.jpg 1x, /b,c.jpg 2x">`, std())
	want := `<img srcset="` + cdn("%2Fa.jpg") + ` 1x, ` + cdn("%2Fb%2Cc.jpg") + ` 2x">`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Srcset != 1 {
		t.Errorf("%v", res)
	}
	// One member this program will not read leaves the whole attribute alone.
	const mixed = `<img srcset="/a.jpg 1x, /n.jpg?x=&nbsp; 2x">`
	got, res = rewrite(t, mixed, std())
	if got != mixed {
		t.Errorf("\n got %q\nwant it untouched", got)
	}
	if res.Srcset != 0 || res.Refused != 1 {
		t.Errorf("%v", res)
	}
	// So does an off-site member: the two would come from different places.
	const offsite = `<img srcset="/a.jpg 1x, https://other/b.jpg 2x">`
	got, res = rewrite(t, offsite, std())
	if got != offsite {
		t.Errorf("\n got %q\nwant it untouched", got)
	}
	if res.Absolute != 1 {
		t.Errorf("%v", res)
	}
}

// TestSomeURLsAreNotThisRewritesBusiness.
func TestSomeURLsAreNotThisRewritesBusiness(t *testing.T) {
	for _, tc := range []struct {
		doc    string
		reason func(Result) int
	}{
		{`<img src="https://other/x.jpg">`, func(r Result) int { return r.Absolute }},
		{`<img src="//other/x.jpg">`, func(r Result) int { return r.Absolute }},
		{`<img src="data:image/gif;base64,R0lGOD">`, func(r Result) int { return r.Absolute }},
		{`<img src="DATA:image/gif;base64,R0lGOD">`, func(r Result) int { return r.Absolute }},
		{`<img src="https://cdn.test/i?url=%2Fa.jpg">`, func(r Result) int { return r.Already }},
	} {
		got, res := rewrite(t, tc.doc, std())
		if got != tc.doc {
			t.Errorf("%q was rewritten to %q", tc.doc, got)
		}
		if tc.reason(res) != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
	// A relative URL with no leading slash is ours: it resolves against the page.
	got, res := rewrite(t, `<img src="hero.jpg">`, std())
	if !strings.Contains(got, "url=hero.jpg") || res.Src != 1 {
		t.Errorf("got %q, %v", got, res)
	}
}

// TestRewritingTwiceChangesNothing, which is what the CDN prefix check is for.
func TestRewritingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<img src="/p/hero.jpg">`,
		`<img srcset="/a.jpg 1x, /b,c.jpg 2x">`,
		`<picture><source srcset="/w.avif"><img src="/w.jpg"></picture>`,
		`<link rel="preload" as="image" href="/p.jpg">`,
		`<img src="https://other/x.jpg">`,
		`<img src="/n.jpg?x=&nbsp;">`,
	} {
		once, _ := rewrite(t, doc, std())
		twice, res := rewrite(t, once, std())
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Src+res.Srcset+res.Preload != 0 {
			t.Errorf("%q: the second pass rewrote something: %v", doc, res)
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries, on a document whose longest tag is a
// srcset - which is also the tag that sets this rewrite's memory floor.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<p>a</p><img src="/p/hero.jpg"><img srcset="/a.jpg 1x, /b,c.jpg 2x, /d.jpg 800w"><img src="https://other/x.jpg">`
	want, wantRes := rewrite(t, doc, std())
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		rw := &rewriter{opts: std()}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, rw.options()...)
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
		if rw.res != wantRes {
			t.Errorf("chunks of %d: %v, want %v", size, rw.res, wantRes)
		}
	}
}

// TestTheMemoryFloorIsTheLongestTagThisMatches, which is the cost of pointing at
// srcset attributes: the same document under the same writes needed less before.
func TestTheMemoryFloorIsTheLongestTagThisMatches(t *testing.T) {
	long := `<img srcset="` + strings.Repeat("/a-fairly-long-image-name.jpg 100w, ", 40) + `/x.jpg 1x">`
	doc := `<p>text</p>` + long + `<p>more</p>`

	floor := func(opts func() []lolhtml.Option) int {
		run := func(limit int) bool {
			all := append([]lolhtml.Option{
				lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}),
			}, opts()...)
			w, err := lolhtml.NewWriter(io.Discard, all...)
			if err != nil {
				return false
			}
			for i := 0; i < len(doc); i += 64 {
				if _, err := w.Write([]byte(doc[i:min(i+64, len(doc))])); err != nil {
					w.Close()
					return false
				}
			}
			return w.Close() == nil
		}
		hi := 8
		for hi < 1<<22 && !run(hi) {
			hi *= 2
		}
		lo := hi / 2
		for lo+1 < hi {
			mid := (lo + hi) / 2
			if run(mid) {
				hi = mid
			} else {
				lo = mid
			}
		}
		return hi
	}

	bare := floor(func() []lolhtml.Option { return nil })
	withRewrite := floor(func() []lolhtml.Option { return (&rewriter{opts: std()}).options() })
	if bare > 128 {
		t.Errorf("with no handlers the floor is %d, want under 128", bare)
	}
	if withRewrite < len(long) {
		t.Errorf("with this rewrite the floor is %d, want at least the tag's %d bytes", withRewrite, len(long))
	}
	if withRewrite <= bare*4 {
		t.Errorf("floors are %d without and %d with: the point is that it rises a lot", bare, withRewrite)
	}
}

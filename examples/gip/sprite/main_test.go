package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const spriteMarkup = `<svg xmlns="http://www.w3.org/2000/svg" hidden><symbol id="i-save"><path d="M0"/></symbol></svg>`

func std() Options {
	return Options{Sprite: spriteMarkup, Prefix: "i-", Class: "icon", Marker: "data-sprite"}
}

func inject(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Inject(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Inject(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestAnIconBecomesAUse.
func TestAnIconBecomesAUse(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<img src="/icons/save.svg" alt="Save">`,
			`<svg class="icon" role="img" aria-label="Save"><use href="#i-save"></use></svg>`},
		// An empty alt says the icon is decoration, and a decorative icon should not
		// be announced.
		{`<img src="/icons/save.svg" alt="">`,
			`<svg class="icon" aria-hidden="true"><use href="#i-save"></use></svg>`},
		// A missing alt is not the same as an empty one to a validator, but there is
		// no name to use either way.
		{`<img src="/icons/save.svg">`,
			`<svg class="icon" aria-hidden="true"><use href="#i-save"></use></svg>`},
		// The name is the file's, with the query and fragment off it.
		{`<img src="/icons/save.svg?v=2" alt="S">`,
			`<svg class="icon" role="img" aria-label="S"><use href="#i-save"></use></svg>`},
		{`<img src="save.SVG" alt="S">`,
			`<svg class="icon" role="img" aria-label="S"><use href="#i-save"></use></svg>`},
	} {
		got, res := inject(t, tc.doc, std())
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%q\n got %q\nwant it to start with %q", tc.doc, got, tc.want)
		}
		if res.Icons != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestAnImageThatIsNotAnIconIsLeftAlone.
func TestAnImageThatIsNotAnIconIsLeftAlone(t *testing.T) {
	for _, doc := range []string{
		`<img src="photo.png" alt="p">`,
		`<img src="photo.jpeg">`,
		`<img alt="no src">`,
		`<img src=".svg">`,
		`<img src="/icons/.svg">`,
	} {
		got, res := inject(t, doc, std())
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Icons != 0 || res.Injected {
			t.Errorf("%q: %v", doc, res)
		}
	}
}

// TestTheNameStaysInAnAttribute, which is the point: an alt attribute may hold a
// raw "<", and an element's text is where that becomes markup.
func TestTheNameStaysInAnAttribute(t *testing.T) {
	const doc = `<img src="i/save.svg" alt="<img src=x onerror=alert(1)>">`
	got, _ := inject(t, doc, std())
	if strings.Contains(got, "onerror=alert(1)>") && !strings.Contains(got, `aria-label="<img src=x onerror=alert(1)>"`) {
		t.Fatalf("the alt escaped its attribute: %q", got)
	}
	// It reads back as one attribute, and the rewritten document has no img in it.
	var labels, imgs int
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnElement("svg[aria-label]", func(*lolhtml.Element) error { labels++; return nil }),
		lolhtml.OnElement("img", func(*lolhtml.Element) error { imgs++; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	if labels != 1 || imgs != 0 {
		t.Errorf("labels=%d imgs=%d in %q", labels, imgs, got)
	}
}

// TestOnlyTheQuoteIsEscaped, because a value that came from the document is already
// source for an attribute: escaping "&" would change what it says.
func TestOnlyTheQuoteIsEscaped(t *testing.T) {
	for _, tc := range []struct{ alt, want string }{
		{`Save &amp; go`, `Save &amp; go`},
		{`Save & go`, `Save & go`},
		{`a<b`, `a<b`},
		{`a'b`, `a'b`},
	} {
		got, _ := inject(t, `<img src="i/s.svg" alt="`+tc.alt+`">`, std())
		if !strings.Contains(got, `aria-label="`+tc.want+`"`) {
			t.Errorf("alt %q gave %q, want aria-label %q", tc.alt, got, tc.want)
		}
	}
	// A double quote can only arrive through a single-quoted attribute, and it is
	// the one character that would end the attribute being written.
	got, _ := inject(t, `<img src="i/s.svg" alt='a"b'>`, std())
	if !strings.Contains(got, `aria-label="a&quot;b"`) {
		t.Errorf("got %q", got)
	}
	// What the library writes for the same value, which is the rule being followed.
	var mirror string
	if _, err := lolhtml.RewriteString(`<span></span>`, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		return e.SetAttribute("aria-label", `a"b`)
	})); err != nil {
		t.Fatal(err)
	}
	mirror = `aria-label="a&quot;b"`
	if !strings.Contains(got, mirror) {
		t.Errorf("got %q, want the library's own spelling %q", got, mirror)
	}
}

// TestTheSpriteGoesInOnce.
func TestTheSpriteGoesInOnce(t *testing.T) {
	got, res := inject(t, `<p>a</p><img src="i/save.svg" alt="S"><img src="i/save.svg" alt="S">`, std())
	if n := strings.Count(got, "data-sprite"); n != 1 {
		t.Errorf("%d sprites in %q", n, got)
	}
	if res.Icons != 2 || !res.Injected {
		t.Errorf("%v", res)
	}
	// The default position is the end of the document, which is the only one still
	// available once the first icon has gone past.
	if !strings.HasSuffix(got, `</svg>`) || !strings.Contains(got[strings.Index(got, "data-sprite"):], "symbol") {
		t.Errorf("the sprite is not at the end: %q", got)
	}
	// And it is not injected at all when the page has no icons, which is what
	// waiting bought.
	got, res = inject(t, `<p>a</p><img src="p.png">`, std())
	if strings.Contains(got, "data-sprite") || res.Injected {
		t.Errorf("got %q, %v", got, res)
	}
}

// TestATopInjectionDoesNotWaitAndDoesNotNeedABodyTag, since most documents do not
// spell one.
func TestATopInjectionDoesNotWaitAndDoesNotNeedABodyTag(t *testing.T) {
	opts := std()
	opts.AtTop = true
	for _, tc := range []struct{ doc, before string }{
		{`<p>a</p>`, `<p>`},
		{`<body><p>a</p></body>`, `<p>`},
		{`<html><head><title>t</title></head><body><p>a</p></body></html>`, `<p>`},
		{`<head><meta charset="utf-8"></head><p>a</p>`, `<p>`},
		{`<div><p>a</p></div>`, `<div>`},
	} {
		got, res := inject(t, tc.doc, opts)
		if !res.Injected {
			t.Errorf("%q: nothing was injected", tc.doc)
			continue
		}
		sprite, body := strings.Index(got, "data-sprite"), strings.Index(got, tc.before)
		if sprite < 0 || body < 0 || sprite > body {
			t.Errorf("%q gave %q: the sprite is not before %q", tc.doc, got, tc.before)
		}
		if n := strings.Count(got, "data-sprite"); n != 1 {
			t.Errorf("%q: %d sprites", tc.doc, n)
		}
	}
	// A page with no icons gets one anyway: that is what not waiting costs.
	got, res := inject(t, `<p>a</p>`, opts)
	if !res.Injected || !strings.Contains(got, "symbol") {
		t.Errorf("got %q, %v", got, res)
	}
}

// TestInjectingTwiceChangesNothing, which the marker attribute is for.
func TestInjectingTwiceChangesNothing(t *testing.T) {
	for _, top := range []bool{false, true} {
		opts := std()
		opts.AtTop = top
		docs := []string{`<p>a</p><img src="i/save.svg" alt="S">`, `<p>a</p>`}
		if !top {
			// A document that spells <body> is the one case a top injection cannot
			// get right: see the test below.
			docs = append(docs, `<body><img src="i/save.svg" alt=""></body>`)
		}
		for _, doc := range docs {
			once, _ := inject(t, doc, opts)
			twice, res := inject(t, once, opts)
			if twice != once {
				t.Errorf("%q (top=%v)\n once %q\ntwice %q", doc, top, once, twice)
			}
			if res.Injected && !res.Doubled {
				t.Errorf("%q (top=%v): a second sprite was injected quietly", doc, top)
			}
			if strings.Contains(once, "data-sprite") && !res.Present {
				t.Errorf("%q (top=%v): the sprite was not recognised", doc, top)
			}
		}
	}
	// A sprite the page already had is left where it is, and no icons are missed.
	got, res := inject(t, spriteWithMarker()+`<img src="i/save.svg" alt="S">`, std())
	if n := strings.Count(got, "data-sprite"); n != 1 {
		t.Errorf("%d sprites in %q", n, got)
	}
	if res.Icons != 1 || res.Injected || !res.Present {
		t.Errorf("%v", res)
	}
}

// TestATopInjectionCannotTellWhetherOneIsThereAlready, because the position is
// above the evidence. It says so rather than pretending.
func TestATopInjectionCannotTellWhetherOneIsThereAlready(t *testing.T) {
	opts := std()
	opts.AtTop = true
	// The body tag comes before the sprite the document already has.
	got, res := inject(t, `<body>`+spriteWithMarker()+`<p>a</p></body>`, opts)
	if n := strings.Count(got, "data-sprite"); n != 2 {
		t.Errorf("%d sprites in %q, want 2", n, got)
	}
	if !res.Doubled || res.OK() {
		t.Errorf("%v: the duplicate was not reported", res)
	}
	// -at end reaches the evidence before the position, so it does not.
	opts.AtTop = false
	got, res = inject(t, `<body>`+spriteWithMarker()+`<img src="i/save.svg" alt=""></body>`, opts)
	if n := strings.Count(got, "data-sprite"); n != 1 {
		t.Errorf("%d sprites in %q, want 1", n, got)
	}
	if res.Doubled || !res.OK() {
		t.Errorf("%v", res)
	}
	// Without a body tag the first element is the sprite itself, and the marker
	// handler runs before the one looking for a position, so even -at top is safe.
	got, res = inject(t, spriteWithMarker()+`<p>a</p>`, opts)
	if n := strings.Count(got, "data-sprite"); n != 1 {
		t.Errorf("%d sprites in %q, want 1", n, got)
	}
	if res.Doubled {
		t.Errorf("%v", res)
	}
}

func spriteWithMarker() string {
	return `<svg data-sprite="1" hidden><symbol id="i-save"></symbol></svg>`
}

// TestThePrefixAndClassAreTheCallers.
func TestThePrefixAndClassAreTheCallers(t *testing.T) {
	opts := std()
	opts.Prefix, opts.Class = "sym_", "ic ic-sm"
	got, _ := inject(t, `<img src="i/save.svg" alt="">`, opts)
	if !strings.HasPrefix(got, `<svg class="ic ic-sm" aria-hidden="true"><use href="#sym_save">`) {
		t.Errorf("got %q", got)
	}
	// An empty prefix is a prefix.
	opts.Prefix, opts.Class = "", "icon"
	got, _ = inject(t, `<img src="i/save.svg" alt="">`, Options{Sprite: spriteMarkup, Prefix: "", Class: "icon", Marker: "data-sprite"})
	if !strings.Contains(got, `href="#i-save"`) {
		t.Errorf("an empty prefix should fall back to the default: %q", got)
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<p>a</p><img src="/icons/save.svg" alt="Save &amp; go"><img src="p.png"><img src="/icons/x.svg" alt="">`
	want, wantRes := inject(t, doc, std())
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		i := &injector{opts: std(), seen: map[string]bool{}}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, i.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for k := 0; k < len(doc); k += size {
			if _, err := w.Write([]byte(doc[k:min(k+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if i.res != wantRes {
			t.Errorf("chunks of %d: %v, want %v", size, i.res, wantRes)
		}
	}
}

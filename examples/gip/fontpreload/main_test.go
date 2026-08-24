package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const css = `
@font-face { font-family: A; src: url("../fonts/a.woff2") format("woff2"), url(../fonts/a.woff) format("woff"); }
@font-face { font-family: B; src: url(https://cdn.example/b.woff2); }
@font-face { font-family: C; src: url(data:font/woff2;base64,AA); }
@font-face { font-family: D; src: url(/fonts/d.eot); }
.not-a-face { background: url(/img/bg.png); }
`

const page = `<html><head><title>t</title></head><body>x</body></html>`

func based(in *injector) { in.base = "https://example.com/css/" }

var corpus = []string{
	page,
	`<html><head><link rel="preload" as="font" href="https://example.com/fonts/a.woff2" crossorigin></head><body>x</body></html>`,
	`<html><head></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// preloads returns the href of every font preload a parser finds.
func preloads(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`link[rel~="preload"][as="font"]`, func(e *lolhtml.Element) error {
			out = append(out, attr(e, "href"))
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(doc string, n int, opts ...func(*injector)) (string, *injector, error) {
	in := defaults()
	in.css = css
	for _, o := range opts {
		o(in)
	}
	if err := in.validate(); err != nil {
		return "", nil, err
	}
	in.collect()
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, in.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(doc); i += n {
		end := min(i+n, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), in, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := chunked(doc, len(doc)+1, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 29} {
			got, _, err := chunked(doc, n, based)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := injectString(doc, css, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, in, err := injectString(once, css, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if in.added != 0 {
			t.Errorf("the second pass of %q added %d", doc, in.added)
		}
	}
}

// TestOnlyFontFaceURLsAreCollected. A background image in an ordinary rule is not
// a font, and preloading it would cost a request on every page view.
func TestOnlyFontFaceURLsAreCollected(t *testing.T) {
	_, in, err := injectString(page, css, based, func(in *injector) { in.only = "" })
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range in.fonts {
		if strings.Contains(f.href, "bg.png") {
			t.Errorf("a background image was collected: %v", in.fonts)
		}
	}
	// Two woff2 and one woff, from the two resolvable faces.
	if len(in.fonts) != 3 {
		t.Errorf("fonts = %v, want three", in.fonts)
	}
}

func TestFontFaceBlocks(t *testing.T) {
	for _, tt := range []struct {
		css  string
		want int
	}{
		{`@font-face { src: url(a) }`, 1},
		{`@FONT-FACE { src: url(a) }`, 1},
		{`@font-face{src:url(a)}@font-face{src:url(b)}`, 2},
		{`.x { color: red } @font-face { src: url(a) }`, 1},
		{`@font-face { src: url(a)`, 0}, // unterminated
		{`@font-face`, 0},
		{``, 0},
		{`.x { background: url(a) }`, 0},
	} {
		if got := len(fontFaceBlocks(tt.css)); got != tt.want {
			t.Errorf("fontFaceBlocks(%q) found %d, want %d", tt.css, got, tt.want)
		}
	}
}

func TestURLsIn(t *testing.T) {
	for _, tt := range []struct {
		css  string
		want []string
	}{
		{`src: url(a.woff2)`, []string{"a.woff2"}},
		{`src: url("a.woff2")`, []string{"a.woff2"}},
		{`src: url('a.woff2')`, []string{"a.woff2"}},
		{`src: url( a.woff2 )`, []string{"a.woff2"}},
		{`src: url(a) format("woff"), url(b)`, []string{"a", "b"}},
		{`src: URL(a)`, []string{"a"}},
		{`src: url()`, nil},
		{`src: url(`, nil},
		{`no urls here`, nil},
	} {
		got := urlsIn(tt.css)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("urlsIn(%q) = %v, want %v", tt.css, got, tt.want)
		}
	}
}

// TestAFontWithNoKnownTypeIsNotPreloaded. A preload without the right type is
// ignored or fetched twice, so a guess would be worse than a refusal.
func TestAFontWithNoKnownTypeIsNotPreloaded(t *testing.T) {
	_, in, err := injectString(page,
		`@font-face { src: url(/f/a.eot) } @font-face { src: url(/f/b.svg) }`,
		based, func(in *injector) { in.only = "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(in.fonts) != 0 {
		t.Errorf("fonts = %v, want none", in.fonts)
	}
	if total(in.skipped) != 2 {
		t.Errorf("skipped = %v, want a note for each", in.skipped)
	}
}

// TestEveryPreloadCarriesTypeAndCrossorigin. A font fetch is CORS, so a preload
// without crossorigin does not match the request and the font is fetched twice.
func TestEveryPreloadCarriesTypeAndCrossorigin(t *testing.T) {
	out, _, err := injectString(page, css, based)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement(`link[rel~="preload"]`, func(e *lolhtml.Element) error {
			checked++
			if v, _ := e.Attribute("as"); v != "font" {
				t.Errorf(`as=%q, want "font"`, v)
			}
			if v, _ := e.Attribute("type"); v != "font/woff2" {
				t.Errorf("type=%q", v)
			}
			if _, ok := e.Attribute("crossorigin"); !ok {
				t.Error("crossorigin is missing, so the font will be fetched twice")
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no preloads were emitted")
	}
}

// TestARelativeURLNeedsABase, because a preload has to resolve to the same URL
// the stylesheet will request.
func TestARelativeURLNeedsABase(t *testing.T) {
	_, in, err := injectString(page, `@font-face { src: url(../fonts/a.woff2) }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.fonts) != 0 {
		t.Errorf("fonts = %v, want none without a base", in.fonts)
	}
	if total(in.skipped) == 0 {
		t.Error("the relative url was not reported")
	}

	// With a base it resolves against the stylesheet's own location.
	_, in, err = injectString(page, `@font-face { src: url(../fonts/a.woff2) }`, based)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.fonts) != 1 || in.fonts[0].href != "https://example.com/fonts/a.woff2" {
		t.Errorf("fonts = %v", in.fonts)
	}
}

// TestADataURLNeedsNoPreload: it is already in the stylesheet.
func TestADataURLNeedsNoPreload(t *testing.T) {
	_, in, err := injectString(page, `@font-face { src: url(data:font/woff2;base64,AA) }`, based)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.fonts) != 0 {
		t.Errorf("fonts = %v", in.fonts)
	}
}

// TestAFontThePageAlreadyPreloadsIsNotPreloadedAgain. The existing link is in the
// head, which the insertion point is after - the one ordering this program gets
// for free.
func TestAFontThePageAlreadyPreloadsIsNotPreloadedAgain(t *testing.T) {
	out, in, err := injectString(
		`<html><head><link rel="preload" as="font" href="https://example.com/fonts/a.woff2" crossorigin>`+
			`</head><body>x</body></html>`, css, based)
	if err != nil {
		t.Fatal(err)
	}
	hrefs := preloads(t, out)
	count := 0
	for _, h := range hrefs {
		if h == "https://example.com/fonts/a.woff2" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the font is preloaded %d times: %v", count, hrefs)
	}
	if total(in.skipped) == 0 {
		t.Error("the existing preload was not reported")
	}
}

// TestTheCapIsReportedOnce, and the note is not double-counted.
func TestTheCapIsReportedOnce(t *testing.T) {
	var sb strings.Builder
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		sb.WriteString(`@font-face { src: url(https://cdn.example/` + name + `.woff2) }`)
	}
	out, in, err := injectString(page, sb.String(), func(in *injector) { in.max = 2 })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(preloads(t, out)); n != 2 {
		t.Errorf("%d preloads, want 2", n)
	}
	for reason, n := range in.skipped {
		if strings.Contains(reason, "beyond -max") && n != 1 {
			t.Errorf("%q counted %d times", reason, n)
		}
	}
}

// TestOnlyFiltersByExtension, since a woff fallback beside a woff2 is a second
// request for the same face.
func TestOnlyFiltersByExtension(t *testing.T) {
	_, in, err := injectString(page,
		`@font-face { src: url(https://c.example/a.woff2), url(https://c.example/a.woff) }`,
		func(in *injector) { in.only = ".woff2" })
	if err != nil {
		t.Fatal(err)
	}
	if len(in.fonts) != 1 || !strings.HasSuffix(in.fonts[0].href, ".woff2") {
		t.Errorf("fonts = %v", in.fonts)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*injector)
	}{
		{"no css", func(in *injector) { in.css = "" }},
		{"a zero cap", func(in *injector) { in.max = 0 }},
		{"a relative base", func(in *injector) { in.base = "/css/" }},
		{"an unknown -only", func(in *injector) { in.only = ".eot" }},
	} {
		if _, _, err := injectString(page, css, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestTheHrefIsEscaped: the links are assembled as markup.
func TestTheHrefIsEscaped(t *testing.T) {
	out, _, err := injectString(page,
		`@font-face { src: url(https://c.example/a.woff2?v=1&x=2) }`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="https://c.example/a.woff2?v=1&amp;x=2"`) {
		t.Errorf("the href was not escaped: %s", out)
	}
}

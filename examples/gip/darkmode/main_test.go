package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func both(a *adder) {
	a.dark = "#101014"
	a.light = "#ffffff"
	a.stylesheet = "/dark.css"
}

var corpus = []string{
	`<html><head><title>t</title></head><body>x</body></html>`,
	`<html><head><meta name="theme-color" content="#000"></head><body>x</body></html>`,
	`<html><head><meta name="theme-color" content="#fff" media="(prefers-color-scheme: light)"></head><body>x</body></html>`,
	`<html><head><meta name="theme-color" content="#000" media="(prefers-color-scheme: dark)"></head><body>x</body></html>`,
	`<html><head><link rel="stylesheet" href="/d.css" media="(prefers-color-scheme: dark)"></head><body>x</body></html>`,
	`<html><head><link rel="stylesheet" href="/a.css"></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// tags asks the parser what the document declares.
func tags(t *testing.T, doc string) (themeColors []string, sheets []string) {
	t.Helper()
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`meta[name="theme-color"]`, func(e *lolhtml.Element) error {
			themeColors = append(themeColors, attr(e, "content")+"|"+attr(e, "media"))
			return nil
		}),
		lolhtml.OnElement(`link[rel~="stylesheet"]`, func(e *lolhtml.Element) error {
			sheets = append(sheets, attr(e, "href")+"|"+attr(e, "media"))
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return themeColors, sheets
}

func chunked(in string, n int, opts ...func(*adder)) (string, *adder, error) {
	a := defaults()
	for _, o := range opts {
		o(a)
	}
	if err := a.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, a.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), a, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := chunked(doc, len(doc)+1, both)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 31} {
			got, _, err := chunked(doc, n, both)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := addString(doc, both)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, a, err := addString(once, both)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if len(a.inserted) != 0 {
			t.Errorf("the second pass of %q inserted %v", doc, a.inserted)
		}
	}
}

// TestBothSchemesAreDeclared, in a fixed order so the output is diffable.
func TestBothSchemesAreDeclared(t *testing.T) {
	out, a, err := addString(`<html><head><title>t</title></head><body>x</body></html>`, both)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a.inserted, ",") != "theme-color/light,theme-color/dark,stylesheet" {
		t.Errorf("inserted %v", a.inserted)
	}
	colors, sheets := tags(t, out)
	if len(colors) != 2 {
		t.Fatalf("%d theme-colors: %v", len(colors), colors)
	}
	if colors[0] != "#ffffff|(prefers-color-scheme: light)" {
		t.Errorf("first theme-color is %q", colors[0])
	}
	if colors[1] != "#101014|(prefers-color-scheme: dark)" {
		t.Errorf("second theme-color is %q", colors[1])
	}
	if len(sheets) != 1 || sheets[0] != "/dark.css|(prefers-color-scheme: dark)" {
		t.Errorf("sheets = %v", sheets)
	}
}

// TestAPageWithAnOpinionKeepsIt, per scheme: a page that declares dark keeps
// dark and still gets light.
func TestAPageWithAnOpinionKeepsIt(t *testing.T) {
	for _, tt := range []struct {
		doc      string
		wantKeys string
	}{
		{`<html><head><meta name="theme-color" content="#000" media="(prefers-color-scheme: dark)"></head><body>x</body></html>`,
			"theme-color/light,stylesheet"},
		{`<html><head><meta name="theme-color" content="#fff" media="(prefers-color-scheme: light)"></head><body>x</body></html>`,
			"theme-color/dark,stylesheet"},
		{`<html><head><link rel="stylesheet" href="/d.css" media="(prefers-color-scheme: dark)"></head><body>x</body></html>`,
			"theme-color/light,theme-color/dark"},
	} {
		_, a, err := addString(tt.doc, both)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(a.inserted, ","); got != tt.wantKeys {
			t.Errorf("%q -> inserted %q, want %q", tt.doc, got, tt.wantKeys)
		}
		if total(a.skipped) == 0 {
			t.Errorf("%q: keeping the page's own was not reported", tt.doc)
		}
	}
}

// TestABareThemeColourIsReportedNotOverridden. It applies to both schemes, which
// is what this program replaces - but removing someone else's tag is not this
// program's call, so it says so instead.
func TestABareThemeColourIsReportedNotOverridden(t *testing.T) {
	const doc = `<html><head><meta name="theme-color" content="#000"></head><body>x</body></html>`
	out, a, err := addString(doc, both)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<meta name="theme-color" content="#000">`) {
		t.Errorf("the bare theme-color was changed: %s", out)
	}
	if len(a.inserted) != 3 {
		t.Errorf("inserted %v, want all three", a.inserted)
	}
	found := false
	for reason := range a.skipped {
		if strings.Contains(reason, "no media") {
			found = true
		}
	}
	if !found {
		t.Errorf("the bare theme-color was not reported: %v", a.skipped)
	}
}

// TestAStylesheetWithoutADarkQueryIsNotOne.
func TestAStylesheetWithoutADarkQueryIsNotOne(t *testing.T) {
	_, a, err := addString(
		`<html><head><link rel="stylesheet" href="/a.css">`+
			`<link rel="stylesheet" href="/p.css" media="print"></head><body>x</body></html>`,
		both)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(a.inserted, "stylesheet") {
		t.Errorf("inserted %v, want the dark stylesheet", a.inserted)
	}
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestValidColour: a theme-color a browser cannot parse is ignored, which looks
// exactly like the meta being absent, so it is refused instead.
func TestValidColour(t *testing.T) {
	for _, good := range []string{
		"#fff", "#ffff", "#ffffff", "#ffffffff", "#ABC", "#abcdef",
		"black", "rebeccapurple", "WHITE",
	} {
		if !validColour(good) {
			t.Errorf("validColour(%q) = false", good)
		}
	}
	for _, bad := range []string{
		"", "#", "#ff", "#fffff", "#ggg", "#12345", "rgb(0,0,0)",
		"var(--x)", "1", "a b", "#ff ff", `#f" onload="x`,
	} {
		if validColour(bad) {
			t.Errorf("validColour(%q) = true", bad)
		}
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*adder)
	}{
		{"nothing given", func(a *adder) {}},
		{"an unparseable dark colour", func(a *adder) { a.dark = "rgb(0,0,0)" }},
		{"an unparseable light colour", func(a *adder) { a.light = "#12345" }},
		{"a colour with a quote in it", func(a *adder) { a.dark = `#f" onload="x` }},
	} {
		if _, _, err := addString(corpus[0], tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestWithNoHeadTheTagsGoBeforeBody.
func TestWithNoHeadTheTagsGoBeforeBody(t *testing.T) {
	out, a, err := addString(`<html><body>x</body></html>`, both)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.inserted) != 3 {
		t.Errorf("inserted %v", a.inserted)
	}
	i, j := strings.Index(out, "theme-color"), strings.Index(out, "<body>")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the tags are not before the body: %s", out)
	}
}

// TestAFragmentIsReported.
func TestAFragmentIsReported(t *testing.T) {
	out, a, err := addString(`<p>fragment</p>`, both)
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p>fragment</p>` || len(a.inserted) != 0 {
		t.Errorf("out=%q inserted=%v", out, a.inserted)
	}
	if total(a.skipped) != 1 {
		t.Errorf("skipped=%v, want one reason", a.skipped)
	}
}

// TestOneFlagAtATime: each of the three can be given alone.
func TestOneFlagAtATime(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*adder)
		want string
	}{
		{"dark only", func(a *adder) { a.dark = "#000" }, "theme-color/dark"},
		{"light only", func(a *adder) { a.light = "#fff" }, "theme-color/light"},
		{"stylesheet only", func(a *adder) { a.stylesheet = "/d.css" }, "stylesheet"},
	} {
		_, a, err := addString(corpus[0], tt.opt)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if strings.Join(a.inserted, ",") != tt.want {
			t.Errorf("%s inserted %v, want %s", tt.name, a.inserted, tt.want)
		}
	}
}

package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func measure(t *testing.T, doc string, m Manifest) Report {
	t.Helper()
	rep, err := Measure(strings.NewReader(doc), m)
	if err != nil {
		t.Fatalf("Measure(%q): %v", doc, err)
	}
	return rep
}

// The srcset attribute is not a comma-separated list, however much it looks like
// one: a URL may contain a comma, and a data: URL almost always does. The
// algorithm takes the URL up to whitespace, and a comma is a separator only
// where the algorithm says it is.
func TestParseSrcset(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one, no descriptor", "/a.png", []string{"/a.png"}},
		{"descriptors", "/a.png 1x, /b.png 2x", []string{"/a.png", "/b.png"}},
		{"width descriptors", "/a.png 320w,/b.png 640w", []string{"/a.png", "/b.png"}},
		{"no space after comma", "/a.png 1x,/b.png 2x", []string{"/a.png", "/b.png"}},
		// Not two candidates. The algorithm collects the URL up to whitespace,
		// and only strips commas when the URL *ends* with one - so this is a
		// single reference to a file with a comma in its name. Splitting on
		// commas would invent two URLs that are not there.
		{"comma inside url", "/a.png,/b.png", []string{"/a.png,/b.png"}},
		{"comma ends the url", "/a.png, /b.png", []string{"/a.png", "/b.png"}},
		{"newlines and tabs", "\n/a.png 1x,\n\t/b.png 2x\n", []string{"/a.png", "/b.png"}},
		{"leading commas", ", ,/a.png", []string{"/a.png"}},
		{"trailing comma", "/a.png 1x,", []string{"/a.png"}},
		// A comma inside the URL. Splitting on commas turns this one reference
		// into two URLs, neither of which exists.
		{"query with comma", "/a.png?crop=1,2,3 1x, /b.png 2x", []string{"/a.png?crop=1,2,3", "/b.png"}},
		{"data url", "data:image/gif;base64,R0lGOD 1x", []string{"data:image/gif;base64,R0lGOD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSrcset(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSrcset(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Inline weight is measured from the source, as the distance between the end of
// the start tag and the start of the end tag. Nothing else gives the bytes that
// were actually on the wire.
func TestInlineWeightIsMeasuredFromTheSource(t *testing.T) {
	tests := []struct {
		name           string
		doc            string
		script, style_ int64
	}{
		{"script", `<script>var x=1;</script>`, 8, 0},
		{"style", `<style>p{color:red}</style>`, 0, 12},
		{"both", `<script>ab</script><style>cd</style>`, 2, 2},
		{"empty", `<script></script>`, 0, 0},
		{"two scripts", `<script>ab</script><script>cde</script>`, 5, 0},
		// A character reference in a script is not decoded, so its source bytes
		// are what it weighs: seven, not three.
		{"reference is not decoded", `<script>a&amp;b</script>`, 7, 0},
		// A CRLF is two bytes on the wire even though a browser's DOM holds one
		// character.
		{"crlf", "<script>a\r\nb</script>", 4, 0},
		{"multibyte", `<script>é</script>`, 2, 0},
		// A script with a src does not execute its content, and the content is
		// still bytes in the document - but it is not this script's weight, and
		// counting it would double the src.
		{"src wins over content", `<script src="/a.js">ignored</script>`, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := measure(t, tt.doc, nil)
			if rep.Scripts.Inline != tt.script {
				t.Errorf("Scripts.Inline = %d, want %d", rep.Scripts.Inline, tt.script)
			}
			if rep.Styles.Inline != tt.style_ {
				t.Errorf("Styles.Inline = %d, want %d", rep.Styles.Inline, tt.style_)
			}
		})
	}
}

// The source span and the bytes a text handler sees must agree for raw text,
// where nothing is decoded. If they ever stop agreeing, one of the two is
// measuring something other than the document.
func TestTheSourceSpanAgreesWithTheTextBytes(t *testing.T) {
	docs := []string{
		`<script>var x=1;</script>`,
		`<script>a&amp;b</script>`,
		"<script>a\r\nb</script>",
		`<script>é</script>`,
		`<style>p{color:red}</style>`,
		`<script></script>`,
		"<script>" + strings.Repeat("x", 5000) + "</script>",
	}
	for _, doc := range docs {
		rep := measure(t, doc, nil)
		var textBytes int64
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnText("script, style", func(c *lolhtml.TextChunk) error {
				textBytes += int64(len(c.Bytes()))
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if got := rep.Scripts.Inline + rep.Styles.Inline; got != textBytes {
			t.Errorf("%.40q: source span %d, text bytes %d", doc, got, textBytes)
		}
	}
}

// An element whose end tag never arrives has no measurable span, so a truncated
// document reports its last inline script as weighing nothing. Reported here
// rather than papered over: the alternative is to accumulate text bytes, which
// measures a different thing and would disagree with the rest of the report.
func TestATruncatedScriptIsNotMeasured(t *testing.T) {
	rep := measure(t, `<script>var x=1;`, nil)
	if rep.Scripts.Inline != 0 {
		t.Errorf("Scripts.Inline = %d, want 0 for a script with no end tag", rep.Scripts.Inline)
	}
}

func TestExternalReferences(t *testing.T) {
	m := Manifest{"/a.js": 100, "/a.css": 200, "/a.png": 300, "/b.png": 400}
	doc := `<script src="/a.js"></script>` +
		`<link rel="stylesheet" href="/a.css">` +
		`<img src="/a.png" srcset="/a.png 1x, /b.png 2x">`
	rep := measure(t, doc, m)
	if rep.Scripts.Known != 100 {
		t.Errorf("Scripts.Known = %d, want 100", rep.Scripts.Known)
	}
	if rep.Styles.Known != 200 {
		t.Errorf("Styles.Known = %d, want 200", rep.Styles.Known)
	}
	// /a.png appears twice and is counted once.
	if rep.Images.Known != 700 {
		t.Errorf("Images.Known = %d, want 700", rep.Images.Known)
	}
	if want := int64(1000); rep.Total() != want {
		t.Errorf("Total = %d, want %d", rep.Total(), want)
	}
	if rep.LargestCandidates != 400 {
		t.Errorf("LargestCandidates = %d, want 400", rep.LargestCandidates)
	}
}

// A URL referenced twice is fetched once, so it weighs once. This is the whole
// reason the program keeps a set rather than a running total.
func TestARepeatedURLIsCountedOnce(t *testing.T) {
	m := Manifest{"/a.png": 1000}
	doc := strings.Repeat(`<img src="/a.png">`, 10)
	rep := measure(t, doc, m)
	if rep.Images.Known != 1000 {
		t.Errorf("Images.Known = %d, want 1000", rep.Images.Known)
	}
	if len(rep.Images.URLs) != 1 {
		t.Errorf("URLs = %q, want one", rep.Images.URLs)
	}
}

// A URL with no size is not zero. Counting it as zero is how a weight report
// comes out reassuring and wrong.
func TestUnknownSizesAreReportedNotAssumed(t *testing.T) {
	rep := measure(t, `<img src="/a.png"><script src="/a.js"></script>`, Manifest{"/a.png": 50})
	if rep.Images.Known != 50 {
		t.Errorf("Images.Known = %d, want 50", rep.Images.Known)
	}
	if got := rep.Unknown(); !reflect.DeepEqual(got, []string{"/a.js"}) {
		t.Errorf("Unknown = %q, want [/a.js]", got)
	}
}

// rel is a token list and its values are matched case-insensitively.
func TestStylesheetLinksAreRecognised(t *testing.T) {
	m := Manifest{"/a.css": 10}
	yes := []string{
		`<link rel="stylesheet" href="/a.css">`,
		`<link rel="StyleSheet" href="/a.css">`,
		`<link rel="preload stylesheet" href="/a.css">`,
		`<link rel=" stylesheet " href="/a.css">`,
	}
	for _, doc := range yes {
		if rep := measure(t, doc, m); rep.Styles.Known != 10 {
			t.Errorf("%s: Styles.Known = %d, want 10", doc, rep.Styles.Known)
		}
	}
	no := []string{
		`<link rel="preload" href="/a.css">`,
		`<link rel="stylesheetx" href="/a.css">`,
		`<link href="/a.css">`,
	}
	for _, doc := range no {
		if rep := measure(t, doc, m); rep.Styles.Known != 0 {
			t.Errorf("%s: Styles.Known = %d, want 0", doc, rep.Styles.Known)
		}
	}
}

// The rewriter is a stream, so the boundaries a reader happens to produce must
// not change the report. A boundary inside an inline script is the case that
// would break a measurement built from chunk lengths.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><head><style>p{color:red}</style>` +
		`<script src="/a.js"></script></head>` +
		`<body><script>var x = 1;</script>` +
		`<img src="/a.png" srcset="/a.png 1x, /b.png 2x"></body></html>`
	m := Manifest{"/a.js": 100, "/a.png": 300, "/b.png": 400}
	want := measure(t, doc, m)
	for n := 1; n <= len(doc); n++ {
		got, err := Measure(&chunked{s: doc, n: n}, m)
		if err != nil {
			t.Fatalf("chunk %d: %v", n, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d changed the report:\n got %+v\nwant %+v", n, got, want)
		}
	}
}

// chunked hands out at most n bytes per Read.
type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

func TestPictureSources(t *testing.T) {
	m := Manifest{"/a.webp": 100, "/a.png": 200, "/a2.png": 900}
	doc := `<picture>` +
		`<source type="image/webp" srcset="/a.webp">` +
		`<img src="/a.png" srcset="/a.png 1x, /a2.png 2x" alt="">` +
		`</picture>`
	rep := measure(t, doc, m)
	if want := int64(1200); rep.Images.Known != want {
		t.Errorf("Images.Known = %d, want %d", rep.Images.Known, want)
	}
	// One srcset offers /a.webp alone, the other tops out at /a2.png.
	if want := int64(1000); rep.LargestCandidates != want {
		t.Errorf("LargestCandidates = %d, want %d", rep.LargestCandidates, want)
	}
}

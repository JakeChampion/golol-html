package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = `<html><head><title>t</title></head><body>` +
	`<h1>Widget &amp; Co</h1><img src="/w.png"><img src="/x.png"></body></html>`

func based(f *filler) { f.base = "https://example.com/p" }

var corpus = []string{
	page,
	`<html><head><meta property="og:title" content="Kept"></head><body><h1>Ignored</h1><img src="/w.png"></body></html>`,
	`<html><head></head><body><h2>Only an h2</h2></body></html>`,
	`<html><head></head><body><h1>   </h1><h2>Second</h2></body></html>`,
	`<html><head></head><body><img src="https://cdn.example/a.png"></body></html>`,
	`<html><body><h1>No head element</h1></body></html>`,
	`<html><head></head><body>nothing to compute from</body></html>`,
	`<p>fragment</p>`,
	``,
}

// ogTags asks the parser what the document declares.
func ogTags(t *testing.T, doc string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("meta[property]", func(e *lolhtml.Element) error {
			key := strings.ToLower(attr(e, "property"))
			if _, seen := out[key]; !seen {
				out[key] = attr(e, "content")
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeChunked(t *testing.T, doc string, size int, opts ...func(*filler)) string {
	t.Helper()
	f := defaults()
	for _, o := range opts {
		o(f)
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.readPass([]byte(doc)); err != nil {
		t.Fatal(err)
	}

	markup := f.markup()
	var out strings.Builder
	sawHead := false
	placed := markup == ""
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if placed {
					return nil
				}
				placed = true
				return end.Before(markup, lolhtml.HTML)
			})
		}),
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if sawHead || placed {
				return nil
			}
			placed = true
			return e.Before(markup, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestTheWritePassIsChunkInvariant. The reading pass takes the whole document by
// construction, so the writing pass is what chunking can affect.
func TestTheWritePassIsChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		whole := writeChunked(t, doc, len(doc)+1, based)
		for _, size := range []int{1, 2, 3, 29} {
			if got := writeChunked(t, doc, size, based); got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					size, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := fillString(doc, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, f, err := fillString(once, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if len(f.inserted) != 0 {
			t.Errorf("the second pass of %q inserted %v", doc, f.inserted)
		}
	}
}

// TestTheTagsAreComputedFromThePage, which is the two-pass case: the tags go in
// the head and what they say comes from the body.
func TestTheTagsAreComputedFromThePage(t *testing.T) {
	out, f, err := fillString(page, based)
	if err != nil {
		t.Fatal(err)
	}
	if f.passes != 2 {
		t.Errorf("passes=%d, want 2", f.passes)
	}
	tags := ogTags(t, out)
	if tags["og:title"] != "Widget &amp; Co" {
		t.Errorf("og:title = %q", tags["og:title"])
	}
	if tags["og:image"] != "https://example.com/w.png" {
		t.Errorf("og:image = %q", tags["og:image"])
	}
	// In the head, and before the body they were read from.
	if i, j := strings.Index(out, "og:title"), strings.Index(out, "</head>"); i < 0 || i > j {
		t.Errorf("the tags are not in the head: %s", out)
	}
}

// TestTheFirstImageWins, not the last: a page's first image is the one a preview
// should use.
func TestTheFirstImageWins(t *testing.T) {
	out, _, err := fillString(page, based)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/w.png") || strings.Contains(ogTags(t, out)["og:image"], "/x.png") {
		t.Errorf("og:image = %q, want the first image", ogTags(t, out)["og:image"])
	}
}

// TestAPageThatSaysSomethingKeepsIt: this program's guesses are for pages that
// said nothing.
func TestAPageThatSaysSomethingKeepsIt(t *testing.T) {
	out, f, err := fillString(
		`<html><head><meta property="og:title" content="Kept"></head><body>`+
			`<h1>Ignored</h1><img src="/w.png"></body></html>`, based)
	if err != nil {
		t.Fatal(err)
	}
	if ogTags(t, out)["og:title"] != "Kept" {
		t.Errorf("og:title = %q, want the page's own", ogTags(t, out)["og:title"])
	}
	if containsString(f.inserted, "og:title") {
		t.Errorf("og:title was inserted anyway: %v", f.inserted)
	}
	if total(f.skipped) == 0 {
		t.Error("keeping the page's own tag was not reported")
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

// TestAnEmptyOgPropertyDoesNotCount. A property present with no content looks
// satisfied and is not, so the page has not said anything.
func TestAnEmptyOgPropertyDoesNotCount(t *testing.T) {
	out, f, err := fillString(
		`<html><head><meta property="og:title" content=""></head><body><h1>Real</h1></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(f.inserted, "og:title") {
		t.Errorf("og:title was not filled in: %v %s", f.inserted, out)
	}
}

// TestARelativeImageIsReportedNotEmitted, because an og:image has to be absolute
// to be any use and inventing a host would be worse.
func TestARelativeImageIsReportedNotEmitted(t *testing.T) {
	out, f, err := fillString(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ogTags(t, out)["og:image"]; ok {
		t.Errorf("a relative image was emitted: %s", out)
	}
	if total(f.skipped) == 0 {
		t.Error("the relative image was not reported")
	}
	// An already-absolute image needs no base.
	out, _, err = fillString(
		`<html><head></head><body><img src="https://cdn.example/a.png"></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if ogTags(t, out)["og:image"] != "https://cdn.example/a.png" {
		t.Errorf("og:image = %q", ogTags(t, out)["og:image"])
	}
}

// TestAWhitespaceHeadingIsNotATitle, and the next heading is tried instead.
func TestAWhitespaceHeadingIsNotATitle(t *testing.T) {
	out, _, err := fillString(
		`<html><head></head><body><h1>   </h1><h2>Second</h2></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := ogTags(t, out)["og:title"]; got != "Second" {
		t.Errorf("og:title = %q, want Second", got)
	}
}

// TestGivingBothSkipsTheReadingPass, which is the point of the flags.
func TestGivingBothSkipsTheReadingPass(t *testing.T) {
	out, f, err := fillString(page, func(f *filler) {
		f.title = "Given"
		f.image = "https://cdn.example/g.png"
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.passes != 1 {
		t.Errorf("passes=%d, want 1", f.passes)
	}
	if f.haveTitle != "" || f.haveImage != "" {
		t.Errorf("the reading pass ran anyway: %q %q", f.haveTitle, f.haveImage)
	}
	tags := ogTags(t, out)
	if tags["og:title"] != "Given" || tags["og:image"] != "https://cdn.example/g.png" {
		t.Errorf("tags = %v", tags)
	}
}

// TestWithNoHeadTheTagsGoBeforeBody, the position #63 established.
func TestWithNoHeadTheTagsGoBeforeBody(t *testing.T) {
	out, _, err := fillString(`<html><body><h1>Title</h1></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(out, "og:title"), strings.Index(out, "<body>")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the tags are not before the body: %s", out)
	}
}

// TestAFragmentIsReported rather than guessed at.
func TestAFragmentIsReported(t *testing.T) {
	out, f, err := fillString(`<p>fragment</p>`, based)
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p>fragment</p>` {
		t.Errorf("the document changed: %s", out)
	}
	if len(f.inserted) != 0 {
		t.Errorf("inserted %v", f.inserted)
	}
}

// TestTheValuesAreEscaped: the tags are assembled as markup.
func TestTheValuesAreEscaped(t *testing.T) {
	out, _, err := fillString(
		`<html><head></head><body><h1>a &amp; b "quoted"</h1></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `content="a &amp; b &quot;quoted&quot;"`) {
		t.Errorf("the title was not escaped: %s", out)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*filler)
	}{
		{"relative base", func(f *filler) { f.base = "/p" }},
		{"unparseable base", func(f *filler) { f.base = "http://[::1" }},
		{"relative image", func(f *filler) { f.image = "/a.png" }},
	} {
		if _, _, err := fillString(page, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

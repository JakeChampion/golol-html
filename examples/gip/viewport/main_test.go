package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<html><head><title>t</title></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content="width=device-width, user-scalable=no"></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content="user-scalable=no"></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content="width=1024"></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content=""></head><body>x</body></html>`,
	`<html><head><meta name="viewport"></head><body>x</body></html>`,
	`<html><head><meta name="viewport" content="width=device-width"><meta name="viewport" content="user-scalable=no"></head><body>x</body></html>`,
	`<html><head><meta name="VIEWPORT" content="user-scalable=no"></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// viewports returns the content of every viewport meta, in document order.
func viewports(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`meta[name="viewport"]`, func(e *lolhtml.Element) error {
			v, _ := e.Attribute("content")
			out = append(out, v)
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(in string, n int, opts ...func(*fixer)) (string, *fixer, error) {
	f := defaults()
	for _, o := range opts {
		o(f)
	}
	if err := f.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, f.options()...)
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
	return out.String(), f, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, in := range corpus {
		whole, _, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, n := range []int{1, 2, 3, 19} {
			got, _, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, in, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range corpus {
		once, _, err := fixString(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		twice, f, err := fixString(once)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", in, once, twice)
		}
		if f.added != 0 || f.repaired != 0 {
			t.Errorf("the second pass of %q added=%d repaired=%d", in, f.added, f.repaired)
		}
	}
}

// TestTheThreeCases is the whole design: absent, harmful, and merely different.
func TestTheThreeCases(t *testing.T) {
	for _, tt := range []struct {
		name, in, wantContent string
		added, repaired       int
	}{
		{
			name:        "absent",
			in:          `<html><head><title>t</title></head><body>x</body></html>`,
			wantContent: "width=device-width, initial-scale=1",
			added:       1,
		},
		{
			name:        "harmful, with something worth keeping",
			in:          `<html><head><meta name="viewport" content="width=device-width, user-scalable=no, maximum-scale=1"></head><body>x</body></html>`,
			wantContent: "width=device-width",
			repaired:    1,
		},
		{
			name:        "harmful, with nothing left",
			in:          `<html><head><meta name="viewport" content="user-scalable=no"></head><body>x</body></html>`,
			wantContent: "width=device-width, initial-scale=1",
			repaired:    1,
		},
		{
			name:        "merely different",
			in:          `<html><head><meta name="viewport" content="width=1024"></head><body>x</body></html>`,
			wantContent: "width=1024",
		},
	} {
		out, f, err := fixString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got := viewports(t, out)
		if len(got) != 1 {
			t.Errorf("%s: %d viewports: %v", tt.name, len(got), got)
			continue
		}
		if got[0] != tt.wantContent {
			t.Errorf("%s: content %q, want %q", tt.name, got[0], tt.wantContent)
		}
		if f.added != tt.added || f.repaired != tt.repaired {
			t.Errorf("%s: added=%d repaired=%d, want %d and %d",
				tt.name, f.added, f.repaired, tt.added, tt.repaired)
		}
	}
}

// TestADeliberateChoiceIsReportedNotOverridden. Overriding a decision because it
// is unusual is how a tool becomes something people turn off.
func TestADeliberateChoiceIsReportedNotOverridden(t *testing.T) {
	_, f, err := fixString(
		`<html><head><meta name="viewport" content="width=1024"></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if f.repaired != 0 || total(f.skipped) != 1 {
		t.Errorf("repaired=%d skipped=%v", f.repaired, f.skipped)
	}

	// Unless asked.
	out, f, err := fixString(
		`<html><head><meta name="viewport" content="width=1024"></head><body>x</body></html>`,
		func(f *fixer) { f.force = true })
	if err != nil {
		t.Fatal(err)
	}
	if f.repaired != 1 || viewports(t, out)[0] != "width=device-width, initial-scale=1" {
		t.Errorf("-force did not replace it: %s", out)
	}
}

// TestWhatCountsAsBlockingZoom.
func TestWhatCountsAsBlockingZoom(t *testing.T) {
	f := defaults()
	for _, tt := range []struct {
		content string
		blocked bool
	}{
		{"user-scalable=no", true},
		{"user-scalable=NO", true},
		{"user-scalable=0", true},
		{"user-scalable=false", true},
		{"user-scalable=yes", false},
		{"user-scalable=1", false},
		{"maximum-scale=1", true},
		{"maximum-scale=1.0", true},
		{"maximum-scale=1.9", true},
		{"maximum-scale=2", false},
		{"maximum-scale=5", false},
		{"maximum-scale=notanumber", false},
		{"width=device-width", false},
		{"initial-scale=1", false},
		{"minimum-scale=1", false},
	} {
		ds := parseContent(tt.content)
		if len(ds) != 1 {
			t.Fatalf("%q parsed to %v", tt.content, ds)
		}
		if _, blocked := f.blocksZoom(ds[0]); blocked != tt.blocked {
			t.Errorf("blocksZoom(%q) = %v, want %v", tt.content, blocked, tt.blocked)
		}
	}
}

// TestTheThresholdIsConfigurable, because "useful zooming" is a policy and the
// WCAG figure of 200 per cent is a default rather than a law of nature.
func TestTheThresholdIsConfigurable(t *testing.T) {
	const doc = `<html><head><meta name="viewport" content="width=device-width, maximum-scale=3"></head><body>x</body></html>`

	_, f, err := fixString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if f.repaired != 0 {
		t.Errorf("maximum-scale=3 was removed at the default threshold")
	}

	_, f, err = fixString(doc, func(f *fixer) { f.minScale = 5 })
	if err != nil {
		t.Fatal(err)
	}
	if f.repaired != 1 {
		t.Errorf("maximum-scale=3 survived a threshold of 5")
	}
}

// TestParseAndFormatRoundTrip, since the declaration is rewritten rather than
// replaced when something in it is worth keeping.
func TestParseAndFormat(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"width=device-width, initial-scale=1", "width=device-width, initial-scale=1"},
		{"Width = device-width ,  initial-scale=1 ", "width=device-width, initial-scale=1"},
		{"width=device-width,,initial-scale=1", "width=device-width, initial-scale=1"},
		{"interactive-widget", "interactive-widget"},
		{"", ""},
	} {
		if got := formatContent(parseContent(tt.in)); got != tt.want {
			t.Errorf("round trip of %q gave %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestAnEmptyContentIsRepaired. A viewport meta with no content looks deliberate
// and does nothing.
func TestAnEmptyContentIsRepaired(t *testing.T) {
	for _, in := range []string{
		`<html><head><meta name="viewport" content=""></head><body>x</body></html>`,
		`<html><head><meta name="viewport" content="   "></head><body>x</body></html>`,
		`<html><head><meta name="viewport"></head><body>x</body></html>`,
	} {
		out, f, err := fixString(in)
		if err != nil {
			t.Fatal(err)
		}
		if f.repaired != 1 {
			t.Errorf("%q: repaired=%d", in, f.repaired)
		}
		if got := viewports(t, out); len(got) != 1 || got[0] != "width=device-width, initial-scale=1" {
			t.Errorf("%q -> %v", in, got)
		}
	}
}

// TestASecondViewportIsLeftAlone: a browser uses the first, so a later one is
// noise that reads as though it applies.
func TestASecondViewportIsLeftAlone(t *testing.T) {
	out, f, err := fixString(
		`<html><head><meta name="viewport" content="width=device-width">` +
			`<meta name="viewport" content="user-scalable=no"></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	got := viewports(t, out)
	if len(got) != 2 || got[1] != "user-scalable=no" {
		t.Errorf("viewports = %v, want the second untouched", got)
	}
	if total(f.skipped) == 0 {
		t.Error("the second viewport was not reported")
	}
}

// TestTheNameIsMatchedWithoutRegardToCase: name is not on the HTML list of
// attributes whose values are matched that way, so the selector cannot do it and
// the app has to.
func TestTheNameIsMatchedWithoutRegardToCase(t *testing.T) {
	out, f, err := fixString(
		`<html><head><meta name="VIEWPORT" content="user-scalable=no"></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	// The selector is name="viewport", which does not match VIEWPORT, so the
	// page gets a second viewport rather than a repair. That is the behaviour,
	// and it is pinned so that a fix is a deliberate change.
	if f.added != 1 || f.repaired != 0 {
		t.Errorf("added=%d repaired=%d: if this changed, the selector now matches "+
			"the value without regard to case", f.added, f.repaired)
	}
	// The document now holds two viewport metas, and the selector sees one of
	// them: name is not on the HTML list of attributes whose values are matched
	// without regard to case, so [name="viewport"] misses name="VIEWPORT". Both
	// counts are asserted, because the gap between them is the point.
	if n := len(viewports(t, out)); n != 1 {
		t.Errorf(`the selector [name="viewport"] found %d, want 1 - if this is now `+
			"2, attribute value matching became case-insensitive", n)
	}

	var metas int
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("meta[name]", func(*lolhtml.Element) error {
			metas++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if metas != 2 {
		t.Errorf("%d meta elements, want 2 - the upper-case one and the added one", metas)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*fixer)
	}{
		{"empty content", func(f *fixer) { f.content = "" }},
		{"a zero threshold", func(f *fixer) { f.minScale = 0 }},
		{"a negative threshold", func(f *fixer) { f.minScale = -1 }},
	} {
		if _, _, err := fixString(corpus[0], tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestWithNoHeadTheTagGoesBeforeBody.
func TestWithNoHeadTheTagGoesBeforeBody(t *testing.T) {
	out, f, err := fixString(`<html><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if f.added != 1 {
		t.Fatalf("added=%d", f.added)
	}
	i, j := strings.Index(out, "viewport"), strings.Index(out, "<body>")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the tag is not before the body: %s", out)
	}
}

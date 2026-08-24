package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func table(in *injector) {
	in.alts = []alternate{
		{"en-GB", "https://example.com/en/p"},
		{"fr", "https://example.com/fr/p"},
		{"de-AT", "https://example.com/de/p"},
	}
	in.self = "en-GB"
}

var corpus = []string{
	`<html><head><title>t</title></head><body>x</body></html>`,
	`<html><head><link rel="alternate" hreflang="FR" href="/old"></head><body>x</body></html>`,
	`<html><head><link rel="alternate" hreflang="fr" href="/a"><link rel="alternate" hreflang="fr" href="/b"></head><body>x</body></html>`,
	`<html><head><link rel="alternate stylesheet" hreflang="fr" href="/a"></head><body>x</body></html>`,
	`<html><head><link rel="alternate" href="/no-hreflang"></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<html><body><link rel="alternate" hreflang="fr" href="/in-body"></body></html>`,
	`<p>fragment</p>`,
	``,
}

// alternates asks the parser what the document declares, in document order.
func alternates(t *testing.T, doc string) [][2]string {
	t.Helper()
	var out [][2]string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`link[rel~="alternate"][hreflang]`, func(e *lolhtml.Element) error {
			out = append(out, [2]string{attr(e, "hreflang"), attr(e, "href")})
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(in string, n int, opts ...func(*injector)) (string, error) {
	j := defaults()
	for _, o := range opts {
		o(j)
	}
	if err := j.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, j.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := injectString(doc, table)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 43} {
			got, err := chunked(doc, n, table)
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
		once, _, err := injectString(doc, table)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, j, err := injectString(once, table)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if j.inserted != 0 || j.removed != 0 {
			t.Errorf("the second pass of %q inserted=%d removed=%d",
				doc, j.inserted, j.removed)
		}
	}
}

// TestOneAlternatePerLanguage: two alternates for one language contradict each
// other, so the table's languages must each appear exactly once.
func TestOneAlternatePerLanguage(t *testing.T) {
	for _, doc := range corpus {
		out, _, err := injectString(doc, table)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		count := map[string]int{}
		for _, a := range alternates(t, out) {
			count[strings.ToLower(a[0])]++
		}
		for tag, n := range count {
			if n > 1 && !strings.Contains(doc, "<body><link") {
				t.Errorf("%q -> %q declares %s %d times", doc, out, tag, n)
			}
		}
	}
}

// TestTheLinksComeOutInTableOrder. Several Before calls would reverse them - the
// newest insertion is the one closest to the unit - so they are built as one
// string and this pins that they are.
func TestTheLinksComeOutInTableOrder(t *testing.T) {
	out, j, err := injectString(`<html><head><title>t</title></head><body>x</body></html>`,
		table, func(in *injector) { in.xdefault = "en-GB" })
	if err != nil {
		t.Fatal(err)
	}
	if j.inserted != 4 {
		t.Fatalf("inserted=%d, want 4", j.inserted)
	}
	got := alternates(t, out)
	want := [][2]string{
		{"en-GB", "https://example.com/en/p"},
		{"fr", "https://example.com/fr/p"},
		{"de-AT", "https://example.com/de/p"},
		{"x-default", "https://example.com/en/p"},
	}
	if len(got) != len(want) {
		t.Fatalf("declares %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d is %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

// TestTheSelfTagComesFirst, because the page's own language is the one a reader
// of the source is looking for.
func TestTheSelfTagComesFirst(t *testing.T) {
	out, _, err := injectString(`<html><head></head><body>x</body></html>`, table,
		func(in *injector) { in.self = "fr" })
	if err != nil {
		t.Fatal(err)
	}
	got := alternates(t, out)
	if len(got) == 0 || got[0][0] != "fr" {
		t.Errorf("the first alternate is %v, want fr: %v", got[0], got)
	}
}

// TestAnExistingAlternateIsRewrittenWithoutRegardToCase: a language tag is
// case-insensitive, so hreflang="FR" is the fr row.
func TestAnExistingAlternateIsRewrittenWithoutRegardToCase(t *testing.T) {
	for _, existing := range []string{"FR", "fr", "Fr"} {
		out, j, err := injectString(
			`<html><head><link rel="alternate" hreflang="`+existing+`" href="/old"></head><body>x</body></html>`,
			table)
		if err != nil {
			t.Fatalf("%s: %v", existing, err)
		}
		if j.rewrote != 1 {
			t.Errorf("hreflang=%q: rewrote=%d, want 1", existing, j.rewrote)
		}
		if strings.Contains(out, "/old") {
			t.Errorf("hreflang=%q: the old href survived: %s", existing, out)
		}
		// And its position is kept, rather than the row being emitted again.
		if n := strings.Count(out, `hreflang="`+existing+`"`); n != 1 {
			t.Errorf("hreflang=%q appears %d times: %s", existing, n, out)
		}
	}
}

// TestARelTokenListStillCounts: rel is a token list, so "alternate stylesheet"
// is an alternate.
func TestARelTokenListStillCounts(t *testing.T) {
	_, j, err := injectString(
		`<html><head><link rel="alternate stylesheet" hreflang="fr" href="/a"></head><body>x</body></html>`,
		table)
	if err != nil {
		t.Fatal(err)
	}
	if j.rewrote != 1 {
		t.Errorf("rewrote=%d, want 1", j.rewrote)
	}
}

// TestAnAlternateWithNoHreflangIsNotOne, because rel=alternate is also how a feed
// and an alternate stylesheet are declared.
func TestAnAlternateWithNoHreflangIsNotOne(t *testing.T) {
	out, j, err := injectString(
		`<html><head><link rel="alternate" type="application/rss+xml" href="/feed"></head><body>x</body></html>`,
		table)
	if err != nil {
		t.Fatal(err)
	}
	if j.rewrote != 0 {
		t.Errorf("rewrote=%d, want 0", j.rewrote)
	}
	if !strings.Contains(out, `href="/feed"`) {
		t.Errorf("the feed link was changed: %s", out)
	}
}

// TestValidLanguageTag: the value goes into an attribute a crawler reads, so its
// shape is checked rather than trusted.
func TestValidLanguageTag(t *testing.T) {
	for _, good := range []string{
		"en", "EN", "en-GB", "zh-Hans", "zh-Hans-CN", "de-AT-1996", "x-default",
		"sr-Latn-RS", "es-419",
	} {
		if !validLanguageTag(good) {
			t.Errorf("validLanguageTag(%q) = false", good)
		}
	}
	for _, bad := range []string{
		"", "-", "en-", "-en", "en--GB", "en GB", "en_GB", "1en", "en.GB",
		`en" onload="x`, "en/GB", strings.Repeat("a", 36), "x-Default",
	} {
		if validLanguageTag(bad) {
			t.Errorf("validLanguageTag(%q) = true", bad)
		}
	}
}

// TestAConfigurationThatCannotWorkIsRefused: a bad table is worse than none,
// because every row of it reaches a crawler.
func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*injector)
	}{
		{"no alternates", func(in *injector) {}},
		{"a malformed tag", func(in *injector) {
			in.alts = []alternate{{"en GB", "https://x.example/p"}}
		}},
		{"a tag with a quote", func(in *injector) {
			in.alts = []alternate{{`en" onload="x`, "https://x.example/p"}}
		}},
		{"a relative href", func(in *injector) {
			in.alts = []alternate{{"en", "/p"}}
		}},
		{"a duplicated tag", func(in *injector) {
			in.alts = []alternate{{"en", "https://x.example/a"}, {"EN", "https://x.example/b"}}
		}},
		{"self not in the table", func(in *injector) {
			in.alts = []alternate{{"en", "https://x.example/p"}}
			in.self = "fr"
		}},
		{"x-default not in the table", func(in *injector) {
			in.alts = []alternate{{"en", "https://x.example/p"}}
			in.xdefault = "fr"
		}},
	} {
		if _, _, err := injectString(`<html><head></head><body>x</body></html>`, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestTheHrefIsEscaped: an & in a query is the ordinary case.
func TestTheHrefIsEscaped(t *testing.T) {
	out, _, err := injectString(`<html><head></head><body>x</body></html>`,
		func(in *injector) {
			in.alts = []alternate{{"en", "https://example.com/p?a=1&b=2"}}
		})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="https://example.com/p?a=1&amp;b=2"`) {
		t.Errorf("the href was not escaped: %s", out)
	}
	// And exactly one link, with one attribute set that a parser agrees with.
	if got := alternates(t, out); len(got) != 1 {
		t.Errorf("declares %v", got)
	}
}

// TestAFragmentIsReported rather than guessed at.
func TestAFragmentIsReported(t *testing.T) {
	for _, in := range []string{`<p>fragment</p>`, ``} {
		out, j, err := injectString(in, table)
		if err != nil {
			t.Fatal(err)
		}
		if j.inserted != 0 || out != in {
			t.Errorf("%q -> %q inserted=%d", in, out, j.inserted)
		}
		if total(j.skipped) != 1 {
			t.Errorf("%q: skipped=%v", in, j.skipped)
		}
	}
}

// TestASecondAlternateForOneLanguageIsRemoved. Noting it and moving on would
// leave the document declaring one language twice, which is the contradiction
// this program exists to remove.
func TestASecondAlternateForOneLanguageIsRemoved(t *testing.T) {
	out, j, err := injectString(
		`<html><head><link rel="alternate" hreflang="fr" href="/a">`+
			`<link rel="alternate" hreflang="FR" href="/b"></head><body>x</body></html>`,
		table)
	if err != nil {
		t.Fatal(err)
	}
	if j.rewrote != 1 || j.removed != 1 {
		t.Errorf("rewrote=%d removed=%d, want 1 and 1", j.rewrote, j.removed)
	}
	n := 0
	for _, a := range alternates(t, out) {
		if strings.EqualFold(a[0], "fr") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("declares fr %d times: %s", n, out)
	}
	// Asked of the parser, not of the string: "</body>" contains "/b", which is
	// how the first version of this assertion failed on correct output.
	for _, a := range alternates(t, out) {
		if a[1] == "/b" {
			t.Errorf("the duplicate survived: %s", out)
		}
	}
}

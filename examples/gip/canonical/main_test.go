package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const want = "https://example.com/page"

func target(e *enforcer) { e.href = want }

var corpus = []string{
	`<html><head><link rel="canonical" href="/old"><title>t</title></head><body>x</body></html>`,
	`<html><head><link rel="CANONICAL" href="/a"><link rel="canonical" href="/b"></head><body>x</body></html>`,
	`<html><head><link rel="alternate canonical" href="/b"></head><body>x</body></html>`,
	`<html><head><title>t</title></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<html><body>x<link rel="canonical" href="/in-body"></body></html>`,
	`<html><head><link rel="canonical" href="/a"></head><body><link rel="canonical" href="/b"></body></html>`,
	`<html><head><link rel="stylesheet" href="/s.css"></head><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// canonicalLinks asks the parser what the document ends up declaring.
func canonicalLinks(t *testing.T, doc string) []string {
	t.Helper()
	var hrefs []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`link[rel~="canonical"]`, func(e *lolhtml.Element) error {
			hrefs = append(hrefs, attr(e, "href"))
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return hrefs
}

func chunked(in string, n int, opts ...func(*enforcer)) (string, error) {
	e := defaults()
	for _, o := range opts {
		o(e)
	}
	if err := e.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, e.options()...)
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
		whole, _, err := enforceString(doc, target)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 41} {
			got, err := chunked(doc, n, target)
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
		once, _, err := enforceString(doc, target)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, e, err := enforceString(once, target)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if e.removed != 0 || e.inserted != 0 || e.droppedOutside != 0 {
			t.Errorf("the second pass of %q removed=%d inserted=%d dropped=%d",
				doc, e.removed, e.inserted, e.droppedOutside)
		}
	}
}

// TestExactlyOneCanonicalLink is the whole promise, checked with the parser
// rather than by counting substrings.
func TestExactlyOneCanonicalLink(t *testing.T) {
	for _, doc := range corpus {
		out, _, err := enforceString(doc, target)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		links := canonicalLinks(t, out)
		switch {
		case len(links) == 1 && links[0] == want:
			// The usual outcome.
		case len(links) == 0 && !strings.Contains(doc, "<body") && !strings.Contains(doc, "<head"):
			// A fragment with nowhere to put one, reported rather than guessed.
		default:
			t.Errorf("%q -> %q declares %v", doc, out, links)
		}
	}
}

// TestARelTokenListStillCounts: rel is a token list, so a link declaring itself
// both alternate and canonical is a canonical link. [rel=v] compares the whole
// value and would miss it; [rel~=v] is the selector for one token.
func TestARelTokenListStillCounts(t *testing.T) {
	for _, in := range []string{
		`<html><head><link rel="alternate canonical" href="/a"></head><body>x</body></html>`,
		`<html><head><link rel="canonical alternate" href="/a"></head><body>x</body></html>`,
		`<html><head><link rel="  canonical  " href="/a"></head><body>x</body></html>`,
	} {
		out, e, err := enforceString(in, target)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if e.rewrote != 1 || e.inserted != 0 {
			t.Errorf("%q: rewrote=%d inserted=%d, want 1 and 0", in, e.rewrote, e.inserted)
		}
		if links := canonicalLinks(t, out); len(links) != 1 || links[0] != want {
			t.Errorf("%q -> %q declares %v", in, out, links)
		}
	}

	// And a rel that merely contains the word is not one.
	for _, in := range []string{
		`<html><head><link rel="canonical-ish" href="/a"></head><body>x</body></html>`,
		`<html><head><link rel="noncanonical" href="/a"></head><body>x</body></html>`,
	} {
		_, e, err := enforceString(in, target)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if e.rewrote != 0 || e.inserted != 1 {
			t.Errorf("%q: rewrote=%d inserted=%d, want 0 and 1", in, e.rewrote, e.inserted)
		}
	}
}

// TestTheRelValueIsMatchedWithoutRegardToCase, which is not the general rule for
// attribute values - rel is on the HTML specification's list. Pinned here because
// this program relies on it rather than lower-casing the value itself.
func TestTheRelValueIsMatchedWithoutRegardToCase(t *testing.T) {
	for _, in := range []string{
		`<html><head><link rel="CANONICAL" href="/a"></head><body>x</body></html>`,
		`<html><head><link rel="Canonical" href="/a"></head><body>x</body></html>`,
		`<html><head><link REL="canonical" href="/a"></head><body>x</body></html>`,
		`<html><head><link rel="ALTERNATE CANONICAL" href="/a"></head><body>x</body></html>`,
	} {
		_, e, err := enforceString(in, target)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if e.rewrote != 1 {
			t.Errorf("%q: rewrote=%d, want 1 - the rel value should match "+
				"case-insensitively", in, e.rewrote)
		}
	}
}

// TestACanonicalLinkOutsideTheHeadIsRemoved. It is not honoured there, so it is
// not a competing declaration - but leaving it would mean the document still had
// two, which is the thing being fixed. It also settles an ordering problem: the
// insertion is decided at the end of the head, and a link in the body arrives
// after that.
func TestACanonicalLinkOutsideTheHeadIsRemoved(t *testing.T) {
	for _, tt := range []struct {
		in                         string
		inserted, rewrote, dropped int
	}{
		{`<html><body>x<link rel="canonical" href="/b"></body></html>`, 1, 0, 1},
		{`<html><head></head><body><link rel="canonical" href="/b"></body></html>`, 1, 0, 1},
		{`<html><head><link rel="canonical" href="/a"></head><body><link rel="canonical" href="/b"></body></html>`, 0, 1, 1},
		{`<html><body><link rel="canonical" href="/b"><link rel="canonical" href="/c"></body></html>`, 1, 0, 2},
	} {
		out, e, err := enforceString(tt.in, target)
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if e.inserted != tt.inserted || e.rewrote != tt.rewrote || e.droppedOutside != tt.dropped {
			t.Errorf("%q: inserted=%d rewrote=%d dropped=%d, want %d %d %d",
				tt.in, e.inserted, e.rewrote, e.droppedOutside,
				tt.inserted, tt.rewrote, tt.dropped)
		}
		if links := canonicalLinks(t, out); len(links) != 1 || links[0] != want {
			t.Errorf("%q -> %q declares %v", tt.in, out, links)
		}
	}
}

// TestKeepLeavesTheFirstHrefAlone, which is the deduplicating mode: the page's
// own canonical is correct and there is simply more than one of it.
func TestKeepLeavesTheFirstHrefAlone(t *testing.T) {
	out, e, err := enforceString(
		`<html><head><link rel="canonical" href="/a"><link rel="canonical" href="/b"></head><body>x</body></html>`,
		func(e *enforcer) { e.keep = true })
	if err != nil {
		t.Fatal(err)
	}
	if e.rewrote != 0 || e.removed != 1 {
		t.Errorf("rewrote=%d removed=%d, want 0 and 1", e.rewrote, e.removed)
	}
	if links := canonicalLinks(t, out); len(links) != 1 || links[0] != "/a" {
		t.Errorf("%q declares %v, want just /a", out, links)
	}
}

// TestNoAddOnlyDeduplicates.
func TestNoAddOnlyDeduplicates(t *testing.T) {
	out, e, err := enforceString(`<html><head><title>t</title></head><body>x</body></html>`,
		target, func(e *enforcer) { e.noAdd = true })
	if err != nil {
		t.Fatal(err)
	}
	if e.inserted != 0 || total(e.skipped) != 1 {
		t.Errorf("inserted=%d skipped=%v", e.inserted, e.skipped)
	}
	if strings.Contains(out, "canonical") {
		t.Errorf("-no-add inserted one anyway: %s", out)
	}
}

// TestWithNoHeadElementTheLinkGoesBeforeBody, which differential/head_test.go
// confirms lands in the head a parser builds.
func TestWithNoHeadElementTheLinkGoesBeforeBody(t *testing.T) {
	out, e, err := enforceString(`<html><body>x</body></html>`, target)
	if err != nil {
		t.Fatal(err)
	}
	if e.inserted != 1 {
		t.Fatalf("inserted=%d, want 1", e.inserted)
	}
	if out != `<html><link rel="canonical" href="`+want+`"><body>x</body></html>` {
		t.Errorf("unexpected placement: %s", out)
	}
}

// TestAFragmentIsReportedNotGuessedAt.
func TestAFragmentIsReportedNotGuessedAt(t *testing.T) {
	for _, in := range []string{`<p>fragment</p>`, ``, `text only`} {
		out, e, err := enforceString(in, target)
		if err != nil {
			t.Fatal(err)
		}
		if e.inserted != 0 || out != in {
			t.Errorf("%q -> %q inserted=%d", in, out, e.inserted)
		}
		if total(e.skipped) != 1 {
			t.Errorf("%q: skipped=%v, want one reason", in, e.skipped)
		}
	}
}

// TestTheHrefIsEscaped: the link is assembled as markup, so the url goes through
// EscapeAttribute. An & in a query is the ordinary case, not an exotic one.
func TestTheHrefIsEscaped(t *testing.T) {
	out, _, err := enforceString(`<html><head></head><body>x</body></html>`,
		func(e *enforcer) { e.href = "https://example.com/p?a=1&b=2" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="https://example.com/p?a=1&amp;b=2"`) {
		t.Errorf("the href was not escaped: %s", out)
	}
	// And it reads back as the url that went in.
	links := canonicalLinks(t, out)
	if len(links) != 1 || links[0] != "https://example.com/p?a=1&amp;b=2" {
		t.Errorf("declares %v", links)
	}
}

// TestARelativeHrefIsRefused: a canonical link has to be absolute, or a crawler
// resolves it against whichever page it found it on.
func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*enforcer)
	}{
		{"no href and not keep", func(e *enforcer) {}},
		{"a relative href", func(e *enforcer) { e.href = "/page" }},
		{"a scheme-relative href", func(e *enforcer) { e.href = "//example.com/page" }},
		{"a bare host", func(e *enforcer) { e.href = "example.com/page" }},
		{"an unparseable href", func(e *enforcer) { e.href = "http://[::1" }},
	} {
		if _, _, err := enforceString(`<html><head></head><body>x</body></html>`, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestOtherLinksAreNotTouched.
func TestOtherLinksAreNotTouched(t *testing.T) {
	const in = `<html><head><link rel="stylesheet" href="/s.css">` +
		`<link rel="alternate" href="/feed" type="application/rss+xml">` +
		`<link rel="preload" href="/f.woff2"></head><body>x</body></html>`
	out, e, err := enforceString(in, target)
	if err != nil {
		t.Fatal(err)
	}
	if e.rewrote != 0 || e.removed != 0 || e.inserted != 1 {
		t.Errorf("rewrote=%d removed=%d inserted=%d", e.rewrote, e.removed, e.inserted)
	}
	for _, keep := range []string{"/s.css", "/feed", "/f.woff2"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%s was lost: %s", keep, out)
		}
	}
}

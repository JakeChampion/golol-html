package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = `<html><head><title>t</title></head><body>` +
	`<script src="https://cdn.example/a.js"></script>` +
	`<img src="/local.png">` +
	`<img srcset="https://img.example/a.png 1x, https://img.example/b.png 2x">` +
	`<iframe src="//frames.example/f"></iframe>` +
	`<a href="https://links.example/x">l</a>` +
	`</body></html>`

var corpus = []string{
	page,
	`<html><head></head><body><script src="https://a.example/1"></script></body></html>`,
	`<html><head><link rel="preconnect" href="https://a.example"></head><body><script src="https://a.example/1"></script></body></html>`,
	`<html><head></head><body><script src="https://example.com/own.js"></script></body></html>`,
	`<html><head></head><body><img src="data:image/png;base64,AA"></body></html>`,
	`<html><head></head><body><script src="not a url at all"></script></body></html>`,
	`<html><body><script src="https://a.example/1"></script></body></html>`,
	`<p>fragment</p>`,
	``,
}

// hints returns the rel and href of every resource hint a parser finds.
func hints(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`link[rel~="preconnect"], link[rel~="dns-prefetch"]`,
			func(e *lolhtml.Element) error {
				out = append(out, attr(e, "rel")+" "+attr(e, "href"))
				return nil
			})); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeChunked(t *testing.T, doc string, size int, opts ...func(*hinter)) string {
	t.Helper()
	h := defaults()
	h.self = "https://example.com/p"
	for _, o := range opts {
		o(h)
	}
	if err := h.validate(); err != nil {
		t.Fatal(err)
	}
	if err := h.readExisting([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := h.readPass([]byte(doc)); err != nil {
		t.Fatal(err)
	}

	markup := h.markup()
	sawHead, placed := false, markup == ""
	var out strings.Builder
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

func TestTheWritePassIsChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		whole := writeChunked(t, doc, len(doc)+1)
		for _, size := range []int{1, 2, 3, 37} {
			if got := writeChunked(t, doc, size); got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					size, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := hintString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, h, err := hintString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if h.added != 0 {
			t.Errorf("the second pass of %q added %d links", doc, h.added)
		}
	}
}

// TestWhichAttributesCarryAFetch. A link's href is not a fetch and a script's src
// is, which is why the attributes are listed per element rather than guessed at.
func TestWhichAttributesCarryAFetch(t *testing.T) {
	for _, tt := range []struct {
		markup string
		want   bool
	}{
		{`<script src="https://a.example/x"></script>`, true},
		{`<link href="https://a.example/x">`, true},
		{`<img src="https://a.example/x">`, true},
		{`<iframe src="https://a.example/x"></iframe>`, true},
		{`<video src="https://a.example/x"></video>`, true},
		{`<video poster="https://a.example/x"></video>`, true},
		{`<object data="https://a.example/x"></object>`, true},
		{`<track src="https://a.example/x">`, true},

		// Not fetches.
		{`<a href="https://a.example/x">l</a>`, false},
		{`<form action="https://a.example/x"></form>`, false},
		{`<div data-src="https://a.example/x"></div>`, false},
		{`<blockquote cite="https://a.example/x"></blockquote>`, false},
	} {
		doc := `<html><head></head><body>` + tt.markup + `</body></html>`
		_, h, err := hintString(doc)
		if err != nil {
			t.Fatalf("%s: %v", tt.markup, err)
		}
		got := len(h.origins) == 1
		if got != tt.want {
			t.Errorf("%s: collected=%v, want %v", tt.markup, got, tt.want)
		}
	}
}

// TestASrcsetCarriesSeveralURLs.
func TestASrcsetCarriesSeveralURLs(t *testing.T) {
	_, h, err := hintString(`<html><head></head><body>` +
		`<img srcset="https://a.example/1.png 1x, https://b.example/2.png 2x, /local.png 3x">` +
		`</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.origins, " ") != "https://a.example https://b.example" {
		t.Errorf("origins = %v", h.origins)
	}
}

func TestSplitURLs(t *testing.T) {
	for _, tt := range []struct {
		attribute, value string
		want             []string
	}{
		{"src", " https://a.example/x ", []string{"https://a.example/x"}},
		{"srcset", "a.png 1x, b.png 2x", []string{"a.png", "b.png"}},
		{"srcset", "a.png", []string{"a.png"}},
		{"srcset", "  a.png   1x  ,  b.png  ", []string{"a.png", "b.png"}},
		{"srcset", "", nil},
		{"srcset", ",,", nil},
	} {
		got := splitURLs(tt.attribute, tt.value)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("splitURLs(%q, %q) = %v, want %v",
				tt.attribute, tt.value, got, tt.want)
		}
	}
}

// TestTheOwnOriginIsNotThirdParty, which is what -self is for.
func TestTheOwnOriginIsNotThirdParty(t *testing.T) {
	_, h, err := hintString(`<html><head></head><body>` +
		`<script src="https://example.com/own.js"></script>` +
		`<script src="https://EXAMPLE.COM/also-own.js"></script>` +
		`<script src="/relative.js"></script>` +
		`<script src="https://other.example/x.js"></script>` +
		`</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.origins, " ") != "https://other.example" {
		t.Errorf("origins = %v", h.origins)
	}
}

// TestASchemeRelativeURLInheritsThePageScheme.
func TestASchemeRelativeURLInheritsThePageScheme(t *testing.T) {
	for _, self := range []string{"https://example.com/p", "http://example.com/p"} {
		_, h, err := hintString(
			`<html><head></head><body><iframe src="//frames.example/f"></iframe></body></html>`,
			func(h *hinter) { h.self = self })
		if err != nil {
			t.Fatal(err)
		}
		want := strings.SplitN(self, ":", 2)[0] + "://frames.example"
		if len(h.origins) != 1 || h.origins[0] != want {
			t.Errorf("self=%s: origins = %v, want %q", self, h.origins, want)
		}
	}
}

// TestNonHTTPOriginsAreIgnored: a data: or mailto: URL has no connection to open.
func TestNonHTTPOriginsAreIgnored(t *testing.T) {
	_, h, err := hintString(`<html><head></head><body>` +
		`<img src="data:image/png;base64,AA">` +
		`<a href="mailto:x@example.com">m</a>` +
		`<script src="ftp://files.example/x.js"></script>` +
		`</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.origins) != 0 {
		t.Errorf("origins = %v", h.origins)
	}
}

// TestBothRelValuesAreEmitted, and why: a browser without preconnect support
// still benefits from dns-prefetch.
func TestBothRelValuesAreEmitted(t *testing.T) {
	out, _, err := hintString(
		`<html><head></head><body><script src="https://a.example/1"></script></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	got := hints(t, out)
	if len(got) != 2 {
		t.Fatalf("hints = %v, want two", got)
	}
	if got[0] != "preconnect https://a.example" || got[1] != "dns-prefetch https://a.example" {
		t.Errorf("hints = %v, want preconnect then dns-prefetch", got)
	}

	// -dns-only drops the connection-holding half.
	out, _, err = hintString(
		`<html><head></head><body><script src="https://a.example/1"></script></body></html>`,
		func(h *hinter) { h.dnsOnly = true })
	if err != nil {
		t.Fatal(err)
	}
	got = hints(t, out)
	if len(got) != 1 || !strings.HasPrefix(got[0], "dns-prefetch") {
		t.Errorf("hints = %v, want dns-prefetch only", got)
	}
}

// TestAnOriginTheHeadAlreadyHintsIsNotHintedAgain.
func TestAnOriginTheHeadAlreadyHintsIsNotHintedAgain(t *testing.T) {
	out, h, err := hintString(
		`<html><head><link rel="preconnect" href="https://a.example"></head><body>` +
			`<script src="https://a.example/1"></script>` +
			`<script src="https://b.example/1"></script></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.origins, " ") != "https://b.example" {
		t.Errorf("origins = %v, want only the un-hinted one", h.origins)
	}
	if n := strings.Count(out, "https://a.example"); n != 2 {
		t.Errorf("%d mentions of the already-hinted origin, want 2 - its own hint "+
			"and its script: %s", n, out)
	}
}

// TestTheCapIsReportedNotSilent. A preconnect holds a connection open, so forty
// of them is not a hint, it is a problem - and dropping thirty-six silently would
// hide it.
func TestTheCapIsReportedNotSilent(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<html><head></head><body>`)
	for _, host := range []string{"a", "b", "c", "d", "e", "f"} {
		sb.WriteString(`<script src="https://` + host + `.example/1"></script>`)
	}
	sb.WriteString(`</body></html>`)

	out, h, err := hintString(sb.String(), func(h *hinter) { h.max = 2 })
	if err != nil {
		t.Fatal(err)
	}
	if len(hints(t, out)) != 4 {
		t.Errorf("%d hints, want 4 - two origins, two rel values", len(hints(t, out)))
	}
	if total(h.skipped) != 1 {
		t.Errorf("skipped = %v, want one note about the cap", h.skipped)
	}
	// And the note is counted once, not once per caller of hinted().
	for reason, n := range h.skipped {
		if n != 1 {
			t.Errorf("%q counted %d times", reason, n)
		}
	}
}

// TestFirstAppearanceOrder: the first origin a page reaches is the one whose
// connection is wanted soonest.
func TestFirstAppearanceOrder(t *testing.T) {
	_, h, err := hintString(`<html><head></head><body>` +
		`<script src="https://third.example/1"></script>` +
		`<script src="https://first.example/1"></script>` +
		`<script src="https://third.example/2"></script>` +
		`</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.origins, " ") != "https://third.example https://first.example" {
		t.Errorf("origins = %v, want document order with duplicates collapsed", h.origins)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*hinter)
	}{
		{"no self", func(h *hinter) { h.self = "" }},
		{"a relative self", func(h *hinter) { h.self = "/p" }},
		{"an unparseable self", func(h *hinter) { h.self = "http://[::1" }},
		{"a zero cap", func(h *hinter) { h.max = 0 }},
	} {
		if _, _, err := hintString(page, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestTheHrefIsEscaped: the links are assembled as markup.
func TestTheHrefIsEscaped(t *testing.T) {
	// A host cannot contain an ampersand, so the escaping is exercised through a
	// self origin with one - which url.Parse accepts in the host for a
	// registry-name.
	out, _, err := hintString(
		`<html><head></head><body><script src="https://a&b.example/x"></script></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `href="https://a&b.example"`) {
		t.Errorf("the ampersand was not escaped: %s", out)
	}
}

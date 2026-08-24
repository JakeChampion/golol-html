package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func gated(g *gate) {
	g.hosts = []string{"third.party"}
	g.selectors = []string{"script#ga-init"}
}

var corpus = []string{
	`<script src="/first-party.js"></script>`,
	`<script src="https://third.party/a.js" async defer></script>`,
	`<script src="https://cdn.third.party/b.js" type="module"></script>`,
	`<script src="https://other.example/c.js"></script>`,
	`<script type="importmap">{"imports":{}}</script>`,
	`<script type="application/ld+json">{"@type":"Thing"}</script>`,
	`<script id="ga-init">window.dataLayer=[];</script>`,
	`<script>ordinary()</script>`,
	`<script src="https://third.party/a.js"></script><script src="https://third.party/b.js"></script>`,
	`<script/>`,
	`<script src=https://third.party/unquoted.js></script>`,
	`<script SRC="HTTPS://THIRD.PARTY/upper.js"></script>`,
	`<p>no scripts</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*gate)) (string, error) {
	g := defaults()
	for _, o := range opts {
		o(g)
	}
	if err := g.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, g.options()...)
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
		whole, _, err := gateString(doc, gated)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 23} {
			got, err := chunked(doc, n, gated)
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
		once, _, err := gateString(doc, gated)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, g, err := gateString(once, gated)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if g.gated != 0 {
			t.Errorf("the second pass of %q gated %d", doc, g.gated)
		}
	}
}

// TestTheRequestIsGatedAndNotJustTheExecution is the point. A browser fetches a
// script's src even for a type it will not execute, so leaving src in place
// would gate the running and not the request.
func TestTheRequestIsGatedAndNotJustTheExecution(t *testing.T) {
	got, g, err := gateString(
		`<script src="https://third.party/a.js" async defer crossorigin="anonymous"></script>`,
		gated)
	if err != nil {
		t.Fatal(err)
	}
	if g.gated != 1 {
		t.Fatalf("gated=%d, want 1", g.gated)
	}

	var attrs map[string]string
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			attrs = map[string]string{}
			for name, value := range e.Attributes() {
				attrs[name] = value
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if _, ok := attrs["src"]; ok {
		t.Errorf("src survived, so the script is still fetched: %s", got)
	}
	if attrs["data-consent-src"] != "https://third.party/a.js" {
		t.Errorf("the src was not saved for restoring: %v", attrs)
	}
	if attrs["type"] != "text/plain" {
		t.Errorf("type is %q, want text/plain", attrs["type"])
	}
	// Everything else the tag carried has to survive, or restoring gives a
	// different script from the one the page asked for.
	for _, name := range []string{"async", "defer", "crossorigin"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("%s was lost: %v", name, attrs)
		}
	}
}

// TestTheOriginalTypeIsRecordedEvenWhenEmpty: absent and empty restore
// differently, so which one it was has to be written down.
func TestTheOriginalTypeIsRecordedEvenWhenEmpty(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`<script src="https://third.party/a.js"></script>`, ""},
		{`<script src="https://third.party/a.js" type=""></script>`, ""},
		{`<script src="https://third.party/a.js" type="module"></script>`, "module"},
		{`<script src="https://third.party/a.js" type="text/javascript"></script>`, "text/javascript"},
	} {
		got, _, err := gateString(tt.in, gated)
		if err != nil {
			t.Fatal(err)
		}
		var saved string
		var present bool
		if _, err := lolhtml.RewriteString(got,
			lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				saved, present = e.Attribute("data-consent-type")
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if !present {
			t.Errorf("%s: the original type was not recorded", tt.in)
		} else if saved != tt.want {
			t.Errorf("%s: recorded type %q, want %q", tt.in, saved, tt.want)
		}
	}
}

// TestDataTypesAreNeverGated: an importmap or a JSON-LD block is read by the
// browser rather than run, so parking it under another type changes what the
// document means.
func TestDataTypesAreNeverGated(t *testing.T) {
	for _, typ := range []string{
		"importmap", "application/json", "application/ld+json", "speculationrules",
		"IMPORTMAP", " application/ld+json ",
	} {
		in := `<script id="ga-init" type="` + typ + `">{}</script>`
		got, g, err := gateString(in, gated)
		if err != nil {
			t.Fatal(err)
		}
		if g.gated != 0 {
			t.Errorf("type %q was gated: %s", typ, got)
		}
		if got != in {
			t.Errorf("type %q: the document changed: %s", typ, got)
		}
	}
}

// TestOnlyTheNamedHostsAreGated: there is no built-in tracker list, because a
// host allowed by one site is a tracker on the next.
func TestOnlyTheNamedHostsAreGated(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want bool
	}{
		{"https://third.party/a.js", true},
		{"https://cdn.third.party/a.js", true},
		{"https://a.b.third.party/a.js", true},
		{"//third.party/a.js", true},
		{"HTTPS://THIRD.PARTY/a.js", true},
		{"https://third.party./a.js", true},

		{"/local.js", false},
		{"a.js", false},
		{"https://other.example/a.js", false},
		{"https://notthird.party/a.js", false},
		{"https://third.party.evil.example/a.js", false},
		{"https://third-party/a.js", false},
		{"", false},
	} {
		g := defaults()
		g.hosts = []string{"third.party"}
		if got := g.gatedHost(tt.src); got != tt.want {
			t.Errorf("gatedHost(%q) = %v, want %v", tt.src, got, tt.want)
		}
	}
}

// TestNothingIsGatedWithoutConfiguration: the default is to do nothing, because
// guessing which third parties a site has consented to is not this program's
// call.
func TestNothingIsGatedWithoutConfiguration(t *testing.T) {
	for _, doc := range corpus {
		got, g, err := gateString(doc)
		if err != nil {
			t.Fatal(err)
		}
		if g.gated != 0 {
			t.Errorf("%q gated %d with no hosts configured", doc, g.gated)
		}
		if got != doc {
			t.Errorf("%q changed with no hosts configured: %s", doc, got)
		}
	}
}

// TestAnInlineScriptIsGatedBySelector, which is what the streaming model allows:
// a selector is decided on the start tag, where the type attribute is.
func TestAnInlineScriptIsGatedBySelector(t *testing.T) {
	got, g, err := gateString(
		`<script id="ga-init">window.dataLayer=[];</script><script>other()</script>`, gated)
	if err != nil {
		t.Fatal(err)
	}
	if g.gated != 1 {
		t.Fatalf("gated=%d, want 1: %s", g.gated, got)
	}
	if !strings.Contains(got, `<script id="ga-init" data-consent-type="" type="text/plain">window.dataLayer=[];</script>`) {
		t.Errorf("the inline script was not parked as expected: %s", got)
	}
	if !strings.Contains(got, `<script>other()</script>`) {
		t.Errorf("an unnamed inline script was touched: %s", got)
	}
	// No data-consent-src: there was no src to move, and inventing an empty one
	// would make the restoring script fetch the page itself.
	if strings.Contains(got, `data-consent-src`) {
		t.Errorf("an inline script was given a saved src: %s", got)
	}
}

// TestASelectorAndAHostGateOnce: both handlers fire for the same element, and
// handlers see each other's edits, so the second must find it already gated.
func TestASelectorAndAHostGateOnce(t *testing.T) {
	got, g, err := gateString(`<script id="ga-init" src="https://third.party/a.js"></script>`,
		gated)
	if err != nil {
		t.Fatal(err)
	}
	if g.gated != 1 {
		t.Errorf("gated=%d, want 1: %s", g.gated, got)
	}
	if n := strings.Count(got, "data-consent-type"); n != 1 {
		t.Errorf("%d saved types: %s", n, got)
	}
}

// TestASelectorThatMatchesSomethingElseIsReported rather than obeyed: rewriting
// a <div>'s type attribute would be nonsense.
func TestASelectorThatMatchesSomethingElseIsReported(t *testing.T) {
	got, g, err := gateString(`<div id="ga-init">x</div>`, func(g *gate) {
		g.selectors = []string{"#ga-init"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.gated != 0 || total(g.skipped) != 1 {
		t.Errorf("gated=%d skipped=%v", g.gated, g.skipped)
	}
	if got != `<div id="ga-init">x</div>` {
		t.Errorf("the div was changed: %s", got)
	}
}

// TestAConfigurationThatCannotWorkIsRefused: a document that looks gated and is
// not is worse than an error.
func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*gate)
	}{
		{"empty type", func(g *gate) { g.gatedType = "" }},
		{"executable type", func(g *gate) { g.gatedType = "text/javascript" }},
		{"executable type in caps", func(g *gate) { g.gatedType = "MODULE" }},
		{"application/javascript", func(g *gate) { g.gatedType = "application/javascript" }},
		{"empty prefix", func(g *gate) { g.prefix = "" }},
		{"prefix with a quote", func(g *gate) { g.prefix = `x" onload="` }},
		{"prefix with a space", func(g *gate) { g.prefix = "a b" }},
	} {
		if _, _, err := gateString(`<script src="https://third.party/a.js"></script>`,
			gated, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

func TestValidAttrPrefix(t *testing.T) {
	for _, good := range []string{"data-consent", "x", "a-b-c", "d1"} {
		if !validAttrPrefix(good) {
			t.Errorf("validAttrPrefix(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "A", "a b", `a"b`, "a=b", "a>b", "a_b", "a.b"} {
		if validAttrPrefix(bad) {
			t.Errorf("validAttrPrefix(%q) = true", bad)
		}
	}
}

// TestTheReportIsStable so it can be diffed between runs.
func TestTheReportIsStable(t *testing.T) {
	_, g, err := gateString(strings.Join(corpus, ""), gated)
	if err != nil {
		t.Fatal(err)
	}
	first := g.report()
	if second := g.report(); first != second {
		t.Errorf("the report is not stable: %q then %q", first, second)
	}
	if !strings.HasPrefix(first, "gated=") {
		t.Errorf("report is %q", first)
	}
}

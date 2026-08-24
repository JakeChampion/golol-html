package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = `<html><head>` +
	`<script src="/a.js"></script>` +
	`<script src="/b.js" async></script>` +
	`<script type="module" src="/c.js"></script>` +
	`<script>inline()</script>` +
	`<script type="importmap" src="/m.json"></script>` +
	`<script src="/d.js" defer></script>` +
	`</head><body><script src="/e.js"></script></body></html>`

var corpus = []string{
	page,
	`<html><head><script src="/a.js"></script></head><body>x</body></html>`,
	`<html><head><script src=""></script></head><body>x</body></html>`,
	`<html><head><script src="/a.js" type="text/javascript"></script></head><body>x</body></html>`,
	`<html><head><script src="/a.js" type="text/template"></script></head><body>x</body></html>`,
	`<html><body><script src="/a.js"></script></body></html>`,
	`<p>fragment</p>`,
	``,
}

// scripts returns each script's src and whether it carries defer.
func scripts(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			src, _ := e.Attribute("src")
			_, deferred := e.Attribute("defer")
			out = append(out, src+":"+map[bool]string{true: "defer", false: "-"}[deferred])
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(in string, n int, opts ...func(*deferrer)) (string, *deferrer, error) {
	d := defaults()
	for _, o := range opts {
		o(d)
	}
	if err := d.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, d.options()...)
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
	return out.String(), d, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, in := range corpus {
		whole, _, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, n := range []int{1, 2, 3, 23} {
			got, _, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, in, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range corpus {
		once, _, err := deferString(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		twice, d, err := deferString(once)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", in, once, twice)
		}
		if d.deferred != 0 {
			t.Errorf("the second pass of %q deferred %d", in, d.deferred)
		}
	}
}

// TestOnlyTheScriptThatCanTakeDeferGetsIt, and every exclusion is reported so a
// caller can see what was left and why.
func TestOnlyTheScriptThatCanTakeDeferGetsIt(t *testing.T) {
	out, d, err := deferString(page)
	if err != nil {
		t.Fatal(err)
	}
	if d.deferred != 1 {
		t.Errorf("deferred=%d, want 1", d.deferred)
	}
	want := []string{
		"/a.js:defer", // a classic script in the head
		"/b.js:-",     // async
		"/c.js:-",     // module
		":-",          // inline
		"/m.json:-",   // importmap
		"/d.js:defer", // already deferred
		"/e.js:-",     // in the body
	}
	got := scripts(t, out)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	// Five exclusions, each with its own reason.
	if len(d.skipped) != 5 {
		t.Errorf("skipped = %v, want five distinct reasons", d.skipped)
	}
}

// TestWhatIsLeftAlone, one at a time, with the reason each would break.
func TestWhatIsLeftAlone(t *testing.T) {
	for _, tt := range []struct {
		name, markup string
		want         bool
	}{
		{"a classic script", `<script src="/a.js"></script>`, true},
		{"an explicit javascript type", `<script src="/a.js" type="text/javascript"></script>`, true},
		{"an uppercase type", `<script src="/a.js" type="TEXT/JAVASCRIPT"></script>`, true},

		{"inline", `<script>x()</script>`, false},
		{"an empty src", `<script src=""></script>`, false},
		{"a whitespace src", `<script src="   "></script>`, false},
		{"async", `<script src="/a.js" async></script>`, false},
		{"a module", `<script type="module" src="/a.js"></script>`, false},
		{"an importmap", `<script type="importmap" src="/a.js"></script>`, false},
		{"a template", `<script type="text/template" src="/a.js"></script>`, false},
		{"already deferred", `<script src="/a.js" defer></script>`, false},
	} {
		doc := `<html><head>` + tt.markup + `</head><body>x</body></html>`
		_, d, err := deferString(doc)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := d.deferred == 1; got != tt.want {
			t.Errorf("%s: deferred=%v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestABodyScriptNeedsAsking. A script placed late in the body was often placed
// there deliberately - the pattern predates defer.
func TestABodyScriptNeedsAsking(t *testing.T) {
	const doc = `<html><head></head><body><p>x</p><script src="/a.js"></script></body></html>`

	_, d, err := deferString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if d.deferred != 0 || total(d.skipped) != 1 {
		t.Errorf("deferred=%d skipped=%v", d.deferred, d.skipped)
	}

	_, d, err = deferString(doc, func(d *deferrer) { d.includeBody = true })
	if err != nil {
		t.Fatal(err)
	}
	if d.deferred != 1 {
		t.Errorf("-body deferred=%d, want 1", d.deferred)
	}
}

// TestTheBodyBoundaryIsTracked: a script after </body> is not in the body, and a
// document with no body element has no body scripts.
func TestTheBodyBoundaryIsTracked(t *testing.T) {
	for _, tt := range []struct {
		doc  string
		want int
	}{
		{`<html><head><script src="/a.js"></script></head><body>x</body></html>`, 1},
		{`<html><head></head><body><script src="/a.js"></script></body></html>`, 0},
		{`<html><head></head><body>x</body><script src="/a.js"></script></html>`, 1},
		{`<script src="/a.js"></script>`, 1},
	} {
		_, d, err := deferString(tt.doc)
		if err != nil {
			t.Fatalf("%s: %v", tt.doc, err)
		}
		if d.deferred != tt.want {
			t.Errorf("%s: deferred=%d, want %d", tt.doc, d.deferred, tt.want)
		}
	}
}

// TestSkipHostMatchesSubdomains, since a host is skipped for a reason that
// applies to its subdomains too.
func TestSkipHostMatchesSubdomains(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want int
	}{
		{"https://ads.example/a.js", 0},
		{"https://sub.ads.example/a.js", 0},
		{"https://ADS.EXAMPLE/a.js", 0},
		{"//ads.example/a.js", 0},
		{"https://notads.example/a.js", 1},
		{"https://ads.example.org/a.js", 1},
		{"/local.js", 1},
	} {
		doc := `<html><head><script src="` + tt.src + `"></script></head><body>x</body></html>`
		_, d, err := deferString(doc, func(d *deferrer) {
			d.skipHosts = []string{"ads.example"}
		})
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}
		if d.deferred != tt.want {
			t.Errorf("%s: deferred=%d, want %d", tt.src, d.deferred, tt.want)
		}
	}
}

func TestHostOf(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"https://a.example/x.js", "a.example"},
		{"//a.example/x.js", "a.example"},
		{"https://a.example:8443/x.js", "a.example"},
		{"https://user@a.example/x.js", "a.example"},
		{"https://a.example", "a.example"},
		{"/local.js", ""},
		{"x.js", ""},
		{"", ""},
	} {
		if got := hostOf(tt.in); got != tt.want {
			t.Errorf("hostOf(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestABooleanAttributeIsWrittenWithAValue. There is no way to write a bare
// attribute through this API, so the output says defer="" where the page's own
// scripts say defer. Any parser treats those as the same attribute; a diff does
// not.
func TestABooleanAttributeIsWrittenWithAValue(t *testing.T) {
	out, _, err := deferString(
		`<html><head><script src="/a.js"></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `defer=""`) {
		t.Errorf("expected the valued spelling: %s", out)
	}

	// A bare one in the input is passed through as it was written.
	out, _, err = deferString(
		`<html><head><script src="/a.js" defer></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `defer></script>`) {
		t.Errorf("a bare attribute was rewritten: %s", out)
	}
}

// TestTheReportWarnsAboutDocumentWrite, which is the limit of what a src
// attribute can tell anyone.
func TestTheReportWarnsAboutDocumentWrite(t *testing.T) {
	_, d, err := deferString(
		`<html><head><script src="/a.js"></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.report(), "document.write") {
		t.Errorf("the report does not mention the risk:\n%s", d.report())
	}

	// And says nothing when it changed nothing.
	_, d, err = deferString(`<html><head></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.report(), "document.write") {
		t.Errorf("warned about a change it did not make:\n%s", d.report())
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	if _, _, err := deferString(page, func(d *deferrer) {
		d.skipHosts = []string{""}
	}); err == nil {
		t.Error("an empty -skip-host was accepted")
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var manifest = map[string]string{
	"/js/app.js":                 "sha384-" + strings.Repeat("A", 64),
	"/css/site.css":              "sha384-" + strings.Repeat("B", 64),
	"https://cdn.example/lib.js": "sha256-" + strings.Repeat("C", 43) + "=",
}

var corpus = []string{
	`<script src="/js/app.js"></script>`,
	`<link rel="stylesheet" href="/css/site.css">`,
	`<link rel="preload" href="/js/app.js" as="script">`,
	`<link rel="modulepreload" href="/js/app.js">`,
	`<link rel="icon" href="/favicon.ico">`,
	`<script src="/js/unknown.js"></script>`,
	`<script src="https://cdn.example/lib.js" crossorigin="use-credentials"></script>`,
	`<script src="/js/app.js" integrity="sha384-` + strings.Repeat("A", 64) + `"></script>`,
	`<script src="/js/app.js" integrity="sha384-WRONG"></script>`,
	`<script>inline()</script>`,
	`<script src=""></script><script src="  "></script>`,
	`<!DOCTYPE html><html><head><script src="/js/app.js"></script></head><body>b</body></html>`,
	`<!-- <script src="/js/disabled.js"></script> -->`,
	`<head></head>`,
	``,
}

func chunked(in string, n int, a *adder) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, a.options()...)
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

// TestChunkInvariance also exercises the streaming insertion: the manifest block
// is produced by a sink at the point the content is needed, which is a different
// moment for every chunk size.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := addString(doc, manifest, true)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 17} {
			got, err := chunked(doc, n, &adder{manifest: manifest, embed: true})
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

// TestUncoveredSubresourcesAreLeftAloneAndReported is the property that makes
// this safe to run: inventing an integrity value breaks the page, and omitting
// the attribute quietly defeats the point of running it.
func TestUncoveredSubresourcesAreLeftAloneAndReported(t *testing.T) {
	in := `<script src="/js/unknown.js"></script>`
	got, a, err := addString(in, manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("changed an uncovered subresource:\n got: %s\nwant: %s", got, in)
	}
	if len(a.uncovered) != 1 || a.uncovered[0] != "/js/unknown.js" {
		t.Errorf("uncovered = %v, want [/js/unknown.js]", a.uncovered)
	}
}

// TestExistingIntegrityIsCheckedNotOverwritten. Replacing a hash that disagrees
// would turn a mismatch the browser would catch into one nobody would.
func TestExistingIntegrityIsCheckedNotOverwritten(t *testing.T) {
	t.Run("agrees", func(t *testing.T) {
		in := `<script src="/js/app.js" integrity="` + manifest["/js/app.js"] + `"></script>`
		got, a, err := addString(in, manifest, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != in {
			t.Errorf("changed a document that already agreed:\n got: %s", got)
		}
		if a.kept != 1 || len(a.conflicts) != 0 {
			t.Errorf("kept=%d conflicts=%v, want 1 and none", a.kept, a.conflicts)
		}
	})

	t.Run("disagrees", func(t *testing.T) {
		in := `<script src="/js/app.js" integrity="sha384-WRONG"></script>`
		got, a, err := addString(in, manifest, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != in {
			t.Errorf("overwrote a conflicting hash:\n got: %s", got)
		}
		if len(a.conflicts) != 1 {
			t.Errorf("conflicts = %v, want one", a.conflicts)
		}
	})
}

// TestCrossoriginIsAddedButNotOverwritten: integrity is only enforced on a CORS
// fetch, so the two travel together - but a document that chose
// use-credentials chose it for a reason.
func TestCrossoriginIsAddedButNotOverwritten(t *testing.T) {
	got, _, err := addString(`<script src="/js/app.js"></script>`, manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `crossorigin="anonymous"`) {
		t.Errorf("crossorigin was not added: %s", got)
	}

	got, _, err = addString(`<script src="/js/app.js" crossorigin="use-credentials"></script>`, manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "use-credentials") {
		t.Errorf("overwrote the document's crossorigin: %s", got)
	}
	if strings.Count(got, "crossorigin") != 1 {
		t.Errorf("crossorigin appears twice: %s", got)
	}
}

// TestOnlyIntegrityHonouringLinksAreTouched. A rel the browser does not check
// integrity for would carry a meaningless attribute.
func TestOnlyIntegrityHonouringLinksAreTouched(t *testing.T) {
	for _, tt := range []struct {
		in    string
		wants bool
	}{
		{`<link rel="stylesheet" href="/css/site.css">`, true},
		{`<link rel="preload" href="/css/site.css">`, true},
		{`<link rel="modulepreload" href="/css/site.css">`, true},
		{`<link rel="icon" href="/css/site.css">`, false},
		{`<link rel="alternate" href="/css/site.css">`, false},
		{`<link href="/css/site.css">`, false},
	} {
		got, _, err := addString(tt.in, manifest, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if has := strings.Contains(got, "integrity="); has != tt.wants {
			t.Errorf("%s -> %s (integrity present = %v, want %v)", tt.in, got, has, tt.wants)
		}
	}
}

// TestManifestBlockIsDataNotScript. The embedded manifest is untrusted input
// echoed into the document, and a JSON block read with JSON.parse is the shape
// that keeps it inert. Nothing in the data may end the script element.
func TestManifestBlockIsDataNotScript(t *testing.T) {
	hostile := map[string]string{
		`/x.js</script><img src=1 onerror=alert(1)>`: "sha384-" + strings.Repeat("A", 64),
	}
	in := `<head><script src="/x.js</script><img src=1 onerror=alert(1)>"></script></head>`
	got, a, err := addString(in, hostile, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.added != 1 {
		t.Fatalf("added=%d, want 1 (the hostile URL is in the manifest)", a.added)
	}
	block := got[strings.Index(got, `id="sri-manifest"`):]
	if strings.Contains(block, "</script><img") {
		t.Errorf("the manifest block was ended by its own data: %s", block)
	}
	if !strings.Contains(block, `\u003c/script\u003e`) {
		t.Errorf("expected < and > escaped by encoding/json: %s", block)
	}
}

// TestManifestBlockOnlyListsWhatWasUsed: embedding the whole manifest would
// disclose the URLs of resources this page does not load.
func TestManifestBlockOnlyListsWhatWasUsed(t *testing.T) {
	got, _, err := addString(`<head><script src="/js/app.js"></script></head>`, manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/js/app.js") {
		t.Errorf("the used entry is missing: %s", got)
	}
	if strings.Contains(got, "cdn.example") {
		t.Errorf("an unused manifest entry was disclosed: %s", got)
	}
}

func TestNoManifestBlockWithoutHead(t *testing.T) {
	got, _, err := addString(`<script src="/js/app.js"></script>`, manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "sri-manifest") {
		t.Errorf("injected a manifest block with no head: %s", got)
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := addString(doc, manifest, false)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, a, err := addString(once, manifest, false)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if a.added != 0 {
			t.Errorf("second pass of %q added %d attribute(s)", doc, a.added)
		}
	}
}

func TestManifestParsing(t *testing.T) {
	good := "# note\n\n/a.js sha384-" + strings.Repeat("A", 64) + "\n/b.css sha512-" + strings.Repeat("B", 88) + "\n"
	m, err := parseManifest(strings.NewReader(good))
	if err != nil {
		t.Fatalf("good manifest rejected: %v", err)
	}
	if len(m) != 2 {
		t.Errorf("parsed %d entries, want 2", len(m))
	}

	for _, bad := range []string{
		"/a.js",
		"/a.js md5-abc",
		"/a.js sha384-",
		"/a.js sha384-not+base64\"onload=x",
		"/a.js sha384-abc def",
		"/a.js abc",
	} {
		if _, err := parseManifest(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted a bad manifest line: %q", bad)
		}
	}
}

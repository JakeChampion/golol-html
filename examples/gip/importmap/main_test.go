package main

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var basic = Map{Imports: map[string]string{"lodash": "/v/lodash.js"}}

func run(t *testing.T, in string, m Map) (string, Report) {
	t.Helper()
	var out bytes.Buffer
	rep, err := Inject(&out, strings.NewReader(in), m)
	if err != nil {
		t.Fatalf("Inject(%q): %v", in, err)
	}
	return out.String(), rep
}

// maps re-parses out and returns every import map in it, in document order,
// decoded. Reading the output back through the library is the only way to know
// what a browser would see; a substring check on serialised markup has told me
// the wrong thing often enough.
func maps(t *testing.T, out string) []Map {
	t.Helper()
	var got []Map
	var buf strings.Builder
	inMap := false
	_, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			if scriptType(e) != "importmap" {
				return nil
			}
			inMap = true
			buf.Reset()
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				inMap = false
				var m Map
				if err := json.Unmarshal([]byte(buf.String()), &m); err != nil {
					t.Errorf("import map body %q is not JSON: %v", buf.String(), err)
					return nil
				}
				got = append(got, m)
				return nil
			})
		}),
		lolhtml.OnText("script", func(c *lolhtml.TextChunk) error {
			if inMap {
				buf.WriteString(c.Text())
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("re-parsing %q: %v", out, err)
	}
	return got
}

// sequence re-parses out and labels every script and link in document order, so
// a test can assert where the injected map sits relative to the anchors rather
// than guessing from byte offsets.
func sequence(t *testing.T, out string) []string {
	t.Helper()
	var seq []string
	_, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			switch scriptType(e) {
			case "importmap":
				seq = append(seq, "importmap")
			case "module":
				seq = append(seq, "module")
			default:
				seq = append(seq, "script")
			}
			return nil
		}),
		lolhtml.OnElement("link", func(e *lolhtml.Element) error {
			if hasToken(e, "rel", "modulepreload") {
				seq = append(seq, "modulepreload")
			} else {
				seq = append(seq, "link")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("re-parsing %q: %v", out, err)
	}
	return seq
}

func indexOf(seq []string, want string) int {
	for i, s := range seq {
		if s == want {
			return i
		}
	}
	return -1
}

// firstAnchor returns the position of the first thing that resolves through an
// import map, which is what the map has to precede.
func firstAnchor(seq []string) int {
	for i, s := range seq {
		if s == "module" || s == "modulepreload" {
			return i
		}
	}
	return -1
}

func TestAnchors(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		anchor string
	}{
		{"module", `<script type="module" src="/a.js"></script>`, `script type="module"`},
		{"module upper", `<script type="MODULE" src="/a.js"></script>`, `script type="module"`},
		{"module mixed", `<script type="Module" src="/a.js"></script>`, `script type="module"`},
		// Browsers strip leading and trailing whitespace from the type before
		// comparing it, so this runs as a module. script[type=module] does not
		// match it, which is why the program selects every script.
		{"module padded", "<script type=\" \tmodule\n\" src=\"/a.js\"></script>", `script type="module"`},
		{"inline module", `<script type="module">import "lodash"</script>`, `script type="module"`},
		{"modulepreload", `<link rel="modulepreload" href="/a.js">`, `link rel="modulepreload"`},
		{"modulepreload token", `<link rel="preload modulepreload" href="/a.js">`, `link rel="modulepreload"`},
		{"modulepreload case", `<link rel="ModulePreload" href="/a.js">`, `link rel="modulepreload"`},
		{"classic first", `<script src="/c.js"></script><script type="module" src="/a.js"></script>`, `script type="module"`},
		{"link first", `<link rel="modulepreload" href="/a.js"><script type="module"></script>`, `link rel="modulepreload"`},
		{"module first", `<script type="module"></script><link rel="modulepreload" href="/a.js">`, `script type="module"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, rep := run(t, tt.in, basic)
			if !rep.Injected {
				t.Fatalf("not injected: %s\nout: %s", rep.Skipped, out)
			}
			if rep.Anchor != tt.anchor {
				t.Errorf("anchor = %q, want %q", rep.Anchor, tt.anchor)
			}
			got := maps(t, out)
			if len(got) != 1 {
				t.Fatalf("got %d import maps, want 1: %s", len(got), out)
			}
			if !reflect.DeepEqual(got[0], basic) {
				t.Errorf("decoded %+v, want %+v", got[0], basic)
			}
			// Present is not enough. The map has to be immediately before
			// the anchor: no module script and no modulepreload link may come
			// between them, and none may come before it.
			seq := sequence(t, out)
			i, j := indexOf(seq, "importmap"), firstAnchor(seq)
			if i < 0 || j != i+1 {
				t.Errorf("map at %d, first anchor at %d, order %v: %s", i, j, seq, out)
			}
		})
	}
}

func TestNotAnchors(t *testing.T) {
	tests := []struct{ name, in string }{
		{"empty", ``},
		{"no scripts", `<p>hello</p>`},
		{"classic", `<script src="/c.js"></script>`},
		{"empty type", `<script type="" src="/c.js"></script>`},
		{"json ld", `<script type="application/ld+json">{}</script>`},
		{"module substring", `<script type="modulez"></script>`},
		{"module in a longer type", `<script type="text/module"></script>`},
		{"preload not module", `<link rel="preload" href="/a.js">`},
		{"modulepreload substring", `<link rel="modulepreloadx" href="/a.js">`},
		// Template content is never executed, so nothing in here is an anchor
		// and nothing in here would be read if the map went inside it.
		{"module in template", `<template><script type="module"></script></template>`},
		{"module in nested template", `<template><template><script type="module"></script></template></template>`},
		{"modulepreload in template", `<template><link rel="modulepreload" href="/a.js"></template>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, rep := run(t, tt.in, basic)
			if rep.Injected {
				t.Fatalf("injected before %s: %s", rep.Anchor, out)
			}
			if rep.Skipped == "" {
				t.Error("neither injected nor skipped")
			}
			if out != tt.in {
				t.Errorf("output changed:\n got %q\nwant %q", out, tt.in)
			}
		})
	}
}

// A template ends, and a module script after it is an anchor again. This is the
// half of the depth counter that a test only checking "skipped" would miss.
func TestTemplateDepthRecovers(t *testing.T) {
	tests := []struct{ name, in string }{
		{"after template", `<template><script type="module"></script></template><script type="module" id="real"></script>`},
		{"after nested", `<template><template></template></template><script type="module" id="real"></script>`},
		{"sibling templates", `<template></template><template></template><script type="module" id="real"></script>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, rep := run(t, tt.in, basic)
			if !rep.Injected {
				t.Fatalf("not injected: %s", rep.Skipped)
			}
			// The map must sit outside the template, immediately before the
			// script with id=real.
			i, j := strings.Index(out, `type="importmap"`), strings.Index(out, `id="real"`)
			if i < 0 || j < 0 || i > j {
				t.Errorf("map at %d, real script at %d: %s", i, j, out)
			}
			if k := strings.LastIndex(out, "</template>"); k > i {
				t.Errorf("map landed inside a template: %s", out)
			}
		})
	}
}

// A self-closing template in foreign content has no end tag. Asking it for one
// fails the rewrite, so the program checks CanHaveContent first and this is the
// test that would notice if it stopped.
func TestSelfClosingTemplateInForeignContent(t *testing.T) {
	in := `<svg><template/></svg><script type="module"></script>`
	out, rep := run(t, in, basic)
	if !rep.Injected {
		t.Fatalf("not injected: %s\nout: %s", rep.Skipped, out)
	}
}

func TestExistingMapIsLeftAlone(t *testing.T) {
	in := `<script type="importmap">{"imports":{"a":"/a.js"}}</script><script type="module"></script>`
	out, rep := run(t, in, basic)
	if rep.Injected {
		t.Fatalf("injected a second import map: %s", out)
	}
	if rep.Skipped != "document already has an import map" {
		t.Errorf("skipped = %q", rep.Skipped)
	}
	if out != in {
		t.Errorf("output changed:\n got %q\nwant %q", out, in)
	}
}

// An import map inside a template is inert, so it is not the document's map and
// it does not stop the injection.
func TestMapInTemplateDoesNotCount(t *testing.T) {
	in := `<template><script type="importmap">{}</script></template><script type="module"></script>`
	out, rep := run(t, in, basic)
	if !rep.Injected {
		t.Fatalf("not injected: %s\nout: %s", rep.Skipped, out)
	}
	if rep.RemovedStale != 0 {
		t.Errorf("removed %d maps, want 0: %s", rep.RemovedStale, out)
	}
}

// An import map after the first module script was already ignored before this
// program ran: a browser reads the first one, and only if it precedes the first
// module script. Removing it leaves one map instead of one and a decoy.
func TestStaleMapAfterAnchorIsRemoved(t *testing.T) {
	in := `<script type="module"></script><script type="importmap">{"imports":{"a":"/a.js"}}</script>`
	out, rep := run(t, in, basic)
	if !rep.Injected {
		t.Fatalf("not injected: %s", rep.Skipped)
	}
	if rep.RemovedStale != 1 {
		t.Errorf("RemovedStale = %d, want 1: %s", rep.RemovedStale, out)
	}
	got := maps(t, out)
	if len(got) != 1 {
		t.Fatalf("got %d import maps, want 1: %s", len(got), out)
	}
	if !reflect.DeepEqual(got[0], basic) {
		t.Errorf("surviving map is %+v, want %+v", got[0], basic)
	}
}

func TestInjectsOnlyOnce(t *testing.T) {
	in := strings.Repeat(`<script type="module"></script>`, 5) +
		strings.Repeat(`<link rel="modulepreload" href="/a.js">`, 5)
	out, _ := run(t, in, basic)
	if got := maps(t, out); len(got) != 1 {
		t.Fatalf("got %d import maps, want 1: %s", len(got), out)
	}
}

// The map goes inside a script, which is raw text. A specifier or URL carrying
// "</script" would end the element early and spill the rest of the JSON into the
// page as markup, so it is escaped - and the check is that the JSON survives the
// round trip, not that some substring is absent.
func TestRawTextHostileSpecifiers(t *testing.T) {
	hostile := []Map{
		{Imports: map[string]string{"</script>": "/a.js"}},
		{Imports: map[string]string{"a": "/a.js?x=</script><img src=x onerror=alert(1)>"}},
		{Imports: map[string]string{"</SCRIPT ": "/a.js"}},
		{Imports: map[string]string{"<!--": "/a.js"}},
		{Imports: map[string]string{"a&b": "/a.js?x=1&y=2"}},
		{Scopes: map[string]map[string]string{"/</script>/": {"a": "/a.js"}}},
	}
	for _, m := range hostile {
		out, rep := run(t, `<script type="module"></script>`, m)
		if !rep.Injected {
			t.Errorf("%+v: not injected: %s", m, rep.Skipped)
			continue
		}
		got := maps(t, out)
		if len(got) != 1 {
			t.Errorf("%+v: got %d import maps, want 1: %s", m, len(got), out)
			continue
		}
		if !reflect.DeepEqual(got[0], m) {
			t.Errorf("%+v round-tripped as %+v\nout: %s", m, got[0], out)
		}
	}
}

// The rewriter is a stream, so the chunk boundaries the reader happens to hand
// it must not change the output. A boundary inside the type attribute of the
// anchor is the case that would break a program reading attributes by hand.
func TestChunkInvariance(t *testing.T) {
	in := `<html><head><template><script type="module"></script></template>` +
		"<script type=\" MODULE \" src=\"/a.js\"></script></head><body><p>x</p></body></html>"
	want, _ := run(t, in, basic)
	for n := 1; n <= len(in); n++ {
		var out bytes.Buffer
		if _, err := Inject(&out, &chunked{s: in, n: n}, basic); err != nil {
			t.Fatalf("chunk %d: %v", n, err)
		}
		if out.String() != want {
			t.Fatalf("chunk %d changed the output:\n got %q\nwant %q", n, out.String(), want)
		}
	}
}

// chunked hands out at most n bytes per Read, so the rewriter sees a different
// set of boundaries for every n.
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

func TestEmptyMapIsAnError(t *testing.T) {
	var out bytes.Buffer
	if _, err := Inject(&out, strings.NewReader(`<script type="module"></script>`), Map{}); err == nil {
		t.Fatal("an empty map injected without complaint")
	}
}

package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func check(t *testing.T, doc string, opts Options) (string, Result, error) {
	t.Helper()
	var out strings.Builder
	res, err := Check(&out, strings.NewReader(doc), opts)
	return out.String(), res, err
}

func classOf(t *testing.T, res Result, url string) (Class, bool) {
	t.Helper()
	for _, f := range res.Findings {
		if f.URL == url {
			return f.Class, true
		}
	}
	return 0, false
}

// TestTheClassificationIsTheSpecificationsSplit: what a browser blocks, what it
// upgrades, and what is not a subresource at all.
func TestTheClassificationIsTheSpecificationsSplit(t *testing.T) {
	for _, tc := range []struct {
		doc   string
		class Class
	}{
		{`<script src="http://x/a.js"></script>`, Blockable},
		{`<iframe src="http://x/f"></iframe>`, Blockable},
		{`<object data="http://x/o"></object>`, Blockable},
		{`<embed src="http://x/e">`, Blockable},
		{`<link rel="stylesheet" href="http://x/s.css">`, Blockable},
		{`<link rel="preload" href="http://x/p">`, Blockable},
		{`<svg><use xlink:href="http://x/i.svg#a"/></svg>`, Blockable},
		{`<img src="http://x/i.png">`, Upgradeable},
		{`<img srcset="http://x/i.png 2x">`, Upgradeable},
		{`<video poster="http://x/p.jpg"></video>`, Upgradeable},
		{`<audio src="http://x/a.mp3"></audio>`, Upgradeable},
		{`<track src="http://x/t.vtt">`, Upgradeable},
		{`<input src="http://x/b.png">`, Upgradeable},
		{`<div style="background:url(http://x/bg.png)"></div>`, Upgradeable},
		{`<style>.a{background:url(http://x/bg.png)}</style>`, Upgradeable},
		{`<a href="http://x/p">l</a>`, Navigation},
		{`<area href="http://x/p">`, Navigation},
		{`<form action="http://x/go"></form>`, Navigation},
		{`<q cite="http://x/c"></q>`, Navigation},
		{`<link rel="canonical" href="http://x/c">`, Navigation},
	} {
		_, res, err := check(t, tc.doc, Options{})
		if err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
		if len(res.Findings) != 1 {
			t.Errorf("%q: findings are %v, want one", tc.doc, res.Findings)
			continue
		}
		if got := res.Findings[0].Class; got != tc.class {
			t.Errorf("%q: class is %v, want %v", tc.doc, got, tc.class)
		}
	}
}

// TestOnlyHTTPCounts: an https URL, a relative one, a protocol-relative one and a
// data URL are not mixed content.
func TestOnlyHTTPCounts(t *testing.T) {
	for _, doc := range []string{
		`<img src="https://x/i.png">`,
		`<img src="/i.png">`,
		`<img src="//x/i.png">`,
		`<img src="data:image/gif;base64,R0lGOD">`,
		`<a href="mailto:a@b">m</a>`,
		`<img src="HTTPS://x/i.png">`,
	} {
		got, res, err := check(t, doc, Options{Upgrade: true})
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if len(res.Findings) != 0 {
			t.Errorf("%q: findings are %v, want none", doc, res.Findings)
		}
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
	// The scheme is matched case-insensitively.
	_, res, err := check(t, `<img src="HTTP://x/i.png">`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Errorf("%v", res.Findings)
	}
}

// TestUpgradeRewritesWhatABrowserWouldAndLeavesNavigations.
func TestUpgradeRewritesWhatABrowserWouldAndLeavesNavigations(t *testing.T) {
	const doc = `<img src="http://x/i.png"><script src="http://x/a.js"></script>` +
		`<a href="http://x/p">l</a><style>.a{background:url(http://x/bg.png)}</style>` +
		`<img srcset="http://x/1.png 1x, https://x/2.png 2x">`
	got, res, err := check(t, doc, Options{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "http://x") != 1 {
		t.Errorf("got %q, want only the link left as http", got)
	}
	if !strings.Contains(got, `<a href="http://x/p">`) {
		t.Errorf("the link was upgraded: %q", got)
	}
	if res.Upgraded != 4 {
		t.Errorf("Upgraded = %d, want 4: img, script, stylesheet url and one srcset member", res.Upgraded)
	}
	// A srcset keeps its descriptors and its secure members.
	if !strings.Contains(got, `srcset="https://x/1.png 1x, https://x/2.png 2x"`) {
		t.Errorf("got %q", got)
	}
	// The stylesheet still parses as CSS: the child combinator case is the reason
	// the text goes back as HTML rather than Text.
	got, _, err = check(t, `<style>.a > .b{background:url(http://x/bg.png)}</style>`, Options{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<style>.a > .b{background:url(https://x/bg.png)}</style>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestStrictRefusesAndWritesNothing, which is the whole point: a rewrite that stops
// mid-page has already delivered a short page unless the caller holds the output.
func TestStrictRefusesAndWritesNothing(t *testing.T) {
	const doc = `<p>a</p><p>b</p><script src="http://x/a.js"></script><p>c</p>`
	got, res, err := check(t, doc, Options{Strict: true})
	if err == nil {
		t.Fatal("Check succeeded, want a refusal")
	}
	var blocked ErrBlockable
	if !errors.As(err, &blocked) {
		t.Errorf("err = %v, want ErrBlockable", err)
	}
	if blocked.Finding.URL != "http://x/a.js" {
		t.Errorf("%v", blocked)
	}
	if got != "" {
		t.Errorf("the caller received %q, want nothing", got)
	}
	if !res.Refused || res.RefusedAt != "http://x/a.js" {
		t.Errorf("%v", res)
	}
	// Upgradeable content does not refuse: a browser would load it over https.
	got, res, err = check(t, `<p>a</p><img src="http://x/i.png">`, Options{Strict: true})
	if err != nil {
		t.Fatalf("an upgradeable finding refused the page: %v", err)
	}
	if got != `<p>a</p><img src="http://x/i.png">` {
		t.Errorf("got %q", got)
	}
	if res.Refused || res.OK() != true {
		t.Errorf("%v", res)
	}
	// A clean page comes through whole in strict mode.
	const clean = `<p>a</p><img src="https://x/i.png"><p>b</p>`
	got, res, err = check(t, clean, Options{Strict: true})
	if err != nil || got != clean {
		t.Errorf("got %q, %v", got, err)
	}
	if !res.OK() {
		t.Errorf("%v", res)
	}
}

// TestWithoutBufferingTheClientWouldHaveHadAPrefix, which is what the buffering in
// Check exists to prevent. This is the library's behaviour, pinned here because the
// program's design depends on it.
func TestWithoutBufferingTheClientWouldHaveHadAPrefix(t *testing.T) {
	const doc = `<p>a</p><p>b</p><script src="http://x/a.js"></script><p>c</p>`
	f := &finder{opts: Options{Strict: true}}
	var direct strings.Builder
	w, err := lolhtml.NewWriter(&direct, f.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err == nil {
		t.Fatal("the handler did not refuse")
	}
	_ = w.Close()
	if direct.String() != `<p>a</p><p>b</p>` {
		t.Errorf("the sink holds %q, want the prefix before the script", direct.String())
	}
	// Which is why Check buffers: same document, same handlers, nothing delivered.
	got, _, err := check(t, doc, Options{Strict: true})
	if err == nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}
}

// TestReportOnlyWritesNothingAndStillFinds, for a checker in a build step.
func TestReportOnlyWritesNothingAndStillFinds(t *testing.T) {
	const doc = `<img src="http://x/i.png"><script src="http://x/a.js"></script>`
	res, err := Check(io.Discard, strings.NewReader(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 2 || len(res.Blockable()) != 1 {
		t.Errorf("%v", res.Findings)
	}
	if res.OK() {
		t.Error("OK() is true with blockable content")
	}
}

// TestCheckingTwiceChangesNothing, with and without upgrading.
func TestCheckingTwiceChangesNothing(t *testing.T) {
	for _, opts := range []Options{{}, {Upgrade: true}} {
		for _, doc := range []string{
			`<img src="http://x/i.png">`,
			`<a href="http://x/p">l</a>`,
			`<style>.a > .b{background:url(http://x/bg.png)}</style>`,
			`<img srcset="http://x/1.png 1x, /2.png 2x">`,
		} {
			once, _, err := check(t, doc, opts)
			if err != nil {
				t.Fatalf("%q: %v", doc, err)
			}
			twice, res, err := check(t, once, opts)
			if err != nil {
				t.Fatalf("%q: %v", doc, err)
			}
			if twice != once {
				t.Errorf("%q (upgrade=%v)\n once %q\ntwice %q", doc, opts.Upgrade, once, twice)
			}
			if opts.Upgrade && res.Upgraded != 0 {
				t.Errorf("%q: the second pass upgraded %d", doc, res.Upgraded)
			}
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<p>a</p><img src="http://x/i.png"><style>.a > .b{background:url(http://x/bg.png)}</style><a href="http://x/p">l</a>`
	want, wantRes, err := check(t, doc, Options{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		f := &finder{opts: Options{Upgrade: true}}
		var out bytes.Buffer
		w, err := lolhtml.NewWriter(&out, f.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if len(f.res.Findings) != len(wantRes.Findings) || f.res.Upgraded != wantRes.Upgraded {
			t.Errorf("chunks of %d: %v, want %v", size, f.res, wantRes)
		}
	}
}

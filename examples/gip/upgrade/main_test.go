package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<img src="http://cdn.example/1.png">`,
	`<img src="https://cdn.example/1.png">`,
	`<img src="http://192.168.1.1/x.png">`,
	`<img src="http://localhost/x.png">`,
	`<img src="http://intranet/x.png">`,
	`<a href="http://link.example/p">t</a>`,
	`<style>body{background:url(http://cdn.example/bg.png)}</style>`,
	`<style>.a{background:URL( "http://cdn.example/a.png" )}</style>`,
	`<style>.b{background:url('http://cdn.example/b.png')}</style>`,
	`<style>.c{background:url(https://ok.example/c.png)}</style>`,
	`<style>.d{background:url(}</style>`,
	`<style></style>`,
	`<div style="background:url(http://cdn.example/i.png)">x</div>`,
	`<link rel="stylesheet" href="http://cdn.example/s.css">`,
	`<script src="http://cdn.example/a.js"></script>`,
	`<form action="http://cdn.example/s"><input formaction="http://cdn.example/t"></form>`,
	`<video poster="http://cdn.example/p.jpg"><source src="http://cdn.example/v.mp4"></video>`,
	`<object data="http://cdn.example/o.swf"></object>`,
	`<head></head>`,
	`<!DOCTYPE html><html><head><style>a{background:url(http://x.example/y)}</style></head><body><p>t</p></body></html>`,
	`<p>no urls at all</p>`,
	``,
}

func chunked(in string, n int, u *upgrader) (string, error) {
	var out bytes.Buffer
	opts := append(u.options(), lolhtml.WithEncoding(u.encoding))
	w, err := lolhtml.NewWriter(&out, opts...)
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

func newUpgrader() *upgrader {
	return &upgrader{encoding: "utf-8", skip: map[string]bool{}}
}

// TestChunkInvariance is the test this program is shaped around. A CSS
// url(http://...) can straddle any chunk boundary, and a text handler that
// rewrote chunk by chunk would silently miss every URL that happened to be
// split - producing a document that looks fine and still fetches over http. So
// the style body is accumulated and replaced at the end tag, and this is what
// says so.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := upgradeString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 5, 19} {
			got, err := chunked(doc, n, newUpgrader())
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

// TestCSSURLSplitAcrossEveryBoundary is the same claim made exhaustively for one
// document: whichever byte the write boundary falls on, the URL is still found.
func TestCSSURLSplitAcrossEveryBoundary(t *testing.T) {
	doc := `<style>body{background:url(http://cdn.example/bg.png)}</style>`
	want := `<style>body{background:url(https://cdn.example/bg.png)}</style>`

	for split := 1; split < len(doc); split++ {
		u := newUpgrader()
		var out bytes.Buffer
		opts := append(u.options(), lolhtml.WithEncoding("utf-8"))
		w, err := lolhtml.NewWriter(&out, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc[:split])); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if _, err := w.Write([]byte(doc[split:])); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if got := out.String(); got != want {
			t.Errorf("split at %d:\n got: %s\nwant: %s", split, got, want)
		}
		if u.cssUpgraded != 1 {
			t.Errorf("split at %d: cssUpgraded=%d, want 1", split, u.cssUpgraded)
		}
	}
}

// TestNavigationsAreNotUpgraded. Upgrading a link changes where the user goes;
// upgrading a subresource changes how a byte is fetched. Only the second is safe
// to do without asking.
func TestNavigationsAreNotUpgraded(t *testing.T) {
	in := `<a href="http://link.example/p">t</a><area href="http://link.example/q">`
	got, u, err := upgradeString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("a navigation was rewritten:\n got: %s\nwant: %s", got, in)
	}
	if u.navigations != 2 {
		t.Errorf("navigations=%d, want 2", u.navigations)
	}
}

// TestHostsThatCannotBeUpgraded: a private address or a bare hostname usually
// has no certificate, so upgrading it turns a working page into a broken one.
func TestHostsThatCannotBeUpgraded(t *testing.T) {
	for _, tt := range []struct{ host, reason string }{
		{"localhost", "localhost"},
		{"api.localhost", "localhost"},
		{"127.0.0.1", "private address"},
		{"::1", "private address"},
		{"10.1.2.3", "private address"},
		{"192.168.0.1", "private address"},
		{"172.16.0.1", "private address"},
		{"169.254.1.1", "private address"},
		{"8.8.8.8", "IP literal"},
		{"intranet", "not a public hostname"},
		{"cdn.example", ""},
		{"a.b.c.example", ""},
	} {
		u := newUpgrader()
		if got := u.cannotUpgrade(tt.host); got != tt.reason {
			t.Errorf("cannotUpgrade(%q) = %q, want %q", tt.host, got, tt.reason)
		}
	}
}

func TestSkipList(t *testing.T) {
	in := `<img src="http://keep.example/x"><img src="http://go.example/y">`
	got, u, err := upgradeString(in, func(u *upgrader) {
		u.skip["keep.example"] = true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `src="http://keep.example/x"`) {
		t.Errorf("skipped host was upgraded: %s", got)
	}
	if !strings.Contains(got, `src="https://go.example/y"`) {
		t.Errorf("other host was not upgraded: %s", got)
	}
	if len(u.unupgradable) != 1 {
		t.Errorf("unupgradable=%v, want one", u.unupgradable)
	}
}

// TestOnlyTheSchemeChanges: reserialising a URL would also normalise its path,
// which is not this program's business and would break signed URLs.
func TestOnlyTheSchemeChanges(t *testing.T) {
	for _, in := range []string{
		`<img src="http://cdn.example/a//b/../c?q=1&amp;r=2#frag">`,
		`<img src="http://cdn.example/%2Fencoded">`,
		`<img src="http://cdn.example:8080/p">`,
		`<img src="http://user:pw@cdn.example/p">`,
	} {
		got, _, err := upgradeString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		want := strings.Replace(in, "http://", "https://", 1)
		if got != want {
			t.Errorf("\n got: %s\nwant: %s", got, want)
		}
	}
}

// TestCSSFormsAndCasing: url() is a case-insensitive token and takes three
// quoting forms, and whitespace inside the parentheses is the document's.
func TestCSSFormsAndCasing(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`url(http://x.example/a)`, `url(https://x.example/a)`},
		{`URL(http://x.example/a)`, `URL(https://x.example/a)`},
		{`Url( http://x.example/a )`, `Url( https://x.example/a )`},
		{`url("http://x.example/a")`, `url("https://x.example/a")`},
		{`url('http://x.example/a')`, `url('https://x.example/a')`},
		{`url(  "http://x.example/a"  )`, `url(  "https://x.example/a"  )`},
		{`url(https://x.example/a)`, `url(https://x.example/a)`},
		{`url()`, `url()`},
		{`url(`, `url(`},
		{`no urls here`, `no urls here`},
		{`url(http://a.example/1) url(http://b.example/2)`, `url(https://a.example/1) url(https://b.example/2)`},
	} {
		u := newUpgrader()
		got, _ := u.upgradeCSS(tt.in)
		if got != tt.want {
			t.Errorf("upgradeCSS(%q)\n got: %s\nwant: %s", tt.in, got, tt.want)
		}
	}
}

// TestLegacyEncoding: handlers see UTF-8 whatever the document is, and the
// output goes back in the document's encoding. A rewrite of a windows-1252 page
// must not turn into UTF-8 halfway through.
func TestLegacyEncoding(t *testing.T) {
	// 0xE9 is é in windows-1252 and invalid alone in UTF-8.
	in := "<p title=\"caf\xe9\"><img src=\"http://cdn.example/caf\xe9.png\"></p>"
	var out bytes.Buffer
	u := &upgrader{encoding: "windows-1252", skip: map[string]bool{}}
	if err := u.run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}

	got := out.Bytes()
	if bytes.Contains(got, []byte{0xc3, 0xa9}) {
		t.Errorf("output was re-encoded to UTF-8: % x", got)
	}
	if !bytes.Contains(got, []byte("https://cdn.example/caf\xe9.png")) {
		t.Errorf("the URL was not upgraded in place: % x", got)
	}
	if n := bytes.Count(got, []byte{0xe9}); n != 2 {
		t.Errorf("expected both 0xe9 bytes to survive, found %d: % x", n, got)
	}
}

func TestUnknownEncodingIsReportedWithItsLabel(t *testing.T) {
	u := &upgrader{encoding: "not-an-encoding", skip: map[string]bool{}}
	err := u.run(strings.NewReader(`<p>x</p>`), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not-an-encoding") {
		t.Errorf("error does not name the label: %v", err)
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := upgradeString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, u, err := upgradeString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if len(u.upgraded) != 0 {
			t.Errorf("second pass of %q upgraded %v", doc, u.upgraded)
		}
	}
}

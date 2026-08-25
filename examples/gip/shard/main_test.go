package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var hosts = []string{"static0.example", "static1.example", "static2.example"}

func std() Options { return Options{Hosts: hosts, Scheme: "https"} }

func shard(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Shard(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Shard(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheSamePathAlwaysGetsTheSameHost, which is the entire contract.
func TestTheSamePathAlwaysGetsTheSameHost(t *testing.T) {
	first := Host(hosts, "/i/a.png")
	for i := 0; i < 100; i++ {
		if got := Host(hosts, "/i/a.png"); got != first {
			t.Fatalf("call %d gave %q, want %q", i, got, first)
		}
	}
	// The host is a function of the path and nothing else: not of the document, not
	// of the order, not of the chunking.
	for _, doc := range []string{
		`<img src="/i/a.png">`,
		`<p>x</p><img src="/i/a.png">`,
		`<img src="/i/z.png"><img src="/i/a.png">`,
	} {
		got, _ := shard(t, doc, std())
		if !strings.Contains(got, "https://"+first+"/i/a.png") {
			t.Errorf("%q gave %q, want the host %q", doc, got, first)
		}
	}
	// And a query or a fragment does not change it, since neither is part of the
	// file being cached.
	for _, suffix := range []string{"", "?v=1", "#frag", "?v=1#frag"} {
		got, _ := shard(t, `<img src="/i/a.png`+suffix+`">`, std())
		if !strings.Contains(got, "https://"+first+"/i/a.png"+suffix) {
			t.Errorf("%q gave %q", suffix, got)
		}
	}
}

// TestTheHostsAreUsed, all of them, and only them.
func TestTheHostsAreUsed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h"} {
		h := Host(hosts, p)
		if !contains(hosts, h) {
			t.Fatalf("%q gave %q, which is not one of the hosts", p, h)
		}
		seen[h] = true
	}
	if len(seen) != len(hosts) {
		t.Errorf("eight paths landed on %d of %d hosts: %v", len(seen), len(hosts), seen)
	}
	// One host is a legitimate configuration and puts everything on it.
	one := Options{Hosts: []string{"cdn.example"}, Scheme: "https"}
	got, res := shard(t, `<img src="/a.png"><img src="/b.png">`, one)
	if strings.Count(got, "cdn.example") != 2 || res.Sharded != 2 {
		t.Errorf("got %q, %v", got, res)
	}
	// No hosts at all changes nothing rather than dividing by zero.
	got, res = shard(t, `<img src="/a.png">`, Options{Scheme: "https"})
	if got != `<img src="/a.png">` || res.Sharded != 0 {
		t.Errorf("got %q, %v", got, res)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestEveryPlaceAnAssetIsNamed.
func TestEveryPlaceAnAssetIsNamed(t *testing.T) {
	for _, tc := range []struct{ doc, attr string }{
		{`<script src="/js/app.js"></script>`, "src"},
		{`<link href="/css/site.css">`, "href"},
		{`<img src="/i/a.png">`, "src"},
		{`<video poster="/i/a.png"></video>`, "poster"},
		{`<object data="/i/a.png"></object>`, "data"},
		{`<track src="/i/a.png">`, "src"},
	} {
		got, res := shard(t, tc.doc, std())
		if !strings.Contains(got, "//static") {
			t.Errorf("%q was not sharded: %q", tc.doc, got)
		}
		if res.Sharded != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
	// A link that is not an asset is not an asset.
	for _, doc := range []string{`<a href="/page">x</a>`, `<div data-src="/i/a.png"></div>`} {
		if got, _ := shard(t, doc, std()); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
}

// TestASrcsetIsShardedMemberByMember, each by its own path, because the browser
// picks one member and that member is what gets cached.
func TestASrcsetIsShardedMemberByMember(t *testing.T) {
	got, res := shard(t, `<img srcset="/i/a.png 1x, /i/b.png 2x">`, std())
	want := `<img srcset="https://` + Host(hosts, "/i/a.png") + `/i/a.png 1x, https://` +
		Host(hosts, "/i/b.png") + `/i/b.png 2x">`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Sharded != 2 {
		t.Errorf("%v", res)
	}
	// A comma inside a URL is part of it.
	got, _ = shard(t, `<img srcset="/i/c,d.png 2x">`, std())
	if !strings.Contains(got, "/i/c,d.png 2x") {
		t.Errorf("got %q", got)
	}
}

// TestAnAbsoluteURLIsSomebodyElsesDecision, and one already on a shard is left where
// it is so that a second run changes nothing.
func TestAnAbsoluteURLIsSomebodyElsesDecision(t *testing.T) {
	for _, tc := range []struct {
		doc    string
		reason func(Result) int
	}{
		{`<img src="https://other/x.png">`, func(r Result) int { return r.Absolute }},
		{`<img src="//other/x.png">`, func(r Result) int { return r.Absolute }},
		{`<img src="data:image/gif;base64,R0lGOD">`, func(r Result) int { return r.Absolute }},
		{`<img src="https://static1.example/i/z.png">`, func(r Result) int { return r.Already }},
		{`<img src="//static2.example/i/z.png">`, func(r Result) int { return r.Already }},
		{`<img src="https://STATIC1.EXAMPLE/i/z.png">`, func(r Result) int { return r.Already }},
	} {
		got, res := shard(t, tc.doc, std())
		if got != tc.doc {
			t.Errorf("%q was rewritten to %q", tc.doc, got)
		}
		if tc.reason(res) != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestTheSchemeIsTheCallers, including the protocol-relative form.
func TestTheSchemeIsTheCallers(t *testing.T) {
	opts := std()
	opts.Scheme = ""
	got, _ := shard(t, `<img src="/i/a.png">`, opts)
	if want := `<img src="//` + Host(hosts, "/i/a.png") + `/i/a.png">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	opts.Scheme = "http"
	got, _ = shard(t, `<img src="/i/a.png">`, opts)
	if !strings.HasPrefix(got, `<img src="http://`) {
		t.Errorf("got %q", got)
	}
}

// TestARelativePathIsMadeAbsolute, which is the one guess this program does make -
// and it makes it visible in the output rather than silently.
func TestARelativePathIsMadeAbsolute(t *testing.T) {
	got, _ := shard(t, `<img src="i/a.png">`, std())
	if !strings.Contains(got, "/i/a.png") {
		t.Errorf("got %q", got)
	}
	// The host comes from the path as written, so the two spellings can differ - a
	// document mixing them gets two hosts for one file, which the report shows.
	a, b := Host(hosts, "i/a.png"), Host(hosts, "/i/a.png")
	if a == b {
		t.Skip("these two spellings happen to hash to the same host")
	}
	got, res := shard(t, `<img src="i/a.png"><img src="/i/a.png">`, std())
	if strings.Count(got, "//static") != 2 {
		t.Errorf("got %q", got)
	}
	if len(res.PerHost) != 2 {
		t.Errorf("%v: the same file on one host from two spellings", res)
	}
}

// TestShardingTwiceChangesNothing.
func TestShardingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<img src="/i/a.png">`,
		`<img srcset="/i/a.png 1x, /i/b.png 2x">`,
		`<script src="/js/app.js"></script>`,
		`<img src="https://other/x.png">`,
	} {
		once, _ := shard(t, doc, std())
		twice, res := shard(t, once, std())
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Sharded != 0 {
			t.Errorf("%q: the second pass moved %d", doc, res.Sharded)
		}
	}
}

// TestPositionIsNotUsedAndCouldNotBe: the reason the shard comes from the path is
// partly that a positional selector does not mean what it looks like. In a list
// written without end tags there is no second child at all.
func TestPositionIsNotUsedAndCouldNotBe(t *testing.T) {
	const implied = `<ul><li><img src="/i/a.png"><li><img src="/i/b.png"></ul>`
	// The rewrite does not care how the list is written.
	got, res := shard(t, implied, std())
	if res.Sharded != 2 {
		t.Errorf("%v", res)
	}
	if !strings.Contains(got, Host(hosts, "/i/a.png")) || !strings.Contains(got, Host(hosts, "/i/b.png")) {
		t.Errorf("got %q", got)
	}
	// And the positional selectors that would have been the alternative count the
	// tokens rather than the tree. The same list with its end tags spelled gives
	// different answers from the same list without them.
	const closed = `<ul><li><img src="/i/a.png"></li><li><img src="/i/b.png"></li></ul>`
	for _, tc := range []struct {
		sel                  string
		wantClosed, wantOpen int
	}{
		{"ul > li", 2, 1},
		{"li > li", 0, 1},
		{"ul > li:nth-child(2)", 1, 0},
	} {
		for _, c := range []struct {
			doc  string
			want int
		}{{closed, tc.wantClosed}, {implied, tc.wantOpen}} {
			n := 0
			if _, err := lolhtml.RewriteString(c.doc,
				lolhtml.OnElement(tc.sel, func(*lolhtml.Element) error { n++; return nil }),
			); err != nil {
				t.Fatal(err)
			}
			if n != c.want {
				t.Errorf("%q matched %d times in %q, want %d", tc.sel, n, c.doc, c.want)
			}
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<script src="/js/app.js"></script><img srcset="/i/a.png 1x, /i/c,d.png 2x"><img src="https://other/x.png"><link href="/css/site.css?v=1">`
	want, wantRes := shard(t, doc, std())
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		s := &sharder{opts: std()}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, s.options()...)
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
		if s.res.Sharded != wantRes.Sharded {
			t.Errorf("chunks of %d: %v, want %v", size, s.res, wantRes)
		}
	}
}

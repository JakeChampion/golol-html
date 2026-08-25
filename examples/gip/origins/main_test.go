package main

import (
	"net/url"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = "https://site.example/docs/index.html"

func report(t *testing.T, doc string, opts Options) Report {
	t.Helper()
	rep, err := Origins(strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Origins(%q): %v", doc, err)
	}
	return rep
}

func find(r Report, name string) *Origin {
	for i := range r.Origins {
		if r.Origins[i].Name == name {
			return &r.Origins[i]
		}
	}
	return nil
}

// TestEveryPlaceAURLCanHide.
func TestEveryPlaceAURLCanHide(t *testing.T) {
	for _, tc := range []struct {
		doc, trigger string
		kind         Kind
	}{
		{`<img src="https://a.example/i.png">`, "img", Fetched},
		{`<img srcset="https://a.example/i.png 2x">`, "img[srcset]", Fetched},
		{`<script src="https://a.example/s.js"></script>`, "script", Fetched},
		{`<iframe src="https://a.example/f"></iframe>`, "iframe", Fetched},
		{`<video poster="https://a.example/p.jpg"></video>`, "video", Fetched},
		{`<object data="https://a.example/o"></object>`, "object", Fetched},
		{`<embed src="https://a.example/e">`, "embed", Fetched},
		{`<track src="https://a.example/t.vtt">`, "track", Fetched},
		{`<svg><use xlink:href="https://a.example/i.svg#a"/></svg>`, "use", Fetched},
		{`<link rel="stylesheet" href="https://a.example/s.css">`, "link[stylesheet]", Fetched},
		{`<link rel="preconnect" href="https://a.example">`, "link[preconnect]", Hinted},
		{`<link rel="dns-prefetch" href="https://a.example">`, "link[dns-prefetch]", Hinted},
		{`<link rel="canonical" href="https://a.example/c">`, "link[canonical]", Navigated},
		{`<a href="https://a.example/p">x</a>`, "a", Navigated},
		{`<area href="https://a.example/p">`, "area", Navigated},
		{`<form action="https://a.example/go"></form>`, "form", Navigated},
		{`<button formaction="https://a.example/go"></button>`, "button", Navigated},
		{`<blockquote cite="https://a.example/c"></blockquote>`, "blockquote", Navigated},
		{`<div style="background:url(https://a.example/bg.png)"></div>`, "style", Fetched},
		{`<style>.a{background:url(https://a.example/bg.png)}</style>`, "css", Fetched},
		{`<style>@import "https://a.example/f.css";</style>`, "css[@import]", Fetched},
	} {
		rep := report(t, tc.doc, Options{Page: page})
		o := find(rep, "https://a.example")
		if o == nil {
			t.Errorf("%q: the origin was not reported at all: %v", tc.doc, rep.Origins)
			continue
		}
		if o.Triggers[tc.trigger] != 1 {
			t.Errorf("%q: triggers are %v, want %q", tc.doc, o.Triggers, tc.trigger)
		}
		if o.Kinds[tc.kind] != 1 {
			t.Errorf("%q: kinds are %v, want %v", tc.doc, o.Kinds, tc.kind)
		}
		if o.First {
			t.Errorf("%q: reported as first-party", tc.doc)
		}
	}
}

// TestRelativeURLsAreTheFirstParty, which is what makes the third-party list mean
// something.
func TestRelativeURLsAreTheFirstParty(t *testing.T) {
	const doc = `<img src="/i/a.png"><script src="s.js"></script><a href="../p">x</a>`
	rep := report(t, doc, Options{Page: page})
	o := find(rep, "https://site.example")
	if o == nil || !o.First {
		t.Fatalf("the page's origin is not in %v", rep.Origins)
	}
	if o.Requests != 3 {
		t.Errorf("%v", *o)
	}
	if len(rep.ThirdParty()) != 0 {
		t.Errorf("third parties: %v", rep.ThirdParty())
	}
	// A protocol-relative URL takes the page's scheme.
	rep = report(t, `<img src="//img.example/a.png">`, Options{Page: page})
	if find(rep, "https://img.example") == nil {
		t.Errorf("%v", rep.Origins)
	}
	// Without a page URL, relative URLs are not attributed to anything.
	rep = report(t, doc, Options{})
	if o := find(rep, "(relative)"); o == nil || o.Requests != 3 {
		t.Errorf("%v", rep.Origins)
	}
}

// TestABaseHrefMovesTheFirstParty, the way it does in a browser.
func TestABaseHrefMovesTheFirstParty(t *testing.T) {
	rep := report(t, `<base href="https://cdn.example/assets/"><img src="a.png">`, Options{Page: page})
	if o := find(rep, "https://cdn.example"); o == nil || o.Requests != 1 {
		t.Errorf("%v", rep.Origins)
	}
	if o := find(rep, "https://cdn.example"); o != nil && o.First {
		t.Error("the base's origin was reported as first-party")
	}
	if rep.Base != "https://cdn.example/assets/" {
		t.Errorf("Base = %q", rep.Base)
	}
	// A relative base resolves against the page.
	rep = report(t, `<base href="/assets/"><img src="a.png">`, Options{Page: page})
	if o := find(rep, "https://site.example"); o == nil || !o.First {
		t.Errorf("%v", rep.Origins)
	}
	// Only the first base counts.
	rep = report(t, `<base href="https://a.example/"><base href="https://b.example/"><img src="x.png">`,
		Options{Page: page})
	if find(rep, "https://a.example") == nil || find(rep, "https://b.example") != nil {
		t.Errorf("%v", rep.Origins)
	}
}

// TestTheShapesThatAreNotOrigins are reported as themselves rather than dropped or
// guessed at.
func TestTheShapesThatAreNotOrigins(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{`data:image/gif;base64,R0lGOD`, "(data)"},
		{`blob:https://site.example/abc`, "(blob)"},
		{`javascript:void(0)`, "(javascript)"},
		{`mailto:a@b.example`, "(mailto)"},
		{`tel:+123`, "(tel)"},
		{`about:blank`, "(about)"},
	} {
		rep := report(t, `<a href="`+tc.url+`">x</a>`, Options{Page: page})
		if o := find(rep, tc.want); o == nil || o.Requests != 1 {
			t.Errorf("%q gave %v, want %q", tc.url, rep.Origins, tc.want)
		}
	}
	// A fragment is not a request at all.
	rep := report(t, `<a href="#top">x</a>`, Options{Page: page})
	if len(rep.Origins) != 0 {
		t.Errorf("%v", rep.Origins)
	}
	// Nor is an empty or blank value.
	rep = report(t, `<img src=""><img src="   ">`, Options{Page: page})
	if len(rep.Origins) != 0 {
		t.Errorf("%v", rep.Origins)
	}
}

// TestThirdPartyIsWhatTheBrowserWouldFetch: an origin that only appears as a
// navigation is not a third party the page loaded.
func TestThirdPartyIsWhatTheBrowserWouldFetch(t *testing.T) {
	const doc = `<script src="https://track.example/t.js"></script>` +
		`<a href="https://link.example/p">x</a>` +
		`<link rel="preconnect" href="https://hint.example">`
	rep := report(t, doc, Options{Page: page})
	third := rep.ThirdParty()
	if len(third) != 1 || third[0].Name != "https://track.example" {
		t.Errorf("third parties are %v, want only track.example", third)
	}
	// The others are still in the report, which is the difference between
	// classifying and filtering.
	for _, name := range []string{"https://link.example", "https://hint.example"} {
		if find(rep, name) == nil {
			t.Errorf("%s is missing from %v", name, rep.Origins)
		}
	}
}

// TestRequestsAreCountedPerAppearance, since one origin appearing forty times is a
// different page from one appearing once.
func TestRequestsAreCountedPerAppearance(t *testing.T) {
	const doc = `<img src="https://a.example/1.png"><img src="https://a.example/2.png">` +
		`<script src="https://a.example/s.js"></script>`
	rep := report(t, doc, Options{Page: page})
	o := find(rep, "https://a.example")
	if o == nil || o.Requests != 3 {
		t.Fatalf("%v", rep.Origins)
	}
	if o.Triggers["img"] != 2 || o.Triggers["script"] != 1 {
		t.Errorf("%v", o.Triggers)
	}
	// The report is ordered: first-party first, then by request count.
	const busy = `<img src="/a.png"><script src="https://b.example/1.js"></script>` +
		`<script src="https://b.example/2.js"></script><script src="https://c.example/3.js"></script>`
	rep = report(t, busy, Options{Page: page})
	if len(rep.Origins) != 3 {
		t.Fatalf("%v", rep.Origins)
	}
	if !rep.Origins[0].First || rep.Origins[1].Name != "https://b.example" {
		t.Errorf("order is %v", rep.Origins)
	}
}

// TestTheShapeIsOneHandler, which is the measured shape for a report-only rewrite: a
// selector list costs what one selector costs to register, while a handler per element
// costs per handler, and a wide selector pays for every element in the document. See
// reportshape_test.go in the library.
func TestTheShapeIsOneHandler(t *testing.T) {
	r := &reporter{opts: Options{}, origins: map[string]*Origin{}}
	if got := len(r.options()); got != 2 {
		t.Errorf("the reporter registers %d handlers, want 2: one selector list for the "+
			"elements and one for stylesheet text", got)
	}
	// The list names elements rather than matching everything.
	if strings.Contains(Elements, "*") {
		t.Errorf("the selector list matches everything: %q", Elements)
	}
	if n := strings.Count(Elements, ",") + 1; n < 15 {
		t.Errorf("the list has %d clauses, which is fewer than the places a URL hides", n)
	}
	// Every element the program knows about is in the list, so nothing is looked for
	// that cannot be matched.
	for tag := range Sources {
		if !strings.Contains(Elements, tag) {
			t.Errorf("%q is in Sources and not in the selector list", tag)
		}
	}
}

// TestTheReportSurvivesChunkBoundaries, including a stylesheet split across writes.
func TestTheReportSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<link rel="preconnect" href="https://cdn.example"><img srcset="//img.example/b.png 2x, /c.png 1x">` +
		`<style>@import "https://fonts.example/f.css"; .a{background:url(https://cdn.example/bg.png)}</style>` +
		`<a href="https://other.example/p">x</a>`
	want := report(t, doc, Options{Page: page})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		r := &reporter{opts: Options{Page: page}, origins: map[string]*Origin{}}
		if u := mustParse(t, page); u != nil {
			r.page = u
		}
		w, err := lolhtml.NewWriter(discard{}, r.options()...)
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
		got := r.report()
		if len(got.Origins) != len(want.Origins) {
			t.Fatalf("chunks of %d: %v, want %v", size, got.Origins, want.Origins)
		}
		for i := range got.Origins {
			if got.Origins[i].Name != want.Origins[i].Name ||
				got.Origins[i].Requests != want.Origins[i].Requests {
				t.Errorf("chunks of %d: %v, want %v", size, got.Origins, want.Origins)
				break
			}
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

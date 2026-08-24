package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<a href="/x?utm_source=n&utm_medium=e&id=42">t</a>`,
	`<a href="/y?fbclid=abc">t</a>`,
	`<a href="/z?a=1&amp;utm_campaign=s&amp;b=2">t</a>`,
	`<a href="https://o.example/p?gclid=1&q=2">t</a>`,
	`<img src="https://www.facebook.com/tr?id=1&ev=PageView" width="1">`,
	`<iframe src="https://www.googletagmanager.com/ns.html?id=GTM-X"></iframe>`,
	`<img src="/real/photo.jpg?utm_source=x">`,
	`<a href="/keep?page=2">t</a>`,
	`<a href="/none">t</a>`,
	`<a href="">t</a><a>t</a>`,
	`<a href="/q?utm_source=">empty value</a>`,
	`<a href="/q?UTM_SOURCE=upper">upper</a>`,
	`<a href="/q?utm_source=a&utm_source=b">repeated</a>`,
	`<a href="/q?only#frag">fragment</a>`,
	`<a href="/q?utm_source=x#frag">tracker plus fragment</a>`,
	`<a href="mailto:a@b.c?utm_source=x">mailto</a>`,
	`<a href="/a%zz?utm_source=x">unparseable</a>`,
	`<!DOCTYPE html><html><body><p>plain</p></body></html>`,
	`<div><img src="https://google-analytics.com/collect?v=1"></div>`,
	`<form action="/s?utm_id=1"><input formaction="/t?gclid=2"></form>`,
	`<video poster="/p.jpg?utm_term=x"><source src="/v.mp4?utm_content=y"></video>`,
	`<!-- google-analytics.com in a comment -->`,
	``,
}

func chunked(in string, n int, s *stripper) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, s.options()...)
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

// TestChunkInvariance: removal and attribute rewriting both happen while the
// rewriter is buffering a start tag, which is exactly where a chunk boundary
// can land. If any of it depended on the whole tag arriving in one write, it
// breaks here.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := stripString(doc)
		if err != nil {
			t.Fatalf("whole write of %q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, newStripper())
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

// TestIdempotent: a URL with its trackers removed must be a fixed point, or a
// pipeline that runs the filter twice would keep editing.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := stripString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, s, err := stripString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if s.urlsChanged != 0 {
			t.Errorf("second pass of %q changed %d URL(s)", doc, s.urlsChanged)
		}
	}
}

// TestNonTrackingParametersSurvive is the property that decides whether this
// program is safe to run blind: it must never drop a parameter that addresses
// the resource.
func TestNonTrackingParametersSurvive(t *testing.T) {
	tests := []struct{ in, want string }{
		{`<a href="/x?id=42&utm_source=n">t</a>`, `<a href="/x?id=42">t</a>`},
		{`<a href="/x?utm_source=n&id=42">t</a>`, `<a href="/x?id=42">t</a>`},
		{`<a href="/x?page=2">t</a>`, `<a href="/x?page=2">t</a>`},
		{`<a href="/x?utm_source=n">t</a>`, `<a href="/x">t</a>`},
		{`<a href="/x?utm_source=n#top">t</a>`, `<a href="/x#top">t</a>`},
		{`<a href="/x?a=1&amp;utm_source=n&amp;b=2">t</a>`, `<a href="/x?a=1&amp;b=2">t</a>`},
		{`<a href="/x?q=utm_source">t</a>`, `<a href="/x?q=utm_source">t</a>`},
		{`<a href="/x?notutm_source=1">t</a>`, `<a href="/x?notutm_source=1">t</a>`},
	}
	for _, tt := range tests {
		got, _, err := stripString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("%s\n got: %s\nwant: %s", tt.in, got, tt.want)
		}
	}
}

// TestEncodedAmpersandFormIsPreserved: attribute values are raw source, so a
// document written with &amp; must come back with &amp;. Emitting a bare & would
// change the query the browser sends.
func TestEncodedAmpersandFormIsPreserved(t *testing.T) {
	got, _, err := stripString(`<a href="/x?a=1&amp;utm_source=n&amp;b=2">t</a>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "?a=1&b=2") {
		t.Errorf("&amp; was decoded to a bare &: %s", got)
	}
	if want := `<a href="/x?a=1&amp;b=2">t</a>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

// TestUnparseableURLIsLeftAlone: guessing at the query string of something
// malformed is how a rewriter breaks a page.
func TestUnparseableURLIsLeftAlone(t *testing.T) {
	in := `<a href="/a%zz?utm_source=x">t</a>`
	got, s, err := stripString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("changed an unparseable URL:\n got: %s\nwant: %s", got, in)
	}
	if s.urlsChanged != 0 {
		t.Errorf("urlsChanged=%d, want 0", s.urlsChanged)
	}
}

// TestPixelsAreRemovedAndCountedOnce. The count matters as much as the removal:
// this is the number the program reports, and a handler that also runs on an
// element another handler removed would inflate it.
func TestPixelsAreRemovedAndCountedOnce(t *testing.T) {
	in := `<p>a</p><img src="https://www.facebook.com/tr?id=1"><p>b</p>` +
		`<iframe src="https://www.googletagmanager.com/ns.html?id=X"></iframe>`
	got, s, err := stripString(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>a</p><p>b</p>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if len(s.removedPixels) != 2 {
		t.Errorf("removedPixels=%d, want 2", len(s.removedPixels))
	}
	if s.urlsSeen != 0 {
		t.Errorf("urlsSeen=%d, want 0: a removed element is not a URL this program rewrote", s.urlsSeen)
	}
}

// TestMarkerCannotInjectMarkup: the marker carries a URL from the document, so
// it is untrusted, and lolhtml.Text has to make it inert.
func TestMarkerCannotInjectMarkup(t *testing.T) {
	in := `<img src="https://google-analytics.com/x?<script>alert(1)</script>">`
	got, _, err := stripString(in, func(s *stripper) { s.mark = true })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("marker injected markup: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("marker is not escaped: %s", got)
	}
}

func TestWildcardParameterMatching(t *testing.T) {
	got, _, err := stripString(`<a href="/x?utm_weird=1&keep=2">t</a>`, func(s *stripper) {
		s.params = map[string]bool{"utm_*": true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<a href="/x?keep=2">t</a>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

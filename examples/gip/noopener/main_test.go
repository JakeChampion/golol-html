package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<a href="/a" target="_blank">a</a>`,
	`<a href="/b" target="_blank" rel="nofollow">b</a>`,
	`<a href="/c" target="_blank" rel="noreferrer">c</a>`,
	`<a href="/d" target="_blank" rel="NOREFERRER">d</a>`,
	`<a href="/e" target="_blank" rel="noopener">e</a>`,
	`<a href="/f" target="_self">f</a>`,
	`<a href="/g" target="_parent">g</a>`,
	`<a href="/h" target="_top">h</a>`,
	`<a href="/i" target="">i</a>`,
	`<a href="/j" target="named">j</a>`,
	`<a href="/k" target="_BLANK">k</a>`,
	`<a href="/l">l</a>`,
	`<area href="/m" target="_blank">`,
	`<form action="/n" target="_blank"></form>`,
	`<button onclick="window.open('/o')">o</button>`,
	`<a href="/p" target="_blank" rel="nofollow sponsored ugc">p</a>`,
	`<a href="/q" target="_blank" rel="  nofollow   ">q</a>`,
	`<!DOCTYPE html><html><body><a href="/r" target="_blank">r</a></body></html>`,
	`<p>no links</p>`,
	``,
}

func chunked(in string, n int, h *hardener) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, h.options()...)
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
		whole, _, err := hardenString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &hardener{noReferrer: true, graceful: true})
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
		once, _, err := hardenString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, h, err := hardenString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if h.hardened != 0 && !strings.Contains(once, "<form") {
			t.Errorf("second pass of %q hardened %d link(s)", doc, h.hardened)
		}
	}
}

// TestExistingRelTokensAreKept is the property that makes this safe on stored
// content. rel carries tokens this program knows nothing about, and dropping
// nofollow or sponsored changes what a search engine does with the link.
func TestExistingRelTokensAreKept(t *testing.T) {
	tests := []struct{ in, want string }{
		{`<a target="_blank">x</a>`, `<a target="_blank" rel="noopener noreferrer">x</a>`},
		{`<a target="_blank" rel="nofollow">x</a>`, `<a target="_blank" rel="nofollow noopener noreferrer">x</a>`},
		{`<a target="_blank" rel="nofollow sponsored ugc">x</a>`, `<a target="_blank" rel="nofollow sponsored ugc noopener noreferrer">x</a>`},
		// noreferrer implies noopener, so a list that has it needs nothing.
		{`<a target="_blank" rel="noreferrer">x</a>`, `<a target="_blank" rel="noreferrer">x</a>`},
		{`<a target="_blank" rel="NOREFERRER">x</a>`, `<a target="_blank" rel="NOREFERRER">x</a>`},
		// noopener does not imply noreferrer, so that one is still added.
		{`<a target="_blank" rel="noopener">x</a>`, `<a target="_blank" rel="noopener noreferrer">x</a>`},
		// Existing case and order are preserved; only new tokens are appended.
		{`<a target="_blank" rel="NoFollow">x</a>`, `<a target="_blank" rel="NoFollow noopener noreferrer">x</a>`},
		// Whitespace is normalised, which is the one visible change to the list.
		{`<a target="_blank" rel="  nofollow   ">x</a>`, `<a target="_blank" rel="nofollow noopener noreferrer">x</a>`},
	}
	for _, tt := range tests {
		got, _, err := hardenString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("%s\n got: %s\nwant: %s", tt.in, got, tt.want)
		}
	}
}

// TestTargetsThatReuseAContextAreLeftAlone: _self, _parent and _top navigate an
// existing context, so there is no opener to sever and no reason to touch them.
func TestTargetsThatReuseAContextAreLeftAlone(t *testing.T) {
	for _, target := range []string{"_self", "_parent", "_top", "", "  "} {
		in := `<a href="/x" target="` + target + `">x</a>`
		got, h, err := hardenString(in)
		if err != nil {
			t.Fatalf("%q: %v", target, err)
		}
		if got != in {
			t.Errorf("target=%q was rewritten:\n got: %s\nwant: %s", target, got, in)
		}
		if h.hardened != 0 {
			t.Errorf("target=%q: hardened=%d, want 0", target, h.hardened)
		}
	}
}

// TestNamedAndUppercaseTargets: target matching is case insensitive, and a named
// target can carry an opener whether or not a context with that name exists.
func TestNamedAndUppercaseTargets(t *testing.T) {
	for _, target := range []string{"_blank", "_BLANK", "_Blank", " _blank ", "named", "sidebar"} {
		in := `<a href="/x" target="` + target + `">x</a>`
		got, h, err := hardenString(in)
		if err != nil {
			t.Fatalf("%q: %v", target, err)
		}
		if !strings.Contains(got, `rel="noopener noreferrer"`) {
			t.Errorf("target=%q was not hardened: %s", target, got)
		}
		if h.hardened != 1 {
			t.Errorf("target=%q: hardened=%d, want 1", target, h.hardened)
		}
	}
}

// TestFormsAreReportedNotRewritten: a form has no rel attribute, so a
// target="_blank" form cannot be hardened in markup. Reporting it is the only
// honest option.
func TestFormsAreReportedNotRewritten(t *testing.T) {
	in := `<form action="/x" target="_blank"></form>`
	got, h, err := hardenString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("a form was rewritten:\n got: %s", got)
	}
	if h.hardened != 1 {
		t.Errorf("hardened=%d, want 1 (counted, not changed)", h.hardened)
	}
}

func TestNoReferrerCanBeTurnedOff(t *testing.T) {
	got, _, err := hardenString(`<a target="_blank">x</a>`, func(h *hardener) {
		h.noReferrer = false
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<a target="_blank" rel="noopener">x</a>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

// TestGracefulBailOutIsReportedNotSwallowed. With the graceful setting the
// response is intact, so run returns no error - but the tail was not hardened,
// which is a security-relevant outcome and has to reach the caller somehow.
func TestGracefulBailOutIsReportedNotSwallowed(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(`<a href="/x" target="_blank">l</a>`)
	}
	b.WriteString(`<a ` + strings.Repeat(`data-x="y" `, 400) + `target="_blank">f</a>`)
	doc := b.String()

	// 512, not 1024: io.Copy reads this whole document in one go and makes a
	// single Write, and a single write completes at 1024. Sizing a limit against
	// the wrong write pattern is exactly the trap the library's own README now
	// warns about, and this test walked into it first.
	var out bytes.Buffer
	h := &hardener{noReferrer: true, limit: 512, graceful: true}
	err := h.run(strings.NewReader(doc), &out)

	if err != nil {
		t.Fatalf("graceful bail-out should not surface as an error: %v", err)
	}
	if !h.bailedOut {
		t.Fatal("the bail-out was not recorded")
	}
	if !strings.Contains(h.report(), "WARNING") {
		t.Errorf("the report does not warn about the unhardened tail:\n%s", h.report())
	}

	// And without the graceful setting it is a hard failure, because the
	// response itself is truncated.
	var out2 bytes.Buffer
	h2 := &hardener{noReferrer: true, limit: 512, graceful: false}
	if err := h2.run(strings.NewReader(doc), &out2); err == nil {
		t.Error("a non-graceful bail-out should surface as an error")
	}
	if !h2.bailedOut {
		t.Error("the bail-out was not recorded")
	}
	if out2.Len() >= len(doc) {
		t.Errorf("expected a truncated response, got %d of %d bytes", out2.Len(), len(doc))
	}
}

func TestMergeTokens(t *testing.T) {
	for _, tt := range []struct {
		have  string
		want  []string
		out   string
		added int
	}{
		{"", []string{"noopener"}, "noopener", 1},
		{"noopener", []string{"noopener"}, "noopener", 0},
		{"a b", []string{"noopener", "noreferrer"}, "a b noopener noreferrer", 2},
		{"noreferrer", []string{"noopener", "noreferrer"}, "noreferrer", 0},
		{"noopener", []string{"noopener", "noreferrer"}, "noopener noreferrer", 1},
		{"NOOPENER", []string{"noopener"}, "NOOPENER", 0},
	} {
		got, added := mergeTokens(tt.have, tt.want)
		if got != tt.out || added != tt.added {
			t.Errorf("mergeTokens(%q, %v) = (%q, %d), want (%q, %d)",
				tt.have, tt.want, got, added, tt.out, tt.added)
		}
	}
}

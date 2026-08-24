package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<iframe src="https://v.example/e"></iframe>`,
	`<iframe src="https://v.example/e" sandbox=""></iframe>`,
	`<iframe src="https://v.example/e" sandbox="allow-forms"></iframe>`,
	`<iframe src="https://v.example/e" sandbox="allow-scripts allow-same-origin"></iframe>`,
	`<iframe src="https://v.example/e" sandbox="ALLOW-SCRIPTS ALLOW-SAME-ORIGIN"></iframe>`,
	`<iframe src="https://v.example/e" referrerpolicy="origin"></iframe>`,
	`<iframe src="/local"></iframe>`,
	`<iframe srcdoc="<p>x</p>"></iframe>`,
	`<iframe srcdoc="<p>x</p>" src="https://v.example/y"></iframe>`,
	`<iframe></iframe>`,
	`<div><iframe src="https://v.example/e"></iframe></div>`,
	`<p>no iframes</p>`,
	``,
}

func chunked(in string, n int, h *hardener) (string, error) {
	var out strings.Builder
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

func newHardener() *hardener {
	return &hardener{
		tokens: []string{"allow-scripts", "allow-popups", "allow-forms"},
		policy: "no-referrer",
		keep:   map[string]bool{},
	}
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := hardenString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, newHardener())
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
		if h.sandboxed != 0 {
			t.Errorf("second pass of %q sandboxed %d", doc, h.sandboxed)
		}
	}
}

// TestTheDefeatingCombinationIsNeverWritten is the judgement this program exists
// to encode. A frame with allow-scripts and allow-same-origin can reach into its
// own document and remove the sandbox attribute, so the pair is no safer than no
// sandbox at all.
func TestTheDefeatingCombinationIsNeverWritten(t *testing.T) {
	for _, doc := range corpus {
		got, _, err := hardenString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		// Only where this program wrote the sandbox: an author's own is left
		// alone and reported instead.
		if !strings.Contains(doc, "sandbox") && strings.Contains(got, "allow-same-origin") {
			t.Errorf("%q: wrote allow-same-origin alongside allow-scripts: %s", doc, got)
		}
	}

	if !defeats([]string{"allow-scripts", "allow-same-origin"}) {
		t.Error("defeats() does not recognise the pair")
	}
	if !defeats([]string{"ALLOW-SAME-ORIGIN", "allow-forms", "Allow-Scripts"}) {
		t.Error("defeats() is case sensitive")
	}
	for _, safe := range [][]string{
		{"allow-scripts"},
		{"allow-same-origin"},
		{"allow-forms", "allow-popups"},
		{},
	} {
		if defeats(safe) {
			t.Errorf("defeats(%v) = true", safe)
		}
	}
}

// TestAnAuthorsSandboxIsReportedNotCorrected. Tightening a sandbox breaks embeds
// that were working, and which token an embed needs is not knowable from here -
// so the dangerous combination is surfaced rather than edited.
func TestAnAuthorsSandboxIsReportedNotCorrected(t *testing.T) {
	in := `<iframe src="https://v.example/e" sandbox="allow-scripts allow-same-origin"></iframe>`
	got, h, err := hardenString(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `sandbox="allow-scripts allow-same-origin"`) {
		t.Errorf("the author's sandbox was edited: %s", got)
	}
	if len(h.defeated) != 1 {
		t.Fatalf("defeated=%v, want one entry", h.defeated)
	}
	if !strings.Contains(h.report(), "sandbox defeated") {
		t.Errorf("the report does not explain it:\n%s", h.report())
	}
	if h.keptOwn != 1 || h.sandboxed != 0 {
		t.Errorf("keptOwn=%d sandboxed=%d, want 1 and 0", h.keptOwn, h.sandboxed)
	}
}

// TestAnEmptySandboxIsTheStrictestOne, so it must be kept rather than replaced
// with something more permissive.
func TestAnEmptySandboxIsTheStrictestOne(t *testing.T) {
	in := `<iframe src="https://v.example/e" sandbox=""></iframe>`
	got, h, err := hardenString(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "allow-") {
		t.Errorf("an empty sandbox was loosened: %s", got)
	}
	if h.keptOwn != 1 {
		t.Errorf("keptOwn=%d, want 1", h.keptOwn)
	}
}

// TestSrcdocIsFirstParty: a srcdoc iframe runs content from this document, so it
// is not a third party whatever its src says, and sandboxing it would break a
// page's own embedded markup.
func TestSrcdocIsFirstParty(t *testing.T) {
	for _, in := range []string{
		`<iframe srcdoc="<p>x</p>"></iframe>`,
		`<iframe srcdoc="<p>x</p>" src="https://v.example/y"></iframe>`,
	} {
		got, h, err := hardenString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was hardened:\n got: %s", in, got)
		}
		if h.sandboxed != 0 {
			t.Errorf("%s: sandboxed=%d, want 0", in, h.sandboxed)
		}
	}
}

func TestSameOriginIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<iframe src="/local"></iframe>`,
		`<iframe src="page.html"></iframe>`,
		`<iframe></iframe>`,
		`<iframe src="https://keep.example/x"></iframe>`,
	} {
		got, h, err := hardenString(in, func(h *hardener) { h.keep["keep.example"] = true })
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was hardened:\n got: %s", in, got)
		}
		if h.sandboxed != 0 {
			t.Errorf("%s: sandboxed=%d", in, h.sandboxed)
		}
	}
}

func TestReferrerPolicy(t *testing.T) {
	got, h, err := hardenString(`<iframe src="https://v.example/e"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `referrerpolicy="no-referrer"`) {
		t.Errorf("no policy was set: %s", got)
	}
	if h.policySet != 1 {
		t.Errorf("policySet=%d, want 1", h.policySet)
	}

	// An author's policy is kept.
	got, h, err = hardenString(`<iframe src="https://v.example/e" referrerpolicy="origin"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `referrerpolicy="origin"`) {
		t.Errorf("the author's policy was replaced: %s", got)
	}
	if h.policySet != 0 {
		t.Errorf("policySet=%d, want 0", h.policySet)
	}

	// Empty means do not set one.
	got, _, err = hardenString(`<iframe src="https://v.example/e"></iframe>`,
		func(h *hardener) { h.policy = "" })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "referrerpolicy") {
		t.Errorf("a policy was set despite an empty setting: %s", got)
	}

	for _, good := range []string{"no-referrer", "strict-origin-when-cross-origin", "ORIGIN", ""} {
		if !validPolicy(good) {
			t.Errorf("validPolicy(%q) = false", good)
		}
	}
	for _, bad := range []string{"nope", "no referrer", "no-referrer;"} {
		if validPolicy(bad) {
			t.Errorf("validPolicy(%q) = true", bad)
		}
	}
}

func TestHostOf(t *testing.T) {
	for _, tt := range []struct{ src, want string }{
		{"https://v.example/e", "v.example"},
		{"https://V.EXAMPLE/e", "v.example"},
		{"//v.example/e", "v.example"},
		{"/local", ""},
		{"", ""},
		{"https://v.example/e?a=1&amp;b=2", "v.example"},
	} {
		if got := hostOf(tt.src); got != tt.want {
			t.Errorf("hostOf(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

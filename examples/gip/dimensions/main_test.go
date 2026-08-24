package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<img src="/a" width="10" height="20">`,
	`<img src="/b">`,
	`<img src="/c" width="10">`,
	`<img src="/d" height="20">`,
	`<img src="/e" width height>`,
	`<img src="/f" width="" height="">`,
	`<img src="/g" width="0" height="0">`,
	`<img src="/h" width="auto" height="auto">`,
	`<img src="/i" style="aspect-ratio:1/1">`,
	`<img src="/j" style="width:10px;height:20px">`,
	`<img src="/k" style="color:red">`,
	`<iframe src="/l"></iframe>`,
	`<video src="/m"></video>`,
	`<embed src="/n">`,
	`<object data="/o"></object>`,
	`<!DOCTYPE html><html><body><img src="/p"></body></html>`,
	`<p>no media</p>`,
	``,
}

func chunked(in string, n int, a *auditor) (string, error) {
	var out bytes.Buffer
	opts := append(a.options(), lolhtml.WithEncoding(a.encoding))
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

func newAuditor() *auditor { return &auditor{encoding: "utf-8"} }

// TestFindingsAreChunkInvariant is the property that matters for an audit tool:
// the report is the output, and it must not depend on how the input arrived.
// Byte ranges especially - they are what a build points at.
func TestFindingsAreChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		_, whole, err := auditString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 13} {
			a := newAuditor()
			if _, err := chunked(doc, n, a); err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if a.report() != whole.report() {
				t.Errorf("chunk size %d changed the report for %q:\n whole: %s\nchunks: %s",
					n, doc, whole.report(), a.report())
			}
		}
	}
}

// TestByteRangesSliceTheInput: a finding is only useful if its range can be cut
// out of the original file. SourceLocation indexes the bytes fed to the
// rewriter, so that holds even when the handler saw different bytes.
func TestByteRangesSliceTheInput(t *testing.T) {
	in := `<p>a</p><img src="/b"><p>c</p><iframe src="/d"></iframe>`
	_, a, err := auditString(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.findings) != 2 {
		t.Fatalf("findings=%d, want 2", len(a.findings))
	}
	for _, f := range a.findings {
		if f.loc.Start < 0 || f.loc.End > len(in) || f.loc.Start > f.loc.End {
			t.Fatalf("range %d-%d is not inside a %d-byte input", f.loc.Start, f.loc.End, len(in))
		}
		got := in[f.loc.Start:f.loc.End]
		if !strings.HasPrefix(got, "<"+f.tag) {
			t.Errorf("range %d-%d sliced %q, which is not the <%s> it reported",
				f.loc.Start, f.loc.End, got, f.tag)
		}
	}
}

// TestByteRangesSliceALegacyEncodedInput is the same claim where it could
// plausibly break: the handler sees UTF-8, the file is windows-1252, and the two
// have different lengths. The range has to belong to the file.
func TestByteRangesSliceALegacyEncodedInput(t *testing.T) {
	// caf\xe9 is four bytes here and five as UTF-8.
	in := "<p>caf\xe9</p><img src=\"/caf\xe9.png\">"

	var out bytes.Buffer
	a := &auditor{encoding: "windows-1252"}
	if err := a.run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if len(a.findings) != 1 {
		t.Fatalf("findings=%d, want 1", len(a.findings))
	}

	f := a.findings[0]
	if f.loc.End > len(in) {
		t.Fatalf("range %d-%d runs past the %d-byte input; the location must index "+
			"input bytes, not the UTF-8 the handler saw", f.loc.Start, f.loc.End, len(in))
	}
	if got := in[f.loc.Start:f.loc.End]; !strings.HasPrefix(got, "<img") {
		t.Errorf("range sliced %q, want the img tag", got)
	}
	// And the src the handler read is UTF-8, not the document's bytes.
	if !strings.Contains(f.src, "café") {
		t.Errorf("handler saw src=%q, want the decoded form", f.src)
	}
}

// TestValuelessAndUnusableDimensionsCount: a valueless width reads as present to
// anything that only checks for the attribute, and reserves no space at all.
func TestValuelessAndUnusableDimensionsCount(t *testing.T) {
	for _, tt := range []struct {
		in    string
		found bool
	}{
		{`<img src="/a" width="10" height="20">`, false},
		{`<img src="/a" width height>`, true},
		{`<img src="/a" width="" height="">`, true},
		{`<img src="/a" width="0" height="0">`, true},
		{`<img src="/a" width="auto" height="auto">`, true},
		{`<img src="/a" width="10" height="0">`, true},
		{`<img src="/a" width=" 10 " height=" 20 ">`, false},
		{`<img src="/a" width="10">`, true},
	} {
		_, a, err := auditString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if found := len(a.findings) == 1; found != tt.found {
			t.Errorf("%s: reported=%v, want %v (%s)", tt.in, found, tt.found, a.report())
		}
	}
}

// TestIntrinsicStyleIsRespected: an author who wrote an aspect-ratio thought
// about this, and reporting it would be noise.
func TestIntrinsicStyleIsRespected(t *testing.T) {
	for _, tt := range []struct {
		in    string
		found bool
	}{
		{`<img src="/a" style="aspect-ratio:16/9">`, false},
		{`<img src="/a" style="ASPECT-RATIO:16/9">`, false},
		{`<img src="/a" style="width:10px;height:20px">`, false},
		{`<img src="/a" style="width:10px">`, true},
		{`<img src="/a" style="color:red">`, true},
		{`<img src="/a" style="">`, true},
	} {
		_, a, err := auditString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if found := len(a.findings) == 1; found != tt.found {
			t.Errorf("%s: reported=%v, want %v", tt.in, found, tt.found)
		}
	}
}

// TestFixReservesSpaceWithoutInventingPixels. The right width and height are
// facts about the image file, which this program cannot see, so -fix declares a
// ratio instead of guessing a size.
func TestFixReservesSpaceWithoutInventingPixels(t *testing.T) {
	got, a, err := auditString(`<img src="/a">`, func(a *auditor) {
		a.fix, a.ratio = true, "16:9"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `style="aspect-ratio:16/9"`) {
		t.Errorf("no ratio was declared: %s", got)
	}
	if strings.Contains(got, "width=") || strings.Contains(got, "height=") {
		t.Errorf("-fix invented a pixel size: %s", got)
	}
	if a.fixed != 1 {
		t.Errorf("fixed=%d, want 1", a.fixed)
	}

	// An existing style is kept and appended to, not replaced.
	got, _, err = auditString(`<img src="/a" style="color:red">`, func(a *auditor) {
		a.fix, a.ratio = true, "1:1"
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<img src="/a" style="color:red;aspect-ratio:1/1">`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestFixIsIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := auditString(doc, func(a *auditor) { a.fix, a.ratio = true, "16:9" })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, a, err := auditString(once, func(a *auditor) { a.fix, a.ratio = true, "16:9" })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if a.fixed != 0 {
			t.Errorf("second pass of %q fixed %d element(s)", doc, a.fixed)
		}
	}
}

func TestRatioParsing(t *testing.T) {
	for _, good := range []string{"16:9", "1:1", " 4 : 3 "} {
		if _, _, err := parseRatio(good); err != nil {
			t.Errorf("parseRatio(%q) = %v", good, err)
		}
	}
	for _, bad := range []string{"", "16", "16:", ":9", "0:1", "1:0", "-1:2", "a:b", "16:9:1"} {
		if _, _, err := parseRatio(bad); err == nil {
			t.Errorf("parseRatio(%q) was accepted", bad)
		}
	}
}

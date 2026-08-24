package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<a href="/a">Real text</a>`,
	`<a href="/b">click here</a>`,
	`<a href="/c"></a>`,
	`<a href="/d">https://example.com/x</a>`,
	`<a href="/e">Docs</a><a href="/f">Docs</a>`,
	`<a href="javascript:x()">js</a>`,
	`<a href="/g"><img src="/i" alt="From alt"></a>`,
	`<a href="#top">Top</a>`,
	`<a href="mailto:a@b.c">Mail</a>`,
	`<a href="tel:+441234">Call</a>`,
	`<a href="https://other.example/x">External</a>`,
	`<a href="">Empty target</a>`,
	`<a>No href</a>`,
	`<a href="/a%zz">Bad escape</a>`,
	`<a href="/x">Caf&eacute; &amp; cr&egrave;me</a>`,
	`<a href="/x?a=1&amp;b=2">Encoded target</a>`,
	`<a href="/x">plain <b>bold <i>italic</i></b> tail</a>`,
	`<a href="  /spaced  ">Spaced target</a>`,
	`<a href="/x">   </a>`,
	`<ul><li><a href="/a">A</a></li><li><a href="/b">B</a></li></ul>`,
	`<p>no links</p>`,
	``,
}

func chunked(in string, n int, r *reporter) error {
	w, err := lolhtml.NewWriter(io.Discard, r.options()...)
	if err != nil {
		return err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return err
		}
	}
	return w.Close()
}

// TestReportIsChunkInvariant: the report is the output, so it has to be the same
// whatever size the writes were. Link text is accumulated across chunks, which
// is exactly what a boundary inside a link would break.
func TestReportIsChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		whole, err := reportString(doc, "https://example.com")
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			r := &reporter{base: "https://example.com"}
			if err := chunked(doc, n, r); err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if r.render() != whole.render() {
				t.Errorf("chunk size %d changed the report for %q:\n whole:\n%s\nchunks:\n%s",
					n, doc, whole.render(), r.render())
			}
		}
	}
}

// TestTextIsDecodedBeforeItIsJudged: the text arrives as raw source, so a link
// reading "click here" written as "click&nbsp;here" has to be recognised, and
// text has to be reported in the form a person would read.
func TestTextIsDecodedBeforeItIsJudged(t *testing.T) {
	r, err := reportString(`<a href="/x">Caf&eacute; &amp; cr&egrave;me</a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.links) != 1 {
		t.Fatalf("links=%d, want 1", len(r.links))
	}
	if want := "Café & crème"; r.links[0].Text != want {
		t.Errorf("Text = %q, want %q", r.links[0].Text, want)
	}

	// And the href likewise, so an encoded query is reported as it means.
	r, err = reportString(`<a href="/x?a=1&amp;b=2">t</a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/x?a=1&b=2"; r.links[0].Href != want {
		t.Errorf("Href = %q, want %q", r.links[0].Href, want)
	}
}

func TestClassification(t *testing.T) {
	for _, tt := range []struct{ href, base, want string }{
		{"/internal", "", "internal"},
		{"sub/page", "", "internal"},
		{"#top", "", "fragment"},
		{"", "", "empty"},
		{"mailto:a@b.c", "", "mailto"},
		{"tel:+44", "", "tel"},
		{"javascript:x()", "", "javascript"},
		{"data:,x", "", "data"},
		{"https://other.example/x", "", "external"},
		{"https://example.com/x", "https://example.com", "internal"},
		{"https://EXAMPLE.com/x", "https://example.com", "internal"},
		{"https://other.example/x", "https://example.com", "external"},
		{"/a%zz", "", "unparseable"},
		{"//cdn.example/x", "", "external"},
	} {
		if got := classify(tt.href, tt.base); got != tt.want {
			t.Errorf("classify(%q, %q) = %q, want %q", tt.href, tt.base, got, tt.want)
		}
	}
}

// TestAltTextCountsAsLinkText: an image is often the whole of a link, and a
// screen reader reads its alt. Reporting such a link as textless would be a false
// finding, which is worse than none.
func TestAltTextCountsAsLinkText(t *testing.T) {
	r, err := reportString(`<a href="/g"><img src="/i" alt="From alt"></a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.links) != 1 {
		t.Fatalf("links=%d, want 1", len(r.links))
	}
	if r.links[0].Text != "From alt" {
		t.Errorf("Text = %q, want the alt text", r.links[0].Text)
	}
	for _, f := range r.findings() {
		if f.Kind == "empty-text" {
			t.Error("a link with alt text was reported as textless")
		}
	}

	// An image with no alt genuinely leaves the link textless.
	r, err = reportString(`<a href="/g"><img src="/i"></a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.findings() {
		if f.Kind == "empty-text" {
			found = true
		}
	}
	if !found {
		t.Error("a link whose only content is an image with no alt was not reported")
	}
}

func TestFindings(t *testing.T) {
	for _, tt := range []struct {
		in   string
		kind string
	}{
		{`<a href="/x"></a>`, "empty-text"},
		{`<a href="/x">   </a>`, "empty-text"},
		{`<a href="/x">click here</a>`, "generic-text"},
		{`<a href="/x">Click Here</a>`, "generic-text"},
		{`<a href="/x">read more</a>`, "generic-text"},
		{`<a href="/x">https://example.com</a>`, "url-as-text"},
		{`<a href="/x">www.example.com</a>`, "url-as-text"},
		{`<a href="/a%zz">t</a>`, "unparseable-target"},
		{`<a href="javascript:x()">t</a>`, "javascript-target"},
		{`<a href="/e">Docs</a><a href="/f">Docs</a>`, "same-text-different-targets"},
	} {
		r, err := reportString(tt.in, "")
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		found := false
		for _, f := range r.findings() {
			if f.Kind == tt.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %q was not reported (got %v)", tt.in, tt.kind, kinds(r.findings()))
		}
	}
}

// TestSameTextSameTargetIsNotAFinding: repeating a link is normal - a navigation
// bar and a footer both linking "Home" is fine. Only the same text going to
// different places is ambiguous.
func TestSameTextSameTargetIsNotAFinding(t *testing.T) {
	r, err := reportString(`<a href="/home">Home</a><a href="/home">Home</a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.findings() {
		if f.Kind == "same-text-different-targets" {
			t.Errorf("two links with the same text and the same target were reported: %v", f)
		}
	}
}

// TestByteRangesSliceTheInput, so a report can point at the source.
func TestByteRangesSliceTheInput(t *testing.T) {
	in := `<p>before</p><a href="/x">text</a><p>after</p>`
	r, err := reportString(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.links) != 1 {
		t.Fatalf("links=%d, want 1", len(r.links))
	}
	l := r.links[0]
	if l.Start < 0 || l.End > len(in) || l.Start > l.End {
		t.Fatalf("range %d-%d is not inside a %d-byte input", l.Start, l.End, len(in))
	}
	if got := in[l.Start:l.End]; !strings.HasPrefix(got, "<a ") {
		t.Errorf("range sliced %q, want the anchor's start tag", got)
	}
}

func TestJSONShape(t *testing.T) {
	r, err := reportString(`<a href="/a">A</a><a href="/b">click here</a>`, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r.summary())
	if err != nil {
		t.Fatal(err)
	}
	var back Summary
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if back.Total != 2 {
		t.Errorf("Total = %d, want 2", back.Total)
	}
	if back.ByKind["internal"] != 2 {
		t.Errorf("ByKind = %v, want two internal", back.ByKind)
	}
	if len(back.Findings) == 0 {
		t.Error("the generic-text link produced no finding")
	}
}

// TestNoOutputDocumentIsWritten: this is a read-only pass, and writing a copy of
// the input to stdout would make it look like a filter.
func TestNoOutputDocumentIsWritten(t *testing.T) {
	var out strings.Builder
	r := &reporter{}
	w, err := lolhtml.NewWriter(&out, r.options()...)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately writing to a real sink here to prove the handlers do not
	// mutate: whatever comes out must equal what went in.
	in := `<a href="/x">t</a>`
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if out.String() != in {
		t.Errorf("the report pass modified the document:\n got: %s\nwant: %s", out.String(), in)
	}
}

func kinds(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Kind)
	}
	return out
}

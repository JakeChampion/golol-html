package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// body builds a multipart body from type/content pairs.
func body(t *testing.T, boundary string, parts ...[2]string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		head := textproto.MIMEHeader{}
		if p[0] != "" {
			head.Set("Content-Type", p[0])
		}
		w, err := mw.CreatePart(head)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// read returns the media types and contents of a multipart body.
func read(t *testing.T, doc, boundary string) [][2]string {
	t.Helper()
	var out [][2]string
	mr := multipart.NewReader(strings.NewReader(doc), boundary)
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		b, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("reading a part: %v", err)
		}
		out = append(out, [2]string{p.Header.Get("Content-Type"), string(b)})
	}
}

func rewrite(t *testing.T, doc, boundary string) (string, Report) {
	t.Helper()
	var out strings.Builder
	r, err := Rewrite(strings.NewReader(doc), &out, boundary, annotate())
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	return out.String(), r
}

// TestOnlyTheHTMLPartsAreRewritten, and everything else comes out byte for byte - a rewriter would
// corrupt a PNG and would lengthen a JSON body without changing what it says.
func TestOnlyTheHTMLPartsAreRewritten(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR<a href=\"/x\">"
	in := body(t, "B",
		[2]string{"text/html", `<a href="/x">y</a>`},
		[2]string{"application/json", `{"a":"<a href=\"/x\">"}`},
		[2]string{"image/png", png},
		[2]string{"text/plain", `<a href="/x">not markup</a>`},
		[2]string{"text/html; charset=utf-8", `<a href="/x">z</a>`},
		[2]string{"", `<a href="/x">no type</a>`},
	)
	out, r := rewrite(t, in, "B")

	got := read(t, out, "B")
	if len(got) != 6 {
		t.Fatalf("%d parts came back", len(got))
	}
	// The two html parts, including the one with a charset parameter.
	for _, i := range []int{0, 4} {
		if !strings.Contains(got[i][1], `rel="noopener"`) {
			t.Errorf("part %d was not rewritten: %q", i, got[i][1])
		}
	}
	// Everything else is untouched, including the part that has no type and the plain-text
	// part that happens to contain markup.
	want := read(t, in, "B")
	for _, i := range []int{1, 2, 3, 5} {
		if got[i][1] != want[i][1] {
			t.Errorf("part %d changed:\n  in:  %q\n  out: %q", i, want[i][1], got[i][1])
		}
	}
	if r.Rewritten() != 2 || r.Copied() != 4 {
		t.Errorf("%d rewritten and %d copied", r.Rewritten(), r.Copied())
	}
	if r.ByType["text/html"].Parts != 2 {
		t.Errorf("counted %d html parts", r.ByType["text/html"].Parts)
	}
	if r.ByType["(none)"].Parts != 1 {
		t.Errorf("a part with no type was counted as %v", r.ByType)
	}
}

// TestAPartEndingMidTagDoesNotReachTheNextPart, which is what makes one rewriter per part correct
// rather than merely convenient: the boundary is written by the multipart writer, so it is out of
// band from anything the rewriter is holding.
func TestAPartEndingMidTagDoesNotReachTheNextPart(t *testing.T) {
	for _, truncated := range []string{
		`<p>a</p><div attr="`,
		`<p>a</p><div`,
		`<p>a</p></div`,
		`<p>a</p><!-- unfinished`,
		`<p>a</p><script>var x = 1`,
	} {
		in := body(t, "B",
			[2]string{"text/html", truncated},
			[2]string{"text/html", `<p>second</p>`},
		)
		out, r := rewrite(t, in, "B")
		got := read(t, out, "B")
		if len(got) != 2 {
			t.Errorf("%q: %d parts came back", truncated, len(got))
			continue
		}
		if got[1][1] != `<p>second</p>` {
			t.Errorf("%q: the second part is %q", truncated, got[1][1])
		}
		if !strings.HasPrefix(got[0][1], `<p>a</p>`) {
			t.Errorf("%q: the first part is %q", truncated, got[0][1])
		}
		if r.Total != 2 {
			t.Errorf("%q: %d parts", truncated, r.Total)
		}
	}

	// The contrast: the same two fragments concatenated without boundaries do contaminate
	// each other, which is the hazard multipart avoids.
	first, err := lolhtml.RewriteString(`<p>a</p><div attr="`, annotate())
	if err != nil {
		t.Fatal(err)
	}
	second, err := lolhtml.RewriteString(`<p>second</p>`, annotate())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if _, err := lolhtml.RewriteString(first+second,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			names = append(names, e.TagName())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	// Concatenated, the unfinished attribute swallows the second fragment's paragraph.
	if strings.Count(strings.Join(names, " "), "p") >= 2 {
		t.Errorf("the concatenation kept both paragraphs (%v), so the contrast this "+
			"documents does not hold", names)
	}
}

// TestTheBoundariesComeOutUnchanged, since a rewriter that touched them would produce a body no
// reader could split.
func TestTheBoundariesComeOutUnchanged(t *testing.T) {
	for _, boundary := range []string{"B", "----=_Part_0_1234", "boundary-with-dashes--x"} {
		in := body(t, boundary,
			[2]string{"text/html", `<a href="/x">y</a>`},
			[2]string{"text/html", `<a href="/y">z</a>`},
		)
		out, r := rewrite(t, in, boundary)
		if got := strings.Count(out, "--"+boundary); got != strings.Count(in, "--"+boundary) {
			t.Errorf("%q: %d boundaries out, %d in", boundary, got, strings.Count(in, "--"+boundary))
		}
		if !strings.Contains(out, "--"+boundary+"--") {
			t.Errorf("%q: no closing boundary:\n%s", boundary, out)
		}
		if r.Total != 2 {
			t.Errorf("%q: %d parts", boundary, r.Total)
		}
		// And a reader can still split it.
		if got := read(t, out, boundary); len(got) != 2 {
			t.Errorf("%q: %d parts came back", boundary, len(got))
		}
	}

	// An empty boundary is refused rather than producing a body nothing can read.
	var out strings.Builder
	if _, err := Rewrite(strings.NewReader("x"), &out, "", annotate()); err == nil {
		t.Error("an empty boundary was accepted")
	}
}

// TestAPartIsClosedBeforeTheNextIsCreated, which is the ordering a multipart writer enforces: a
// part stops accepting writes once the next one starts, and a rewriter writes at Close.
func TestAPartIsClosedBeforeTheNextIsCreated(t *testing.T) {
	// The failure, demonstrated: closing the rewriter after the next part has begun.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary("B"); err != nil {
		t.Fatal(err)
	}
	head := textproto.MIMEHeader{"Content-Type": {"text/html"}}
	appendEnd := lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		return d.Append("<!-- TAIL -->", lolhtml.HTML)
	})

	p1, err := mw.CreatePart(head)
	if err != nil {
		t.Fatal(err)
	}
	w1, err := lolhtml.NewWriter(p1, appendEnd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Write([]byte("<p>one</p>")); err != nil {
		t.Fatal(err)
	}
	if _, err := mw.CreatePart(head); err != nil {
		t.Fatal(err)
	}
	// The first rewriter's Close now has nowhere to write, and says so.
	err = w1.Close()
	if err == nil {
		t.Error("closing a rewriter after the next part began succeeded")
	} else if !strings.Contains(err.Error(), "finished part") {
		t.Errorf("Close said %v", err)
	}

	// In order, the tail arrives.
	in := body(t, "B", [2]string{"text/html", `<p>one</p>`})
	var out strings.Builder
	if _, err := Rewrite(strings.NewReader(in), &out, "B", appendEnd); err != nil {
		t.Fatal(err)
	}
	got := read(t, out.String(), "B")
	if len(got) != 1 || !strings.Contains(got[0][1], "<!-- TAIL -->") {
		t.Errorf("the tail did not arrive: %q", got)
	}
}

// TestAnEmptyBodyAndAnEmptyPart, which are the shapes that break a loop written for the happy case.
func TestAnEmptyBodyAndAnEmptyPart(t *testing.T) {
	in := body(t, "B",
		[2]string{"text/html", ``},
		[2]string{"application/json", ``},
	)
	out, r := rewrite(t, in, "B")
	if r.Total != 2 {
		t.Errorf("%d parts", r.Total)
	}
	got := read(t, out, "B")
	if len(got) != 2 {
		t.Fatalf("%d parts came back", len(got))
	}
	for i, p := range got {
		if p[1] != "" {
			t.Errorf("part %d is %q", i, p[1])
		}
	}

	// A body with no parts at all.
	empty := body(t, "B")
	out, r = rewrite(t, empty, "B")
	if r.Total != 0 {
		t.Errorf("%d parts in an empty body", r.Total)
	}
	if !strings.Contains(out, "--B--") {
		t.Errorf("the closing boundary is missing:\n%s", out)
	}
	if !strings.Contains(r.String(), "0 parts") {
		t.Errorf("report:\n%s", r)
	}
}

// TestTheReportCountsBytesPerType, since the point of the report is to say what the rewrite cost
// where.
func TestTheReportCountsBytesPerType(t *testing.T) {
	html := strings.Repeat(`<a href="/x">y</a>`, 10)
	json := `{"a":1}`
	in := body(t, "B",
		[2]string{"text/html", html},
		[2]string{"application/json", json},
	)
	_, r := rewrite(t, in, "B")

	h := r.ByType["text/html"]
	if h == nil {
		t.Fatalf("no html counts: %v", r.ByType)
	}
	if h.In != int64(len(html)) {
		t.Errorf("counted %d html bytes in, want %d", h.In, len(html))
	}
	if h.Out <= h.In {
		t.Errorf("the rewrite did not lengthen the part: %d in, %d out", h.In, h.Out)
	}
	j := r.ByType["application/json"]
	if j == nil || j.In != int64(len(json)) || j.Out != j.In {
		t.Errorf("json counts %+v for %d bytes", j, len(json))
	}
	if fmt.Sprint(j.Rewritten) != "false" {
		t.Error("the json part was marked rewritten")
	}
}

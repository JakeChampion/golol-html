package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func page(n int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := range n {
		fmt.Fprintf(&b, `<div><a href="https://a.example/%d">item %d</a> some text here</div>`, i, i)
	}
	b.WriteString("</body></html>")
	return b.String()
}

// closeInOrder writes doc through a rewriter into a gzip writer, closing them in the order given,
// and returns what a reader gets back plus the rewriter's Close error.
func closeInOrder(t *testing.T, doc string, gzipFirst bool, opts ...lolhtml.Option) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	w, err := lolhtml.NewWriter(zw, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	var rewriterErr error
	if gzipFirst {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		rewriterErr = w.Close()
	} else {
		rewriterErr = w.Close()
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", rewriterErr
	}
	out, _ := io.ReadAll(zr)
	return string(out), rewriterErr
}

// TestClosingTheCompressorFirstLosesWhatTheRewriterWritesAtClose, which is the hazard: a rewriter
// writes during its own Close, and by then the compressor may be shut.
func TestClosingTheCompressorFirstLosesWhatTheRewriterWritesAtClose(t *testing.T) {
	appendEnd := lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		return d.Append("<!-- APPENDED -->", lolhtml.HTML)
	})
	holdText := lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
		if c.IsLastInTextNode() {
			return c.Replace("HELD", lolhtml.Text)
		}
		c.Remove()
		return nil
	})

	for _, tt := range []struct {
		name  string
		doc   string
		opts  []lolhtml.Option
		loses bool
	}{
		// Nothing is written at Close, so the order does not matter - which is why the
		// mistake survives testing on ordinary documents.
		{"a complete document, no append", `<html><body><p>x</p></body></html>`, nil, false},
		{"text held, document complete", `<p>hello</p>`,
			[]lolhtml.Option{holdText}, false},
		{"document ends inside a tag, no append", `<p>x</p><div attr="v`, nil, false},

		// These write at Close.
		{"an append at the document end", `<html><body><p>x</p></body></html>`,
			[]lolhtml.Option{appendEnd}, true},
		{"an append, document ends inside a tag", `<p>x</p><div attr="v`,
			[]lolhtml.Option{appendEnd}, true},
		{"text held, document ends inside the text", `<p>tail`,
			[]lolhtml.Option{holdText}, true},
	} {
		right, errRight := closeInOrder(t, tt.doc, false, tt.opts...)
		wrong, errWrong := closeInOrder(t, tt.doc, true, tt.opts...)

		if errRight != nil {
			t.Errorf("%s: closing in order gave %v", tt.name, errRight)
		}
		if lost := right != wrong; lost != tt.loses {
			t.Errorf("%s: bytes lost = %v, want %v\n  in order: %q\n  reversed: %q",
				tt.name, lost, tt.loses, right, wrong)
		}
		if tt.loses {
			if len(wrong) >= len(right) {
				t.Errorf("%s: the reversed order lost nothing: %d against %d",
					tt.name, len(wrong), len(right))
			}
			// The mistake is reported, which is the only reason it is findable -
			// and a deferred Close is where the report goes to die.
			if errWrong == nil {
				t.Errorf("%s: the rewriter's Close returned nil after the "+
					"compressor was closed", tt.name)
			} else if !strings.Contains(errWrong.Error(), "closed") {
				t.Errorf("%s: Close said %v", tt.name, errWrong)
			}
		}
	}
}

// TestTheWritePatternDoesNotChangeTheCompressedSize, because a rewriter writes in many small pieces
// and that looks like it should cost something. It does not: a gzip writer buffers and nothing
// here flushes.
func TestTheWritePatternDoesNotChangeTheCompressedSize(t *testing.T) {
	doc := page(200)

	var want int
	for i, chunk := range []int{1, 16, 512, 1 << 20} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		w, err := lolhtml.NewWriter(zw, annotate())
		if err != nil {
			t.Fatal(err)
		}
		for at := 0; at < len(doc); at += chunk {
			if _, err := w.Write([]byte(doc[at:min(at+chunk, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = buf.Len()
			continue
		}
		if buf.Len() != want {
			t.Errorf("chunk %d gave %d bytes, chunk 1 gave %d", chunk, buf.Len(), want)
		}
	}

	// And the same as compressing the finished output in one write, which is the claim that
	// makes wrapping the compressor in a bufio.Writer pointless here.
	rewritten, err := lolhtml.RewriteString(doc, annotate())
	if err != nil {
		t.Fatal(err)
	}
	var one bytes.Buffer
	zw := gzip.NewWriter(&one)
	if _, err := zw.Write([]byte(rewritten)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if one.Len() != want {
		t.Errorf("one write gave %d bytes, the rewriter gave %d", one.Len(), want)
	}
}

// TestTheRoundTripIsExact, over shapes that include the ones where the rewriter writes at Close.
func TestTheRoundTripIsExact(t *testing.T) {
	for _, doc := range []string{
		page(1),
		page(50),
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<p>x</p><script>var a = 1 < 2;</script><style>.a > .b{}</style>`,
		`<!-- c --><table><tr><td>x</table>`,
		`<p attr="unfinished`,
		``,
		`just text`,
		strings.Repeat("a", 100000),
	} {
		s, err := RoundTrip(strings.NewReader(doc), annotate())
		if err != nil {
			t.Fatalf("%.40q: %v", doc, err)
		}
		if !s.RoundTrip {
			t.Errorf("%.40q: the round trip is not exact", doc)
		}
		if s.In != len(doc) {
			t.Errorf("%.40q: counted %d bytes for %d", doc, s.In, len(doc))
		}
		if doc != "" && s.Gzipped == 0 {
			t.Errorf("%.40q: nothing was compressed", doc)
		}
	}
}

// TestWhatTheRewriteCostsCompressed, which is the figure worth having: what a rewrite adds to the
// wire is not what it adds to the document, when what it adds repeats.
func TestWhatTheRewriteCostsCompressed(t *testing.T) {
	doc := page(200)
	s, err := RoundTrip(strings.NewReader(doc), annotate())
	if err != nil {
		t.Fatal(err)
	}
	added := s.Rewritten - s.In
	addedZ := s.Gzipped - s.InGzipped
	if added <= 0 {
		t.Fatalf("the rewrite added %d bytes", added)
	}
	if addedZ <= 0 {
		t.Fatalf("the rewrite added %d compressed bytes", addedZ)
	}
	// The claim is an order of magnitude, not a number: repeated content compresses away.
	if addedZ*10 > added {
		t.Errorf("the rewrite added %d bytes and %d compressed, which is not the "+
			"order-of-magnitude difference the documentation claims", added, addedZ)
	}

	// A rewrite that adds distinct content per element is not nearly free, which is the
	// other half of the claim.
	n := 0
	distinct := lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		n++
		return e.SetAttribute("data-id", fmt.Sprintf("id-%d-%x", n, n*2654435761))
	})
	d, err := RoundTrip(strings.NewReader(doc), distinct)
	if err != nil {
		t.Fatal(err)
	}
	distinctAddedZ := d.Gzipped - d.InGzipped
	if distinctAddedZ <= addedZ*2 {
		t.Errorf("distinct content added %d compressed bytes against %d for repeated, "+
			"which does not show the difference", distinctAddedZ, addedZ)
	}
}

// TestForgettingToCloseTheCompressorIsLoud, since that is the other way to lose the output and it
// is worth knowing it fails rather than truncates quietly.
func TestForgettingToCloseTheCompressorIsLoud(t *testing.T) {
	doc := page(20)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	w, err := lolhtml.NewWriter(zw, annotate())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// zw is never closed.

	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		// A header that is not there at all is also a loud failure.
		return
	}
	if _, err := io.ReadAll(zr); err == nil {
		t.Error("an unclosed gzip stream decompressed without error")
	}
}

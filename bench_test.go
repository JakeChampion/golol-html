package lolhtml_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// benchDoc is a rough stand-in for a real page: enough elements for selector
// matching to matter, and enough text for chunking to kick in.
func benchDoc(links int) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><title>bench</title></head><body>`)
	for i := range links {
		fmt.Fprintf(&b, `<div class="row"><a href="/page/%d">link %d</a><p>Some text for row %d.</p></div>`, i, i, i)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func benchmarkRewrite(b *testing.B, doc string, opts ...lolhtml.Option) {
	in := []byte(doc)
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		w, err := lolhtml.NewWriter(io.Discard, opts...)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(in); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPassthrough measures the floor: parse and re-serialise with no
// handlers, so the cost is lol-html plus the cgo and sink overhead.
func BenchmarkPassthrough(b *testing.B) {
	benchmarkRewrite(b, benchDoc(200))
}

// BenchmarkSetAttribute is the common case: one selector, one mutation per
// match, so it shows the per-callback cost of crossing into Go.
func BenchmarkSetAttribute(b *testing.B) {
	benchmarkRewrite(b, benchDoc(200), lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("href", "https://example.com/x")
	}))
}

// BenchmarkReadAttributes isolates the cost of pulling strings back out of C.
func BenchmarkReadAttributes(b *testing.B) {
	benchmarkRewrite(b, benchDoc(200), lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		for range e.Attributes() {
		}
		return nil
	}))
}

// BenchmarkTextHandler exercises the chunked text path, where handler
// invocations outnumber elements.
func BenchmarkTextHandler(b *testing.B) {
	benchmarkRewrite(b, benchDoc(200), lolhtml.OnText("p", func(t *lolhtml.TextChunk) error {
		_ = t.Text()
		return nil
	}))
}

// BenchmarkStreamingAppend compares the streaming insertion path against the
// eager one above.
func BenchmarkStreamingAppend(b *testing.B) {
	benchmarkRewrite(b, benchDoc(200), lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		return e.StreamAppend(func(s *lolhtml.Sink) error {
			return s.WriteString("x", lolhtml.Text)
		})
	}))
}

// BenchmarkChunkedWrite shows the cost of many small writes, as when piping a
// network response straight through.
func BenchmarkChunkedWrite(b *testing.B) {
	in := []byte(benchDoc(200))
	const chunk = 1400 // roughly one TCP segment

	b.SetBytes(int64(len(in)))
	b.ReportAllocs()

	for b.Loop() {
		w, err := lolhtml.NewWriter(io.Discard, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("href", "https://example.com/x")
		}))
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < len(in); i += chunk {
			if _, err := w.Write(in[i:min(i+chunk, len(in))]); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

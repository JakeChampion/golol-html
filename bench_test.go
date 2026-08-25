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

// BenchmarkCrossing is the cost of a handler that does nothing, and of each way
// of asking the element a question, against the same document. It exists because
// throughput here tracks the number of crossings rather than the size of the
// document, so the interesting number is what one crossing costs before any work
// is done.
//
// Read allocs/op and B/op; ns/op on this benchmark is dominated by whatever else
// the machine is doing. Measured once on benchDoc(200), which is about 604
// elements: passthrough 14 allocations for the whole rewrite, a no-op element
// handler 625, TagName 1229, and CanHaveContent and NamespaceURI none beyond the
// crossing.
func BenchmarkCrossing(b *testing.B) {
	doc := benchDoc(200)
	b.Run("noop", func(b *testing.B) {
		benchmarkRewrite(b, doc, lolhtml.OnElement("*", func(*lolhtml.Element) error { return nil }))
	})
	b.Run("TagName", func(b *testing.B) {
		benchmarkRewrite(b, doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			_ = e.TagName()
			return nil
		}))
	})
	b.Run("NamespaceURI", func(b *testing.B) {
		benchmarkRewrite(b, doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			_ = e.NamespaceURI()
			return nil
		}))
	})
	b.Run("CanHaveContent", func(b *testing.B) {
		benchmarkRewrite(b, doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			_ = e.CanHaveContent()
			return nil
		}))
	})
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

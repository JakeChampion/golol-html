package lolhtml_test

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// FuzzRewrite checks the property that makes the streaming API trustworthy: the
// output must not depend on how the input was split. Any difference between a
// single write and a byte-at-a-time write is a bug in the buffering, in the
// handler dispatch, or in how partial UTF-8 is carried across chunks.
//
// It also serves as a crash check for the C boundary: malformed markup must
// produce an error or an odd document, never a panic or a memory fault.
func FuzzRewrite(f *testing.F) {
	seeds := []string{
		`<a href="/x">link</a>`,
		`<!DOCTYPE html><html><body><p>hi</p></body></html>`,
		`<div><!--c--><span>t</span></div>`,
		`<svg viewBox="0 0 1 1"><circle/></svg>`,
		"<p>café üñîçødé</p>",
		`<a href=`,             // truncated attribute
		`<div><div><div><div>`, // unclosed nesting
		`<script>var x = "</p>";</script>`,
		`<textarea><b>not markup</b></textarea>`,
		`<p>a</p` + strings.Repeat("x", 200),
		"<p>\xff\xfe invalid utf8</p>",
		``,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// Text chunk counts are deliberately not compared: lol-html splits text at
	// input chunk boundaries, so a byte-at-a-time write legitimately produces
	// more text chunks than a single write. Structural handlers - elements,
	// comments, the doctype - must fire the same number of times either way.
	handlers := func(hits *int) []lolhtml.Option {
		return []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				*hits++
				href, _ := e.Attribute("href")
				return e.SetAttribute("href", "/"+strings.TrimPrefix(href, "/"))
			}),
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				*hits++
				return e.Append("<!--d-->", lolhtml.HTML)
			}),
			lolhtml.OnText("p", func(t *lolhtml.TextChunk) error {
				if t.Text() == "" {
					return nil
				}
				return t.Replace(strings.ToUpper(t.Text()), lolhtml.Text)
			}),
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				*hits++
				return c.SetText("x")
			}),
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				*hits++
				return nil
			}),
		}
	}

	rewrite(f, handlers)
}

// maxFuzzInput bounds the harness so the fuzzer keeps making progress; see the
// note in the Fuzz body.
const maxFuzzInput = 8 << 10

// fuzzChunk keeps the split fine-grained where it is cheap - one byte at a time
// is the strictest test of chunk-invariance - while bounding the number of
// writes for larger inputs.
func fuzzChunk(n int) int {
	if n <= 1024 {
		return 1
	}
	return max(1, n/64)
}

func rewrite(f *testing.F, handlers func(*int) []lolhtml.Option) {
	f.Fuzz(func(t *testing.T, in string) {
		// Writing one byte at a time is quadratic when the rewriter is
		// buffering an unclosed tag - measured 4.4ms for 4KB against 43.7ms for
		// 16KB - so an unbounded harness stalls as the fuzzer grows inputs.
		// Cap the size, and keep the write count bounded above that.
		if len(in) > maxFuzzInput {
			t.Skip("input larger than the harness budget")
		}

		var wholeHits int
		var whole bytes.Buffer

		w, err := lolhtml.NewWriter(&whole, handlers(&wholeHits)...)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		_, wholeWriteErr := w.Write([]byte(in))
		wholeCloseErr := w.Close()

		var pieceHits int
		var pieces bytes.Buffer
		w2, err := lolhtml.NewWriter(&pieces, handlers(&pieceHits)...)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		var pieceWriteErr error
		for i := 0; i < len(in); i += fuzzChunk(len(in)) {
			end := min(i+fuzzChunk(len(in)), len(in))
			if _, pieceWriteErr = w2.Write([]byte(in[i:end])); pieceWriteErr != nil {
				break
			}
		}
		pieceCloseErr := w2.Close()

		// Either both fail or both succeed; a split that changes success is a
		// bug regardless of what the document was.
		wholeFailed := wholeWriteErr != nil || wholeCloseErr != nil
		pieceFailed := pieceWriteErr != nil || pieceCloseErr != nil
		if wholeFailed != pieceFailed {
			t.Fatalf("chunking changed the outcome for %q:\n whole: write=%v close=%v\n bytewise: write=%v close=%v",
				in, wholeWriteErr, wholeCloseErr, pieceWriteErr, pieceCloseErr)
		}
		if wholeFailed {
			return
		}

		if !bytes.Equal(whole.Bytes(), pieces.Bytes()) {
			t.Fatalf("chunking changed the output for %q:\n whole:    %q\n bytewise: %q",
				in, whole.String(), pieces.String())
		}
		if wholeHits != pieceHits {
			t.Fatalf("chunking changed structural handler invocations for %q: whole=%d bytewise=%d",
				in, wholeHits, pieceHits)
		}
	})
}

// TestUnclosedWriterIsReclaimed exercises the runtime.AddCleanup backstop for a
// Writer the caller drops without closing. It cannot assert that C memory was
// freed - there is nothing to observe - but it does prove the cleanup path runs
// without faulting, which is the failure that would matter.
func TestUnclosedWriterIsReclaimed(t *testing.T) {
	for range 200 {
		w, err := lolhtml.NewWriter(&bytes.Buffer{},
			lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("x", "y")
			}))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if _, err := w.Write([]byte(`<a>x</a>`)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		// Deliberately no Close.
		_ = w
	}

	// Two cycles: the first queues the cleanups, the second lets them finish.
	for range 2 {
		runtime.GC()
	}
}

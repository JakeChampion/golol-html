package lolhtml_test

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
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
	handlers := func(hits *int, saw *bytes.Buffer) []lolhtml.Option {
		// saw records what each handler was told, not just that it ran.
		//
		// Comparing output bytes and invocation counts leaves a whole class of
		// regression invisible: a source location that became relative to the
		// current Write, a tag name reported with the wrong case, an attribute
		// read from the wrong element. All of those produce identical output and
		// identical counts. What a handler sees is the library's other interface,
		// and this is the only place it is compared across chunkings.
		//
		// Text is deliberately not recorded per chunk. Chunk boundaries do split
		// text nodes - that is the documented behaviour, not a bug - so the
		// digest would differ legitimately. The text handler contributes its
		// concatenation at the end instead, via textSeen.
		note := func(parts ...string) {
			for i, s := range parts {
				if i > 0 {
					saw.WriteByte('|')
				}
				saw.WriteString(s)
			}
			saw.WriteByte('\n')
		}

		return []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				*hits++
				href, _ := e.Attribute("href")
				note("a", e.TagName(), e.SourceLocation().String(), href,
					strconv.FormatBool(e.IsSelfClosing()))
				return e.SetAttribute("href", "/"+strings.TrimPrefix(href, "/"))
			}),
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				*hits++
				note("div", e.TagName(), e.SourceLocation().String(),
					strconv.FormatBool(e.CanHaveContent()), e.NamespaceURI())
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
				note("comment", c.Text(), c.SourceLocation().String())
				return c.SetText("x")
			}),
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				*hits++
				name, _ := d.Name()
				pub, _ := d.PublicID()
				sys, _ := d.SystemID()
				note("doctype", name, pub, sys, d.SourceLocation().String())
				return nil
			}),
		}
	}

	rewrite(f, handlers)
}

// invarianceSettings derives an encoding and a strict-mode choice from the
// input, so that chunk-invariance is checked under a legacy encoding and with
// strict parsing off, not only with the defaults.
//
// Both are safe to vary here because neither changes whether a document can be
// rewritten in one write but not in several. A memory limit is not safe to vary
// for exactly that reason, and is left out.
func invarianceSettings(in string) []lolhtml.Option {
	if in == "" {
		return nil
	}
	b := in[len(in)-1]
	return []lolhtml.Option{
		lolhtml.WithEncoding(invarianceEncodings[int(b)%len(invarianceEncodings)]),
		lolhtml.WithStrict(b&0x40 == 0),
	}
}

var invarianceEncodings = []string{"utf-8", "windows-1252", "shift_jis", "koi8-r"}

// maxFuzzInput bounds the harness so the fuzzer keeps making progress; see the
// note in the Fuzz body.
const maxFuzzInput = 4 << 10

// fuzzChunk keeps the split fine-grained where it is cheap - one byte at a time
// is the strictest test of chunk-invariance - while bounding the number of
// writes for larger inputs.
//
// The byte-at-a-time threshold is low on purpose. Every write costs a crossing
// into C whatever its size, so a 1 KB input meant roughly a thousand crossings
// where a chunked one means a handful, and every iteration does that on top of a
// whole-document rewrite. On a CI runner that dropped throughput to about// 1600 execs/sec and the engine then failed to shut down inside its grace
// period at the end of a timed run, reporting "context deadline exceeded".
// Cheap iterations find more than thorough ones that barely run.
func fuzzChunk(n int) int {
	if n <= 256 {
		return 1
	}
	return max(1, n/64)
}

func rewrite(f *testing.F, handlers func(*int, *bytes.Buffer) []lolhtml.Option) {
	f.Fuzz(func(t *testing.T, in string) {
		// Writing one byte at a time costs a crossing into C per byte - about
		// eight times the time of one whole write on a 64 KB page - so an
		// unbounded harness stalls as the fuzzer grows inputs. Linearly, but
		// from a starting point several times too expensive. Cap the size, and
		// keep the write count bounded above that. See bytecost_test.go.
		if len(in) > maxFuzzInput {
			t.Skip("input larger than the harness budget")
		}

		handlesBefore := lolhtml.LiveHandles()

		// The configuration is varied from the input, so that the invariant is
		// tested against a legacy encoding and with strict mode off as well as
		// with the defaults. Both writers get the same settings, or the
		// comparison would be meaningless.
		//
		// A memory limit is deliberately absent, and not by oversight: the
		// memory a rewrite needs depends on how the input is fed, by a factor of
		// eight in one measured case, so a limit that one of these two writers
		// stays under and the other does not would make them differ legitimately
		// and this test would report it as a bug. See the note on sizing in
		// MemorySettings.MaxMemory.
		settings := invarianceSettings(in)

		var wholeHits int
		var whole, wholeSaw bytes.Buffer

		w, err := lolhtml.NewWriter(&whole, append(handlers(&wholeHits, &wholeSaw), settings...)...)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		_, wholeWriteErr := w.Write([]byte(in))
		wholeCloseErr := w.Close()

		var pieceHits int
		var pieces, pieceSaw bytes.Buffer
		w2, err := lolhtml.NewWriter(&pieces, append(handlers(&pieceHits, &pieceSaw), settings...)...)
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
		// A leaked handle is invisible in the output, so it has to be
		// asserted separately - and every iteration is the cheapest place.
		// No GC here: this runs on every iteration, and releases only ever
		// lower the count, so growth alone is a reliable leak signal.
		if after := lolhtml.LiveHandles(); after > handlesBefore {
			t.Fatalf("leaked %d cgo handles rewriting %q", after-handlesBefore, in)
		}
		if wholeHits != pieceHits {
			t.Fatalf("chunking changed structural handler invocations for %q: whole=%d bytewise=%d",
				in, wholeHits, pieceHits)
		}
		// And what those handlers were told, which the output does not show.
		if !bytes.Equal(wholeSaw.Bytes(), pieceSaw.Bytes()) {
			t.Fatalf("chunking changed what the handlers saw for %q:\n whole:\n%s\n bytewise:\n%s",
				in, wholeSaw.String(), pieceSaw.String())
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

// TestNoHandleLeak checks the invariant directly, at a scale where a single
// missed delete would be obvious.
func TestNoHandleLeak(t *testing.T) {
	before := settledHandles()

	for range 100 {
		_, err := lolhtml.RewriteString(`<div id="a"><p>hi</p><!--c--></div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				if err := e.SetUserData("x"); err != nil {
					return err
				}
				// Replacing user data must release the value it displaces.
				if err := e.SetUserData("y"); err != nil {
					return err
				}
				if err := e.StreamAppend(func(s *lolhtml.Sink) error {
					return s.WriteString("s", lolhtml.Text)
				}); err != nil {
					return err
				}
				return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
			}),
			lolhtml.OnText("p", func(*lolhtml.TextChunk) error { return nil }),
			lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return nil }),
		)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
	}

	if after := settledHandles(); after > before {
		t.Errorf("leaked %d cgo handles across 100 rewrites (%d -> %d)",
			after-before, before, after)
	}
}

// TestNoHandleLeakOnFailure covers the paths that skip the ordinary teardown:
// a handler error, a panic, and a writer that is never closed at all.
func TestNoHandleLeakOnFailure(t *testing.T) {
	before := settledHandles()
	boom := errors.New("boom")

	for range 50 {
		_, _ = lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				_ = e.StreamAppend(func(*lolhtml.Sink) error { return nil })
				return boom
			}))

		func() {
			defer func() { _ = recover() }()
			_, _ = lolhtml.RewriteString(`<div>x</div>`,
				lolhtml.OnElement("div", func(e *lolhtml.Element) error {
					_ = e.SetUserData("x")
					panic("handler exploded")
				}))
		}()
	}

	if after := settledHandles(); after > before {
		t.Errorf("leaked %d cgo handles across failing rewrites (%d -> %d)",
			after-before, before, after)
	}
}

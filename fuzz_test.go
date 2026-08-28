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
	// comments, the doctype - must fire the same number of times either way, and
	// so must the text *nodes*: how many there are and what each says once its
	// chunks are joined does not depend on the writes. Only the boundaries
	// between chunks do. See nodeinvariance_test.go for that stated as a table.
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
		// Text is recorded per node rather than per chunk. Chunk boundaries do
		// split text nodes - that is the documented behaviour, not a bug - so a
		// per-chunk digest would differ legitimately, and an earlier version of
		// this harness therefore recorded no text at all, with a comment
		// promising a concatenation that no code in it ever produced. The node
		// is the unit that does not move: accumulate to IsLastInTextNode and the
		// digest is comparable, which puts every text node's content under the
		// same comparison as everything else a handler sees.
		// node accumulates the current text node, so that the digest records what
		// each node said rather than how it happened to be cut up.
		var node strings.Builder

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
			lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
				node.WriteString(t.Text())
				if t.IsLastInTextNode() {
					note("textnode", strconv.Quote(node.String()))
					node.Reset()
				}
				return nil
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
//
// It was 4 KB, which put lol-html's own buffering out of reach of the one oracle
// that says the buffering does not depend on the writes: the deterministic tests
// feed 8 KB and 16 KB documents precisely because that is where the internal
// buffer grows and where a rescan would show up (bytecost_test.go), and this
// target never saw a document that size.
//
// 16 KB is where raising it stops paying. The cost of an iteration is set by the
// document, not by the number of writes - measured here on a document dense with
// matches, one iteration is 0.3 ms at 1 KB, 1 ms at 4 KB, 3.8 ms at 16 KB and
// 7.8 ms at 32 KB, while the writes in it stay bounded throughout. 16 KB clears
// the 8 KB boundary with room on both sides for about four times the price of
// the old ceiling; 32 KB doubles that price for nothing the boundaries below it
// do not already cover, and this target is one where cheap iterations find more
// than thorough ones that barely run.
const maxFuzzInput = 16 << 10

// maxFuzzWrites bounds how many times one iteration crosses into C. A write
// costs a crossing whatever its size, so this is what keeps a large input from
// also being a slow one: 256 is about what a 256-byte input already spends
// writing itself byte at a time.
const maxFuzzWrites = 256

// fuzzChunkSizes are the write sizes the split is drawn from once an input is
// too large to write byte at a time.
//
// They sit on the boundaries a buffering bug hides at - the powers of two the
// buffer grows through, and one either side of each, so that a document is cut
// just before, exactly at, and just after the place the library changes its
// mind. A single ratio like n/64 lands on the same relative offset for every
// input and so never lands on any of them.
var fuzzChunkSizes = []int{
	1, 2, 3, 7, 63, 64, 65, 255, 256, 257,
	1023, 1024, 1025, 4095, 4096, 4097, 8191, 8192,
}

// fuzzChunk keeps the split fine-grained where it is cheap - one byte at a time
// is the strictest test of chunk-invariance - while bounding the number of
// writes for larger inputs. Which size a larger input gets is drawn from the
// input itself, so the search covers the boundaries rather than one ratio.
//
// The byte-at-a-time threshold is low on purpose. Every write costs a crossing
// into C whatever its size, so a 1 KB input meant roughly a thousand crossings
// where a chunked one means a handful, and every iteration does that on top of a
// whole-document rewrite. On a CI runner that dropped throughput to about
// 1600 execs/sec and the engine then failed to shut down inside its grace
// period at the end of a timed run, reporting "context deadline exceeded".
// Cheap iterations find more than thorough ones that barely run.
//
// The floor is what keeps that true as the ceiling rises: a 16 KB input split
// into 7-byte writes would be 2340 crossings, so any size below the floor is
// raised to it, and the fine splits stay where they are affordable - on the
// smaller inputs, which is also where a rune split across a write is the thing
// worth searching for. At the ceiling the floor is 64, so every size in the
// table from 64 up is still reached there, including all four that straddle
// 4 KB and 8 KB.
func fuzzChunk(n int, sel byte) int {
	if n <= 256 {
		return 1
	}
	size := fuzzChunkSizes[int(sel)%len(fuzzChunkSizes)]
	return max(size, (n+maxFuzzWrites-1)/maxFuzzWrites)
}

// expectedFailure reports whether err is a refusal the library documents, as
// opposed to a rewrite that should have worked and did not.
//
// The distinction is the whole point of asking. This harness feeds malformed
// markup on purpose, and malformed markup is not a reason to fail: lol-html
// parses it the way a browser would. So an error here is either one of these
// refusals, each of which has a sentinel precisely so a caller can act on it, or
// it is a document that used to rewrite and now does not - which is the
// regression no other oracle in this target can see, because comparing the two
// runs to each other says nothing when both of them fail.
//
// Only ErrAmbiguousTag is expected to arrive in practice: the settings vary the
// encoding and strict mode and nothing else, and the handlers insert a fixed
// comment, an uppercased copy of text the document already held, and an href the
// document already held. The rest are listed because they are refusals with a
// sentinel to match rather than because anything here provokes them. Nothing
// broader is: an error without a sentinel is an unexplained failure, and this
// oracle exists to notice exactly that.
func expectedFailure(err error) bool {
	for _, refusal := range []error{
		lolhtml.ErrAmbiguousTag,
		lolhtml.ErrMemoryLimitExceeded,
		lolhtml.ErrRawTextBreakout,
		lolhtml.ErrCommentBreakout,
		lolhtml.ErrInvalidUTF8,
		lolhtml.ErrIncompleteRune,
	} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// firstErr is the failure a run reported, which is the one from Write if there
// was one: a Close after a failed Write answers ErrPoisoned wrapping it, so the
// later error says less than the earlier one.
func firstErr(writeErr, closeErr error) error {
	if writeErr != nil {
		return writeErr
	}
	return closeErr
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

		// The write size for the split run is drawn from the input, so that the
		// same document is cut in different places across iterations. Anything
		// up to 256 bytes is still written byte at a time whatever this says.
		var chunkSel byte
		if len(in) > 0 {
			chunkSel = in[0]
		}
		chunk := fuzzChunk(len(in), chunkSel)

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
		for i := 0; i < len(in); i += chunk {
			end := min(i+chunk, len(in))
			if _, pieceWriteErr = w2.Write([]byte(in[i:end])); pieceWriteErr != nil {
				break
			}
		}
		pieceCloseErr := w2.Close()

		// Either both fail or both succeed; a split that changes success is a
		// bug regardless of what the document was.
		wholeErr := firstErr(wholeWriteErr, wholeCloseErr)
		pieceErr := firstErr(pieceWriteErr, pieceCloseErr)
		if (wholeErr != nil) != (pieceErr != nil) {
			t.Fatalf("chunking changed the outcome for %q:\n whole: write=%v close=%v\n bytewise: write=%v close=%v",
				in, wholeWriteErr, wholeCloseErr, pieceWriteErr, pieceCloseErr)
		}
		// Failing the same way, not merely failing: the same document reaching
		// the same refusal by two routes is the claim, and two different
		// refusals would satisfy the check above while meaning the split
		// changed what the parser saw.
		if wholeErr != nil && pieceErr != nil && wholeErr.Error() != pieceErr.Error() {
			t.Fatalf("chunking changed the error for %q:\n whole:    %v\n bytewise: %v",
				in, wholeErr, pieceErr)
		}
		// And a failure at all has to be one the library explains. A document
		// that used to rewrite and now does not is a regression this target
		// would otherwise report as a pass, because comparing two runs that both
		// fail says nothing about whether either should have.
		for _, err := range []error{wholeErr, pieceErr} {
			if err != nil && !expectedFailure(err) {
				t.Fatalf("rewriting %q failed with an error nothing here should provoke: %v\n"+
					"either the input is legitimately refused - in which case add the sentinel "+
					"to expectedFailure - or a rewrite that should have worked did not", in, err)
			}
		}

		// The output is compared only when there is a whole document to compare.
		// What a failed rewrite has already handed to the destination is a
		// prefix cut off at the token that failed, and this harness makes no
		// claim about where the two runs stop flushing.
		if wholeErr == nil && !bytes.Equal(whole.Bytes(), pieces.Bytes()) {
			t.Fatalf("chunking changed the output for %q:\n whole:    %q\n bytewise: %q",
				in, whole.String(), pieces.String())
		}
		// Everything below runs whether or not the rewrite failed, which is the
		// half that used to be skipped. Teardown after an error is the path a
		// binding leaks on - it is the one the ordinary Close does not take -
		// so the failing iterations are the ones worth asserting on, not the
		// ones to return early from.
		//
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

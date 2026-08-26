package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func longTag() string {
	return `<div ` + strings.Repeat(`data-x="1" `, 500) + `>x</div>`
}

// widestGap feeds a document one byte at a time and returns the largest number of bytes the input
// was ahead of the output - which is what the rewriter was holding.
func widestGap(t *testing.T, doc string, opts ...lolhtml.Option) int64 {
	t.Helper()
	var rewritten, verbatim strings.Builder
	s, err := Tee(strings.NewReader(doc), &rewritten, &verbatim, 1, opts...)
	if err != nil {
		t.Fatalf("Tee: %v", err)
	}
	if verbatim.String() != doc {
		t.Fatalf("the verbatim copy differs from the input")
	}
	return s.WidestGap
}

// TestAStartTagIsHeldUntilEverySelectorIsRuledOut, which is the whole measurement: the gap between
// the two destinations is what the rewriter holds, and what it holds depends on the selectors
// rather than on whether any handler ran.
func TestAStartTagIsHeldUntilEverySelectorIsRuledOut(t *testing.T) {
	doc := longTag()
	nothing := func(*lolhtml.Element) error { return nil }

	for _, tt := range []struct {
		selector string
		held     bool
	}{
		// The tag name rules these out at the name, so the rest of the tag streams.
		{"a[href]", false},
		{"span[data-x]", false},
		{"p", false},

		// These cannot be ruled out until the tag ends, whether they match or not.
		{"div[data-x]", true},
		{"div[data-absent]", true},
		{"div.absent", true},
		{"div#absent", true},
		{"[data-absent]", true},
		{"*", true},
		{"div", true},
	} {
		gap := widestGap(t, doc, lolhtml.OnElement(tt.selector, nothing))
		// Held means the gap is most of the tag; not held means a handful of bytes.
		if held := gap > 1000; held != tt.held {
			t.Errorf("%s: gap %d bytes, held = %v, want %v", tt.selector, gap, held, tt.held)
		}
	}

	// With no handlers at all the tag streams, which is the floor.
	if gap := widestGap(t, doc); gap > 64 {
		t.Errorf("with no handlers the gap was %d bytes", gap)
	}

	// A selector ruled out by name costs the same as no handlers, which is the point: it is
	// the ruling out that matters, not the registration.
	byName := widestGap(t, doc, lolhtml.OnElement("a[href]", nothing))
	none := widestGap(t, doc)
	if byName > none+16 {
		t.Errorf("a ruled-out selector held %d bytes against %d with none", byName, none)
	}
}

// TestTextIsNeverHeld, whatever handlers are registered - so a document of prose runs the two
// destinations together however it is read.
func TestTextIsNeverHeld(t *testing.T) {
	doc := `<p>` + strings.Repeat("word ", 2000) + `</p>`
	for _, tt := range []struct {
		name string
		opts []lolhtml.Option
	}{
		{"no handlers", nil},
		{"a text handler", []lolhtml.Option{
			lolhtml.OnText("p", func(*lolhtml.TextChunk) error { return nil })}},
		{"an element handler", []lolhtml.Option{
			lolhtml.OnElement("p", func(*lolhtml.Element) error { return nil })}},
	} {
		if gap := widestGap(t, doc, tt.opts...); gap > 64 {
			t.Errorf("%s: the gap was %d bytes over a %d-byte text node",
				tt.name, gap, len(doc))
		}
	}

	// Accumulating the node is the caller's buffer rather than the rewriter's, and it is
	// worth telling apart: this holds nearly the whole document.
	held := widestGap(t, doc, lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
		if c.IsLastInTextNode() {
			return nil
		}
		c.Remove()
		return nil
	}))
	if held < int64(len(doc))/2 {
		t.Errorf("accumulating the node held only %d of %d bytes", held, len(doc))
	}
}

// TestTheVerbatimCopyIsTheInput, byte for byte, at every read size - which is what makes teeing the
// reader the whole implementation.
func TestTheVerbatimCopyIsTheInput(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<div><a href="/x">link</a><ul><li>a<li>b</ul></div>`,
		`<p>x</p><script>var a = 1 < 2;</script><style>.a > .b{}</style>`,
		`<!-- c --><table><tr><td>x</table>`,
		`<p attr="unfinished`,
		``,
		`just text`,
	} {
		for _, chunk := range []int{1, 3, 64, 4096} {
			var rewritten, verbatim strings.Builder
			s, err := Tee(strings.NewReader(doc), &rewritten, &verbatim, chunk, annotate())
			if err != nil {
				t.Fatalf("%q chunk %d: %v", doc, chunk, err)
			}
			if verbatim.String() != doc {
				t.Errorf("%q chunk %d: the verbatim copy differs:\n  %q",
					doc, chunk, verbatim.String())
			}
			if s.Verbatim != int64(len(doc)) || s.In != int64(len(doc)) {
				t.Errorf("%q chunk %d: counted %d in and %d verbatim for %d bytes",
					doc, chunk, s.In, s.Verbatim, len(doc))
			}
			// And the rewritten copy is what one rewriter alone would have produced.
			alone, err := lolhtml.RewriteString(doc, annotate())
			if err != nil {
				t.Fatal(err)
			}
			if rewritten.String() != alone {
				t.Errorf("%q chunk %d: the rewritten copy differs from a plain "+
					"rewrite:\n  %q\n  %q", doc, chunk, rewritten.String(), alone)
			}
		}
	}
}

// TestTheVerbatimCopyIsAheadWhenTheRewriteFails, which is the asymmetry a caller has to know
// about: there is no ordering that avoids it, so the counts have to say what happened.
func TestTheVerbatimCopyIsAheadWhenTheRewriteFails(t *testing.T) {
	doc := strings.Repeat(`<div><a href="/x">link</a> text</div>`, 50)

	var verbatim strings.Builder
	broken := &failAfter{limit: 100}
	s, err := Tee(strings.NewReader(doc), broken, &verbatim, 64, annotate())
	if err == nil {
		t.Fatal("a destination that fails did not produce an error")
	}
	if verbatim.Len() == 0 {
		t.Error("the verbatim copy got nothing, so there is no asymmetry to report")
	}
	if s.Verbatim <= s.Rewritten {
		t.Errorf("verbatim %d is not ahead of rewritten %d", s.Verbatim, s.Rewritten)
	}
	// The report is what makes a partial pair recognisable rather than surprising.
	if !strings.Contains(s.String(), "bytes in") {
		t.Errorf("report:\n%s", s)
	}

	// And a verbatim destination that fails stops the run before the rewriter is given the
	// bytes, so the two cannot diverge in the other direction by more than one read.
	var rewritten strings.Builder
	s2, err := Tee(strings.NewReader(doc), &rewritten, &failAfter{limit: 100}, 64, annotate())
	if err == nil {
		t.Fatal("a failing verbatim destination did not produce an error")
	}
	if s2.Verbatim < s2.Rewritten {
		t.Errorf("rewritten %d ran ahead of verbatim %d", s2.Rewritten, s2.Verbatim)
	}
}

// failAfter accepts limit bytes and then refuses.
type failAfter struct {
	limit, n int
}

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n >= f.limit {
		return 0, fmt.Errorf("the destination is gone")
	}
	n := min(len(p), f.limit-f.n)
	f.n += n
	if n < len(p) {
		return n, fmt.Errorf("the destination is gone")
	}
	return n, nil
}

// TestOneReadOfTheInput, which is the point of teeing rather than reading twice: a reader that can
// only be read once is the common case for a response body.
func TestOneReadOfTheInput(t *testing.T) {
	doc := strings.Repeat(`<div><a href="/x">y</a></div>`, 20)
	src := &onceOnly{r: strings.NewReader(doc)}
	var rewritten, verbatim strings.Builder
	if _, err := Tee(src, &rewritten, &verbatim, 64, annotate()); err != nil {
		t.Fatal(err)
	}
	if verbatim.String() != doc {
		t.Error("the verbatim copy differs")
	}
	if src.seeks != 0 {
		t.Errorf("the reader was seeked %d times", src.seeks)
	}
	_ = io.Discard
}

// onceOnly is a reader that records any attempt to go back.
type onceOnly struct {
	r     io.Reader
	seeks int
}

func (o *onceOnly) Read(p []byte) (int, error) { return o.r.Read(p) }

func (o *onceOnly) Seek(int64, int) (int64, error) {
	o.seeks++
	return 0, fmt.Errorf("this reader cannot seek")
}

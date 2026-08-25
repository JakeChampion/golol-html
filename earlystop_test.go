package lolhtml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// errStopHere is a caller's own sentinel, which is the shape this file is about: a handler
// returns something it can recognise later, and the question is what survives.
var errStopHere = errors.New("stop here")

// stopUnit is repeated to make the documents below, so a stopping position can be stated in
// units rather than in bytes.
const stopUnit = `<h2 id="s">head</h2><p>body text</p><!--c-->`

// stopRun feeds a document chunk bytes at a time until a write fails, and reports what
// happened.
type stopRun struct {
	out      string
	writeErr error
	closeErr error
	handles  int64
}

func runToStop(t *testing.T, doc string, chunk int, opts ...lolhtml.Option) stopRun {
	t.Helper()

	var out strings.Builder
	before := lolhtml.LiveHandles()
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		t.Fatal(err)
	}
	step := chunk
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	var r stopRun
	for i := 0; i < len(doc); i += step {
		if _, r.writeErr = w.Write([]byte(doc[i:min(i+step, len(doc))])); r.writeErr != nil {
			break
		}
	}
	r.closeErr = w.Close()
	r.out = out.String()
	r.handles = lolhtml.LiveHandles() - before
	return r
}

// rewriteOfPrefix is what a fresh rewriter, with a handler that stops at nothing, produces
// from the first n bytes of doc.
func rewriteOfPrefix(t *testing.T, doc string, n int) string {
	t.Helper()
	got, err := lolhtml.RewriteString(doc[:n],
		lolhtml.OnElement("h2", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestStoppingFromAHandlerLeavesARewriteOfAPrefix.
//
// A handler that returns an error ends the rewrite, and what has already reached the sink is
// not a truncation: it is byte for byte what a fresh rewriter produces from that many bytes of
// the input. No half-serialised element, no tag cut in the middle, and the unit whose handler
// stopped is not emitted at all.
//
// That is what makes stopping early usable rather than merely possible - a caller scanning a
// stream for something can keep or serve what it has. Measured for every handler kind that can
// stop, at write sizes from one byte to the whole document.
func TestStoppingFromAHandlerLeavesARewriteOfAPrefix(t *testing.T) {
	doc := strings.Repeat(stopUnit, 10)

	kinds := []struct {
		name string
		opt  func() lolhtml.Option
	}{
		{"an element handler", func() lolhtml.Option {
			n := 0
			return lolhtml.OnElement("h2", func(*lolhtml.Element) error {
				n++
				if n == 3 {
					return errStopHere
				}
				return nil
			})
		}},
		{"an end-tag handler", func() lolhtml.Option {
			n := 0
			return lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					n++
					if n == 3 {
						return errStopHere
					}
					return nil
				})
			})
		}},
		{"a text handler", func() lolhtml.Option {
			n := 0
			return lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				if c.Text() == "" {
					return nil
				}
				n++
				if n == 5 {
					return errStopHere
				}
				return nil
			})
		}},
		{"a comment handler", func() lolhtml.Option {
			n := 0
			return lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				n++
				if n == 3 {
					return errStopHere
				}
				return nil
			})
		}},
	}

	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			for _, chunk := range []int{0, 64, 7, 1} {
				r := runToStop(t, doc, chunk, k.opt())

				if !errors.Is(r.writeErr, errStopHere) {
					t.Fatalf("write size %d: write reported %v", chunk, r.writeErr)
				}
				if !errors.Is(r.closeErr, errStopHere) {
					t.Errorf("write size %d: close reported %v", chunk, r.closeErr)
				}
				if !errors.Is(r.closeErr, lolhtml.ErrPoisoned) {
					t.Errorf("write size %d: close did not report the poisoning: %v",
						chunk, r.closeErr)
				}
				if r.out == "" {
					t.Errorf("write size %d: nothing reached the sink", chunk)
				}
				if want := rewriteOfPrefix(t, doc, len(r.out)); r.out != want {
					t.Errorf("write size %d: the sink holds %d bytes that are not a rewrite "+
						"of that prefix:\n got  %q\n want %q", chunk, len(r.out), r.out, want)
				}
				if r.handles != 0 {
					t.Errorf("write size %d: %d handles survived the stop", chunk, r.handles)
				}
			}
		})
	}
}

// TestWhereAStopLandsIsAFactAboutTheDocument - for the handlers whose position is one.
//
// An element handler stops at the bytes before its element's start tag, and an end-tag handler
// before its end tag, whatever the write sizes. A text handler is different in kind, and the
// next test says so.
func TestWhereAStopLandsIsAFactAboutTheDocument(t *testing.T) {
	doc := strings.Repeat(stopUnit, 10)

	tests := []struct {
		name string
		want int
		opt  func() lolhtml.Option
	}{
		{"the third start tag", 2 * len(stopUnit), func() lolhtml.Option {
			n := 0
			return lolhtml.OnElement("h2", func(*lolhtml.Element) error {
				n++
				if n == 3 {
					return errStopHere
				}
				return nil
			})
		}},
		{"the third end tag", 2*len(stopUnit) + len(`<h2 id="s">head`), func() lolhtml.Option {
			n := 0
			return lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					n++
					if n == 3 {
						return errStopHere
					}
					return nil
				})
			})
		}},
		{"the third comment", 2*len(stopUnit) + len(`<h2 id="s">head</h2><p>body text</p>`), func() lolhtml.Option {
			n := 0
			return lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				n++
				if n == 3 {
					return errStopHere
				}
				return nil
			})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, chunk := range []int{0, 64, 7, 1} {
				r := runToStop(t, doc, chunk, tt.opt())
				if len(r.out) != tt.want {
					t.Errorf("write size %d stopped with %d bytes out, want %d",
						chunk, len(r.out), tt.want)
				}
			}
		})
	}
}

// TestAStopCountedInTextChunksIsNot, which is the exception and the reason to count nodes.
//
// A text handler runs once per chunk, and how many chunks a node arrives in depends on the
// write sizes - so "stop at the fifth chunk" names a different place in the document depending
// on the reader upstream. Counting to IsLastInTextNode instead makes the position the
// document's again, which the second half of this test shows.
func TestAStopCountedInTextChunksIsNot(t *testing.T) {
	doc := strings.Repeat(stopUnit, 10)

	byChunk := map[int]int{}
	for _, chunk := range []int{0, 64, 7, 1} {
		n := 0
		r := runToStop(t, doc, chunk, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() == "" {
				return nil
			}
			n++
			if n == 5 {
				return errStopHere
			}
			return nil
		}))
		byChunk[chunk] = len(r.out)
	}
	if byChunk[0] == byChunk[1] {
		t.Errorf("stopping at the fifth chunk landed at %d bytes both written whole and "+
			"written a byte at a time, which would make chunk counts write-invariant",
			byChunk[0])
	}

	// Counting nodes instead: the same place every time.
	byNode := map[int]int{}
	for _, chunk := range []int{0, 64, 7, 1} {
		nodes := 0
		r := runToStop(t, doc, chunk, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if !c.IsLastInTextNode() {
				return nil
			}
			nodes++
			if nodes == 5 {
				return errStopHere
			}
			return nil
		}))
		byNode[chunk] = len(r.out)
	}
	for chunk, got := range byNode {
		if got != byNode[0] {
			t.Errorf("stopping at the fifth text node landed at %d bytes with write size %d "+
				"and %d written whole", got, chunk, byNode[0])
		}
	}
}

// TestAfterAStopTheWriterRefusesAndRemembersWhy. A caller in a loop does not have to break out
// of it on the first error: the next write is refused, with the cause still findable.
func TestAfterAStopTheWriterRefusesAndRemembersWhy(t *testing.T) {
	doc := strings.Repeat(stopUnit, 10)

	var out strings.Builder
	n := 0
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("h2", func(*lolhtml.Element) error {
		n++
		if n == 2 {
			return errStopHere
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); !errors.Is(err, errStopHere) {
		t.Fatalf("the write that stopped reported %v", err)
	}
	before := out.Len()

	for i := 0; i < 3; i++ {
		_, err := w.Write([]byte(doc))
		if !errors.Is(err, lolhtml.ErrPoisoned) {
			t.Errorf("write %d after the stop reported %v, want the poisoning", i+2, err)
		}
		if !errors.Is(err, errStopHere) {
			t.Errorf("write %d after the stop lost the cause: %v", i+2, err)
		}
	}
	if out.Len() != before {
		t.Errorf("the refused writes added %d bytes to the sink", out.Len()-before)
	}
	if err := w.Close(); !errors.Is(err, errStopHere) {
		t.Errorf("close reported %v", err)
	}
}

// TestStoppingByNotWritingIsNotAnError, which is the other way to stop and the one to reach
// for when the condition is the caller's rather than the document's.
func TestStoppingByNotWritingIsNotAnError(t *testing.T) {
	doc := strings.Repeat(stopUnit, 10)

	for _, chunk := range []int{64, 7, 1} {
		var out strings.Builder
		seen := 0
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("h2", func(*lolhtml.Element) error {
			seen++
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		fed := 0
		for i := 0; i < len(doc) && seen < 3; i += chunk {
			n := min(i+chunk, len(doc)) - i
			if _, err := w.Write([]byte(doc[i : i+n])); err != nil {
				t.Fatalf("write size %d: %v", chunk, err)
			}
			fed += n
		}
		if err := w.Close(); err != nil {
			t.Errorf("write size %d: close reported %v", chunk, err)
		}
		if want := rewriteOfPrefix(t, doc, fed); out.String() != want {
			t.Errorf("write size %d: fed %d bytes and got %q, want %q",
				chunk, fed, out.String(), want)
		}
	}
}

// TestTheStopIsFindableThroughAWrappedError, because a handler usually has something to say
// beyond the sentinel and wrapping is how it says it.
func TestTheStopIsFindableThroughAWrappedError(t *testing.T) {
	doc := strings.Repeat(stopUnit, 4)

	r := runToStop(t, doc, 0, lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
		id, _ := e.Attribute("id")
		return fmt.Errorf("stopping at %q: %w", id, errStopHere)
	}))
	for _, err := range []error{r.writeErr, r.closeErr} {
		if !errors.Is(err, errStopHere) {
			t.Errorf("%v does not match the sentinel", err)
		}
		if !strings.Contains(err.Error(), `stopping at "s"`) {
			t.Errorf("%v lost the handler's own message", err)
		}
	}
}

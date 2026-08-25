package lolhtml_test

// MaxMemory does not bound the document. It bounds the copies the parser makes when a
// token straddles two writes, so the same document that passes under a kilobyte in one
// Write needs sixty-four in four-kilobyte writes - and a megabyte of HTML goes through
// a one-kilobyte limit without complaint if it arrives in one call.
//
// Which means the option is not a defence against a large body. Only bounding the input
// is that. What it defends against is a single enormous token, and what it is sensitive
// to is the write pattern, so a limit chosen with RewriteString will be wrong under
// io.Copy.

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// feedUnder writes doc in chunks of the given size (zero for one call) under a memory
// limit, and reports whether the rewrite completed.
func feedUnder(t *testing.T, doc string, limit, chunk int, handler bool) bool {
	t.Helper()
	opts := []lolhtml.Option{
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}),
	}
	if handler {
		opts = append(opts, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("class")
			return nil
		}))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	step := chunk
	if step <= 0 {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, err := w.Write([]byte(doc[i:min(i+step, len(doc))])); err != nil {
			w.Close()
			return false
		}
	}
	return w.Close() == nil
}

// TestAMegabytePassesAKilobyteLimitInOneWrite, which is the shape of the option: it
// bounds the parser's copies, not the document.
func TestAMegabytePassesAKilobyteLimitInOneWrite(t *testing.T) {
	// Fifty thousand small paragraphs: about a megabyte, with no token anywhere near
	// the limit.
	doc := strings.Repeat(`<p class="a">text</p>`, 50000)
	if len(doc) < 1<<20 {
		t.Fatalf("the document is %d bytes; this test wants a megabyte", len(doc))
	}

	if !feedUnder(t, doc, 1024, 0, true) {
		t.Errorf("a %d-byte document failed under a 1 KiB limit in one Write", len(doc))
	}
	// The same document in four-kilobyte writes does not, which is the warning the
	// option's documentation gives, quantified.
	if feedUnder(t, doc, 1024, 4096, true) {
		t.Errorf("the same document completed under 1 KiB in 4 KiB writes; the write " +
			"pattern was expected to matter")
	}
	// And it does complete once the limit covers the write.
	if !feedUnder(t, doc, 64<<10, 4096, true) {
		t.Errorf("the same document failed under a 64 KiB limit in 4 KiB writes")
	}
}

// TestTheLimitIsNotADefenceAgainstALargeBody, stated as the comparison a caller has to
// make: the limit that passes a megabyte in one call fails a much smaller document whose
// writes are bigger than the limit. The size of the body is not what it measures.
func TestTheLimitIsNotADefenceAgainstALargeBody(t *testing.T) {
	small := strings.Repeat(`<p class="a">text</p>`, 500)   // about 10 KiB
	large := strings.Repeat(`<p class="a">text</p>`, 50000) // about a megabyte

	if !feedUnder(t, large, 512, 0, false) {
		t.Errorf("a megabyte failed under 512 bytes in one Write")
	}
	if feedUnder(t, small, 512, 4096, false) {
		t.Errorf("10 KiB in 4 KiB writes completed under a 512-byte limit")
	}
	// So the two documents are the other way round from what the limit suggests: the
	// big one passes and the small one does not, because what is measured is the
	// write and not the body.
}

// TestTheWritePatternDecidesTheFloor, over a range of chunk sizes on one document, so
// the relationship is visible rather than anecdotal.
func TestTheWritePatternDecidesTheFloor(t *testing.T) {
	doc := strings.Repeat(`<p class="a">text</p>`, 5000)

	floor := func(chunk int) int {
		for limit := 8; limit <= 1<<20; limit *= 2 {
			if feedUnder(t, doc, limit, chunk, true) {
				return limit
			}
		}
		return 0
	}

	one := floor(0)
	small := floor(64)
	large := floor(4096)
	if one == 0 || small == 0 || large == 0 {
		t.Fatalf("no limit worked: one=%d 64=%d 4096=%d", one, small, large)
	}
	if !(one <= small && small <= large) {
		t.Errorf("floors are %d for one Write, %d for 64-byte writes and %d for 4096-byte "+
			"writes; they should not decrease as the writes grow", one, small, large)
	}
	if large <= one {
		t.Errorf("4096-byte writes need %d and one Write needs %d; the write size was "+
			"expected to cost something", large, one)
	}
}

// TestTheFloorIsNotAFunctionOfTheWriteSize, which is what makes "size the limit with
// the writes you will actually make" a measurement rather than a formula. Two documents
// of the same length, fed the same way, need different limits - and on one of them a
// larger write size needs a *smaller* limit, because what decides the floor is where
// the write boundaries fall relative to the tokens.
//
// Measured on 14 KB of paragraphs, with a handler on each:
//
//	write size      short paragraphs   long paragraphs
//	one Write                    832               832
//	64                           908               899
//	1024                        1868              1858
//	4095                        4930              4928
//	4096                        4930               832
//
// So the honest advice is to measure the floor with the write pattern that will be used,
// which is what examples/gip/bailout does with -floor.
func TestTheFloorIsNotAFunctionOfTheWriteSize(t *testing.T) {
	short := strings.Repeat(`<p class="a">text</p>`, 700)
	long := strings.Repeat(`<p class="a">paragraph with some text in it</p>`, 320)

	floorAt := func(doc string, chunk int) int {
		lo, hi := 8, 8
		for hi < 1<<22 && !feedUnder(t, doc, hi, chunk, true) {
			lo, hi = hi, hi*2
		}
		for lo+1 < hi {
			mid := (lo + hi) / 2
			if feedUnder(t, doc, mid, chunk, true) {
				hi = mid
			} else {
				lo = mid
			}
		}
		return hi
	}

	// One Write is the cheap case for both, and much cheaper than the middle of the
	// range: that part is stable.
	for _, doc := range []string{short, long} {
		one, middle := floorAt(doc, 0), floorAt(doc, 2048)
		if one >= middle {
			t.Errorf("one Write needs %d and 2048-byte writes %d, want the single write "+
				"to be cheaper", one, middle)
		}
	}

	// And the two documents do not agree, at some write size in the range: the floor
	// is not a function of the write size alone.
	agree := true
	for _, chunk := range []int{64, 256, 1024, 2048, 4096, 8192} {
		if floorAt(short, chunk) != floorAt(long, chunk) {
			agree = false
			break
		}
	}
	if agree {
		t.Errorf("two different documents of the same length needed the same limit at " +
			"every write size, which would make the floor derivable after all")
	}
}

// TestAnElementHandlerCostsAFixedAmountAndATextHandlerDoesNot, which is the other half
// of the floor: what the handlers are, rather than how the input arrives. Measured in one
// Write, where nothing is retained across boundaries at all.
func TestAnElementHandlerCostsAFixedAmountAndATextHandlerDoesNot(t *testing.T) {
	doc := strings.Repeat(`<p class="a">text</p>`, 400)

	floorWith := func(mk func() []lolhtml.Option) int {
		try := func(limit int) bool {
			opts := append([]lolhtml.Option{
				lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}),
			}, mk()...)
			w, err := lolhtml.NewWriter(io.Discard, opts...)
			if err != nil {
				return false
			}
			if _, err := w.Write([]byte(doc)); err != nil {
				w.Close()
				return false
			}
			return w.Close() == nil
		}
		lo, hi := 4, 4
		for hi < 1<<22 && !try(hi) {
			lo, hi = hi, hi*2
		}
		for lo+1 < hi {
			mid := (lo + hi) / 2
			if try(mid) {
				hi = mid
			} else {
				lo = mid
			}
		}
		return hi
	}

	none := floorWith(func() []lolhtml.Option { return nil })
	text := floorWith(func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil })}
	})
	read := floorWith(func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("class")
			return nil
		})}
	})
	write := floorWith(func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-x", "1")
		})}
	})

	if text != none {
		t.Errorf("a text handler needs %d and no handler %d, want the same", text, none)
	}
	if read <= none {
		t.Errorf("an element handler needs %d and no handler %d, want more", read, none)
	}
	if write != read {
		t.Errorf("a mutating handler needs %d and a reading one %d, want the same: the "+
			"cost is the element, not the edit", write, read)
	}
}

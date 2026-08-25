package lolhtml_test

import (
	"bufio"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// writeShapeSink records how the output was divided rather than what it said.
type writeShapeSink struct {
	calls int
	bytes int
	small int // writes of four bytes or fewer
}

func (c *writeShapeSink) Write(p []byte) (int, error) {
	c.calls++
	c.bytes += len(p)
	if len(p) <= 4 {
		c.small++
	}
	return len(p), nil
}

// writesFor rewrites doc in a single Write and reports how the destination was called.
func writesFor(t *testing.T, doc string, opts ...lolhtml.Option) *writeShapeSink {
	t.Helper()

	dst := &writeShapeSink{}
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// sinkAnchors is the page these tests use: one attribute to read, one to remove, and text.
const sinkAnchors = `<a href="/x" class="c">link</a>`

// TestMatchingSplitsTheOutputEvenWhenTheHandlerDoesNothing.
//
// The documentation said the number of writes a destination receives "is decided by what the
// rewrite does", and illustrated it with a mutation: one attribute set turning one write into
// twelve. Editing is not the whole of it. Matching alone splits the output, and a handler that
// does nothing at all costs two destination writes per element it matched:
//
//	200 anchors, one Write of 6200 bytes, and the destination sees
//	  no handlers                       1 write
//	  a selector matching nothing       1 write
//	  a handler that does nothing     400 writes
//	  the same, reading an attribute  400 writes
//	  an end-tag handler              600 writes
//	  RemoveAttribute                1200 writes
//	  SetAttribute                   2600 writes
//
// It matters for the case where nobody expects a cost: a read-only instrumentation pass over
// a rewrite that streams to an unbuffered destination turns one write per document into two
// per element. The output is identical - that part of the documentation is right - but the
// write pattern is not, and an unbuffered destination pays per write.
func TestMatchingSplitsTheOutputEvenWhenTheHandlerDoesNothing(t *testing.T) {
	const anchors = 200
	doc := strings.Repeat(sinkAnchors, anchors)

	plain := writesFor(t, doc)
	if plain.calls != 1 {
		t.Errorf("a passthrough of one Write cost %d destination writes, want 1", plain.calls)
	}

	unmatched := writesFor(t, doc,
		lolhtml.OnElement("span.nope", func(*lolhtml.Element) error { return nil }))
	if unmatched.calls != 1 {
		t.Errorf("a selector matching nothing cost %d writes, want 1", unmatched.calls)
	}

	empty := writesFor(t, doc, lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
	if empty.calls != 2*anchors {
		t.Errorf("a handler that does nothing cost %d writes over %d matches, want %d",
			empty.calls, anchors, 2*anchors)
	}

	reading := writesFor(t, doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("href")
		return nil
	}))
	if reading.calls != empty.calls {
		t.Errorf("reading an attribute cost %d writes and doing nothing %d",
			reading.calls, empty.calls)
	}

	// The bytes are unchanged, which is the half of the claim that was already right.
	if empty.bytes != plain.bytes || reading.bytes != plain.bytes {
		t.Errorf("the output changed size: %d and %d against %d",
			empty.bytes, reading.bytes, plain.bytes)
	}
}

// TestEditingMultipliesTheWritesFurther, and does it with tiny writes: a mutated start tag is
// re-serialised piece by piece, so most of what the destination receives is a byte or two.
func TestEditingMultipliesTheWritesFurther(t *testing.T) {
	const anchors = 200
	doc := strings.Repeat(sinkAnchors, anchors)

	empty := writesFor(t, doc, lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
	set := writesFor(t, doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "nofollow")
	}))
	removed := writesFor(t, doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.RemoveAttribute("class")
	}))

	if set.calls <= 3*empty.calls {
		t.Errorf("setting an attribute cost %d writes and doing nothing %d", set.calls, empty.calls)
	}
	if removed.calls <= empty.calls || removed.calls >= set.calls {
		t.Errorf("removing cost %d writes, doing nothing %d, setting %d",
			removed.calls, empty.calls, set.calls)
	}
	// Most of a mutating rewrite's writes are tiny.
	if set.small*2 < set.calls {
		t.Errorf("only %d of %d writes were four bytes or fewer", set.small, set.calls)
	}
}

// TestTheWriteCountIsPerMatchNotPerByte, so a page with twice the elements costs twice the
// writes and a page with twice the prose does not.
func TestTheWriteCountIsPerMatchNotPerByte(t *testing.T) {
	handler := func() lolhtml.Option {
		return lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "nofollow")
		})
	}

	hundred := writesFor(t, strings.Repeat(sinkAnchors, 100), handler())
	twoHundred := writesFor(t, strings.Repeat(sinkAnchors, 200), handler())
	if twoHundred.calls != 2*hundred.calls {
		t.Errorf("200 anchors cost %d writes and 100 cost %d", twoHundred.calls, hundred.calls)
	}

	// The same number of anchors with a great deal more prose between them costs the same
	// number of writes, plus the ones the prose itself is divided into.
	padded := writesFor(t, strings.Repeat(sinkAnchors+strings.Repeat("word ", 200), 100), handler())
	if padded.calls > hundred.calls*2 {
		t.Errorf("padding the page with prose took the writes from %d to %d",
			hundred.calls, padded.calls)
	}
}

// TestABufferCollapsesTheWrites to the number of buffer-fulls, which is why the library
// declines to buffer for the caller: the choice between latency to the first byte and writes
// per document is the caller's to make.
func TestABufferCollapsesTheWrites(t *testing.T) {
	doc := strings.Repeat(sinkAnchors, 200)

	direct := writesFor(t, doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "nofollow")
	}))

	dst := &writeShapeSink{}
	bw := bufio.NewWriterSize(dst, 4096)
	w, err := lolhtml.NewWriter(bw, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "nofollow")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}

	if dst.calls > 4 {
		t.Errorf("a 9200-byte output through a 4096-byte buffer cost %d writes", dst.calls)
	}
	if direct.calls < dst.calls*100 {
		t.Errorf("the buffer saved only %dx: %d writes against %d",
			direct.calls/max(dst.calls, 1), direct.calls, dst.calls)
	}
	if dst.bytes != direct.bytes {
		t.Errorf("the buffer changed the output: %d bytes against %d", dst.bytes, direct.bytes)
	}
}

// TestRemovingEverythingWritesNothing, which is the far end of the same rule: the destination
// hears from a rewrite only when there is output to hand over.
func TestRemovingEverythingWritesNothing(t *testing.T) {
	doc := strings.Repeat(sinkAnchors, 50)

	dst := writesFor(t, doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		e.Remove()
		return nil
	}))
	if dst.calls != 0 || dst.bytes != 0 {
		t.Errorf("a document of nothing but removed elements cost %d writes and %d bytes",
			dst.calls, dst.bytes)
	}
}

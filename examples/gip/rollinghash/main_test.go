package main

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// body builds a deterministic document big enough to be chunked, with enough
// variety that boundaries fall in different places rather than periodically.
func body(n int) string {
	var b strings.Builder
	x := uint32(12345)
	for i := range n {
		x = x*1664525 + 1013904223
		fmt.Fprintf(&b, `<div class="r%d"><a href="/p/%d">link %d</a><p>%s</p></div>`,
			x%97, i, i, strings.Repeat("word ", int(x%13)+1))
	}
	return b.String()
}

func TestSumIsFNVOfTheOutput(t *testing.T) {
	doc := body(50)
	var out bytes.Buffer
	d, err := Rewrite(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := fnv.New64a()
	want.Write(out.Bytes())
	if d.Sum != want.Sum64() {
		t.Errorf("Sum = %016x, want %016x", d.Sum, want.Sum64())
	}
	if d.Bytes != int64(out.Len()) {
		t.Errorf("Bytes = %d, want %d", d.Bytes, out.Len())
	}
}

// The digest is of the bytes, so nothing about how they arrive may change it.
// Both boundaries matter: how the input is written, and how the rewriter chooses
// to hand its output over.
func TestTheDigestIsInvariantToBoundaries(t *testing.T) {
	doc := body(60)
	opts := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		})}
	}

	want, err := Rewrite(io.Discard, strings.NewReader(doc), opts()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 7, 64, 4096} {
		got, err := Rewrite(io.Discard, &chunked{s: doc, n: n}, opts()...)
		if err != nil {
			t.Fatalf("input chunk %d: %v", n, err)
		}
		if got.Sum != want.Sum || got.Bytes != want.Bytes {
			t.Errorf("input chunk %d: sum %016x/%d bytes, want %016x/%d",
				n, got.Sum, got.Bytes, want.Sum, want.Bytes)
		}
		if !reflect.DeepEqual(got.Chunks, want.Chunks) {
			t.Errorf("input chunk %d: chunk boundaries moved", n)
		}
	}

	// And directly: the same bytes handed to the Hasher in different pieces.
	var whole bytes.Buffer
	if _, err := Rewrite(&whole, strings.NewReader(doc), opts()...); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 3, 100, 100000} {
		h := NewHasher(nil)
		for i := 0; i < whole.Len(); i += n {
			if _, err := h.Write(whole.Bytes()[i:min(i+n, whole.Len())]); err != nil {
				t.Fatal(err)
			}
		}
		got := h.Digest()
		if got.Sum != want.Sum || !reflect.DeepEqual(got.Chunks, want.Chunks) {
			t.Errorf("write size %d: digest differs from the streamed one", n)
		}
	}
}

// The point of a content-defined boundary: an insertion near the front moves one
// chunk and leaves the rest where they were. A fixed-size split would move every
// boundary after the insertion.
func TestAnInsertionAtTheFrontLeavesLaterChunksAligned(t *testing.T) {
	doc := body(1200)
	base, err := Rewrite(io.Discard, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	shifted, err := Rewrite(io.Discard, strings.NewReader(`<p>a banner</p>`+doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Chunks) < 4 {
		t.Fatalf("only %d chunks; the document is too small for this test", len(base.Chunks))
	}

	// How many chunk sizes agree, counting back from the end.
	shared := 0
	for shared < len(base.Chunks) && shared < len(shifted.Chunks) &&
		base.Chunks[len(base.Chunks)-1-shared] == shifted.Chunks[len(shifted.Chunks)-1-shared] {
		shared++
	}
	if shared < len(base.Chunks)-2 {
		t.Errorf("only %d of %d chunks stayed aligned after a 15-byte insertion; "+
			"content-defined chunking is meant to resynchronise", shared, len(base.Chunks))
	}
	// A fixed split would have moved everything, so this is only interesting if
	// the sizes are not all identical to begin with.
	distinct := map[int]bool{}
	for _, c := range base.Chunks {
		distinct[c] = true
	}
	if len(distinct) < 2 {
		t.Errorf("every chunk is the same size (%v); the boundaries are not "+
			"content-defined and this test proves nothing", base.Chunks)
	}
}

func TestChunkSizesRespectTheBounds(t *testing.T) {
	d, err := Rewrite(io.Discard, strings.NewReader(body(600)))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Chunks) < 2 {
		t.Fatalf("only %d chunks", len(d.Chunks))
	}
	// The last chunk is whatever is left, so it is exempt from the minimum.
	for i, c := range d.Chunks[:len(d.Chunks)-1] {
		if c < minChunk || c > maxChunk {
			t.Errorf("chunk %d is %d bytes, outside [%d, %d]", i, c, minChunk, maxChunk)
		}
	}
	total := 0
	for _, c := range d.Chunks {
		total += c
	}
	if int64(total) != d.Bytes {
		t.Errorf("chunks total %d, stream was %d", total, d.Bytes)
	}
}

// A rewrite that changes the output changes the digest, which is the only thing
// a caching caller needs it for.
func TestADifferentRewriteIsADifferentDigest(t *testing.T) {
	doc := body(20)
	plain, err := Rewrite(io.Discard, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := Rewrite(io.Discard, strings.NewReader(doc),
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Sum == rewritten.Sum {
		t.Error("adding an attribute to every link did not change the digest")
	}
	// And a handler that changes nothing does not change it either.
	touched, err := Rewrite(io.Discard, strings.NewReader(doc),
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			_, _ = e.Attribute("href")
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if touched.Sum != plain.Sum {
		t.Error("reading an attribute changed the output")
	}
}

// How many times the destination is written to depends on what the rewrite does,
// not on the document. These numbers are the reason the package comment tells a
// caller to put a bufio.Writer in front of a socket.
func TestTheWriteCountDependsOnTheRewrite(t *testing.T) {
	const doc = `<div class="row"><a href="/p">link</a></div>`
	tests := []struct {
		name   string
		opts   []lolhtml.Option
		writes int
	}{
		{"passthrough", nil, 1},
		{"a handler that matches", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil })}, 3},
		{"reading an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				_, _ = e.Attribute("href")
				return nil
			})}, 3},
		{"setting an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "noopener")
			})}, 12},
		{"removing an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				return e.RemoveAttribute("href")
			})}, 5},
	}
	for _, tt := range tests {
		d, err := Rewrite(io.Discard, strings.NewReader(doc), tt.opts...)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if d.Writes != tt.writes {
			t.Errorf("%s: %d writes, want %d", tt.name, d.Writes, tt.writes)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	d, err := Rewrite(io.Discard, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if d.Bytes != 0 || d.ChunkCount() != 0 || d.Writes != 0 {
		t.Errorf("empty input gave %+v", d)
	}
	if d.Sum != 0 {
		// fnv.New64a().Sum64() is the offset basis, and this Hasher starts
		// there too, so an empty stream is that value - not zero. The check is
		// that the two agree.
		if d.Sum != fnv.New64a().Sum64() {
			t.Errorf("Sum = %016x for an empty stream, want the FNV offset basis %016x",
				d.Sum, fnv.New64a().Sum64())
		}
	}
}

// A destination that fails must not be hidden by the wrapper.
func TestADestinationErrorSurfaces(t *testing.T) {
	_, err := Rewrite(failing{}, strings.NewReader(body(10)))
	if err == nil {
		t.Fatal("a failing destination reported nothing")
	}
	if !strings.Contains(err.Error(), "no room") {
		t.Errorf("err = %v, want the destination's message", err)
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errNoRoom }

var errNoRoom = fmt.Errorf("no room")

// A destination that accepts less than it was given breaks io.Writer's contract,
// and silently truncating the response is the failure that follows.
func TestAShortWriteIsReported(t *testing.T) {
	_, err := Rewrite(shortWriter{}, strings.NewReader(body(10)))
	if err == nil {
		t.Fatal("a short write reported nothing")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) <= 1 {
		return len(p), nil
	}
	return len(p) - 1, nil
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

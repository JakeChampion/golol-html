// Command rollinghash digests the rewritten output as it streams, without
// holding it.
//
// Two hashes, because they answer different questions and only one of them is
// rolling.
//
// A whole-stream digest (FNV-1a here) answers "is this the same output as last
// time" - an ETag, a cache key, a comparison between two rewrites. It depends on
// the bytes and not on how they arrive.
//
// A rolling hash over a sliding window answers "where should this stream be cut"
// - content-defined chunking, the thing that makes a delta useful when a page
// changes in the middle. Boundaries fall where the window's hash has a run of low
// bits, so inserting a banner near the top shifts one chunk and leaves the rest
// aligned, which a fixed-size split would not.
//
// The reason this program is worth writing against this library rather than any
// io.Reader is what wrapping the destination shows. The rewriter does not hand
// its output over in convenient pieces, and how many pieces there are depends on
// what the rewrite does rather than on the document:
//
//	<div class="row"><a href="/p">link</a></div>
//
//	passthrough                     1 write
//	a handler that matches          3 writes
//	the handler sets one attribute  12 writes
//
// because a mutated start tag is re-serialised piece by piece: "<", "a", " ",
// `href="/p"`, " ", "rel", `="`, "noopener", `"`, ">". Measured over 2000 such
// elements, one 132 KB write becomes 22,001 writes with a median size of one
// byte. Both hashes here are indifferent to that - which is the point of
// checking them against it - but a destination that is a socket or a file is
// not, and belongs behind a bufio.Writer.
package main

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Chunking parameters. The window is what the rolling hash sees at any moment;
// the mask decides how often a boundary falls, at one in mask+1 positions on
// random data, so 8191 gives chunks averaging 8 KB.
const (
	windowSize   = 64
	boundaryMask = 8191
	minChunk     = 1024
	maxChunk     = 65536
	// prime is the Rabin-Karp base. Any odd value works; this one is the usual
	// choice and keeps the removal factor cheap to precompute.
	prime = 16777619
)

// A Digest is what the stream added up to.
type Digest struct {
	// Sum is the FNV-1a 64 hash of every byte written.
	Sum uint64
	// Bytes is how many bytes were written.
	Bytes int64
	// Chunks are the sizes of the content-defined chunks, in order.
	Chunks []int
	// Writes is how many times the destination was written to, and the sizes
	// are what the rewriter chose rather than what the document contains.
	Writes int
	// SmallWrites counts the writes of eight bytes or fewer, which is the
	// number that says whether a destination wants buffering.
	SmallWrites int
}

// ChunkCount is the number of content-defined chunks, including the last
// partial one.
func (d Digest) ChunkCount() int { return len(d.Chunks) }

// A Hasher is an io.Writer that passes bytes through to a destination while
// digesting them.
//
// It holds one window of bytes, not the stream: everything it reports is
// computed as the bytes go past.
type Hasher struct {
	dst io.Writer
	d   Digest

	sum uint64

	// window is a ring buffer of the last windowSize bytes.
	window [windowSize]byte
	pos    int
	filled int

	rolling uint32
	// removal is prime^windowSize, the factor that takes the oldest byte back
	// out of the rolling hash.
	removal uint32

	// sinceBoundary is the size of the chunk being accumulated.
	sinceBoundary int
}

// NewHasher returns a Hasher writing through to dst. A nil dst discards the
// output, which is what a caller that only wants the digest passes.
func NewHasher(dst io.Writer) *Hasher {
	if dst == nil {
		dst = io.Discard
	}
	h := &Hasher{dst: dst, sum: fnv.New64a().Sum64(), removal: 1}
	for range windowSize {
		h.removal *= prime
	}
	return h
}

// fnvOffset and fnvPrime are FNV-1a 64's constants, used directly so the sum can
// be advanced byte by byte without an interface call per byte.
const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func (h *Hasher) Write(p []byte) (int, error) {
	if len(p) == 0 {
		// A rewriter may hand over an empty write; it is not a boundary and
		// not a write worth counting.
		return 0, nil
	}
	h.d.Writes++
	if len(p) <= 8 {
		h.d.SmallWrites++
	}
	for _, b := range p {
		h.step(b)
	}
	h.d.Bytes += int64(len(p))

	n, err := h.dst.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return len(p), nil
}

// step folds one byte into both hashes and decides whether a chunk ends here.
func (h *Hasher) step(b byte) {
	h.sum = (h.sum ^ uint64(b)) * fnvPrime

	// Roll: add the new byte, and once the window is full, remove the oldest.
	h.rolling = h.rolling*prime + uint32(b)
	if h.filled == windowSize {
		h.rolling -= h.removal * uint32(h.window[h.pos])
	} else {
		h.filled++
	}
	h.window[h.pos] = b
	h.pos = (h.pos + 1) % windowSize

	h.sinceBoundary++
	switch {
	case h.sinceBoundary < minChunk:
		// Too small to cut: a boundary here would make chunks that cost more to
		// track than they save.
	case h.sinceBoundary >= maxChunk, h.rolling&boundaryMask == 0:
		h.d.Chunks = append(h.d.Chunks, h.sinceBoundary)
		h.sinceBoundary = 0
	}
}

// Digest finishes the stream and returns what it added up to. It does not close
// the destination.
func (h *Hasher) Digest() Digest {
	d := h.d
	d.Sum = h.sum
	if h.sinceBoundary > 0 {
		d.Chunks = append(append([]int(nil), d.Chunks...), h.sinceBoundary)
	}
	return d
}

// Rewrite streams src through opts into dst, digesting the output.
func Rewrite(dst io.Writer, src io.Reader, opts ...lolhtml.Option) (Digest, error) {
	h := NewHasher(dst)
	w, err := lolhtml.NewWriter(h, opts...)
	if err != nil {
		return Digest{}, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return Digest{}, err
	}
	if err := w.Close(); err != nil {
		return Digest{}, err
	}
	return h.Digest(), nil
}

// Report renders a digest for a person.
func Report(d Digest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sum      %016x\n", d.Sum)
	fmt.Fprintf(&b, "bytes    %d\n", d.Bytes)
	fmt.Fprintf(&b, "chunks   %d", d.ChunkCount())
	if n := d.ChunkCount(); n > 0 {
		sizes := append([]int(nil), d.Chunks...)
		sort.Ints(sizes)
		fmt.Fprintf(&b, " (min %d, median %d, max %d)",
			sizes[0], sizes[len(sizes)/2], sizes[len(sizes)-1])
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "writes   %d, %d of them 8 bytes or fewer\n", d.Writes, d.SmallWrites)
	return b.String()
}

func main() {
	rel := len(os.Args) > 1 && os.Args[1] == "-rel"
	var opts []lolhtml.Option
	if rel {
		opts = append(opts, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}))
	}
	d, err := Rewrite(os.Stdout, os.Stdin, opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rollinghash:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, Report(d))
}

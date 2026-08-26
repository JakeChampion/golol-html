// Command etag computes an entity tag for a rewritten page, and does it without waiting for the
// page to be rewritten.
//
//	$ etag -version v3 page.html
//	ETag: "v3-4f8a1c9d2b7e6035"
//
//	$ etag -verify -version v3 page.html
//	ETag: "v3-4f8a1c9d2b7e6035"
//	  from              the input and the rewriter's name
//	  output hash       4f8a1c9d2b7e6035 matches: the rewrite is deterministic
//	  bytes             232806 in, 245806 out
//
// # An etag describes the output, so hash the input
//
// The output only exists as it is written, and an etag is a header, so hashing the output means
// either sending the header late or holding the body. Both are available and neither is needed: a
// rewrite is a function, so the same input through the same rewriter gives the same output, and
// hashing the input with the rewriter's identity names the output without producing it.
//
// Fastest of twenty runs over a 233 KB page on an M3 Pro, normalised to a plain rewriting pass:
//
//	approach                              time   relative   etag known
//	rewrite only (the baseline)         5.161ms      1.00x   -
//	hash the input, sha256, up front    5.296ms      1.02x   before the body
//	one pass, buffered, then hash       5.389ms      1.04x   before the body, holding it
//	hash the output, fnv64a, streaming  5.623ms      1.09x   after the body
//	hash the output, sha256, streaming  5.881ms      1.14x   after the body
//	two rewriting passes               10.473ms      2.03x   before the body
//
// Hashing the input is the cheapest row *and* the only one that is both up front and O(1) memory.
// The last row is why this is not the same problem as examples/gip/cachetags: there, the second
// pass could register no handlers and cost 0.08x, so two passes came to less than one. Here the
// second pass has to do the rewriting, because the rewriting is what the etag is about, so two
// passes cost twice.
//
// # What the etag has to include, and what it is trusting
//
// The rewriter's identity, because two rewriters over one input produce two pages. A version
// string is the whole of it: change the handlers, change the string, and every etag changes with
// them. Leaving it out is the bug this design invites - the page changes, the etag does not, and
// caches serve the old one until they are told otherwise.
//
// And it trusts the rewrite to be deterministic, which is a property of the handlers rather than
// of the library. Two ways to lose it, both easy: iterate a map inside a handler and write what
// comes out, or read a clock. -verify hashes the output as well and reports whether the two agree,
// which is how a rewrite that has stopped being a function announces itself. The tests do the same
// over a corpus and at every chunk size, since a rewrite whose output depends on how the input
// arrived is not deterministic either.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Tag is what a run produced.
type Tag struct {
	// Version identifies the rewriter. Two rewriters over one input are two pages, so this
	// is part of the tag rather than beside it.
	Version string
	// Input is the hash of the bytes that went in, which with Version names the output.
	Input string
	// Output is the hash of the bytes that came out, empty unless -verify asked for it.
	// It is not expected to equal Input - they are hashes of different bytes. What -verify
	// checks is that a second rewrite of the same input produces the same output hash.
	Output string
	// Deterministic is whether that second rewrite agreed, and is only meaningful when
	// Output is set.
	Deterministic bool
	// InputBytes and OutputBytes are the sizes, which a caller wants beside a validator.
	InputBytes, OutputBytes int64
}

// Value is the ETag header value, quoted as the specification requires.
func (t Tag) Value() string {
	return fmt.Sprintf("%q", t.Version+"-"+t.Input)
}

// Header is the whole header line.
func (t Tag) Header() string { return "ETag: " + t.Value() }

// Verified reports whether the rewrite was checked and found to be a function of its input.
func (t Tag) Verified() bool { return t.Output != "" && t.Deterministic }

func (t Tag) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", t.Header())
	fmt.Fprintf(&b, "  %-18s the input and the rewriter's name\n", "from")
	if t.Output != "" {
		if t.Deterministic {
			fmt.Fprintf(&b, "  %-18s %s, and a second rewrite of the same input "+
				"produced it again\n", "output hash", t.Output)
		} else {
			fmt.Fprintf(&b, "  %-18s %s, and a second rewrite of the same input "+
				"produced something else - the rewrite is not a function of its "+
				"input, so this etag is wrong\n", "output hash", t.Output)
		}
	}
	fmt.Fprintf(&b, "  %-18s %d in, %d out\n", "bytes", t.InputBytes, t.OutputBytes)
	return b.String()
}

// hashing wraps a writer and hashes what goes through it, counting the bytes.
type hashing struct {
	w io.Writer
	h hash.Hash
	n int64
}

func (x *hashing) Write(p []byte) (int, error) {
	n, err := x.w.Write(p)
	if n > 0 {
		x.h.Write(p[:n])
		x.n += int64(n)
	}
	return n, err
}

// newHash is the hash this uses. An etag is a cache key rather than a signature: it has to change
// when the bytes change and it does not have to resist an adversary choosing the bytes, so a fast
// non-cryptographic hash is the right tool and a cryptographic one is a per-request cost paid for
// nothing.
func newHash() hash.Hash { return fnv.New64a() }

// Rewrite copies src to dst through the rewriter, and returns the tag. The tag is complete before
// the first byte of the body is written, which is what makes it usable as a header.
//
// verify asks for the output to be hashed too, which costs a hash over the output and answers a
// question the design otherwise assumes: whether the rewrite is a function of its input.
func Rewrite(src io.Reader, dst io.Writer, version string, verify bool) (Tag, error) {
	if version == "" {
		return Tag{}, fmt.Errorf("etag: no rewriter version, and an etag without one does " +
			"not change when the rewriter does")
	}

	// The input has to be read before it is rewritten, because the tag comes first. For a
	// file that is a second open; for a stream it is a copy, and this takes the reader it is
	// given rather than deciding.
	input, err := io.ReadAll(src)
	if err != nil {
		return Tag{}, err
	}

	inputHash := newHash()
	inputHash.Write(input)
	t := Tag{
		Version:    version,
		Input:      hex.EncodeToString(inputHash.Sum(nil)),
		InputBytes: int64(len(input)),
	}

	// The output is hashed either way: it costs a hash over the bytes and it is the only way
	// to report the size, which a caller wants beside a validator.
	counted := &hashing{w: dst, h: newHash()}

	w, err := lolhtml.NewWriter(counted, annotate())
	if err != nil {
		return t, err
	}
	if _, err := w.Write(input); err != nil {
		w.Close()
		return t, err
	}
	if err := w.Close(); err != nil {
		return t, err
	}
	t.OutputBytes = counted.n
	if !verify {
		return t, nil
	}

	// The tag names the output without producing it, which assumes the rewrite is a function
	// of its input. Rewriting again is the only way to ask, so -verify is where a second pass
	// is paid for on purpose - and the comparison is output against output, not output
	// against input: those are hashes of different bytes.
	t.Output = hex.EncodeToString(counted.h.Sum(nil))
	second := &hashing{w: io.Discard, h: newHash()}
	w2, err := lolhtml.NewWriter(second, annotate())
	if err != nil {
		return t, err
	}
	if _, err := w2.Write(input); err != nil {
		w2.Close()
		return t, err
	}
	if err := w2.Close(); err != nil {
		return t, err
	}
	t.Deterministic = hex.EncodeToString(second.h.Sum(nil)) == t.Output
	return t, nil
}

// annotate is the rewrite. It is deliberately deterministic: no map iteration, no clock, no
// randomness - the three ways a handler stops being a function of its input.
func annotate() lolhtml.Option {
	return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		href, _ := e.Attribute("href")
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
			return e.SetAttribute("rel", "noopener")
		}
		return nil
	})
}

func main() {
	version := flag.String("version", "v1", "the rewriter's identity, part of the etag")
	verify := flag.Bool("verify", false, "rewrite twice and report whether the outputs agree")
	body := flag.Bool("body", false, "write the rewritten document after the header")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "etag:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	dst := io.Writer(io.Discard)
	if *body {
		dst = os.Stdout
	}
	t, err := Rewrite(src, dst, *version, *verify)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *verify {
		fmt.Print(t)
	} else {
		fmt.Println(t.Header())
	}
	if *verify && !t.Verified() {
		os.Exit(1)
	}
}

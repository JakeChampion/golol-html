// Command streamvsmemory runs the same rewrite twice - once in memory, once streamed -
// and reports what differs.
//
//	$ streamvsmemory -chunk 64 -floor < page.html
//	output            identical (4472 bytes)
//	element handlers  118 both ways
//	text handlers     240 in memory, 276 streamed
//	text nodes        120 both ways
//	comment handlers  2 both ways
//	doctype handlers  1 both ways
//	memory floor      832 bytes in memory, 905 bytes streamed
//
// Most of those lines are guarantees and two are not. The output is byte-identical
// however the input arrives, and so is the number of times an element, comment or
// doctype handler runs - and so is the number of text nodes, which is why this program
// counts them separately. A text handler is different: a text node is delivered in chunks
// whose boundaries follow the writes, so the same node arrives once in memory and several
// times when streamed - which is why anything accumulating text has to accumulate to
// [lolhtml.TextChunk.IsLastInTextNode] rather than treat a chunk as a node.
//
// The last line, printed only for -floor, is the memory limit, and it is where the two
// shapes really part company: a document that completes under a tight limit in one Write
// needs more when streamed, because a token that straddles two writes has to be copied.
// It is measured rather than estimated - the smallest MaxMemory under which the whole
// rewrite completes, found by bisection, in bytes - because a figure rounded up to a power
// of two is not a floor and is not a budget. A run that completes under no limit says so
// in words rather than reporting a number. See [lolhtml.MemorySettings].
//
// # Why compare at all
//
// Because the in-memory shape is the one people test with. RewriteString is the shortest
// way to try a rewrite, and every difference above is invisible in it: the text handler
// sees whole nodes, the memory limit looks generous, and a bug that only appears at a
// chunk boundary never fires. This program is the check that a rewrite tested one way
// behaves the other way, on a caller's own document and at a caller's own write size.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Counts is how often each handler kind ran.
type Counts struct {
	Elements int
	Texts    int
	Nodes    int // text nodes, counted by their boundary chunk
	Comments int
	Doctypes int
}

// Pass is one run.
type Pass struct {
	Name   string
	Chunk  int
	Output []byte
	Counts Counts
	Floor  int // the smallest MaxMemory that completes this way, if asked for
	Err    error
}

// Result is the comparison.
type Result struct {
	Memory    Pass
	Streamed  Pass
	Identical bool
	Diff      string
}

// OK reports whether the two runs agree about everything that is meant to be a guarantee.
func (r Result) OK() bool {
	return r.Identical &&
		r.Memory.Counts.Elements == r.Streamed.Counts.Elements &&
		r.Memory.Counts.Comments == r.Streamed.Counts.Comments &&
		r.Memory.Counts.Doctypes == r.Streamed.Counts.Doctypes &&
		r.Memory.Counts.Nodes == r.Streamed.Counts.Nodes &&
		r.Memory.Err == nil && r.Streamed.Err == nil
}

func (r Result) String() string {
	var b strings.Builder
	if r.Identical {
		fmt.Fprintf(&b, "output            identical (%d bytes)\n", len(r.Memory.Output))
	} else {
		fmt.Fprintf(&b, "output            DIFFERS: %s\n", r.Diff)
	}
	line := func(name string, a, c int) {
		if a == c {
			fmt.Fprintf(&b, "%-17s %d both ways\n", name, a)
			return
		}
		fmt.Fprintf(&b, "%-17s %d in memory, %d streamed\n", name, a, c)
	}
	line("element handlers", r.Memory.Counts.Elements, r.Streamed.Counts.Elements)
	line("text handlers", r.Memory.Counts.Texts, r.Streamed.Counts.Texts)
	line("text nodes", r.Memory.Counts.Nodes, r.Streamed.Counts.Nodes)
	line("comment handlers", r.Memory.Counts.Comments, r.Streamed.Counts.Comments)
	line("doctype handlers", r.Memory.Counts.Doctypes, r.Streamed.Counts.Doctypes)
	if r.Memory.Floor != 0 || r.Streamed.Floor != 0 {
		mem, streamed := limit(r.Memory.Floor), limit(r.Streamed.Floor)
		if mem == streamed {
			fmt.Fprintf(&b, "%-17s %s both ways\n", "memory floor", mem)
		} else {
			fmt.Fprintf(&b, "%-17s %s in memory, %s streamed\n", "memory floor", mem, streamed)
		}
	}
	return b.String()
}

// limit renders one memory floor. FloorNotFound is spelled out rather than
// printed as a number, because every number on this line is a budget somebody
// might copy.
func limit(n int) string {
	if n == FloorNotFound {
		return fmt.Sprintf("not found under %d bytes", MaxLimit)
	}
	return fmt.Sprintf("%d bytes", n)
}

// rewrite is the rewrite being compared: it sets an attribute on every paragraph and
// counts everything it is handed. Any rewrite would do; this one is edited enough to
// prove the output comparison means something.
func options(c *Counts) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			c.Elements++
			if e.TagName() == "p" {
				return e.SetAttribute("data-seen", "1")
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			c.Texts++
			if t.IsLastInTextNode() {
				c.Nodes++
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			c.Comments++
			return nil
		}),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
			c.Doctypes++
			return nil
		}),
	}
}

// run makes one pass, in one Write when chunk is zero.
func run(name string, doc []byte, chunk int, limit int) Pass {
	p := Pass{Name: name, Chunk: chunk}
	var out bytes.Buffer
	opts := options(&p.Counts)
	if limit > 0 {
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}))
	}
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		p.Err = err
		return p
	}
	step := chunk
	if step <= 0 {
		step = len(doc)
	}
	if step == 0 {
		step = 1
	}
	for i := 0; i < len(doc); i += step {
		end := i + step
		if end > len(doc) {
			end = len(doc)
		}
		if _, err := w.Write(doc[i:end]); err != nil {
			p.Err = err
			w.Close()
			p.Output = out.Bytes()
			return p
		}
	}
	if err := w.Close(); err != nil {
		p.Err = err
	}
	p.Output = out.Bytes()
	return p
}

// FloorNotFound is the Floor of a pass that completed under no limit up to
// MaxLimit. It is a different fact from a small floor and has to read as one: a
// zero there would be reported as "needs nothing".
const FloorNotFound = -1

// MaxLimit is the largest MaxMemory tried before giving up.
const MaxLimit = 1 << 24

// floor is the smallest MaxMemory that completes at this write size, exactly, or
// FloorNotFound.
//
// Doubling finds one limit that completes and one that does not; bisecting
// between them finds the boundary. The doubling alone would report a power of
// two, which is an upper bound of up to twice the real figure - a number to
// multiply a safety margin by, not a floor, and the whole point of this line is
// to size a MaxMemory budget from a caller's own document.
func floor(doc []byte, chunk int) int {
	lo, hi := 0, 8 // lo never completes, hi is not known to yet
	for {
		if p := run("floor", doc, chunk, hi); p.Err == nil {
			break
		}
		if hi >= MaxLimit {
			return FloorNotFound
		}
		lo, hi = hi, hi*2
	}
	for lo+1 < hi {
		mid := lo + (hi-lo)/2
		if p := run("floor", doc, chunk, mid); p.Err == nil {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// Compare runs both shapes.
func Compare(doc []byte, chunk int, findFloor bool) Result {
	res := Result{
		Memory:   run("in memory", doc, 0, 0),
		Streamed: run("streamed", doc, chunk, 0),
	}
	res.Identical = bytes.Equal(res.Memory.Output, res.Streamed.Output)
	if !res.Identical {
		res.Diff = difference(res.Memory.Output, res.Streamed.Output)
	}
	if findFloor {
		res.Memory.Floor = floor(doc, 0)
		res.Streamed.Floor = floor(doc, chunk)
	}
	return res
}

func difference(a, b []byte) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("first difference at byte %d", i)
		}
	}
	return fmt.Sprintf("%d bytes in memory against %d streamed", len(a), len(b))
}

func main() {
	chunk := flag.Int("chunk", 64, "write size for the streamed pass")
	findFloor := flag.Bool("floor", false, "also find the smallest memory limit each shape needs")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "streamvsmemory:", err)
		os.Exit(2)
	}
	res := Compare(doc, *chunk, *findFloor)
	fmt.Print(res)
	if res.Memory.Err != nil {
		fmt.Fprintln(os.Stderr, "in memory:", res.Memory.Err)
	}
	if res.Streamed.Err != nil {
		fmt.Fprintln(os.Stderr, "streamed:", res.Streamed.Err)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

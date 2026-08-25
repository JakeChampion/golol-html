// Command streamvsmemory runs the same rewrite twice - once in memory, once streamed -
// and reports what differs.
//
//	$ streamvsmemory -chunk 64 < page.html
//	output            identical (4,812 bytes)
//	element handlers  118 both ways
//	text handlers     41 in memory, 96 streamed
//	comment handlers  2 both ways
//	memory floor      5 in memory, 76 streamed
//
// Two of those lines are guarantees and one is not. The output is byte-identical however
// the input arrives, and so is the number of times an element, comment or doctype handler
// runs. A text handler is different: a text node is delivered in chunks whose boundaries
// follow the writes, so the same node arrives once in memory and several times when
// streamed - which is why anything accumulating text has to accumulate to
// [lolhtml.TextChunk.IsLastInTextNode] rather than treat a chunk as a node.
//
// The fourth line is the memory limit, which is where the two shapes really part company:
// a document that completes under a tiny limit in one Write can need a hundred times as
// much when streamed, because a token that straddles two writes has to be copied. See
// [lolhtml.MemorySettings].
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
	if r.Memory.Floor > 0 || r.Streamed.Floor > 0 {
		line("memory floor", r.Memory.Floor, r.Streamed.Floor)
	}
	return b.String()
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

// floor is the smallest power-of-two MaxMemory that completes at this write size.
func floor(doc []byte, chunk int) int {
	for limit := 8; limit <= 1<<24; limit *= 2 {
		if p := run("floor", doc, chunk, limit); p.Err == nil {
			return limit
		}
	}
	return 0
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

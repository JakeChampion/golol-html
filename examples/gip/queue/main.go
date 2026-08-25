// Command queue runs a rewriter per goroutine over a queue of documents, checks that no worker
// sees another's work, and says how much of the time went on building rewriters rather than on
// rewriting.
//
//	$ queue -workers 4 -items 400 -selectors 50 -size 1024
//	400 items of 1024 bytes, 4 workers, 50 selectors each
//	  cross-talk       none: every output equals the same document rewritten alone
//	  wall clock       13.3ms for the queue, 33.4µs per item across 4 workers
//	  worker time      114.1µs per item, of which building the rewriter 50.4µs (44%)
//	  work done        12620 elements rewritten across the queue
//	  advice           construction is a noticeable slice; worth checking the selector list
//
// A [lolhtml.Writer] cannot be reused: Close ends it, and there is no reset. So a queue pays
// the cost of building one per item, and that cost is a function of the selector list rather
// than of the document - about 8 allocations and three quarters of a microsecond per
// registered selector, whether the document is empty or sixteen kilobytes.
//
// Measured on an M3 Pro, one rewriter built and closed per document:
//
//	document      1 selector   10 selectors   50 selectors
//	nothing          1.7µs         8.2µs         38.4µs
//	30 bytes         2.3µs         8.6µs         38.4µs
//	1 KB            10.4µs        17.8µs         52.2µs
//	16 KB          141.4µs       153.4µs        252.9µs
//
// Read down a column and the construction is a constant; read across a row and it is the whole
// story for small documents. With fifty selectors, a queue of documents under about sixteen
// kilobytes spends more time building rewriters than rewriting, and there is nothing to
// amortise it against: the parsed selectors belong to the Writer and cannot be shared with the
// next one.
//
// # How many workers
//
// Fewer than you would think, and the number is a property of the machine rather than of the
// library, so -scan measures it instead of guessing:
//
//	$ queue -scan -items 400 -selectors 50 -size 1024
//	workers   wall clock   items/sec   speedup
//	      1       36.2ms       11049      1.00x
//	      2       21.1ms       18957      1.72x
//	      4       12.1ms       33057      2.99x
//	      8       19.2ms       20833      1.88x
//	     12       20.7ms       19323      1.75x
//
// Peak at four on a twelve-thread M3 Pro, and worse above it. A rewrite of a small document
// with a large rule set is mostly allocation - 406 allocations before a byte is written, with
// fifty selectors - and allocation-heavy work contends. The large-document case saturates
// instead of declining: 3.4x at four workers and the same at twelve. Either way the useful
// number is not the core count.
//
// # What a queue does not have to worry about
//
// Cross-talk, as long as the options are built per item. Each worker's handlers close over
// their own state, so nothing is shared: this program checks every output against the same
// document rewritten on its own, and a mismatch is a failure rather than a warning. What is
// not safe is building one [lolhtml.Option] at startup and handing it to every worker, because
// then the closure's state is shared - see the package documentation on reusing an Option.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Item is one document to rewrite, with the identity that lets a cross-talk check work: each
// item's document contains its own number, so an output that came from the wrong input says so.
type Item struct {
	N   int
	Doc string
}

// makeItems builds a queue of documents of about size bytes each, every one carrying its own
// number.
func makeItems(count, size int) []Item {
	items := make([]Item, count)
	for i := range items {
		unit := fmt.Sprintf(`<a href="/%d" class="c1">l%d</a>`, i, i)
		reps := size/len(unit) + 1
		items[i] = Item{N: i, Doc: strings.Repeat(unit, reps)}
	}
	return items
}

// options builds one worker's handlers over its own state. Selectors beyond the first match
// nothing: they are there to cost what a real rule set costs, which is the point being
// measured.
func options(selectors int) ([]lolhtml.Option, *int) {
	matched := 0
	opts := []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			matched++
			return e.SetAttribute("rel", "noopener")
		}),
	}
	for i := 1; i < selectors; i++ {
		opts = append(opts, lolhtml.OnElement(fmt.Sprintf(".c%d", i+1),
			func(*lolhtml.Element) error { return nil }))
	}
	return opts, &matched
}

// Outcome is what the run measured.
type Outcome struct {
	Items     int
	Size      int
	Workers   int
	Selectors int

	// Wall is elapsed time for the whole queue. BuildTime and ItemTime are sums across
	// the workers, so they are comparable with each other and not with Wall: four workers
	// spend four seconds of item time in one second of wall clock.
	Wall      time.Duration
	BuildTime time.Duration
	ItemTime  time.Duration
	CrossTalk int
	Matched   int
}

// PerItem is the wall-clock time divided by the queue length, which is what a caller sizing a
// queue wants.
func (o Outcome) PerItem() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.Wall / time.Duration(o.Items)
}

// BuildShare is the fraction of the work that went on building rewriters rather than
// rewriting. Both figures are sums over the items, so the ratio is meaningful however many
// workers were running.
func (o Outcome) BuildShare() float64 {
	if o.ItemTime == 0 {
		return 0
	}
	return float64(o.BuildTime) / float64(o.ItemTime)
}

// PerItemBuild and PerItemWork are the averages behind that ratio.
func (o Outcome) PerItemBuild() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.BuildTime / time.Duration(o.Items)
}

func (o Outcome) PerItemWork() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.ItemTime / time.Duration(o.Items)
}

// Run drives the queue: workers goroutines, each taking items and rewriting them with its own
// rewriter, and each output compared against the same document rewritten on its own.
func Run(items []Item, workers, selectors int) (Outcome, error) {
	if workers < 1 {
		return Outcome{}, errors.New("queue: at least one worker")
	}

	out := Outcome{Items: len(items), Workers: workers, Selectors: selectors}
	if len(items) > 0 {
		out.Size = len(items[0].Doc)
	}

	// The expected output per item, computed sequentially: this is the answer the workers
	// have to agree with, and computing it here rather than in the workers is what makes
	// it an independent check.
	want := make([]string, len(items))
	for i, item := range items {
		opts, _ := options(selectors)
		got, err := lolhtml.RewriteString(item.Doc, opts...)
		if err != nil {
			return out, err
		}
		want[i] = got
	}

	type result struct {
		n       int
		out     string
		matched int
		build   time.Duration
		total   time.Duration
		err     error
	}

	queue := make(chan Item)
	results := make(chan result, len(items))

	var wg sync.WaitGroup
	start := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				// A rewriter per item, because a Writer cannot be reused: this is
				// the cost the program is here to measure.
				itemStart := time.Now()
				buildStart := itemStart
				opts, matched := options(selectors)
				var sb strings.Builder
				w, err := lolhtml.NewWriter(&sb, opts...)
				build := time.Since(buildStart)
				if err != nil {
					results <- result{n: item.N, err: err, build: build,
						total: time.Since(itemStart)}
					continue
				}
				if _, err := w.Write([]byte(item.Doc)); err != nil {
					w.Close()
					results <- result{n: item.N, err: err, build: build,
						total: time.Since(itemStart)}
					continue
				}
				if err := w.Close(); err != nil {
					results <- result{n: item.N, err: err, build: build,
						total: time.Since(itemStart)}
					continue
				}
				results <- result{n: item.N, out: sb.String(), matched: *matched,
					build: build, total: time.Since(itemStart)}
			}
		}()
	}

	go func() {
		for _, item := range items {
			queue <- item
		}
		close(queue)
	}()

	wg.Wait()
	out.Wall = time.Since(start)
	close(results)

	seen := make([]bool, len(items))
	for r := range results {
		if r.err != nil {
			return out, fmt.Errorf("item %d: %w", r.n, r.err)
		}
		if seen[r.n] {
			return out, fmt.Errorf("item %d came back twice", r.n)
		}
		seen[r.n] = true
		out.BuildTime += r.build
		out.ItemTime += r.total
		out.Matched += r.matched
		if r.out != want[r.n] {
			out.CrossTalk++
		}
	}
	for i, ok := range seen {
		if !ok {
			return out, fmt.Errorf("item %d never came back", i)
		}
	}
	return out, nil
}

func (o Outcome) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d items of %d bytes, %d workers, %d selectors each\n",
		o.Items, o.Size, o.Workers, o.Selectors)
	if o.CrossTalk == 0 {
		fmt.Fprintf(&b, "  %-16s none: every output equals the same document rewritten alone\n",
			"cross-talk")
	} else {
		fmt.Fprintf(&b, "  %-16s %d of %d outputs were not what that document rewrites to\n",
			"cross-talk", o.CrossTalk, o.Items)
	}
	fmt.Fprintf(&b, "  %-16s %v for the queue, %v per item across %d workers\n", "wall clock",
		o.Wall.Round(100*time.Microsecond), o.PerItem().Round(100*time.Nanosecond), o.Workers)
	fmt.Fprintf(&b, "  %-16s %v per item, of which building the rewriter %v (%.0f%%)\n", "worker time",
		o.PerItemWork().Round(100*time.Nanosecond),
		o.PerItemBuild().Round(100*time.Nanosecond),
		o.BuildShare()*100)
	fmt.Fprintf(&b, "  %-16s %d elements rewritten across the queue\n", "work done", o.Matched)
	fmt.Fprintf(&b, "  %-16s %s\n", "advice", advice(o.BuildShare()))
	return b.String()
}

// advice turns the build share into the sentence a caller would otherwise have to work out.
func advice(share float64) string {
	switch {
	case share > 0.5:
		return "most of the time is construction: fewer selectors, or larger items, or both"
	case share > 0.2:
		return "construction is a noticeable slice; worth checking the selector list"
	default:
		return "construction is in the noise here"
	}
}

// Scan runs the same queue at several worker counts and reports the throughput of each, which
// is the only honest way to pick a number: the knee depends on the machine, the document size
// and the size of the rule set.
func Scan(items []Item, selectors int, counts []int) (string, error) {
	if len(items) == 0 {
		return "", errors.New("queue: nothing to scan")
	}
	type row struct {
		workers int
		wall    time.Duration
	}
	rows := make([]row, 0, len(counts))
	for _, n := range counts {
		out, err := Run(items, n, selectors)
		if err != nil {
			return "", err
		}
		if out.CrossTalk > 0 {
			return "", fmt.Errorf("%d workers: %d outputs were not what their document rewrites to",
				n, out.CrossTalk)
		}
		rows = append(rows, row{n, out.Wall})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d items of %d bytes, %d selectors each\n\n",
		len(items), len(items[0].Doc), selectors)
	fmt.Fprintf(&b, "%-9s %12s %11s %9s\n", "workers", "wall clock", "items/sec", "speedup")
	base := rows[0].wall
	best := rows[0]
	for _, r := range rows {
		perSec := float64(len(items)) / r.wall.Seconds()
		fmt.Fprintf(&b, "%-9d %12v %11.0f %8.2fx\n", r.workers,
			r.wall.Round(100*time.Microsecond), perSec, float64(base)/float64(r.wall))
		if r.wall < best.wall {
			best = r
		}
	}
	fmt.Fprintf(&b, "\nfastest at %d workers on this machine, for this document size and rule set\n",
		best.workers)
	return b.String(), nil
}

func main() {
	workers := flag.Int("workers", 4, "how many goroutines")
	items := flag.Int("items", 400, "how many documents in the queue")
	size := flag.Int("size", 1024, "size of each document in bytes")
	selectors := flag.Int("selectors", 50, "how many selectors each rewriter registers")
	scan := flag.Bool("scan", false, "run the queue at several worker counts and report the throughput of each")
	flag.Parse()

	if *items < 1 || *size < 1 || *selectors < 1 {
		fmt.Fprintln(os.Stderr, "queue: -items, -size and -selectors are counts, not zero")
		os.Exit(2)
	}

	if *scan {
		report, err := Scan(makeItems(*items, *size), *selectors, []int{1, 2, 4, 8, 12})
		if err != nil {
			fmt.Fprintln(os.Stderr, "queue:", err)
			os.Exit(1)
		}
		fmt.Print(report)
		return
	}

	out, err := Run(makeItems(*items, *size), *workers, *selectors)
	if err != nil {
		fmt.Fprintln(os.Stderr, "queue:", err)
		os.Exit(1)
	}
	fmt.Print(out)

	if out.CrossTalk > 0 {
		os.Exit(1)
	}
}

// Command queue runs a rewriter per goroutine over a queue of documents, checks that no worker
// sees another's work, and says how much of the time went on building rewriters rather than on
// rewriting.
//
//	$ queue -workers 4 -items 400 -selectors 50 -size 1024
//	400 items of 1050 bytes, 4 workers, 50 selectors each
//	  cross-talk       none: every output equals the same document rewritten alone
//	  wall clock       12.8ms for the queue, 32µs per item across 4 workers
//	  median item      101µs, of which building the rewriter 40.4µs (40%)
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
// # Why the median item and not the average
//
// Because the thing being timed is a few microseconds long and the operating system's
// scheduler is not. One item that loses its core for a millisecond adds more to the total than
// the other fifty-nine items put together, and it lands wherever it lands: if it lands in the
// build, the build looks like the whole cost, and if it lands in the rewrite, the build looks
// free.
//
// The spread that produces, measured on an M3 Pro over sixty 128-byte documents with a
// one-selector rule set and forty spinning processes for company - the same build, sixty times:
//
//	run   median build   95th   slowest   slowest over median
//	  1        1.708µs   21.5µs   41.5µs                  24x
//	  2        1.458µs    4.0µs   11.9µs                   8x
//	  3        4.125µs   28.4µs   92.1µs                  22x
//	  4        4.041µs   17.4µs   28.2µs                   7x
//	  5        1.875µs    9.4µs   29.8µs                  16x
//
// A mean over those samples is mostly the slowest one. Over twenty such runs the mean build
// share for a one-selector rule set ranged from 0.16 to 0.45 while the median stayed between
// 0.16 and 0.18.
//
// The same effect shows up in the report itself. Seven runs of the command at the top of this
// file, the first four on a machine with forty spinning processes and the last three on a quiet
// one:
//
//	wall clock   median item   build share
//	    40.9ms       235.2µs           34%
//	    33.8ms       223.3µs           36%
//	    31.2ms       225.1µs           36%
//	   146.1ms       274.5µs           36%
//	    12.0ms        90.6µs           39%
//	    13.2ms       102.2µs           41%
//	    12.8ms       101.0µs           40%
//
// The wall clock spans twelve-fold and the build share spans seven points, four of which are
// the load slowing the build and the rewrite by different amounts. That is what a figure worth
// printing looks like.
//
// The mean is not a noisier version of the same number - it is a different number, and which
// one it is depends on what else the machine was doing. A CI run of this program's own test
// reported 0.64 for one selector and 0.60 for fifty, which is the ordering backwards. So every
// per-item figure here is a median over the items, and the totals the medians came from are not
// reported at all: there is one answer, and it is the one that survives a loaded machine.
//
// The median is not free of the machine either - a slower CPU builds slower and rewrites
// slower - but it is free of the *load*, which is what makes two runs comparable. Ratios of two
// medians from the same run, which is what [Outcome.BuildShare] is, are the figures to trust.
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
	"slices"
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

	// Wall is elapsed time for the whole queue, so it is a figure per queue and not per
	// item: four workers spend four seconds of worker time in one second of wall clock.
	Wall      time.Duration
	CrossTalk int
	Matched   int

	// Builds and Totals hold one sample per item rather than a running total, because the
	// statistic worth reporting is the median and a total cannot be turned back into one.
	// See "Why the median item and not the average" above.
	Builds []time.Duration
	Totals []time.Duration
}

// PerItem is the wall-clock time divided by the queue length, which is what a caller sizing a
// queue wants.
func (o Outcome) PerItem() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.Wall / time.Duration(o.Items)
}

// BuildShare is the fraction of a typical item's time that went on building the rewriter
// rather than on rewriting: the median build over the median total, both from this run, so it
// is meaningful however many workers were running.
func (o Outcome) BuildShare() float64 {
	work := median(o.Totals)
	if work == 0 {
		return 0
	}
	return float64(median(o.Builds)) / float64(work)
}

// PerItemBuild and PerItemWork are the two medians behind that ratio.
func (o Outcome) PerItemBuild() time.Duration { return median(o.Builds) }

func (o Outcome) PerItemWork() time.Duration { return median(o.Totals) }

// median sorts a copy and takes the middle sample, the upper of the two for an even count. It
// does not average the middle pair: the point of the statistic is that it is one of the
// readings rather than a blend of them.
func median(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
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
		out.Builds = append(out.Builds, r.build)
		out.Totals = append(out.Totals, r.total)
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
	fmt.Fprintf(&b, "  %-16s %v, of which building the rewriter %v (%.0f%%)\n", "median item",
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

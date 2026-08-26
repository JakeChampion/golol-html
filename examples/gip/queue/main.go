// Command queue runs a rewriter per goroutine over a queue of documents, checks that no worker
// sees another's work, and says how much of the time went on building rewriters rather than on
// rewriting.
//
//	$ queue -workers 4 -items 400 -selectors 50 -size 1024
//	400 items of 1050 bytes, 4 workers, 50 selectors each
//	  cross-talk       none: every output equals the same document rewritten alone
//	  wall clock       11.9ms for the queue, 29.8µs per item across 4 workers
//	  build and close  6.3ms for the queue, 15.8µs per item (53%)
//	  allocations      495 per item, of which building and closing 415 (84%)
//	  work done        12620 elements rewritten across the queue
//	  advice           most of the time is construction: fewer selectors, or larger items, or both
//	  clock tick       41ns, fastest of 3 passes each
//
// Two shares, because they answer different questions and one of them needs a clock: see
// "Why two queues and not two stopwatches" and "The figure that does not need a clock".
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
// # Why two queues and not two stopwatches
//
// The first version of this program timed each item's build and each item's total with
// [time.Since] and reported the mean. That does not work, for two independent reasons, and both
// of them were found by CI rather than here.
//
// The first is that the thing being timed is a few microseconds long and the scheduler is not.
// One item that loses its core for a millisecond outweighs the other fifty-nine put together,
// and where the pause lands decides the answer: in the build, and the build looks like the
// whole cost; in the rewrite, and it looks free. Sixty 128-byte documents, one selector, on an
// M3 Pro with forty spinning processes for company - the same build, sixty times:
//
//	run   median build   95th   slowest   slowest over median
//	  1        1.708µs   21.5µs   41.5µs                  24x
//	  2        1.458µs    4.0µs   11.9µs                   8x
//	  3        4.125µs   28.4µs   92.1µs                  22x
//	  4        4.041µs   17.4µs   28.2µs                   7x
//	  5        1.875µs    9.4µs   29.8µs                  16x
//
// A mean over those samples is mostly the slowest one. Over twenty runs the mean build share
// ranged from 0.16 to 0.45 while the median of the same samples held between 0.16 and 0.18. A
// CI run reported 0.64 for one selector against 0.60 for fifty, which is the ordering backwards.
//
// The second reason is that a median does not save it. On the Windows runner every per-item
// figure came out as exactly zero: the clock's tick is coarser than an item, so sixty readings
// of a hundred microseconds each read zero, and the median of sixty zeros is zero. The mean had
// been hiding that - a sum of mostly-zero readings with the occasional tick in it is not zero,
// so it looked like a measurement. The program reports the tick it measured, because the number
// is a property of the platform and not of the library.
//
// So there is no per-item stopwatch here at all. The queue is run twice: once doing the work,
// and once building a rewriter per item and closing it without writing the document, which is
// the cost that cannot be amortised. Both are whole-queue intervals of milliseconds, thousands
// of ticks even on a coarse clock, and the build share is the ratio of the two. Each is run
// three times and the fastest kept, because preemption only ever adds time.
//
// Checked against the method it replaced, one worker on a quiet machine, and with the per-item
// stopwatch counting Close as part of the overhead so that both methods measure the same thing:
//
//	documents          selectors   two queues   per-item medians
//	400 x 1050 bytes          50        0.458              0.461
//	400 x 150 bytes           50        0.839              0.852
//	400 x 150 bytes            1        0.253              0.272
//	200 x 32 KB               50        0.027              0.028
//
// The agreement is the point: the per-item method could get the right answer, it just could not
// be relied on to, and on a coarse clock it could not get one at all.
//
// Two things the timed ratio does not say. It is a figure for the worker count it ran at - the
// overhead pass is pure allocation, which contends more than the mixed workload, so four
// workers put the same rule set at 0.51 where one worker puts it at 0.46 - and it is a ratio of
// totals, so it says nothing about the spread across items.
//
// And two passes are not always separable. They are separate runs, so whichever goes second pays
// for the first one's rubbish, and the work pass allocates far more: on the project's arm64
// runner the overhead pass came out *longer* than the work pass and the share was 1.33. The
// passes alternate which goes first and each starts from a collected heap now, which is the fix
// for the bias, and a share that still comes out at or above 1 is reported as two passes that
// could not be separated rather than printed. [Outcome.Resolvable] is the test, and the counted
// share below has none of this in it.
//
// # The figure that does not need a clock
//
// The report carries a second share, counted rather than timed: the mallocs an item cannot
// avoid, over the mallocs it makes in total. That number has no clock in it and no scheduler
// either. Eight runs of this program's own test against forty spinning processes:
//
//	                        allocation share   time share
//	200 x 128 B, 50 sel                0.963   0.831 to 0.909
//	200 x 128 B, 1 sel                 0.636   0.242 to 0.377
//	20 x 32 KB, 50 sel                 0.158   0.005 to 0.034
//
// The allocation shares did not move at all across the eight runs. The time share for a 32 KB
// document moved sevenfold.
//
// It is not the time share and does not pretend to be: allocation misses the per-byte parsing,
// which is most of a large document's time and almost none of its allocation, so it reads high
// wherever parsing dominates. What it does is rank - over six combinations of document size and
// rule set, measured, the two shares put them in exactly the same order:
//
//	document    selectors   allocation share   time share
//	150 bytes          50              0.963        0.825
//	1050 bytes         50              0.828        0.431
//	150 bytes           1              0.605        0.229
//	1050 bytes          1              0.243        0.042
//	32 KB              50              0.158        0.025
//	32 KB               1              0.012        0.002
//
// So the tests assert on the counted share, which holds on every platform and at any load, and
// on the timed share only where the clock can resolve the intervals - and say which one they
// skipped rather than passing quietly. A reader wanting to know what their own queue costs
// should read the timed figure; a reader comparing two rule sets should read the counted one.
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
	"math"
	"os"
	"runtime"
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

	// Wall is elapsed time for the whole queue and Overhead is elapsed time for the same
	// queue building a rewriter per item and closing it without writing anything - so
	// Overhead includes Close, which a rewrite cannot avoid either. Both are the fastest of
	// several passes. They are figures per queue rather than per item: four
	// workers spend four seconds of worker time in one second of wall clock.
	Wall     time.Duration
	Overhead time.Duration

	// Tick is the smallest interval this platform's clock reports, measured rather than
	// assumed. Both figures above have to be a large multiple of it to mean anything - see
	// "Why two queues and not two stopwatches".
	Tick time.Duration

	// AllocsBuild and AllocsFull are the same two things counted rather than timed: mallocs
	// per item for building and closing a rewriter, and for the whole item. A count is the
	// same number on every platform and at any load, so this is the figure to compare
	// between machines and the one the tests assert on.
	AllocsBuild float64
	AllocsFull  float64

	CrossTalk int
	Matched   int
	Passes    int
}

// BuildShare is the fraction of the queue's time that went on building rewriters rather than on
// rewriting: two whole-queue intervals from the same run, so it does not depend on the machine's
// speed, and neither of them is small enough for the clock to lose.
func (o Outcome) BuildShare() float64 {
	if o.Wall == 0 {
		return 0
	}
	return float64(o.Overhead) / float64(o.Wall)
}

// AllocShare is the same ratio counted rather than timed: the mallocs an item cannot avoid over
// the mallocs it makes in total. It is not the time share and does not try to be - allocation
// misses the per-byte parsing, which is most of a large document's time and almost none of its
// allocation - but it ranks rule sets and document sizes in the same order, measured over six
// combinations of size and selector count, and neither the clock nor the load can move it by
// more than a couple of allocations in four hundred and fifty.
func (o Outcome) AllocShare() float64 {
	if o.AllocsFull == 0 {
		return 0
	}
	return o.AllocsBuild / o.AllocsFull
}

// Resolvable reports whether the timed share is worth printing. Two things can stop it: a clock
// too coarse for the intervals, which is what a per-item figure hit on the Windows runner, and an
// overhead pass that did not come out shorter than the work pass, which is what the arm64 runner
// produced and which means the two could not be separated on that machine at that size. Neither
// is a failure of the program - they are what an honest instrument has to say rather than print a
// figure over 1.
func (o Outcome) Resolvable() bool {
	return o.Tick > 0 && o.Overhead > 20*o.Tick && o.Wall > 20*o.Tick && o.Overhead < o.Wall
}

// PerItem and PerItemBuild divide the two intervals by the queue length, which is what a caller
// sizing a queue wants. They are averages by construction - a whole-queue interval has no
// per-item samples in it to take a median over - and that is the trade the two-queue method
// makes: a figure the clock can resolve, in exchange for not knowing the spread.
func (o Outcome) PerItem() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.Wall / time.Duration(o.Items)
}

func (o Outcome) PerItemBuild() time.Duration {
	if o.Items == 0 {
		return 0
	}
	return o.Overhead / time.Duration(o.Items)
}

// clockTick measures the smallest interval this platform's clock reports, by reading it until
// the reading changes. It takes the smallest of several attempts, since a preemption between
// two reads inflates one.
func clockTick() time.Duration {
	best := time.Duration(0)
	for range 5 {
		start := time.Now()
		var d time.Duration
		for d == 0 {
			d = time.Since(start)
		}
		if best == 0 || d < best {
			best = d
		}
	}
	return best
}

// allocsPer counts mallocs per call of f, following the pattern the rest of this repository
// uses: a garbage collection first so the figure is not another goroutine's rubbish, one call
// outside the measurement so first-call initialisation is not attributed to it, and a count
// rather than an interval so the answer does not depend on the machine.
//
// Two details keep it nearly repeatable, which is the whole point of counting rather than
// timing. The measurement runs with one processor, the same thing [testing.AllocsPerRun] does,
// because the malloc counter is process-wide. And the answer is rounded, because the true figure
// is a whole number of allocations per item, so an average would leave room for a stray malloc
// to show up as a fraction of one.
//
// Nearly, not exactly: the malloc counter includes the runtime's own allocations, which depend
// on the state of the heap, so the figure for the same input moves a little and no amount of
// extra runs removes it. An M3 Pro moves by one allocation in four hundred; the project's macOS
// and Linux arm64 runners moved by two in four hundred and fifty. That is the same wobble
// alloc_test.go documents for the fixed part of a count, and it allows eight for it.
//
// Two allocations in four hundred and fifty does not move a share quoted to two figures, which
// is what this is for, and it is nothing like the two-and-a-half-fold spread a timing has.
func allocsPer(runs int, f func()) float64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	f()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return math.Round(float64(after.Mallocs-before.Mallocs) / float64(runs))
}

// fastest returns the smallest of the samples, which is the right statistic for an interval
// that noise can only lengthen.
func fastest(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	return slices.Min(samples)
}

// Run drives the queue: workers goroutines, each taking items and rewriting them with its own
// rewriter, and each output compared against the same document rewritten on its own.
//
// It drives the queue twice per pass and several passes over: once doing the work, and once
// building a rewriter per item and closing it without writing, which isolates the cost that
// cannot be amortised. Only the fastest pass of each is kept.
func Run(items []Item, workers, selectors int) (Outcome, error) {
	const passes = 3
	// A hundred runs, not twenty: measured, the count wobbles by one or two allocations at
	// twenty and is exactly repeatable at a hundred, because the few allocations of setup
	// stop mattering once they are spread that far.
	const allocRuns = 100

	if workers < 1 {
		return Outcome{}, errors.New("queue: at least one worker")
	}

	out := Outcome{Items: len(items), Workers: workers, Selectors: selectors,
		Tick: clockTick(), Passes: passes}
	if len(items) > 0 {
		out.Size = len(items[0].Doc)
	}

	// The expected output per item, computed sequentially: this is the answer the workers
	// have to agree with, and computing it here rather than in the workers is what makes
	// it an independent check. It is outside every timed interval.
	want := make([]string, len(items))
	for i, item := range items {
		opts, _ := options(selectors)
		got, err := lolhtml.RewriteString(item.Doc, opts...)
		if err != nil {
			return out, err
		}
		want[i] = got
	}

	var walls, overheads []time.Duration
	// The two passes alternate which goes first, and each starts from a collected heap,
	// because whichever runs second pays for the first one's rubbish. Without that the work
	// pass, which allocates far more, left the overhead pass holding the collection - and on
	// the arm64 runner the overhead pass came out *longer* than the work pass, which made the
	// share 1.33.
	for pass := range passes {
		workFirst := pass%2 == 0
		var wall, overhead time.Duration
		var matched, crossTalk int
		var err error

		run := func(want []string) (time.Duration, int, int, error) {
			runtime.GC()
			return drive(items, workers, selectors, want)
		}

		if workFirst {
			wall, matched, crossTalk, err = run(want)
			if err != nil {
				return out, err
			}
			overhead, _, _, err = run(nil)
		} else {
			overhead, _, _, err = run(nil)
			if err != nil {
				return out, err
			}
			wall, matched, crossTalk, err = run(want)
		}
		if err != nil {
			return out, err
		}
		walls = append(walls, wall)
		overheads = append(overheads, overhead)
		out.Matched, out.CrossTalk = matched, crossTalk
	}
	out.Wall, out.Overhead = fastest(walls), fastest(overheads)

	// And the same two things counted rather than timed, on one representative item and on
	// this goroutine alone, since the malloc counter is process-wide.
	if len(items) > 0 {
		doc := []byte(items[0].Doc)
		out.AllocsBuild = allocsPer(allocRuns, func() {
			opts, _ := options(selectors)
			var sb strings.Builder
			w, err := lolhtml.NewWriter(&sb, opts...)
			if err != nil {
				return
			}
			w.Close()
		})
		out.AllocsFull = allocsPer(allocRuns, func() {
			opts, _ := options(selectors)
			var sb strings.Builder
			w, err := lolhtml.NewWriter(&sb, opts...)
			if err != nil {
				return
			}
			if _, err := w.Write(doc); err != nil {
				w.Close()
				return
			}
			w.Close()
		})
	}
	return out, nil
}

// drive runs one pass of the queue and returns its elapsed time. With want non-nil each worker
// rewrites its document and the output is checked against want; with want nil each worker builds
// a rewriter and closes it without writing, so the pass measures construction and teardown and
// nothing else. The two differ only in the Write, which is what makes their difference the cost
// of the rewriting.
func drive(items []Item, workers, selectors int, want []string) (time.Duration, int, int, error) {
	type result struct {
		n       int
		out     string
		matched int
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
				opts, matched := options(selectors)
				var sb strings.Builder
				w, err := lolhtml.NewWriter(&sb, opts...)
				if err != nil {
					results <- result{n: item.N, err: err}
					continue
				}
				if want != nil {
					if _, err := w.Write([]byte(item.Doc)); err != nil {
						w.Close()
						results <- result{n: item.N, err: err}
						continue
					}
				}
				if err := w.Close(); err != nil {
					results <- result{n: item.N, err: err}
					continue
				}
				results <- result{n: item.N, out: sb.String(), matched: *matched}
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
	elapsed := time.Since(start)
	close(results)

	var matched, crossTalk int
	seen := make([]bool, len(items))
	for r := range results {
		if r.err != nil {
			return elapsed, 0, 0, fmt.Errorf("item %d: %w", r.n, r.err)
		}
		if seen[r.n] {
			return elapsed, 0, 0, fmt.Errorf("item %d came back twice", r.n)
		}
		seen[r.n] = true
		matched += r.matched
		if want != nil && r.out != want[r.n] {
			crossTalk++
		}
	}
	for i, ok := range seen {
		if !ok {
			return elapsed, 0, 0, fmt.Errorf("item %d never came back", i)
		}
	}
	return elapsed, matched, crossTalk, nil
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
	fmt.Fprintf(&b, "  %-16s %v for the queue, %v per item (%.0f%%)\n", "build and close",
		o.Overhead.Round(100*time.Microsecond),
		o.PerItemBuild().Round(100*time.Nanosecond),
		o.BuildShare()*100)
	fmt.Fprintf(&b, "  %-16s %.0f per item, of which building and closing %.0f (%.0f%%)\n",
		"allocations", o.AllocsFull, o.AllocsBuild, o.AllocShare()*100)
	fmt.Fprintf(&b, "  %-16s %d elements rewritten across the queue\n", "work done", o.Matched)
	if o.Resolvable() {
		fmt.Fprintf(&b, "  %-16s %s\n", "advice", advice(o.BuildShare()))
	} else if o.Overhead >= o.Wall && o.Wall > 20*o.Tick {
		fmt.Fprintf(&b, "  %-16s the two passes could not be separated here - building "+
			"alone took %v against %v for the whole queue, so the share is noise: "+
			"try more items\n", "advice",
			o.Overhead.Round(100*time.Microsecond), o.Wall.Round(100*time.Microsecond))
	} else {
		fmt.Fprintf(&b, "  %-16s this clock ticks every %v, which is too coarse for a "+
			"queue this short: try more items\n", "advice", o.Tick)
	}
	fmt.Fprintf(&b, "  %-16s %v, fastest of %d passes each\n", "clock tick", o.Tick, o.Passes)
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

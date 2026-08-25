package lolhtml_test

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// pipeStages builds a chain of writers, the last writing to dst and each earlier one writing
// to the next. It returns them in pipeline order, so writers[0] is the one to feed and the one
// to close first.
func pipeStages(t *testing.T, dst io.Writer, stages ...[]lolhtml.Option) []*lolhtml.Writer {
	t.Helper()

	writers := make([]*lolhtml.Writer, len(stages))
	next := dst
	for i := len(stages) - 1; i >= 0; i-- {
		w, err := lolhtml.NewWriter(next, stages[i]...)
		if err != nil {
			t.Fatal(err)
		}
		writers[i] = w
		next = w
	}
	return writers
}

// closePipe closes upstream first, which is the order each stage's tail needs.
func closePipe(writers []*lolhtml.Writer) error {
	var first error
	for _, w := range writers {
		if err := w.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// insertSpan is a first stage: it produces markup a selector could match.
func insertSpan() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.Prepend(`<span class="new">x</span>`, lolhtml.HTML)
	})}
}

// annotateNew is a second stage: it matches what the first produced.
func annotateNew() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(".new", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-seen", "yes")
	})}
}

// TestAWriterIsAnIOWriterSoStagesCompose - and the second stage matches markup the first
// produced, which one pass cannot do.
//
// The package documentation says a second pass is the answer for acting on produced markup,
// and describes it as the thing that stops a rewrite streaming, because the document has to be
// held. That is true of a second pass that needs what the first pass learned - a table of
// contents, a canonical URL from the body. It is not true of a second pass that is just
// another rewrite: a Writer is an io.Writer, so it can be another Writer's destination, and
// then both stages run at once and neither holds the document.
func TestAWriterIsAnIOWriterSoStagesCompose(t *testing.T) {
	const doc = `<p>text</p><p>more</p>`

	var out strings.Builder
	pipe := pipeStages(t, &out, insertSpan(), annotateNew())
	if _, err := pipe[0].Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := closePipe(pipe); err != nil {
		t.Fatal(err)
	}

	if want := 2; strings.Count(out.String(), `data-seen="yes"`) != want {
		t.Errorf("piped output annotated %d spans, want %d: %s",
			strings.Count(out.String(), `data-seen="yes"`), want, out.String())
	}

	// The same handlers in one rewriter do not, which is the deliberate absence of a
	// cascade rather than a defect.
	onePass, err := lolhtml.RewriteString(doc, append(insertSpan(), annotateNew()...)...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(onePass, "data-seen") {
		t.Errorf("one pass matched its own insertion: %s", onePass)
	}
}

// TestAPipelineDoesNotHoldTheDocument, which is the difference from the buffered kind of second
// pass and the reason to reach for it.
func TestAPipelineDoesNotHoldTheDocument(t *testing.T) {
	unit := `<a href="/x">link</a>`
	chunk := []byte(strings.Repeat(unit, 4096/len(unit)+1))

	peaks := map[int]uint64{}
	for _, mb := range []int{1, 4} {
		size := mb << 20

		var base, now runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&base)

		pipe := pipeStages(t, io.Discard,
			[]lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "nofollow")
			})},
			[]lolhtml.Option{lolhtml.OnElement("a[rel]", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-checked", "1")
			})})

		var peak uint64
		for written, writes := 0, 0; written < size; writes++ {
			n := min(4096, size-written)
			if _, err := pipe[0].Write(chunk[:n]); err != nil {
				t.Fatal(err)
			}
			written += n
			if writes%64 == 0 {
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > base.HeapAlloc && now.HeapAlloc-base.HeapAlloc > peak {
					peak = now.HeapAlloc - base.HeapAlloc
				}
			}
		}
		if err := closePipe(pipe); err != nil {
			t.Fatal(err)
		}
		peaks[mb] = peak
	}

	// Four times the document, the same working set. The tolerance is generous because a
	// heap high-water mark is a sampled number; what would fail this is holding the
	// document, which is four megabytes rather than a fraction of one.
	if peaks[4] > peaks[1]*2 {
		t.Errorf("piping 1 MB peaked at %.2f MB above the baseline and 4 MB at %.2f MB",
			float64(peaks[1])/(1<<20), float64(peaks[4])/(1<<20))
	}
}

// TestPipingCostsWhatBufferingCosts, in allocations - so the pipeline saves the document
// without paying for it elsewhere.
//
// Two stages cost about twice one, which is the price of parsing twice and is already
// documented. What is new is that arranging the second pass as a pipeline rather than as a
// buffer does not add to it.
func TestPipingCostsWhatBufferingCosts(t *testing.T) {
	requireRealAllocationCounts(t)

	doc := strings.Repeat(`<a href="/x">link</a>`, 400)
	stage1 := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "nofollow")
		})}
	}
	stage2 := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("a[rel]", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-checked", "1")
		})}
	}

	onePass := func() {
		w, err := lolhtml.NewWriter(io.Discard, append(stage1(), stage2()...)...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	piped := func() {
		pipe := pipeStages(t, io.Discard, stage1(), stage2())
		if _, err := pipe[0].Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := closePipe(pipe); err != nil {
			t.Fatal(err)
		}
	}
	buffered := func() {
		var mid strings.Builder
		w1, err := lolhtml.NewWriter(&mid, stage1()...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w1.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := w1.Close(); err != nil {
			t.Fatal(err)
		}
		w2, err := lolhtml.NewWriter(io.Discard, stage2()...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w2.Write([]byte(mid.String())); err != nil {
			t.Fatal(err)
		}
		if err := w2.Close(); err != nil {
			t.Fatal(err)
		}
	}

	onePass()
	piped()
	buffered()

	one := testing.AllocsPerRun(allocRuns, onePass)
	two := testing.AllocsPerRun(allocRuns, piped)
	buf := testing.AllocsPerRun(allocRuns, buffered)

	if two < one*1.5 || two > one*3 {
		t.Errorf("piping two stages cost %.0f allocations against %.0f for one pass, which is "+
			"not the documented doubling", two, one)
	}
	if two > buf {
		t.Errorf("piping cost %.0f allocations and buffering %.0f", two, buf)
	}
}

// TestTheOutputIsTheSameEitherWay, so a caller moving from a buffered second pass to a pipeline
// is not changing the answer.
func TestTheOutputIsTheSameEitherWay(t *testing.T) {
	doc := strings.Repeat(`<a href="/x">link</a><p>t</p>`, 20)

	var pipedOut strings.Builder
	pipe := pipeStages(t, &pipedOut, insertSpan(), annotateNew())
	if _, err := pipe[0].Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := closePipe(pipe); err != nil {
		t.Fatal(err)
	}

	mid, err := lolhtml.RewriteString(doc, insertSpan()...)
	if err != nil {
		t.Fatal(err)
	}
	bufferedOut, err := lolhtml.RewriteString(mid, annotateNew()...)
	if err != nil {
		t.Fatal(err)
	}

	if pipedOut.String() != bufferedOut {
		t.Errorf("piped gave %q and buffered gave %q", pipedOut.String(), bufferedOut)
	}
}

// TestCloseUpstreamFirst. Each stage's Close flushes into the next, so the downstream stage has
// to still be open. The wrong order truncates the tail and reports ErrClosed - which is at
// least loud, on a document that has a tail.
func TestCloseUpstreamFirst(t *testing.T) {
	// A document ending inside a token, so the upstream stage writes during Close.
	const doc = `<p>a</p`

	var right strings.Builder
	pipe := pipeStages(t, &right, insertSpan(), annotateNew())
	if _, err := pipe[0].Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := closePipe(pipe); err != nil {
		t.Fatalf("closing upstream first reported %v", err)
	}

	var wrong strings.Builder
	backwards := pipeStages(t, &wrong, insertSpan(), annotateNew())
	if _, err := backwards[0].Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := backwards[1].Close(); err != nil {
		t.Fatalf("closing the downstream stage reported %v", err)
	}
	err := backwards[0].Close()

	if !errors.Is(err, lolhtml.ErrClosed) {
		t.Errorf("the upstream Close reported %v, want ErrClosed", err)
	}
	if wrong.String() == right.String() {
		t.Errorf("both orders produced %q", right.String())
	}
	if !strings.HasSuffix(right.String(), "</p") {
		t.Errorf("the right order lost the tail as well: %q", right.String())
	}
}

// TestAnErrorCrossesEveryStage, with its identity, so a pipeline needs no error plumbing.
func TestAnErrorCrossesEveryStage(t *testing.T) {
	sentinel := errors.New("this stage says no")
	failing := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("p", func(*lolhtml.Element) error {
			return sentinel
		})}
	}
	nothing := func() []lolhtml.Option { return nil }

	for at := 0; at < 3; at++ {
		stages := [][]lolhtml.Option{nothing(), nothing(), nothing()}
		stages[at] = failing()

		var out strings.Builder
		pipe := pipeStages(t, &out, stages...)
		_, writeErr := pipe[0].Write([]byte(`<p>text</p>`))
		closeErr := closePipe(pipe)

		if !errors.Is(writeErr, sentinel) && !errors.Is(closeErr, sentinel) {
			t.Errorf("a failure in stage %d reported write=%v close=%v", at+1, writeErr, closeErr)
		}
	}
}

// TestTheDownstreamStageSeesBytesBeforeTheDocumentEnds, which is what "streaming" means here
// and what a buffered second pass cannot do.
func TestTheDownstreamStageSeesBytesBeforeTheDocumentEnds(t *testing.T) {
	var downstream int

	var out strings.Builder
	pipe := pipeStages(t, &out,
		[]lolhtml.Option{lolhtml.OnElement("*", func(*lolhtml.Element) error { return nil })},
		[]lolhtml.Option{lolhtml.OnElement("*", func(*lolhtml.Element) error {
			downstream++
			return nil
		})})

	if _, err := pipe[0].Write([]byte(`<p>one</p>`)); err != nil {
		t.Fatal(err)
	}
	if downstream == 0 {
		t.Error("the downstream stage saw nothing after the first write")
	}
	afterFirst := downstream

	if _, err := pipe[0].Write([]byte(`<div>two</div>`)); err != nil {
		t.Fatal(err)
	}
	if err := closePipe(pipe); err != nil {
		t.Fatal(err)
	}
	if downstream <= afterFirst {
		t.Errorf("the downstream stage saw %d elements after one write and %d after two",
			afterFirst, downstream)
	}
}

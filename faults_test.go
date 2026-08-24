package lolhtml_test

// Deterministic fault injection.
//
// This is the part of deterministic simulation testing that actually applies
// here. The full technique - a deterministic scheduler, a simulated clock,
// injected network and disk faults - assumes a concurrent, I/O-bound system,
// and this library has no internal concurrency, no timers and no I/O of its
// own. What does transfer is the useful core: one seed decides every variable
// in a scenario, so a failure is reported with the seed that reproduces it
// exactly rather than as an unrepeatable flake.
//
// A seed here chooses the document, how the input is chunked, when the
// destination writer starts failing, how tight the memory limit is, and whether
// a handler errors or panics on a particular invocation. The assertions are
// about what must hold however those land: the failure is reported rather than
// swallowed, the Writer refuses further work, Close stays safe and idempotent,
// and no cgo handle is leaked on any path.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var (
	errInjectedSink    = errors.New("injected sink failure")
	errInjectedHandler = errors.New("injected handler failure")
)

// failingWriter fails every write from the nth onwards, so the failure can land
// at the start, in the middle, or never.
//
// shortFrom is the other way a destination misbehaves: accepting some of the
// chunk and reporting no error. That breaks io.Writer's contract, and trusting
// it would truncate the response in silence, so the rewriter reports
// io.ErrShortWrite the way io.Copy does. It is in the scenario space because a
// silent truncation is exactly the failure a seeded run should be able to
// produce.
type failingWriter struct {
	buf       bytes.Buffer
	failAt    int // -1 never
	shortFrom int // -1 never: accept only shortAccept bytes from this write on
	shortN    int
	writes    int
	failed    bool
	shorted   bool
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.failAt >= 0 && w.writes > w.failAt {
		w.failed = true
		return 0, errInjectedSink
	}
	if w.shortFrom >= 0 && w.writes > w.shortFrom && len(p) > w.shortN {
		w.shorted = true
		n, _ := w.buf.Write(p[:w.shortN])
		return n, nil
	}
	return w.buf.Write(p)
}

type scenario struct {
	seed         uint64
	doc          string
	chunk        int
	sinkFailAt   int
	sinkShortAt  int // report a short write from this write on; -1 never
	sinkShortN   int // bytes to accept when writing short
	maxMemory    int
	handlerFail  int // fail on the nth element handler call; -1 never
	handlerPanic bool
}

func scenarioFor(seed uint64) scenario {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))

	docs := []string{
		`<!DOCTYPE html><html><body><div id="a"><p>hello</p><!--c--></div></body></html>`,
		`<div>` + strings.Repeat(`<a href="/x">link</a>`, 12) + `</div>`,
		`<p>` + strings.Repeat("text ", 40) + `</p>`,
		`<div><span>a</span><b>b</b><i>c</i></div>`,
		// Unclosed tag: forces the rewriter to buffer, which is what makes a
		// tight memory limit bite.
		`<div ` + strings.Repeat("a", 300),
	}

	s := scenario{
		seed:  seed,
		doc:   docs[r.IntN(len(docs))],
		chunk: 1 + r.IntN(64),
	}

	switch r.IntN(4) {
	case 0:
		s.sinkFailAt = r.IntN(4) // fails early
	case 1:
		s.sinkFailAt = 1 + r.IntN(20)
	default:
		s.sinkFailAt = -1
	}

	s.sinkShortAt = -1
	if s.sinkFailAt < 0 && r.IntN(3) == 0 {
		// Only when the sink is not already failing, so the two do not race to
		// be the reported cause.
		s.sinkShortAt = r.IntN(6)
		s.sinkShortN = r.IntN(4)
	}

	if r.IntN(3) == 0 {
		s.maxMemory = 32 + r.IntN(512)
	}

	switch r.IntN(6) {
	case 0:
		s.handlerFail = r.IntN(4)
	case 1:
		s.handlerFail = -1
		s.handlerPanic = true
	default:
		s.handlerFail = -1
	}
	return s
}

func (s scenario) String() string {
	return fmt.Sprintf("seed=%d chunk=%d sinkFailAt=%d sinkShortAt=%d/%d maxMemory=%d handlerFail=%d panic=%v",
		s.seed, s.chunk, s.sinkFailAt, s.sinkShortAt, s.sinkShortN, s.maxMemory,
		s.handlerFail, s.handlerPanic)
}

// run executes one scenario and returns whether it panicked as instructed.
func (s scenario) run(t *testing.T) {
	t.Helper()

	dst := &failingWriter{failAt: s.sinkFailAt, shortFrom: s.sinkShortAt, shortN: s.sinkShortN}
	calls := 0
	panicked := false

	mem := lolhtml.MemorySettings{PreallocatedParsingBuffer: 16}
	if s.maxMemory > 0 {
		mem.MaxMemory = s.maxMemory
		mem.GracefulBailOut = s.seed%2 == 0
	}

	opts := []lolhtml.Option{
		lolhtml.WithMemorySettings(mem),
		lolhtml.OnElement("div, a, p", func(e *lolhtml.Element) error {
			calls++
			if s.handlerPanic && calls == 2 {
				panic(errInjectedHandler)
			}
			if s.handlerFail >= 0 && calls > s.handlerFail {
				return errInjectedHandler
			}
			return e.SetAttribute("data-seen", fmt.Sprint(calls))
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { return c.SetText("x") }),
	}

	var writeErr, closeErr error

	func() {
		defer func() {
			if v := recover(); v != nil {
				if !errors.Is(v.(error), errInjectedHandler) {
					panic(v) // not ours: a real failure
				}
				panicked = true
			}
		}()

		w, err := lolhtml.NewWriter(dst, opts...)
		if err != nil {
			t.Fatalf("%v: NewWriter: %v", s, err)
		}
		// Deferred so a panicking handler still closes; Close is idempotent.
		defer w.Close()

		for i := 0; i < len(s.doc); i += s.chunk {
			end := min(i+s.chunk, len(s.doc))
			if _, writeErr = w.Write([]byte(s.doc[i:end])); writeErr != nil {
				break
			}
		}
		closeErr = w.Close()

		// Close must stay safe and quiet however the run went.
		if again := w.Close(); again != nil {
			t.Fatalf("%v: second Close returned %v, want nil", s, again)
		}

		// Writing after Close must be refused, not acted on.
		if _, err := w.Write([]byte("<p>after</p>")); err == nil {
			t.Fatalf("%v: Write after Close succeeded", s)
		}
	}()

	if panicked {
		return // a panicking handler has no error to report
	}

	failure := writeErr
	if failure == nil {
		failure = closeErr
	}

	// Whatever was injected must be reported rather than swallowed.
	switch {
	case dst.failed && failure == nil:
		t.Fatalf("%v: the destination writer failed but the rewrite reported nothing", s)
	case dst.shorted && failure == nil:
		t.Fatalf("%v: the destination accepted only part of a chunk but the rewrite "+
			"reported nothing, so the output is silently truncated", s)
	case s.handlerFail >= 0 && calls > s.handlerFail && failure == nil:
		t.Fatalf("%v: a handler failed but the rewrite reported nothing", s)
	}

	// And the error reported must be one that was actually injected, rather than
	// something invented on the way out. Which of them wins depends on what the
	// rewriter reached first, so any injected cause is acceptable.
	if failure != nil && s.maxMemory == 0 {
		switch {
		case errors.Is(failure, errInjectedHandler):
			if s.handlerFail < 0 {
				t.Fatalf("%v: reported a handler failure that was not injected", s)
			}
		case errors.Is(failure, errInjectedSink):
			if !dst.failed {
				t.Fatalf("%v: reported a sink failure that was not injected", s)
			}
		case errors.Is(failure, io.ErrShortWrite):
			if !dst.shorted {
				t.Fatalf("%v: reported a short write that did not happen", s)
			}
		default:
			t.Fatalf("%v: rewrite failed with %v, which is none of the injected faults",
				s, failure)
		}
	}
}

// TestSeededFaults sweeps deterministic scenarios. A failure names the seed, so
// it can be replayed exactly with TestSeededFaultReplay.
func TestSeededFaults(t *testing.T) {
	const seeds = 400

	before := settledHandles()
	for seed := range uint64(seeds) {
		s := scenarioFor(seed)
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { s.run(t) })
	}
	if after := settledHandles(); after > before {
		t.Errorf("leaked %d cgo handles across %d fault scenarios (%d -> %d)",
			after-before, seeds, before, after)
	}
}

// TestSeededFaultReplay re-runs a single scenario. Set the seed to whatever a
// failure reported:
//
//	go test -run 'TestSeededFaultReplay' -args -seed=1234
func TestSeededFaultReplay(t *testing.T) {
	const seed = 0 // replace with a reported seed to reproduce
	s := scenarioFor(seed)
	t.Logf("replaying %v", s)
	s.run(t)
}

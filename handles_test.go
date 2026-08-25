package lolhtml_test

import (
	"runtime"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// settledHandles reads the live handle count with no cleanups pending.
//
// LiveHandles counts the whole process, and a Writer the caller drops without
// closing releases its handles from a runtime.AddCleanup callback - which runs
// asynchronously, one GC cycle after the Writer becomes unreachable. So a test
// that samples the count before and after its own work can see the count fall,
// because a Writer abandoned by an earlier test was collected in between. That
// is what TestUnclosedWriterIsReclaimed leaves behind on purpose: 200 of them.
//
// Sampling through this reduces that noise: draining the queue before the window
// means nothing already queued can land inside it.
//
// It does not remove the noise, and an earlier version of this comment claimed
// it did. Draining runs the cleanups that are ready; it cannot collect an object
// that is still reachable when the window opens - a Writer from an earlier test
// left in a stack slot the compiler has not reused is exactly that, and it
// becomes collectable partway through this test instead. CI caught it: a test
// asserting equality reported "1 before, 0 after", which is not a leak and never
// could be. So the assertion has to be one-sided, which is what
// requireNoHandleLeak is for.
//
// Not used by the fuzz targets. They sample once per iteration, and three forced
// collections per iteration would cost more than the coverage is worth, so they
// check only for growth.
func settledHandles() int64 {
	// Three cycles rather than one: a cleanup is queued by one GC and executed
	// by the runtime's cleanup goroutine after it, so a single collection
	// returns before the queue has drained.
	for range 3 {
		runtime.GC()
		runtime.Gosched()
	}
	return lolhtml.LiveHandles()
}

// requireNoHandleLeak fails if the live handle count grew over the window that
// started at before.
//
// Only growth. A leak is handles this test created and did not release, and that
// can only push the count up. A count that fell means someone else's cleanup
// landed inside the window - the counter is process-wide, and nothing a test
// does can stop an object from an earlier test becoming collectable at an
// arbitrary moment. Asserting equality makes the gate fail on that, which is a
// false alarm on a number the test does not control.
func requireNoHandleLeak(t *testing.T, before int64) {
	t.Helper()
	if after := settledHandles(); after > before {
		t.Errorf("%d handles leaked (%d before, %d after)", after-before, before, after)
	}
}

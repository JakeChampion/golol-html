package lolhtml_test

import (
	"runtime"

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
// Sampling through this instead makes the reading deterministic. Draining the
// queue before the window means nothing from an earlier test can land inside it,
// which is what lets a leak assertion compare for equality: growth is a leak,
// and a decrease is no longer someone else's tidying arriving late, so it is a
// signal too.
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

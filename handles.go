package lolhtml

import (
	"runtime/cgo"
	"sync/atomic"
)

// Every Go value that crosses into Rust does so as a cgo handle, and each one
// has to be deleted exactly once: leak them and the payloads live as long as
// the process, delete one twice and the runtime panics. Neither failure is
// visible in a rewriter's output, so nothing about the rewritten HTML would
// ever reveal it.
//
// Routing every create and delete through this pair keeps a live count, which
// tests assert returns to its baseline. That turns "the handles were probably
// released" into a checkable post-condition, cheap enough to assert on every
// fuzz iteration.
//
// The counter is atomic because streaming drop callbacks may run on whichever
// thread lol-html happens to be using.
var liveHandles atomic.Int64

func newHandle(v any) cgo.Handle {
	h := cgo.NewHandle(v)
	liveHandles.Add(1)
	return h
}

func deleteHandle(h cgo.Handle) {
	h.Delete()
	liveHandles.Add(-1)
}

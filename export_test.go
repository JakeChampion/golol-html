package lolhtml

// LiveHandles reports how many cgo handles are currently outstanding. It exists
// for tests: a rewrite that finishes must leave the count where it started, and
// nothing about the rewritten HTML would reveal a leak otherwise.
func LiveHandles() int64 { return liveHandles.Load() }

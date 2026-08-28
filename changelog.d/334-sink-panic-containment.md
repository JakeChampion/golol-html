- A panic from the destination `io.Writer` is now contained the way a panic from
  a handler always was: parked at the callback boundary and re-raised from
  `Write` or `Close` on the caller's goroutine.

  The output sink is the one `//export`ed callback that runs user code without
  being a handler, and it called the destination with no recover around it. A
  destination that panicked therefore unwound through lol-html's own frames,
  which are built with `panic = "abort"` and carry no cleanup: the drop callbacks
  those frames own never ran, so their handles leaked, and the rewriter was then
  freed from underneath a write that had been abandoned mid-document. Reachable
  from ordinary code - `bytes.Buffer` panics with `bytes.ErrTooLarge` when it
  cannot grow, and `Rewrite` writes into one - and from any `http.ResponseWriter`
  that panics. `panic_test.go` now covers the destination alongside every
  handler.

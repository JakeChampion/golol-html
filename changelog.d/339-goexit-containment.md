<!-- bump: patch -->

- `runtime.Goexit` from a handler, a `StreamFunc` or the destination writer -
  which is what `t.Fatal`, `t.FailNow` and `t.Skip` do - now poisons the
  Writer and releases its native resources, the way a panic already did.

  A Goexit is not a panic, so the recover that parks a panic at the cgo
  boundary saw nothing, and the goroutine left through lol-html's frames with
  the Writer looking healthy: not poisoned, not closed. The caller's deferred
  `Close` - `Rewrite`'s own included - then ran lol-html's end on a rewriter
  abandoned in the middle of a write and reported nil for a document truncated
  at the point of exit, and every streaming insertion pending in the abandoned
  frame leaked its handle for the life of the process. `Close` now reports
  `ErrPoisoned`, and the handles are reclaimed when the Writer is released,
  which the cleanup does even for a Writer nobody closes. The Rust frames that
  were skipped are still skipped - nothing can run a destructor the unwinder
  did not, and LeakSanitizer puts what they owned at about 240 bytes an exit -
  so the rule stands: leave a handler by returning an error. Measured in
  panic_test.go, on every callback that runs user code, in every build but
  -asan, where the frames the exit skips leave the sanitizer's own stack
  poison behind and every later report is suspect.

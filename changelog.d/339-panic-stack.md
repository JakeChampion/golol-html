<!-- bump: patch -->

- `Writer.PanicStack` returns the stack the goroutine had when a panic from a
  handler, a `StreamFunc` or the destination writer was caught at the cgo
  boundary. A panic cannot unwind through lol-html's frames, so it is caught
  there and raised again from the `Write` or `Close` that was running - with
  the same value, but with a trace that begins at that `Write` or `Close`,
  because the frames that panicked were gone by then. With several handlers
  registered nothing in it said which one failed. The stack is taken inside
  the recover, where those frames still are, and it survives the re-raise and
  the caller's deferred `Close`. `Rewrite` and `RewriteString` own their
  Writer, so a panic through them keeps only the value.

  The panic is also raised exactly once now. It used to be recovered a second
  time on the way out of `Write`, to release the native resources before it
  continued, and re-raised, which printed the trace twice, "[recovered]" and
  all; the cleanup now keys on the same "call never returned" flag that
  catches a `runtime.Goexit`, and recovers nothing.

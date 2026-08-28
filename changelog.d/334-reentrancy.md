- `Writer.Write` and `Writer.Close` refuse a reentrant call with the new
  `ErrReentrant`, rather than re-entering lol-html from inside a handler.

  A handler runs in the middle of `lol_html_rewriter_write`, and lol-html has no
  idea it is being called. A nested `Write` handed the same rewriter to Rust
  twice - a second `&mut` alias - and corrupted the parser state, which surfaced,
  when it surfaced at all, as an internal consistency error against a document
  that was fine; the outer `Write` still reported success. A nested `Close` was
  worse: it finished the document and freed the rewriter and every handle
  underneath the call still running on them, which was demonstrated to crash
  inside the output sink of a rewriter that had already been freed.

  The reflex it refuses is stopping early from a handler. Return an error from
  the handler instead: `Write` reports it, and the Writer is left poisoned rather
  than half-freed. Nothing changes for a Writer driven the ordinary way - the
  guard is per call, not sticky, and a refused call leaves the Writer exactly as
  it found it, so the interrupted call still reports whatever it was going to.

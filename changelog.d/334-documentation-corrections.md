- Documentation corrections, each against the behaviour the tests already
  measure: the README's strict-mode trigger list gave the `<frameset>` set as the
  `<select>` set minus `<noframes>`, when it is that plus `<script>` and
  `<textarea>`; the README and `docs/gip/wontfix.md` said a retained unit
  "returns `ErrDetached`", when only a mutator does and a getter answers with a
  silent zero value; `SPEC.md` contradicted itself and the tests on what
  `SetAttribute` escapes, which is the double quote and nothing else;
  `known-behaviours.md` B16 still described the behaviour from before the
  raw-text guard and cited a test that no longer exists; `SPEC.md`'s layout and
  deferred sections named files that do not exist, the wrong `make` target, a
  stale platform list and a stale benchmark count; and `SourceLocation` carried
  two versions of the same paragraph, the first of which stated the claim the
  second corrects.

  New notes for behaviour that was only ever implicit: `Writer.Write` says that
  the destination is handed a view of lol-html's own buffer rather than a copy,
  so a destination that retains it holds a pointer into freed native memory;
  `NewWriter` says that the cleanup which frees a Writer dropped without `Close`
  cannot run at all if a handler closes over the Writer, because the handle table
  then keeps it reachable; `StreamFunc` says that none of the streaming
  insertions is checked for a raw-text breakout; and the README's memory section
  says that bounding an untrusted rewrite takes a limit, a bounded input and
  care with `OnEndTag`, rather than any one of the three.

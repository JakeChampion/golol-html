- `NewWriter`'s error paths free the rewriter builder before the selectors it
  accepted, which is the order lol-html asks for.

  The header is explicit - "Deallocate all dependant rewriter builders first and
  then use `lol_html_selector_free`" - and the success path honoured it. Both
  error paths did the reverse: the builder was freed by a `defer`, so it ran
  after the `release()` that frees the selectors. Reachable with two handlers
  whose second selector fails to parse, or with any valid selector plus a bad
  encoding. Today's upstream builder drop only releases borrowed references
  without dereferencing them, so nothing crashed; the ordering was a bet on an
  implementation detail against a documented contract, and it is now freed
  explicitly on each path instead.

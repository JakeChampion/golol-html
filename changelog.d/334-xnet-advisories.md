- `differential`'s `golang.org/x/net` moves from v0.35.0 to v0.55.0, clearing
  eight advisories in `x/net/html` - the package it uses as its second opinion
  on what a document means. Two of them are a quadratic parse and an infinite
  loop on hostile markup, which is not what you want in an oracle fed generated
  documents. Test-only either way: `differential` is a separate module with a
  `replace`, so nothing about it reaches a consumer's module graph.

  v0.55.0 rather than latest, and the ceiling is measured: v0.56.0 changes what
  the oracle says about a NUL in an attribute value, the `<select>` content
  model, and what a rename does to content, so v0.56.0 and later fail three
  tests here. Those are upstream conformance changes, so moving past v0.55.0
  means deciding which behaviour is right and rewriting the expectations rather
  than bumping a number. The reasoning is in `differential/go.mod` where the
  next person to try will see it.

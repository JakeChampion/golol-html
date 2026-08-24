# Measured and accepted behaviours

Behaviours that have been measured, written down, and pinned by a test. They are
not defects and must not be reported as new findings.

`Owner` says who could change it. `upstream` means the behaviour is lol-html's,
the version is pinned (W7), and the most a GIP can do is improve the test or the
documentation. `binding` means it is ours, it is accepted as it stands, and a
proposal to change it has to argue against the reason given.

Closing a row here, when the reason no longer holds, is a first-rate GIP. Adding
one is not: a row is added by the pull request that measured it, as part of the
change that pins it.

| # | Behaviour | Owner | Pinned by |
|---|---|---|---|
| B1 | Character references are never decoded on the way out. The href of `<a href="?a=1&amp;b=2">` reads as `?a=1&amp;b=2`. | upstream | `TestCharacterReferencesAreNotDecoded` |
| B2 | `SetAttribute` takes raw source text, not a literal value: only `"` is escaped, because only it would break the syntax. Content insertion with `Text` escapes fully. | upstream | `TestSetAttributeTakesRawSourceText` (properties) |
| B3 | A leading U+FEFF is dropped when an attribute value is read back. The value is serialised faithfully, and a U+FEFF in any other position survives. | upstream | `TestLeadingBOMIsStrippedOnRead` |
| B4 | Text chunk counts are not chunk-invariant: lol-html splits text at input chunk boundaries. Output is invariant; handler invocation counts are not. | upstream | `FuzzRewrite`, which compares output always and invocation counts only for structural handlers |
| B5 | Writes are quadratic at byte granularity while an unclosed tag is buffered, because each write rescans the pending buffer. | upstream | documented in README; the fuzz harness bounds input size and write count because of it |
| B6 | With `GracefulBailOut` false, exceeding `MaxMemory` delivers nothing to the sink: the response is broken. With it true, every input byte is preserved. | upstream | the memory-limit cases in `errors_test.go` |
| B7 | A handler panic does not unwind through Rust. It is caught at the boundary and re-raised on the goroutine that called `Write` or `Close`. | binding | the panic cases in `errors_test.go`. This is what made the v0.1.1 leak possible; the leak is fixed and the re-raise is kept |
| B8 | `withStream`'s own handle cleanup path is unreachable: the C API rejects only a NULL handler struct and the shim never passes one. Probed on v3.0.1, every `Stream*` method succeeds even on a void element. | binding | deliberately untested, recorded in SPEC.md |
| B9 | musl cannot be detected by build constraint, so it is selected by `-tags musl`. Getting it wrong fails at link time in both directions, loudly. | binding | `scripts/check-platforms.sh` |
| B10 | `-lunwind` is dropped on musl, because the c-api crate builds with `panic = "abort"` and Alpine does not ship libunwind. | binding | the Alpine job in `ci.yml` exists to falsify this; an undefined `_Unwind_*` there closes the row |
| B11 | `-lSystem -lc -lm` are dropped on darwin, because macOS links libSystem implicitly and naming it again makes `ld` warn on every consumer build. | binding | the darwin rows in `ci.yml` |
| B13 | Every selector-associated handler runs before every document-level handler for the same unit, whatever order the options were written in: `OnComment` before `OnDocumentComment`, `OnText` before `OnDocumentText`. The C API keeps the two in separate vectors, so the interleaving is lost before lol-html sees it. | upstream | `TestSelectorHandlersRunBeforeDocumentHandlers` |
| B12 | Rust builds are not bit-reproducible here: two `native` runs of the same commit produced darwin archives differing by about 1 KB. | upstream | `make verify` is a diff to read, not an equality assertion |

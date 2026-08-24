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
| B14 | Removal suppresses output, not handler dispatch: handlers still run for content inside a removed element, and their edits are discarded. | upstream | `TestHandlersInsideRemovedContentStillRun` |
| B15 | `Remove()` then an inner insertion (`Append`, `Prepend`, `SetInnerContent`) emits the content without the element's tags; the reverse order discards it. `IsRemoved()` cannot guard it, being true after `RemoveAndKeepContent` too, where appending is well defined. Upstream's `should_remove_content` is `pub(crate)` and not in the C API, so the binding cannot tell the two removals apart. | upstream | `TestInsertionAfterRemoveIsStillEmitted`, `TestRemovalOrderAcrossTwoHandlers` |
| B16 | Neither `ContentType` is correct inside `<script>` or `<style>`: `Text` escapes `<>&` into raw-text content where references are not decoded, corrupting it; `HTML` is verbatim, so `</script>` in the content ends the element. Escaping it properly is a JavaScript transformation, not an HTML one. | upstream | `TestTextIntoRawTextElementsIsCorruptedNotDecoded`, `TestHTMLIntoAScriptCanEndTheElement` |
| B17 | `Text` escapes exactly `<`, `>` and `&`. A NUL passes through as a literal zero byte, so it does not survive a round trip through any parser. | upstream | `TestTextEscapesExactlyThreeCharacters` |
| B18 | The WHATWG encoding labels are aliases: `iso-8859-1`, `latin1`, `ascii` and `us-ascii` all select windows-1252, so bytes 0x80-0x9F decode as printable characters rather than controls. Required by the standard and what browsers do. | upstream | `TestEncoding/latin-1_labels_are_windows-1252` |
| B19 | Handlers always see UTF-8 whatever the document's encoding; inserted content is taken as UTF-8 and encoded on the way out, with unrepresentable characters becoming numeric character references. UTF-16 labels are refused, because the rewriter must find ASCII markup in the byte stream. | upstream | `TestEncoding/unrepresentable_inserted_characters_become_references`, `TestEncoding/utf-16*_is_rejected` |
| B20 | With `GracefulBailOut` false, a memory bail-out delivers nothing when the document arrived in one `Write` and a truncated rewritten prefix when it arrived in chunks. The cut lands on an element boundary, so the output is well-formed HTML. | upstream | `TestMemoryBailOutReachesTheSink`, `TestTruncatedBailOutLooksLikeAValidDocument` |
| B21 | The memory a rewrite needs depends on the write pattern: one measured document completes at `MaxMemory` 1024 in a single write and needs 8192 in 256-byte writes. | upstream | `TestMemoryNeededDependsOnHowTheInputIsFed` |
| B22 | Strict mode aborts on a text-content tag opening inside `<select>` (`title`, `style`, `iframe`, `xmp`, `plaintext`, `noembed`, `noframes`, `noscript`) or inside `<frameset>` (the same minus `noframes`, legal there). `<script>` is allowed in a select; `select`, `textarea`, `input` and `keygen` end the context. The abort is mid-stream, so the response is truncated. | upstream | `TestStrictModeTriggers`, `TestStrictModeFailureTruncatesTheResponse` |
| B23 | With strict off, content from an ambiguous tag to its closing tag - or to end of input if unclosed - is treated as raw text, so no handler runs for it. A sanitiser silently passes it through. | upstream | `TestLenientModeHandsContentPastTheHandlers`, `TestLenientModeMissedRegionEndsAtTheClosingTag` |
| B24 | Without `WithESITags`, an `esi:` element is an ordinary container: its content runs to the next matching end tag, so replacing or removing an unclosed `<esi:include>` swallows the enclosing element's end tag. A trailing slash does not help - HTML ignores it on an element that is neither void nor foreign. | upstream, opt-in | `TestESITags` |
| B25 | Allocations per rewrite are `base + k x matches`, with base independent of document length: 1 per unit wrapper, 1 per string crossing the boundary, 0 for a `SourceLocation`, 4 per attribute for `AttributeList`/`Attributes`. Nothing is cached. | binding, gated | `alloc_test.go` |
| B26 | A `StreamFunc` runs when its content is emitted: it cannot see anything not yet parsed, and it is skipped entirely if the content is discarded (the element or an ancestor was removed). | binding/upstream | `TestStreamFuncCannotSeeLaterContent`, `TestStreamFuncIsSkippedWhenItsContentIsDiscarded` |
| B12 | Rust builds are not bit-reproducible here: two `native` runs of the same commit produced darwin archives differing by about 1 KB. | upstream | `make verify` is a diff to read, not an equality assertion |

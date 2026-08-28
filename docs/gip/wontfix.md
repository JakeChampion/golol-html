# Settled rulings

Proposals that have been considered and declined, with the reason. A GIP that
re-reports one of these is closed unread, so check here first.

Each entry is a *ruling*, not a description of current behaviour: the behaviours
themselves live in `known-behaviours.md`. A ruling can be reopened only by new
information, and the entry says what that information would have to be.

## W1. cgo is required; `CGO_ENABLED=0` is not supported in v1

The binding links a prebuilt static archive of lol-html. There is no pure-Go
path, and a Go build cannot run cargo, so there is nothing to fall back to.
The wasm/wazero backend that would allow it is deferred, not discarded, and
costs a host/guest crossing plus copies per handler call.

Reopened by: a concrete wazero design with measured per-crossing cost against
the cgo path, proposed as its own build tag rather than as a replacement.

## W2. No pure-Go HTML parsing

Use `golang.org/x/net/html` for that. This module is bindings.

## W3. A unit is invalid outside its handler

`*Element`, `*Comment`, `*TextChunk`, `*Doctype`, `*EndTag` and `*DocumentEnd`
are detached when the handler returns: a mutator then returns `ErrDetached`, and
a getter, having nowhere to put an error, answers with a zero value and says
nothing. lol-html guarantees the pointer for the duration of the call and no
longer. Copying out what you need is the documented path.

Reopened by: nothing. A wrapper that outlives its handler is a use-after-free.

## W4. A failed rewriter is poisoned

lol-html cannot resume after an error, so `Write` and `Close` return
`ErrPoisoned` afterwards. "Retry the rewrite" means building a new `Writer`.

## W5. Character references are not decoded

`TextChunk.Text`, `Comment.Text` and attribute values return raw source text.
A rewriter must be able to re-emit what it read. Pinned by
`TestCharacterReferencesAreNotDecoded`; see `known-behaviours.md`.

## W6. The root module stays dependency-free

`go.mod` has no `require` lines and must keep none. Test-only dependencies go
in their own module (`differential/`, `properties/`) so they never reach a
consumer's module graph. A proposal that adds a root dependency needs to argue
why every consumer should carry it.

## W7. The vendored archives are not rebuilt as part of a GIP

Upstream is pinned at lol_html v3.0.1 (C API crate 1.4.0), commit
`608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c`. Bumping it is the `native`
workflow's job and its own pull request, because it rebuilds seven archives and
changes 23.4 MB of committed binary. A defect whose fix is upstream is
reported upstream, pinned here by a test, and noted in `known-behaviours.md`.

## W8. Retired platforms

linux/arm, 32-bit anything and windows/arm64 are deferred, and no archive is
built for them. The guard in `unsupported.go` failing at type-check time with a
name that explains the gap is the intended experience.

## W9. `-msan` is not added to CI

It reports uninitialised reads in uninstrumented code, and the vendored archive
is entirely uninstrumented, so every finding would be false. ASan is kept
because lol-html uses the system allocator, which ASan interposes globally.

## W10. Full deterministic simulation testing

A deterministic scheduler and a simulated clock need concurrency and I/O to act
on, and this library has neither internally. The part that transfers, seed-driven
scenario generation, is already in `faults_test.go`.

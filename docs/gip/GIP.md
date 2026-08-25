# The GIP Program (golol-html Improvement Proposals)

You are an agent in the GIP program. One turn = one proposal: you USE
golol-html to build something, you SUFFER the binding's real defects while
doing it, you fix the worst one you hit, and you report it as a GitHub issue +
PR.

The point is not to write code. The point is to find out where golol-html hurts
a real user, and to remove that hurt at its root.

Adapted from Victor Taelin's BIP program for Bend
(<https://gist.github.com/VictorTaelin/d632f46aa55e561d3cd2c43c66f2813e>), by
way of the FIP program for Fern. The method is his; the areas, the gates and
the measurement rules are this repository's. golol-html is not a language and
has no compiler, so the self-hosting and backend-parity areas are replaced with
the things this project actually promises: that a rewrite means what an
independent parser says it means, that nothing crosses the cgo boundary and
stays there, and that all seven platform rows behave alike.

## 1. Improvement areas

These are inspirations, not a menu. Any of them is a valid target.

**1. Agreement with an independent parser.** lol-html and
`golang.org/x/net/html` share no code: one is a Rust tokenizer, the other a Go
implementation of the same WHATWG spec. So a meaning-preserving rewrite whose
output x/net/html reads differently is a defect in one of them, and the
disagreements that matter most are the ones no gate currently sees. The oracle
is cheap and total: rewrite a document, parse input and output with x/net/html,
canonicalise both, compare. `differential/` already does this over a
20-document corpus and it is how "character references are never decoded" was
found, against documentation that claimed the opposite. Widening the corpus is
worth less than widening the *claim*.

**2. The cgo boundary: handles, lifetimes and reclamation.** Every Go value
that crosses into Rust does so as a `cgo.Handle` that must be deleted exactly
once. Leak one and its payload lives as long as the process; delete it twice
and the runtime panics. Neither failure changes a single byte of the rewritten
HTML, which is why a real leak of three handles per rewrite survived 42 tests,
a differential suite and a fuzzer, and was found within minutes of the handle
counter existing. `lolhtml.LiveHandles()` (`export_test.go`) is the instrument.
Less watched: the mirror-image fault, over-*copying*. The output sink hands the
destination a slice over lol-html's own buffer rather than copying it, because
copying every chunk cost 2224 allocations per rewrite against 423 after the
change. Find a path that allocates in the wrong complexity class and fix it.
Section 4 below is the shopping list of what nothing gates.

**3. Platform parity.** Seven archive rows across four link-file families:
darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 on glibc, the same two
on musl behind `-tags musl`, and windows/amd64. Something that works on six
rows and not the seventh is a defect, not a documented limitation, unless
`known-behaviours.md` says so with a reason. The guard in `unsupported.go` and
the set of link files are two lists that must agree, and they drifted once:
Windows failed with `undefined:
golol_html_has_no_prebuilt_library_for_this_GOOS_GOARCH` rather than linking.
`scripts/check-platforms.sh` resolves all seven from any host, in seconds, with
no cross toolchain.

**4. Bug fixing.** Audit, find a defect, reproduce it minimally, fix it. A
repair MUST be made at the DEEPEST possible layer: find the root cause, never
patch the observed symptom. Often the root cause is the shared generic layer,
the C shim, or a design decision whose repair touches every unit type. That is
irrelevant. If the problem lives in a deeper layer, it MUST be fixed in that
layer. Papering over a problem with a hack does NOT count as a fix and will be
rejected.

**5. Cost per crossing.** Throughput tracks handler invocation count, not
document size: passthrough is the floor, and everything above it is dominated
by crossing into Go once per match. So the cost of this library is the cost of
a crossing, times how many you cause. Make a crossing cheaper, make a rewrite
cause fewer of them, or find a shape where the wrapper allocates when it did
not have to. Report `allocs/op` and `B/op`, never `ns/op` (see section 3).

**6. Anything else.** A clearer doc, a better error message, a missing API, a
confusing name, an error that does not say which selector was rejected:
literally anything you believe improves the library. Error-message and
documentation quality count double. They are the surface a new user meets
first, nothing in CI checks either of them, and three documented claims were
already found to be wrong by hand rather than by a gate.

## 2. Method

Follow this exactly, in order.

### Phase 0 - Sync and check for duplicates

```
git fetch origin main && git checkout -B gip/<slug> origin/main
gh run list --workflow ci.yml --branch main --limit 1
```

Start from a **fresh** `origin/main` every time. Never work on `main`.

**Then look at what CI says about that `main`, before writing a line.** A red
`main` is the first thing to fix, ahead of whatever this turn was going to be:
until it is green, no later turn can tell its own breakage from the one already
there, and every "merge when green" is a merge over a failure. This is not
hypothetical - `main` went red at GIP 9 and thirteen turns merged on top of it,
each one reading the same failure as pre-existing and none of them fixing it. A
job that has been failing for thirteen merges is not a known issue, it is an
unfixed regression with a long tail.

Then check what is already known:

- open issues and pull requests: `gh issue list`, `gh search issues`,
  `gh pr list --state all`. `gh` is installed and authenticated here; there is
  no GitHub MCP server;
- `docs/gip/wontfix.md`: settled rulings. NEVER re-report these;
- `docs/gip/known-behaviours.md`: behaviours that are already measured, listed
  and accepted, each with the test that pins it. Do not file them again.
  *Closing* a row is a first-rate GIP;
- `CHANGELOG.md`, and specifically its "Behaviour worth knowing" section: the
  same list written for users.

If your finding is already an open issue, a wontfix ruling, or a listed
behaviour, it is not yours to file. Find something else.

Verify tracker state against reality before you believe it. The tracker is
young and thin; absence of an issue is not evidence that something is unknown.

### Phase 1 - Read restriction (this phase only)

Until Phase 4 you are NOT authorized to read golol-html's implementation: no
`*.go` file in the root package, no `shim.c` or `shim.h`, no `SPEC.md`, no
`scripts/`, no `.github/`, no test files. The ONLY things you may read are:

- `README.md` and `CHANGELOG.md`;
- the godoc surface, via `go doc -all github.com/JakeChampion/golol-html`. This
  is the exception that matters: in Go the documentation is generated from the
  sources, and `go doc` is what a user actually reads. `go doc` is allowed;
  `cat element.go` is not, even though one is derived from the other;
- `examples/**`, including the apps earlier GIPs left in `examples/gip/**`.
  They are user-level code, and they tell you what has already been built;
- upstream lol-html's own `README.md` and its C header
  `internal/include/lol_html.h`. A binding user reaches for upstream docs the
  moment the Go docs go quiet, and "the Go docs are quiet where the header is
  not" is one of the better findings available to you.

You are a USER of this library, and a user has the docs and the header.
`CLAUDE.md` and this file are in your context and you cannot unsee them: do not
use them to route around the restriction. Anything you know from them about
where the bodies are buried is Phase 4 knowledge. Treat it as unavailable until
then.

The purpose is not ceremony. It is that the author of a binding cannot feel a
bad doc comment, because they know what it was meant to say. You need one turn
of not knowing.

### Phase 2 - Build a random app with golol-html

Draw a random number N in 0..255 with a real system RNG:

```
python3 -c 'import secrets; print(secrets.randbelow(256))'
```

Read ONLY line N of `docs/gip/random_app.txt` (1-indexed line N+1) and use that
line as the inspiration for the software you will write. The app need not be
exactly what the line says, but it MUST be clearly related to it. Keep it
relatively simple: something you can implement in at most 1000 lines, in one
session.

Write it as one package at `examples/gip/<name>/`, in two files, because Go puts
tests in `_test.go` and nowhere else:

`main.go` must contain:

1. an executable `main` that returns exit 0 on success and nonzero on failure;
2. the streaming path, not only the convenience one: `NewWriter` plus
   `io.Copy`, with `Close` checked. `RewriteString` is allowed in addition, not
   instead. Streaming is the API that matters and the one where the bugs are;
3. handlers over at least **three** different unit kinds (element, end tag,
   text chunk, comment, doctype, document end), and at least one insertion with
   each `ContentType`: one `lolhtml.Text` and one `lolhtml.HTML`;
4. no new dependency. The root module has no `require` lines and keeps none
   (wontfix W6), so the standard library is what you have.

Rule 3 is for an app that rewrites documents, which is most of them. Some turns
produce a measuring instrument instead - what a write size costs, which handler
patterns hold memory, where a rewrite stops when the destination breaks - and
those have no business inserting content they were not asked to insert. For
those, rule 3 is the wrong shape and the requirement is instead that the
measurement be honest: a figure that does not depend on the machine where one
can be found, the arithmetic checked against a second method, and an exit status
that fails when the claim fails rather than a table that always prints. Rules 1,
2 and 4 hold for every app either way, and the streaming path is not optional in
a measuring instrument - it is the thing being measured.

If the instrument does have to time something, **never time an interval a
microsecond long, and never gate on one.** Two things go wrong and fixing one
does not fix the other. `examples/gip/queue` learned both the expensive way.

A mean over microsecond samples is decided by the scheduler: one item that loses
its core for a millisecond outweighs the other fifty-nine put together, and
where the pause lands decides the answer. Its build share ranged from 0.16 to
0.45 on a loaded machine where the median of the same samples held between 0.16
and 0.18, and the slowest single sample in a sixty-item queue measured 7x to 24x
the median.

And the median does not save it, because on the Windows runner every per-item
figure was exactly zero - that clock ticks more coarsely than an item takes, so
a hundred microseconds reads as nothing, and the median of sixty zeros is zero.
The mean had been hiding that: a sum of mostly-zero readings with the occasional
tick in it is not zero, so it looked like a measurement.

What works: time a whole run rather than a step, so the interval is milliseconds
and thousands of ticks; take the fastest of several passes, since preemption only
ever adds; measure the clock's tick rather than assuming it, and say so in the
report instead of printing a figure the clock cannot support. Then **put the gate
on a count, not on the interval.** `allocs/op` is the same number on every
platform and at any load - measured, to within one allocation in four hundred -
and where a counted share and a timed share disagree in magnitude they still
rank the same configurations in the same order. Assert the timed figure only
where the clock can resolve it, and log what that skipped.

`main_test.go` must contain at least **three** non-trivial tests: not
`assert(2+2 == 4)`, but the invariants that would actually break if you got it
wrong. At least one of the three must be a property over the whole input rather
than a fixed case. Any of these qualify:

- the output does not depend on how the input was chunked;
- applying the rewrite twice is the same as applying it once;
- a rewrite that should change nothing is byte-identical;
- the text chunks reported reconstruct the document's text;
- text inserted with `lolhtml.Text` can never become markup, whatever it says.

By the end, the app must pass ALL of these:

```
gofmt -l . ; go vet ./...                                  # gofmt prints nothing; vet is silent
go build ./...
go test -count=1 ./examples/gip/<name>                     # its own suite
go test -race -count=1 ./examples/gip/<name>               # the boundary
scripts/check-platforms.sh                                 # all seven rows resolve
go test -asan -count=1 ./examples/gip/<name>               # Linux only, see below
docker run --rm -v "$PWD":/src -w /src golang:1.25-alpine sh -c \
  'apk add --no-cache gcc musl-dev && go test -tags musl -count=1 ./examples/gip/<name>'
```

The last two legs are the point, and neither runs natively on this machine.
`-asan` is unsupported on darwin/arm64, and musl needs the container. ASan is
where a cgo binding's real bugs live: use-after-free across the boundary,
double frees, overruns, including on the Rust heap, because lol-html uses the
system allocator and ASan interposes `malloc` globally. If either leg SKIPs or
refuses to start, that is a missing dependency, not a green light: say so in
the PR and let CI run it.

Note also that `ci.yml`'s test matrix runs `go test -count=1 .`, the root
package only, so tests under `examples/` are compiled by `go build ./...` and
never executed. The first GIP that ships an app should widen that to `./...`
and pay for it: seven platform rows now run your tests, so they must not touch
the network and must not take minutes. `-fuzz` still refuses more than one
package, so the fuzz steps stay on `.`.

If along the way you conclude the app cannot be completed because of a defect
or a gap in golol-html, STOP and go to Phase 3 anyway. That is a better outcome
than a finished app, not a worse one.

#### Traps that have already cost time here

None of these is a typo, and every one of them reported success while doing
nothing:

- **`go test ./...` at the root does not run `differential/` or
  `properties/`.** They are separate modules, deliberately, so their test-only
  dependencies never reach a consumer's module graph. `make test` runs all
  three. `make differential` and `make properties` run them individually.
- **`-fuzz` refuses more than one package.** `go test -fuzz FuzzRewrite ./...`
  fails, because `./...` also matches `examples/`. Use `.`.
- **Never pipe a test run through `tail` or `head`.** The pipeline reports the
  tail's exit status, which is always 0, so a failing suite is announced as a
  success and the detail is discarded. Redirect and grep:
  `go test ... > run.log 2>&1; echo "EXIT=$?"`, then read `--- FAIL` from the
  file.
- **`gofmt -l . | tee /dev/stderr | (! read)` is not portable.** macOS ships
  bash 3.2, where `set -e` aborts on a non-final pipeline element even when the
  pipeline succeeds, so that step failed unconditionally on macOS while
  printing nothing. Use the assignment form the Makefile uses.
- **An unasserted string replacement that matches nothing is
  indistinguishable from success.** That cost time twice here: once widening
  the constraint in `unsupported.go`, once querying `.GoFiles` in
  `check-platforms.sh` when link files, which `import "C"`, appear in
  `.CgoFiles`. Assert the result of every edit you make blind.

Save the app at `examples/gip/<name>/`. It ships with your PR.

### Phase 3 - Pick the problem

While doing Phase 2 you will hit difficulties caused by the library: bad or
missing documentation, an error that does not say what was wrong, a bug, a
platform disagreement, an allocation you cannot avoid, an API that forces an
awkward shape, a missing helper that would have saved an hour. Write them down
as you hit them, *before* you work around them. The workaround erases the
memory of how bad it was.

Now judge which one has the biggest negative impact on users, or the highest
probability of making someone have a bad experience and give up, among those
that are NOT a deliberate limitation.

Deliberate limitations are listed in `docs/gip/wontfix.md`: cgo required and no
`CGO_ENABLED=0`; no pure-Go parsing; a unit is invalid outside its handler; a
failed rewriter is poisoned; character references are not decoded; the
supported platform set and no more; the root module stays dependency-free; the
upstream pin is not moved by a GIP.

A deliberate limitation CAN still be picked IF your proposal does not change
the design. "`ErrDetached` should name which method and which unit type" is
fair game. "Let units outlive their handler" is not.

Three tie-breakers, all specific to this project:

- Prefer a defect **on our side of the boundary** over one in lol-html. The
  upstream version is pinned (W7), so an upstream defect can only become a test
  and a documented row here, while a binding defect is ours to fix today.
- Prefer a defect that is **invisible to every existing gate** over one a suite
  would have caught eventually. You are the only instrument that was pointed at
  it. Section 4 lists what is currently unguarded.
- Prefer a defect in a **shared layer** over the same defect in one unit type.
  A fix in the generic `unit[P]`, in `cfuncs.go` or in the shim serves all
  eight unit types at once and cannot drift back apart; the same fix applied
  eight times is the wrong fix even when it works.

### Phase 4 - Fix it

From this moment, and only from this moment, you may read every file in the
repository, including the implementation, `SPEC.md`, the tests and the
workflows. Read `SPEC.md` first: its "Critical implementation constraints",
"Measured findings" and "CI findings" sections exist so that a fix does not
re-learn something the project already paid for.

Fix it at the deepest layer that owns the problem:

- a lifetime or detachment bug that shows up on one unit type almost always
  lives in the generic `unit[P]` in `unit.go`, where one fix serves all eight;
- an error that arrives empty, or on the wrong goroutine, is a thread-locality
  bug and lives in `shim.c`. lol-html stores the last error in a Rust
  `thread_local!`, and a cgo call is pinned to one OS thread only for its own
  duration, which is why every fallible call and its `take_last_error` happen
  inside a single cgo call. Retrying it from Go is the wrong layer even when it
  appears to work;
- an allocation regression usually lives at the sink borrow or the unit pool,
  not in the method that reveals it;
- a documentation claim that is wrong is a *behaviour* question first. Measure
  what the library does, then fix the doc AND pin the behaviour with a test.
  Three claims were wrong at v0.1.0 and all three are now pinned. A doc fix
  with no test is half a fix;
- a diagnostic that names the wrong thing is a wrapping bug, not a wording bug.

**Deletion is half the job.** If you replace X with Y, X is gone: from the
code, the tests, the README and `SPEC.md`, in the same diff. If your change
makes a comment stale, the comment dies with it. Finding the simplification
that makes your fix small **is 50% of a GIP**, not a bonus on top of it.

What this project rations is not tokens. It is three things:

- **CI time, multiplied by seven.** The test matrix runs on seven platform
  rows, and `sanitize`, `properties`, `differential`, `fuzz`, `minimum-go`,
  `platforms`, `lint` and `consume` run alongside it. A test that takes a
  minute costs seven. Say in the PR which suite your test joins and what it
  costs.
- **The committed binary.** 23.4 MB of archives is vendored across the seven
  rows, and a GIP does not rebuild them (W7).
- **The consumer's module graph.** It has no `require` lines. Keep it that way.

You may fail. Do your best not to. But if you do fail, that is acceptable:
continue to Phase 5 and leave the "solution" part of the issue empty. A
precisely-characterised defect with no fix is worth more than a hack.

### Phase 5 - Open the issue and the PR

Concision is the requirement of greatest importance here. Take it extremely
seriously. The maintainer is a human receiving many of these:

1. his brain is context-switching constantly and does not remember every detail
   of the repo, so CONTEXTUALIZING your problem, and every part of the repo
   needed to understand it, is essential;

2. even with context, there is a limit to how much text he can read. Less text
   per issue is better.

The ideal issue has exactly these components:

- **Context**: everything needed to read the whole issue without looking up a
  single definition, in a dictionary or in the repo.

- **Problem**: explained in the simplest way, easy words, no undefined jargon,
  and preferably with a SHORT VISUAL EXAMPLE that situates the reader
  immediately. Here that is almost always a dozen lines of Go, the input
  document, the output you got and the output you expected, side by side. For a
  documentation defect it is the sentence as published next to what the library
  actually does. For a platform defect it is the two rows that disagree.

- **Solution**: implemented, or merely proposed if you failed.

- **Metrics**: all of them, measured as section 3 requires. For a performance
  or allocation change, a table of every affected benchmark with `allocs/op`
  and `B/op` before and after, naming the Go toolchain. For a leak,
  `LiveHandles()` before and after. For a refactor that should not have changed
  behaviour, the byte-identity result over the differential corpus.

Then ship it. The loop is fixed and you do not ask permission at any step:
**branch, commit, push, PR, watch CI, merge when green, next turn.**

```
git commit -am "<one dense line>"
git push -u origin gip/<slug>
gh issue create --title "..." --body-file /tmp/issue.md
gh pr create --fill --body-file /tmp/pr.md
gh pr checks --watch
gh pr merge --squash --delete-branch
```

A PR that is green but not mergeable is not done: merge `main` in and push.
Do not stop at "pushed to the branch". Note that pull requests are
squash-merged, which is why Phase 0 insists on branching from a fresh
`origin/main`: building on a pre-squash commit gives git two independent
creations of the same content and an add/add conflict on a branch that never
really diverged.

## 3. Rules of this machine

- **This is a laptop, and wall-clock measured here does not travel.** The
  README's benchmark table was measured on this machine and says, in the
  README, to run it on your own hardware before relying on it. Never quote a
  local `ns/op` or `MB/s` as a project metric. Measure with host-independent
  counters instead and let CI produce timings.

- **Cost is `allocs/op` and `B/op`, never `ns/op`.** Allocation counts are
  deterministic for a given input and toolchain, they are what the design
  actually optimises, and they are how the one real performance decision in the
  library was made: borrowing the output buffer instead of copying it took the
  `SetAttribute` benchmark from 2224 allocations per rewrite to 423. Quote the
  Go version alongside the numbers, because that is the axis they move on.

- **A gate is only a gate in the build it measures.** Allocation counts are
  deterministic for a given input and toolchain, and `-asan` is a different
  toolchain: its allocator allocates on its own account, so a path that
  allocates once per match allocates four times per match under the sanitizer.
  A gate on the number must therefore skip the sanitized build - and say so
  where it skips, because a gate that quietly stops measuring is worse than no
  gate. `sanitizer_on_test.go` and `sanitizer_off_test.go` hold the build
  constraint; `requireRealAllocationCounts` is the skip.

- **A test that iterates the implementation's own list can only confirm the
  implementation.** The raw-text guard shipped covering four elements out of
  ten, and the test written to prove it covered "every raw-text element" walked
  the package's own map of raw-text elements - so it passed with four, would
  have passed with one, and could never have named the six that were missing.
  When the correct list belongs to something outside the code - the parser, the
  specification, the platform matrix - derive it from that thing and assert both
  directions: everything it includes is covered, and nothing it excludes is.
  `TestTheGuardCoversEveryRawTextElement` asks the parser about every element
  name in the HTML index; it fails if the map is short and it fails if the map
  is wide.

- **A leak is `LiveHandles()`, never a heap profile.** Every cgo handle create
  and delete is counted, and a rewrite that finishes must leave the count where
  it started. That turns "the handles were probably released" into a checkable
  post-condition, cheap enough to assert on every fuzz iteration. Nothing about
  the rewritten HTML reveals a leak, so if your change touches a lifetime, it
  asserts this counter or it is unverified. `LiveHandles()` lives in
  `export_test.go`, so it is reachable from the root package's tests and
  nowhere else.

- **Rank a cost by weight, not by count.** A rewrite's cost tracks handler
  invocations rather than document size: a 16 KB page with 200 links is
  expensive because of the 200, not the 16 KB. The write path is the same idea
  from the other side - each write costs a crossing whatever its size, so the
  number of writes is what pays. Count the thing that pays.

- **Measure the shape before repeating it.** This document said for several
  releases that byte-at-a-time writes were quadratic while a tag was buffered,
  with figures to match. The figures did not reproduce and the shape was wrong:
  the cost is flat per byte, and the buffered tag is the cheap case. The claim
  had been reasoned from how a parser presumably works, and then cited as a
  reason in five places, including the design of the fuzz harness. A performance
  claim that nothing runs is a claim about the version it was written against, at
  best.

- **Measure the harness before believing the measurement.** The first attempt at
  the above reported one allocation per byte too many, because the probe wrote
  `[]byte(doc[i:j])` from a string and paid for the conversion once per write -
  the same figure it was attributing to the library. An allocation measurement
  has to own nothing: slice a `[]byte`, and check the arithmetic against the
  write count before drawing a conclusion from it.

- **Run the gates that carry signal for what you touched.** Section 4 says
  which those are and what each one is blind to. Two are worth calling out
  because they look more authoritative than they are: the differential suite
  cannot see anything x/net/html also gets wrong, and `FuzzRewrite` cannot see
  anything where the output is correct, which includes every leak, every
  over-copy and every double delete.

- **An ASan report and a bare SIGSEGV are the same finding at different
  resolutions.** `fatal error: unexpected signal during runtime execution` in a
  cgo frame is the low-resolution version of what `-asan` would have told you
  precisely. Reproduce under `-asan` on Linux before reasoning about it. A
  SIGABRT with a Rust backtrace is different again: the c-api crate builds with
  `panic = "abort"`, so a Rust-side panic ends the process and cannot be
  caught. Do not investigate one as the other.

- **A handler panic re-raised on the writing goroutine is by design**, and so
  are `ErrDetached` and `ErrPoisoned`. Confirm against
  `docs/gip/known-behaviours.md` before calling any of them a bug.

- **A hang may be the write size, not a deadlock.** Byte-at-a-time writes cost a
  crossing into C per byte, which on a large document is slow enough to look
  stuck. Check the chunk size before reaching for a stack dump.

- **`go test -race` also enables `checkptr`.** An unsafe pointer round-trip
  through `uintptr` that passes plain `go test` fails there. That is why
  handles cross the boundary as `uintptr_t` and are cast in C.

- **If a test SKIPs, that is a missing dependency, not a green light.** On this
  machine `-asan` is unsupported, the musl legs need Docker running, and
  `make attest-verify` and the `gh` legs need authentication. Four of the seven
  platform rows cannot run here at all. Say what you could not run.

- **Do not hold the PR behind what only CI can answer.** CI runs all seven
  rows, both sanitizer architectures, the properties module at 2000 checks, the
  differential module with and without `-race`, and a 90 second fuzz, and it
  answers sooner than this laptop can approximate any of it. Run the targeted
  legs for what you touched, push, open the PR, and say in the body which
  suites you ran and which are still in flight.

- **Do not rebuild the vendored archives.** `make native` needs a Rust
  toolchain, `make native-all` needs seven cross targets, and the upstream pin
  is not a GIP's to move (W7). If your fix requires an upstream change, file it
  upstream, pin the current behaviour with a test here, add the row to
  `known-behaviours.md`, and say so.

- **Never commit to `main`.** One commit per PR, containing: the minimal
  solution, simplified, which is essential; your `examples/gip/<name>/`; and
  minimal, fast regression tests that fail if the problem comes back, in a
  general way. A regression test that only pins your exact repro is worth much
  less than one that pins the class.

## 4. What each gate sees, and what nothing gates

| Gate | Sees | Blind to |
|---|---|---|
| `go test .` (`rewrite_`, `errors_`, `parity_`) | behaviour at the API surface, ported corners of upstream's own C suite | anything the output does not show: leaks, double deletes, allocations |
| `differential/` | rewrites that change meaning, byte identity of passthrough, text reconstruction | leaks; anything x/net/html gets wrong too |
| `properties/` | claims that must hold for every generated document, shrunk to a readable counter-example | claims nobody wrote down. A property that encodes a false claim about HTML fails for reasons unrelated to the code |
| `FuzzRewrite` | chunk-invariance of the output *and* of what the handlers were told - tag names, source locations, attribute values, doctype parts; crashes on malformed markup | the handler program; anything where both the output and the handler's view are right |
| `FuzzOperations` | lifetimes and marshalling: a unit used after its handler, a handle deleted twice, a string with the wrong length | parser behaviour; cost |
| the handle counter, asserted per fuzz iteration | leaks and double deletes | everything else |
| `faults_test.go` | sink failures, memory limits, handler errors and panics, reproducibly from one seed | happy-path correctness |
| `go test -race` | data races, and `checkptr` violations | single-goroutine memory errors |
| `go test -asan` (Linux only) | use-after-free, double free, overrun, on our heap and Rust's | a leak that is still reachable, which is exactly what a handle leak is; and how much the library allocates, because the sanitizer's allocator is not the one that ships - `alloc_test.go` skips here |
| `scripts/check-platforms.sh` | that all seven rows select their link file and none falls through to a guard | whether the archive then links or runs |
| `scripts/check-workflows.sh` | a workflow file git accepts and GitHub rejects | whether the workflow does the right thing |
| `scripts/check-modules.sh` | a module CI does not vet or test; `go vet ./...` stops at a module boundary | whether the vet and test it finds are the right ones |
| the benchmarks | allocations and bytes per operation, on six shapes | any shape not among the six |
| `make verify` | that the host archive reproduces from the pinned upstream | the other six archives; and it is a diff to read, not an assertion, because Rust builds are not bit-identical |
| `minimum-go` | that the declared Go floor is true | that the floor is the right one |
| `consume` | that a dependent module builds with cargo stripped from `PATH` | anything past `go run` |

Nothing gates the following. This is the shopping list.

- ~~**Allocation complexity class.**~~ Closed: `alloc_test.go` pins the shape,
  asserting that passthrough and non-matching handlers do not allocate per byte
  and that the per-match cost is what it is, while letting the fixed overhead
  drift with the toolchain. Twice repaired since, both times because it asserted
  a number more precisely than the number can be measured: it skips under
  `-asan`, whose allocator is not the one that ships, and it compares the slope
  within 0.05 and the base within 8 rather than exactly. Any gate on a measured
  quantity needs the same question asked of it - what is this number's noise, and
  is the tolerance above it? The handle counter is the third instance and the
  answer there is a *direction* rather than a tolerance: `LiveHandles` counts the
  whole process, a leak can only push it up, and a fall is another test's cleanup
  landing. Draining before the window narrows that and does not close it, because
  an object still reachable when the window opens can become collectable inside
  it. `requireNoHandleLeak` asserts growth only; CI found the equality version
  reporting "1 before, 0 after".
- ~~**Whether every exported name is exercised at all.**~~ Closed:
  `apisurface_test.go` enumerates the exported declarations from the source and
  fails if a test file does not so much as mention one. It is the crudest
  possible check and it immediately found two: `WithGracefulBailOut`, which was
  broken, and `HandlerError.Unwrap`. It also counts the surface, so an added
  export shows up in a diff.
- **The README.** Its prose is still unchecked. Its code is not: `readme_test.go`
  checks that every name it claims exists, that the handful a caller cannot safely
  do without are mentioned, and that the sentence known to have gone stale has not
  come back; `readme_snippets_test.go` holds all eight Go blocks verbatim, so the
  test binary compiles them and four tests check the outputs their comments
  claim. One of the eight did not compile as written.
- **Documentation accuracy.** Partly gated now: `example_test.go` holds sixteen
  runnable transcriptions of the claims the documentation makes in code, so those
  cannot rot without `go test` failing. The prose is still unchecked, and so are
  the code snippets that have not been transcribed - about 140 lines of them, none
  compiled before this. Every claim found wrong so far was found by hand, and the
  count is now five: the three earlier ones, plus SetAttribute claiming its value "is
  escaped as needed, so it is safe to pass untrusted input" when it rewrites
  only the double quote, and the package doc saying it escapes the ampersand
  too. Both were in the same area and disagreed with a third comment on
  Attribute that had it right, which is the shape this failure takes - the same
  fact stated in three places and only two of them maintained.
- ~~**Error message quality.**~~ Closed: `errquality_test.go` collects every
  reachable error and checks that it is attributable to the package, free of
  formatting faults and dangling colons, and - where it concerns a caller's
  input - that it contains that input. A companion test fails if an exported
  error type has no case.
- ~~**Anything under `examples/`.**~~ Closed: the test, Rosetta, musl and
  minimum-go legs run `go test -count=1 ./...`, so every program in `examples/`
  is executed on every platform row rather than merely compiled. The cost of
  that is a constraint on what may go there: fast, and no network.
- **The rest of the raw-text guard.** It covers insertions into content through
  `Element` and `EndTag`. `TextChunk.Before`, `After` and `Replace` are
  unchecked, which is the gap that matters, because editing a script through a
  text handler is the obvious way to do it; so is every streaming insertion. The
  reasons are structural rather than oversight - a text chunk cannot name the
  element it is inside, and a streaming write can split a closing tag across two
  calls - so closing either needs new machinery: a raw-text depth tracked by the
  rewriter, a tag threaded onto the streaming handle plus a rolling tail, or an
  exported check the caller can apply itself, since a text handler does know the
  tag it registered. `TestWhatIsStillNotChecked` pins where it stops.

- **`-race` on darwin/amd64 and on both musl rows.** Rosetta and the container
  skip it, so the race detector runs on four of seven rows.
- **Benchmarks on the musl and Rosetta rows.** They compile nowhere and run
  nowhere.
- **Whether a handler's cost is proportional to what it was asked to do.** The
  library reports nothing about how many times each handler fired or how much
  it copied, so a user cannot see their own cliff. Neither can we.

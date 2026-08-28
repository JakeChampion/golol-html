# Where the vendored archives came from

The seven `internal/lib/*/liblolhtml.a` files are prebuilt binaries. They are
what lets a consumer `go get` this module without a Rust toolchain, and they are
the one part of the repository a reader cannot audit by reading it.

This file records what can actually be established about their origin, the exact
commands that establish it, and what is left over as trust. It deliberately does
not claim more than was measured. Everything under "What was verified" was run
against the committed files on 2026-08-28; everything under "What is not
verifiable from the repository alone" was not, and says why.

## The short version

`internal/lib/SHA256SUMS` proves the archives have not rotted since they were
committed. It proves nothing about where they came from: whoever can push the
archives can push the sums with them.

The origin claim is nevertheless checkable, and stronger than the audit assumed:
**all seven archives rebuild bit-for-bit** from the pinned lol-html revision with
the pinned Rust toolchain. That check is exact, but it is path-sensitive - see
[Reproducing the archives](#reproducing-the-archives), because the naive
`make verify` will report a mismatch on almost every machine for a reason that
has nothing to do with tampering.

## What the pin is

`scripts/build-native.sh` is the single source of truth:

```
$ scripts/build-native.sh --print-pins
lol_html_ref=608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c
rust_toolchain=1.95.0
```

That revision is upstream tag `v3.0.1` exactly - not merely a commit near it:

```
$ git ls-remote --tags https://github.com/cloudflare/lol-html.git | grep v3.0.1
608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c  refs/tags/v3.0.1
```

`scripts/check-pins.sh` keeps `SPEC.md` and the workflows from growing a second,
drifting copy of that string. It checks that the prose agrees with the pin. It
does not, and cannot, check that the committed binaries were built from it.

## What was verified

### 1. The archives have not rotted

Self-consistency only. Run from `internal/lib`, which is what `ci.yml` does:

```
$ cd internal/lib && sha256sum --check SHA256SUMS
```

All seven pass. This is the weakest of the checks here and the only one CI runs
on every pull request.

### 2. The vendored header and licence are byte-identical to upstream

These two files are text, so this is a full-strength check with no caveats:

```
$ git clone --no-checkout https://github.com/cloudflare/lol-html.git /tmp/lol
$ git -C /tmp/lol fetch --depth 1 origin 608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c
$ git -C /tmp/lol checkout --force FETCH_HEAD
$ diff /tmp/lol/c-api/include/lol_html.h internal/include/lol_html.h
$ diff /tmp/lol/LICENSE LICENSE-lol-html
```

Both are identical (`sha256 7fe574dd…` for the header, `e4ddaa9d…` for the
licence). Note the resolution limit: `lol_html.h` is unchanged between upstream
`v3.0.0` and `v3.0.1`, so the header alone places the vendored copy in `3.0.x`,
not at `v3.0.1` specifically.

### 3. The archives identify their own toolchain

Every archive carries the compiler's own version string, and all seven agree
with the pin:

```
$ for t in internal/lib/*/; do
    printf '%-40s ' "$t"
    strings -a "$t/liblolhtml.a" | grep -ao 'rustc version [0-9][^)]*)' | sort -u
  done
```

All seven report `rustc version 1.95.0 (59807616e 2026-04-14)`, which is the
`rustc --version` of the pinned `RUST_TOOLCHAIN=1.95.0`. On the ELF targets the
same fact is in the object metadata rather than inferred from a string:

```
$ mkdir /tmp/m && cd /tmp/m
$ ar x <repo>/internal/lib/linux_amd64/liblolhtml.a \
    "$(ar t <repo>/internal/lib/linux_amd64/liblolhtml.a | head -1)"
$ readelf -p .comment *.o
  [     1]  rustc version 1.95.0 (59807616e 2026-04-14)
```

### 4. The archives identify their own build host and source tree

Rust embeds absolute source paths in panic-location metadata. Those paths
survive `--strip-debug`, and they name the machine the build ran on:

```
$ strings -a internal/lib/linux_amd64/liblolhtml.a | grep -a '\.native-build'
/home/runner/work/golol-html/golol-html/.native-build/lol-html/src/rewriter/mod.rs
/home/runner/work/golol-html/golol-html/.native-build/lol-html/src/selectors_vm/ast.rs
/home/runner/work/golol-html/golol-html/.native-build/lol-html/src/selectors_vm/compiler.rs
/home/runner/work/golol-html/golol-html/.native-build/lol-html/src/selectors_vm/mod.rs
```

`/home/runner/work/<owner>/<repo>` is a GitHub-hosted runner's workspace, and
`.native-build/lol-html` is exactly the working directory `scripts/build-native.sh`
clones into (`work="${repo_root}/.native-build"`). All seven archives carry the
same four paths, so all seven came off the same kind of runner through the same
script - they were not assembled by hand on someone's laptop. (On the COFF
target two of the four appear run together with the string literal in front of
them, which is a layout difference, not a different path; `grep -ao
'/home/runner[^ ]*\.rs'` shows them separately.)

The dependency set is embedded the same way, via `CARGO_HOME`:

```
$ strings -a internal/lib/linux_amd64/liblolhtml.a \
    | grep -ao 'index\.crates\.io-[a-f0-9]*/[A-Za-z0-9_.-]*' | sed 's|.*/||' | sort -u
cssparser-0.36.0  encoding_rs-0.8.35  hashbrown-0.17.0  mime-0.3.17
selectors-0.37.0  servo_arc-0.4.3     smallvec-1.15.1
```

Every one of those versions matches `c-api/Cargo.lock` at the pinned revision.
Resolution limit again: that lockfile is identical between `v3.0.0` and `v3.0.1`
apart from `lol_html`'s own version line, so the dependency fingerprint also
only places the build in `3.0.x`.

### 5. All seven archives reproduce bit-for-bit

This is the check that actually closes the question, and it was run:

| target | committed sha256 | rebuilt |
|---|---|---|
| `darwin_amd64` | `acee2eab…` | identical |
| `darwin_arm64` | `fdf97642…` | identical |
| `linux_amd64` | `6eea33e4…` | identical |
| `linux_amd64_musl` | `37adcd05…` | identical |
| `linux_arm64` | `3ccbbf61…` | identical |
| `linux_arm64_musl` | `b8fc601e…` | identical |
| `windows_amd64` | `426a468b…` | identical |

Restricting the crate type to `staticlib` is what makes this cheap: no linker
for the target is involved, so every one of the seven cross-builds on one
ordinary x86-64 Linux host with nothing but `rustup target add`.

## Reproducing the archives

The build is **not** path-independent. Rust records the absolute path of the
source tree and of `CARGO_HOME` in the binary, so the same source and the same
compiler produce a different archive when built somewhere else. Measured, same
source and same `rustc 1.95.0`, differing only in build location:

```
built at /home/runner/work/golol-html/golol-html/.native-build/lol-html
  with CARGO_HOME=/home/runner/.cargo
  -> 6eea33e4ad3676527135604c474536bf88ea02ae45fc8b96afa5862a5a4cc727  (matches)
built at /tmp/claude-0/othpath/lol-html
  with CARGO_HOME=/root/.cargo
  -> 7f278803fb079e88870be4bf3384a3aa86ee7920f3dd38ddfd85440a6623adee  (differs)
```

So `make verify` in an ordinary checkout will report `DIFFERS` even when nothing
is wrong, and the message it prints blames the toolchain patch version, which is
the wrong lead. To reproduce exactly, recreate the CI runner's two paths:

```
# 1. the pinned toolchain
rustup toolchain install 1.95.0 --profile minimal

# 2. the source, at the path CI used
mkdir -p /home/runner/work/golol-html/golol-html/.native-build
git clone --no-checkout https://github.com/cloudflare/lol-html.git \
    /home/runner/work/golol-html/golol-html/.native-build/lol-html
cd /home/runner/work/golol-html/golol-html/.native-build/lol-html
git fetch --depth 1 origin 608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c
git checkout --force FETCH_HEAD

# 3. the registry, at the path CI used
export CARGO_HOME=/home/runner/.cargo

# 4. build and strip, exactly as scripts/build-native.sh does
cd c-api
rustup target add --toolchain 1.95.0 x86_64-unknown-linux-gnu
cargo +1.95.0 rustc --release --target x86_64-unknown-linux-gnu --crate-type staticlib
cp target/x86_64-unknown-linux-gnu/release/liblolhtml.a /tmp/rebuilt.a
llvm-strip --strip-debug /tmp/rebuilt.a
sha256sum /tmp/rebuilt.a   # compare against internal/lib/SHA256SUMS
```

Substitute the Rust target triple for another platform; `rust_target_for` in
`scripts/build-native.sh` has the mapping. Use `llvm-strip`, not GNU `strip` or
GNU `nm`: LTO leaves bitcode in the archive members, which binutils cannot read
and reports as zero symbols rather than as an error.

A mismatch after all of that is worth investigating. A mismatch from a plain
`make verify` is expected and means nothing on its own.

## What the attestation attests to

`.github/workflows/native.yml` runs `actions/attest-build-provenance` with

```yaml
subject-path: |
  internal/lib/*/liblolhtml.a
  internal/include/lol_html.h
```

so the signed statement binds those file digests to one workflow run of this
repository - which workflow, which golol-html commit, which run. The signature
is issued to the workflow identity rather than to whoever can push, which is why
it is worth more than `SHA256SUMS` for the question "did this file come from a
build, or from a person".

What it does **not** directly assert is the lol-html revision. The workflow
resolves that at run time from `scripts/build-native.sh --print-pins`, and a
`workflow_dispatch` input can override it. The attested golol-html commit tells
you what the script's default was; it does not by itself tell you whether an
input overrode it on that run. Reading the run's recorded parameters, or
reproducing the archive per the section above, is what settles that.

Verify with:

```
make attest-verify           # every archive, plus the header
gh attestation verify internal/lib/<target>/liblolhtml.a --repo JakeChampion/golol-html
```

This needs the `gh` CLI and network access to GitHub; it could not be exercised
while writing this file, so the commands above are from `Makefile` and
`native.yml` rather than from a run.

Two limits worth knowing before relying on it:

- Archives built before the attestation step existed, or in a private fork, have
  nothing to verify. GitHub does not support attestations for user-owned private
  repositories, and `native.yml` skips the step with a notice rather than failing.
- The header only joined the attestation subject and `SHA256SUMS` recently. The
  committed `internal/lib/SHA256SUMS` still has seven lines - the archives only -
  so `internal/include/lol_html.h` is not covered by the checksum file that is in
  the tree today. It will be at the next rebuild; `make attest-verify`'s comment
  says the same. Until then the header's integrity rests on check 2 above, which
  is a direct comparison against upstream and is stronger anyway.

## What is not verifiable from the repository alone

- **Nothing in the repository ties the committed binaries to the pin.**
  `SHA256SUMS` is self-referential, `check-pins.sh` checks prose against prose,
  and CI's checksum step compares the archives to a file committed beside them.
  The reproduction in check 5 is the link, and it is not automated: no job
  rebuilds and compares. A rebuild PR merged while the pin says something else
  would pass every check the repository runs today.
- **Nothing gates a tag.** `ci.yml` triggers on `push: branches: [main]` and
  `pull_request`; a tag ref matches neither, and there is no release workflow.
  Tagging is a local act, so the binaries a tag ships are whatever was in the
  tree at that commit. `v0.1.0` and `v0.1.1` are annotated but unsigned, and
  carry the same seven archive blobs as `main` does today - checked with
  `git ls-tree -r <tag> -- internal/lib`, which is the check to repeat for any
  future tag.
- **The string fingerprints cannot resolve `v3.0.0` from `v3.0.1`.** The
  non-test string literals, the header and the dependency lockfile are all
  identical across those two releases. Only the bit-for-bit reproduction
  discriminates them.
- **The upstream source is trusted as upstream.** Reproducing an archive proves
  it was built from `cloudflare/lol-html` at `608cc4a…`; it says nothing about
  whether that revision is itself sound. `608cc4a…` is upstream's `v3.0.1`, and
  it was also upstream's `main` and `HEAD` when this was written - the newest
  release rather than one that has had time to settle.
- **The toolchain is trusted.** `rustc 1.95.0` is identified by a string the
  compiler wrote about itself. A compromised compiler would write the same
  string, and would also reproduce bit-for-bit for anyone using the same
  compromised build.
- **Reproduction was performed on Linux/x86-64 for all seven targets.** The
  cross-built archives match, which is what `native.yml` also does - one runner
  builds every target. It does not independently confirm the darwin or windows
  archives behave correctly on those hosts; that is what `native.yml`'s smoke
  matrix and `ci.yml`'s platform matrix are for.

## If someone handed you this repository

You could establish, with no trust beyond upstream GitHub serving the right
commit: the header and licence are exactly upstream `v3.0.1`'s; every archive
was built by `rustc 1.95.0` on a GitHub-hosted runner through this repository's
own build script; the dependency set is upstream's lockfile; and every archive
rebuilds byte-identically from that revision. That last one is conclusive - it
leaves nowhere for an extra object file or a patched function to hide.

You would still be taking on trust: that `cloudflare/lol-html` at that revision
is what it appears to be, that `rustc 1.95.0` is honest, and - if you skip the
rebuild and lean on the attestation instead - that the dispatch inputs on that
workflow run did not override the pin.

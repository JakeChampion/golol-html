# Cutting a release

`release.yml` does this. A push to `main` with fragments pending opens a
**Release vX.Y.Z** pull request holding the changelog fold; merging that pull
request tags what it produced. Nobody chooses the number: it is the highest bump
the fragments themselves declared, applied to the newest `v*` tag.

It is the shape `changesets` uses, without the npm toolchain - see
`changelog.d/README.md` for why a Go module gets the discipline and not the
dependency. Until `v0.2.0` none of this existed and tagging was a local act
nobody had written down, which is how `v0.1.1` came to be the version `go get`
served for a week after `main` had been replaced by a history it shares no
ancestor with.

## Why a tag is not a formality here

`go get github.com/JakeChampion/golol-html` resolves to the **highest semver
tag**, not to `main`. Nothing on `main` reaches a consumer until a tag points at
it. Check what the proxy is actually serving rather than assuming:

```
$ curl -s https://proxy.golang.org/github.com/!jake!champion/golol-html/@latest
{"Version":"v0.2.0", ...,"Ref":"refs/tags/v0.2.0"}
```

If that version is behind `main`, everyone installing the documented way is on
old code, and no amount of green CI on `main` says otherwise.

## The steps

1. **Write a fragment with every change.** That is the whole of a contributor's
   part in this. `changelog.d/README.md` has the format; the first line declares
   `major`, `minor` or `patch`.

2. **Merge to `main`.** `release.yml` opens a **Release vX.Y.Z** pull request
   with the fold. It reopens on every later push, so a change that lands after
   it was opened is picked up rather than missed.

3. **Read the `CHANGELOG.md` diff.** That is the release notes, and the only
   place they exist. Push a correction onto the release branch if it reads
   badly.

4. **Merge the release pull request.** The next run tags it, with the section
   body as the tag message, and CI and `verify-native` run on the tag.

5. **Confirm the proxy caught up** with the `@latest` query above. It takes a
   few minutes, and it is the only check that speaks for the thing users get.

To cut one by hand - if the workflow is broken, or a release has to happen from
somewhere else - `scripts/changelog.sh --release` does steps 2 and 3, and
`git tag -a <version>` and a push do step 4. The workflow is only calling those.

## Choosing the number

Nobody does. It is the highest bump the fragments declared, applied to the
newest `v*` tag by version order, and the two commands that compute it -
`scripts/changelog.sh --bump` and `--next-version` - are the same ones the
workflow runs.

What a bump *means* here is a convention, and `changelog.d/README.md` states it:
the module is `v0` and the API is not frozen, so a **minor** is the signal for a
breaking change and a **patch** is everything else. `v0.2.0` was a minor because,
against what `v0.1.1` actually published, it is a different API.

## What is still by hand

- **Tags are unsigned.** `v0.1.0`, `v0.1.1` and `v0.2.0` are annotated but not
  signed, and a tag `release.yml` creates will be too - a GitHub Actions token
  cannot sign.
- **`RELEASE_PR_TOKEN` decides whether the release is checked.** A pull request
  opened, or a tag pushed, with the default `GITHUB_TOKEN` starts no workflows,
  so without that secret neither the release pull request nor the tag gets CI.
  `release.yml` still does the work and warns in the run summary saying exactly
  what did not happen, but a release nothing ran on is the one worth noticing.
- **The archives are tied to the pin by one job on one platform.**
  `verify-native.yml` rebuilds `linux_amd64` from the pinned revision and diffs
  it against `SHA256SUMS`, on every `v*` tag, on a pull request touching the pin
  or `internal/`, and weekly. That closes the hole where a pin bumped and tagged
  before the `native` rebuild landed would ship old binaries under a new claimed
  revision with every check green. The other six are cross-built from the same
  source in the same run of `native.yml`, so one is strong evidence for all
  seven - but it is evidence, not proof, and a tag run that is red means do not
  release whatever else is green.
- **`git describe` does not work on `main`** and will not until the pre-`v0.2.0`
  tags are irrelevant, because they sit on the abandoned history. Nothing
  depends on it; it is worth knowing before reaching for it in a script.

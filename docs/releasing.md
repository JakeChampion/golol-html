# Cutting a release

There is no release workflow. Tagging is a local act, and until `v0.2.0` there
was nothing written down about it - which is how `v0.1.1` came to be the version
`go get` served for a week after `main` had been replaced by a history it shares
no ancestor with. What follows is the procedure that release established.

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

1. **Be on a clean `main`** that CI has passed. `git fetch origin main && git
   checkout main && git status`.

2. **Fold the changelog** and open it as a pull request - `scripts/changelog.sh
   --apply` splices `changelog.d/*.md` into `## Unreleased` and deletes the
   fragments it consumed. `scripts/check-changelog.sh` recognises that shape, so
   the fold is the one commit allowed to add entries to `CHANGELOG.md`.

3. **Rename the section.** Change `## Unreleased` to `## vX.Y.Z` and put a fresh
   `## Unreleased` above it, keeping the HTML comment that points at
   `changelog.d/`. Say in the section what the version means for a caller
   upgrading, not just what changed.

4. **Merge, and let CI run on `main`.**

5. **Tag the merge commit**, annotated:

   ```
   git tag -a v0.2.0 -m "..." <sha>
   git push origin v0.2.0
   ```

   CI runs on `v*` tags, so watch that run: it is the only thing that gates what
   a consumer installs.

6. **Confirm the proxy caught up** with the `@latest` query above. It can take a
   few minutes, and it is the only check that speaks for the thing users get.

## Choosing the number

The module is `v0` and the API is not frozen, so a minor bump is the signal for
a breaking change and a patch for everything else. `v0.2.0` was a minor bump
because, against what `v0.1.1` actually published, it is a different API.

## What is still by hand

- **Tags are unsigned.** `v0.1.0`, `v0.1.1` and `v0.2.0` are annotated but not
  signed.
- **Nothing ties the committed archives to the pinned lol-html revision at
  release time.** `SHA256SUMS` is self-referential and `check-pins.sh` compares
  prose against prose; the link is the bit-for-bit rebuild in
  `docs/provenance.md`, which no job runs. A pin bumped and tagged before the
  `native` rebuild landed would ship old binaries under a new claimed revision
  with every check green.
- **`git describe` does not work on `main`** and will not until the pre-`v0.2.0`
  tags are irrelevant, because they sit on the abandoned history. Nothing
  depends on it; it is worth knowing before reaching for it in a script.

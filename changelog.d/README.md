# changelog.d

One file per change, folded into `CHANGELOG.md` at release time.

`CHANGELOG.md`'s `## Unreleased` section is one list that every branch appends
to at the same point, so two open pull requests conflict on it by construction -
and every merge to `main` re-conflicts every other open branch, whatever else
they touch. Four open pull requests, none of them near each other in the code,
all conflicted in exactly this one file; that is what this directory is for.

## Writing an entry

Add a file named `<pr-number>-<slug>.md`, e.g. `331-ci-concurrency.md`. The
number is what orders the entries when they are folded in, so use the pull
request's own; a branch without one yet can use the next number that is free and
rename later, or leave the number off entirely and sort last.

The first line declares how far the change moves the version. Then a blank
line, then the entry exactly as it should read in the changelog: a top-level
`-` bullet, continuation lines indented two spaces, wrapped at 80 columns like
the entries already there.

```markdown
<!-- bump: minor -->

- `Element.Something`: what changed, and what it means for a caller who was
  relying on the old behaviour.

  A second paragraph if the first cannot carry it. Measurements belong here
  rather than in a commit message, because this is what gets read later.
```

`major`, `minor` or `patch`. The module is `v0` and the API is not frozen, so
here a **minor** is the signal for a breaking change and a **patch** is
everything else; `major` is reserved for `v1`. The bump line is stripped when
the fragment is folded in.

It is required rather than defaulted, and that is the whole point of it. A
default is a guess made by whoever is least able to make it - the release, weeks
later, from a diff someone has to reconstruct - instead of the author with the
change in front of them. `scripts/changelog.sh --bump` prints the highest of
them and `--next-version` applies it to the newest `v*` tag, so the release
number is derived rather than decided.

This is the discipline `changesets` is built around, without the npm toolchain
it comes in: a Go module has nothing to publish to a registry, so `changeset
publish` would have been `git push origin <tag>` wearing a `package.json`.

Nothing parses these beyond `scripts/check-changelog.sh`, which checks that a
fragment is a bullet and is free of conflict markers and tabs. The text is
otherwise passed through verbatim.

## Releasing

`release.yml` does this, and `docs/releasing.md` describes the whole cycle. A
push to `main` with fragments pending opens a **Release vX.Y.Z** pull request
holding the fold; merging that pull request tags it. Nobody chooses the number.

The commands underneath, which are worth knowing because the workflow is only
calling them:

```
scripts/changelog.sh                  # print the assembled section, change nothing
scripts/changelog.sh --bump           # the highest bump pending, or "none"
scripts/changelog.sh --next-version   # that bump applied to the newest v* tag
scripts/changelog.sh --apply          # fold into ## Unreleased
scripts/changelog.sh --release        # fold, and name the section with the version
```

The fold is the only commit that edits `CHANGELOG.md`, so it is the only one
that can conflict there, and it conflicts with nothing because the fragments it
consumes are gone in the same commit. `scripts/check-changelog.sh` knows that
shape and lets it through.

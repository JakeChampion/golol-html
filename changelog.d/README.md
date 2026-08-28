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

The content is the entry exactly as it should read in the changelog: a top-level
`-` bullet, continuation lines indented two spaces, wrapped at 80 columns like
the entries already there.

```markdown
- `Element.Something`: what changed, and what it means for a caller who was
  relying on the old behaviour.

  A second paragraph if the first cannot carry it. Measurements belong here
  rather than in a commit message, because this is what gets read later.
```

Nothing parses these beyond `scripts/check-changelog.sh`, which checks that a
fragment is a bullet and is free of conflict markers and tabs. The text is
otherwise passed through verbatim.

## Releasing

`scripts/changelog.sh` prints the assembled section; `scripts/changelog.sh
--apply` splices it into the top of `## Unreleased` in `CHANGELOG.md` and
deletes the fragments it consumed. Run it when cutting a release - or whenever
the directory has grown enough to be worth folding in - and commit the result.
The fold is the only commit that edits `CHANGELOG.md`, so it is the only one
that can conflict there, and it conflicts with nothing because the fragments it
consumes are gone in the same commit.

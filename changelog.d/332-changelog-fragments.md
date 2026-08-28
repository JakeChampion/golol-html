- Unreleased changelog entries move to `changelog.d/`, one file per change,
  folded into `CHANGELOG.md` at release time by `scripts/changelog.sh`. The
  Unreleased list was a single list that every branch appended to at the same
  point, so two open pull requests conflicted there by construction - and every
  merge to `main` re-conflicted every other open branch, whatever else they
  touched. Four open pull requests, none of them near each other in the code,
  were all conflicted in exactly this one file.

  `scripts/check-changelog.sh` runs in the lint job and fails a branch that adds
  an entry to `CHANGELOG.md` instead of a fragment. It looks for an added `- `
  bullet rather than any edit, so fixing a typo in a released section is still
  free, and a release fold - the one commit that is supposed to add entries - is
  recognised by the fragments it deletes in the same diff.

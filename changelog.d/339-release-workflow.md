<!-- bump: patch -->

- `release.yml` keeps one release pull request, `release/next`, whatever the
  version turns out to be, tags an untagged version heading before it
  proposes the next one, and reproduces the `linux_amd64` archive at the fold
  commit before it pushes the tag - in the same run, with no token to
  configure.

  The branch used to carry the version in its name, so a `minor` fragment
  landing while a `patch` release PR was open forked a second PR and left the
  first mergeable; merging the first put a version heading on `main` that the
  plan step, checking "fragments pending" before "heading untagged", never
  tagged. A folded-but-untagged heading with any fragment pending made every
  later run fail on "already has a section", because the next version was
  derived from tags alone; it is now the higher of the newest tag and the
  newest heading. And the gate had not gated: v0.2.1 was tagged with the
  default token, so neither ci nor verify-native ran on the tag, and the
  release PR - which touches only the changelog - could not match
  verify-native's paths filter either. The tag now needs the reproduce job,
  lands on the fold commit rather than on whatever `main` had moved to (a
  queued run for the fold commit is no longer cancelled by a newer push:
  `queue: max`), refuses a heading whose commit deleted no fragments, and
  keeps every `#`-prefixed line of the notes (`--cleanup=verbatim`).
  `scripts/check-changelog.sh` refuses a version heading added by hand, and
  `scripts/changelog.sh` refuses a `major` bump past v1 unless the module
  path carries the matching `/vN`, which is the only way Go can resolve it.

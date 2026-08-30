<!-- bump: patch -->

- Versioning and releasing now work the way `changesets` does, without the npm
  toolchain. Each fragment in `changelog.d/` declares how far it moves the
  version on a first line reading `<!-- bump: patch -->`; `release.yml` opens a
  **Release vX.Y.Z** pull request when any are pending, and tags what that pull
  request produced once it merges.

  The number stops being a judgement someone makes at the end, from a diff they
  have to reconstruct, and becomes the sum of what each change said about itself
  while its author still had it in front of them. `scripts/changelog.sh` grew
  `--bump`, `--next-version` and `--release` to do the arithmetic, and
  `check-changelog.sh` now requires the bump line rather than defaulting it - a
  default is a guess made by whoever is least placed to make it.

  Not `@changesets/cli` itself: this repository has no JavaScript, a root
  `go.mod` with no requires, and a `scripts/` directory that is deliberately
  dependency-free. What changesets offers a Go module is the discipline and the
  two-phase release, and both are a few lines of shell - publishing, the part
  that would have justified the dependency, is `git push origin <tag>` here.

- `verify-native.yml` rebuilds the `linux_amd64` archive from the pinned
  lol-html revision and diffs it against `SHA256SUMS` - on every `v*` tag, on a
  pull request touching the pin or `internal/`, and weekly.

  It is the only thing that ties the binaries under `internal/lib` to the
  revision they claim to come from. `SHA256SUMS` proves the files have not
  rotted rather than where they came from, since whoever can push the archives
  can push the sums with them, and `check-pins.sh` compares prose against prose.
  A pin bumped and tagged before the `native` rebuild landed would have shipped
  the old binaries under a new claimed revision with every other check green.

  It can only work on a runner. rustc records the absolute path of the source
  tree and of `CARGO_HOME` in metadata that survives stripping, so the same
  revision built with the same compiler somewhere else hashes differently -
  which is why `make verify` fails on a laptop and why a job whose workspace is
  exactly the path the archives were built at can succeed. The first step
  asserts that path rather than trusting it, so an image change reports itself
  instead of looking like tampering.

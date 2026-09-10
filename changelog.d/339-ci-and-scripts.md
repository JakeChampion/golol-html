<!-- bump: patch -->

- `internal/lib/SHA256SUMS` carries the vendored header, and
  `build-native.sh --verify` compares the header and licence it syncs from
  the pin against the committed copies instead of silently restoring them.
  The header joined the manifest's writer in v0.2.0, "at the next rebuild";
  a text file's sum needs no rebuild, and the tagged release still shipped a
  seven-line manifest the ci checksum step could not see the header through.

- The `platforms` job installs `llvm-18` so `check-abi.sh` reads the two
  darwin archives it was silently skipping - the ubuntu runner has no
  `llvm-nm` on its PATH and GNU nm cannot read Mach-O, so the job was green
  having checked five of the seven archives it said it checked. In CI the
  script runs with `--require-all`, which makes a skip a failure.

- The two `golang:1.25-alpine` images are pinned by digest, the one build
  input that still floated after every action was pinned by SHA; `apt`
  installs `llvm-18` rather than whatever `llvm` points at, since
  `llvm-strip` decides the bytes of every archive; `cargo rustc` runs
  `--locked`; a Dependabot config moves the action pins. `check-pins.sh`
  also checks the copies of the pin in `docs/gip/wontfix.md` and
  `docs/provenance.md`, and the two scripts that grepped for a tab with
  `grep -P` - which BSD grep does not have, so `make lint` on a Mac never
  failed a tab - grep for a literal one.

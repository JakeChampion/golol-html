- The vendored archives are now shown to come from the pinned lol-html revision
  rather than assumed to: all seven rebuild bit-for-bit from `608cc4a`, which is
  upstream's `v3.0.1`, with the pinned `rustc 1.95.0`. `docs/provenance.md`
  records what that establishes, the exact commands, and what is left over as
  trust.

  It also records why `make verify` reports `DIFFERS` on almost every machine
  while nothing is wrong: rustc embeds the absolute path of the source tree and
  of `CARGO_HOME` in metadata that survives stripping, so the same revision
  built with the same compiler somewhere else hashes differently. The script
  used to blame the toolchain patch version for this, which sent a reader after
  the wrong thing; it now names the paths to recreate, and the lasting fix
  (`--remap-path-prefix`) for whoever does the next rebuild.

- `scripts/check-abi.sh` pins the vendored header against the archives beside
  it, run from `make lint` and CI. C linkage carries no type information, so a
  header that has drifted from its binary is silent corruption rather than a
  compile error, and the header is the only description of those archives that
  anything reads. It checks that every symbol the binding calls is declared and
  defined in all seven archives, that the seven export an identical set, and -
  on the host archive, because this part has to run the code - that the structs
  and callback signatures behave as the header describes.

  Nothing was wrong. The one thing it found is that the archives export four
  functions the header does not declare - `lol_html_comment_streaming_before`,
  `_after`, `_replace` and `lol_html_end_tag_replace` - which is why there is no
  `Comment.StreamBefore` or non-streaming `EndTag.Replace` in the Go API. They
  work when declared by hand, so this is an upstream cbindgen gap rather than a
  bad header sync, and the check pins the set of four so a change to it is
  noticed.

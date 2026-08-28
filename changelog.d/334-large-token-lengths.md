- Token lengths no longer narrow to a 32-bit `int` on the way out of C, so a
  token larger than 2 GiB is copied whole rather than truncated.

  `C.GoStringN` and `C.GoBytes` take a `C.int` length, and lol-html reports a
  `size_t`. Nothing bounds the size of a comment or a text node, so a token past
  2 GiB kept only the low 32 bits - silently halving a 4 GiB one, and panicking
  outright whenever bit 31 landed set. The conversion now carries the length the
  library actually reported. It still costs exactly one allocation per string,
  which is what `alloc_test.go` pins: the shorter `string(unsafe.Slice(...))`
  would sometimes cost none, but only for a short result the caller discards, and
  a per-call cost that depends on the length of a document's identifiers is not
  one worth documenting.

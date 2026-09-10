<!-- bump: patch -->

- Streaming content through `Sink.AsWriter` or `Sink.WriteChunk` no longer
  allocates the size of the stream: `WriteChunk` converted every chunk to a
  string to look at its last four bytes, so an `io.Copy` of a 12.6 MB report
  through the API whose purpose is to avoid assembling content in memory
  allocated 12.6 MB per rewrite, in pieces. It looks at the four bytes.
  Pinned in alloc_test.go: 64 KB and 1 MB through `AsWriter` cost the same.

- Every mutation, insertion, end-tag registration and sink write reports
  failure through the Writer's own error slot rather than through a local
  whose address escaped to the heap - the allocation v0.2.0 removed from
  `Write`, still paid once per call everywhere else. Setting an attribute is
  one allocation per match rather than two, and so is every other write; a
  read is still two, one of them the string. `README.md`'s Cost section and
  `alloc_test.go` now say and pin that: one per wrapper, one per string read,
  none per write.

- `Element.Attributes` no longer fetches the source spelling of each name it
  does not yield: three lol-html calls per attribute rather than four.
  `AttributeList`, which does yield it, is unchanged.

- `RewriteString` writes into a `strings.Builder` rather than converting
  `Rewrite`'s bytes, which copied the whole output a second time: half the
  bytes per rewrite on a 220 KB document.

<!-- bump: patch -->

- `Sink.WriteString` and `Sink.WriteChunk` refuse with `ErrReentrant` when
  called from inside the destination writer that a sink write is running. The
  destination runs synchronously inside a sink write - not, as two sentences
  in the streaming documentation said, after it - so a destination that could
  reach the live `Sink` wrote into it underneath the write already using it,
  and the inner bytes landed first with no error. The guard is the one
  `Writer.Write` and `Close` already have. Measured in reentrancy_test.go.

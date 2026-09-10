<!-- bump: patch -->

- Five small corrections to which error a failure reports, each measured:

  `Writer.Write` on a Writer that failed and was then closed reports
  `ErrPoisoned` wrapping the cause, as its documentation promised "however
  late it is asked for", rather than bare `ErrClosed`. A panic inside `Close`
  still leaves the Writer closed rather than poisoned, as `Close` documents.

  `SetAttribute` classifies invalid UTF-8 per argument, so a name ending in a
  lead byte and a value starting with its continuation - two invalid strings
  that concatenate into one valid one - now match `ErrInvalidUTF8`.
  `HasAttribute` classifies too, as `RemoveAttribute` already did, and
  `Attribute` answers a name that is not valid UTF-8 with absent without
  calling lol-html, which used to leave the reason in lol-html's thread-local
  error slot for the next failure to misattribute.

  `Sink.WriteChunk` ending in a byte no UTF-8 sequence starts with (0xC0, 0xF5
  and up) or in a surrogate prefix (0xED 0xA0) matches `ErrInvalidUTF8`; those
  were counted as a trailing partial that a later chunk might complete, which
  nothing can, so lol-html's refusal matched neither sentinel.

  `WithEncoding("")` fails from `NewWriter` with an `EncodingError`, like every
  other unusable label, so a caller that branches on it to fall back to a raw
  copy handles the empty charset too.

  `Element.OnEndTag` on an element that has no end tag no longer keeps the
  refused registration's handle until `Close`.

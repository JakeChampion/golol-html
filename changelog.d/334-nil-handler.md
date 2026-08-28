- An option carrying a nil handler is refused by `NewWriter`, and
  `Element.OnEndTag(nil)` is refused where it is called.

  `ErrNilOption` refuses a nil `Option` on the grounds that "a rewrite that
  quietly did less than it was told to is worse than one that did not start", but
  `OnElement("p", nil)` is a non-nil option carrying nothing, and it was exactly
  that quiet skip: the rewrite built, ran, matched, did nothing and reported
  success. `OnComment`, `OnText`, `OnDoctype`, `OnDocumentComment`,
  `OnDocumentText` and `OnDocumentEnd` all behaved the same way. The error names
  the call the caller wrote, since that is what they have to go and find.

  `Element.OnEndTag(nil)` was the sharper edge: it registered the nil function,
  cost a handle for the rest of the rewrite, and dereferenced it when the end tag
  arrived - reaching the caller as a nil-pointer panic out of `Write`, with the
  Writer poisoned and the stack pointing at the library.

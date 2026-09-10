<!-- bump: patch -->

- The raw-text breakout guard folds case the way the tokenizer does - ASCII
  only - and never slices the caller's string with an offset taken from a
  copy. `CheckRawText`, and `Element.Append`, `Prepend`, `SetInnerContent` and
  `EndTag.Before` with `HTML`, panicked on content holding a letter whose
  lower-case form is a different number of bytes (U+023A "Ⱥ" grows from two to
  three), because the search ran over a `strings.ToLower` copy and the error
  message then indexed the original with the copy's offset. The same fold
  mapped U+0130 "İ" to "i", so `</scrİpt>` was refused although lol-html does
  not end a script there, and `IsRawText("scrİpt")` was true for an element
  that holds markup. The documented rule was already "the tokenizer's"; the
  code now is. Measured in rawtext_test.go, including the offset the error
  reports for content that is not ASCII.

  The check inside `Element.Append` and its siblings borrows the tag name from
  lol-html rather than copying it out, so an `HTML` insertion into an element's
  content costs one allocation rather than three.

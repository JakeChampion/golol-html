<!-- bump: patch -->

- `EndTag.SetName` refuses the names `Element.SetTagName` refuses - empty, not
  starting with an ASCII letter, or containing whitespace, "/" or ">" - with
  lol-html's own messages. lol-html validates a start tag's new name and not an
  end tag's, so a name of `a>b` produced `</a>b>`, which a parser reads as an
  end tag followed by text, the empty name produced `</>`, which it drops, and
  a name carrying `<img src=x onerror=…>` produced a live element. No error in
  any case. A rename computed from the document inherited an injection its
  sibling was protected against. Invalid UTF-8 is still lol-html's to report,
  so it still matches `ErrInvalidUTF8`. Measured in endtagname_test.go against
  what `SetTagName` says for the same names.

  `EndTag.Before`'s raw-text check keys on the name the tag was parsed with,
  not on what it has been renamed to: the content in front of the tag was
  tokenised as raw text under the original name and stays raw text whatever
  the tag is called now, so `SetName("div")` no longer switches the check off.

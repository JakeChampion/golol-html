<!-- bump: patch -->

- A selector nested more than 32 levels of parentheses deep, or carrying more
  than 128 combinators, is refused from `NewWriter` with a `SelectorError`
  naming the limit. lol-html's selector parser and matcher builder recurse on
  both, and the vendored archives abort rather than unwind, so a selector deep
  enough - measured, about 3,100 nested `:not(` or 14,000 ` > ` on an 8 MB
  stack, sixty times fewer on a musl thread stack - overflowed the native
  stack and killed the process with a SIGSEGV no recover could see. The limits
  sit a hundredfold below that and far above any selector anyone writes;
  brackets and quoted strings are not counted. Measured in selector_test.go.

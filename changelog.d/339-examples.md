<!-- bump: patch -->

- `examples/gip/csrf` judged a form's `action` on the raw attribute source, so
  an entity-encoded or backslash-spelled cross-origin URL -
  `action="&#104;ttps://evil.example/…"`, `action="\\evil.example/…"` - was
  taken for a same-origin relative one and given the token, the one thing the
  program exists to refuse. It now decodes, normalises backslashes to slashes
  as a browser does, and fails closed; each spelling is a test row. The same
  backslash gap in `examples/gip/sandbox` left an iframe a browser loads
  cross-origin without a sandbox; fixed the same way.

- `examples/gip/sri` matched `rel` by exact value, so `rel="stylesheet
  preload"` got no integrity and was not reported as uncovered. It now uses
  `~=` with the case-insensitive flag, which lol-html supports, and its scan
  of commented-out markup is case-insensitive. `examples/rewrite-url` honours
  the response charset before registering a text handler, which otherwise
  turns every non-UTF-8 title into U+FFFD. `examples/gip/consentgate`'s usage
  names the flag the program defines.

# Example applications: audit of all 171 apps under `examples/gip`

`docs/audit-2026-08-28.md` recorded this as a gap - "~150 example applications
under `examples/gip/` are effectively unaudited". This closes it. All 171 were
read in 22 batches, and every finding was then handed to a separate reader told
to refute it against the code. 129 survived: 32 high, 64 medium, 32 low, 1
informational.

**No finding here is against the library.** It behaved correctly in all 129. CI
already runs every one of these programs on every platform, so they compile and
they run; what nobody had checked is whether they are *right*, and whether the
shape a reader would copy is safe.

## 129 findings, about eight mistakes

They collapse into a handful of root causes, each repeated across many apps -
which is what you would expect of files written to a common pattern. Fixing a
pattern once fixes it everywhere it appears, so the table is the plan.

| Root cause | Apps | Findings | High |
|---|---|---|---|
| Depth counters that never come back down | 17 | 17 | 9 |
| OnEndTag on an element that cannot have content | 7 | 7 | 3 |
| Insertion at an implied `</head>` | 9 | 10 | 5 |
| Matching raw source instead of decoded values | 12 | 12 | 5 |
| Writer abandoned without Close | 10 | 10 | 0 |
| Untrusted text assembled into a comment | 2 | 2 | 1 |
| Go quoting used for HTML attributes | 2 | 2 | 0 |
| Documentation contradicting the code | 24 | 24 | 3 |
| Other, one-off | 39 | 45 | 6 |

The worst of them is the comment breakout: `absolutise` turns a payload that was
inert inside a quoted attribute in the *input* into a live `<script>` element in
the *output*. Reproduced:

```
$ printf '<base href="/x?q=--><script>alert(1)</script>"><p>hi</p>' \
    | go run ./examples/gip/absolutise -base https://example.com/
<base href="https://example.com/x?q=--><script>alert(1)</script>"><p>hi</p>
<!-- absolutise: base=https://example.com/x?q=--><script>alert(1)</script> rewritten=1 ... -->
```

The file already knows the rule - one screen earlier it uses `Text` for an
untrusted URL, with the comment "so it is untrusted and must not be able to
become markup" - and then hand-assembles a comment without it.

## Root causes

### Depth counters that never come back down

A handler tracks "am I inside a region to skip" with a counter raised on the start tag and lowered in OnEndTag. HTML lets end tags be omitted - `</option>`, `</p>`, `</li>`, `</head>` - so the decrement never runs, the counter stays raised, and the feature silently switches off for the rest of the document. It is silent because the output is still well-formed; only the work is missing.

Affects **17 apps**, 17 findings.

- **[high] emoji** — The skipped-region depth counter is never decremented for an implicitly closed element, silently disabling expansion for the rest of the document  
  `examples/gip/emoji/main.go:117`
- **[high] firstlink** — Same stuck depth counter, and it makes the program report mentioned terms as "not mentioned"  
  `examples/gip/firstlink/main.go:261`
- **[high] glossary** — The exclusion depth counter never comes back down after an implied or misnested end tag, so the rest of the page is silently left unlinked  
  `examples/gip/glossary/main.go:252`
- **[high] headings** — hidden-region counter never decrements for an implicitly closed element, silently disabling every check for the rest of the document  
  `examples/gip/headings/main.go:173`
- **[high] highlight** — skip-depth counter is stuck open after an implicitly closed `<option>`, so nothing in the rest of the document is highlighted  
  `examples/gip/highlight/main.go:104`
- **[high] keywords** — Depth counters leak on omitted end tags, silently zeroing the whole report for ordinary HTML  
  `examples/gip/keywords/main.go:163`
- **[high] linkify** — The `t.Name() != tag` guard makes linkify's depth counter stick, silently disabling linking for the rest of the page  
  `examples/gip/linkify/main.go:84`
- **[high] plaintext** — Skip-depth guard never unwinds for `<head>` and `<option>`, whose end tags are omissible — the whole document's text is silently dropped  
  `examples/gip/plaintext/main.go:152`
- **[high] readingtime** — skipDepth is never decremented when a skipped element's end tag is implied, so the rest of the document counts as zero words  
  `examples/gip/readingtime/main.go:123`
- **[medium] autoplay** — inScript goes negative after the first reported script, so later scripts are never scanned and ordinary prose is  
  `examples/gip/autoplay/main.go:228`
- **[medium] collapse** — depth is incremented before the CanHaveContent bail-out, so a self-closing foreign raw-text element disables collapsing for the rest of the document  
  `examples/gip/collapse/main.go:136`
- **[medium] mentions** — Name-guarded end tag on a depth counter leaves it permanently raised, silently disabling linking for the rest of the document  
  `examples/gip/mentions/main.go:78`
- **[medium] microdata** — No implied-end-tag handling: implicitly closed itemprops merge their sibling's text and nest scopes that are not nested  
  `examples/gip/microdata/main.go:161`
- **[medium] summary** — skipDepth is never decremented when a skipped element's end tag is omitted, so extraction dies at the first `<option>`  
  `examples/gip/summary/main.go:165`
- **[medium] tablelayout** — The open-paragraph counter ignores implied `</p>`, so an unclosed `<p>` refuses every later conversion  
  `examples/gip/tablelayout/main.go:142`
- **[medium] units** — The skip-region depth counter is incremented before the CanHaveContent early return, so it never comes back down  
  `examples/gip/units/main.go:184`
- **[low] markdown** — `<pre>``<code>` emits stray backticks inside the fenced block  
  `examples/gip/markdown/main.go:239`

### OnEndTag on an element that cannot have content

`Element.OnEndTag` fails for an element that has no end tag - a self-closing `<script/>` or `<a/>` in foreign content. Registered without checking `CanHaveContent` first, that error aborts the rewrite *after* a prefix has already reached the destination, so the output is truncated mid-document.

Affects **7 apps**, 7 findings.

- **[high] descript** — OnEndTag registered on `<script>` without a CanHaveContent guard aborts the rewrite and truncates stdout on a self-closing SVG/MathML script  
  `examples/gip/descript/main.go:122`
- **[high] upgrade** — OnEndTag registered on `<style>` without the CanHaveContent guard aborts the whole rewrite  
  `examples/gip/upgrade/main.go:140`
- **[high] widows** — IsSelfClosing() used as "this element is empty", so a heading containing `<span/>` loses its `</h1>`  
  `examples/gip/widows/main.go:225`
- **[medium] controls** — OnEndTag called on "template" without the CanHaveContent guard: a self-closing foreign `<template/>` aborts the rewrite and truncates the page  
  `examples/gip/controls/main.go:87`
- **[medium] cspnonce** — OnEndTag called on "script, style" without the CanHaveContent guard: a self-closing foreign `<script/>` aborts the rewrite and truncates the page  
  `examples/gip/cspnonce/main.go:158`
- **[medium] linkreport** — OnEndTag is registered without checking CanHaveContent, so a self-closing SVG anchor aborts the whole report  
  `examples/gip/linkreport/main.go:111`
- **[medium] linktext** — OnEndTag is registered without checking CanHaveContent, so one self-closing SVG anchor fails the whole rewrite after a truncated page has been written  
  `examples/gip/linktext/main.go:166`

### Insertion at an implied `</head>`

Inserting at the head's end tag assumes the document spells `</head>`. When it is omitted the insertion lands outside the head, after `</body>`, or is dropped entirely - while the report still claims it was inserted.

Affects **9 apps**, 10 findings.

- **[high] canonical** — Canonical link is inserted outside the head when `</head>` is omitted (unguarded OnEndTag position)  
  `examples/gip/canonical/main.go:129`
- **[high] fontpreload** — When `</head>` is omitted the preloads are emitted after `</body>`, at the very end of the document  
  `examples/gip/fontpreload/main.go:256`
- **[high] hreflang** — head end-tag handler has no name guard, so an omitted `</head>` puts the alternates outside the head or drops them silently  
  `examples/gip/hreflang/main.go:208`
- **[high] noindex** — Robots meta lands after `</body>` when the source omits `</head>`, the one thing the package doc says it must never do  
  `examples/gip/noindex/main.go:154`
- **[high] ogcompute** — With `</head>` omitted nothing is inserted, yet the report claims the tags were inserted and blames a head and body that are both present  
  `examples/gip/ogcompute/main.go:243`
- **[medium] darkmode** — The head end-tag handler has no EndTag.Name() guard and the body fallback is keyed on seeing `<head>`, so on a page that omits `</head>` the tags land outside the head — or are not inserted at all, with a report that says the page has no head or body  
  `examples/gip/darkmode/main.go:149`
- **[medium] preconnect** — Hints are inserted at the head's *implied* end tag, so on a document without `</head>` they land after `</body>` or are dropped while the report claims success  
  `examples/gip/preconnect/main.go:233`
- **[medium] printstyles** — The print stylesheet is inserted at the head's implied end tag, so it lands outside the head or is silently dropped  
  `examples/gip/printstyles/main.go:130`
- **[medium] sri** — The embedded manifest is written at `</head>` and silently omits every subresource below it  
  `examples/gip/sri/main.go:196`
- **[low] darkmode** — validColour accepts any all-alphabetic string as a "named colour", contradicting the package doc's promise that an unparseable colour is refused rather than emitted  
  `examples/gip/darkmode/main.go:88`

### Matching raw source instead of decoded values

A guard tests an attribute's source text, but a browser decodes character references first. A `javascript:` URL or an email address spelled with `&#106;` passes the check untouched. Every one of these is a bypass that is one entity away.

Affects **12 apps**, 12 findings.

- **[high] email** — isJavaScriptURL decides on raw source text, so entity-encoded javascript: URLs survive the strip  
  `examples/gip/email/main.go:532`
- **[high] mixed** — Mixed-content check tests raw attribute source, so a character reference bypasses it entirely — including -strict refusal  
  `examples/gip/mixed/main.go:331`
- **[high] modulesplit** — Uses html.UnescapeString on attribute values, which silently rewrites the URL the file comment claims it preserves  
  `examples/gip/modulesplit/main.go:161`
- **[high] placeholders** — Event-handler and srcdoc attributes are treated as plain string positions, so EscapeAttribute is applied to a JavaScript/HTML context — injection reported as "resolved"  
  `examples/gip/placeholders/main.go:429`
- **[high] redact** — Attribute values are matched as raw source, so any character-reference-encoded address survives redaction  
  `examples/gip/redact/main.go:165`
- **[medium] cspnonce** — isJavaScriptURL reads the raw attribute value, so a character-reference-encoded javascript: URL is not reported and the program exits 0  
  `examples/gip/cspnonce/main.go:244`
- **[medium] ids** — ids and references are compared as raw source, so duplicates spelled with character references are missed and valid fragment links are reported broken  
  `examples/gip/ids/main.go:153`
- **[medium] imgcdn** — decodeAmpersands treats "&amp" as a reference regardless of what follows, silently corrupting URLs it claims it would refuse  
  `examples/gip/imgcdn/main.go:257`
- **[medium] references** — keep() assumes no multi-code-point reference contains markup, so &nvlt; is decoded to a bare "<" and written back as HTML  
  `examples/gip/references/main.go:290`
- **[medium] srcset** — The src is percent-encoded without being decoded first, so any src containing a character reference yields a srcset of broken URLs  
  `examples/gip/srcset/main.go:144`
- **[low] islands** — An existing data-hydrate value is HTML-unescaped and written back raw, changing what the browser sees  
  `examples/gip/islands/main.go:185`
- **[low] jsonld** — typeOf HTML-unescapes script-body text, contradicting the file's own rule that references are not decoded there  
  `examples/gip/jsonld/main.go:258`

### Writer abandoned without Close

An error path returns without closing the Writer. The document is never finished, the native rewriter and its handles wait on the cleanup backstop, and any error from the final flush is lost.

Affects **10 apps**, 10 findings.

- **[medium] charset** — validate() builds a Writer only to test an encoding label and never Closes it  
  `examples/gip/charset/main.go:82`
- **[medium] dupsection** — The Writer is abandoned without Close on both error paths  
  `examples/gip/dupsection/main.go:179`
- **[medium] selectorcoverage** — The selector probe discards its Writer without closing it, one leaked rewriter per selector  
  `examples/gip/selectorcoverage/main.go:259`
- **[medium] tailcomment** — The Writer is abandoned without Close on the io.Copy error path  
  `examples/gip/tailcomment/main.go:108`
- **[medium] tailreport** — The Writer is abandoned without Close on both the write-error and read-error paths  
  `examples/gip/tailreport/main.go:81`
- **[low] clientgone** — CloseWrites abandons its Writer on the Write error path  
  `examples/gip/clientgone/main.go:219`
- **[low] needsrewrite** — Writer is abandoned without Close on the Write error path  
  `examples/gip/needsrewrite/main.go:223`
- **[low] shrink** — structuralCuts abandons its Writer without closing it on the write-error path  
  `examples/gip/shrink/main.go:148`
- **[low] textmap** — Writer is abandoned without Close on every write-error path  
  `examples/gip/textmap/main.go:85`
- **[low] texttruth** — Writer is abandoned without Close when Write fails  
  `examples/gip/texttruth/main.go:121`

### Untrusted text assembled into a comment

Document-derived text is concatenated into a comment and inserted as HTML. A `-->` in that text ends the comment early and the rest becomes live markup - turning inert input into executing script. `CheckComment` exists for exactly this and is not called.

Affects **2 apps**, 2 findings.

- **[high] absolutise** — Document-controlled `<base href>` is injected into the trailing HTML comment, which breaks out of the comment  
  `examples/gip/absolutise/main.go:214`
- **[medium] importmap** — payload comment claims the library checks the injected `<script>` for a raw-text breakout; Element.Before is not checked  
  `examples/gip/importmap/main.go:166`

### Go quoting used for HTML attributes

`fmt` `%q` applies Go string quoting, not HTML attribute escaping. The value is both an injection point and corrupted by the wrong escapes.

Affects **2 apps**, 2 findings.

- **[medium] deployid** — %q is used to quote HTML attribute values: the meta name is an injection point and the escaped deploy id is corrupted  
  `examples/gip/deployid/main.go:100`
- **[medium] idmerge** — Go's %q is used to quote an HTML attribute value, giving attribute injection in the section wrapper  
  `examples/gip/idmerge/main.go:343`

### Documentation contradicting the code

The prose promises behaviour the code does not have. Harmless in isolation; these are teaching files, so the prose is much of the point.

Affects **24 apps**, 24 findings.

- **[high] inlinesvg** — The "inlining is a privilege change" sanitiser is bypassed by `<foreignObject>``<iframe srcdoc>`, giving same-origin script execution  
  `examples/gip/inlinesvg/main.go:181`
- **[high] reencode** — Converts the bytes to UTF-8 but leaves the document declaring the old encoding, then reports "fingerprints match"  
  `examples/gip/reencode/main.go:282`
- **[high] shard** — Page-relative asset URLs are rewritten to a different resource, and the comment claims the opposite  
  `examples/gip/shard/main.go:184`
- **[medium] abtest** — -strict returns an error but the truncated page has already been streamed to the destination  
  `examples/gip/abtest/main.go:256`
- **[medium] breadcrumb** — Package doc claims the library refuses a "`</script>`" in the inserted JSON; on the default path nothing checks it  
  `examples/gip/breadcrumb/main.go:17`
- **[medium] greet** — Attacker-controlled header is written into any attribute the page names, including href and on* — EscapeAttribute is presented as the whole rule  
  `examples/gip/greet/main.go:233`
- **[medium] gunzip** — the comment claims gzip.Reader.Close is where a checksum failure appears; it never is, and the trailerErr branch is dead code  
  `examples/gip/gunzip/main.go:150`
- **[medium] numbering** — Package doc claims re-running is a no-op; it compounds, and -skip-numbered does not do what its help says  
  `examples/gip/numbering/main.go:15`
- **[medium] rebase** — CSS URLs seen before the `<base>` are neither resolved nor counted as Early, so the program removes the base and reports success while leaving broken URLs  
  `examples/gip/rebase/main.go:260`
- **[medium] streamvsmemory** — The "memory floor" is a power-of-two upper bound, reports 0 when no limit works, and the sample output in the package doc cannot be produced by this program  
  `examples/gip/streamvsmemory/main.go:176`
- **[medium] tailreport** — Inline comment says a failed rewrite discards the output, contradicting this file's own package doc, its test, and the library  
  `examples/gip/tailreport/main.go:92`
- **[medium] transitions** — Top-level siblings are never numbered, so they collide on one path and get the same view-transition-name  
  `examples/gip/transitions/main.go:133`
- **[medium] weight** — Report.LargestCandidates is only the srcset maxima, not what either doc comment says it is  
  `examples/gip/weight/main.go:178`
- **[low] deferscripts** — hostOf invents a host for relative URLs that contain "//", contradicting the comment that says a relative src has no host  
  `examples/gip/deferscripts/main.go:151`
- **[low] dimensions** — The package doc says "every image and iframe" but the selector also rewrites video, embed and object  
  `examples/gip/dimensions/main.go:1`
- **[low] email** — The javascript:-URL removal is scoped to three selectors, so form/iframe/input URL attributes are never checked  
  `examples/gip/email/main.go:419`
- **[low] highlight** — the hand-copied raw-text skip list omits plaintext, so content there is escaped and corrupted — the exact failure the package doc says is avoided  
  `examples/gip/highlight/main.go:43`
- **[low] hoiststyle** — the documented usage line uses an -at flag that the program does not define, so the example command exits 2  
  `examples/gip/hoiststyle/main.go:4`
- **[low] mojibake** — The usage example labels the classic mojibake with the direction reversed, contradicting Kind.String and the rest of the doc  
  `examples/gip/mojibake/main.go:5`
- **[low] pagenav** — The documented one-pass streaming mode does not stream: run always reads the whole document into memory  
  `examples/gip/pagenav/main.go:291`
- **[low] poisoned** — The documented table says the memory-limit failure delivers a prefix; the program prints "nothing"  
  `examples/gip/poisoned/main.go:8`
- **[low] preserve** — The package doc's sample output lists a rewrite the program does not have  
  `examples/gip/preserve/main.go:11`
- **[low] tablelayout** — RefusedInParagraph counts refused columns as well as rows, but the report calls them all rows  
  `examples/gip/tablelayout/main.go:86`
- **[info] ids** — Package doc says five reference attributes hold several ids; the code lists six  
  `examples/gip/ids/main.go:24`

### Other, one-off

No shared root cause.

Affects **39 apps**, 45 findings.

- **[high] breadcrumb** — Nested crumb markup (`<a>``<span>`…`</span>``</a>`) duplicates every item and throws away every href  
  `examples/gip/breadcrumb/main.go:180`
- **[high] bust** — -strict fails the rewrite after a prefix of the document has already been written to the destination  
  `examples/gip/bust/main.go:377`
- **[high] formschema** — A `<select>` whose `<option>` end tags are omitted reports the wrong value and the wrong options  
  `examples/gip/formschema/main.go:295`
- **[high] placeholders** — URL scheme guard runs per placeholder value, so two adjacent placeholders compose javascript: and pass unrefused  
  `examples/gip/placeholders/main.go:442`
- **[high] slots** — An abandoned definition leaks its tail into the live document, turning inert `<template>` content into executing markup  
  `examples/gip/slots/main.go:334`
- **[high] toc** — Heading text is inserted as HTML unescaped; text from a raw-text descendant is a working XSS  
  `examples/gip/toc/main.go:335`
- **[medium] autocomplete** — A password field that already has an autocomplete is not counted, flipping the sibling field to current-password  
  `examples/gip/autocomplete/main.go:250`
- **[medium] consentgate** — validate() only rejects three executable type values, so a legacy JavaScript MIME type or a whitespace-only type parks scripts that still run while the report claims they were gated  
  `examples/gip/consentgate/main.go:224`
- **[medium] cspnonce** — style="..." attributes are not reported as unnonceable, yet the policy the program prints blocks every one of them  
  `examples/gip/cspnonce/main.go:80`
- **[medium] deferscripts** — Body detection keys on the `<body>` start tag, so scripts in an implied body are deferred despite the documented promise not to  
  `examples/gip/deferscripts/main.go:80`
- **[medium] emailstrip** — The allow-list admits meta http-equiv + content, which lets `<meta http-equiv="refresh">` through the strip intact  
  `examples/gip/emailstrip/main.go:72`
- **[medium] etag** — The tag is only returned after the body has been written, and the CLI prints the body before the header  
  `examples/gip/etag/main.go:234`
- **[medium] headonly** — a `<template>` in the head stops the rewrite at the template's own contents, so the rest of the head is never rewritten and the report claims otherwise  
  `examples/gip/headonly/main.go:115`
- **[medium] histogram** — namespace stack is corrupted by a foreign-content breakout, mislabelling every element in the rest of the document as svg:  
  `examples/gip/histogram/main.go:109`
- **[medium] hoiststyle** — plausibleDeclarations lets "/*" through, so one style attribute comments out every rule that follows it in the generated stylesheet  
  `examples/gip/hoiststyle/main.go:211`
- **[medium] hoiststyle** — normalise lower-cases custom property names, which are case-sensitive, silently breaking var() references  
  `examples/gip/hoiststyle/main.go:198`
- **[medium] landmarks** — Candidate extent is taken from an unguarded end tag, so a `<p>` with an omitted end tag swallows its siblings and their roles are dropped  
  `examples/gip/landmarks/main.go:247`
- **[medium] linktext** — fromHref title-cases the first *byte*, corrupting any non-ASCII URL slug it writes into the document  
  `examples/gip/linktext/main.go:340`
- **[medium] modernise** — Renames `<xmp>`/`<listing>` to `<pre>` by default, turning inert text into live markup — the exact rename its own comment lists as a failure  
  `examples/gip/modernise/main.go:80`
- **[medium] noopener** — A graceful memory bail-out is turned into success, so an entirely un-hardened page exits 0  
  `examples/gip/noopener/main.go:109`
- **[medium] origins** — cssImports indexes the original string with an offset from a progressively shortened one, so only the first @import in a stylesheet is ever reported  
  `examples/gip/origins/main.go:381`
- **[medium] pagenav** — rel is classified by exact string equality against a selector that matches a token list, so a multi-token rel is rewritten as the wrong link  
  `examples/gip/pagenav/main.go:188`
- **[medium] redact** — A duplicated attribute whose first copy is clean is rebuilt from the later copy, silently changing the value a browser uses  
  `examples/gip/redact/main.go:166`
- **[medium] references** — Decoding attributes through AttributeList writes every copy's value onto the first copy  
  `examples/gip/references/main.go:132`
- **[medium] sandbox** — "No host means same origin" silently exempts data:, blob: and javascript: iframes, and url.Parse failures fail open  
  `examples/gip/sandbox/main.go:124`
- **[medium] shadow** — No end-tag name guard: a shadow root is inserted outside its host and still counted as given  
  `examples/gip/shadow/main.go:202`
- **[medium] slots** — The end-tag repair guard compares against the fill name instead of the tag name, so a colliding name loses an end tag  
  `examples/gip/slots/main.go:313`
- **[medium] slugs** — A heading whose end tag never arrives shifts the plan, giving one heading another heading's anchor  
  `examples/gip/slugs/main.go:202`
- **[medium] split** — The open-tag stack is lol-html's token nesting, so a document with omitted end tags accumulates stale ancestors and parts come out unbalanced  
  `examples/gip/split/main.go:214`
- **[medium] untrack** — Document-controlled query-parameter names are appended as ContentType HTML, so a URL can break out of the report comment  
  `examples/gip/untrack/main.go:169`
- **[medium] upgrade** — A `<style>` the source never closes has its whole body silently deleted  
  `examples/gip/upgrade/main.go:165`
- **[medium] viewport** — Viewport content is split on commas only, so a semicolon- or space-separated user-scalable=no survives and is reported as harmless  
  `examples/gip/viewport/main.go:83`
- **[low] bindings** — Comment says the literal parser accepts a double-quoted string; it accepts single quotes and backticks  
  `examples/gip/bindings/main.go:191`
- **[low] clientgone** — "accepted N of M bytes" compares output bytes accepted against input bytes offered to the rewriter  
  `examples/gip/clientgone/main.go:178`
- **[low] cspnonce** — The -mark comment describes content the code does not insert and calls Element.After "element content", which is the position distinction the raw-text rule turns on  
  `examples/gip/cspnonce/main.go:198`
- **[low] dupsection** — OnEndTag is registered for every content element, so memory grows with the document the example claims not to hold  
  `examples/gip/dupsection/main.go:103`
- **[low] formschema** — The orphan note asserts every orphan carries a form attribute, which is not what the code collects  
  `examples/gip/formschema/main.go:343`
- **[low] glossary** — The text handler inserts markup into raw-text elements its hand-written exclusion list does not know about  
  `examples/gip/glossary/main.go:59`
- **[low] linkreport** — The report attributes text to `r.links[len-1]` and to a single `r.open` flag, so nested or unclosed anchors get the wrong text and produce false findings  
  `examples/gip/linkreport/main.go:113`
- **[low] noopener** — Forms with a new-window target are counted as hardened although nothing is changed  
  `examples/gip/noopener/main.go:140`
- **[low] summary** — Result.Total is documented and rendered but never set by Extract  
  `examples/gip/summary/main.go:71`
- **[low] tablejson** — Renamed keeps only the last rename per original name, so the duplicate count under-reports  
  `examples/gip/tablejson/main.go:406`
- **[low] units** — An unused struct field documents retaining *TextChunk values past their handler  
  `examples/gip/units/main.go:160`
- **[low] untrack** — -mark is documented as leaving a comment but inserts visible page text  
  `examples/gip/untrack/main.go:126`
- **[low] widgets** — Widgets that are skipped for an omitted end tag still get slot attributes written onto their children  
  `examples/gip/widgets/main.go:219`

## Every finding

Severity is the verifier's, assigned after it tried and failed to refute the claim.

### 1. [high] absolutise — Document-controlled `<base href>` is injected into the trailing HTML comment, which breaks out of the comment

*`examples/gip/absolutise/main.go:214`*

The OnDocumentEnd handler appends a report comment as ContentType HTML, and the report line embeds r.base (main.go:283). r.base is not just the trusted -base flag: the base[href] handler replaces it with the document's own `<base href>` (r.base = r.base.ResolveReference(nb)). url.URL.String() emits RawQuery verbatim without escaping, so a query containing "-->" survives intact and terminates the comment. Verified end to end: $ printf '`<base href="/x?q=-->``<script>`alert(1)`</script>`">...' | absolutise -base https://example.com/ ...<!-- absolutise: base=https://example.com/x?q=-->`<script>`alert(1)`</script>` rewritten=2 ... --> The rewrite has turned an inert attribute value in the input into an executing `<script>` in the output — the exact thing the -annotate path one screen earlier is careful to avoid, with the comment "this is an unresolvable URL from the input document, so it is untrusted and must not be able to become markup". Anyone copying this file's "append a summary comment" shape into a response rewriter has an XSS that fires on any page carrying a crafted `<base>`.

### 2. [high] breadcrumb — Nested crumb markup (`<a>``<span>`…`</span>``</a>`) duplicates every item and throws away every href

*`examples/gip/breadcrumb/main.go:180`*

The crumb handler is registered for `nav a, nav span` (via descendants()) and keeps the crumb state in three variables shared by every match: `href`, `text` and `collecting`. When a crumb is spelled the ordinary schema.org way — `<a href="/"><span>Home</span></a>` — the inner `<span>` matches too, resets `text`, and overwrites `href` with the span's (absent) href. The span's end tag then appends one item, and the enclosing `<a>`'s end tag appends the same name a second time, by now with an empty href. Measured on `<nav class="breadcrumb"><a href="/"><span>Home</span></a> / <a href="/docs"><span>Docs</span></a> / <span>Page</span></nav>` the program emits a BreadcrumbList of five items — Home, Home, Docs, Docs, Page — and not one of them carries an "item" URL, because `href` was clobbered before every end tag fired. Anyone who copies this ships invalid structured data to search engines: duplicated positions and no URLs, silently, with `emitted=1` reported as a success. Nothing in the tests covers a nested crumb (main_test.go uses only flat `<a>text</a>`).

### 3. [high] bust — -strict fails the rewrite after a prefix of the document has already been written to the destination

*`examples/gip/bust/main.go:377`*

Bust writes straight to os.Stdout while streaming, and -strict returns an error from inside the element handler part-way through the document. The rewrite stops there, but everything produced up to that point is already in the destination. The package comment sells -strict as the option "for a build step that would rather not ship a page referring to an asset nobody hashed" — what it actually ships is a truncated page. Measured: with a manifest that knows /js/known.js but not /img/unknown.png, `bust -manifest m.txt -strict < in.html > out.html` exits non-zero and leaves out.html holding 97 of the input's 141 bytes, ending at `<script src="/js/known.js?v=abc123"></script>` with no `</body>`</html>. A build step that redirects to a file and does not check the exit status now has a half-page on disk; one that does check the status still has to know to delete the partial file.

### 4. [high] canonical — Canonical link is inserted outside the head when `</head>` is omitted (unguarded OnEndTag position)

*`examples/gip/canonical/main.go:129`*

The insertion point is taken from the head's OnEndTag callback without the name guard the library documents. When a document omits the optional `</head>` (legal HTML and very common), the head is closed by the `<body>` start tag and no `</head>` token exists, so the callback fires on a *foreign* end tag — `</body>` or </html> — and `end.Before(...)` writes there. Verified: `<html><head><title>t</title><body><p>x</p></body></html>` produces `...</body><link rel="canonical" href="..."></html>` and reports `inserted=1`; with `</body>` also omitted the link lands immediately before </html>. Both positions are parsed into the body, which is precisely where this program's own package doc says a canonical link "is not honoured" and where it *removes* any link it finds (`droppedOutside`). So on such a page the tool silently produces the state it exists to prevent while reporting success.

### 5. [high] descript — OnEndTag registered on `<script>` without a CanHaveContent guard aborts the rewrite and truncates stdout on a self-closing SVG/MathML script

*`examples/gip/descript/main.go:122`*

The package doc at lines 12-14 reasons its way out of the guard: "A void element has no end tag, so OnEndTag fails on one rather than doing nothing. Scripts are never void, so this program does not need the guard." That is false in foreign content. In SVG and MathML a self-closing tag really is self-closing, so `<svg><script/></svg>` gives an element with IsSelfClosing()=true and CanHaveContent()=false, and Element.OnEndTag returns "element_add_end_tag_handler: No end tag". The handler returns that error, which poisons the Writer and stops the rewrite. Concrete consequence: `descript < page.html > out.html` on any page containing inline SVG with a self-closing script writes a silently TRUNCATED document. Measured: `printf '<p>a</p><svg><script/></svg><p>b</p>' | descript` emits `<p>a</p><svg>` to stdout, then exits 1 — the prefix is already delivered, so out.html is a half page whose `</svg>`, `<p>b</p>` and everything after are gone.

### 6. [high] email — isJavaScriptURL decides on raw source text, so entity-encoded javascript: URLs survive the strip

*`examples/gip/email/main.go:532`*

Element.Attribute returns raw source with character references left encoded (element.go:397: "The value is raw source text, with character references left encoded"), but isJavaScriptURL matches the literal prefix "javascript:" against that raw value. Every browser and mail client decodes the reference before acting, so the check is bypassed by exactly the forms the sibling example emailstrip already documents as the hole it once had (emailstrip/main.go scheme(): "The first version of this program had exactly that hole, with the library's documentation warning about it on the page above the one being read at the time"). Reproduced against this file: `<a href="&#106;avascript:alert(1)">`, `<a href="jav&#x09;ascript:alert(2)">` and a literal CR in `java\rscript:alert(3)` all pass through with report.JavascriptURLs == 0 — and the CR case is re-serialised by the tokenizer as a plain, working `href="javascript:alert(3)"` in the output.

### 7. [high] emoji — The skipped-region depth counter is never decremented for an implicitly closed element, silently disabling expansion for the rest of the document

*`examples/gip/emoji/main.go:117`*

depth++ is taken for every element in the `skipped` set, and the end-tag handler only decrements when t.Name() == tag. The library is explicit that a foreign end-tag name means the element was closed implicitly and has already ended (element.go OnEndTag: "Both items' handlers run at </ul> ... EndTag.Name reports \"ul\" for both") — the name guard exists to reject a *position*, not to reject the fact that the element ended. `option` is in the skipped set and its end tag is optional, so any ordinary `<select><option>a</select>` leaks depth permanently. Reproduced: input `<p>:smile:</p><select><option>a</select><p>:tada:</p>` yields `<p>😄</p>...<p>:tada:</p>` and reports "1 expansions", while the same document with an explicit `</option>` expands both. A copier gets a text-rewriting pass that silently stops working part-way through any document containing a common HTML idiom, with a report that claims success.

### 8. [high] firstlink — Same stuck depth counter, and it makes the program report mentioned terms as "not mentioned"

*`examples/gip/firstlink/main.go:261`*

Identical shape to glossary: `depth++` for every noLink element, `depth--` only when the end tag's name matches. `option` (in noLink) has an optional end tag and misnesting produces the same foreign end tag, so the counter sticks and every later text node is skipped. Here the damage is doubled because the summary is derived from the same state: reproduced with `<dl><dt>widget<dd>a thing</dl><select><option>x<option>y</select><p>a widget here</p>`, the output is unchanged and the report says "1 terms: 0 linked, 0 already linked by the page, 1 not mentioned / not mentioned: widget" — a positive claim that the page does not mention a term it plainly does. The first pass has the same defect at survey/main.go:161-171: an unclosed `<a>` leaves `linkDepth` stuck, after which no link text is ever recorded and no term is marked AlreadyLinked, so the program adds a second link to text the page already links — the exact outcome the package doc says is "worse than adding none". Fix: decrement unconditionally in both counters and keep the name test only for reporting.

### 9. [high] fontpreload — When `</head>` is omitted the preloads are emitted after `</body>`, at the very end of the document

*`examples/gip/fontpreload/main.go:256`*

The hints are written at the head's end tag. `</head>` is optional in HTML and routinely omitted by hand-written pages and minifiers; with no tree, the head element then ends at the *next* end tag token, which is `</body>`. The `body` start-tag handler, which is the intended fallback, is disabled by `sawHead`, so nothing catches it. Reproduced: `<html><head><title>t</title><body><p>hi</p></body></html>` emits `...</body><link rel="preload" ...></html>` — outside the body, after the entire document has been parsed, where a preload does nothing at all — and the program still reports `preloaded=1`. That is the whole purpose of the program silently not happening, on valid input, with a success report. Fix: make the body handler the fallback whenever the hints have not been placed yet (drop `sawHead` from its guard — with a real `</head>` that token precedes `<body>`, so `placed` is already true), and have the head end-tag handler check `end.Name() == "head"` before writing, noting the case when it is not.

### 10. [high] formschema — A `<select>` whose `<option>` end tags are omitted reports the wrong value and the wrong options

*`examples/gip/formschema/main.go:295`*

An option's end tag is optional in HTML, so `<select name=sort><option>date<option>score</select>` is valid and a browser submits `sort=date`. Every option registers its end-tag handler on the *same* `</select>` token, and those handlers run before the select's own; each reads the shared `text` builder, which by then holds only the *last* option's text and is reset by the first handler that reads it. Reproduced: that input yields `"value": "score", "options": ["score", ""]` — the wrong submitted value, a duplicated option and a phantom empty one. Nothing errors and the JSON looks well formed. For a program whose stated purpose is replay ("everything a client needs to send the same request the browser would") a copier silently replays a different request. main_test.go only ever uses explicitly closed options, so the gap is invisible.

### 11. [high] glossary — The exclusion depth counter never comes back down after an implied or misnested end tag, so the rest of the page is silently left unlinked

*`examples/gip/glossary/main.go:252`*

`depth` is incremented for every noLink element but decremented only when the end tag's name matches. The comment justifying that guard — "These all have mandatory end tags, so a foreign one means the document ended inside the element" — is false: `option` is in noLink and its end tag is optional, and any misnesting (`<p><code>x</p>`) also delivers a foreign end tag with the document continuing. When it happens the counter is stuck above zero and the text handler returns early forever. Reproduced: `<dl><dt>widget<dd>a thing</dl><p>a widget here</p><select><option>x<option>y</select><p>another widget there</p>` links the first mention and silently leaves the second, reporting "1 mentions linked". No error, no warning — the failure mode the package doc opens by calling out ("worse than not doing it at all because the result looks like it worked"). examples/gip/flags gets this right in the same repo: its counter decrements unconditionally and uses the name test only for reporting overreach.

### 12. [high] headings — hidden-region counter never decrements for an implicitly closed element, silently disabling every check for the rest of the document

*`examples/gip/headings/main.go:173`*

hiddenRegion increments c.hidden and decrements it from an Element.OnEndTag callback. The library documents that if nothing closes an element at all — `<p>`a`<p>`b — the end-tag handler never runs (element.go, OnEndTag: "If nothing closes the element at all ... the handler does not run"), and that an implicitly closed element's handler runs at the enclosing tag instead. So a single `<p hidden>` (or `<li hidden>`, `<td hidden>`, `<option hidden>`) leaves c.hidden stuck above zero, and heading() then treats every remaining heading in the document as hidden and skips it. Verified with the built binary: `printf '<p hidden>x<p>y</p><h3>Skipped, no h1</h3><h6>worse</h6>' | headings` prints "0 headings, 2 hidden; 0 findings" and exits 0, while the identical document without the `<p hidden>` prefix reports 3 findings and exits 1. Someone copying this gets an accessibility linter that reports a clean pass on a document it never examined — the worst failure mode for a checker.

### 13. [high] highlight — skip-depth counter is stuck open after an implicitly closed `<option>`, so nothing in the rest of the document is highlighted

*`examples/gip/highlight/main.go:104`*

The element handler increments depth for a skipped tag and decrements it in an OnEndTag callback guarded by `t.Name() != tag`. That guard is the library's recommended test for *positioning* content, and OnEndTag's own documentation warns it is not sufficient for knowing an element is over: an implicitly closed element is handed the enclosing end tag, whose name differs, so the guard returns early and depth is never decremented. `option` and `select` are both in the skipped map, and `<option>` is routinely written without a closing tag. Verified with the built binary: `printf '<select><option>alpha<option>beta</select><p>alpha appears here too</p>' | highlight alpha` outputs the document unchanged and reports "0 marks", while the same paragraph without the select reports 1 mark. depth ends at 2 and never returns to 0, so every text node after the first `<select>` on the page is silently skipped. A copier gets a highlighter that quietly stops working part-way down real pages, with no error and an exit status of 0.

### 14. [high] hreflang — head end-tag handler has no name guard, so an omitted `</head>` puts the alternates outside the head or drops them silently

*`examples/gip/hreflang/main.go:208`*

The alternates are inserted from `e.OnEndTag` registered on `<head>`, with no check that the end tag actually belongs to the head. `</head>` is optional in HTML (and routinely omitted by minifiers and template engines). The library documents this exact trap under Element.OnEndTag - "the handler then runs against the tag that did close them, which belongs to an enclosing element" - and gives the guard `if t.Name() != tag { return nil }`. Without it, two real shapes break, both silently and both reported as success: $ printf '<html>`<head>``<title>`t`</title>``<body>``<p>`hi`</p>``</body>`</html>' | hreflang -alt fr=https://example.com/fr <html>`<head>``<title>`t`</title>``<body>``<p>`hi`</p>``</body>`<link rel="alternate" hreflang="fr" href="..."></html> inserted=1 rewrote=0 removed=0 The link lands after `</body>`, outside the head, where crawlers ignore hreflang entirely - and the program prints inserted=1. $ printf '<!doctype html><html>`<head>``<meta charset=utf-8>``<body>`hi' | hreflang -alt fr=https://example.com/fr <!doctype html><html>`<head>``<meta charset=utf-8>``<body>`hi inserted=0 ...

### 15. [high] inlinesvg — The "inlining is a privilege change" sanitiser is bypassed by `<foreignObject>``<iframe srcdoc>`, giving same-origin script execution

*`examples/gip/inlinesvg/main.go:181`*

The package doc states the inliner exists because inlining an `<img>` into the page is a privilege change, and that the nested `clean` rewrite makes an untrusted file safe: it "drops script and style elements, drops every on* attribute, and drops href values that are not local fragments. ... one that can be pointed at user uploads needs it." Those three rules are the whole filter — every other element and every other attribute is passed through verbatim. Inside `<foreignObject>` the parser switches back to HTML, so an `<iframe>` there is a real HTML iframe, and `srcdoc`/`src` are neither on* nor href. Reproduced: the file `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject width="100" height="100"><iframe srcdoc="&lt;script&gt;alert(document.domain)&lt;/script&gt;"></iframe></foreignObject></svg>` is inlined unchanged into the page (`0 scripts and 0 handlers dropped`), and a srcdoc iframe inherits the embedding page's origin, so that script runs same-origin.

### 16. [high] keywords — Depth counters leak on omitted end tags, silently zeroing the whole report for ordinary HTML

*`examples/gip/keywords/main.go:163`*

The end-tag handler only decrements excludeDepth/skipDepth when EndTag.Name() equals the element's own tag, justified by the comment "These are all elements with mandatory end tags". That is false for two entries in the `skipped` table: `<option>` and `<head>` both have omissible end tags in HTML. When the end tag is omitted, OnEndTag fires against the enclosing element's tag (as OnEndTag documents), the guard rejects it, and skipDepth is never decremented — so `text` drops every remaining text chunk in the document. Reproduced against the real library: `<select><option>alpha<option>beta</select><p>hello world hello</p>` and `<html><head><title>T</title><body><p>hello world hello</p></body></html>` both report `0 words, 0 distinct` (with `</option>`/`</head>` present the same documents correctly report 3 words). A copier gets a keyword extractor that returns an empty ranking, with no error and no warning, on any page with a select menu or an omitted `</head>` — the failure looks like "this page has no content" rather than like a bug.

### 17. [high] linkify — The `t.Name() != tag` guard makes linkify's depth counter stick, silently disabling linking for the rest of the page

*`examples/gip/linkify/main.go:84`*

The end-tag handler only decrements `depth` when the end-tag token's name equals the element's own tag name. But `Element.OnEndTag` fires exactly once per element whatever token closed it, and for an implicitly closed element the token belongs to an ancestor — so the guard is never satisfied and `depth` is never decremented. From that point on `OnDocumentText` returns early for every remaining text node in the document and nothing else is linked, with no error and no counter to show it. This is not an exotic input. Measured on this code: `<p><code>x</p> see http://q.example here` linked 0 URLs (`</li>` and `</p>` omission is ordinary, and `</li>` omission is *valid* HTML5); `<ul><li><a href=x>y<li>z</ul> see http://q.example here` linked 0; `<div><a href="x">y</div> see http://q.example here` linked 0. The identical document with `</a>` present links 1. Someone copying this ships a linkifier that quietly stops working partway down any real-world page and reports `linked=0` as if the page had no URLs.

### 18. [high] mixed — Mixed-content check tests raw attribute source, so a character reference bypasses it entirely — including -strict refusal

*`examples/gip/mixed/main.go:331`*

insecure() is handed the value from e.Attribute(), which the library documents as "raw source text, with character references left encoded". A browser decodes references in attribute values, so `src="http&#58;//evil.example/x.js"` is an http:// subresource to a browser and a string that does not start with "http://" to this checker. Every call site is affected: element src/href/data/poster, srcset members, and the `style="...url(...)"` scan (which also greps the raw source for the literal "url("). Reproduced: `printf '<script src="http&#58;//evil.example/x.js"></script><img src="http://cdn/a.png">' | go run ./examples/gip/mixed -strict` prints "0 blockable, 1 upgradeable", forwards the page, and exits 0. The plain `<img>` is caught; the entity-encoded `<script>` is not. Consequence for a copier: this is a security tool whose headline feature is refusing a page (-strict) and whose file comment goes to some length about failing closed by buffering.

### 19. [high] modulesplit — Uses html.UnescapeString on attribute values, which silently rewrites the URL the file comment claims it preserves

*`examples/gip/modulesplit/main.go:161`*

The whole point of this example, per its header, is exact attribute-value round-tripping: it shows a table of "the attribute value as reported / EscapeAttribute of it — wrong: a different url / decoded, then EscapeAttribute — right". But the decode step uses stdhtml.UnescapeString, which is the wrong decoder for attribute values. In an attribute, a named reference without its semicolon is not a reference when the next character is "=" or ASCII alphanumeric; html.UnescapeString decodes it anyway. The library's Element.Attribute doc states this and names examples/gip/references as the implementation of the correct rule. Reproduced: `printf '<script src="/a.js?x=1&copy=2"></script>' | go run ./examples/gip/modulesplit` emits `<script src="/a.mjs?x=1©=2" type="module">``</script>``<script src="/a.js?x=1©=2" nomodule defer="">``</script>` A browser reads the input as a query parameter named `copy`; both halves of the emitted pair now request a URL containing U+00A9 instead.

### 20. [high] noindex — Robots meta lands after `</body>` when the source omits `</head>`, the one thing the package doc says it must never do

*`examples/gip/noindex/main.go:154`*

The head handler's OnEndTag callback never checks EndTag.Name(), which the library documents as mandatory ("The test is the name... a name that differs is not this element's end tag and no position taken from it belongs to this element", element.go OnEndTag). `</head>` is optional in HTML, and lol-html does not synthesise one: for `<html><head><title>t</title><body>...</body></html>` the head's end-tag callback runs at </html>, so end.Before() inserts the meta at the very end of the document. The `<body>` handler cannot rescue it either, because it bails on `sawHead`. Measured: `printf '<!doctype html><html><head><title>t</title><body><p>hi</p></body></html>' | go run ./examples/gip/noindex -always` emits `...<p>hi</p></body><meta name="robots" content="noindex"></html>` and reports `inserted=1`. A robots meta in the body is not honoured, so a copier's staging host, print view or search page is indexed while the tool reports success - and this is exactly the "append at the end of the output" fallback the package doc (lines 24-27) says is "wrong twice over".

### 21. [high] ogcompute — With `</head>` omitted nothing is inserted, yet the report claims the tags were inserted and blames a head and body that are both present

*`examples/gip/ogcompute/main.go:243`*

`sawHead` is set from the `head` start tag, but the insertion happens in that element's OnEndTag handler — which never runs when `</head>` is omitted (measured: no end-tag token is delivered for an unclosed head). The `body` fallback is then skipped because `sawHead` is true, so `placed` stays false and no og tags reach the document. Worse, `f.inserted` was appended to in `markup()` before placement was attempted, so the report lies. Measured: `printf '<html><head><meta charset="utf-8"><body><h1>Widget &amp; Co</h1><img src="/w.png">' | ogcompute -base https://example.com/p` emits the document unchanged and prints `passes=2 inserted=og:title,og:image` plus `note: no head and no body to put the tags in (1)` — both statements false — and exits 0. Adding `</head>` makes it work. A copier running this in a build pipeline sees a success report and ships pages with no Open Graph tags; `</head>` omission is legal HTML and is what minifiers emit.

### 22. [high] placeholders — Event-handler and srcdoc attributes are treated as plain string positions, so EscapeAttribute is applied to a JavaScript/HTML context — injection reported as "resolved"

*`examples/gip/placeholders/main.go:429`*

`resolveAttribute` recognises exactly two attribute positions: URL attributes (scheme-checked) and everything else (EscapeAttribute). An `on*` attribute is a JavaScript context and `srcdoc` is an HTML-document context, and in both the browser decodes character references in the attribute value *before* the inner language sees it — so EscapeAttribute provides no protection at all there, only quote-safety against ending the attribute. Measured: `<button onclick="greet('{{ name }}')">` with name=`');alert(document.cookie);//` emits `onclick="greet('&#39;);alert(document.cookie);//')"`, and the browser decodes `&#39;` to `'` before parsing the JavaScript, so the injected statement runs; `<button onclick="{{ code }}">` with code=`alert(1)` is emitted verbatim and the report says "1 placeholders: 1 resolved, 0 refused", exit 0. `<iframe srcdoc="{{ x }}">` with x=`<img src=x onerror=alert(1)>` emits `&lt;img …&gt;`, which the parser decodes back into a live element inside the frame.

### 23. [high] placeholders — URL scheme guard runs per placeholder value, so two adjacent placeholders compose javascript: and pass unrefused

*`examples/gip/placeholders/main.go:442`*

`dangerous()` is applied to each substituted value in isolation inside `ReplaceAllStringFunc`, not to the composed attribute value. Neither half of a split scheme trips the prefix test. Measured: `<a href="{{ a }}{{ b }}">x</a>` with a=`java` and b=`script:alert(1)` emits `<a href="javascript:alert(1)">x</a>` and reports "2 placeholders: 2 resolved, 0 refused", exit 0 — while the single-placeholder form of the same value is correctly refused with exit 1. The same bypass works from the template side (`href="java{{ b }}"`). Both halves can come from untrusted values, so this is not a malicious-template-only problem. The consequence for a copier is a sanitiser that reports clean on exactly the input it exists to stop, and an exit status a build gate would trust. Fix: build the candidate value first and run `dangerous()` on the fully composed result before writing it back — refuse the attribute as a whole (leaving the source untouched) rather than deciding placeholder by placeholder.

### 24. [high] plaintext — Skip-depth guard never unwinds for `<head>` and `<option>`, whose end tags are omissible — the whole document's text is silently dropped

*`examples/gip/plaintext/main.go:152`*

`skipped` contains `head` and `option`. Both increment `skipDepth`, and both rely on an OnEndTag handler to decrement it — but `</head>` and `</option>` are exactly the end tags HTML lets an author omit, and the inline comment asserting "These elements do not have omissible end tags" is wrong for both. Measured against this library: for `<html><head><title>t</title><body>…` the head's OnEndTag handler never runs at all (no end-tag token is synthesised), and for `<select><option>a<option>b</select>` the option handlers are handed `</select>`, so `t.Name() != tag` returns nil and the counter is never decremented. Consequence for a copier: `printf '<html><head><title>T</title><body><p>Hello world</p><p>Second</p>' | plaintext` prints an empty document, exit 0 — every byte of prose is dropped with no error, no warning and no test failure. `<p>before</p><select><option>a<option>b</select><p>after</p>` prints only "before". `</head>` omission is legal and is what most HTML minifiers emit, so this is the common case, not a corner one.

### 25. [high] readingtime — skipDepth is never decremented when a skipped element's end tag is implied, so the rest of the document counts as zero words

*`examples/gip/readingtime/main.go:123`*

The skip counter is incremented for every skipped element (script, style, head, title, select, option, ...) but the end-tag handler only decrements when t.Name() == tag. Per the library's own OnEndTag documentation an omitted end tag makes the handler run against the *enclosing* tag, so the guard is false and skipDepth is never lowered again — the counter is stuck above zero for the remainder of the document and OnDocumentText returns early for every later chunk. Consequences are silent and large: `<html><head><title>T</title><body><p>one two three four</p></body></html>` (a legal, very common shape — `</head>` is optional and minifiers drop it) prints "no words" instead of 4 words, and `<p>hello there</p><select><option>a</select><p>one two three four five</p>` prints 2 words instead of 7 because `<option>`'s implied end leaves one level stuck.

### 26. [high] redact — Attribute values are matched as raw source, so any character-reference-encoded address survives redaction

*`examples/gip/redact/main.go:165`*

redactAttributes runs the regexes against `a.Value`, which Element.Attribute/AttributeList document as "raw source text, with character references left encoded". The text path deliberately calls stdhtml.UnescapeString before matching; the attribute path deliberately does not. So an address written with references is invisible to the patterns and stays in the page fully functional. Reproduced: `<a href="mailto:bob&#64;example.com" title="call &#43;1 555 123 4567">` comes out as `<a href="mailto:bob&#64;example.com" title="call &#43;[phone removed]">` - the email is untouched and the browser still resolves the mailto, while the same address in text is removed. The package doc presents the source-in/source-out rule as the correct handling and gives "the &amp; in a query string comes back as &amp;amp;amp; the second time round" as the justification, but that doubling comes from escaping without decoding, not from decoding and re-escaping; it never mentions that not decoding defeats the redaction.

### 27. [high] reencode — Converts the bytes to UTF-8 but leaves the document declaring the old encoding, then reports "fingerprints match"

*`examples/gip/reencode/main.go:282`*

Convert transcodes every byte through the table and writes the result, but nothing ever touches the document's own charset declaration. A legacy page that says `<meta charset="windows-1252">` (the normal way such a file identifies itself, and the exact case the usage line `reencode -from windows-1252 < old.html > new.html` describes) comes out as UTF-8 bytes still declaring windows-1252. Reproduced: input `<!doctype html><meta charset="windows-1252"><p>caf\xe9 \x93quoted\x94>` produces output containing `charset="windows-1252"` alongside `c3 a9` and `e2 80 9c`, and the program prints "64 bytes in, 69 out; 25 characters, fingerprints match" and exits 0. A browser opening that file reads it as windows-1252 and renders "cafÃ©" and "â€œquotedâ€" - every non-ASCII character is mojibake.

### 28. [high] shard — Page-relative asset URLs are rewritten to a different resource, and the comment claims the opposite

*`examples/gip/shard/main.go:184`*

slash's doc comment says a path with no leading slash "was relative to the page, and this program will not guess where that is, so those are left alone by the caller of this function". No caller leaves them alone: one() only bails out for "//", "://" and "data:", so anything else falls through to `prefix + "//" + host + slash(raw)`, and slash prepends "/" instead of declining. Confirmed by running the program with -hosts s0.example,s1.example: `<img src="img/a.png">` became `<img src="https://s1.example/img/a.png">`. On a page served from /blog/post.html that URL resolved to /blog/img/a.png and now points at /img/a.png on another host — every such asset 404s, silently, and the report counts it as successfully moved. The same fall-through mangles fragment-only and query-only references: `<link href="#frag">` became `https://s1.example/#frag` and `<img src="?v=1">` became `https://s1.example/?v=1`. Someone copying this for a CDN rollout breaks every relative asset on the site.

### 29. [high] slots — An abandoned definition leaks its tail into the live document, turning inert `<template>` content into executing markup

*`examples/gip/slots/main.go:334`*

When a definition outgrows MaxDefinition, or holds a raw-text element, collector.add returns false and the handlers return without removing the token — the comment on add says this avoids "half a definition in the output", but it produces exactly that. The `<template>` tags were already taken away by RemoveAndKeepContent at the start tag, so the surviving tail is no longer inside a template: it lands in the page as live content. Confirmed by running the program: `<p>before</p><template data-fill="x">a<script>alert(1)</script>b</template><slot name="x">default</slot>` produced `<p>before</p><script>alert(1)</script>b<slot name="x">default</slot>` — a script that the browser would have treated as inert template content now executes. A 72 KB definition against the default 64 KiB limit leaked 6,493 bytes of its tail into the page the same way. The package doc claims "The definition is dropped rather than mangled" and the test named TestADefinitionLargerThanTheLimitIsDropped only asserts the definition is not emitted twice, so nothing catches it.

### 30. [high] toc — Heading text is inserted as HTML unescaped; text from a raw-text descendant is a working XSS

*`examples/gip/toc/main.go:335`*

renderList writes the accumulated heading text into the table of contents as ContentType HTML with no escaping, justified by the comment at main.go:282: "Re-emitting source text as HTML round-trips, because a literal < could not have been text in the first place." That invariant is false. OnText("h2, h3, h4") also fires for text nodes inside descendants of the heading, and inside a raw-text descendant (script, style, textarea, title, iframe, noscript, xmp) a literal "<" IS text. Reproduced against the built binary: $ printf '`<div id="toc">``</div>`\n<h2>Hi`<script>`document.write("`<img src=x onerror=alert(1)>`")`</script>`</h2>' | toc -at-end ... (or -marker '#toc' on a file) `<div id="toc">`<ul>`<li>`<a href="#hidocument-write-img-src-x-onerror-alert-1">Hidocument.write("`<img src=x onerror=alert(1)>`")`</a>``</li>`</ul>`</div>` The `<img onerror>` in the output is a live element, not text: this example turns any page carrying an inline `<script>` or `<style>` inside a heading into stored XSS in the generated contents.

### 31. [high] upgrade — OnEndTag registered on `<style>` without the CanHaveContent guard aborts the whole rewrite

*`examples/gip/upgrade/main.go:140`*

The `style` handler calls `e.OnEndTag(...)` unconditionally. `Element.CanHaveContent` is false for a self-closing element in foreign content, and `OnEndTag` on such an element returns an error (element.go: "OnEndTag returns an error, because there is no end tag to wait for, and that error fails the rewrite. So a handler on a selector that can match a void element must check this before calling OnEndTag"). Selectors ignore namespaces, so `style` matches `<svg><style/>`, which is ordinary SVG. Confirmed: `printf '<p>hello</p><svg><style/></svg><img src="http://a.example/x.png">' | upgrade` prints `<p>hello</p><svg>` to stdout and then fails with `lolhtml: element handler for "style": lolhtml: element_add_end_tag_handler: No end tag.` and exit 1. Consequence for a copier: a single self-closing SVG style (or a `<svg><script/>`, `<svg><title/>` under an equivalent selector) kills the rewrite on that document, and because a failed rewrite has already delivered a prefix, the destination is left holding a truncated document that the caller's error handling cannot take back.

### 32. [high] widows — IsSelfClosing() used as "this element is empty", so a heading containing `<span/>` loses its `</h1>`

*`examples/gip/widows/main.go:225`*

Element.IsSelfClosing reports how the tag was *written*, not whether it is empty — the library doc says so explicitly and gives `<div/>` as the counterexample (IsSelfClosing true, CanHaveContent true, "using it as [a test for whether an element is empty] is wrong wherever an author wrote a slash out of habit"). widows ORs it with `!CanHaveContent()` and then calls `e.Remove()`, which removes the element *and its whole subtree*. For an HTML element written with a stray trailing slash the subtree is the rest of the heading, and the token that closes it is the heading's own end tag — so `e.Remove()` deletes the `</h1>`. Verified against the built example: `<h2>Read the <a href="/x"/>docs now</h2><p>after</p>` produces `<h2>Read the <a href="/x"/>docs now<p>after</p>` — the `</h2>` is gone, and the following paragraph is now inside both the heading and the anchor. `<h1>x y <span/>z w</h1><h2>e f g</h2>` produces `<h1>x y <span/>z w<h2>e f g</h2>` — the whole next heading is nested inside the first.

### 33. [medium] abtest — -strict returns an error but the truncated page has already been streamed to the destination

*`examples/gip/abtest/main.go:256`*

The package doc justifies -strict with "a page that has silently lost half its content is worse than a failed request", and the test only asserts err != nil. But Rewrite writes straight into dst, so when the end-tag handler returns the strict error the prefix is already in the destination and nothing removes it. Measured: $ printf '`<p>`header`</p>`<ul><li data-experiment="hero" data-variant="LOSER">lose<li data-experiment="hero" data-variant="WINNER">keep</ul>`<p>`footer`</p>`' | abtest -strict any 'hero:LOSER=0,WINNER=10000' `<meta name="ab-bucket" content="hero=WINNER">``<p>`header`</p>`<ul> (stdout, exit 1) So -strict does not refuse the document; it delivers a shorter, more broken one and reports an error on the side. Copied into an HTTP handler with dst = http.ResponseWriter, this ships a half page under a 200 that has already been committed — the exact hazard Writer.Write documents ("a caller who returns an error to refuse a document has already delivered a short version of it unless it held the output itself").

### 34. [medium] autocomplete — A password field that already has an autocomplete is not counted, flipping the sibling field to current-password

*`examples/gip/autocomplete/main.go:250`*

scanner.field returns early when the element already carries an autocomplete attribute (line 250), before the password tally at lines 257-260. So a password field that already declares autocomplete="new-password" does not contribute to form.passwords, the "two password fields means a password is being set" rule in decide() never fires, and the un-annotated sibling gets current-password. Measured: $ printf '`<form action="/x">`<input type="password" autocomplete="new-password" name="pw">`<input type="password" name="confirm">``</form>`' | autocomplete ...<input type="password" name="confirm" autocomplete="current-password"> That is precisely the failure the package doc says the whole two-pass design exists to prevent: "getting it backwards … makes a password manager offer to fill a new-password field with the old password". A partially annotated registration or change-password form — a common state for a page being incrementally fixed — is made worse by running this program, silently.

### 35. [medium] autoplay — inScript goes negative after the first reported script, so later scripts are never scanned and ordinary prose is

*`examples/gip/autoplay/main.go:228`*

When a play call is found, the handler sets s.inScript = 0 to stop reporting the current script. The script's end tag handler then runs and decrements it to -1. From then on the guard `if s.inScript == 0 { return nil }` is false for every text node in the document, so: (a) the next `<script>` increments the counter back to 0 and its own body is skipped entirely, and (b) any ordinary page text is scanned and reported as a playing script. Measured: '`<script>`a.play()`</script>``<p>`x`</p>``<script>`b.play()`</script>``<script>`c.play()`</script>`' -> "1 script(s)" (want 3) '`<script>`a.play()`</script>``<p>`call video.play() to start`</p>`' -> "2 script(s)" (the `<p>` is prose) The second case directly contradicts the example's own test, which asserts that `<p>call video.play() to start</p>` reports 0 — true only when it is the first thing in the document.

### 36. [medium] breadcrumb — Package doc claims the library refuses a "`</script>`" in the inserted JSON; on the default path nothing checks it

*`examples/gip/breadcrumb/main.go:17`*

The file comment says of the JSON-LD content: "The library refuses that outright now, which means the program cannot get it wrong quietly - ... That is why encoding/json is not used here: it does not escape the slash, so its output would be refused, correctly." That is only true for the -placeholder path, which uses Element.SetInnerContent on a `<script>`. The default path builds the whole element as a string and inserts it with EndTag.After on the nav's end tag, and ErrRawTextBreakout checks only Element.Prepend/Append/SetInnerContent and EndTag.Before — an insertion after an end tag writes outside the element and is deliberately not checked (rawtext.go, "What is checked is the position"). Confirmed against the library: end.After of `<script type="application/ld+json">{"n":"</script><img src=x onerror=alert(1)>"}</script>` returns nil and emits the breakout verbatim.

### 37. [medium] charset — validate() builds a Writer only to test an encoding label and never Closes it

*`examples/gip/charset/main.go:82`*

The encoding label is validated by constructing a real rewriter and throwing it away: the *Writer is discarded into `_` and Close is never called. NewWriter has already allocated the native rewriter and its selectors by the time it returns, and the library is explicit that the runtime cleanup is "a leak guard, not the supported path" and that callers must "Close every Writer, including one being abandoned" — partly because that cleanup stops working entirely as soon as any handler closes over the Writer. validate() runs once per document (it is the first thing `run` does), so a server that copies this idiom to check a Content-Type label per request holds one unfreed rewriter per request until the GC gets round to it, and a reader who adapts the helper to also register a handler that touches the Writer converts it into a permanent leak with nothing reporting it. The fix is one line: keep the Writer and close it — `w, err := lolhtml.NewWriter(io.Discard, lolhtml.WithEncoding(f.encoding)); if err != nil { ...

### 38. [medium] collapse — depth is incremented before the CanHaveContent bail-out, so a self-closing foreign raw-text element disables collapsing for the rest of the document

*`examples/gip/collapse/main.go:136`*

`element` does `c.depth++` and only then returns early when `!e.CanHaveContent()`, without registering the end-tag handler that would decrement it. CanHaveContent is false for self-closing foreign elements, and IsRawText matches several tags that occur inside <svg>: title, style, script. So `<svg>`<title/>`</svg>` leaves depth stuck at 1 forever and every later text chunk takes the `c.depth > 0` branch. Verified: ``<p>`a b`</p>`<svg>`<title/>`</svg>`<p>`c d`</p>`` outputs ``<p>`a b`</p>`<svg>`<title/>`</svg>`<p>`c d`</p>`` — the second paragraph is not collapsed at all, and the report ("6 bytes of text -> 3") does not mention that the rest of the page was skipped. The code also contradicts its own comment, which explains the branch purely in terms of <plaintext> ("nothing closes it, so nothing after it is prose this program may touch") — deliberate for plaintext, accidental for a self-closing SVG title.

### 39. [medium] consentgate — validate() only rejects three executable type values, so a legacy JavaScript MIME type or a whitespace-only type parks scripts that still run while the report claims they were gated

*`examples/gip/consentgate/main.go:224`*

validate()'s own comment says it "refuses a configuration that cannot work, rather than producing a document that looks gated and is not", and it knows the empty-type rule (an empty type is treated as JavaScript). But the executable check is three exact strings. The HTML spec's JavaScript MIME type list has ~17 essence matches, and the type attribute is stripped of leading/trailing ASCII whitespace before the check. Verified against the built binary: `-gated-type text/ecmascript` produces `<script data-consent-src="https://third.party/a.js" data-consent-type="" type="text/ecmascript">`</script>`` and reports `gated=1`; `-gated-type ' '` produces `type=" "` and also reports `gated=1`. In both cases the browser still executes the script, so the operator believes third-party code is gated when it is not — the exact failure the function exists to prevent.

### 40. [medium] controls — OnEndTag called on "template" without the CanHaveContent guard: a self-closing foreign <template/> aborts the rewrite and truncates the page

*`examples/gip/controls/main.go:87`*

Same defect as in cspnonce. `f.template` is registered for the selector `template` and calls `e.OnEndTag` unconditionally; OnEndTag returns an error when the element cannot have content, and that error fails the rewrite (element.go:203-206). A self-closing ``<template/>`` inside foreign content has CanHaveContent() == false. Verified: `printf '<svg>`<template/>`</svg><video src=a.mp4>' | controls` writes `<svg>` to stdout, then `controls: lolhtml: element handler for "template": lolhtml: element_add_end_tag_handler: No end tag.`, exit 1 — and the <video> is never processed. Consequence for a copier: a valid page silently loses its rewrite and the client receives a truncated document. Fix: `if !e.CanHaveContent() { return nil }` before the OnEndTag registration (an element with no content cannot enclose media, so the depth counter does not need incrementing either — note f.tmpl++ currently also runs on the failing path).

### 41. [medium] cspnonce — OnEndTag called on "script, style" without the CanHaveContent guard: a self-closing foreign <script/> aborts the rewrite and truncates the page

*`examples/gip/cspnonce/main.go:158`*

Element.OnEndTag is documented to return an error when CanHaveContent() is false, and that error fails the whole rewrite: "a handler on a selector that can match a void element must check this before calling OnEndTag" (element.go:203-206). The selector `script, style` matches self-closing foreign elements, for which CanHaveContent is false. Verified against the built binary: `printf '<svg>`<script/>`</svg>' | cspnonce -nonce abcdefgh` prints `<svg>` to stdout and then fails with `lolhtml: element handler for "script, style": lolhtml: element_add_end_tag_handler: No end tag.`, exit 1. Consequence for a copier: a page containing inline SVG with a self-closed <script/> or <style/> — perfectly valid markup — makes the tool hard-fail, and because the rewrite streams straight to os.Stdout it has *already delivered a truncated prefix* of the document to the client before the error is noticed. Every other example in this batch (comments, cookiebanner, corpus, csrf, darkmode) guards the call; this one does not. Fix: guard the registration, e.g.

### 42. [medium] cspnonce — isJavaScriptURL reads the raw attribute value, so a character-reference-encoded javascript: URL is not reported and the program exits 0

*`examples/gip/cspnonce/main.go:244`*

Element.Attribute returns raw source text with character references left encoded (element.go:397-398). isJavaScriptURL strips whitespace/control characters but never decodes references, so `href="&#106;avascript:alert(1)"` is seen as the literal string `&#106;avascript:...` and does not match. Verified: `printf '`<a href="&#106;avascript:alert(1)">`x`</a>`' | cspnonce -nonce abcdefgh` reports nothing under "no nonce can cover" and exits 0. The package doc promises "Exit status is 1 if the document contains something a nonce cannot cover — an inline event handler, or a javascript: URL", and the function's own comment claims to have handled bypasses ("java\tscript: is a real bypass, so the comparison is made after removing them"), so a copier will reasonably trust the clean exit. The sibling example in this same batch does it correctly: comments/main.go:311 runs `stdhtml.UnescapeString(v)` before the scheme check, precisely for this reason. Fix: decode first — `v = stdhtml.UnescapeString(v)` — then strip whitespace/controls, then compare the scheme.

### 43. [medium] cspnonce — style="..." attributes are not reported as unnonceable, yet the policy the program prints blocks every one of them

*`examples/gip/cspnonce/main.go:80`*

The program prints `style-src 'nonce-<value>'` as the policy to install, and its stated job is to "report the constructs that no nonce can rescue". A nonce cannot be applied to an inline style attribute: under CSP, style-src governs style="..." and only 'unsafe-hashes'/'unsafe-inline' permits it. The scan looks only at eventHandlerAttrs and at javascript: URLs, so `style` attributes are invisible to it. Verified: `printf '`<p style="color:red">`x`</p>`...' | cspnonce -nonce abcdefgh` reports no unnonceable construct and exits 0. Consequence for a copier: they ship the printed header, and every element carrying a style attribute renders unstyled in production, with the tool having certified the page as clean. This is the same class of construct as the inline event handler already listed, and more common than javascript: URLs. Fix: report `style` as an unnonceable construct alongside the event handlers (or, at minimum, say in the package doc that style attributes are outside what the printed style-src covers).

### 44. [medium] darkmode — The head end-tag handler has no EndTag.Name() guard and the body fallback is keyed on seeing <head>, so on a page that omits </head> the tags land outside the head — or are not inserted at all, with a report that says the page has no head or body

*`examples/gip/darkmode/main.go:149`*

OnEndTag fires on whatever token closes the element, and the library is explicit that the name must be checked before using that position: "a name that differs is not this element's end tag and no position taken from it belongs to this element" (element.go:960-969). darkmode calls `end.Before(markup, lolhtml.HTML)` without checking `end.Name()`. Two verified consequences. (1) `printf '<html>`<head>``<title>`t`</title>``<body>`x`</body>`</html>' | darkmode -dark '#101014'` emits `...`<body>`x`</body>``<meta name="theme-color" ...>`</html>` — the head handler fired on `</html>`, so the tags land after </body>, not in the head as the package doc's own example shows. Omitting </head> is common (HTML permits it; minifiers with removeOptionalTags emit it). (2) When nothing closes the head at all, `printf '<html>`<head>``<title>`t`</title>``<body>`x' | darkmode -dark '#101014'` inserts nothing and reports `note: no head and no body to put the tags in` — on a document that has both. The `body` fallback at line 163 is disabled by `sawHead`, which is set at the <head> *start* tag, i.e.

### 45. [medium] deferscripts — Body detection keys on the <body> start tag, so scripts in an implied body are deferred despite the documented promise not to

*`examples/gip/deferscripts/main.go:80`*

inBody is set only by a handler on the literal `body` element. HTML lets the body start tag be omitted — a parser opens the body at the first element that is not head content — and minifiers strip it routinely. lol-html reports tokens, not the parser's implied tags, so for such a document inBody is never true and every body script is deferred. Measured: `<html>`<head>``<script src="/h.js">``</script>``</head>``<p>`x`</p>``<script src="/b.js">``</script>`` reports `deferred=2` with no note, where the same document with ``<body>`` spelled reports `deferred=1` plus "a script in the body was left alone". Concrete consequence: this is precisely the failure the package doc spends two paragraphs warning about — "deferring a script that calls document.write after parsing has finished blanks the page", and "a script placed late in the body was often placed there deliberately".

### 46. [medium] deployid — %q is used to quote HTML attribute values: the meta name is an injection point and the escaped deploy id is corrupted

*`examples/gip/deployid/main.go:100`*

Meta() builds raw markup that is inserted with ContentType HTML, quoting both attribute values with Go's %q. %q is strconv.Quote — Go string quoting, not HTML attribute quoting — and the two differ in exactly the way that matters. 1. r.Name is not escaped at all. %q turns a `"` in the name into `\"`, and HTML does not honour backslash escapes, so the attribute value ends at that quote. Measured: name = `x">`<img src=q onerror=alert(1)>`<meta z="` emits ``<meta name="x\">``<img src=q onerror=alert(1)>``<meta z=\"" content="d7f3a91">``, which x/net/html parses as three nodes including a live ``<img src="q" onerror="alert(1)">``. Every other example in the tree writes `="` + lolhtml.EscapeAttribute(v) + `"` (abtest, canonical, captions, charset, csrf, cookiebanner, beacon…); this one is the outlier, and its own comment on the line above insists values are "escaped rather than trusted", which makes a copier trust %q as the escaping. 2. Even the correctly-escaped id is then corrupted by the second quoting.

### 47. [medium] dupsection — The Writer is abandoned without Close on both error paths

*`examples/gip/dupsection/main.go:179`*

Duplicate has no `defer rw.Close()`, and two returns leave the rewriter open: the rw.Write failure at line 179 and the read failure at line 202. Only the success path at line 205 closes it. NewWriter's doc calls the drop-without-Close cleanup "a backstop rather than a second way of doing this" and says "Close every Writer, including one being abandoned" — the native rewriter, its selectors and, here, one cgo handle per content element (see the next finding) stay alive until a GC cycle happens to run the cleanup. This is teaching code for a streaming proxy, and the sibling examples in the same batch all get it right (dir uses `defer w.Close()`, doctypepick/email/emailstrip/envbadge/encodingmatrix all call Close explicitly on the error path), so this one reads as the accident it is. Fix: add `defer rw.Close()` immediately after the NewWriter error check and keep `return st, rw.Close()` as the checked call — the second Close returns nil, which is the shape the library's own Rewrite uses (lolhtml.go:1941).

### 48. [medium] emailstrip — The allow-list admits meta http-equiv + content, which lets <meta http-equiv="refresh"> through the strip intact

*`examples/gip/emailstrip/main.go:72`*

AllowedElements contains "meta" and AllowedAttributes["meta"] contains both "http-equiv" and "content", and no handler inspects the URL inside a content value — the URL check only fires for attributes literally named href or src. Reproduced against this file: `<meta http-equiv="refresh" content="0;url=https://evil.example/phish">` survives byte-for-byte and the report says "kept 7 elements, removed 2" with nothing noted. That is a silent hole in the one property this program argues for at length: "An allow-list is the only defensible shape for this. A block-list is a list of the things somebody thought of". The consequence for a copier — and this allow-list is well factored enough to be copied wholesale into a general HTML sanitiser — is an attacker-controlled redirect that the report certifies as clean. Fix: drop "http-equiv" (and with it the refresh vector) from the meta allow-list, or special-case it: allow http-equiv only for content-type/x-ua-compatible, and run the content value's URL portion through allowedURL when http-equiv is refresh.

### 49. [medium] etag — The tag is only returned after the body has been written, and the CLI prints the body before the header

*`examples/gip/etag/main.go:234`*

The design premise stated at the top is that the etag names the output without producing it, "which is what makes it usable as a header", and a test is even named TestTheTagIsKnownBeforeTheBodyIsWritten. But `Rewrite` returns the Tag only after the full rewrite, so a caller has no way to obtain it before the first byte reaches `dst`. `main` demonstrates the inversion: with `-body` it emits the whole document and then the header line (verified: ``<p>`...`</p>`ETag: "v3-1af020cd49aa1166"`). Someone copying this into an http.Handler writes `Rewrite(src, w, ...)` and then `w.Header().Set("ETag", t.Value())`, which is a no-op — net/http commits the header map on the first body write, so the validator is silently dropped and the caching behaviour the whole program exists for never happens. Fix: split the API so the tag is available first — e.g. `Tag(input []byte, version string) Tag` plus `Rewrite(dst, input)` — or have Rewrite take a `func(Tag) error` called before the writer is created; and have main print the header before the body.

### 50. [medium] greet — Attacker-controlled header is written into any attribute the page names, including href and on* — EscapeAttribute is presented as the whole rule

*`examples/gip/greet/main.go:233`*

The package doc frames this program as the reference for inserting "a string an attacker chooses" into four positions, and gives EscapeAttribute as the complete answer for the attribute position ("it is not optional even though nothing visible breaks without it"). But `attribute` accepts *any* suffix after `data-greet-` except json/append/prepend, so the header value lands in whatever attribute the page names. EscapeAttribute only stops the value escaping its quotes; it does nothing about the attribute's own semantics. Reproduced: ``<a data-greet-href="X-Url">`` with `X-Url: javascript:alert(document.domain)` emits `<a data-greet-href="X-Url" href="javascript:alert(document.domain)">`, and ``<div data-greet-onclick="X-N">`` with `X-N: alert(1)` emits `onclick="alert(1)"`. Someone copying this pattern into a personalisation edge worker gets stored/reflected XSS from a request header, in the one example that exists to teach the safe rules. main_test.go never exercises a URL or event-handler attribute.

### 51. [medium] gunzip — the comment claims gzip.Reader.Close is where a checksum failure appears; it never is, and the trailerErr branch is dead code

*`examples/gip/gunzip/main.go:150`*

The comment says the trailer is read at zr.Close() "so this is where a checksum failure appears". Go's compress/gzip verifies CRC32 and ISIZE inside Read when the stream ends, and Reader.Close just forwards the flate decompressor's error — it returns nil for both a bad checksum and a truncated stream. Measured against compress/gzip directly: a corrupted CRC gives copyErr="gzip: invalid checksum" and closeErr=nil; a stream truncated to 90% gives copyErr="unexpected EOF" and closeErr=nil. So `case trailerErr != nil` is unreachable and the program's integrity check rests entirely on the copyErr branch, not on where the comment points. The program still reaches the right answer, but the teaching point is false, and it is the dangerous direction: a reader who takes away "close the gzip reader and the checksum is verified" and then restructures — checking only Close in a helper, using io.CopyN, or swallowing the copy error as an expected EOF — accepts a corrupt or truncated body as verified.

### 52. [medium] headonly — a <template> in the head stops the rewrite at the template's own contents, so the rest of the head is never rewritten and the report claims otherwise

*`examples/gip/headonly/main.go:115`*

The stop handler matches "*" and ends the head at the first element not in headElements. headElements includes "template", but the handler also sees the elements *inside* the template, which are ordinary flow content and are not in the list — so the rewrite stops at the template's first child. Verified with the built binary on ``<head>``<template>``<div>`t`</div>``</template>`<link rel=stylesheet href=a.css>`<title>`T`</title>``</head>``<body>``<p>`body`</p>``</body>``: it reports "stopped at <div>, the first element that cannot be in a head" and the output is the input verbatim — the link never gets data-critical and the title never gets its bullet. The same document with the template removed rewrites both. The consequence for a copier is a head rewrite that silently does nothing on any page using a template (component frameworks emit them routinely), while the report states success and names an element that is in fact perfectly legal in a head.

### 53. [medium] histogram — namespace stack is corrupted by a foreign-content breakout, mislabelling every element in the rest of the document as svg:

*`examples/gip/histogram/main.go:109`*

The comment on the pop guard asserts "These elements do not have omissible end tags, so the guard is a statement of that rather than a workaround." That is not the only way an entry fails to be popped. An HTML tag name inside an <svg> takes the parser out of foreign content (Element.NamespaceURI documents this: "An HTML tag name inside an <svg> ends the svg - 44 names do it"), after which sibling tags still inside the source <svg> report the HTML namespace, get pushed onto the stack, and are then closed by </svg> — a name that fails the guard — so their entry is never popped while the </svg> pop unwinds the wrong one. Verified with the built binary: `printf '<svg><circle/>`<p>`a`</p>`<rect/></svg>`<div>`after`</div>``<span>`x`</span>`' | histogram 20` reports svg:p, svg:div and svg:span; the same document without the <p> breakout correctly reports div and span with no prefix. The stack ends at depth 2 and never returns, so every element for the remainder of the document is attributed to SVG.

### 54. [medium] hoiststyle — plausibleDeclarations lets "/*" through, so one style attribute comments out every rule that follows it in the generated stylesheet

*`examples/gip/hoiststyle/main.go:211`*

plausibleDeclarations is documented as refusing "anything that would not survive being written into a stylesheet", but it only rejects <>{}" — it does not reject a CSS comment opener. A declaration block containing an unterminated /* is written verbatim into the single generated <style> element, where it comments out the closing brace and every later rule (CSS closes an unterminated comment at EOF). Verified with the built binary: `printf '`<p style="color:red;/*">`a`</p>`<b style="color:blue">b</b>' | hoiststyle` emits ``<style>`.s-jugwrqps{color:red;/*}.s-alagfhqp{color:blue}`</style>`` and reports "hoisted=2 rules=2 skipped=0" — the <b> silently loses its style, and so would every other element on the page. On a page carrying any untrusted markup, one style attribute is enough to strip the styling from every element the tool touched, and the tool reports success. Fix: reject a block containing "/*" or "*/" (adding them to the refusal alongside <>{}") and put it in the skipped list, or strip comments during normalise.

### 55. [medium] hoiststyle — normalise lower-cases custom property names, which are case-sensitive, silently breaking var() references

*`examples/gip/hoiststyle/main.go:198`*

normalise lower-cases every property name, justified by the comment "Property names are lower-cased because CSS matches them case-insensitively. Values are not: a font family, a content string or a custom property value can all be case-sensitive." Custom property *names* are case-sensitive too — --Brand and --brand are different properties — so the rule the comment relies on does not hold for them. The value is left alone, so a var() reference to the original spelling stops resolving. Verified with the built binary: `printf '`<p style="--Brand:#fff;color:var(--Brand)">`x`</p>`' | hoiststyle` emits `.s-jkyn7c2i{--brand:#fff;color:var(--Brand)}` — the definition and the reference no longer match, and the element loses its colour with no error and no entry in the skipped list. Anyone copying this into a build step corrupts every page that uses mixed-case custom properties. Fix: skip the lower-casing when the name starts with "--" (`if !strings.HasPrefix(name, "--") { name = strings.ToLower(name) }`), and say so in the comment.

### 56. [medium] idmerge — Go's %q is used to quote an HTML attribute value, giving attribute injection in the section wrapper

*`examples/gip/idmerge/main.go:343`*

`fmt.Fprintf(w, "<section data-source=%q>", in.Name)` uses Go string quoting where HTML attribute escaping is required. %q escapes a double quote as backslash-quote, which is not an escape in HTML: the backslash is a literal character and the quote closes the attribute. Demonstrated with a file literally named `a" onload="alert(1).html`: $ idmerge 'a" onload="alert(1).html' <section data-source="a\" onload=\"alert(1).html"><p id="a">x</p></section> An HTML parser reads that as data-source="...a\" plus an `onload` event-handler attribute - a working script injection. %q also mangles every non-ASCII filename into \u.... escapes. This is the one place in the program that assembles markup as a string, in a file whose own doc comment spends thirty lines on how concatenating markup goes wrong, and every sibling example in this directory (honeypot, hreflang) uses `lolhtml.EscapeAttribute` for exactly this. A reader copying the merge loop into a service where the part names come from a manifest, a URL, or an upload gets stored XSS.

### 57. [medium] ids — ids and references are compared as raw source, so duplicates spelled with character references are missed and valid fragment links are reported broken

*`examples/gip/ids/main.go:153`*

Every id and every reference is read with `e.Attribute(...)` and compared verbatim. The library is explicit that this is raw source with character references left encoded (Element.Attribute: "the href of <a href=\"?a=1&amp;b=2\"> is \"?a=1&amp;b=2\""), and the sibling example idmerge decodes for exactly this reason ("two ids are the same id when they are the same after decoding, and a document can spell one with a character reference"). ids does not, and it fails in both directions: $ printf '<h2 id="caf&eacute;">x</h2><a href="#caf\xc3\xa9">go</a><p id="a&#98;c">y</p><p id="abc">z</p>' | ids -:1:32: <a fragment link="café"> names no id in this document ids: 3 id attributes, 3 distinct; 1 references; 1 findings The fragment link resolves perfectly in a browser and is reported as broken (false positive, and the tool exits 1). The two ids that a parser reads as the same "abc" are counted as distinct and the duplicate - the one thing this program exists to find - is not reported (false negative).

### 58. [medium] imgcdn — decodeAmpersands treats "&amp" as a reference regardless of what follows, silently corrupting URLs it claims it would refuse

*`examples/gip/imgcdn/main.go:257`*

`decodeAmpersands` matches the prefix `&amp` (the semicolon-less form, line 58 of Ampersands) with no look at the next character. In an attribute value a semicolon-less named reference is not a reference at all when the character after it is "=" or ASCII alphanumeric - the rule the library sets out under Element.Attribute and implements in examples/gip/references. So a perfectly ordinary query parameter is silently rewritten: "/x.jpg?volts=1&ampere=5" -> "/x.jpg?volts=1&ere=5" ok=true "/x.jpg?amp=1&amp=2" -> "/x.jpg?amp=1&=2" ok=true That decoded string is then url.QueryEscape'd into the CDN's `url=` parameter, so the CDN is asked to fetch a URL the page never named and the image 404s. The same gap runs the other way for numeric references, which browsers do decode without the semicolon: "/x.jpg?a=1&#38b=2" is passed through literally rather than as "&b=2".

### 59. [medium] importmap — payload comment claims the library checks the injected <script> for a raw-text breakout; Element.Before is not checked

*`examples/gip/importmap/main.go:166`*

The comment on `payload` says of the JSON escaping: "The result is checked afterwards anyway: the library refuses a breakout, and an error here is much better than a page that silently renders the map." No such check happens. The payload is inserted with `e.Before(payload, lolhtml.HTML)` (line 150), and `Element.content` applies `checkRawText` only for `element_prepend`, `element_append` and `element_set_inner_content` (element.go:794-810); ErrRawTextBreakout's own doc says "[Element.Before], [Element.After] and [Element.Replace] write outside the element ... and are not affected." The claim is also self-contradictory: the payload always ends in ``</script>``, so if the check did apply to Before it would reject every map this program builds. No live vulnerability here - the `strings.NewReplacer` escaping of <, > and & is what actually makes it safe - but the comment teaches a guarantee the library does not offer.

### 60. [medium] landmarks — Candidate extent is taken from an unguarded end tag, so a <p> with an omitted end tag swallows its siblings and their roles are dropped

*`examples/gip/landmarks/main.go:247`*

`c.end` is set from `t.SourceLocation().End` inside OnEndTag with no check that the end tag is this element's own — the guard the library's OnEndTag documentation calls "the test is the name", and which keywords, islands and labels in this batch all apply in some form. `candidate()` accepts `p` (and `ul`) as candidate tags, and <p> is precisely the element whose end tag HTML omits: a following <div> closes it implicitly, so the p's handler runs against the ancestor's end tag and `c.end` is recorded as the end of that ancestor. The nesting check `c.at > outer.at && c.at < outer.end` then treats every later sibling as nested inside the p and deletes it. Reproduced: ``<body>``<p id="sidebar">`side`<div id="maincontent">`m`</div>``</body>`` yields ``<p id="sidebar" role="complementary">`` and no role at all on the div, reporting "1 nested candidates dropped" — the page loses its main landmark, which is the single most important one for the keyboard user this program exists to serve, and the report presents the loss as a deliberate decision.

### 61. [medium] linkreport — OnEndTag is registered without checking CanHaveContent, so a self-closing SVG anchor aborts the whole report

*`examples/gip/linkreport/main.go:111`*

Same defect as linktext: `OnElement("a", ...)` matches SVG anchors, and `<svg>`<a href="#x"/>`</svg>` is genuinely self-closing, so `e.OnEndTag` returns `element_add_end_tag_handler: No end tag.` and fails the rewrite. Measured on ``<p>``<a href="/a">`Read the report`</a>``</p>`<svg>`<a href="#x"/>`</svg>`: `reportString` returns that error, `run` returns it, and main prints `linkreport: …` and exits 1 with no report at all. Any page with an inline SVG containing a link — an icon, a chart, a clickable map — is unreportable, and the error message says nothing about SVG. A copier auditing a real site sees a native-library error and no findings. Fix: `if !e.CanHaveContent() { return nil }` before registering the end-tag handler (record the link, skip the text accumulation), as examples/gip/linkify and examples/gip/locate already do.

### 62. [medium] linktext — OnEndTag is registered without checking CanHaveContent, so one self-closing SVG anchor fails the whole rewrite after a truncated page has been written

*`examples/gip/linktext/main.go:166`*

`OnElement("a[href]", ...)` matches by tag name and so also matches ``<a>`` in SVG content, where ``<a href="#x"/>`` really is self-closing. `Element.OnEndTag` returns an error for an element that cannot have content, and that error fails the rewrite. lolhtml's own doc says so: "a handler on a selector that can match a void element must check this before calling OnEndTag". Measured on ``<p>``<a href="/a">`click here`</a>``</p>`<svg viewBox="0 0 10 10">`<a href="#x"/>`</svg>`<p>`tail`</p>``: `pass` returns `element_add_end_tag_handler: No end tag.` In `-flag` mode the destination is os.Stdout, so by then the prefix ``<p>`…`</p>`<svg viewBox="0 0 10 10">` has already been written — a caller redirecting to a file or piping to a client gets a truncated, unclosed document plus a nonzero exit, which is exactly the "a failed rewrite has already delivered a prefix" shape. In default report mode any page with an inline SVG icon link reports a confusing native error instead of a report.

### 63. [medium] linktext — fromHref title-cases the first *byte*, corrupting any non-ASCII URL slug it writes into the document

*`examples/gip/linktext/main.go:340`*

`words[0][:1]` slices one byte off a UTF-8 string. When the first character is multi-byte, `strings.ToUpper` sees an invalid byte, replaces it with U+FFFD, and the remaining continuation bytes are concatenated raw — the result is mojibake, not a capitalised word. Measured with this exact function: "/uber-uns" -> "Uber uns" (fine) "/über-uns" -> "�\xbcber uns" "/Écoute-nous" -> "�\x89coute nous" "/日本語のページ-abc" -> "�\x97\xa5本語のページ abc" This string is not merely printed: in `-fix` mode it is the replacement written into the page via `e.SetInnerContent(o.replacement, lolhtml.Text)`. So running `linktext -fix` over any site with non-ASCII URL slugs — which is most non-English sites, and the accessibility tooling in this example is aimed squarely at them — replaces "click here" with a link label containing a replacement character and broken bytes, permanently, in the output document. Fix: decode the first rune before upper-casing — `r, n := utf8.DecodeRuneInString(words[0]); words[0] = string(unicode.ToUpper(r)) + words[0][n:]`.

### 64. [medium] mentions — Name-guarded end tag on a depth counter leaves it permanently raised, silently disabling linking for the rest of the document

*`examples/gip/mentions/main.go:78`*

The `t.Name() != tag` guard is the library's documented idiom for *writing at an end tag's position* — it answers "is this position mine". Here it gates a depth counter that answers a different question, "has this element ended". The library's OnEndTag doc draws that distinction explicitly: "A handler that only wants to know that the element is over, rather than to write at its position, needs a finer distinction than that guard makes." When a noLink element is closed implicitly, the callback fires with an ancestor's tag name, the guard returns early, and depth is never decremented. Reproduced with <option>, which HTML almost always leaves unclosed: printf '<p>hi @alice</p><select><option>A<option>B</select><p>bye @bob</p>' | go run ./examples/gip/mentions <p>hi <a href="/u/alice">@alice</a></p><select><option>A<option>B</select><p>bye @bob</p> mentions: 1 mentions, 0 tags, 0 rejected @bob is not linked, and neither is anything after it in the document. Both <option> handlers fire at </select>, so depth is left at 2.

### 65. [medium] microdata — No implied-end-tag handling: implicitly closed itemprops merge their sibling's text and nest scopes that are not nested

*`examples/gip/microdata/main.go:161`*

Both stacks are popped from an OnEndTag callback with no accounting for when the element actually ended. The library's OnEndTag doc sets out three timings and warns that a callback firing at an ancestor's end tag can be *later* than where the element ended, "and the difference matters to anything accumulating"; it names examples/gip/markdown and examples/gip/depth as the ones that get it right by keeping an open-element stack and applying implied end tags. This program keeps a stack but never applies implied end tags, so the package comment's claim — "popped at the matching end tag, which works because those arrive in order" — is false for any element HTML lets close implicitly (p, li, dd/dt, td/th, tr, option).

### 66. [medium] modernise — Renames <xmp>/<listing> to <pre> by default, turning inert text into live markup — the exact rename its own comment lists as a failure

*`examples/gip/modernise/main.go:80`*

The file comment states the safety rule and then breaks it in the same paragraph. Its hazard table lists ``<xmp>`<b>x</b>`</xmp>` renamed to pre — the text becomes an element` as one of the measured ways SetTagName goes wrong, and eight lines later says "this program renames only within that set: ... listing and xmp to pre". The library's SetTagName doc makes the same point with an XSS example and says a rename across the raw-text boundary is only safe "when you know what the content is". <xmp> and <listing> are raw-text elements: their content is not markup and a parser will not build elements from it. Renaming the tag to <pre> does not touch the content bytes, but whoever parses the output now reads them as markup. Reproduced: `printf '`<xmp>``<img src=x onerror=alert(1)>``</xmp>`' | go run ./examples/gip/modernise` emits ``<pre>``<img src=x onerror=alert(1)>``</pre>`` and exits 0, with only a stderr line "its content was text and is now markup".

### 67. [medium] noopener — A graceful memory bail-out is turned into success, so an entirely un-hardened page exits 0

*`examples/gip/noopener/main.go:109`*

classify swallows ErrMemoryLimitExceeded to nil whenever -graceful is set, which is the default, so run() reports success and main exits 0. The library is explicit that this is the wrong default for this kind of rewrite: "For a rewrite that removes or neutralises something - a sanitiser, a token, an autoplay attribute, a tracking script - continuing to serve is serving the thing the rewrite existed to stop. There the truncated response is the safer failure" (options.go, MemorySettings.GracefulBailOut). The warning that is printed is also wrong about scope - it says "the tail of this document was passed through unhardened", but the same doc notes the bail-out is usually decided on the first write, so it can be the whole document. Measured with 200 target=_blank links and -limit 600: the complete un-hardened page reaches stdout, the report says hardened=0, and the exit status is 0. A copier who wires this into a proxy and checks the error serves opener-leaking links and is told nothing failed.

### 68. [medium] numbering — Package doc claims re-running is a no-op; it compounds, and -skip-numbered does not do what its help says

*`examples/gip/numbering/main.go:15`*

The package doc concludes "the accumulator sees 'Intro', not '1. Intro', so re-running this program is a no-op rather than a compounding one", and the -skip-numbered flag (default true) is described as "leave headings that already start with a number alone". Both are false. The label is inserted with Prepend at the start tag, before any text has been seen, so the already-numbered test at the end tag can only adjust counters. Measured: piping ``<h1>`Intro`</h1>`<h2>Sub</h2>` through the program twice yields ``<h1>`1. 1. Intro`</h1>`<h2>1.1. 1.1. Sub</h2>`. The code's own comment at line 166-174 admits this, so the package doc contradicts the implementation ten lines below it. Someone who copies this into a pipeline that re-renders pages (a cache refill, a re-publish) gets doubling labels. Fix: correct the package doc to say the rewrite is not idempotent, and make -skip-numbered real by deciding at the end tag - accumulate the heading's text and write the label with EndTag.Before/SetInnerContent, or buffer the heading - rather than describing a flag that only moves counters.

### 69. [medium] origins — cssImports indexes the original string with an offset from a progressively shortened one, so only the first @import in a stylesheet is ever reported

*`examples/gip/origins/main.go:381`*

`lower` is advanced each iteration (`lower = lower[i+len("@import"):]`) but `s` is not, and `rest` is taken as `s[i+len("@import"):]` with `i` being an index into the *shortened* `lower`. After the first match every subsequent slice of `s` starts at the wrong offset, lands on something that is not a quote, and hits the `continue`. Measured: ``<style>`@import "https://a.example/one.css"; @import "https://b.example/two.css"; @import "https://c.example/three.css";`</style>`` reports only `https://a.example`. For a program whose package doc opens "reports every origin a page would contact" and whose `-third-party` mode exits 1 as a build gate, silently missing third-party origins is the one failure mode that matters — a copier gets a green gate on a page loading stylesheets from hosts it never listed. Fix: track an absolute offset (e.g. `base += i + len("@import")` and slice `s[base:]`), or simply slice `s` in lockstep with `lower` so the two never drift.

### 70. [medium] pagenav — rel is classified by exact string equality against a selector that matches a token list, so a multi-token rel is rewritten as the wrong link

*`examples/gip/pagenav/main.go:188`*

`relSelector` uses `[rel~="next"]`, which matches any whitespace-separated token list containing `next`, but the handler decides with `if rel != "next" { kind = "prev" }` on the whole attribute value. Any `rel` with more than one token is therefore classified as prev. Measured: `<link rel="alternate next" href="/feed">` with `-current 2` comes out as `<link rel="alternate next" href="/p/1">` — an unrelated alternate link's href destroyed and pointed at the previous page — and because the handler then sets `done["prev"]=true`, the real `<link rel="prev">` is never inserted, so the page loses correct pagination metadata as well. The report says `inserted=1 rewrote=1` and exits 0. `rel="alternate next"` and `rel="next nofollow"` are ordinary in feed and pagination markup. Fix: classify on the token set, matching the selector — split the decoded rel on whitespace and look for `next`, then `prev`/`previous`, and leave a link alone (with a note) when it carries both or when the matched token is not the only pagination token.

### 71. [medium] preconnect — Hints are inserted at the head's *implied* end tag, so on a document without </head> they land after </body> or are dropped while the report claims success

*`examples/gip/preconnect/main.go:233`*

The head handler registers OnEndTag with no check that the end tag is actually the head's. The library documents that an omitted end tag makes the handler run at whatever closed the element (an ancestor's tag), or not at all. Measured: `<html>`<head>``<title>`t`</title>``<body>``<img src="https://cdn.example/a.png">``<p>`x`</p>``</body>`</html>` emits the two <link> hints immediately before </html>, i.e. after </body> at the very end of the document — the exact opposite of the program's stated design ("the origins are in the body and the hints belong in the head"), and useless as a hint since it arrives after every resource it is meant to warm up. Worse, ``<head>``<title>`t`</title>``<p>``<img src="https://cdn.example/a.png">``</p>`` (head never closed at all) emits nothing while stderr reports `links=1` and the note "no head and no body to put the hints in" — a head and a body were both present, and the count is a lie because markup() increments h.added before anything is placed. A reader copying this pattern ships a rewrite that silently no-ops on minified pages.

### 72. [medium] printstyles — The print stylesheet is inserted at the head's implied end tag, so it lands outside the head or is silently dropped

*`examples/gip/printstyles/main.go:130`*

Same defect as preconnect: OnEndTag on <head> with no name guard. Measured: `<html>`<head>``<title>`t`</title>``<body>`...`</body>`</html>` puts `<link rel="stylesheet" href="/print.css" media="print">` after </body>, not in the head as the package doc promises; ``<head>``<title>`t`</title>``<p>`...` (no </head>, no </html>) links nothing at all and reports `linked=0` plus the false note "no head and no body to link the stylesheet from" when a head was present. The sawHead flag then also suppresses the body fallback that exists precisely for this case, so the fallback can never fire on the documents that need it. Someone copying this loses the print stylesheet on any page whose optional </head> was omitted, with a misleading diagnostic pointing them at the wrong cause. Fix: guard the callback with `if end.Name() != "head" { return nil }` and only treat the head as handled once that guard has actually placed the link, so the body/document-end fallback still runs otherwise.

### 73. [medium] rebase — CSS URLs seen before the <base> are neither resolved nor counted as Early, so the program removes the base and reports success while leaving broken URLs

*`examples/gip/rebase/main.go:260`*

The package doc promises the program "counts each URL that went by before the base arrived and exit[s] non-zero" so a caller knows to run the two-pass version. urls() does that with pendingU, but styleAttribute() simply returns when r.base == nil and styleText() replaces the sheet unchanged, and neither records anything — nothing is added to res.Early. Measured: ``<style>`a{background:url(x.png)}`</style>``<p style="background:url(y.png)">`t`</p>``<base href="/assets/">``<img src="b.png">`` outputs both CSS URLs untouched, deletes the <base>, prints "resolved 1 urls, 0 styles, 0 targets; 0 urls went past first" and exits 0. Because the base element has been removed, url(x.png) and url(y.png) now resolve against the page URL instead of the base — the rewrite has silently broken them and told the caller everything was fine. A copier who trusts the exit code to gate deployment ships broken backgrounds.

### 74. [medium] redact — A duplicated attribute whose first copy is clean is rebuilt from the later copy, silently changing the value a browser uses

*`examples/gip/redact/main.go:166`*

The loop walks every copy from AttributeList and `continue`s past a copy with no match without recording the name, so the first copy that does match decides the rebuilt value. When the clean copy comes first, RemoveAttribute takes every copy and SetAttribute then writes the *later* copy's scrubbed value in its place. Reproduced: `<a href="/safe" href="mailto:bob@example.com">x`</a>`` becomes ``<a href="mailto:[email removed]">`x`</a>`` - the link target a browser would have followed changed from /safe to a removed mailto, and the report says only "1 attributes moved because they were duplicated". This is precisely the hazard the library's package documentation names under "An attribute can appear twice": "reading a value, deciding from it, and writing it back is consistent, because all three use the first. Reading through the iterator and writing back is not." The existing test only covers two identical dirty copies, so the mixed case is unguarded.

### 75. [medium] references — Decoding attributes through AttributeList writes every copy's value onto the first copy

*`examples/gip/references/main.go:132`*

element() iterates AttributeList, which yields every copy of a repeated attribute, and writes back with SetAttribute, which the library documents as replacing the first copy only. When a later copy is the one that changes, its decoded value lands on the first copy - the one a browser actually uses. Reproduced with -attributes: ``<a href="/x" href="/y&copy;z">`t`</a>`` becomes ``<a href="/y©z" href="/y&copy;z">`t`</a>``, so the effective href changed from /x to /y©z. The program's stated job is to decode references without changing what a value means, and here it changes the value outright - this is the exact pattern the library's "An attribute can appear twice" section warns against ("Reading through the iterator and writing back is not [consistent]"). Anyone copying this loop into a normaliser or a URL rewriter inherits a silent value swap on any element with a repeated attribute. Fix: skip names already seen (`if seen[a.Name] { continue }; seen[a.Name] = true`) so only the first copy is read and written, or read the deciding value with e.Attribute(name), which returns the first copy.

### 76. [medium] references — keep() assumes no multi-code-point reference contains markup, so &nvlt; is decoded to a bare "<" and written back as HTML

*`examples/gip/references/main.go:290`*

keep() decodes the first rune and, if the reference stands for more than one code point, returns false with the comment "more than one character, and none of those are markup". That is false: the HTML named reference set contains `&nvlt;` = U+003C U+20D2 and `&nvgt;` = U+003E U+20D2, both of which Go's html.UnescapeString returns. Because the text handler writes with lolhtml.HTML (correctly, so the references it removed are not put back), the decoded "<" reaches the output unescaped. Reproduced: ``<p>`a &nvlt; b`</p>`` produces the bytes ``<p>`a < \342\203\222 b`</p>`` - a literal 0x3C in character data - and ``<title>`a &nvlt;/title&gt; b`</title>`` produces ``<title>`a <⃒/title> b`</title>``, an unescaped "<" written into a raw-text element (TextChunk.Replace is one of the insertion paths the library documents as unchecked for raw-text breakout). It contradicts the program's own headline rule - "the ones that do carry meaning are few: & and < in text" and "This program decodes the rest and leaves those alone".

### 77. [medium] sandbox — "No host means same origin" silently exempts data:, blob: and javascript: iframes, and url.Parse failures fail open

*`examples/gip/sandbox/main.go:124`*

hostOf returns "" both for a URL url.Parse rejects and for any URL with no authority component, and the guard on line 124 treats "" as "leave it alone", filing it under the report line "same origin, srcdoc or no src". Reproduced: `<iframe src="data:text/html,`<script>`alert(1)`</script>`">`, ``<iframe src="javascript:alert(2)">``, ``<iframe src="blob:https://evil.example/abc">`` and ``<iframe src="http://[::1">`` all pass through with no sandbox and no referrerpolicy, counted as 4 "same origin, srcdoc or no src", while only the https:// frame is hardened. Skipping a relative src is right; a data: or blob: frame is untrusted embedded content that is exactly what the sandbox attribute exists for, and a src Go cannot parse is one a browser may still load - so the parse error is a fail-open in a hardening tool, and the report actively mislabels the reason. The doc comment enumerates the program's judgements (allow-scripts with allow-same-origin, author-written sandboxes left alone) and says nothing about schemes, so a copier has no signal that these frames are being skipped.

### 78. [medium] selectorcoverage — The selector probe discards its Writer without closing it, one leaked rewriter per selector

*`examples/gip/selectorcoverage/main.go:259`*

unsupported() builds a one-selector Writer to ask the library whether a selector is usable, assigns it to the blank identifier and never closes it on the success path. The library is explicit that every Writer must be Closed, including one being abandoned, and that the drop cleanup is "a backstop rather than a second way of doing this". Because unsupported() runs once per selector, a stylesheet with thousands of rules creates thousands of live lol-html rewriters and their selectors, held until the GC happens to run the cleanup — in a program whose own doc comment tells the reader that stylesheets with thousands of rules are the expected input. The identical probe in examples/gip/selectorcheck closes it and says why: "a validator that leaked a handle per selector would be a poor example of anything". Fix: bind the writer and close it — `w, err := lolhtml.NewWriter(...); if err == nil { w.Close(); return "" }`.

### 79. [medium] shadow — No end-tag name guard: a shadow root is inserted outside its host and still counted as given

*`examples/gip/shadow/main.go:202`*

The end-tag handler uses the EndTag position unconditionally. The library documents that an element closed implicitly hands its handler an enclosing element's tag ("The test is the name"), and the sibling examples shrink and slots both apply that guard; this one does not. Confirmed by running the program on `<my-card><my-badge>x</my-card>` with templates for both tags: the output was `<my-card><my-badge>x`<template shadowrootmode="open">`BADGE`</template>``<template shadowrootmode="open">`CARD`</template>`</my-card>`. Both templates parse as children of my-badge, so my-badge acquires a second declarative shadow root (a parse error a browser drops) and my-card acquires none — yet the report said "2 hosts, 0 already had a shadow root, 2 given one". That contradicts the package doc's promise that the report "counts those hosts rather than pretending they were done", and someone copying it ships pages where the shadow root is attached to the wrong custom element.

### 80. [medium] slots — The end-tag repair guard compares against the fill name instead of the tag name, so a colliding name loses an end tag

*`examples/gip/slots/main.go:313`*

The library's rule for an end-tag handler is to compare EndTag.Name() with the element's own tag name; here the element is always a <template>, but the guard also accepts c.name, which is the value of data-fill — a fill name, not a tag name. When a fill name happens to equal the tag of the enclosing element that actually closed the template, the repair is skipped and that element's end tag is swallowed. Confirmed by running the program: ``<div>``<template data-fill="div">`a`</div>`tail...` produced ``<div>`tail<slot name="div">a</slot>` with the ``</div>`` gone, while the identical document with data-fill="x" correctly produced ``<div>``</div>`tail...`. The output is unbalanced markup, from the exact hazard the package doc says this program guards against in both places it unwraps something. Fix: drop the `t.Name() != c.name` term — the condition should be `if t.Name() != "template"`, matching the shape used in unwrap() and collect(), which compare against the element's own tag name.

### 81. [medium] slugs — A heading whose end tag never arrives shifts the plan, giving one heading another heading's anchor

*`examples/gip/slugs/main.go:202`*

The first pass appends to s.seen only from the end-tag handler, but the second pass indexes s.planned by the heading's ordinal (h.ord-1). A heading that is never closed contributes no entry, so every later heading's id slides up one slot. Confirmed by running the program: ``<h1>`Alpha`<div>`<h2>Beta</h2>`</div>`` produced ``<h1 id="beta">`Alpha`<div>`<h2>Beta</h2>`</div>`` — the h1 was given the h2's slug and the h2 was given no id at all, while the report claimed headings=1 assigned=1. With -map this is worse than a cosmetic slip: the mapping is written keyed by the h2's ordinal while the document carries that anchor on the h1, so the two disagree permanently and the tool's entire premise — a stable heading-to-id mapping — is broken by one unclosed tag in a real page. Fix: index the plan by the heading's ordinal rather than by arrival order — record into s.planned at position h.ord-1 (growing the slice as needed), or set a placeholder in the element handler and fill it in at the end tag — so a heading with no end-tag callback leaves a hole rather than shifting its successors.

### 82. [medium] split — The open-tag stack is lol-html's token nesting, so a document with omitted end tags accumulates stale ancestors and parts come out unbalanced

*`examples/gip/split/main.go:214`*

s.open is pushed on every start tag and popped from an OnEndTag handler, and the header presents that as complete ("Nothing else is needed - no tree, and no second pass"). But lol-html's end-tag callback is a fact about the token stream, not the tree: HTML lets a document omit </p>, </li>, </td> and friends, and the library documents that the callback then arrives only when an ancestor's explicit end tag closes the element ("in <ul><li><em>a<li>b</ul> the em's callback arrives after 'b' has been reported"), or never. So every unclosed element stays on s.open and is reopened in every subsequent part. Verified with the built binary on <article><p>intro text<h2>One</h2><p>body one<h2>Two</h2><p>body two</article>: part 2 is reopened as <article><p> and comes out as <article><p><h2>One</h2><p>body one</p></p></article>, and part 3 is reopened as <article><p><p> and is left with unbalanced open tags.

### 83. [medium] srcset — The src is percent-encoded without being decoded first, so any src containing a character reference yields a srcset of broken URLs

*`examples/gip/srcset/main.go:144`*

Element.Attribute returns raw source text with character references left encoded - the library says so explicitly ("the href of <a href=\"?a=1&amp;b=2\"> is \"?a=1&amp;b=2\", not \"?a=1&b=2\"") and points at examples/gip/references for the decoder. rendered() feeds that raw value straight to url.QueryEscape, so the ampersand entity is encoded as data rather than decoded first. Verified: <img src="/img.php?id=1&amp;size=2"> produces srcset="/cdn?u=%2Fimg.php%3Fid%3D1%26amp%3Bsize%3D2&w=320 320w, ...", i.e. the CDN is asked for the URL /img.php?id=1&amp;size=2, which is not the resource the browser would have fetched from src. Writing & as &amp; is the correct way to spell a query URL in HTML, so this is not an exotic input. The concrete consequence for someone copying this into an image pipeline: on every such image the src stays right and the srcset is wrong, and a browser that supports srcset ignores src entirely - so the images silently 404 or return the wrong asset, on exactly the pages that use query-parameter image URLs.

### 84. [medium] sri — The embedded manifest is written at </head> and silently omits every subresource below it

*`examples/gip/sri/main.go:196`*

The -embed block is produced by a StreamFunc registered on the </head> end tag, so it runs while </head> is being serialised - long before the rewriter has parsed the body. a.used is still only whatever was found in the head at that moment, and the library documents exactly this trap for StreamFunc: "It cannot see anything the rewriter has not parsed yet ... the failure is silent - you get the empty result your closure computed, not an error." Verified by running the built binary on <html><head><link rel=stylesheet href=/css/site.css></head><body><p>hi</p><script src=/js/app.js></script></body></html> with both entries in the manifest: the report says "integrity added=2" while the embedded block contains only {"/css/site.css":...}. The consequence for a copier: the package doc promises "The manifest that was actually used is embedded in the output", and a security tool ships a page whose own record of what it protected is missing the scripts at the end of <body> - the single commonest placement for <script src>.

### 85. [medium] streamvsmemory — The "memory floor" is a power-of-two upper bound, reports 0 when no limit works, and the sample output in the package doc cannot be produced by this program

*`examples/gip/streamvsmemory/main.go:176`*

floor() searches limit = 8, 16, 32 ... 1<<24, so it can only ever report a power of two - yet the package doc's worked example advertises "memory floor 5 in memory, 76 streamed", numbers lifted from the library's own MemorySettings measurements that this program cannot output (running it prints 1024 for both on a small page). The same sample omits the "text nodes" and "doctype handlers" lines the program always prints. Worse, when no limit up to 16 MB completes, floor() returns 0 and the report renders that as "memory floor 0", which reads as "needs nothing" rather than "not found", and if both passes fail the line disappears entirely. Someone copying this to size a MaxMemory budget gets a value up to 2x larger than the real floor presented as "the smallest memory limit each shape needs" (the -floor flag's own words), or a 0 they will read as a measurement.

### 86. [medium] summary — skipDepth is never decremented when a skipped element's end tag is omitted, so extraction dies at the first <option>

*`examples/gip/summary/main.go:165`*

The skip counter is incremented for every element in `boilerplate`/`skipped`, but the end-tag handler only decrements when `t.Name() == tag`. The library documents that an element whose end tag the source omits is closed by an enclosing element's end tag and the handler is handed *that* token (see EndTag.Name and Element.OnEndTag in endtag.go). `option` is in the `skipped` set and its end tag is optional in HTML, so ``<select>``<option>`One`<option>`Two`</select>`` fires each option's end-tag handler with Name()=="select", the guard returns early, and skipDepth stays permanently above zero. Every text chunk for the rest of the document is then dropped. Verified by running Extract on `<header>`<select>``<option>`One`<option>`Two`</select>`</header>`<p>`The real first paragraph.`</p>``: it returns ErrNoSummary, while the same document with ``</option>`` written out returns "The real first paragraph." A copier gets a content extractor that silently reports 'no summary' on any page with an unclosed <option>, <li>-style optional tag added to the set later, or mis-nested boilerplate.

### 87. [medium] tablelayout — The open-paragraph counter ignores implied </p>, so an unclosed <p> refuses every later conversion

*`examples/gip/tablelayout/main.go:142`*

`paragraphs` is incremented on every <p> and decremented in its OnEndTag handler. HTML lets </p> be omitted, and a <div> start tag closes an open paragraph — but lol-html has no tree, so the <p>'s end-tag handler does not run until an *ancestor's* end tag arrives (or never, if the document ends first). Verified: `<!doctype html>`<body>``<p>`Intro`<div class="row">``<div class="col-6">`A`</div>``</div>``</body>`` comes out completely unconverted, with the report claiming `refused 2 rows inside a `<p>`` and main() exiting 1 — even though a real parser has already closed that paragraph, so the doctype ambiguity the refusal exists for does not apply. Adding ``</p>`` converts the same document fine. Someone copying this into a mail-template pipeline gets templates that silently pass through unconverted (and a failing exit status) on perfectly ordinary markup with omitted </p>.

### 88. [medium] tailcomment — The Writer is abandoned without Close on the io.Copy error path

*`examples/gip/tailcomment/main.go:108`*

On any failure from io.Copy — a read error, a handler error, or a destination error — Run returns immediately and never calls rw.Close(). NewWriter's documentation is explicit that every Writer must be closed, including one being abandoned: the runtime cleanup is a leak backstop, not a second way of doing it, and until it runs the rewriter, its selectors and its cgo handles stay allocated. In a long-lived service converting templates per request this is a native-memory leak on every failed request, and it is exactly the line a copier lifts. Every other example in this batch (summary, tableaudit, tablecsv, tablejson, tablelayout, strictmode) calls Close on its error path; this one does not. Fix: `if _, err := io.Copy(rw, r); err != nil { rw.Close(); return summary, err }` — the returned err is the one to report, and a second Close later is harmless.

### 89. [medium] tailreport — The Writer is abandoned without Close on both the write-error and read-error paths

*`examples/gip/tailreport/main.go:81`*

The copy loop returns on a failed rw.Write (line 81) and on a non-EOF read error (line 88) without ever calling rw.Close(). Same consequence as in tailcomment: the rewriter, selectors and cgo handles are held until the runtime cleanup happens to run, which the library documents as a backstop rather than the supported path — and a handler that ever captured the Writer would disable that backstop entirely, turning this into a permanent leak. The read-error path is the worse of the two, because there the Writer is still perfectly healthy and simply dropped. Fix: close on both paths (`rw.Close()` before returning the error), keeping the returned error as the reported one.

### 90. [medium] tailreport — Inline comment says a failed rewrite discards the output, contradicting this file's own package doc, its test, and the library

*`examples/gip/tailreport/main.go:92`*

The comment above the Close check states that an error there means "the output was discarded, and a report appended to nothing". That is the misconception the package doc at the top of this very file exists to correct — it says in as many words "and not for the reason I first wrote down. The output is not discarded... everything already emitted is in the sink... the documented early-stop prefix" — and main_test.go asserts the prefix (`before`<a href="/">`l`</a>`after` leaves "before" in the sink). Writer.Write's documentation says the same: failing is not atomic, so a caller refusing a document has already delivered a short version of it. A reader who takes this comment at face value will believe an error rolls the output back and will not buffer-and-forward when they need to actually refuse a document, shipping a truncated page to a client. Fix: restate the reason as the package doc does — Close must be checked first because a failure means the sink already holds a truncated document, which is not something to append a report to.

### 91. [medium] transitions — Top-level siblings are never numbered, so they collide on one path and get the same view-transition-name

*`examples/gip/transitions/main.go:133`*

The :nth disambiguator is only computed when there is a parent frame on the stack (`if len(open) > 0`). Elements at the top level of the input -- which this program explicitly supports, the package doc says "a fragment beginning with <body> has a path starting at 'body'" -- always get nth = 1, so two sibling roots of the same shape produce the identical path, and Scan then silently drops the second (`if _, seen := doc.Elements[...]; !seen`). Apply recomputes the same colliding path and stamps the same name on both.

### 92. [medium] units — The skip-region depth counter is incremented before the CanHaveContent early return, so it never comes back down

*`examples/gip/units/main.go:184`*

`element` does `c.depth++` and only then returns early when `!e.CanHaveContent()`, skipping the `OnEndTag` registration that is the sole decrement. Any self-closing foreign element whose name is in `Skip` or is raw text - `<svg>`<title/>``, `<svg>`<style/>``, `<svg>`<script/>`` - leaves `depth` permanently above zero, and `text` (units/main.go:196) returns immediately whenever `c.depth > 0`. Confirmed: `printf '<svg>`<title/>`</svg>`<p>`12 miles`</p>`' | units` converts nothing and reports `0 conversions ... in 0 text nodes`, while the same document without the `<svg>` converts normally. Consequence for a copier: one inline SVG near the top of a page silently disables the rewrite for the entire rest of the document, with a success exit code and a report that says zero rather than saying it gave up. typography/main.go:122 gets this right (`if !verbatim[tag] || !e.CanHaveContent() { return nil }` before the increment). Fix: move the `c.depth++` below the CanHaveContent check, or only increment when the end-tag handler is actually registered.

### 93. [medium] untrack — Document-controlled query-parameter names are appended as ContentType HTML, so a URL can break out of the report comment

*`examples/gip/untrack/main.go:169`*

`OnDocumentEnd` appends `"\n<!-- untrack: "+s.oneLine()+" -->\n"` with ContentType HTML. `oneLine` interpolates the keys of `s.strippedParams`, and those keys are raw bytes taken straight out of a document attribute (`name = pair[:i]` from the URL's query string, stored un-lowercased at untrack/main.go:205). Nothing escapes them, and a comment ends at the first `-->`. With the default exact-match parameter list the names are constrained, but `match` (untrack/main.go:216) advertises a trailing `*` wildcard, and the `-params` flag is the documented way to supply one. Confirmed: `printf '`<a href="/x?utm_evil-->``<script>`alert(1)`</script>`<!--=1&b=2">l`</a>`' | untrack -params 'utm_*'` emits `<!-- untrack: urls=1 changed=1 pixels=0 [utm_evil-->`<script>`alert(1)`</script>`<!--=1] -->`, i.e. a live script tag in the output. The report comment is on by default in main (`-quiet` defaults to false).

### 94. [medium] upgrade — A <style> the source never closes has its whole body silently deleted

*`examples/gip/upgrade/main.go:165`*

The text handler removes every chunk of a ``<style>`` body and relies on the end-tag handler to re-emit it. An end-tag handler for an element nothing closes never runs at all (rewriter.go, Close: "An end-tag handler for an element nothing closes never runs at all"), so on a truncated document the removal stands and nothing replaces it. Confirmed: `printf '`<img src="http://a.example/x.png">``<style>`body{color:red}' | upgrade` outputs ``<img src="https://a.example/x.png">``<style>`` - the stylesheet is gone - and exits 0 reporting success. The code comment two lines above shows the authors already hit the mirror image of this bug ("returning early here would delete the stylesheet") but only for the case where the handler does run. Consequence for a copier: a remove-and-re-emit text pipeline silently drops content whenever the document is truncated mid-element (a client that disconnected, a partial upstream response), with no error to notice it by.

### 95. [medium] viewport — Viewport content is split on commas only, so a semicolon- or space-separated user-scalable=no survives and is reported as harmless

*`examples/gip/viewport/main.go:83`*

`parseContent` splits the content attribute on `,` alone. Browsers treat `,`, `;` and ASCII whitespace as directive separators in a viewport declaration (Blink's viewport parser has all three in its separator predicate), and `width=device-width; user-scalable=no` is a common spelling in the wild. As written, that whole string parses as one directive with key `width`, `blocksZoom` never fires, and the program takes the third of its three documented branches. Confirmed: `printf '<meta name="viewport" content="width=device-width; user-scalable=no">' | viewport` leaves the tag untouched and prints `note: the page has its own viewport and it does not block zooming (1)`; the same content with a space instead of a semicolon behaves identically; with a comma it is correctly repaired. Consequence for a copier: an accessibility remediation tool that silently misses the failure it exists to fix, and actively certifies the page as fine - worse than reporting nothing. Fix: split on any of `,`, `;`, and whitespace (e.g.

### 96. [medium] weight — Report.LargestCandidates is only the srcset maxima, not what either doc comment says it is

*`examples/gip/weight/main.go:178`*

The field doc says LargestCandidates is "the total of Images plus, for each srcset, only its heaviest candidate ... the weight of the page on the device that picks the worst option", and the package doc (weight/main.go:18) names it `Images.LargestCandidates`, a field that does not exist on `Kind` at all. What the code assigns is `largestSum`, the bare sum of the heaviest known candidate per srcset - it excludes every ``<img src>`` with no srcset, every `<link rel=stylesheet>`/script weight, and inline weight. Confirmed against a manifest {/plain.png:7, /a.png:100, /b.png:300, /c.png:50} on ``<img src="/plain.png">``<img srcset="/a.png 1x, /b.png 2x, /c.png 3x">``: Images.Known is 457, the documented worst-case page weight is 307 (7 + 300), and the field holds 300. Consequence for a copier: a budget check built on this number under-reports the page by the whole non-responsive image set, and silently - the figure looks plausible because it is the right order of magnitude.

### 97. [low] bindings — Comment says the literal parser accepts a double-quoted string; it accepts single quotes and backticks

*`examples/gip/bindings/main.go:191`*

The comment above the quoted-string branch reads "A quoted string, single or double", but the test is `expr[0] == '\'' || expr[0] == '`'` — an apostrophe or a backtick. A double-quoted expression is never recognised as a literal. In practice that is a conservative miss rather than a wrong rewrite: `:title='"Home"'` (a single-quoted HTML attribute holding a double-quoted template string) is reported under "the value is an expression, and nothing here evaluates one", which is not why it was skipped. Someone copying this to widen the accepted syntax will read the comment, believe double quotes are already handled, and not add them. Fix: make the comment say what the code does — "A quoted string: single quotes or a template literal's backticks. A double-quoted one cannot appear inside a double-quoted attribute unescaped, so it is not accepted" — or accept '"' as a third delimiter and add it to the ContainsAny guard.

### 98. [low] clientgone — CloseWrites abandons its Writer on the Write error path

*`examples/gip/clientgone/main.go:219`*

`CloseWrites` returns the Write error without calling `w.Close()`, so on that path the rewriter, its selectors and its handle table are left to the cleanup backstop rather than released. Every other path in this file is correct (`Rewrite` stores `w.Close()` into `r.CloseErr`), which makes the omission read as an accident rather than a simplification. It matters more here than in most examples because this is the file people will read specifically to learn how a rewrite behaves when the destination fails, so its error paths are the part being copied; and the guarded resource is exactly the one the package doc says a handler capturing the Writer would make unreclaimable. Fix: `defer w.Close()` immediately after the NewWriter error check — the later explicit `closeErr = w.Close()` still reports the flush error, since a second Close returns nil, which is the shape the library's own Rewrite helper uses.

### 99. [low] clientgone — "accepted N of M bytes" compares output bytes accepted against input bytes offered to the rewriter

*`examples/gip/clientgone/main.go:178`*

`r.Accepted` is `dst.Written` — rewritten *output* bytes the destination took — while `r.Offered` is the sum of the *input* chunk lengths handed to `w.Write`. The report prints them as two halves of one quantity: "the destination accepted 99 of 760 bytes and then failed", which reads as though 760 bytes were on their way to the destination. They were not: the rewrite adds `rel="nofollow"` to each of the 20 anchors, so the full output for the default 760-byte page is about 1080 bytes, and in the failing run the destination was never offered anything close to either number. The same sentence is reproduced as sample output in the package doc comment. This is a program whose entire subject is exact accounting under partial delivery — "they say what the rewrite had reached rather than what the page contains. Those are different numbers" — so a reader is entitled to trust that these two figures are commensurable, and will carry the same conflation into their own instrumentation. Fix: report the two separately, e.g.

### 100. [low] cspnonce — The -mark comment describes content the code does not insert and calls Element.After "element content", which is the position distinction the raw-text rule turns on

*`examples/gip/cspnonce/main.go:198`*

The comment says the marker "echoes an attribute value taken from the document, so it is untrusted" and is "Inserted as element content, which is the context Text escaping is defined for". Neither is true of the code: the format string interpolates only the integer `found`, no document value at all; and Element.After writes *outside* the element, not into its content. That second half is the misleading one in teaching code: the library draws a sharp line between Prepend/Append/SetInnerContent/EndTag.Before, which are inside the element and are checked against ErrRawTextBreakout, and Before/After/Replace, which write outside it and are explicitly not checked (rawtext.go, "What is checked is the position, not the type"). A reader who takes this comment at face value will conclude After is an inner-content insertion and therefore guarded, and will later use it with HTML content believing the breakout check applies.

### 101. [low] darkmode — validColour accepts any all-alphabetic string as a "named colour", contradicting the package doc's promise that an unparseable colour is refused rather than emitted

*`examples/gip/darkmode/main.go:88`*

The package doc states: "The colours are checked before they go anywhere. A theme-color that a browser cannot parse is ignored, which looks identical to the meta being absent, so an unparseable one is refused rather than emitted." validColour's named-colour branch accepts any run of ASCII letters, with no list. Verified: `printf '<head></head>' | darkmode -dark 'zzzz'` emits `<meta name="theme-color" content="zzzz" media="(prefers-color-scheme: dark)">` and exits 0. Consequence for a copier: the failure mode the doc says is being prevented — a meta a browser silently ignores, indistinguishable from no meta — is exactly what a typo'd colour name produces, and the tool reports success. There is no injection risk (the value goes through EscapeAttribute), so this is a correctness/claim mismatch only. Fix: check the string against the CSS named-colour list (plus transparent/currentcolor), or soften the package doc to say only hex values are actually validated.

### 102. [low] deferscripts — hostOf invents a host for relative URLs that contain "//", contradicting the comment that says a relative src has no host

*`examples/gip/deferscripts/main.go:151`*

The comment promises "a relative src has no host and is first-party by definition", but the implementation just looks for the first "//" anywhere in the string, so any relative URL containing a doubled slash — in the path or in a query — yields a bogus host. Measured: `/assets//a.js` -> "a.js"; `a.js?v=//evil.com` -> "evil.com". It also does not lower-case its result before returning, so the caller's suffix match is the only thing saving `https://CDN.Example.COM/a.js` (hostIsSkipped does lower-case, so that one is fine). Concrete consequence: -skip-host matches or misses silently in both directions. `-skip-host js` skips `/assets//a.js` because the invented host "a.js" ends in ".js"; a first-party script under a doubled-slash path is attributed to a third party. The effect is confined to which scripts get deferred, so it is a correctness wart rather than a safety hole — but a copier reusing hostOf for anything that decides trust (a CSP allowlist, a third-party inventory) inherits a URL parser that answers confidently and wrongly.

### 103. [low] dimensions — The package doc says "every image and iframe" but the selector also rewrites video, embed and object

*`examples/gip/dimensions/main.go:1`*

The first line of the package doc, the program's contract, names two element types. The selector matches five. With -fix that is not just a reporting difference: `<video>`, `<embed>` and `<object>` get an aspect-ratio style attribute written into them, which a reader of the doc did not agree to. Measured: `dimensions -fix -ratio 16:9` turns `<object data="o"></object>` into `<object data="o" style="aspect-ratio:16/9">`. The report compounds it by identifying every finding through the src attribute, which <object> does not have (it uses data) and which <video> often omits in favour of child <source> elements. The same run prints `65-82 <object src="">: no width or height` — a finding whose only human-readable identifier is empty. The byte range still locates it, so this is a legibility problem rather than a wrong answer. Concrete consequence: someone copying this to gate a build on layout shift under-scopes what they think it touches, and is surprised when -fix edits embeds.

### 104. [low] dupsection — OnEndTag is registered for every content element, so memory grows with the document the example claims not to hold

*`examples/gip/dupsection/main.go:103`*

The bookkeeping handler on `OnElement("*")` calls e.OnEndTag for every element that can have content, purely to advance lastEnd. OnEndTag's doc measures this at about 240 bytes per element held "until the rewrite ends, not until the end tag arrives", and states that MemorySettings.MaxMemory does not bound it because it is the binding's handle table rather than lol-html's parsing buffer — "register this only where an element actually needs it - a narrow selector, or a condition checked before registering". The package doc of this example is built on the opposite claim: "without ever holding the whole document", and the measured table ("documents of 5 KB, 41 KB and 801 KB retained 1072, 1504 and 1408 bytes at peak ... not with the document") reports only the `retained` buffer, so a reader is actively told memory is flat while the handle table is O(elements). Someone copying this for a streaming proxy over untrusted input gets an unbounded allocation the documented MaxMemory knob cannot cap.

### 105. [low] email — The javascript:-URL removal is scoped to three selectors, so form/iframe/input URL attributes are never checked

*`examples/gip/email/main.go:419`*

The URL handler matches only `a[href], img[src], link[href]` and inspects only the names "href" and "src". The package doc presents the removal without that scope: "And URLs whose scheme is javascript:, which are neither a link nor a script but get treated as one by something eventually." Reproduced against this file: `<form action="javascript:alert(4)">` and `<iframe src="javascript:alert(5)">` pass through untouched with report.JavascriptURLs == 0. Also unchecked: input/button `formaction`, `object data`, `embed src`, `svg a xlink:href`, `base href`, and `meta http-equiv=refresh content`. A copier reading the report line "removed N javascript: URLs" believes the document is clean of them when it is not. Fix: either match `*` and test every URL-bearing attribute name (href, src, action, formaction, data, poster, xlink:href, and the URL inside a meta refresh), or narrow the package doc to say exactly which three selectors are covered — the allow-list shape that emailstrip uses is the more defensible of the two.

### 106. [low] formschema — The orphan note asserts every orphan carries a form attribute, which is not what the code collects

*`examples/gip/formschema/main.go:343`*

`record` sends any field outside a form to `Orphans`, whether or not it has a `form` attribute, but the note printed for them says they "carry a form attribute and are not inside a form". Reproduced: `<input name=x value=1><form id=a action=/a></form>` emits an orphan with no `form` key alongside that note. The two cases are different for a replay client — a field naming a form is submitted with that form, a field naming nothing is submitted with none — and the note tells the reader they are the first when they may be the second. Fix: partition on `f.FormAttr != ""` and emit two notes, or drop fields with no form attribute (they submit nothing) rather than reporting them as orphans.

### 107. [low] glossary — The text handler inserts markup into raw-text elements its hand-written exclusion list does not know about

*`examples/gip/glossary/main.go:59`*

`OnDocumentText` replaces whole text nodes with `lolhtml.HTML`, which the library explicitly does not raw-text check (see the ErrRawTextBreakout and CheckRawText docs: TextChunk.Before/After/Replace are one of the two measured gaps, and "a text handler has to guard itself... IsRawText answers for a tag it does not know in advance"). The guard here is the hardcoded `noLink` map, which covers only 4 of the 10 raw-text elements — script, style, textarea, title — and misses iframe, noembed, noframes, noscript, xmp and plaintext. Reproduced: `<xmp>a widget here</xmp><noscript>widget again</noscript>` comes out as `<xmp>a <a href="#term-widget" class="glossary">widget</a> here</xmp><noscript><a ...>widget</a> again</noscript>` — inside `<xmp>` that anchor is rendered to the reader as literal source text, corrupting the page. There is no breakout (raw-text content cannot itself contain the closing sequence), so this is corruption rather than injection, but it teaches a hand-copied partial list where the library ships the complete predicate.

### 108. [low] highlight — the hand-copied raw-text skip list omits plaintext, so content there is escaped and corrupted — the exact failure the package doc says is avoided

*`examples/gip/highlight/main.go:43`*

The package comment states "Raw-text elements are skipped, because Text escaping inside a script produces \"&lt;\" rather than \"<\" and corrupts the script rather than protecting it." The skipped map hand-copies the raw-text names and leaves out plaintext, which lolhtml.IsRawText lists (rawtext.go documents all ten and says the list exists so a caller does not "copy ten names out of a doc comment - which then falls behind the parser silently"). Verified with the built binary: `printf '<p>a</p><plaintext>alpha & <b>not markup</b> alpha' | highlight alpha` emits `&amp;` and `&lt;b&gt;` inside the plaintext, which a browser renders literally — the content is corrupted, not protected. plaintext runs to the end of the input, so this affects the whole remainder of any document containing one. Fix: replace the hand-copied membership test with `lolhtml.IsRawText(tag)` (keeping the extra non-raw-text names such as template, option and select as a separate check); the depth then stays incremented for plaintext, which is correct because plaintext has no end tag.

### 109. [low] hoiststyle — the documented usage line uses an -at flag that the program does not define, so the example command exits 2

*`examples/gip/hoiststyle/main.go:4`*

The package comment's second usage example is `hoiststyle -prefix h -at head < page.html`, but main registers only -prefix and -report. Running the documented command prints "flag provided but not defined: -at" plus the usage text and exits 2 — verified with the built binary. It also contradicts the paragraph immediately below it, which states that the stylesheet is emitted at the document end because that "is the only place a single pass can put it". A reader following the example's own synopsis hits a failure on the first try and is left unsure which of the two statements is true. Fix: delete the -at example line (the doc's own reasoning says the placement is not selectable in one pass), or add the flag with a two-pass implementation.

### 110. [low] islands — An existing data-hydrate value is HTML-unescaped and written back raw, changing what the browser sees

*`examples/gip/islands/main.go:185`*

Element.Attribute returns raw source text with character references still encoded, and SetAttribute takes raw source text (escaping only the double quote) — so a value read, decoded with html.UnescapeString, and written straight back is decoded once too often. The handler does exactly that for data-hydrate: it reads the page's own value, unescapes it, and passes the decoded string to SetAttribute. Reproduced: `<div data-island="A" data-hydrate="a&amp;amp;b">` becomes `data-hydrate="a&amp;b"`, i.e. the value a browser reads changes from `a&amp;b` to `a&b`. This also contradicts the comment on `props` at :237-239, which states the principle correctly — "The attribute itself is left exactly as the page wrote it: the decoded form is what a bundler wants and the raw form is what the browser needs" — and then the one attribute this program writes back breaks it. A copier who applies the same read-unescape-write shape to href, src or a data-* value carrying markup gets silently corrupted attributes.

### 111. [low] jsonld — typeOf HTML-unescapes script-body text, contradicting the file's own rule that references are not decoded there

*`examples/gip/jsonld/main.go:258`*

`check` states the rule for this program correctly at :128-130: "The raw text is a script body, so character references are not decoded in it by a parser and must not be decoded here either: \"&amp;\" in a JSON string is those five characters." `typeOf`, which produces the @type shown for every block in the report, then calls stdhtml.UnescapeString on a substring of that same raw script body. A block whose @type is the JSON string "A&amp;B" — five characters that encoding/json and every JSON-LD consumer read literally — is reported as `@type=A&B`, a value that appears nowhere in the document and that a reader grepping for it will not find. The rest of the program is careful about exactly this, which is what makes the one decode misleading to copy. Fix: return `rest[:k]` unchanged, and if the report needs to be safe to paste into a terminal, quote it (strconv.Quote) rather than decode it.

### 112. [low] linkreport — The report attributes text to `r.links[len-1]` and to a single `r.open` flag, so nested or unclosed anchors get the wrong text and produce false findings

*`examples/gip/linkreport/main.go:113`*

The end-tag handler assumes the last element appended to `r.links` is the one it belongs to, and text accumulation is a single shared builder gated by one boolean that any later `<a>` resets. Both assumptions fail as soon as two anchors are open at once, which lol-html reports faithfully because it is a token rewriter and does not un-nest. Measured: on `<a href=1>one<a href=2>two</a>` the second start tag calls `r.text.Reset()`, discarding "one"; the only end-tag handler that fires is the inner one, so `links[0].Text` stays `""` and `links[1].Text` becomes "two". `findings()` then reports link #1 under `empty-text` — "a link with no text is unreachable by name" — which is false, and `main` exits 1 on it, so a CI gate built on this fails on a lie. On `<a href=1><a href=2>two</a></a>` the outer handler runs last and writes over `links[1]`, giving it the outer anchor's `End` offset, so the byte range no longer slices the link it names.

### 113. [low] markdown — `<pre>``<code>` emits stray backticks inside the fenced block

*`examples/gip/markdown/main.go:239`*

push() writes the inline delimiter for `code` unconditionally, without checking whether a fenced block is already open (preDepth > 0). `<pre>``<code>` is the standard HTML spelling of a code block, so the inline backtick lands inside the fence, where Markdown does not read it as a delimiter and renders it literally. Reproduced: printf '`<pre>``<code>`fn main() {}\n`</code>``</pre>``<p>`after`</p>`' | go run ./examples/gip/markdown ``` `fn main() {} ` ``` after The fenced block's content is "`fn main() {}\n`" — two backticks that were not in the source appear in the rendered code. Consequence for a copier: the package comment lists "code" and "pre" among the subset it converts, and this is the single most common markup for both. The converted document contains characters the input did not, which for a code block is a content change rather than a formatting one — and the Dropped() report says nothing, because nothing was dropped. Fix: in push(), suppress the inline delimiter when c.preDepth > 0 (and correspondingly in pop()), so a `<code>` inside a `<pre>` contributes no delimiter of its own.

### 114. [low] mojibake — The usage example labels the classic mojibake with the direction reversed, contradicting Kind.String and the rest of the doc

*`examples/gip/mojibake/main.go:5`*

The sample output labels "Itâ€™s a cafÃ©" as "windows-1252 read as utf-8" and "PrÃ¼fung" as "utf-8 read as windows-1252", but both are the same phenomenon and the program only ever prints the second: UTF8AsCP1252.String() is "utf-8 read as windows-1252", and the doc's own "What it looks for" section says "The signature of UTF-8 read as windows-1252". A reader taking the direction from the usage block learns it backwards on a topic where the direction is the whole point, and then cannot match the label against the program's real output. Fix: label both sample lines "utf-8 read as windows-1252", or use "not utf-8 at all" for the second if it was meant to illustrate the InvalidBytes kind.

### 115. [low] needsrewrite — Writer is abandoned without Close on the Write error path

*`examples/gip/needsrewrite/main.go:223`*

Run returns as soon as rw.Write fails and never calls rw.Close, so the rewriter, its selectors and its cgo handles are held until the runtime cleanup happens to fire - the library documents that cleanup as "a leak guard, not the supported path" (rewriter.go NewWriter), and it is one a copier disables the moment a handler closes over the Writer. In a server that calls Run per response the shape leaks native memory for as long as the GC lets it, and the error the failed Close would have carried is never seen. The success path is fine; only the failure path drops it. Fix: `defer` nothing and instead close on both paths, e.g. `if _, werr := rw.Write(doc); werr != nil { rw.Close(); return true, changed, werr }` - keeping exactly one Close whose error is checked on the success path, as the Close doc requires.

### 116. [low] noopener — Forms with a new-window target are counted as hardened although nothing is changed

*`examples/gip/noopener/main.go:140`*

The form branch increments h.hardened and returns before doing anything, because a form has no rel attribute. The report then claims work that did not happen: `printf '<form target="_blank" action="/x"></form>' | noopener` prints `hardened=1` with byte-identical output. Anyone using the counter to confirm a corpus was hardened - the obvious use for a report in a security tool - is told a page is fixed when its form still hands window.opener to the new context. Fix: count these separately (e.g. an `unfixable` counter reported as "form target=_blank, cannot be hardened by an attribute: N") and leave hardened for elements actually rewritten.

### 117. [low] pagenav — The documented one-pass streaming mode does not stream: run always reads the whole document into memory

*`examples/gip/pagenav/main.go:291`*

The package doc is built around the distinction `-next and -prev given → one pass, streaming` versus `neither given → two passes, and the document is held in memory`, and closes with "A caller who already knows the neighbours … should say so and stay streaming." But `run` calls `io.ReadAll(r)` unconditionally before deciding whether a reading pass is needed, and `writePass` then hands the whole slice to one `Write`. So the advertised streaming path holds the entire document exactly like the two-pass one; the only thing the flags save is the second parse. A copier who follows the doc's advice to keep peak memory flat on large pages gets no such thing, and the measured claim they are trusting ("What does grow with the document is the buffer") is false for the mode it exempts. Fix: branch before reading — when `given()` and `current == 0`, build the writer and `io.Copy(out, r)` straight through; only take the `io.ReadAll` path when a reading pass is actually required.

### 118. [low] poisoned — The documented table says the memory-limit failure delivers a prefix; the program prints "nothing"

*`examples/gip/poisoned/main.go:8`*

The header table's "delivered" column claims `memory limit ... prefix`, and the prose says "Everything up to the failing token has been written". Running the program gives `memory limit ... nothing`: with MaxMemory 512 and 256-byte chunks the limit is hit before any output is flushed to the destination. The whole point of this example is that its table is the authoritative state machine, so a reader takes the wrong lesson — that a memory-limit failure always leaves a partial page at the client — when in this configuration it leaves nothing. Fix: correct the table row to match what the program prints (or note that whether a prefix exists depends on when the limit is hit relative to the first flush).

### 119. [low] preserve — The package doc's sample output lists a rewrite the program does not have

*`examples/gip/preserve/main.go:11`*

The header shows seven rows, including "comment after every element, guarded", but Rewrites() returns six and that one is absent (the prose one line below correctly says five preserving plus one hazard). Running the program prints six rows. A reader comparing the documented output against a real run, or looking for the guarded-after-comment recipe that the doc advertises as one of the demonstrated shapes, finds neither. Fix: drop the stale row from the sample output, or add the rewrite it names.

### 120. [low] shrink — structuralCuts abandons its Writer without closing it on the write-error path

*`examples/gip/shrink/main.go:148`*

The Close path is handled, but the Write error path returns without closing the Writer, so the rewriter, its selectors and every handle registered by the OnElement("*") handler stay allocated until the drop cleanup runs. structuralCuts is called once per reduction round on documents chosen precisely because they make the rewriter misbehave, so this is the path most likely to be taken in this program, and it is the one that leaks. The library states the rule plainly: "Close every Writer, including one being abandoned." A reader copying this carries the omission into a loop of their own. Fix: `defer w.Close()` right after the NewWriter check (dropping the explicit Close, since the cuts are discarded on any failure anyway), or close explicitly before `return nil`.

### 121. [low] summary — Result.Total is documented and rendered but never set by Extract

*`examples/gip/summary/main.go:71`*

Result.Total is documented as "the size of the document, so the two can be compared" and String() prints "read %d of %d bytes" with it, but Extract never assigns it — only main_test.go fills it in by hand. Verified: Extract on a 127-byte document returns a Result whose String() is "... (first paragraph, read 112 of 0 bytes)". A copier who logs Result.String() ships a log line that always says "of 0 bytes", which is worse than saying nothing since it reads as a measurement. It cannot be filled in generally either — the point of the example is not to read the whole document — so the honest fixes are to drop the field and the "of %d" from String(), or to have Extract take the total from the caller (e.g. a size argument) and document that it is optional.

### 122. [low] tablejson — Renamed keeps only the last rename per original name, so the duplicate count under-reports

*`examples/gip/tablejson/main.go:406`*

names() records disambiguations in a map keyed by the original name, so a header row with three cells called "a" produces keys ["a","a 2","a 3"] but `renamed` = {"a":"a 3"} — the "a 2" rename is overwritten and lost. Verified on `<table><tr><th>a<th>a<th>a<tr><td>1<td>2<td>3</table>`: the JSON reports one rename and stderr prints "table 1 had 1 duplicate column name(s)" where two columns were renamed. The package doc says the program "keeps the original name in the report, and counts what it had to rename", which is the whole basis on which a caller is supposed to decide whether the table was worth reading, so the undercount defeats the feature's stated purpose. Fix: make Renamed a slice of {index, original, final} (or key it by the final name, which is unique), and count entries rather than map size.

### 123. [low] tablelayout — RefusedInParagraph counts refused columns as well as rows, but the report calls them all rows

*`examples/gip/tablelayout/main.go:86`*

Both the row handler (line 151) and the column handler (line 160) increment RefusedInParagraph, while the report line prints "%d rows inside a `<p>`". Verified: a single row containing a single column inside a paragraph prints "refused 2 rows inside a `<p>`". Since the report is the whole point of the program — the package doc calls it the thing that lets a caller check a conversion nobody else can check — a count that inflates rows by the number of refused cells misleads the person doing that checking. Fix: keep two counters (refused rows and refused columns) and print each, or increment only in the row handler.

### 124. [low] textmap — Writer is abandoned without Close on every write-error path

*`examples/gip/textmap/main.go:85`*

Map, TextRegions and Rewritten all return the error from feed() without calling w.Close(). The library is explicit that this is not a supported shape (NewWriter doc: "Close every Writer, including one being abandoned. There is a cleanup attached here ... but it is a backstop rather than a second way of doing this"). Every other example in this batch (tee, toc, transitions, translate, tweetquote, twoways) does call w.Close() before returning a write error, so this file is the outlier a reader is likely to copy from because it is the shortest. Consequence for a copier: the lol-html rewriter, its selectors and every cgo handle stay alive until the GC happens to run the AddCleanup backstop -- unbounded native memory growth in a long-lived server that hits write errors (a client that went away, a broken destination).

### 125. [low] texttruth — Writer is abandoned without Close when Write fails

*`examples/gip/texttruth/main.go:121`*

ParsedText returns the error from w.Write(doc) without calling w.Close(), so the Writer is dropped mid-document. The library's NewWriter doc says to "Close every Writer, including one being abandoned" and that the AddCleanup path is "a leak guard, not the supported path". A copier who lifts this function into a service leaks the native rewriter and its cgo handles until the GC runs the backstop on every failed write, and the backstop stops working altogether the moment one of their handlers closes over the Writer (the handle table then keeps the Writer permanently reachable, so the cleanup can never fire). Fix: if _, err := w.Write(doc); err != nil { w.Close() return "", err }

### 126. [low] units — An unused struct field documents retaining *TextChunk values past their handler

*`examples/gip/units/main.go:160`*

`converter.pending []*lolhtml.TextChunk` is declared with the comment "chunks are the chunks of the node so far, so they can be removed once the whole node is known" and is never read or written anywhere in the package. The pattern it describes does not work: a TextChunk is valid only inside the handler that received it, and `TextChunk.Remove` on a detached chunk silently does nothing (text.go:208, `if p, err := t.live(); err == nil`). Consequence for a copier: this is the one place in the batch that names the retained-unit anti-pattern approvingly, and a reader who implements what the comment describes gets a rewrite that drops nothing and duplicates every text node, with no error anywhere. The working code alongside it already removes each chunk in its own handler. Fix: delete the field and its comment.

### 127. [low] untrack — -mark is documented as leaving a comment but inserts visible page text

*`examples/gip/untrack/main.go:126`*

The flag help says "leave a comment where each pixel was removed" and the code comment at line 124 repeats "The marker is inserted after the element rather than replacing it", but the insertion is `e.After("removed tracker: "+src, lolhtml.Text)` - ContentType Text, so it is escaped character data, not a comment. Confirmed: `printf '<p>x<img src="https://www.google-analytics.com/collect?v=1">y</p>' | untrack -mark` produces `<p>xremoved tracker: https://www.google-analytics.com/collect?v=1y</p>` - the marker is rendered prose spliced into the middle of a paragraph. Consequence for a copier: switching on the audit-trail flag defaces every page it processes with visible text, and the intent (an invisible trace for debugging) is not achieved. The Text choice is the right instinct for the untrusted URL; the delivery is wrong. Fix: build the comment with `d`/`e` comment insertion, or - since the URL is untrusted and a comment has no escaping - emit `<!-- untrack removed a tracker -->` as HTML with the URL omitted or scrubbed of `--` and `>`.

### 128. [low] widgets — Widgets that are skipped for an omitted end tag still get slot attributes written onto their children

*`examples/gip/widgets/main.go:219`*

The part handlers are registered as independent `OnElement(rule.Match+" > "+sel)` options and have no access to the `own[...]` decision the rule handler makes, so they set `slot="..."` on children of every match, including the ones the program deliberately declines to rename. Confirmed: `printf '<ul><li class="tabs"><div class="tab-title">a</div><li class="tabs">b</ul>' | widgets -rule 'li.tabs=my-tabs,part=div.tab-title:title'` reports `0 widgets upgraded, 2 skipped` and still emits `<div class="tab-title" slot="title">a</div>`. Consequence for a copier: the package doc's promise that skipped widgets are "counted and reported, which is the honest answer rather than a corrupted list" is not what the output shows - the document comes back half-migrated, with slot attributes that mean nothing outside a shadow root, and a second run over the output cannot tell what was already touched. Fix: apply the slot inside `apply()` from the parent handler (via `e.OnEndTag`/accumulated state), or have the part handler consult the same `own` map by recording the enclosing match's start offset.

### 129. [info] ids — Package doc says five reference attributes hold several ids; the code lists six

*`examples/gip/ids/main.go:24`*

The doc comment enumerates twelve id-naming attributes and then says "Five of them hold several ids separated by spaces rather than one, which is a detail a program has to get right to report on them at all." The `Multiple` slice the code actually uses has six: headers, aria-controls, aria-describedby, aria-flowto, aria-labelledby, aria-owns. Consequence for a copier: the doc comment is the summary a reader trusts when lifting the attribute lists into their own tool, and it undercounts the space-separated set by one - inviting them to reconstruct a five-entry list and treat one of the six (most likely `headers`, the non-ARIA one) as single-valued, which silently mis-parses `headers="a b"` as one id named "a b". Fix: change "Five" to "Six" in the doc comment.

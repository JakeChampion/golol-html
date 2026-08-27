package lolhtml

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRawTextBreakout is returned by an insertion into the content of a raw-text
// element when the inserted content would end that element.
//
// Ten element names hold content an HTML parser does not read as markup. Nine of
// them can be ended from inside, and an insertion into one of those is checked:
//
//	script style                        raw text; references are not decoded
//	iframe noembed noframes noscript xmp  the same, and nothing else is special
//	textarea title                      escapable raw text; references decode
//
// The tenth is plaintext, which runs to the end of the input: nothing closes it,
// so nothing can break out of it and there is nothing to check. See the note on
// [Element.CanHaveContent] and plaintext.
//
// Inside those nine an HTML parser is not looking for markup, so the only thing
// that can end one is its own closing tag. Content passed as [HTML] is inserted
// verbatim, which means a "</script>" in it does not become part of the script -
// it closes the script, and everything after it is markup in the document. That
// is a working injection whenever the content came from anywhere untrusted, and
// it is silent: the output parses, nothing errors, and the script that runs is
// not the script that was written.
//
// [Comment.SetText] has always refused the same shape of input for the same
// reason. This is the other half of that.
//
// The insertion is refused rather than escaped because the escape that works
// depends on the element, and for five of the nine there is none. Where one
// exists the error names it: a JavaScript or JSON "<\/script" for a script, a CSS
// "\3c /style" for a style, and for a textarea or a title, inserting as [Text]
// instead, because references are decoded there. Inside an iframe, noembed,
// noframes, noscript or xmp, references are not decoded and there is no inner
// language, so the sequence cannot be represented at all and the content has to
// change.
//
// What is checked is the position, not the type: insertions into the element's
// own content, which are [Element.Prepend], [Element.Append],
// [Element.SetInnerContent] and [EndTag.Before]. [Element.Before],
// [Element.After] and [Element.Replace] write outside the element, where a
// closing tag is ordinary markup, and are not affected. Nor is [ContentType]
// [Text], which escapes the "<" and so cannot end anything - though in the seven
// where references do not decode, Text corrupts the content instead; see the
// package documentation on inserting into a script or a style.
//
// Two gaps, both measured. The streaming insertions ([Element.StreamPrepend] and
// the rest) are not checked, because content arrives in pieces and a closing tag
// can straddle two of them. Neither are [TextChunk.Before], [TextChunk.After]
// and [TextChunk.Replace], which is the more surprising one, because editing a
// script through a text handler is the obvious way to do it: a text chunk has no
// way to name the element it is inside, so the check has nothing to look up.
// Until it does, a text handler has to guard itself: it knows the tag, because it
// registered the selector, and [IsRawText] answers for a tag it does not know in
// advance.
//
// A rename is the other way round this. [Element.SetTagName] can turn a script
// into a div, and its text into markup, without inserting anything at all - so
// there is nothing for this check to look at. See that method, and
// [Element.RemoveAndKeepContent], which does it by taking the tags away
// altogether. [IsRawText] is the list, for a caller who has to decide.
//
// The check is by tag name only, so it does not consider namespaces. In SVG and
// MathML none of these elements is raw text, and the refusal there is
// conservative rather than wrong: an inserted "</title>" still ends an
// <svg><title>, by ordinary tree construction rather than by the tokenizer.
var ErrRawTextBreakout = errors.New("lolhtml: inserted content would end the raw-text element it is inside")

// IsRawText reports whether an element with this tag name holds content that an
// HTML parser does not read as markup.
//
// Ten names do:
//
//	script style                          raw text; character references are not decoded
//	iframe noembed noframes noscript xmp  the same, and nothing else is special
//	textarea title                        escapable raw text; references decode
//	plaintext                             raw text, and it runs to the end of the input
//
// The package already uses this list for [ErrRawTextBreakout], which covers nine
// of the ten: nothing closes a plaintext, so nothing can break out of one. This
// reports all ten, because the hazards a caller has to handle for itself are
// about the content not being markup, not about closing tags:
//
//	[Element.SetTagName]          renaming one turns its text into markup
//	[Element.RemoveAndKeepContent] taking the tags away does the same
//	[TextChunk.Before] and the rest  insertions through a text handler are not checked
//
// Each of those says so, and until now said it without giving the caller any way
// to ask. A tool that renames, unwraps, or rewrites text under a wide selector
// has to know the list, and the alternative to asking is copying ten names out of
// a doc comment - which then falls behind the parser silently. The list here is
// measured against the parser by TestTheGuardCoversEveryRawTextElement, so it
// cannot.
//
// It is the predicate for the insertion question - can content written into this
// element end it - and not for the other question these ten names come up in:
// whether character references in the content are decoded. That one is
// [DecodesCharacterReferences], which is this list minus textarea and title. The
// NUL rule does key on this list exactly - inside these ten a NUL becomes U+FFFD,
// elsewhere a parser drops it - and both are measured against the parser for every
// element name in differential/texttruth_test.go, with the whole conversion in
// examples/gip/texttruth.
//
// The comparison is by tag name and is case-insensitive for ASCII, so it accepts
// both what [Element.TagName] reports and what
// [Element.TagNamePreserveCase] does. It does not consider namespaces: in SVG and
// MathML none of these elements is raw text, and a <title> inside an <svg> holds
// ordinary markup. A caller who cares about that distinction has the namespace -
// see [Element.NamespaceURI].
func IsRawText(tag string) bool {
	if isRawTextLower(tag) {
		return true
	}
	lower := strings.ToLower(tag)
	return lower != tag && isRawTextLower(lower)
}

// DecodesCharacterReferences reports whether an HTML parser decodes character
// references in the content of an element named tag.
//
// It is the predicate for the reading question, where [IsRawText] is the
// predicate for the writing one. The same ten names come up in both, and the
// answers are different sets: of the ten elements whose content is not markup,
// eight leave references alone and two - textarea and title, the escapable
// raw-text pair - decode them. Everything else decodes, because its content is
// markup.
//
// So a program deciding whether to unescape the text it was handed wants this,
// and IsRawText would be wrong by exactly those two names. Wrong in both
// directions and silently: unescaping a <style>'s content makes it say something
// it does not say, and not unescaping a <title>'s loses the decoding a parser
// performs. The NUL rule does key on IsRawText exactly - inside those ten a NUL
// becomes U+FFFD, elsewhere a parser drops it.
//
// Measured against golang.org/x/net/html for every element name in the HTML
// index, in both directions, by TestTheDecodeListIsTheParsersList in the
// differential suite - which is what makes this a question the library can answer
// rather than two names to copy out of a doc comment. examples/gip/texttruth
// composes it with the other three rules that separate reported text from parsed
// text.
//
// Comparison is by tag name, case-insensitive for ASCII, as for IsRawText. It
// does not consider namespaces: in SVG and MathML none of these elements is raw
// text, so a <title> inside an <svg> holds ordinary markup and decodes. A name
// that is not an element decodes, which is the right answer for content that is
// parsed as markup.
func DecodesCharacterReferences(tag string) bool {
	if !IsRawText(tag) {
		return true
	}
	return escapableRawText(tag)
}

// escapableRawText reports whether tag is one of the two raw-text elements whose
// content still has its character references decoded. Kept next to
// rawTextElements so the two lists cannot drift apart unnoticed.
func escapableRawText(tag string) bool {
	switch tag {
	case "textarea", "title":
		return true
	}
	lower := strings.ToLower(tag)
	return lower != tag && (lower == "textarea" || lower == "title")
}

func isRawTextLower(tag string) bool {
	if tag == "plaintext" {
		return true
	}
	_, ok := rawTextElements[tag]
	return ok
}

// rawTextElements are the elements whose content an HTML parser does not read as
// markup and which can be ended from inside. The map is by lower-case tag name,
// which is what TagName reports, and the value is what the error suggests doing
// instead - per element, because the answers have nothing in common: one is a
// JavaScript escape, one is a CSS escape, one is a different ContentType, and one
// is "you cannot".
//
// The list was measured rather than read off the specification: every element
// name in the HTML index was tried, and ten are the ones where an element inside
// is not an element. The tenth is plaintext, deliberately absent because nothing
// closes it. See TestTheGuardCoversEveryRawTextElement, which repeats that
// measurement so this map cannot fall behind the parser.
var rawTextElements = map[string]string{
	"script": `escape it for the language inside the element, as "<\/script" in JavaScript or JSON`,
	"style":  `escape it for CSS, as "\3c /style" inside a string`,

	// Escapable raw text: character references are decoded, so Text works.
	"textarea": decodesReferences("textarea"),
	"title":    decodesReferences("title"),

	// Raw text with no inner language: references are not decoded either, so
	// there is nothing the caller can write.
	"iframe":   noEscapeExists("iframe"),
	"noembed":  noEscapeExists("noembed"),
	"noframes": noEscapeExists("noframes"),
	"noscript": noEscapeExists("noscript"),
	"xmp":      noEscapeExists("xmp"),
}

func decodesReferences(tag string) string {
	return "insert it with ContentType Text instead: character references are " +
		"decoded in <" + tag + ">, so \"&lt;\" reads back as \"<\""
}

func noEscapeExists(tag string) string {
	return "character references are not decoded in <" + tag + "> and it has no " +
		"inner language, so this sequence cannot appear inside it at all"
}

// checkRawText refuses content that would close a raw-text element named tag.
// It returns nil for any element not in rawTextElements - including plaintext,
// which cannot be closed - and for content with nothing in it that could close
// one.
func checkRawText(tag, content string) error {
	lower := strings.ToLower(tag)
	advice, ok := rawTextElements[lower]
	if !ok {
		return nil
	}
	i := findClosingTag(lower, content)
	if i < 0 {
		return nil
	}
	return fmt.Errorf("%w: <%s> content contains %q at byte %d; %s",
		ErrRawTextBreakout, lower,
		content[i:min(i+len(lower)+3, len(content))], i, advice)
}

// CheckRawText reports whether content would end the raw-text element named tag,
// returning an error wrapping [ErrRawTextBreakout] if it would and nil otherwise.
// A tag that is not one of the raw-text elements is always nil, and so is
// [plaintext], which nothing closes.
//
// It exists because one set of insertion paths cannot apply this check for you. The
// [Element] and [EndTag] methods know which element they are writing into and refuse
// a breakout themselves. A [TextChunk] does not: lol-html hands a chunk over with no
// way to ask what element it came from, so [TextChunk.Before], [TextChunk.After] and
// [TextChunk.Replace] with [HTML] write whatever they are given.
//
// That is the path a rewrite editing a stylesheet or a script body has to use,
// because [Text] escapes the three markup characters and raw text does not decode
// references - so a CSS ">" comes back as "&gt;" and a script's "a < b" as
// "a &lt; b". A handler registered as OnText("style") knows the tag name it asked
// for, and can hand it to this:
//
//	lolhtml.OnText("style", func(t *lolhtml.TextChunk) error {
//		// … accumulate to IsLastInTextNode, rewrite the CSS …
//		if err := lolhtml.CheckRawText("style", css); err != nil {
//			return err
//		}
//		return t.Replace(css, lolhtml.HTML)
//	})
//
// The rule it applies is the tokenizer's, measured rather than assumed: raw text ends
// at "</" followed by the tag name in any case, followed by ">", "/", ASCII
// whitespace, or the end of the content - because what follows an insertion is the
// rest of the document. The error names the offending sequence, its offset, and what
// to write instead for that element.
func CheckRawText(tag, content string) error { return checkRawText(tag, content) }

// findClosingTag returns the index of a sequence that would end a raw-text
// element named tag, or -1.
//
// The rule is the tokenizer's, and it was measured against golang.org/x/net/html
// rather than read off the specification: raw text ends at "</" followed by the
// tag name, compared without regard to case, followed by something that can
// terminate a tag name - ">", "/", or ASCII whitespace. So "</scriptx" does not
// end a script and "</script foo>" does.
//
// The end of the content counts as a terminator too, and that is not
// conservatism. What follows an insertion is the rest of the document: after
// Prepend it is the element's original content, so Prepend("</script") in front
// of ">alert(1)" produces "</script>alert(1)" and the element is closed by the
// two halves together.
func findClosingTag(tag, content string) int {
	lower := strings.ToLower(content)
	needle := "</" + tag
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return -1
		}
		j += i
		rest := lower[j+len(needle):]
		if rest == "" || isTagNameEnd(rest[0]) {
			return j
		}
		i = j + len(needle)
	}
}

// isTagNameEnd reports whether c terminates a tag name.
func isTagNameEnd(c byte) bool {
	switch c {
	case '>', '/', ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

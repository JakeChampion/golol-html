package lolhtml

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRawTextBreakout is returned by an insertion into the content of a script,
// style, textarea or title element when the inserted content would end that
// element.
//
// Those four hold raw text: an HTML parser does not look for markup inside them,
// so the only thing that can end one is its own closing tag. Content passed as
// [HTML] is inserted verbatim, which means a "</script>" in it does not become
// part of the script - it closes the script, and everything after it is markup in
// the document. That is a working injection whenever the content came from
// anywhere untrusted, and it is silent: the output parses, nothing errors, and
// the script that runs is not the script that was written.
//
// [Comment.SetText] has always refused the same shape of input for the same
// reason. This is the other half of that.
//
// The insertion is refused rather than escaped because there is no escaping that
// works here. Escaping for HTML corrupts the script instead - see the package
// documentation on inserting into a script or a style - and escaping for what
// the script means needs to know where in the script the content lands. Do that
// in the caller: in JavaScript a string literal can carry "<\/script>", and CSS
// and JSON have their own answers.
//
// Only insertions into the element's own content are checked: [Element.Prepend],
// [Element.Append], [Element.SetInnerContent] and [EndTag.Before] on one of the
// four. [Element.Before], [Element.After] and [Element.Replace] write outside the
// element, where a closing tag is ordinary markup, and are not affected. Nor is
// [ContentType] [Text], which escapes the "<" and so cannot end anything.
var ErrRawTextBreakout = errors.New("lolhtml: inserted content would end the raw-text element it is inside")

// rawTextElements are the elements whose content is not parsed as markup. The
// map is by lower-case tag name, which is what TagName reports.
//
// script and style are raw text; textarea and title are escapable raw text,
// where character references are decoded. The difference does not matter here:
// all four end only at their own closing tag.
var rawTextElements = map[string]bool{
	"script":   true,
	"style":    true,
	"textarea": true,
	"title":    true,
}

// checkRawText refuses content that would close a raw-text element named tag.
// It returns nil for any element that is not one of the four, and for content
// with nothing in it that could close one.
func checkRawText(tag, content string) error {
	if !rawTextElements[strings.ToLower(tag)] {
		return nil
	}
	if i := findClosingTag(strings.ToLower(tag), content); i >= 0 {
		lower := strings.ToLower(tag)
		return fmt.Errorf("%w: <%s> content contains %q at byte %d; escape it for "+
			"the language inside the element instead, as <\\/%s in JavaScript or JSON",
			ErrRawTextBreakout, lower,
			content[i:min(i+len(lower)+3, len(content))], i, lower)
	}
	return nil
}

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

// Command texttruth reconstructs a document's text - the characters, not the source bytes - from
// the text chunks a rewrite reports, and it exists because the two are not the same thing and
// the difference is four rules with three different element lists.
//
// TextChunk.Text is source. `<p>caf&eacute;</p>` reports "caf&eacute;", which is the right
// answer for a rewrite writing content back (textroundtrip_test.go is about getting that half
// right) and the wrong one for a program that wants what the page says: a word counter, a
// readability score, a search index, a translation memory. Turning one into the other is what
// this does, and the answer is checked against golang.org/x/net/html's text nodes in the
// differential suite rather than against my reading of the specification.
//
// The rules, each measured over the 144 element names in the HTML index:
//
//	CR and CRLF become LF          every element, no exception
//	character references decode    every element except eight
//	NUL becomes U+FFFD             the ten raw-text elements; elsewhere it is dropped
//	one leading LF is dropped      pre, listing and textarea only
//
// The eight where references do not decode are iframe, noembed, noframes, noscript, plaintext,
// script, style and xmp. That is the raw-text set minus textarea and title, which are escapable
// raw text: they hold no markup but their references do decode. So a program that unescapes
// everything corrupts a stylesheet - `<style>a{content:"&amp;"}</style>` says "&amp;" and not
// "&" - and one that skips everything lolhtml.IsRawText reports loses the decoding in a title.
// IsRawText is the right predicate for the NUL rule and the wrong one for this rule, and the
// difference is exactly those two names - which is why the library answers it directly, as
// lolhtml.DecodesCharacterReferences.
//
// The leading-newline list is the one that is not any raw-text set: pre, listing and textarea
// drop one, xmp does not, and a CRLF there counts as the one newline. It applies to the text
// that opens the element, not to text anywhere inside it, so `<pre>\nx</pre>` says "x" while
// `<pre><b>\nx</b></pre>` says "\nx".
//
// What this cannot do is decide the boundary cases a tree gives away for free. A document-level
// text handler is handed text with no indication of what it is inside, so the state here is
// tracked from the element and end-tag handlers around it - which is exact for the raw-text
// elements, because their content cannot contain markup, and for the opening text of a pre,
// because the start tag is the token before it.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// unescape decodes character references. The standard library's decoder agrees with
// golang.org/x/net/html on every case that distinguishes them - the prefix rule for a reference
// with no semicolon ("&notit;" is "¬it;", "&amp" is "&"), an unknown name left alone, a code
// point out of range replaced - so a program that only reads text needs no dependency for this.
// Checked in the tests, since "they agree" is the kind of thing that stops being true.
var unescape = stdhtml.UnescapeString

// noDecode asks the library. This used to be a literal list of eight names copied out of a doc
// comment - which is what lolhtml.DecodesCharacterReferences now answers, so that a program reading
// text cannot fall behind the parser silently. It is lolhtml.IsRawText minus textarea and title,
// and the differential suite measures both against the parser for every element name.
func noDecode(tag string) bool { return !lolhtml.DecodesCharacterReferences(tag) }

// eatsLeadingNewline are the elements that drop one newline immediately after the start tag.
// Not a raw-text set: textarea is raw text and pre and listing are not, and xmp is raw text and
// is not here.
var eatsLeadingNewline = map[string]bool{"pre": true, "listing": true, "textarea": true}

// ParsedText returns the document's text as a parser would report it, from the chunks a rewrite
// reports.
func ParsedText(doc []byte) (string, error) {
	var b strings.Builder

	// The element the text is inside, where that changes the answer. Raw-text content cannot
	// contain markup, so one name is enough rather than a stack.
	var raw string
	// Set when the token just seen was the start tag of a pre, listing or textarea, so the
	// next text chunk is the one that loses a newline.
	var pendingNewlineEater string

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			name := e.TagName()
			if lolhtml.IsRawText(name) {
				raw = name
				// plaintext never ends, so nothing clears this, which is right.
				if name != "plaintext" {
					if err := e.OnEndTag(func(*lolhtml.EndTag) error {
						raw = ""
						return nil
					}); err != nil {
						return err
					}
				}
			}
			if !eatsLeadingNewline[name] {
				pendingNewlineEater = ""
				return nil
			}
			pendingNewlineEater = name
			// An empty pre emits no text chunk at all, so without this the flag would
			// survive the element and eat the newline after it: `<pre></pre>\nx` said "x".
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				pendingNewlineEater = ""
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			text := tc.Text()
			eat := pendingNewlineEater != ""
			pendingNewlineEater = ""
			if text == "" {
				return nil
			}
			b.WriteString(convert(text, raw, eat))
			return nil
		}),
	)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(doc); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// convert applies the four rules to one chunk's source text. in is the raw-text element the text
// is inside, or "" for ordinary content; eatNewline is set for the text that opens a pre,
// listing or textarea.
func convert(text, in string, eatNewline bool) string {
	// Newlines first: the leading-newline rule is about a newline, and a CRLF there is one.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if eatNewline {
		text = strings.TrimPrefix(text, "\n")
	}

	if in == "" {
		// Ordinary content: NUL is dropped, references decode.
		text = strings.ReplaceAll(text, "\x00", "")
		return unescape(text)
	}
	// Raw text of either kind: NUL becomes the replacement character.
	text = strings.ReplaceAll(text, "\x00", "�")
	if noDecode(in) {
		return text
	}
	return unescape(text) // textarea and title
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "texttruth: give a file")
		os.Exit(1)
	}
	doc, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "texttruth:", err)
		os.Exit(1)
	}
	text, err := ParsedText(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "texttruth:", err)
		os.Exit(1)
	}
	fmt.Print(text)
}

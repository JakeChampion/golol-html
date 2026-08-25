package lolhtml

import "strings"

// This file is the other half of the warning in the package documentation: if
// you assemble markup as a string you are the serialiser, so here is the
// serialiser. Both functions are pure Go and allocate nothing when there is
// nothing to escape, so they are cheap to apply unconditionally.

// EscapeText escapes s for a position where HTML text is expected: between tags,
// where the value would otherwise be read as markup.
//
// It replaces &, < and > with the character references &amp;, &lt; and &gt;,
// and leaves everything else alone. That is exactly what the library does for
// content passed with ContentType Text, so
//
//	e.SetInnerContent(s, Text)
//
// and
//
//	e.SetInnerContent("<b>"+EscapeText(s)+"</b>", HTML)
//
// escape s identically; the second is how to keep that guarantee while also
// inserting markup. The equivalence is pinned by a test that compares the two
// paths over a corpus rather than assumed.
//
// EscapeText is not enough for an attribute value: quotes pass through it
// unchanged, so a value containing one would end the attribute and start
// another. Use EscapeAttribute there.
//
// The argument is a literal value, not markup. Everything this library reports -
// Element.Attribute, TextChunk.Text, Comment.Text - is raw source with character
// references still encoded, so escaping one of those again double-escapes it and
// "Configure &amp; run" becomes "Configure &amp;amp; run". For a value read from
// the document, either leave it raw and do not escape it, or decode it first with
// html.UnescapeString from the standard library and escape the result - remembering
// that in an attribute value that decoder is not the parser's, since it decodes a
// semicolon-less name that a browser leaves alone. Which of
// those is right depends on where the value came from as well as where it is
// going, because each context lets through the character the other one ends on. A
// value that came from text can be written back into text raw, and needs the quote
// escaped to go inside quotes you chose yourself. A value that came from an
// attribute can go into another attribute raw, and needs the "<" escaped to become
// text - an attribute may hold a raw "<", so a title of "<img src=x
// onerror=alert(1)>" written into an element's text is an element. Measured both
// ways in differential/context_test.go.
//
// Escaping is not sanitising. A URL is still a URL after escaping, so
//
//	`<a href="` + EscapeAttribute(u) + `">`
//
// is well-formed markup even when u is "javascript:alert(1)". Deciding which
// schemes to allow is a separate job, and neither function does it.
func EscapeText(s string) string { return escape(s, false) }

// EscapeAttribute escapes s for the inside of a quoted attribute value that you
// are writing yourself, as in
//
//	`<img src="` + EscapeAttribute(u) + `" alt="` + EscapeAttribute(a) + `">`
//
// It escapes what EscapeText escapes and both quote characters, so the result
// is safe between single or double quotes without the caller having to say
// which. It does not escape whitespace, so the value has to be quoted: an
// unquoted attribute ends at the first space and no escaping prevents that.
//
// Element.SetAttribute is the better tool whenever the attribute is on an
// element a handler already has, because it needs no escaping from the caller
// at all. This is for the case SetAttribute cannot reach: an element that does
// not exist yet, being built as markup.
//
// The caveats on EscapeText apply here too, in particular that escaping a URL
// does not make the URL safe.
func EscapeAttribute(s string) string { return escape(s, true) }

// escape does the work for both. The two differ only in whether quotes are
// escaped, and the byte loop is safe on invalid UTF-8 because every byte it
// acts on is ASCII and so cannot be part of a multi-byte sequence.
//
// Two passes, so the result is allocated once at exactly the right size. A
// strings.Builder with a guessed Grow reallocated three times on a string with
// sixty escapes in it, which is the sort of thing a caller applying this to
// every value in a document would rather not pay.
func escape(s string, quotes bool) string {
	extra := 0
	for i := 0; i < len(s); i++ {
		if r := ref(s[i], quotes); r != "" {
			extra += len(r) - 1 // the byte is replaced by the reference
		}
	}
	if extra == 0 {
		return s
	}

	// A Builder rather than a byte slice: its String does not copy, so the
	// exact Grow above is the only allocation.
	var b strings.Builder
	b.Grow(len(s) + extra)
	last := 0
	for i := 0; i < len(s); i++ {
		r := ref(s[i], quotes)
		if r == "" {
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(r)
		last = i + 1
	}
	b.WriteString(s[last:])
	return b.String()
}

// ref is the character reference for one byte, or the empty string if the byte
// is written through unchanged.
func ref(c byte, quotes bool) string {
	switch c {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '"':
		if quotes {
			return "&quot;"
		}
	case '\'':
		if quotes {
			// &#39; rather than &apos;: both are correct in HTML, and the
			// numeric one is also understood by XML tooling that predates
			// &apos; being in the HTML entity table.
			return "&#39;"
		}
	}
	return ""
}

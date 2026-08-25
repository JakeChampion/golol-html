package main

import (
	"fmt"
	"strings"
)

// Corpus is the set of documents the check runs over.
//
// It is written out rather than fuzzed because the point is coverage of shapes
// somebody chose: the ones a serialiser might normalise, the ones a parser has
// special rules for, and the ones that have caused trouble in this repository
// before. The fuzz targets cover the random half.
func Corpus() []Case {
	cases := []Case{
		// The ordinary shapes.
		{Name: "empty", Doc: ``},
		{Name: "text only", Doc: `hello`},
		{Name: "one element", Doc: `<p>hello</p>`},
		{Name: "a whole page", Doc: `<!DOCTYPE html><html><head><title>t</title>` +
			`<meta charset="utf-8"></head><body><h1>h</h1><p>text</p></body></html>`},

		// Attribute spelling, which a serialiser is most likely to normalise.
		{Name: "unquoted attribute", Doc: `<a href=/x>l</a>`},
		{Name: "single-quoted attribute", Doc: `<a href='/x'>l</a>`},
		{Name: "empty attribute value", Doc: `<a href="">l</a>`},
		{Name: "valueless attribute", Doc: `<input disabled>`},
		{Name: "valueless with equals", Doc: `<input disabled=>`},
		{Name: "spaces around equals", Doc: `<a href = "/x">l</a>`},
		{Name: "duplicate attributes", Doc: `<a href="/1" href="/2">l</a>`},
		{Name: "upper-case attribute", Doc: `<A HREF="/X">l</A>`},
		{Name: "attribute with newline", Doc: "<a\nhref=\"/x\"\ttitle='y'>l</a>"},
		{Name: "attribute containing markup", Doc: `<a title="a<b>c">l</a>`},
		{Name: "attribute containing quotes", Doc: `<a title='he said "hi"'>l</a>`},
		{Name: "many attributes", Doc: `<a ` + strings.Repeat(`data-x="y" `, 50) + `>l</a>`},

		// Tag shapes.
		{Name: "self-closing html element", Doc: `<div/>text</div>`},
		{Name: "self-closing void element", Doc: `<br/>`},
		{Name: "unclosed element", Doc: `<div><span>a`},
		{Name: "unclosed tag at the end", Doc: `<div class="x"`},
		{Name: "stray end tags", Doc: `</p></div></body>`},
		{Name: "empty end tag", Doc: `</>`},
		{Name: "implicit end tags", Doc: `<ul><li>a<li>b</ul>`},
		{Name: "table soup", Doc: `<table><tr><td>a<td>b</table>`},
		{Name: "deep nesting", Doc: strings.Repeat("<div>", 200) + "x" + strings.Repeat("</div>", 200)},

		// Comments and declarations, where "comment" is wider than it looks.
		{Name: "comment", Doc: `<!-- a comment -->`},
		{Name: "unclosed comment", Doc: `<!-- never ends`},
		{Name: "bogus comment", Doc: `<!x>`},
		{Name: "processing instruction", Doc: `<?php echo 1; ?>`},
		{Name: "xml declaration", Doc: `<?xml version="1.0"?>`},
		{Name: "comment with dashes", Doc: `<!--- a --- b --->`},
		{Name: "doctype", Doc: `<!DOCTYPE html>`},
		{Name: "legacy doctype", Doc: `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">`},
		{Name: "doctype in the middle", Doc: `<p>a</p><!DOCTYPE html><p>b</p>`},

		// Raw text, where markup is not markup.
		{Name: "script with markup inside", Doc: `<script>if (a<b) { document.write("<p>"); }</script>`},
		{Name: "style with a brace", Doc: `<style>p::after{content:"}"}</style>`},
		{Name: "textarea with markup", Doc: `<textarea><b>not bold</b></textarea>`},
		{Name: "title with an entity", Doc: `<title>a &amp; b</title>`},
		{Name: "unclosed script", Doc: `<script>var x = 1`},
		{Name: "xmp", Doc: `<xmp><b>x</b></xmp>`},
		{Name: "plaintext", Doc: `<plaintext>a</plaintext>b`},

		// Character references, which are never decoded on the way through.
		{Name: "named reference", Doc: `<p>caf&eacute;</p>`},
		{Name: "numeric reference", Doc: `<p>&#233; &#x2603;</p>`},
		{Name: "unterminated reference", Doc: `<p>&notanentity &amp</p>`},
		{Name: "ampersand alone", Doc: `<p>a & b</p>`},
		{Name: "lone angle bracket", Doc: `<p>a < b</p>`},

		// Whitespace and control characters.
		{Name: "crlf", Doc: "<p>a\r\nb</p>"},
		{Name: "cr alone", Doc: "<p>a\rb</p>"},
		{Name: "form feed", Doc: "<p>a\fb</p>"},
		{Name: "nul byte", Doc: "<p>a\x00b</p>"},
		{Name: "leading whitespace", Doc: "   \n\t<p>a</p>\n"},

		// Foreign content.
		{Name: "svg", Doc: `<svg><linearGradient id="g"/><rect width="1"/></svg>`},
		{Name: "svg with html inside", Doc: `<svg><foreignObject><p>a</p></foreignObject></svg>`},
		{Name: "mathml", Doc: `<math><mrow><mi>x</mi></mrow></math>`},
		{Name: "svg self-closing", Doc: `<svg><path d="M0 0"/></svg>`},

		// Non-ASCII, which must survive decoding and re-encoding.
		{Name: "utf-8 text", Doc: `<p>café 日本語 🎉</p>`},
		{Name: "utf-8 in an attribute", Doc: `<a title="日本語">l</a>`},
		{Name: "utf-8 tag name", Doc: `<日本>x</日本>`},

		// Ambiguous parses, which strict mode refuses.
		{Name: "xmp in select", Doc: `<select><xmp>a</xmp></select><p>after</p>`, NeedsLenientMode: true},
		{Name: "title in frameset", Doc: `<frameset><title>t</title></frameset>`, NeedsLenientMode: true},

		// The documented exception: bytes that are not valid in the declared
		// encoding, which any text handler rewrites to U+FFFD.
		{Name: "invalid utf-8 in text", Doc: "<p>caf\xe9</p>", TextHandlerChanges: true},
		{Name: "invalid utf-8 alone", Doc: "a\xffb", TextHandlerChanges: true},
	}

	// A larger document made of the small ones, because a corpus of fragments
	// does not exercise anything that only happens after a few kilobytes.
	var big strings.Builder
	big.WriteString(`<!DOCTYPE html><html><body>`)
	for _, c := range cases {
		if c.NeedsLenientMode || c.TextHandlerChanges {
			continue
		}
		fmt.Fprintf(&big, `<section data-name="%s">%s</section>`, c.Name, c.Doc)
	}
	big.WriteString(`</body></html>`)
	cases = append(cases, Case{Name: "everything concatenated", Doc: big.String()})

	return cases
}

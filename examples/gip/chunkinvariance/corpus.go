package main

import "strings"

// Documents is the corpus. It is aimed at the boundaries: a write can land in
// the middle of a tag name, an attribute value, a character reference, a
// multi-byte character, a comment, or raw text, and each of those is a different
// piece of the parser.
func Documents() map[string]string {
	return map[string]string{
		"empty":            ``,
		"text only":        `hello world`,
		"page":             `<!DOCTYPE html><html><head><title>t &amp; u</title></head><body><p>one <b>two</b> three</p><!-- c --><a href="/x" class="y">l</a></body></html>`,
		"long text":        `<p>` + strings.Repeat("word ", 200) + `</p>`,
		"long attribute":   `<a title="` + strings.Repeat("x", 500) + `">l</a>`,
		"many attributes":  `<a ` + strings.Repeat(`data-x="y" `, 100) + `>l</a>`,
		"long tag name":    `<` + strings.Repeat("a", 200) + `>x</` + strings.Repeat("a", 200) + `>`,
		"raw text":         `<script>var s = "` + strings.Repeat("x", 300) + `";</script>`,
		"long comment":     `<!--` + strings.Repeat("c", 300) + `-->`,
		"references":       `<p>` + strings.Repeat("caf&eacute; &amp; &#233; ", 40) + `</p>`,
		"two byte runes":   `<p>` + strings.Repeat("é", 100) + `</p>`,
		"three byte runes": `<p>` + strings.Repeat("日", 100) + `</p>`,
		"four byte runes":  `<p>` + strings.Repeat("🎉", 100) + `</p>`,
		"mixed runes":      `<p>` + strings.Repeat("aé日🎉", 50) + `</p>`,
		"runes in attr":    `<a title="` + strings.Repeat("日", 100) + `">l</a>`,
		"runes in comment": `<!--` + strings.Repeat("日", 100) + `-->`,
		"runes in script":  `<script>` + strings.Repeat("日", 100) + `</script>`,
		"nested markup":    strings.Repeat(`<div><span>a</span></div>`, 50),
		"deep nesting":     strings.Repeat("<div>", 100) + "x" + strings.Repeat("</div>", 100),
		"foreign content":  `<svg><linearGradient/><foreignObject><p>a</p></foreignObject></svg>`,
		"unclosed":         `<div><span>a`,
		"legacy doctype":   `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd"><p>x</p>`,
	}
}

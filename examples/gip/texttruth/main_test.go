package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestTheFourRules, one at a time, on the smallest document that shows each.
func TestTheFourRules(t *testing.T) {
	for _, tt := range []struct{ name, doc, want string }{
		{"references decode in ordinary content", `<p>caf&eacute; &amp; b</p>`, "café & b"},
		{"a reference with no semicolon still decodes", `<p>&amp</p>`, "&"},
		{"an unknown reference is left alone", `<p>&unknown;</p>`, "&unknown;"},
		{"the prefix rule", `<p>&notit;</p>`, "¬it;"},
		{"CRLF becomes LF", "<p>a\r\nb</p>", "a\nb"},
		{"a lone CR becomes LF", "<p>a\rb</p>", "a\nb"},
		{"NUL is dropped in ordinary content", "<p>a\x00b</p>", "ab"},
		{"NUL is U+FFFD in a script", "<script>a\x00b</script>", "a�b"},
		{"NUL is U+FFFD in a title", "<title>a\x00b</title>", "a�b"},
		{"a script's references do not decode", `<script>a &amp; b</script>`, "a &amp; b"},
		{"a style's references do not decode", `<style>a{content:"&amp;"}</style>`, `a{content:"&amp;"}`},
		{"a title's references do decode", `<title>a &amp; b</title>`, "a & b"},
		{"a textarea's references do decode", `<textarea>a &amp; b</textarea>`, "a & b"},
		{"an xmp's references do not decode", `<xmp>a &amp; b</xmp>`, "a &amp; b"},
		{"pre drops one leading newline", "<pre>\nx</pre>", "x"},
		{"pre drops a leading CRLF as one newline", "<pre>\r\nx</pre>", "x"},
		{"pre drops only one", "<pre>\n\nx</pre>", "\nx"},
		{"listing drops one", "<listing>\nx</listing>", "x"},
		{"textarea drops one", "<textarea>\nx</textarea>", "x"},
		{"xmp does not", "<xmp>\nx</xmp>", "\nx"},
		{"an ordinary element does not", "<p>\nx</p>", "\nx"},
		{"a script does not", "<script>\nx</script>", "\nx"},
		{"the rule is the opening text, not any text inside", "<pre><b>\nx</b></pre>", "\nx"},
	} {
		got, err := ParsedText([]byte(tt.doc))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: %q gave %q, want %q", tt.name, tt.doc, got, tt.want)
		}
	}
}

// TestIsRawTextIsTheWrongPredicateForDecoding is the finding, stated as a test: the set of
// elements whose references do not decode is not the set lolhtml.IsRawText reports, and the
// difference is exactly textarea and title. A program that used IsRawText here would corrupt
// both.
func TestIsRawTextIsTheWrongPredicateForDecoding(t *testing.T) {
	var rawButDecodes []string
	for tag := range map[string]bool{
		"script": true, "style": true, "iframe": true, "noembed": true, "noframes": true,
		"noscript": true, "xmp": true, "plaintext": true, "textarea": true, "title": true,
	} {
		if !lolhtml.IsRawText(tag) {
			t.Errorf("IsRawText(%q) is false, so this list is not the raw-text list", tag)
			continue
		}
		if !noDecode(tag) {
			rawButDecodes = append(rawButDecodes, tag)
		}
	}
	if len(rawButDecodes) != 2 {
		t.Fatalf("raw-text elements whose references decode: %v, want exactly textarea and title",
			rawButDecodes)
	}
	for _, tag := range rawButDecodes {
		if tag != "textarea" && tag != "title" {
			t.Errorf("%q decodes references and is raw text, which is new", tag)
		}
	}

	// And the consequence, measured rather than argued: using IsRawText for the decode rule
	// leaves a title undecoded. The library's own predicate does not.
	got, err := ParsedText([]byte(`<title>a &amp; b</title>`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "a & b" {
		t.Errorf("a title's text is %q, want it decoded", got)
	}
	if using := convertWith(`a &amp; b`, "title", lolhtml.IsRawText); using == "a & b" {
		t.Error("keying the decode rule on IsRawText gave the same answer for a title, so " +
			"the two predicates are interchangeable here after all")
	}
}

// TestPlaintextRunsToTheEnd. Nothing closes a plaintext, so everything after it is its content
// and the no-decode rule has to stay in force to the end of the document.
func TestPlaintextRunsToTheEnd(t *testing.T) {
	got, err := ParsedText([]byte(`<p>a &amp; b<plaintext>c &amp; d</p>e &amp; f`))
	if err != nil {
		t.Fatal(err)
	}
	const want = "a & bc &amp; d</p>e &amp; f"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got[strings.Index(got, "c "):], " & ") {
		t.Error("something after the plaintext start tag was decoded")
	}
}

// TestTheEmptyChunkIsSkipped. Every text node ends with an empty chunk; it must not be treated
// as the opening text of a pre, or the newline rule would fire on the wrong chunk.
func TestTheEmptyChunkIsSkipped(t *testing.T) {
	got, err := ParsedText([]byte("<pre></pre>\nx"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "\nx" {
		t.Errorf("got %q, want the newline after the pre kept", got)
	}
}

// convertWith is convert with the decode decision taken by an arbitrary predicate, so the test
// above can show that the obvious wrong predicate gives a different answer.
func convertWith(text, in string, noDecodePred func(string) bool) string {
	if in == "" || !noDecodePred(in) {
		return unescape(text)
	}
	return text
}

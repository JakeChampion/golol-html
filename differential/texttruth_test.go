package differential

// Turning the text a rewrite reports into the text a parser reports.
//
// The four differences are already established: preprocess_test.go measures newline
// normalisation, the NUL rule and the leading newline inside a pre, and rawtext_test.go measures
// which elements decode character references. What is not established is that those lists are
// complete, because each is a hand-written table - the same gap that made the raw-text guard ship
// covering four elements out of ten, and the reason rawtext_test.go's
// TestTheGuardCoversEveryRawTextElement asks the parser about every element name instead of
// iterating the package's own list.
//
// So this file sweeps all of them across every element name there is, and adds the observation a
// caller needs: which of the three element-dependent rules the exported predicate answers.
//
//	CR and CRLF become LF          every element              no predicate needed
//	NUL becomes U+FFFD             the ten raw-text elements  lolhtml.IsRawText
//	references decode              all but eight              nothing: IsRawText minus two
//	one leading LF is dropped      pre, listing, textarea      nothing
//
// IsRawText answers the NUL rule exactly. It is the wrong predicate for the decode rule - the one
// that corrupts a stylesheet if you get it wrong - and the difference is exactly textarea and
// title. examples/gip/texttruth composes the four into the conversion itself.

import (
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// noDecode is the list examples/gip/texttruth carries. The test below is the reason it can be a
// literal: it asks the parser about every element name there is.
var noDecode = map[string]bool{
	"iframe": true, "noembed": true, "noframes": true, "noscript": true,
	"plaintext": true, "script": true, "style": true, "xmp": true,
}

// eatsLeadingNewline is the other list, which is not a raw-text set.
var eatsLeadingNewline = map[string]bool{"pre": true, "listing": true, "textarea": true}

// everyElementName is the HTML index of elements plus the obsolete ones a parser still knows,
// the same list rawtext_test.go uses to keep the raw-text guard honest.
var everyElementName = strings.Fields(`a abbr acronym address applet area article aside audio
b base basefont bdi bdo bgsound big blink blockquote body br button canvas caption center cite
code col colgroup data datalist dd del details dfn dialog dir div dl dt em embed fieldset
figcaption figure font footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html
i iframe image img input ins isindex kbd keygen label legend li link listing main map mark
marquee menu menuitem meta meter multicol nav nextid nobr noembed noframes noscript object ol
optgroup option output p param picture plaintext portal pre progress q rb rp rt rtc ruby s samp
script search section select selectedcontent shadow slot small source spacer span strike strong
style sub summary sup table tbody td template textarea tfoot th thead time title tr track tt u
ul var video wbr xmp`)

// parsedText is the parser's answer: every text node's data, concatenated.
func parsedText(t *testing.T, doc string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return b.String()
}

// TestTheDecodeListIsTheParsersList asks the parser, for every element name, whether a reference
// in its content is decoded, and compares that with the list the recipe carries. Both directions
// matter: a list that is too long loses the decoding somewhere it was wanted, and one that is
// too short corrupts a script or a stylesheet.
func TestTheDecodeListIsTheParsersList(t *testing.T) {
	checked := 0
	for _, tag := range everyElementName {
		text := parsedText(t, "<"+tag+">x&amp;y</"+tag+">")
		decoded := strings.Contains(text, "x&y")
		raw := strings.Contains(text, "x&amp;y")
		if !decoded && !raw {
			// frameset drops its content; there is no reference to ask about.
			continue
		}
		checked++
		if want := !noDecode[tag]; decoded != want {
			t.Errorf("<%s>: the parser %s the reference, and the recipe says it does %s",
				tag, map[bool]string{true: "decodes", false: "does not decode"}[decoded],
				map[bool]string{true: "not", false: ""}[!want])
		}
	}
	if checked < 140 {
		t.Errorf("only %d element names had a reference to ask about, which is too few for "+
			"this to be a sweep", checked)
	}
}

// TestTheDecodeListIsIsRawTextMinusTwo is the finding: the predicate the library exports is not
// the one this rule needs, and the difference is exactly textarea and title.
func TestTheDecodeListIsIsRawTextMinusTwo(t *testing.T) {
	var rawText, differ []string
	for _, tag := range everyElementName {
		if lolhtml.IsRawText(tag) {
			rawText = append(rawText, tag)
			if !noDecode[tag] {
				differ = append(differ, tag)
			}
		}
		if noDecode[tag] && !lolhtml.IsRawText(tag) {
			t.Errorf("<%s> does not decode references and IsRawText is false, so the decode "+
				"list is not a subset of the raw-text list", tag)
		}
	}
	if len(rawText) != 10 {
		t.Errorf("IsRawText is true for %d names (%v), want the documented ten",
			len(rawText), rawText)
	}
	if strings.Join(differ, " ") != "textarea title" {
		t.Errorf("raw-text elements that still decode: %v, want textarea and title", differ)
	}

	// And the consequence, measured against the parser: using IsRawText for this rule leaves
	// a title's references undecoded, where the parser decodes them.
	if got := parsedText(t, `<title>a &amp; b</title>`); got != "a & b" {
		t.Errorf("the parser's text for a title is %q, so the premise of this test is wrong", got)
	}
}

// TestTheNulRuleIsExactlyIsRawText - the one rule the exported predicate does answer.
func TestTheNulRuleIsExactlyIsRawText(t *testing.T) {
	for _, tag := range everyElementName {
		text := parsedText(t, "<"+tag+">a\x00b</"+tag+">")
		switch {
		case strings.Contains(text, "a�b"):
			if !lolhtml.IsRawText(tag) {
				t.Errorf("<%s>: NUL became U+FFFD and IsRawText is false", tag)
			}
		case strings.Contains(text, "ab"):
			if lolhtml.IsRawText(tag) {
				t.Errorf("<%s>: NUL was dropped and IsRawText is true", tag)
			}
		}
	}
}

// TestTheLeadingNewlineListIsTheParsersList. This one is not any raw-text set: textarea is raw
// text and pre and listing are not, and xmp is raw text and is not in it.
func TestTheLeadingNewlineListIsTheParsersList(t *testing.T) {
	for _, tag := range everyElementName {
		if tag == "html" || tag == "head" || tag == "body" || tag == "frameset" {
			// A newline in these lands where the tree builder moves or drops it for
			// reasons that have nothing to do with this rule.
			continue
		}
		withNewline := parsedText(t, "<"+tag+">\nx</"+tag+">")
		eaten := !strings.HasPrefix(withNewline, "\n") && strings.Contains(withNewline, "x")
		if want := eatsLeadingNewline[tag]; eaten != want {
			t.Errorf("<%s>: leading newline eaten: %v, want %v (text %q)",
				tag, eaten, want, withNewline)
		}
	}

	// A CRLF counts as the one newline, and only one goes.
	for _, tt := range []struct{ doc, want string }{
		{"<pre>\r\nx</pre>", "x"},
		{"<pre>\n\nx</pre>", "\nx"},
		{"<pre>\r\n\nx</pre>", "\nx"},
		{"<textarea>\r\nx</textarea>", "x"},
	} {
		if got := parsedText(t, tt.doc); got != tt.want {
			t.Errorf("%q: parser text %q, want %q", tt.doc, got, tt.want)
		}
	}
}

// TestCarriageReturnsAreNormalisedEverywhere, with no exception for raw text - the one rule that
// needs no element list at all.
func TestCarriageReturnsAreNormalisedEverywhere(t *testing.T) {
	for _, tag := range everyElementName {
		text := parsedText(t, "<"+tag+">a\r\nb\rc</"+tag+">")
		if strings.Contains(text, "\r") {
			t.Errorf("<%s>: a carriage return survived: %q", tag, text)
		}
	}
}

// TestTheRecipeAgreesWithTheParser is the whole thing end to end. The recipe is repeated here
// rather than imported, because examples/gip/texttruth is a main package; if the two ever drift,
// this is the one that is checked against a parser.
func TestTheRecipeAgreesWithTheParser(t *testing.T) {
	for _, doc := range []string{
		`<p>caf&eacute; &amp; more</p>`,
		`<p>&notit; &amp &unknown;</p>`,
		`<style>a{content:"&amp;"}</style>`,
		`<script>if (a &amp; b) {}</script>`,
		`<title>a &amp; b</title>`,
		`<textarea>a &amp; b</textarea>`,
		`<xmp>a &amp; b</xmp>`,
		"<pre>\nkept</pre>",
		"<pre>\n\nkept</pre>",
		"<textarea>\r\nx</textarea>",
		"<listing>\nx</listing>",
		"<p>a\r\nb\rc</p>",
		"<p>a\x00b</p>",
		"<script>a\x00b</script>",
		`<ul><li>a &amp; b<li>c</ul>`,
		`<div><p>one</p><p>two &lt;</p></div>`,
		`<p>a<!--c-->b</p>`,
		`<table>stray<tr><td>a &amp; b</table>`,
		`<p>text</p><script>var s = "</p>";</script>`,
		"<pre><b>\nx</b></pre>",
		`<p>a &amp; b<plaintext>c &amp; d</p>e &amp; f`,
		"<pre></pre>\nx",
		"<textarea></textarea>\nx",
	} {
		got, err := recipe(doc)
		if err != nil {
			t.Errorf("%q: %v", doc, err)
			continue
		}
		if want := parsedText(t, doc); got != want {
			t.Errorf("%q: the recipe gives %q, the parser %q", doc, got, want)
		}
	}
}

// recipe is examples/gip/texttruth's ParsedText, kept compact.
func recipe(doc string) (string, error) {
	var b strings.Builder
	var raw, pending string
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			name := e.TagName()
			if lolhtml.IsRawText(name) {
				raw = name
				if name != "plaintext" {
					if err := e.OnEndTag(func(*lolhtml.EndTag) error { raw = ""; return nil }); err != nil {
						return err
					}
				}
			}
			if !eatsLeadingNewline[name] {
				pending = ""
				return nil
			}
			pending = name
			return e.OnEndTag(func(*lolhtml.EndTag) error { pending = ""; return nil })
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			text := tc.Text()
			eat := pending != ""
			pending = ""
			if text == "" {
				return nil
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "\n")
			if eat {
				text = strings.TrimPrefix(text, "\n")
			}
			if raw == "" {
				b.WriteString(stdhtml.UnescapeString(strings.ReplaceAll(text, "\x00", "")))
				return nil
			}
			text = strings.ReplaceAll(text, "\x00", "�")
			if noDecode[raw] {
				b.WriteString(text)
			} else {
				b.WriteString(stdhtml.UnescapeString(text))
			}
			return nil
		}),
	)
	if err != nil {
		return "", err
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// TestTheStandardLibraryDecoderIsEnough. The example is in the root module, which has no
// dependencies, so it uses the standard library's decoder. That is only sound if it agrees with
// the parser's, including on the cases that distinguish decoders.
func TestTheStandardLibraryDecoderIsEnough(t *testing.T) {
	for _, s := range []string{
		"&amp;", "&lt;", "&notit;", "&amp", "&unknown;", "&#65;", "&#x42;", "&nots;", "&not",
		"&AMP;", "&amp;amp;", "&#0;", "&#x110000;", "a&b", "&times", "&timesbar;", "&lt&gt;",
		"&NotGreaterGreater;", "&CounterClockwiseContourIntegral;", "&#38;#38;",
	} {
		if got, want := stdhtml.UnescapeString(s), html.UnescapeString(s); got != want {
			t.Errorf("%q: standard library gives %q, x/net/html %q", s, got, want)
		}
	}
}

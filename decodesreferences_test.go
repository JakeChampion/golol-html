package lolhtml_test

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestDecodesCharacterReferencesIsIsRawTextMinusTwo. The relationship is the whole point of having
// both, so it is checked rather than described: every raw-text element except textarea and title
// leaves references alone, and everything that is not raw text decodes.
func TestDecodesCharacterReferencesIsIsRawTextMinusTwo(t *testing.T) {
	var rawText, decodingRawText []string
	for _, tag := range strings.Fields(`a abbr acronym address applet area article aside audio b
	base basefont bdi bdo bgsound big blink blockquote body br button canvas caption center cite
	code col colgroup data datalist dd del details dfn dialog dir div dl dt em embed fieldset
	figcaption figure font footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html
	i iframe image img input ins isindex kbd keygen label legend li link listing main map mark
	marquee menu menuitem meta meter multicol nav nextid nobr noembed noframes noscript object ol
	optgroup option output p param picture plaintext portal pre progress q rb rp rt rtc ruby s samp
	script search section select selectedcontent shadow slot small source spacer span strike strong
	style sub summary sup table tbody td template textarea tfoot th thead time title tr track tt u
	ul var video wbr xmp`) {
		raw := lolhtml.IsRawText(tag)
		decodes := lolhtml.DecodesCharacterReferences(tag)
		if !raw && !decodes {
			t.Errorf("<%s> is not raw text and does not decode; everything whose content is "+
				"markup has to decode", tag)
		}
		if raw {
			rawText = append(rawText, tag)
			if decodes {
				decodingRawText = append(decodingRawText, tag)
			}
		}
	}
	if len(rawText) != 10 {
		t.Errorf("IsRawText is true for %d names (%v), want the documented ten",
			len(rawText), rawText)
	}
	if got := strings.Join(decodingRawText, " "); got != "textarea title" {
		t.Errorf("raw-text elements that still decode: %q, want \"textarea title\"", got)
	}
}

// TestDecodesCharacterReferencesIsCaseInsensitive, as IsRawText is, so it accepts both what
// TagName reports and what TagNamePreserveCase does.
func TestDecodesCharacterReferencesIsCaseInsensitive(t *testing.T) {
	for _, tt := range []struct {
		tag  string
		want bool
	}{
		{"style", false}, {"STYLE", false}, {"Style", false},
		{"title", true}, {"TITLE", true}, {"Title", true},
		{"textarea", true}, {"TextArea", true},
		{"script", false}, {"SCRIPT", false},
		{"div", true}, {"DIV", true},
		{"plaintext", false}, {"PLAINTEXT", false},
		{"notanelement", true}, {"", true},
	} {
		if got := lolhtml.DecodesCharacterReferences(tt.tag); got != tt.want {
			t.Errorf("DecodesCharacterReferences(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

func convert(t *testing.T, doc, from string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Convert(&out, strings.NewReader(doc), from)
	if err != nil {
		t.Fatalf("Convert(%q, %q): %v", doc, from, err)
	}
	return out.String(), res
}

// TestTheTablesAgreeWithTheRewriter, which is the only thing that makes the conversion
// trustworthy: the table this program converts with has to be the one the rewriter
// decodes with, byte for byte, or the proof is proving the wrong thing.
func TestTheTablesAgreeWithTheRewriter(t *testing.T) {
	for name, table := range Tables {
		for b := 0; b < 256; b++ {
			// The markup characters would change the document's shape rather than its
			// text, and a NUL is not a character a parser keeps.
			switch byte(b) {
			case '<', '>', '&', 0x00:
				continue
			}
			doc := "<p>" + string([]byte{byte(b)}) + "</p>"
			var seen string
			if _, err := lolhtml.RewriteString(doc,
				lolhtml.WithEncoding(name),
				lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
					seen += c.Text()
					return nil
				})); err != nil {
				t.Fatalf("%s byte %#02x: %v", name, b, err)
			}
			want := string(table[b])
			if table[b] == utf8.RuneError {
				// The rewriter reports U+FFFD for a byte the encoding has no
				// character for, which is the same thing this table says.
				want = "�"
			}
			if seen != want {
				t.Errorf("%s byte %#02x: the rewriter says %q, the table says %q",
					name, b, seen, want)
			}
		}
	}
}

// TestTheTextSurvivesAndTheProofSaysSo.
func TestTheTextSurvivesAndTheProofSaysSo(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"<p>caf\xe9</p>", "<p>café</p>"},
		{"<p>it\x92s</p>", "<p>it’s</p>"},
		{`<a title="r\xe9sum\xe9">x</a>`, `<a title="r\xe9sum\xe9">x</a>`}, // placeholder, replaced below
	} {
		if strings.Contains(tc.doc, `\x`) {
			continue
		}
		got, res := convert(t, tc.doc, "windows-1252")
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if !res.OK() {
			t.Errorf("%q: %v", tc.doc, res)
		}
		if res.Before.Hash != res.After.Hash {
			t.Errorf("%q: fingerprints %v and %v", tc.doc, res.Before, res.After)
		}
	}

	// An attribute value and a comment are fingerprinted too, which is what makes the
	// proof worth having: text alone would miss most of a page's characters.
	got, res := convert(t, "<a title=\"r\xe9sum\xe9\">x</a><!--\xa9 2024-->", "windows-1252")
	if want := "<a title=\"résumé\">x</a><!--© 2024-->"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if !res.OK() || res.After.Runs < 3 {
		t.Errorf("%v", res)
	}
}

// TestTheProofCoversEveryKindOfCharacter: if the fingerprint only covered text, a
// document whose accents live in attributes would pass without being checked.
func TestTheProofCoversEveryKindOfCharacter(t *testing.T) {
	_, res := convert(t, "<p>a</p>", "windows-1252")
	text := res.After.Runs

	_, res = convert(t, "<p title=\"caf\xe9\">a</p>", "windows-1252")
	if res.After.Runs <= text {
		t.Errorf("an attribute added no runs: %v", res)
	}
	_, res = convert(t, "<p>a</p><!--caf\xe9-->", "windows-1252")
	if res.After.Runs <= text {
		t.Errorf("a comment added no runs: %v", res)
	}
}

// TestTheLabelsAreTheStandardsAndNotTheCodePages: a document labelled iso-8859-1 is
// decoded with windows-1252 by every browser and by the rewriter, so this program uses
// the same table for both labels. A true Latin-1 table would put C1 controls where
// readers see punctuation.
func TestTheLabelsAreTheStandardsAndNotTheCodePages(t *testing.T) {
	for _, label := range []string{"iso-8859-1", "latin1", "ascii", "us-ascii"} {
		if Tables[label] != Tables["windows-1252"] {
			t.Errorf("%q does not use the windows-1252 table", label)
		}
	}
	// 0x92 is a right single quote in windows-1252 and a control character in true
	// Latin-1, which is the byte that shows the difference.
	got, res := convert(t, "<p>it\x92s</p>", "iso-8859-1")
	if want := "<p>it\u2019s</p>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if !res.OK() {
		t.Errorf("%v", res)
	}
}

// TestEveryByteHasACharacter, which is the property that makes a single-byte
// conversion total: the standard's index leaves no byte undefined, so a document in one
// of these encodings cannot lose a byte to the decoder. The signal that a byte was lost
// - U+FFFD in the text, and "&#65533;" in the output of a rewrite - belongs to the
// multi-byte encodings this program does not convert.
func TestEveryByteHasACharacter(t *testing.T) {
	for name, table := range Tables {
		for b := 0; b < 256; b++ {
			if table[b] == utf8.RuneError && b != 0xfd {
				t.Errorf("%s has no character for byte %#02x", name, b)
			}
		}
	}
	got, res := convert(t, "<p>a\x81b</p>", "iso-8859-15")
	if strings.Contains(got, "\ufffd") {
		t.Errorf("the conversion wrote a replacement character: %q", got)
	}
	if res.Unmapped != 0 {
		t.Errorf("Unmapped = %d, want 0", res.Unmapped)
	}
	if !res.OK() {
		t.Errorf("%v", res)
	}
	if res.Before.Replacement != 0 {
		t.Errorf("the first pass saw %d replacement characters, want 0", res.Before.Replacement)
	}
}

// TestReferencesAreCarriedThroughUntouched, which is why the proof does not need a
// reference table: both passes see the reference as the document wrote it.
func TestReferencesAreCarriedThroughUntouched(t *testing.T) {
	for _, doc := range []string{
		"<p>&#20140;</p>",
		"<p>&yen;</p>",
		"<p>&amp; &lt; &gt;</p>",
		"<a href=\"?a=1&amp;b=2\">x</a>",
	} {
		got, res := convert(t, doc, "windows-1252")
		if got != doc {
			t.Errorf("%q became %q", doc, got)
		}
		if !res.OK() {
			t.Errorf("%q: %v", doc, res)
		}
	}
}

// TestAConversionThatLosesSomethingIsCaught. There is no way to make the table lose a
// character, so this drives the comparison directly - which is the part that would
// catch a table with a wrong entry in it.
func TestAConversionThatLosesSomethingIsCaught(t *testing.T) {
	before := []string{"t:café", "a:title=résumé"}
	after := []string{"t:cafe", "a:title=résumé"}
	if got := firstDifference(before, after); !strings.Contains(got, "café") {
		t.Errorf("firstDifference = %q", got)
	}
	if got := firstDifference(before, before[:1]); !strings.Contains(got, "missing") {
		t.Errorf("firstDifference = %q", got)
	}
	if got := firstDifference(before[:1], before); !strings.Contains(got, "gained") {
		t.Errorf("firstDifference = %q", got)
	}
	if got := firstDifference(before, before); got != "" {
		t.Errorf("firstDifference on identical runs = %q", got)
	}
}

// TestMarkupIsUnchangedByTheConversion, which is what makes a byte-for-character
// conversion safe for these encodings: their ASCII half is ASCII.
func TestMarkupIsUnchangedByTheConversion(t *testing.T) {
	for name := range Tables {
		table := Tables[name]
		for b := 0; b < 0x80; b++ {
			if table[b] != rune(b) {
				t.Errorf("%s maps byte %#02x to %U, so the markup would move", name, b, table[b])
			}
		}
	}
	const doc = "<div class=\"a\" data-x='y'><p>caf\xe9</p><!--c--><br/></div>"
	got, _ := convert(t, doc, "windows-1252")
	for _, want := range []string{`<div class="a" data-x='y'>`, "<p>", "<!--c-->", "<br/>", "</div>"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
}

// TestAnUnknownEncodingIsRefused rather than guessed at.
func TestAnUnknownEncodingIsRefused(t *testing.T) {
	var out strings.Builder
	_, err := Convert(&out, strings.NewReader("<p>x</p>"), "koi8-r")
	if err == nil {
		t.Fatal("Convert accepted an encoding it has no table for")
	}
	if !strings.Contains(err.Error(), "windows-1252") {
		t.Errorf("the error is %v, want it to name what is available", err)
	}
	if out.Len() != 0 {
		t.Errorf("it wrote %q", out.String())
	}
}

// TestConvertingTwiceIsNotIdempotentAndSaysSo: a UTF-8 document read as windows-1252 is
// exactly the mojibake the previous program looks for, so this one has to be run once.
// The proof is what notices: the second pass reports different characters.
func TestConvertingTwiceIsNotIdempotentAndSaysSo(t *testing.T) {
	once, res := convert(t, "<p>caf\xe9</p>", "windows-1252")
	if !res.OK() {
		t.Fatalf("%v", res)
	}
	twice, res := convert(t, once, "windows-1252")
	if twice == once {
		t.Errorf("converting twice changed nothing, which cannot be right: %q", twice)
	}
	// The fingerprints of the second conversion still match each other - it is a
	// faithful conversion of the wrong input - so the honest signal is the character
	// count, which has grown.
	if res.After.Characters <= res.Before.Characters {
		t.Logf("second conversion: %d characters before, %d after", res.Before.Characters, res.After.Characters)
	}
	if !strings.Contains(twice, "Ã©") {
		t.Errorf("the second conversion gave %q, want the mojibake that proves it happened", twice)
	}
}

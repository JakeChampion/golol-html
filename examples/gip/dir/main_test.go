package main

import (
	"io"
	"strings"
	"testing"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

const arabic = "مرحبا بالعالم"
const hebrew = "שלום עולם"
const english = "Hello world, this is English text."

func annotate(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Annotate(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Annotate(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheFirstStrongCharacterDecides, which is the rule and not a majority. Both
// directions of the same point: a mostly-English paragraph that starts with an
// Arabic word is right to left, and a mostly-Arabic one that starts with an English
// word is not.
func TestTheFirstStrongCharacterDecides(t *testing.T) {
	for _, tc := range []struct {
		what, text string
		marked     bool
	}{
		{"all Arabic", arabic, true},
		{"all Hebrew", hebrew, true},
		{"all English", english, false},
		{"Arabic first, then mostly English",
			"مرحبا this paragraph is mostly English words after that first one", true},
		{"English first, then mostly Arabic",
			"Hello " + arabic + " " + arabic + " " + arabic, false},
		// Everything before the first letter is skipped.
		{"digits and punctuation first", `2026 — "` + arabic + `"`, true},
		{"digits and punctuation first, English after", `2026 — "Hello"`, false},
		{"parentheses", "(" + hebrew + ")", true},
	} {
		doc := "<body><p>" + tc.text + "</p></body>"
		got, res := annotate(t, doc, Options{})
		if marked := strings.Contains(got, `dir="rtl"`); marked != tc.marked {
			t.Errorf("%s: marked = %v, want %v: %q (%v)", tc.what, marked, tc.marked, got, res)
		}
	}

	// And the case a majority rule gets wrong, measured rather than asserted: most
	// of the letters in that paragraph are Latin.
	mixed := "مرحبا this paragraph is mostly English words after that first one"
	latin, rtl := 0, 0
	for _, r := range mixed {
		switch strong(r) {
		case ltr:
			latin++
		case rtlDir:
			rtl++
		}
	}
	if latin <= rtl {
		t.Fatalf("%d Latin letters against %d right-to-left ones; this test needs the "+
			"Latin ones to be the majority to mean anything", latin, rtl)
	}
}

// rtlDir is the direction constant under a name that does not collide with the
// package's rtl value in a test that also talks about counts.
const rtlDir = rtl

// TestNoStrongCharacterMeansNothingToSay, because the direction is inherited and
// inheritance is the right answer for a number.
func TestNoStrongCharacterMeansNothingToSay(t *testing.T) {
	for _, text := range []string{"1234 5678", "— — —", "()", "  ", "2026-08-25", "+44 20 7946 0018"} {
		doc := "<body><p>" + text + "</p></body>"
		got, res := annotate(t, doc, Options{})
		if got != doc {
			t.Errorf("%q was rewritten to %q", text, got)
		}
		if res.NoEvidence == 0 {
			t.Errorf("%q: NoEvidence = 0", text)
		}
	}
}

// TestAutoDelegatesTheRuleToTheBrowser, which is the better answer where the text
// can change after the page is built.
func TestAutoDelegatesTheRuleToTheBrowser(t *testing.T) {
	doc := "<body><p>" + arabic + "</p><p>" + english + "</p></body>"
	got, res := annotate(t, doc, Options{Auto: true})
	if !strings.Contains(got, `dir="auto"`) || strings.Contains(got, `dir="rtl"`) {
		t.Errorf("got %q", got)
	}
	if res.Marked != 1 {
		t.Errorf("%v", res)
	}
	// The same elements are chosen either way: only the value differs.
	plain, _ := annotate(t, doc, Options{})
	if strings.ReplaceAll(plain, `dir="rtl"`, `dir="auto"`) != got {
		t.Errorf("\n auto %q\nplain %q", got, plain)
	}
}

// TestWhatTheDocumentSaidIsLeftAlone, in either direction: a page that says
// dir="ltr" on Arabic text has done something deliberate.
func TestWhatTheDocumentSaidIsLeftAlone(t *testing.T) {
	for _, doc := range []string{
		`<p dir="rtl">` + arabic + `</p>`,
		`<p dir="ltr">` + arabic + `</p>`,
		`<p dir="auto">` + arabic + `</p>`,
		`<p dir="">` + arabic + `</p>`,
	} {
		got, res := annotate(t, doc, Options{})
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Already != 1 {
			t.Errorf("%q: Already = %d, want 1", doc, res.Already)
		}
	}
}

// TestTheElementsItStaysOutOf. A bdo exists to override the algorithm, so a
// program implementing the algorithm has no business inside it.
func TestTheElementsItStaysOutOf(t *testing.T) {
	for _, tag := range []string{"code", "kbd", "samp", "var", "pre", "script", "style", "textarea"} {
		doc := "<" + tag + ">" + arabic + "</" + tag + ">"
		if got, _ := annotate(t, doc, Options{}); got != doc {
			t.Errorf("<%s> was rewritten to %q", tag, got)
		}
	}
	// Inside a bdo, including a nested element.
	doc := `<bdo dir="ltr"><span>` + arabic + `</span></bdo>`
	if got, _ := annotate(t, doc, Options{}); got != doc {
		t.Errorf("inside a bdo: %q", got)
	}
	// And a code inside a paragraph does not stop the paragraph being marked.
	doc = "<p>" + arabic + " <code>ls -la</code></p>"
	got, _ := annotate(t, doc, Options{})
	if !strings.Contains(got, `<p dir="rtl">`) || strings.Contains(got, `<code dir=`) {
		t.Errorf("got %q", got)
	}
}

// TestOwnTextRatherThanDescendants, so the paragraph is marked and not the body.
func TestOwnTextRatherThanDescendants(t *testing.T) {
	doc := "<body><div><section><p>" + arabic + "</p></section></div></body>"
	got, res := annotate(t, doc, Options{})
	if !strings.Contains(got, `<p dir="rtl">`) {
		t.Errorf("got %q", got)
	}
	for _, tag := range []string{"body", "div", "section"} {
		if strings.Contains(got, "<"+tag+" dir=") {
			t.Errorf("the <%s> was marked: %q", tag, got)
		}
	}
	if res.Marked != 1 {
		t.Errorf("%v", res)
	}
}

// TestANestedMarkSaysNothingNew, because dir is inherited.
func TestANestedMarkSaysNothingNew(t *testing.T) {
	doc := "<body><div>" + arabic + "<p>" + hebrew + "</p></div></body>"
	got, res := annotate(t, doc, Options{})
	if n := strings.Count(got, `dir="rtl"`); n != 1 {
		t.Errorf("%d marks, want 1: %q", n, got)
	}
	if !strings.Contains(got, `<div dir="rtl">`) {
		t.Errorf("the outer element should carry it: %q", got)
	}
	if res.Nested != 1 {
		t.Errorf("Nested = %d, want 1", res.Nested)
	}
}

// TestAnnotatingTwiceChangesNothing.
func TestAnnotatingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		"<body><p>" + english + "</p><p>" + arabic + "</p></body>",
		"<body><div>" + hebrew + "<p>" + hebrew + "</p></div></body>",
		"<body><p>1234</p></body>",
	} {
		once, _ := annotate(t, doc, Options{})
		twice, res := annotate(t, once, Options{})
		if twice != once {
			t.Errorf("\n once %q\ntwice %q", once, twice)
		}
		if res.Marked != 0 {
			t.Errorf("the second pass marked %d", res.Marked)
		}
	}
}

// TestTheFirstPassIsChunkInvariant, which is what makes the offsets identity.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := "<html><body><p>" + english + "</p><div><p>" + arabic + "</p></div>" +
		"<code>" + hebrew + "</code><p>2026 " + hebrew + "</p></body></html>"
	want, _, err := Scan([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{}
		w, err := lolhtml.NewWriter(io.Discard, s.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		got := s.decide()
		if len(got) != len(want) {
			t.Errorf("chunks of %d: %v, want %v", size, got, want)
			continue
		}
		for at := range want {
			if !got[at] {
				t.Errorf("chunks of %d: offset %d was not marked", size, at)
			}
		}
	}
}

// TestTheRTLScriptsAreTheRTLScripts, so the table cannot quietly lose one.
func TestTheRTLScriptsAreTheRTLScripts(t *testing.T) {
	for _, tc := range []struct {
		what string
		r    rune
		want direction
	}{
		{"Arabic", 'م', rtl},
		{"Hebrew", 'ש', rtl},
		{"Syriac", 'ܐ', rtl},
		{"Thaana", 'ހ', rtl},
		{"NKo", 'ߊ', rtl},
		{"Samaritan", 'ࠀ', rtl},
		{"Latin", 'a', ltr},
		{"Greek", 'α', ltr},
		{"Cyrillic", 'д', ltr},
		{"Han", '文', ltr},
		{"digit", '4', unknown},
		{"space", ' ', unknown},
		{"punctuation", '—', unknown},
		{"an isolate control", '⁦', unknown},
	} {
		if got := strong(tc.r); got != tc.want {
			t.Errorf("%s (%q): strong = %v, want %v", tc.what, tc.r, got, tc.want)
		}
	}
	// Every table in RTL has to be a real one, or a letter in it would be called
	// left to right.
	for i, table := range RTL {
		if table == nil || len(table.R16)+len(table.R32) == 0 {
			t.Errorf("RTL[%d] is empty", i)
		}
	}
	// And a letter this program calls right to left has to be a letter.
	if !unicode.IsLetter('م') {
		t.Error("the classification rests on unicode.IsLetter")
	}
}

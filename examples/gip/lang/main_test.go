package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func annotate(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Annotate(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Annotate(%q): %v", doc, err)
	}
	return out.String(), res
}

const english = "This is an English paragraph with plenty of words in it."
const russian = "Здравствуйте, это довольно длинный русский текст."
const greek = "Καλημέρα κόσμε, τι κάνεις σήμερα το πρωί;"
const japanese = "これは日本語の文章です。もう少し長くしておきます。"
const arabic = "هذه فقرة عربية طويلة بما فيه الكفاية."

// TestTheElementInAnotherScriptIsMarked, and the rest of the page is not.
func TestTheElementInAnotherScriptIsMarked(t *testing.T) {
	doc := "<html><body><p>" + english + "</p><p>" + russian + "</p></body></html>"
	got, res := annotate(t, doc, Options{})
	if !strings.Contains(got, `<p lang="und-Cyrl">`+russian) {
		t.Errorf("the Russian paragraph is not marked: %q", got)
	}
	if strings.Contains(got, `<p lang="und-Latn">`) {
		t.Errorf("the English paragraph was marked: %q", got)
	}
	if res.Document != "Latn" || res.Marked["und-Cyrl"] != 1 {
		t.Errorf("%v", res)
	}
}

// TestTheScriptsAreTold apart, one element each.
func TestTheScriptsAreTold(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{russian, "und-Cyrl"},
		{greek, "und-Grek"},
		{arabic, "und-Arab"},
		{"这是一段足够长的中文文字，用来测试。", "und-Hani"},
		{"이것은 충분히 긴 한국어 문장입니다.", "und-Hang"},
		{"यह एक पर्याप्त लंबा हिंदी वाक्य है।", "und-Deva"},
		{"นี่คือประโยคภาษาไทยที่ยาวพอ", "und-Thai"},
	} {
		doc := "<body><p>" + english + "</p><p>" + tc.text + "</p></body>"
		got, res := annotate(t, doc, Options{})
		if !strings.Contains(got, `lang="`+tc.want+`"`) {
			t.Errorf("%q was not marked %q: %q (%v)", tc.text, tc.want, got, res)
		}
	}
}

// TestALanguageMapReplacesTheScriptSpelling, for a caller who knows which language
// the script is being used for. A script is not a language, so the default says
// only what it knows.
func TestALanguageMapReplacesTheScriptSpelling(t *testing.T) {
	doc := "<body><p>" + english + "</p><p>" + russian + "</p></body>"
	got, res := annotate(t, doc, Options{Language: map[string]string{"Cyrl": "ru"}})
	if !strings.Contains(got, `lang="ru"`) || strings.Contains(got, "und-Cyrl") {
		t.Errorf("got %q", got)
	}
	if res.Marked["ru"] != 1 {
		t.Errorf("%v", res)
	}
	// A mapping for a script the page does not use changes nothing.
	got2, _ := annotate(t, doc, Options{Language: map[string]string{"Thai": "th"}})
	if !strings.Contains(got2, "und-Cyrl") {
		t.Errorf("got %q", got2)
	}
}

// TestTheInnermostElementGetsIt, not the ancestor that happens to contain it.
func TestTheInnermostElementGetsIt(t *testing.T) {
	doc := "<body><p>" + english + "</p><div><section><p>" + russian + "</p></section></div></body>"
	got, res := annotate(t, doc, Options{})
	if !strings.Contains(got, `<p lang="und-Cyrl">`) {
		t.Errorf("the paragraph is not marked: %q", got)
	}
	for _, tag := range []string{"div", "section", "body"} {
		if strings.Contains(got, "<"+tag+" lang=") {
			t.Errorf("the <%s> was marked: %q", tag, got)
		}
	}
	if res.Marked["und-Cyrl"] != 1 {
		t.Errorf("%v", res)
	}
}

// TestANestedMarkThatSaysTheSameThingIsPruned: an element inside another with the
// same script adds nothing, and two attributes where one would do is noise in
// every diff of the page for ever.
func TestANestedMarkThatSaysTheSameThingIsPruned(t *testing.T) {
	// The div has its own Russian text as well as a Russian paragraph inside it.
	// The document's script is given rather than counted, because this test is
	// about the pruning and a page that is mostly Russian would mark the English.
	doc := "<body><p>" + english + "</p><div>" + russian + "<p>" + russian + "</p></div></body>"
	got, res := annotate(t, doc, Options{DocumentScript: "Latn"})
	if n := strings.Count(got, `lang="und-Cyrl"`); n != 1 {
		t.Errorf("%d marks, want 1: %q", n, got)
	}
	if !strings.Contains(got, `<div lang="und-Cyrl">`) {
		t.Errorf("the outer element should carry it: %q", got)
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", res.Pruned)
	}
	// A different script inside is not pruned.
	doc = "<body><p>" + english + "</p><div>" + russian + "<p>" + greek + "</p></div></body>"
	got, res = annotate(t, doc, Options{DocumentScript: "Latn"})
	if !strings.Contains(got, `<div lang="und-Cyrl">`) || !strings.Contains(got, `<p lang="und-Grek">`) {
		t.Errorf("got %q (%v)", got, res)
	}
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0", res.Pruned)
	}
}

// TestTooLittleTextIsNotEvidence, and a mixture is not either.
func TestTooLittleTextIsNotEvidence(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"one short word", "<body><p>" + english + "</p><p>да</p></body>"},
		{"a quotation inside a sentence",
			"<body><p>" + english + "</p><p>He said привет and left the room quietly.</p></body>"},
		{"no letters at all", "<body><p>" + english + "</p><p>12345 -- 67890</p></body>"},
	} {
		got, _ := annotate(t, tc.doc, Options{})
		if strings.Contains(got, "lang=") {
			t.Errorf("%s: something was marked: %q", tc.what, got)
		}
	}
	// A genuine mixture with enough letters is counted rather than guessed at.
	doc := "<body><p>" + english + "</p><p>Half English words here плюс половина русских слов</p></body>"
	got, res := annotate(t, doc, Options{})
	if strings.Contains(got, "lang=") {
		t.Errorf("a mixed element was marked: %q", got)
	}
	if res.Mixed != 1 {
		t.Errorf("Mixed = %d, want 1", res.Mixed)
	}
}

// TestWhatIsNotProseIsNotEvidence.
func TestWhatIsNotProseIsNotEvidence(t *testing.T) {
	for _, tag := range []string{"code", "kbd", "samp", "var", "pre", "script", "style", "textarea"} {
		doc := "<body><p>" + english + "</p><" + tag + ">" + russian + "</" + tag + "></body>"
		got, res := annotate(t, doc, Options{})
		if strings.Contains(got, "lang=") {
			t.Errorf("<%s>: something was marked: %q", tag, got)
		}
		if res.Regions == 0 {
			t.Errorf("<%s>: no region counted", tag)
		}
	}
}

// TestAnElementThatAlreadySaysSoIsLeftAlone, which is also what makes a second
// pass over the output a no-op.
func TestAnElementThatAlreadySaysSoIsLeftAlone(t *testing.T) {
	doc := `<body><p>` + english + `</p><p lang="uk">` + russian + `</p></body>`
	got, res := annotate(t, doc, Options{})
	if got != doc {
		t.Errorf("\n got %q\nwant it unchanged", got)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
}

// TestAnnotatingTwiceChangesNothing.
func TestAnnotatingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		"<html><body><p>" + english + "</p><p>" + russian + "</p></body></html>",
		"<body><p>" + japanese + "</p></body>",
		"<body><p>" + english + "</p><div>" + greek + "<p>" + greek + "</p></div></body>",
	} {
		once, _ := annotate(t, doc, Options{})
		twice, res := annotate(t, once, Options{})
		if twice != once {
			t.Errorf("\n once %q\ntwice %q", once, twice)
		}
		if len(res.Marked) != 0 {
			t.Errorf("the second pass marked %v", res.Marked)
		}
	}
}

// TestTheDocumentScriptIsWhatThePageMostlySays, so a Russian page marks its English
// aside rather than the other way round.
func TestTheDocumentScriptIsWhatThePageMostlySays(t *testing.T) {
	doc := "<body><p>" + russian + "</p><p>" + russian + "</p><p>" + english + "</p></body>"
	got, res := annotate(t, doc, Options{})
	if res.Document != "Cyrl" {
		t.Errorf("Document = %q, want Cyrl", res.Document)
	}
	if !strings.Contains(got, `<p lang="und-Latn">`+english) {
		t.Errorf("the English paragraph is not marked: %q", got)
	}
	if strings.Contains(got, "und-Cyrl") {
		t.Errorf("a Russian paragraph was marked in a Russian document: %q", got)
	}
	// And a caller who knows can say so, which changes the answer.
	got, res = annotate(t, doc, Options{DocumentScript: "Latn"})
	if res.Document != "Latn" || strings.Count(got, "und-Cyrl") != 2 {
		t.Errorf("%v: got %q", res, got)
	}
}

// TestTheOffsetsAreOnlyMeaningfulForTheSameBytes. The two passes are joined by
// byte offsets, so feeding the second pass a different document does not
// misplace an attribute - it silently places none, which is why the program
// buffers its input instead of reading it twice from anywhere.
func TestTheOffsetsAreOnlyMeaningfulForTheSameBytes(t *testing.T) {
	first := []byte("<body><p>" + english + "</p><p>" + russian + "</p></body>")
	marks, res, err := Scan(first, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Fatalf("the first pass found %d marks, want 1", len(marks))
	}

	// The same document with one byte more in front of it: every offset is off by
	// one, and every lookup misses.
	second := append([]byte(" "), first...)
	var out strings.Builder
	if err := Apply(&out, second, marks, &res); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "lang=") {
		t.Errorf("an attribute was placed from stale offsets: %q", out.String())
	}
	if out.String() != string(second) {
		t.Errorf("the document changed: %q", out.String())
	}

	// And on the bytes it was measured against, it lands.
	out.Reset()
	if err := Apply(&out, first, marks, &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `lang="und-Cyrl"`) {
		t.Errorf("got %q", out.String())
	}
}

// TestTheFirstPassIsChunkInvariant, which is what makes the offsets identity: they
// are absolute, so how the document was written in cannot change them.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := "<html><body><p>" + english + "</p><div><p>" + russian + "</p></div>" +
		"<code>" + greek + "</code><p>" + japanese + "</p></body></html>"
	want, _, err := Scan([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{res: Result{Marked: map[string]int{}}, counts: map[int]map[string]int{}}
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
		for at, value := range want {
			if got[at] != value {
				t.Errorf("chunks of %d: offset %d is %q, want %q", size, at, got[at], value)
			}
		}
	}
}

// TestAWritingSystemCanBeMoreThanOneScript, which is the case a majority rule over
// scripts alone gets wrong: no single script holds a Japanese sentence, so counting
// scripts would call it mixed and say nothing.
func TestAWritingSystemCanBeMoreThanOneScript(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{japanese, "und-Jpan"},
		{"漢字とひらがなとカタカナが混ざる文章です。", "und-Jpan"},
		// Han with no kana is not Japanese.
		{"这是一段足够长的中文文字，用来测试。", "und-Hani"},
		// Hangul with Han is Korean; the subtag says the combination.
		{"이것은 한국어 문장입니다 漢字도 조금 있습니다.", "und-Kore"},
		{"이것은 충분히 긴 한국어 문장입니다.", "und-Hang"},
	} {
		doc := "<body><p>" + english + "</p><p>" + tc.text + "</p></body>"
		got, res := annotate(t, doc, Options{})
		if !strings.Contains(got, `lang="`+tc.want+`"`) {
			t.Errorf("%q\n got %q\nwant %s (%v)", tc.text, got, tc.want, res)
		}
	}
	// And a caller who knows can say ja, which is the answer a reader wants.
	doc := "<body><p>" + english + "</p><p>" + japanese + "</p></body>"
	got, _ := annotate(t, doc, Options{Language: map[string]string{"Jpan": "ja"}})
	if !strings.Contains(got, `lang="ja"`) {
		t.Errorf("got %q", got)
	}
}

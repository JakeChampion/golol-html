package main

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// mangle turns text into the mojibake a UTF-8 document read as windows-1252 produces.
func mangle(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		// Every byte has a windows-1252 character, which is what makes this
		// mis-decoding silent: the table in the program is the one used here.
		b.WriteRune(cp1252[s[i]])
	}
	return b.String()
}

func find(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Find(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Find(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestItFindsTheClassicMojibakeAndReconstructsIt.
func TestItFindsTheClassicMojibakeAndReconstructsIt(t *testing.T) {
	for _, original := range []string{
		"It\u2019s",                            // a right single quote
		"caf\u00e9",                            // an acute e
		"a \u2014 b",                           // an em dash
		"\u201cquoted\u201d",                   // curly quotes
		"Pr\u00fcfung",                         // an umlaut
		"\u041c\u043e\u0441\u043a\u0432\u0430", // Cyrillic
		"\u4eac\u90fd",                         // CJK, three bytes each
		"\U0001f600",                           // an emoji, four bytes
	} {
		moji := mangle(t, original)
		doc := "<p>" + moji + "</p>"
		_, res := find(t, doc, Options{})
		if res.Runs == 0 {
			t.Errorf("%q (as %q) was not detected", original, moji)
			continue
		}
		fixed, res := find(t, doc, Options{Fix: true})
		if want := "<p>" + original + "</p>"; fixed != want {
			t.Errorf("%q\n got %q\nwant %q", moji, fixed, want)
		}
		if res.Fixed == 0 {
			t.Errorf("%q: %v", moji, res)
		}
	}
}

// TestOrdinaryTextIsNotTouched, which is what makes the detector usable: the pairs it
// looks for do not occur in ordinary writing, and a run that does not survive the round
// trip is not reported.
func TestOrdinaryTextIsNotTouched(t *testing.T) {
	for _, s := range []string{
		"plain ascii text",
		"caf\u00e9 na\u00efve r\u00e9sum\u00e9", // real accents
		"\u041c\u043e\u0441\u043a\u0432\u0430",  // real Cyrillic
		"\u4eac\u90fd \u6771\u4eac",             // real CJK
		"\u00c3 alone",                          // a lead character with nothing after it
		"\u00c3\u00c3\u00c3",                    // leads with no continuation
		"A\u00c9I\u00d3U",                       // accented capitals
		"1 \u00d7 2 = 2",                        // a multiplication sign
		"\u00a9 2024",                           // a copyright sign
		"\U0001f600 emoji",                      // a real emoji
	} {
		doc := "<p>" + s + "</p>"
		out, res := find(t, doc, Options{Fix: true})
		if out != doc {
			t.Errorf("%q was rewritten to %q", s, out)
		}
		if res.Runs != 0 {
			t.Errorf("%q: %d runs, %v", s, res.Runs, res.Findings)
		}
	}
}

// TestTheRoundTripIsTheTest: reconstruct only reports success when the bytes it rebuilds
// are valid UTF-8, which is the whole safety argument.
func TestTheRoundTripIsTheTest(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{mangle(t, "caf\u00e9"), true},
		{mangle(t, "It\u2019s"), true},
		{"\u00c3", false},       // one lead byte is not a sequence
		{"\u00c3\u00c3", false}, // two leads are not a sequence either
		{"plain", true},         // ASCII rebuilds to itself
		{"\u4eac", false},       // a CJK character is not in windows-1252
	} {
		out, ok := reconstruct(tc.in)
		if ok != tc.ok {
			t.Errorf("reconstruct(%q) = %q, %v, want ok = %v", tc.in, out, ok, tc.ok)
		}
		if ok && !utf8.ValidString(out) {
			t.Errorf("reconstruct(%q) gave invalid UTF-8 %q", tc.in, out)
		}
	}
}

// TestBytesThatAreNotUTF8AreADifferentDiagnosis: not mojibake, a wrong declaration -
// and the program says so rather than guessing.
func TestBytesThatAreNotUTF8AreADifferentDiagnosis(t *testing.T) {
	const bad = "\x92" // a windows-1252 right single quote, invalid UTF-8
	doc := "<p>it" + bad + "s</p>"
	_, res := find(t, doc, Options{})
	if res.Invalid != 1 {
		t.Fatalf("%v", res)
	}
	if res.Runs != 0 {
		t.Errorf("counted as mojibake: %v", res)
	}
	if e := res.Encoding(); !strings.Contains(e, "declared as utf-8") {
		t.Errorf("Encoding() = %q", e)
	}
	if res.OK() {
		t.Error("OK() is true for a document whose text is not what it says")
	}
}

// TestReportingWritesNothing, which is the point: reading the text of a mis-declared
// document is enough to change it, so the diagnosis is not the pass that copies.
func TestReportingWritesNothing(t *testing.T) {
	const bad = "\x92"
	doc := "<p>it" + bad + "s</p>"

	out, res := find(t, doc, Options{})
	if out != "" {
		t.Errorf("the reporter wrote %q, want nothing", out)
	}
	if res.Invalid != 1 {
		t.Errorf("%v", res)
	}

	// What the copy would have cost: the same handlers with a real destination give
	// back U+FFFD where the document had a byte.
	f := &finder{opts: Options{Fix: true}}
	var copied strings.Builder
	w, err := lolhtml.NewWriter(&copied, f.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if copied.String() == doc {
		t.Skip("the library now preserves invalid bytes through a text handler")
	}
	if !strings.Contains(copied.String(), "\ufffd") {
		t.Errorf("the copy holds %q, want the replacement character", copied.String())
	}
}

// TestRunsAreCountedAndOrdered, since one mis-decoded quote on every line is one
// problem rather than forty.
func TestRunsAreCountedAndOrdered(t *testing.T) {
	moji := mangle(t, "It\u2019s")
	acute := mangle(t, "caf\u00e9")
	doc := "<p>" + moji + "</p><p>" + moji + "</p><p>" + acute + "</p>"
	_, res := find(t, doc, Options{})
	if res.Runs != 3 {
		t.Errorf("%v", res)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("%v", res.Findings)
	}
	if res.Findings[0].Count != 2 {
		t.Errorf("the commonest run is %v, want the one appearing twice", res.Findings[0])
	}
}

// TestMinLengthTradesRecallForCertainty.
func TestMinLengthTradesRecallForCertainty(t *testing.T) {
	acute := mangle(t, "caf\u00e9") // two mis-decoded characters
	dash := mangle(t, "a \u2014 b") // three
	doc := "<p>" + acute + " " + dash + "</p>"

	_, res := find(t, doc, Options{})
	if res.Runs != 2 {
		t.Errorf("%v", res)
	}
	_, res = find(t, doc, Options{Min: 3})
	if res.Runs != 1 {
		t.Errorf("with -min 3: %v", res)
	}
}

// TestFixingTwiceChangesNothing, which is a real risk here: a reconstruction that was
// itself mojibake-shaped would be mangled again.
func TestFixingTwiceChangesNothing(t *testing.T) {
	for _, original := range []string{"It\u2019s a caf\u00e9", "a \u2014 b", "\u041c\u043e\u0441\u043a\u0432\u0430"} {
		doc := "<p>" + mangle(t, original) + "</p>"
		once, _ := find(t, doc, Options{Fix: true})
		twice, res := find(t, once, Options{Fix: true})
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", original, once, twice)
		}
		if res.Runs != 0 {
			t.Errorf("%q: the second pass found %d runs", original, res.Runs)
		}
	}
}

// TestTheReportSurvivesChunkBoundaries, including a mojibake run split across writes.
func TestTheReportSurvivesChunkBoundaries(t *testing.T) {
	doc := "<p>" + mangle(t, "It\u2019s a caf\u00e9 \u2014 nice") + "</p><p>plain</p>"
	want, wantRes := find(t, doc, Options{Fix: true})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		f := &finder{opts: Options{Fix: true}}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, f.options()...)
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
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if got := f.report(); got.Runs != wantRes.Runs {
			t.Errorf("chunks of %d: %d runs, want %d", size, got.Runs, wantRes.Runs)
		}
	}
}

// TestNothingIsWrittenWhenReporting is the io.Discard half, asserted through the public
// entry point so the program cannot quietly start copying.
func TestNothingIsWrittenWhenReporting(t *testing.T) {
	doc := "<p>" + mangle(t, "caf\u00e9") + "</p>"
	var out strings.Builder
	if _, err := Find(&out, strings.NewReader(doc), Options{}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("the reporter wrote %q", out.String())
	}
	// And with -fix it writes the whole document, not only the runs.
	out.Reset()
	if _, err := Find(&out, strings.NewReader(doc+"<p>tail</p>"), Options{Fix: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<p>tail</p>") {
		t.Errorf("the fixer wrote %q", out.String())
	}
	_ = io.Discard
}

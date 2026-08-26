package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const (
	bom8  = "\xef\xbb\xbf"
	bom16 = "\xff\xfe"
	// The same document: a UTF-8 BOM, then "café" with a two-byte e-acute.
	doc = bom8 + "<p>caf\xc3\xa9</p>"
)

func run(t *testing.T, in, declared string) (string, Detected, Text) {
	t.Helper()
	var out bytes.Buffer
	det, text, err := Run(strings.NewReader(in), &out, declared)
	if err != nil {
		t.Fatalf("%q declared %q: %v", in, declared, err)
	}
	return out.String(), det, text
}

// TestTheMarkOutranksTheDeclaredLabel is the finding. Declared windows-1252, a document with a
// UTF-8 BOM has to be read as UTF-8, because that is what a browser does.
func TestTheMarkOutranksTheDeclaredLabel(t *testing.T) {
	for _, declared := range []string{"windows-1252", "shift_jis", "utf-8", ""} {
		_, det, text := run(t, doc, declared)
		if det.Encoding != "utf-8" || !det.FromMark {
			t.Errorf("declared %q: decoded as %q from the mark %v, want utf-8 from the mark",
				declared, det.Encoding, det.FromMark)
		}
		if text.Body != "café" {
			t.Errorf("declared %q: text %q, want %q", declared, text.Body, "café")
		}
	}

	// With no mark, the declared label is used and nothing is sniffed.
	_, det, _ := run(t, "<p>caf\xe9</p>", "windows-1252")
	if det.FromMark || det.Encoding != "windows-1252" {
		t.Errorf("without a mark: %+v, want the declared label", det)
	}
}

// TestWhatTheLibraryDoesOnItsOwn is the other half, and the reason the program exists: handed the
// declared label directly, the handlers are given the wrong characters and nothing errors.
func TestWhatTheLibraryDoesOnItsOwn(t *testing.T) {
	read := func(encoding string) string {
		var text strings.Builder
		var out bytes.Buffer
		w, err := lolhtml.NewWriter(&out,
			lolhtml.WithEncoding(encoding),
			lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
				text.WriteString(tc.Text())
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return text.String()
	}

	if got, want := read("utf-8"), "\ufeffcafé"; got != want {
		t.Errorf("as utf-8 the handlers see %q, want %q - the mark included, since it is "+
			"reported as text", got, want)
	}
	if got, want := read("windows-1252"), "ï»¿cafÃ©"; got != want {
		t.Errorf("as windows-1252 the handlers see %q, want %q", got, want)
	}
	// Which is the whole point: two readings of one document, and the library takes the
	// caller's word for which.
	if read("utf-8") == read("windows-1252") {
		t.Error("both labels gave the same text, so the divergence this program fixes does " +
			"not exist")
	}
}

// TestTheMarkIsReportedAsText, at the front of the document, with a range over its own bytes -
// which is what a program accumulating text has to know.
func TestTheMarkIsReportedAsText(t *testing.T) {
	var chunks []string
	var first lolhtml.SourceLocation
	seen := false
	if _, err := lolhtml.Rewrite([]byte(doc), lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
		if tc.Text() == "" {
			return nil
		}
		if !seen {
			first = tc.SourceLocation()
			seen = true
		}
		chunks = append(chunks, tc.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("no text reported")
	}
	if chunks[0] != "\ufeff" {
		t.Errorf("the first text chunk is %q, want the mark", chunks[0])
	}
	if first.Start != 0 || first.End != 3 {
		t.Errorf("the mark's range is %v, want 0..3 - its own three bytes", first)
	}
}

// TestTheOutputKeepsTheMark. Stripping it would change what the next consumer sniffs, so the
// bytes are passed through even though the text is not.
func TestTheOutputKeepsTheMark(t *testing.T) {
	out, _, text := run(t, doc, "windows-1252")
	if !strings.HasPrefix(out, bom8) {
		t.Errorf("the output lost the mark: %q", out)
	}
	if out != doc {
		t.Errorf("the output is %q, want the input unchanged", out)
	}
	if strings.HasPrefix(text.Body, "\ufeff") {
		t.Errorf("the text kept the mark: %q", text.Body)
	}
	if !text.MarkDropped {
		t.Error("MarkDropped is false, so a caller cannot tell the mark was removed")
	}
}

// TestUtf16IsRefused. The rewriter cannot process it - no selector matches its markup, and a text
// handler turns every byte that is not valid UTF-8 into U+FFFD - so the honest answer is an error.
func TestUtf16IsRefused(t *testing.T) {
	for _, mark := range []string{bom16, "\xfe\xff"} {
		var out bytes.Buffer
		_, _, err := Run(strings.NewReader(mark+"<p>x</p>"), &out, "windows-1252")
		if !errors.Is(err, ErrUnsupportedEncoding) {
			t.Errorf("%q: error %v, want ErrUnsupportedEncoding", mark, err)
		}
		if out.Len() != 0 {
			t.Errorf("%q: wrote %q before refusing", mark, out.String())
		}
	}

	// And the reason, measured: rewriting it anyway corrupts the mark.
	got, err := lolhtml.Rewrite([]byte(bom16+"<p>x</p>"),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(got, []byte(bom16)) {
		t.Error("the mark survived a text-handler pass, so refusing is unnecessary")
	}
}

// TestDetectIsAPrefixTest, with the cases that would break a naive one: a mark in the middle is
// not a mark, and a two-byte mark must not be found inside the three-byte one.
func TestDetectIsAPrefixTest(t *testing.T) {
	for _, tt := range []struct {
		in       string
		encoding string
		fromMark bool
	}{
		{bom8 + "<p>x", "utf-8", true},
		{bom16 + "<p>x", "utf-16le", true},
		{"\xfe\xff<p>x", "utf-16be", true},
		{"<p>a" + bom8 + "b", "windows-1252", false},
		{"", "windows-1252", false},
		{"\xef\xbb", "windows-1252", false},
		{"\xef", "windows-1252", false},
	} {
		got := Detect([]byte(tt.in), "windows-1252")
		if got.Encoding != tt.encoding || got.FromMark != tt.fromMark {
			t.Errorf("%q: %+v, want %s from-mark=%v", tt.in, got, tt.encoding, tt.fromMark)
		}
	}
	// No declaration means UTF-8, which is the library's own default.
	if got := Detect([]byte("<p>x"), ""); got.Encoding != "utf-8" {
		t.Errorf("no declaration gave %q, want utf-8", got.Encoding)
	}
}

// TestTwoMarksLeaveTheSecondAsText. Only the first is a mark; a second is a zero-width no-break
// space in the content, and a browser shows it.
func TestTwoMarksLeaveTheSecondAsText(t *testing.T) {
	_, det, text := run(t, bom8+bom8+"<p>x</p>", "windows-1252")
	if !det.FromMark || det.Encoding != "utf-8" {
		t.Errorf("%+v, want utf-8 from the mark", det)
	}
	if want := "\ufeffx"; text.Body != want {
		t.Errorf("text %q, want %q - one mark removed, the second is content", text.Body, want)
	}
}

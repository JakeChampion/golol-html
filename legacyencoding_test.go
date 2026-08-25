package lolhtml_test

// What a legacy encoding does on the way through, and what it will not do.
//
// The rewriter decodes with the document's encoding and encodes back with the same
// one: there is no output-encoding option, and replacing every unit with itself does
// not change that. So a rewrite cannot convert a document from one encoding to
// another, and a program that has to convert must transcode the bytes itself - using
// the rewriter as the thing that proves the text survived, which is what
// examples/gip/reencode does.
//
// The other half is what happens to a byte the decoder cannot use. In a document
// declared UTF-8 it comes back as the three bytes of U+FFFD. In a legacy encoding it
// comes back as "&#65533;" - a reference, in the text, seven bytes where the document
// had one - because U+FFFD is not in the target repertoire and a reference is the
// fallback for that. Same failure, two shapes.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// legacyDocs are documents in encodings whose bytes are not UTF-8.
var legacyDocs = []struct {
	name, encoding, doc, text string
}{
	{"windows-1252", "windows-1252", "<p>caf\xe9</p>", "café"},
	{"windows-1252 punctuation", "windows-1252", "<p>it\x92s</p>", "it’s"},
	{"iso-8859-2", "iso-8859-2", "<p>\xe8\xed</p>", "čí"},
	{"shift_jis", "shift_jis", "<p>\x8b\x9e\x93s</p>", "京都"},
	{"euc-jp", "euc-jp", "<p>\xb5\xfe\xc5\xd4</p>", "京都"},
	{"gbk", "gbk", "<p>\xb1\xb1\xbe\xa9</p>", "北京"},
}

// TestALegacyDocumentRoundTripsByteForByte, with and without a text handler: the
// decoding is exact and so is the encoding back.
func TestALegacyDocumentRoundTripsByteForByte(t *testing.T) {
	for _, tc := range legacyDocs {
		out, err := lolhtml.RewriteString(tc.doc, lolhtml.WithEncoding(tc.encoding))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if out != tc.doc {
			t.Errorf("%s with no handlers: % x, want % x", tc.name, out, tc.doc)
		}

		var seen strings.Builder
		out, err = lolhtml.RewriteString(tc.doc,
			lolhtml.WithEncoding(tc.encoding),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				seen.WriteString(c.Text())
				return nil
			}))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if seen.String() != tc.text {
			t.Errorf("%s: the handler saw %q, want %q", tc.name, seen.String(), tc.text)
		}
		if out != tc.doc {
			t.Errorf("%s with a text handler: % x, want % x", tc.name, out, tc.doc)
		}
	}
}

// TestReplacingEveryUnitWithItselfDoesNotConvertTheDocument, which is the thing to
// know before trying: the output is in the document's encoding whatever the handlers
// do, because a handler's UTF-8 is encoded on the way out.
func TestReplacingEveryUnitWithItselfDoesNotConvertTheDocument(t *testing.T) {
	for _, tc := range legacyDocs {
		out, err := lolhtml.RewriteString(tc.doc,
			lolhtml.WithEncoding(tc.encoding),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				if c.IsLastInTextNode() {
					return nil
				}
				return c.Replace(c.Text(), lolhtml.Text)
			}),
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				for _, a := range e.AttributeList() {
					if err := e.SetAttribute(a.NamePreserveCase, a.Value); err != nil {
						return err
					}
				}
				return nil
			}))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if out != tc.doc {
			t.Errorf("%s: % x, want the original % x", tc.name, out, tc.doc)
		}
		// In particular the text is not UTF-8 afterwards, which is what a caller
		// hoping to convert would be looking for.
		if strings.Contains(out, tc.text) {
			t.Errorf("%s: the output holds the UTF-8 text %q, so it was converted after all",
				tc.name, tc.text)
		}
	}
}

// TestAnUndecodableByteComesBackAsAReference in a legacy encoding, where the same byte
// in a document declared UTF-8 comes back as the bytes of U+FFFD.
func TestAnUndecodableByteComesBackAsAReference(t *testing.T) {
	// 0x81 is a lead byte in shift_jis, and a space cannot follow it.
	const doc = "<p>a\x81 b</p>"

	var seen strings.Builder
	out, err := lolhtml.RewriteString(doc,
		lolhtml.WithEncoding("shift_jis"),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			seen.WriteString(c.Text())
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen.String(), "�") {
		t.Errorf("the handler saw %q, want U+FFFD", seen.String())
	}
	if !strings.Contains(out, "&#65533;") {
		t.Errorf("the output is % x, want a numeric reference for U+FFFD", out)
	}
	if len(out) <= len(doc) {
		t.Errorf("the output is %d bytes and the input was %d: the reference should be longer",
			len(out), len(doc))
	}

	// With no text handler the byte passes through, as in the UTF-8 case.
	out, err = lolhtml.RewriteString(doc, lolhtml.WithEncoding("shift_jis"))
	if err != nil {
		t.Fatal(err)
	}
	if out != doc {
		t.Errorf("with no handler: % x, want % x", out, doc)
	}

	// And the same failure in a document declared UTF-8 is the raw replacement
	// character rather than a reference, because UTF-8 can hold it.
	out, err = lolhtml.RewriteString("<p>a\x81 b</p>",
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "�") {
		t.Errorf("declared utf-8: % x, want the replacement character itself", out)
	}
	if strings.Contains(out, "&#65533;") {
		t.Errorf("declared utf-8: % x, want no reference", out)
	}
}

// TestAReferenceInTheSourceIsReportedAsItself, so a converter counting characters has
// to decide what a reference is worth: the handler is given the six or eight bytes the
// document wrote, not the character.
func TestAReferenceInTheSourceIsReportedAsItself(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"<p>&#20140;</p>", "&#20140;"},
		{"<p>&yen;</p>", "&yen;"},
		{"<p>&#x4eac;</p>", "&#x4eac;"},
	} {
		var seen strings.Builder
		out, err := lolhtml.RewriteString(tc.doc,
			lolhtml.WithEncoding("windows-1252"),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				seen.WriteString(c.Text())
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if seen.String() != tc.want {
			t.Errorf("%q: the handler saw %q, want %q", tc.doc, seen.String(), tc.want)
		}
		if out != tc.doc {
			t.Errorf("%q: the output is %q", tc.doc, out)
		}
	}
}

// TestInsertingBeyondTheRepertoireIsAReference, which is the other direction and the
// reason a converter cannot be built by inserting: the character it wants to write
// comes out as its number.
func TestInsertingBeyondTheRepertoireIsAReference(t *testing.T) {
	out, err := lolhtml.RewriteString("<p>x</p>",
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetInnerContent("京", lolhtml.Text)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>&#20140;</p>"; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
	// The same insertion into a UTF-8 document is the character.
	out, err = lolhtml.RewriteString("<p>x</p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetInnerContent("京", lolhtml.Text)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>京</p>"; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
}

package lolhtml_test

// What the declared encoding changes, and what it cannot.
//
// It changes the characters: the same byte is a different letter in each encoding, and a
// handler is given the letter. It does not change the markup - which byte spans are
// elements, what they are called, which are text, which are comments - and that is
// worth pinning rather than assuming, because the opposite is a well-known way to get
// script past a filter in a browser: in a legacy multi-byte encoding a lead byte can
// swallow the byte after it, and if that byte were a quote or a ">" the filter and the
// browser would disagree about where the tag ended.
//
// Measured over all 36 encodings the rewriter accepts, against a corpus that puts every
// markup character after nine different lead bytes: the structure is identical in every
// one of them.

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// acceptedEncodings is every canonical name from the WHATWG index that the rewriter
// takes. The four it refuses are in encoding_test.go, with the reasons.
var acceptedEncodings = strings.Fields(`
utf-8 ibm866 iso-8859-2 iso-8859-3 iso-8859-4 iso-8859-5 iso-8859-6 iso-8859-7
iso-8859-8 iso-8859-8-i iso-8859-10 iso-8859-13 iso-8859-14 iso-8859-15
iso-8859-16 koi8-r koi8-u macintosh windows-874 windows-1250 windows-1251
windows-1252 windows-1253 windows-1254 windows-1255 windows-1256 windows-1257
windows-1258 x-mac-cyrillic gbk gb18030 big5 euc-jp shift_jis euc-kr
x-user-defined
`)

// structureCorpus puts every markup character after a byte that is a lead byte in one
// or another multi-byte encoding, which is the shape that would hide it.
func structureCorpus() string {
	var b strings.Builder
	b.WriteString(`<div class="c" data-x='y'>`)
	for _, lead := range []byte{0x81, 0x8b, 0xa1, 0xc2, 0xe0, 0xf0, 0xfe, 0x80, 0x9f} {
		for _, m := range []byte{'>', '<', '"', '\'', '&', '=', '/', ' ', 0x5c} {
			b.WriteString("<p a=\"")
			b.WriteByte(lead)
			if m != '"' {
				b.WriteByte(m)
			}
			b.WriteString("\">t</p>")
		}
	}
	b.WriteString("<!--c--><span>x</span></div>")
	return b.String()
}

// structureUnder returns everything about a document that should not depend on the
// declared encoding: the byte spans of the elements, their names, their attribute
// names, and the byte spans of the text and comments.
func structureUnder(t *testing.T, doc, encoding string) string {
	t.Helper()
	var b strings.Builder
	opts := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			l := e.SourceLocation()
			fmt.Fprintf(&b, "el %s [%d:%d]", e.TagName(), l.Start, l.End)
			for _, a := range e.AttributeList() {
				fmt.Fprintf(&b, " @%s", a.Name)
			}
			b.WriteString("\n")
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			if l.Start == l.End {
				return nil
			}
			fmt.Fprintf(&b, "text [%d:%d]\n", l.Start, l.End)
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			l := c.SourceLocation()
			fmt.Fprintf(&b, "comment [%d:%d]\n", l.Start, l.End)
			return nil
		}),
	}
	if encoding != "" {
		opts = append(opts, lolhtml.WithEncoding(encoding))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		t.Fatalf("%s: %v", encoding, err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		w.Close()
		t.Fatalf("%s: %v", encoding, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("%s: %v", encoding, err)
	}
	return b.String()
}

// TestTheEncodingDoesNotChangeWhatIsMarkup, over every encoding the rewriter accepts.
func TestTheEncodingDoesNotChangeWhatIsMarkup(t *testing.T) {
	doc := structureCorpus()
	// x-user-defined is the baseline: it is single-byte and maps every high byte to a
	// character of its own, so it cannot combine bytes even in principle.
	want := structureUnder(t, doc, "x-user-defined")
	if strings.Count(want, "\n") < 100 {
		t.Fatalf("the corpus produced %d lines of structure; it is meant to be busy",
			strings.Count(want, "\n"))
	}
	for _, enc := range acceptedEncodings {
		got := structureUnder(t, doc, enc)
		if got == want {
			continue
		}
		wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
		for i := range wl {
			if i >= len(gl) || wl[i] != gl[i] {
				line := "<missing>"
				if i < len(gl) {
					line = gl[i]
				}
				t.Errorf("%s differs at line %d: %q, want %q", enc, i, line, wl[i])
				break
			}
		}
	}
	// And with no encoding option at all, which is UTF-8.
	if got := structureUnder(t, doc, ""); got != want {
		t.Errorf("with no encoding option the structure differs")
	}
}

// TestTheEncodingDoesChangeTheCharacters, which is the contrast that makes the test
// above worth having: the same bytes are different letters everywhere.
func TestTheEncodingDoesChangeTheCharacters(t *testing.T) {
	const doc = "<p>\xe9\xa1\x81</p>"
	seen := map[string][]string{}
	for _, enc := range acceptedEncodings {
		var text strings.Builder
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.WithEncoding(enc),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				text.WriteString(c.Text())
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", enc, err)
		}
		seen[text.String()] = append(seen[text.String()], enc)
	}
	if len(seen) < 10 {
		t.Errorf("36 encodings produced only %d different readings of the same three bytes",
			len(seen))
	}
	// And the text occupies the same bytes either way: the disagreement is about
	// characters rather than about where the text is. How many chunks it arrives in
	// does vary, because a byte the decoder cannot use is a chunk of its own - so
	// what is compared is the span, not the count.
	for _, enc := range acceptedEncodings {
		first, last := -1, -1
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.WithEncoding(enc),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				l := c.SourceLocation()
				if l.Start == l.End {
					return nil
				}
				if first < 0 {
					first = l.Start
				}
				last = l.End
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if first != 3 || last != 6 {
			t.Errorf("%s: the text spans [%d:%d], want [3:6]", enc, first, last)
		}
	}
}

// TestSourceLocationsAreByteOffsetsWhateverTheEncoding, which is what makes the
// comparison above possible and what a two-pass rewrite over a legacy document needs.
func TestSourceLocationsAreByteOffsetsWhateverTheEncoding(t *testing.T) {
	// Two two-byte shift_jis characters, so a character offset and a byte offset
	// cannot be the same number.
	const doc = "<p>\x8b\x9e\x93s</p><b>x</b>"
	var spans []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.WithEncoding("shift_jis"),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			l := e.SourceLocation()
			spans = append(spans, doc[l.Start:l.End])
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"<p>", "<b>"}; strings.Join(spans, ",") != strings.Join(want, ",") {
		t.Errorf("the spans are %q, want %q", spans, want)
	}
}

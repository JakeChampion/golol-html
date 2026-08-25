package lolhtml_test

// Content streamed into a sink that stops in the middle of a UTF-8 sequence.
//
// lol-html holds an incomplete sequence waiting for the rest of it, which is
// what makes copying from an arbitrary reader into AsWriter safe: a rune split
// across two writes is joined. If the StreamFunc returns while a sequence is
// still open, those bytes are dropped - measured, `WriteChunk("ab\xc3")` used to
// produce "ab" with no error at all.

import (
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// stream runs fn as a StreamFunc appended to a div and returns the output and
// the error.
func stream(t *testing.T, fn lolhtml.StreamFunc) (string, error) {
	t.Helper()
	return lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamAppend(fn) }))
}

func TestAnUnfinishedSequenceIsReported(t *testing.T) {
	tests := []struct {
		name string
		fn   lolhtml.StreamFunc
		want bool
	}{
		{"complete", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("café"), lolhtml.Text)
		}, false},
		{"ascii", func(s *lolhtml.Sink) error {
			return s.WriteString("plain", lolhtml.Text)
		}, false},
		{"nothing written", func(s *lolhtml.Sink) error { return nil }, false},
		{"empty write", func(s *lolhtml.Sink) error {
			return s.WriteChunk(nil, lolhtml.Text)
		}, false},

		{"two-byte rune cut short", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("ab\xc3"), lolhtml.Text)
		}, true},
		{"three-byte rune cut short", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("ab\xe6\x97"), lolhtml.Text)
		}, true},
		{"four-byte rune cut short", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("ab\xf0\x9f\x8e"), lolhtml.Text)
		}, true},
		{"cut short after a long run", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte(strings.Repeat("x", 5000)+"\xe6\x97"), lolhtml.Text)
		}, true},

		// Splitting a rune is fine; leaving one open is not.
		{"split then finished", func(s *lolhtml.Sink) error {
			if err := s.WriteChunk([]byte("caf\xc3"), lolhtml.Text); err != nil {
				return err
			}
			return s.WriteChunk([]byte("\xa9 more"), lolhtml.Text)
		}, false},
		{"split then left open", func(s *lolhtml.Sink) error {
			if err := s.WriteChunk([]byte("caf\xc3"), lolhtml.Text); err != nil {
				return err
			}
			return s.WriteChunk([]byte("\xa9\xe6\x97"), lolhtml.Text)
		}, true},
		{"finished by a later write of exactly four bytes", func(s *lolhtml.Sink) error {
			if err := s.WriteChunk([]byte("\xe6"), lolhtml.Text); err != nil {
				return err
			}
			return s.WriteChunk([]byte("\x97\xa5abc"), lolhtml.Text)
		}, false},

		// The case the documentation recommends, with a truncated source.
		{"copy of a truncated source", func(s *lolhtml.Sink) error {
			_, err := io.Copy(s.AsWriter(lolhtml.Text), strings.NewReader("hello \xe6\x97"))
			return err
		}, true},
		{"copy of a whole source", func(s *lolhtml.Sink) error {
			_, err := io.Copy(s.AsWriter(lolhtml.Text), strings.NewReader("hello 日本語"))
			return err
		}, false},

		// HTML content is not decoded differently, so the same rule holds.
		{"html content, cut short", func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("<b>x</b>\xc3"), lolhtml.HTML)
		}, true},
		{"html content, whole", func(s *lolhtml.Sink) error {
			return s.WriteString("<b>café</b>", lolhtml.HTML)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := stream(t, tt.fn)
			got := errors.Is(err, lolhtml.ErrIncompleteRune)
			if got != tt.want {
				t.Fatalf("reported=%v want=%v (out %q, err %v)", got, tt.want, out, err)
			}
			if !tt.want && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// A rune split across writes is joined, whatever the split. This is the property
// that makes io.Copy into AsWriter safe, and the reason the check is at the end
// of the StreamFunc rather than inside each write.
func TestAnySplitOfValidTextIsAccepted(t *testing.T) {
	const content = "aé日🎉bcé日🎉"
	for n := 1; n <= len(content); n++ {
		out, err := stream(t, func(s *lolhtml.Sink) error {
			for i := 0; i < len(content); i += n {
				if err := s.WriteChunk([]byte(content[i:min(i+n, len(content))]), lolhtml.Text); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("writes of %d: %v", n, err)
		}
		if want := `<div>` + content + `</div>`; out != want {
			t.Fatalf("writes of %d: got %q, want %q", n, out, want)
		}
	}
}

// And every truncation of that same text is reported, which is the other half:
// the check must fire on exactly the prefixes that end mid-sequence.
func TestEveryTruncationIsReported(t *testing.T) {
	const content = "aé日🎉bcé日🎉"
	for i := 1; i <= len(content); i++ {
		prefix := content[:i]
		_, err := stream(t, func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte(prefix), lolhtml.Text)
		})
		reported := errors.Is(err, lolhtml.ErrIncompleteRune)
		// The prefix ends mid-sequence exactly when decoding its last rune
		// gives an error with a size of one byte over more than one byte of
		// input - which is what utf8.ValidString detects for this shape.
		want := !utf8.ValidString(prefix)
		if reported != want {
			t.Errorf("prefix of %d bytes (%q): reported=%v, valid=%v, err=%v",
				i, prefix, reported, utf8.ValidString(prefix), err)
		}
	}
}

// The error says what was dropped, because the caller's next question is how
// much of its content is missing.
func TestTheErrorNamesTheBytes(t *testing.T) {
	_, err := stream(t, func(s *lolhtml.Sink) error {
		return s.WriteChunk([]byte("ab\xe6\x97"), lolhtml.Text)
	})
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"e6 97", "2 trailing byte"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Every streaming insertion goes through the same sink, so every one of them
// gets the check.
func TestEveryStreamingInsertionIsChecked(t *testing.T) {
	cut := func(s *lolhtml.Sink) error { return s.WriteChunk([]byte("\xc3"), lolhtml.Text) }
	inserts := map[string]func(*lolhtml.Element) error{
		"StreamBefore":          func(e *lolhtml.Element) error { return e.StreamBefore(cut) },
		"StreamAfter":           func(e *lolhtml.Element) error { return e.StreamAfter(cut) },
		"StreamPrepend":         func(e *lolhtml.Element) error { return e.StreamPrepend(cut) },
		"StreamAppend":          func(e *lolhtml.Element) error { return e.StreamAppend(cut) },
		"StreamSetInnerContent": func(e *lolhtml.Element) error { return e.StreamSetInnerContent(cut) },
		"StreamReplace":         func(e *lolhtml.Element) error { return e.StreamReplace(cut) },
		"EndTag.StreamBefore": func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.StreamBefore(cut) })
		},
	}
	for name, insert := range inserts {
		_, err := lolhtml.RewriteString(`<div>x</div>`, lolhtml.OnElement("div", insert))
		if !errors.Is(err, lolhtml.ErrIncompleteRune) {
			t.Errorf("%s: err = %v, want ErrIncompleteRune", name, err)
		}
	}
}

// A WriteString does not complete a sequence left open by a WriteChunk: the
// held bytes become U+FFFD and the string follows them. WriteChunk's
// documentation said not to do it; nothing said what happened if you did.
func TestWriteStringAfterAnOpenSequenceIsRefused(t *testing.T) {
	out, err := stream(t, func(s *lolhtml.Sink) error {
		if err := s.WriteChunk([]byte("caf\xc3"), lolhtml.Text); err != nil {
			return err
		}
		return s.WriteString("x", lolhtml.Text)
	})
	if !errors.Is(err, lolhtml.ErrIncompleteRune) {
		t.Fatalf("err = %v, want ErrIncompleteRune (out %q)", err, out)
	}
	if !strings.Contains(err.Error(), "c3") {
		t.Errorf("the error does not name the waiting bytes: %v", err)
	}

	// After the sequence is completed by a WriteChunk, WriteString is fine
	// again - the refusal is about the open sequence, not about mixing the two.
	out, err = stream(t, func(s *lolhtml.Sink) error {
		if err := s.WriteChunk([]byte("caf\xc3"), lolhtml.Text); err != nil {
			return err
		}
		if err := s.WriteChunk([]byte("\xa9"), lolhtml.Text); err != nil {
			return err
		}
		return s.WriteString(" and more", lolhtml.Text)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `<div>café and more</div>`; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

// WriteString itself refuses content that is not whole UTF-8, which lol-html
// does and this package leaves alone - pinned so the two checks are not confused
// with each other.
func TestWriteStringRefusesBrokenContentItself(t *testing.T) {
	for _, bad := range []string{"a\xffb", "ab\xe6\x97"} {
		_, err := stream(t, func(s *lolhtml.Sink) error {
			return s.WriteString(bad, lolhtml.Text)
		})
		if err == nil {
			t.Errorf("WriteString(%q) was accepted", bad)
			continue
		}
		if errors.Is(err, lolhtml.ErrIncompleteRune) {
			t.Errorf("WriteString(%q) reported ErrIncompleteRune; that sentinel is "+
				"for a sequence this package is holding, not for content lol-html "+
				"rejected outright: %v", bad, err)
		}
	}
}

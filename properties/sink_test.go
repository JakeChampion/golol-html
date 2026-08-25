package properties

// Properties of streaming content into a sink.
//
// lol-html holds an incomplete UTF-8 sequence waiting for the rest of it, so a
// rune split across two writes is joined. That is what makes copying from an
// arbitrary reader into a sink safe, and it is also why a sequence that never
// gets finished is dropped without a word. Both halves are properties rather
// than examples: the first has to hold for every split, the second for every
// truncation.

import (
	"errors"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
	"pgregory.net/rapid"
)

// TestAnySplitOfValidTextArrivesWhole: for any string and any set of write
// boundaries, the content that comes out is the content that went in.
func TestAnySplitOfValidTextArrivesWhole(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "content")
		if !utf8.ValidString(s) {
			t.Skip("the generator produced invalid UTF-8; that is the other property")
		}
		cuts := rapid.SliceOfN(rapid.IntRange(0, len(s)), 0, 6).Draw(t, "cuts")
		bounds := boundaries(len(s), cuts)

		out, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(sink *lolhtml.Sink) error {
					prev := 0
					for _, b := range bounds {
						if err := sink.WriteChunk([]byte(s[prev:b]), lolhtml.Text); err != nil {
							return err
						}
						prev = b
					}
					return nil
				})
			}))
		if err != nil {
			t.Fatalf("writing %q in %v pieces: %v", s, bounds, err)
		}
		want := `<x>` + lolhtml.EscapeText(s) + `</x>`
		if out != want {
			t.Fatalf("writing %q in %v pieces gave %q, want %q", s, bounds, out, want)
		}
	})
}

// TestEveryTruncationIsReported: for any string, cutting it at any byte offset
// is an error exactly when the cut lands inside a UTF-8 sequence.
func TestEveryTruncationIsReported(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "content")
		if !utf8.ValidString(s) {
			t.Skip("the generator produced invalid UTF-8")
		}
		n := rapid.IntRange(0, len(s)).Draw(t, "cut")
		prefix := s[:n]

		_, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(sink *lolhtml.Sink) error {
					return sink.WriteChunk([]byte(prefix), lolhtml.Text)
				})
			}))
		reported := errors.Is(err, lolhtml.ErrIncompleteRune)
		want := !utf8.ValidString(prefix)
		if reported != want {
			t.Fatalf("%q cut at %d: reported=%v, want=%v (err %v)", s, n, reported, want, err)
		}
	})
}

// boundaries turns a set of cut positions into sorted write boundaries ending at
// the length, with duplicates and zeroes removed so every write is non-empty
// except where the generator asked for none.
func boundaries(n int, cuts []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, c := range cuts {
		if c > 0 && c < n && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return append(out, n)
}

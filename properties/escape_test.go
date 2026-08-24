package properties

// Properties of the two escapers.
//
// EscapeText and EscapeAttribute are the only pure-Go code in the library that
// has to agree with the Rust side, and the agreement is what makes them worth
// having: a caller who must build markup keeps the guarantee they would have had
// from letting the library write the value. A table of examples checks the
// characters someone thought of. These check every string rapid can produce.

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"pgregory.net/rapid"
)

// TestEscapeTextIsContentTypeText is the claim in one line: for any value, going
// through EscapeText and inserting as HTML is the same as inserting as Text.
//
// If these ever disagree, the safe-looking route is the unsafe one.
func TestEscapeTextIsContentTypeText(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "value")

		byLibrary, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.SetInnerContent(s, lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("inserting %q as Text: %v", s, err)
		}
		byHand, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.SetInnerContent(lolhtml.EscapeText(s), lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("inserting EscapeText(%q) as HTML: %v", s, err)
		}
		if byLibrary != byHand {
			t.Fatalf("EscapeText(%q) is not ContentType Text:\n  Text: %q\n  ours: %q",
				s, byLibrary, byHand)
		}
	})
}

// TestEscapeAttributeMakesOneAttribute: whatever the value, the element that
// comes back carries exactly the attribute that was written and no other. The
// parser decides what an attribute is, which is the only judge worth asking.
func TestEscapeAttributeMakesOneAttribute(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "value")
		quote := rapid.SampledFrom([]string{`"`, `'`}).Draw(t, "quote")

		markup := `<a x=` + quote + lolhtml.EscapeAttribute(s) + quote + `>`

		var names []string
		var tag string
		if _, err := lolhtml.RewriteString(markup,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				tag = e.TagName()
				for name := range e.Attributes() {
					names = append(names, name)
				}
				return nil
			})); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if tag != "a" {
			t.Fatalf("EscapeAttribute(%q) between %s quotes gave the element %q: %s",
				s, quote, tag, markup)
		}
		if len(names) != 1 || names[0] != "x" {
			t.Fatalf("EscapeAttribute(%q) between %s quotes gave attributes %v: %s",
				s, quote, names, markup)
		}
	})
}

// TestEscapeAttributeRoundTrips: the value a parser reads back is the value that
// went in. Decoded with the standard library rather than with anything from the
// package under test, so the two cannot be wrong together.
func TestEscapeAttributeRoundTrips(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "value")
		if strings.HasPrefix(s, "\ufeff") {
			// Documented on Element.Attribute: lol-html's decoder drops a
			// leading byte-order mark on the way out. The serialised form is
			// still faithful, so this is the reader's behaviour, not the
			// escaper's.
			t.Skip("a leading U+FEFF is dropped when the value is read back")
		}
		quote := rapid.SampledFrom([]string{`"`, `'`}).Draw(t, "quote")

		var raw string
		var ok bool
		if _, err := lolhtml.RewriteString(
			`<a x=`+quote+lolhtml.EscapeAttribute(s)+quote+`>`,
			lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				raw, ok = e.Attribute("x")
				return nil
			})); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if !ok {
			t.Fatalf("EscapeAttribute(%q) between %s quotes lost the attribute", s, quote)
		}
		if got := stdhtml.UnescapeString(raw); got != s {
			t.Fatalf("EscapeAttribute(%q) between %s quotes reads back as %q",
				s, quote, got)
		}
	})
}

// TestEscapingLeavesCleanValuesAlone: a value with nothing to escape comes back
// identical, which is what makes applying these unconditionally reasonable.
func TestEscapingLeavesCleanValuesAlone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "value")
		if strings.ContainsAny(s, `&<>"'`) {
			t.Skip("there is something to escape")
		}
		if got := lolhtml.EscapeText(s); got != s {
			t.Fatalf("EscapeText(%q) = %q", s, got)
		}
		if got := lolhtml.EscapeAttribute(s); got != s {
			t.Fatalf("EscapeAttribute(%q) = %q", s, got)
		}
	})
}

// TestEscapeAttributeIsEscapeTextPlusQuotes states the relationship between the
// two, so that a change to one that does not belong in the other fails here.
func TestEscapeAttributeIsEscapeTextPlusQuotes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genString().Draw(t, "value")
		want := strings.NewReplacer(`"`, "&quot;", "'", "&#39;").Replace(lolhtml.EscapeText(s))
		if got := lolhtml.EscapeAttribute(s); got != want {
			t.Fatalf("EscapeAttribute(%q) = %q, want EscapeText plus quotes = %q", s, got, want)
		}
	})
}

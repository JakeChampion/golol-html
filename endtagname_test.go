package lolhtml_test

// What EndTag.SetName refuses.
//
// lol-html checks a start tag's name and not an end tag's, so the two renames
// used to disagree: a name Element.SetTagName refused was written by
// EndTag.SetName as it was, between "</" and ">". A name of "a>b" produced
// "</a>b>", which a parser reads as an end tag followed by text, and one with a
// "<" in it opened an element. The check is now made on this side, with
// lol-html's own messages, so the same name is wrong in both places for the
// same reason - and this file holds the two to that by asking each in turn.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// renameStartTag reports the *NativeError Element.SetTagName gives for name, or
// nil if it accepts it.
func renameStartTag(t *testing.T, name string) *lolhtml.NativeError {
	t.Helper()
	_, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetTagName(name)
		}))
	if err == nil {
		return nil
	}
	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) {
		t.Fatalf("SetTagName(%q) failed with something other than a NativeError: %v", name, err)
	}
	return ne
}

// renameEndTag is the same for EndTag.SetName. The element is left alone, so a
// refusal here is the end tag's own.
func renameEndTag(t *testing.T, name string) (string, *lolhtml.NativeError) {
	t.Helper()
	out, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(tag *lolhtml.EndTag) error {
				return tag.SetName(name)
			})
		}))
	if err == nil {
		return out, nil
	}
	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) {
		t.Fatalf("EndTag.SetName(%q) failed with something other than a NativeError: %v", name, err)
	}
	return out, ne
}

// TestEndTagSetNameRefusesWhatSetTagNameRefuses, with the same message. The
// table is built by asking SetTagName rather than by copying its wording in,
// so that a change in lol-html's messages shows up as a difference between the
// two and not as a stale string here.
func TestEndTagSetNameRefusesWhatSetTagNameRefuses(t *testing.T) {
	for _, name := range []string{"", "1a", "a b", "a\tb", "a\nb", "a/b", "a>b", "a\fb", "a\rb"} {
		want := renameStartTag(t, name)
		if want == nil {
			t.Errorf("SetTagName(%q) accepted the name; this table assumes it is refused", name)
			continue
		}
		out, got := renameEndTag(t, name)
		if got == nil {
			t.Errorf("EndTag.SetName(%q) accepted the name and wrote %q", name, out)
			continue
		}
		if got.Op != "end_tag_name_set" {
			t.Errorf("EndTag.SetName(%q): Op = %q, want %q", name, got.Op, "end_tag_name_set")
		}
		if got.Message != want.Message {
			t.Errorf("EndTag.SetName(%q) says %q; SetTagName says %q", name, got.Message, want.Message)
		}
	}
}

// TestEndTagSetNameAcceptsWhatSetTagNameAccepts: the check is no wider than
// lol-html's. "=" is not a character that ends a tag name, so it goes through.
func TestEndTagSetNameAcceptsWhatSetTagNameAccepts(t *testing.T) {
	for _, name := range []string{"span", "a=b"} {
		if ne := renameStartTag(t, name); ne != nil {
			t.Errorf("SetTagName(%q) refused the name: %v", name, ne)
		}
		out, ne := renameEndTag(t, name)
		if ne != nil {
			t.Errorf("EndTag.SetName(%q) refused the name: %v", name, ne)
			continue
		}
		if want := "<p>x</" + name + ">"; out != want {
			t.Errorf("EndTag.SetName(%q) wrote %q, want %q", name, out, want)
		}
	}
}

// TestEndTagSetNameLeavesInvalidUTF8ToLolHTML, which reports it as
// ErrInvalidUTF8 like every other write path. The name starts with a letter so
// that the byte, and not the first-character rule, is what refuses it - the
// same allowance invalidutf8_test.go makes for SetTagName.
//
// Only that shape is pinned. A name whose first byte is the invalid one - "\xff"
// on its own - is refused by the first-character rule before lol-html sees it,
// with that rule's message and without the ErrInvalidUTF8 match; SetTagName on
// the same name reports the UTF-8 error, because lol-html decodes before it
// checks. That is the one name the two renames still answer differently.
func TestEndTagSetNameLeavesInvalidUTF8ToLolHTML(t *testing.T) {
	const name = "a\xff"
	_, ne := renameEndTag(t, name)
	if ne == nil {
		t.Fatalf("EndTag.SetName(%q) was accepted", name)
	}
	if !errors.Is(ne, lolhtml.ErrInvalidUTF8) {
		t.Errorf("EndTag.SetName(%q) = %v; does not match ErrInvalidUTF8", name, ne)
	}
	if want := renameStartTag(t, name); want == nil || want.Message != ne.Message {
		t.Errorf("EndTag.SetName(%q) says %q; SetTagName says %v", name, ne.Message, want)
	}
}

// TestARenamedEndTagStillGuardsTheRawText. The content in front of a script's
// end tag was tokenised as raw text, and stays raw text whatever the tag is
// renamed to - so the check in Before keys on the name the tag was parsed with,
// not the one it has now. Otherwise SetName("div") was a way to switch the
// guard off, in either order.
func TestARenamedEndTagStillGuardsTheRawText(t *testing.T) {
	const payload = `</script><img src=x>`

	for name, edit := range map[string]func(*lolhtml.EndTag) error{
		"rename then insert": func(tag *lolhtml.EndTag) error {
			if err := tag.SetName("div"); err != nil {
				return err
			}
			return tag.Before(payload, lolhtml.HTML)
		},
		"insert then rename": func(tag *lolhtml.EndTag) error {
			if err := tag.Before(payload, lolhtml.HTML); err != nil {
				return err
			}
			return tag.SetName("div")
		},
	} {
		out, err := lolhtml.RewriteString(`<script>a</script>`,
			lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				return e.OnEndTag(edit)
			}))
		if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
			t.Errorf("%s: %v (output %q), want ErrRawTextBreakout", name, err, out)
		}
		if err != nil && !strings.Contains(err.Error(), "<script>") {
			t.Errorf("%s: the error names the wrong element: %v", name, err)
		}
	}
}

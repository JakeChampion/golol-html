package lolhtml_test

// Inserting into a document whose encoding cannot hold what is being inserted.
//
// WithEncoding says a character the target encoding cannot represent is emitted
// as a numeric character reference. That is right wherever a reference is
// decoded. Raw text is where it is not, so a <script> or a <style> in a legacy
// encoding cannot hold a character outside that encoding at all - and nothing
// reports it.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestUnrepresentableCharactersBecomeReferences is the documented rule, in the
// positions where it works.
func TestUnrepresentableCharactersBecomeReferences(t *testing.T) {
	for _, tt := range []struct {
		encoding, content, want string
	}{
		// Representable: transcoded, not referenced.
		{"windows-1252", "café", "caf\xe9"},
		{"iso-8859-2", "cafe", "cafe"},
		{"shift_jis", "日", "\x93\xfa"},
		{"utf-8", "café 日", "café 日"},

		// Not representable: a numeric reference.
		{"windows-1252", "日", "&#26085;"},
		{"windows-1252", "🎉", "&#127881;"},
		{"shift_jis", "é", "&#233;"},
		{"windows-1252", "a日b", "a&#26085;b"},
	} {
		out, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.WithEncoding(tt.encoding),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent(tt.content, lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("%s %q: %v", tt.encoding, tt.content, err)
		}
		if want := "<p>" + tt.want + "</p>"; out != want {
			t.Errorf("%s inserting %q: got %q, want %q", tt.encoding, tt.content, out, want)
		}
	}
}

// TestAReferenceIsDecodedInAnAttribute, so the fallback is sound there too.
func TestAReferenceIsDecodedInAnAttribute(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-x", "日")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p data-x="&#26085;">x</p>` {
		t.Errorf("got %q", out)
	}
	// And a parser reads the attribute back as the character.
	var got string
	if _, err := lolhtml.RewriteString(out, lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			got, _ = e.Attribute("data-x")
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	// Attribute reports raw source, so the reference is still encoded here; what
	// matters is that it is a reference and not eight stray characters of text.
	if got != "&#26085;" {
		t.Errorf("the attribute reads back as %q", got)
	}
}

// TestRawTextCannotHoldAnUnrepresentableCharacter is the hazard. Both rules were
// followed - Text is the right content type for a script body of known-safe
// characters, and the reference is the documented fallback - and the script now
// says something else.
func TestRawTextCannotHoldAnUnrepresentableCharacter(t *testing.T) {
	for _, tag := range []string{"script", "style"} {
		for _, ct := range []struct {
			name string
			ct   lolhtml.ContentType
		}{{"Text", lolhtml.Text}, {"HTML", lolhtml.HTML}} {
			out, err := lolhtml.RewriteString("<"+tag+">x</"+tag+">",
				lolhtml.WithEncoding("windows-1252"),
				lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
					return e.SetInnerContent(`var s = '日'`, ct.ct)
				}))
			if err != nil {
				t.Fatalf("%s %s: %v", tag, ct.name, err)
			}
			if !strings.Contains(out, "&#26085;") {
				t.Errorf("%s %s: the reference is gone, so this hazard may be fixed "+
					"and the documentation can change: %q", tag, ct.name, out)
			}
			// The content type makes no difference: the substitution happens
			// after escaping.
			if want := "<" + tag + ">var s = '&#26085;'</" + tag + ">"; out != want {
				t.Errorf("%s %s: got %q, want %q", tag, ct.name, out, want)
			}
		}
	}
}

// TestEscapableRawTextIsFine: a textarea and a title decode references, so the
// fallback works in them. This is the line between "raw text" and "escapable raw
// text" showing up somewhere other than escaping.
func TestEscapableRawTextIsFine(t *testing.T) {
	for _, tag := range []string{"textarea", "title"} {
		out, err := lolhtml.RewriteString("<"+tag+">x</"+tag+">",
			lolhtml.WithEncoding("windows-1252"),
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("日", lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if want := "<" + tag + ">&#26085;</" + tag + ">"; out != want {
			t.Errorf("%s: got %q, want %q", tag, out, want)
		}
	}
}

// TestNothingReportsIt: no error from the insertion, from Write or from Close.
// That is the reason it needs writing down rather than gating.
func TestNothingReportsAnUnrepresentableInsertion(t *testing.T) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out,
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			// The insertion itself succeeds.
			if err := e.SetInnerContent("日", lolhtml.Text); err != nil {
				t.Errorf("SetInnerContent reported %v; if it now refuses this, the "+
					"documentation can change", err)
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<script>x</script>`)); err != nil {
		t.Errorf("Write reported %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close reported %v", err)
	}
}

// TestHandlersAlwaysSeeUTF8, whatever the document's encoding, and the output
// stays in the document's encoding. The documented rule, pinned in both
// directions.
func TestHandlersAlwaysSeeUTF8(t *testing.T) {
	doc := []byte("<p title=\"caf\xe9\">caf\xe9</p>")

	var title, text string
	out, err := lolhtml.Rewrite(doc,
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			title, _ = e.Attribute("title")
			return nil
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			text += tc.Text()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if title != "café" || text != "café" {
		t.Errorf("handlers saw title=%q text=%q, want UTF-8 %q", title, text, "café")
	}
	if string(out) != string(doc) {
		t.Errorf("passthrough changed the bytes:\n in: % x\nout: % x", doc, out)
	}
}

// TestAMisdeclaredEncodingChangesWhatHandlersSeeAndNotTheOutput. Bytes that are
// valid in both encodings pass through unchanged either way; what moves is the
// string a handler is given, which is the thing a rewrite makes decisions from.
func TestAMisdeclaredEncodingChangesWhatHandlersSeeAndNotTheOutput(t *testing.T) {
	// UTF-8 bytes for é, which are also two valid windows-1252 characters.
	doc := []byte("<p title=\"caf\xc3\xa9\">caf\xc3\xa9</p>")

	for _, tt := range []struct{ encoding, wantTitle string }{
		{"utf-8", "café"},
		{"windows-1252", "cafÃ©"},
	} {
		var title string
		out, err := lolhtml.Rewrite(doc,
			lolhtml.WithEncoding(tt.encoding),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				title, _ = e.Attribute("title")
				return nil
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.encoding, err)
		}
		if title != tt.wantTitle {
			t.Errorf("declared %s: the handler saw %q, want %q",
				tt.encoding, title, tt.wantTitle)
		}
		if string(out) != string(doc) {
			t.Errorf("declared %s: the bytes changed:\n in: % x\nout: % x",
				tt.encoding, doc, out)
		}
	}
}

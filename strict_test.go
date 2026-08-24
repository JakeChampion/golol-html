package lolhtml_test

// Strict mode, and what turning it off actually costs.
//
// The rewriter works on a token stream with no DOM to backtrack through, so a
// few shapes of markup leave it unable to tell whether what follows is markup or
// raw text. Strict mode stops there; lenient mode guesses.
//
// Neither is simply the safe one, and the difference is not visible in a rewrite
// that happens to work, so both halves are pinned here: which documents trigger
// it, that strict mode leaves a truncated response behind, and that lenient mode
// hands content past the handlers entirely. The last one is a sanitiser bypass,
// and it is asserted so that nobody flips the flag to get past a failure without
// a test telling them what it allows.

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// ambiguousTags trigger the guard when they open inside a <select>. The list is
// upstream's, in ambiguity_guard.rs.
var ambiguousTags = []string{
	"title", "style", "iframe", "xmp", "plaintext", "noembed", "noframes", "noscript",
}

// Inside a <frameset> the same list applies except <noframes>, which is legal
// there and so is not ambiguous.
func ambiguousIn(container string) []string {
	if container != "frameset" {
		return ambiguousTags
	}
	out := make([]string, 0, len(ambiguousTags)-1)
	for _, t := range ambiguousTags {
		if t != "noframes" {
			out = append(out, t)
		}
	}
	return out
}

// safeInSelect do not trigger it: script is explicitly allowed, and the others
// end the ambiguous context rather than entering it.
var safeInSelect = []string{"script", "textarea", "input", "keygen", "select", "div", "option"}

func rewriteStrict(doc string, strict bool) (out string, fired []string, err error) {
	var buf bytes.Buffer
	w, werr := lolhtml.NewWriter(&buf,
		lolhtml.WithStrict(strict),
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			fired = append(fired, "script")
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			fired = append(fired, "img")
			return e.SetAttribute("loading", "lazy")
		}))
	if werr != nil {
		return "", nil, werr
	}
	if _, e := w.Write([]byte(doc)); e != nil {
		w.Close()
		return buf.String(), fired, e
	}
	if e := w.Close(); e != nil {
		return buf.String(), fired, e
	}
	return buf.String(), fired, nil
}

// TestStrictModeTriggers pins the exact set. A tag leaving or joining it is a
// change in which documents this library refuses, which is worth noticing.
func TestStrictModeTriggers(t *testing.T) {
	for _, container := range []string{"select", "frameset"} {
		t.Run(container, func(t *testing.T) {
			for _, tag := range ambiguousIn(container) {
				doc := fmt.Sprintf(`<%s><%s></%s></%s><img src="/a">`,
					container, tag, tag, container)
				if _, _, err := rewriteStrict(doc, true); err == nil {
					t.Errorf("<%s> inside <%s> did not trigger strict mode", tag, container)
				}
				if _, _, err := rewriteStrict(doc, false); err != nil {
					t.Errorf("<%s> inside <%s> failed in lenient mode too: %v", tag, container, err)
				}
			}
		})
	}

	t.Run("tags that do not trigger it", func(t *testing.T) {
		for _, tag := range safeInSelect {
			doc := fmt.Sprintf(`<select><%s></%s></select><img src="/a">`, tag, tag)
			if _, _, err := rewriteStrict(doc, true); err != nil {
				t.Errorf("<%s> inside <select> triggered strict mode: %v", tag, err)
			}
		}
	})

	t.Run("nothing outside those contexts", func(t *testing.T) {
		for _, tag := range ambiguousTags {
			doc := fmt.Sprintf(`<div><%s></%s></div><img src="/a">`, tag, tag)
			if _, _, err := rewriteStrict(doc, true); err != nil {
				t.Errorf("<%s> inside <div> triggered strict mode: %v", tag, err)
			}
		}
	})
}

// TestStrictModeFailureTruncatesTheResponse: the failure is mid-stream, so
// whatever was already emitted has reached the sink. Same hazard as a memory
// bail-out, and for the same reason the caller has to discard rather than serve.
func TestStrictModeFailureTruncatesTheResponse(t *testing.T) {
	doc := `<img src="/first"><select><xmp></xmp></select><img src="/last">`

	out, fired, err := rewriteStrict(doc, true)
	if err == nil {
		t.Fatal("expected strict mode to abort")
	}

	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %T, want *NativeError", err)
	}
	if !strings.Contains(ne.Message, "ambiguous") {
		t.Errorf("the message does not explain itself: %q", ne.Message)
	}
	if ne.MemoryLimitExceeded() {
		t.Error("MemoryLimitExceeded is true for a parsing ambiguity")
	}

	if !strings.Contains(out, `src="/first"`) {
		t.Errorf("the prefix before the ambiguity did not reach the sink: %q", out)
	}
	if strings.Contains(out, `src="/last"`) {
		t.Errorf("content after the ambiguity reached the sink: %q", out)
	}
	if len(out) >= len(doc) {
		t.Errorf("expected a truncated response, got %d of %d bytes", len(out), len(doc))
	}
	if got := strings.Join(fired, ","); got != "img" {
		t.Errorf("handlers fired %q, want only the one before the ambiguity", got)
	}
}

// TestLenientModeHandsContentPastTheHandlers is the cost of the other choice,
// and the reason WithStrict's documentation now says to leave it on. A sanitiser
// that removes every script does not remove this one: the content after the
// ambiguous tag is treated as text, so no handler is invoked for it and it is
// emitted verbatim, with no error to notice.
func TestLenientModeHandsContentPastTheHandlers(t *testing.T) {
	const payload = `<select><xmp><script>alert(1)</script>`

	out, fired, err := rewriteStrict(payload, false)
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if len(fired) != 0 {
		t.Errorf("handlers fired %v; the point of this test is that none do", fired)
	}
	if !strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("the script did not survive, so this bypass has been closed "+
			"and the WithStrict documentation should be revisited: %q", out)
	}

	// Strict mode refuses the same document rather than passing it through.
	if _, _, err := rewriteStrict(payload, true); err == nil {
		t.Error("strict mode accepted the payload")
	}
}

// TestLenientModeMissedRegionEndsAtTheClosingTag says how far the damage
// reaches, which decides how bad it is. If the ambiguous tag is closed, the
// region ends there and the rest of the document is rewritten normally. If it is
// left open - and a document that trips this guard is malformed already, so it
// often is - the region runs to the end of the input and nothing after it is
// ever seen.
func TestLenientModeMissedRegionEndsAtTheClosingTag(t *testing.T) {
	t.Run("closed: only the region inside is missed", func(t *testing.T) {
		out, fired, err := rewriteStrict(`<select><xmp></xmp></select><img src="/a">`, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(fired) != 1 || !strings.Contains(out, `loading="lazy"`) {
			t.Errorf("the img after the closing tag should still be rewritten: %q %v", out, fired)
		}
	})

	t.Run("unclosed: nothing afterwards is seen", func(t *testing.T) {
		out, fired, err := rewriteStrict(
			`<select><xmp><img src="/a"></select><img src="/b">`, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(fired) != 0 {
			t.Errorf("handlers fired %v; both images are inside the raw-text region", fired)
		}
		if strings.Contains(out, `loading="lazy"`) {
			t.Errorf("something after an unclosed ambiguous tag was rewritten: %q", out)
		}
	})

	t.Run("plaintext is never closed", func(t *testing.T) {
		// <plaintext> has no end tag, so the region always runs to the end of
		// the document however the markup is written.
		out, fired, err := rewriteStrict(
			`<select><plaintext></plaintext></select><img src="/a">`, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(fired) != 0 {
			t.Errorf("handlers fired %v after <plaintext>: %q", fired, out)
		}
	})

	// The context is what decides, not the img: the same tag on its own is
	// rewritten in either mode.
	t.Run("a plain img is unaffected", func(t *testing.T) {
		for _, strict := range []bool{true, false} {
			out, fired, err := rewriteStrict(`<img src="/a">`, strict)
			if err != nil {
				t.Fatal(err)
			}
			if len(fired) != 1 || !strings.Contains(out, `loading="lazy"`) {
				t.Errorf("strict=%v: a plain img was not rewritten: %q %v", strict, out, fired)
			}
		}
	})
}

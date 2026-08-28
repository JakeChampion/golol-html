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

// ambiguousInFrameset is the frameset list, which is not the select list with one
// name removed: <noframes> is legal there, and <script> and <textarea> - both fine
// in a <select> - are ambiguous. Measured by trying every element name in the HTML
// index, in TestTheAmbiguousSetIsExactlyThese.
var ambiguousInFrameset = []string{
	"title", "style", "iframe", "xmp", "plaintext", "noembed", "noscript",
	"script", "textarea",
}

// htmlElementNamesForStrict is every element name in the HTML specification's
// index, so the two lists above can be measured rather than trusted.
var htmlElementNamesForStrict = strings.Fields(`
a abbr acronym address applet area article aside audio b base basefont bdi bdo big
blockquote body br button canvas caption center cite code col colgroup data datalist
dd del details dfn dialog dir div dl dt em embed fieldset figcaption figure font
footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html i iframe img
input ins isindex kbd keygen label legend li link listing main map mark marquee menu
meta meter nav nobr noembed noframes noscript object ol optgroup option output p
param picture plaintext pre progress q rb rp rt rtc ruby s samp script search section
select slot small source span strike strong style sub summary sup table tbody td
template textarea tfoot th thead time title tr track tt u ul var video wbr xmp`)

// Inside a <frameset> a different list applies: <noframes> is legal there, and
// <script> and <textarea> - both fine in a <select> - are ambiguous.
func ambiguousIn(container string) []string {
	if container == "frameset" {
		return ambiguousInFrameset
	}
	return ambiguousTags
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

	if !errors.Is(err, lolhtml.ErrAmbiguousTag) {
		t.Fatalf("err = %v, want ErrAmbiguousTag", err)
	}
	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %T, want *NativeError", err)
	}
	if !strings.Contains(ne.Message, "ambiguous") {
		t.Errorf("the message does not explain itself: %q", ne.Message)
	}
	if errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
		t.Error("a parsing ambiguity matched ErrMemoryLimitExceeded")
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

// TestTheAmbiguousSetIsExactlyThese measures the two lists rather than trusting
// them: every element name in the HTML index, inside each context.
//
// The frameset list used to be written here as the select list minus <noframes>,
// which is what WithStrict's documentation said too, and the sweep is what
// showed it was two names short.
func TestTheAmbiguousSetIsExactlyThese(t *testing.T) {
	for _, tc := range []struct {
		context string
		want    []string
	}{
		{"select", ambiguousTags},
		{"frameset", ambiguousInFrameset},
	} {
		want := map[string]bool{}
		for _, tag := range tc.want {
			want[tag] = true
		}
		var got []string
		for _, tag := range htmlElementNamesForStrict {
			doc := fmt.Sprintf("<%s><%s>x", tc.context, tag)
			_, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true))
			ambiguous := errors.Is(err, lolhtml.ErrAmbiguousTag)
			if ambiguous {
				got = append(got, tag)
			}
			if ambiguous != want[tag] {
				t.Errorf("<%s> inside <%s>: ambiguous = %v, want %v",
					tag, tc.context, ambiguous, want[tag])
			}
		}
		if len(got) != len(tc.want) {
			t.Errorf("inside <%s>, %d names are ambiguous (%v); the documented list has %d",
				tc.context, len(got), got, len(tc.want))
		}
	}
}

// TestNoOtherContextHasIt, for every name in the index, in four containers that
// are not the two.
func TestNoOtherContextHasIt(t *testing.T) {
	for _, container := range []string{"div", "table", "template", "optgroup"} {
		for _, tag := range htmlElementNamesForStrict {
			doc := fmt.Sprintf("<%s><%s>x", container, tag)
			if _, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true)); errors.Is(err, lolhtml.ErrAmbiguousTag) {
				t.Errorf("<%s> inside <%s> triggered the guard", tag, container)
			}
		}
	}
}

// TestWhatEndsTheAmbiguousContextDiffersByContext, which is the other half of the
// correction: the four tags that end it in a <select> end nothing in a <frameset>.
func TestWhatEndsTheAmbiguousContextDiffersByContext(t *testing.T) {
	for _, ender := range []string{"select", "textarea", "input", "keygen"} {
		doc := fmt.Sprintf("<select><%s><title>x", ender)
		if _, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true)); err != nil {
			t.Errorf("<%s> did not end the select context: %v", ender, err)
		}
		doc = fmt.Sprintf("<frameset><%s><title>x", ender)
		if _, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true)); !errors.Is(err, lolhtml.ErrAmbiguousTag) {
			t.Errorf("<%s> ended the frameset context; the documentation says only "+
				"<noframes> does: %v", ender, err)
		}
	}
	// <noframes> is what ends it there, being the one legal member of the list.
	if _, err := lolhtml.RewriteString("<frameset><noframes><title>x",
		lolhtml.WithStrict(true)); err != nil {
		t.Errorf("<noframes> did not end the frameset context: %v", err)
	}
}

// TestLenientModeIsNotSilence, which is what "no handler is invoked" above could be
// read to mean and is not the whole picture. The ambiguous element itself fires, the
// elements after the region fire, and the region's content arrives as text - so a
// rewrite that registers a text handler can see the bytes it is not being shown as
// markup, and refuse the document on its own terms.
func TestLenientModeIsNotSilence(t *testing.T) {
	const payload = `<select><xmp><script>alert(1)</script></xmp></select><p>after</p>`

	var els, texts []string
	out, err := lolhtml.RewriteString(payload,
		lolhtml.WithStrict(false),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			els = append(els, e.TagName())
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if s := strings.TrimSpace(c.Text()); s != "" {
				texts = append(texts, s)
			}
			return nil
		}))
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if out != payload {
		t.Errorf("the document changed: %q", out)
	}

	// The select, the xmp and the paragraph after the region: three elements, and the
	// script is not one of them because it is inside raw text.
	if want := "select xmp p"; strings.Join(els, " ") != want {
		t.Errorf("elements fired: %q, want %q", strings.Join(els, " "), want)
	}
	// The script's markup arrives as text, in pieces - the tokenizer splits a "<"
	// that does not begin a tag into a chunk of its own - so what a sanitiser has to
	// look for is a run of text, not an element.
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "script>alert(1)") {
		t.Errorf("the text handler saw %q, want the script's source", texts)
	}
	if len(texts) < 2 {
		t.Errorf("the region arrived as %d chunks, want it split around the \"<\"", len(texts))
	}

	// And a text handler is enough to refuse it, which is the practical answer for a
	// rewrite that cannot use strict mode.
	sentinel := errors.New("markup in text")
	_, err = lolhtml.RewriteString(payload,
		lolhtml.WithStrict(false),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if strings.Contains(c.Text(), "script") {
				return sentinel
			}
			return nil
		}))
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the handler's refusal", err)
	}
}

// TestLenientModeCanSeeEverythingAfterAClosedAmbiguousTag, which is the difference
// between a bypass and a hole: what is missed is the raw-text content, not the rest of
// the document.
func TestLenientModeCanSeeEverythingAfterAClosedAmbiguousTag(t *testing.T) {
	// A title inside a select: the ambiguity is at the title, and everything after it
	// - including an img with an event handler - is still markup to the rewrite.
	var els []string
	if _, err := lolhtml.RewriteString(`<select><title></title><img src=x onerror=alert(1)></select>`,
		lolhtml.WithStrict(false),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			els = append(els, e.TagName())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if want := "select title img"; strings.Join(els, " ") != want {
		t.Errorf("elements fired: %q, want %q", strings.Join(els, " "), want)
	}
}

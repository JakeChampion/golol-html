package main

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<html><head><link rel="canonical" href="https://x.example/a"></head><body><p>page</p></body></html>`,
	`<html><body><p>no canonical</p></body></html>`,
	`<p>a fragment with no body</p>`,
	`<html><body></body></html>`,
	`<html><body><div class="cookie-banner">already here</div></body></html>`,
	`<html><body><p>a</p></body><body><p>b</p></body></html>`,
	`<html><head><link rel="canonical" href="https://x.example/a?b=1&amp;c=2"></head><body>x</body></html>`,
	`<body/><p>self-closing body</p>`,
	``,
}

func withPolicy(in *injector) { in.b.policy = "https://x.example/privacy?v=2" }

func chunked(text string, n int, opts ...func(*injector)) (string, error) {
	in := &injector{b: defaults()}
	for _, o := range opts {
		o(in)
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, in.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(text); i += n {
		end := min(i+n, len(text))
		if _, err := w.Write([]byte(text[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := injectString(doc, withPolicy)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 19} {
			got, err := chunked(doc, n, withPolicy)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

// TestIdempotent: the marker class is both what this program adds and what it
// looks for, so its own output is recognised on a second pass.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := injectString(doc, withPolicy)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, in, err := injectString(once, withPolicy)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if in.injected != 0 {
			t.Errorf("the second pass of %q injected %d", doc, in.injected)
		}
	}
}

// TestTheBannerGoesInsideTheBody is the request: before the closing body
// content, not after the document.
func TestTheBannerGoesInsideTheBody(t *testing.T) {
	got, in, err := injectString(`<html><body><p>page</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if in.injected != 1 {
		t.Fatalf("injected=%d, want 1", in.injected)
	}
	banner := strings.Index(got, "cookie-banner")
	closing := strings.Index(got, "</body>")
	if banner < 0 || closing < 0 || banner > closing {
		t.Errorf("the banner is not inside the body: %s", got)
	}
}

// TestAFragmentStillGetsABanner: a page with no body is common in a partial
// render, and the alternative is silently doing nothing.
func TestAFragmentStillGetsABanner(t *testing.T) {
	got, in, err := injectString(`<p>a fragment</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if in.injected != 1 {
		t.Errorf("injected=%d, want 1: %s", in.injected, got)
	}
	if !strings.Contains(got, "cookie-banner") {
		t.Errorf("no banner: %s", got)
	}
}

// TestTwoBodiesGetOneBanner: a rewriter has no tree, so both <body> elements
// reach a handler, and only the first end tag may insert.
func TestTwoBodiesGetOneBanner(t *testing.T) {
	got, in, err := injectString(`<html><body><p>a</p></body><body><p>b</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if in.injected != 1 {
		t.Errorf("injected=%d, want 1", in.injected)
	}
	if n := strings.Count(got, `class="cookie-banner"`); n != 1 {
		t.Errorf("%d banners: %s", n, got)
	}
}

func TestAnExistingBannerIsLeftAlone(t *testing.T) {
	const in0 = `<html><body><div class="cookie-banner">ours already</div></body></html>`
	got, in, err := injectString(in0)
	if err != nil {
		t.Fatal(err)
	}
	if in.injected != 0 || total(in.skipped) != 1 {
		t.Errorf("injected=%d skipped=%v", in.injected, in.skipped)
	}
	if got != in0 {
		t.Errorf("the document changed: %s", got)
	}
}

// TestTheCanonicalURLRoundTrips: it is read from an attribute, which is raw
// source, and written into one this program quotes itself. Decoded on the way in
// and escaped on the way out, so it arrives at the server as it was on the page.
func TestTheCanonicalURLRoundTrips(t *testing.T) {
	got, _, err := injectString(
		`<html><head><link rel="canonical" href="https://x.example/a?b=1&amp;c=2"></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}

	var value string
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnElement(`input[name=return_to]`, func(e *lolhtml.Element) error {
			value, _ = e.Attribute("value")
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if want := "https://x.example/a?b=1&amp;c=2"; value != want {
		t.Errorf("return_to is %q, want %q", value, want)
	}
	if decoded := stdhtml.UnescapeString(value); decoded != "https://x.example/a?b=1&c=2" {
		t.Errorf("return_to decodes to %q", decoded)
	}
}

// TestNoOperatorValueBecomesMarkup is the point of the escaping. Every one of
// these is a string an operator could paste in, and none of them may become a
// tag, an attribute, or a broken form.
func TestNoOperatorValueBecomesMarkup(t *testing.T) {
	hostile := []string{
		`" onmouseover="alert(1)`,
		`' onmouseover='alert(1)`,
		`</form><script>alert(1)</script><form>`,
		`<img src=x onerror=alert(1)>`,
		`a & b`,
		`a < b > c`,
		`"><script>alert(1)</script>`,
	}

	for _, bad := range hostile {
		for name, opt := range map[string]func(*injector){
			"message": func(in *injector) { in.b.message = bad },
			"accept":  func(in *injector) { in.b.accept = bad },
			"action":  func(in *injector) { in.b.action = bad },
			"policy":  func(in *injector) { in.b.policy = bad; in.b.policyBy = bad },
		} {
			got, _, err := injectString(`<html><body><p>page</p></body></html>`, opt)
			if err != nil {
				t.Fatalf("%s=%q: %v", name, bad, err)
			}

			// The parser is the judge. A search of the serialised output would
			// fail on output that is correct, because an escaped value contains
			// the same characters inside a value.
			var tags []string
			var attrs []string
			if _, err := lolhtml.RewriteString(got,
				lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					tags = append(tags, e.TagName())
					for n := range e.Attributes() {
						attrs = append(attrs, n)
					}
					return nil
				})); err != nil {
				t.Fatal(err)
			}
			for _, tag := range tags {
				if tag == "script" || tag == "img" {
					t.Errorf("%s=%q produced a %s element: %s", name, bad, tag, got)
				}
			}
			for _, a := range attrs {
				if strings.HasPrefix(a, "on") {
					t.Errorf("%s=%q produced the attribute %q: %s", name, bad, a, got)
				}
			}
			// And the form is still one form with its two buttons.
			if n := strings.Count(got, `type="submit"`); n != 2 {
				t.Errorf("%s=%q left %d submit buttons: %s", name, bad, n, got)
			}
		}
	}
}

// TestTheMessageIsReadableAfterEscaping: escaping that mangles the copy is not
// much better than escaping that fails. An ampersand in the message must reach
// the reader as an ampersand.
func TestTheMessageIsReadableAfterEscaping(t *testing.T) {
	got, _, err := injectString(`<html><body>x</body></html>`, func(in *injector) {
		in.b.message = `Cookies & "choices" you can change <at any time>`
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnText("p.cookie-banner-message", func(tc *lolhtml.TextChunk) error {
			text.WriteString(tc.Text())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if got := stdhtml.UnescapeString(text.String()); got != `Cookies & "choices" you can change <at any time>` {
		t.Errorf("the message reads back as %q", got)
	}
}

func TestASelfClosingBodyIsNotGivenAnEndTag(t *testing.T) {
	// <body/> has no content and no end tag to insert before, so the banner
	// goes at the end of the document instead of nowhere.
	got, in, err := injectString(`<body/><p>x</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if in.injected != 1 {
		t.Errorf("injected=%d, want 1: %s", in.injected, got)
	}
}

// TestAMarkerClassMustBeAnIdentifier: the class is interpolated into a selector
// as well as into markup, and a selector is a third escaping context. Rather
// than write an escaper for it, the program refuses - and says why, which is
// more than "invalid selector" in the middle of a rewrite.
func TestAMarkerClassMustBeAnIdentifier(t *testing.T) {
	for _, bad := range []string{
		"", `" onmouseover="alert(1)`, "a b", "a.b", "a#b", "a>b", "1abc", "a:hover", "*",
	} {
		if _, _, err := injectString(`<html><body>x</body></html>`,
			func(in *injector) { in.b.klass = bad }); err == nil {
			t.Errorf("class %q was accepted", bad)
		}
	}
	for _, good := range []string{"cookie-banner", "a", "A_1", "_x", "x-y_z9"} {
		if _, _, err := injectString(`<html><body>x</body></html>`,
			func(in *injector) { in.b.klass = good }); err != nil {
			t.Errorf("class %q was refused: %v", good, err)
		}
	}
}

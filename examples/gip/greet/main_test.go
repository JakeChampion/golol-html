package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

func greet(t *testing.T, doc string, header http.Header) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Greet(&out, strings.NewReader(doc), header)
	if err != nil {
		t.Fatalf("Greet(%q): %v", doc, err)
	}
	return out.String(), res
}

func header(value string) http.Header {
	h := http.Header{}
	h.Set("X-Name", value)
	return h
}

// hostile are the values a header can hold. Every one of them goes into every
// position, and none of them may become markup.
var hostile = []struct{ what, value string }{
	{"plain", "Ada"},
	{"markup", "</span><script>alert(1)</script>"},
	{"an attribute breakout", `" onload="alert(1)`},
	{"a single-quoted breakout", `' onload='alert(1)`},
	{"an entity", "&amp;"},
	{"a script closer", "</script>"},
	{"a comment closer", "-->"},
	{"latin-1", "caf\xe9"},
	{"an invalid byte", "a\xffb"},
	{"a NUL", "a\x00b"},
	{"an escape", "a\x1b[31mb"},
	{"a newline", "a\nb"},
	{"long", strings.Repeat("A", 8<<10)},
	{"only spaces", "   "},
	{"only controls", "\x00\x01\x02"},
}

const page = `<title data-greet-append="X-Name">Hi </title>` +
	`<p><span data-greet="X-Name">there</span></p>` +
	`<div data-greet-title="X-Name"></div>` +
	`<script type="application/json" data-greet-json="X-Name"></script>`

// TestTheGreetingGoesInAndNothingElseDoes. One test over every hostile value: the
// output has to parse to the same elements as the page did, with one text node
// changed and one attribute added.
func TestTheGreetingGoesInAndNothingElseDoes(t *testing.T) {
	want := tags(t, page)
	for _, tc := range hostile {
		got, _ := greet(t, page, header(tc.value))
		if !utf8.ValidString(got) {
			t.Errorf("%s: the output is not valid UTF-8: %q", tc.what, got)
		}
		if tags(t, got) != want {
			t.Errorf("%s: the elements changed\n got %s\nwant %s", tc.what, tags(t, got), want)
		}
		// The div has exactly the two attributes it should, whatever the value.
		attrs := attributes(t, got, "div")
		if len(attrs) != 2 {
			t.Errorf("%s: the div has %d attributes (%v), want 2", tc.what, len(attrs), attrs)
		}
		// And the script is still one script whose content is JSON.
		body := scriptBody(t, got)
		if strings.Contains(body, "</script") {
			t.Errorf("%s: the script body can close the script: %q", tc.what, body)
		}
		var out map[string]string
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Errorf("%s: the script body is not JSON (%q): %v", tc.what, body, err)
		}
	}
}

// TestALatin1HeaderDoesNotFailThePage is the finding in one test: the raw bytes
// would fail the rewrite, so they are repaired before they reach an insertion.
func TestALatin1HeaderDoesNotFailThePage(t *testing.T) {
	got, res := greet(t, page, header("caf\xe9"))
	if res.Repaired != 4 {
		t.Errorf("Repaired = %d, want 4 - one per position", res.Repaired)
	}
	if !strings.Contains(got, "caf\uFFFD") {
		t.Errorf("got %q, want the repaired name", got)
	}

	// Without the repair, that value fails the whole rewrite rather than one
	// insertion. Measured here so the repair is not decoration.
	_, err := lolhtml.RewriteString(`<span></span>`, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		return e.SetInnerContent("caf\xe9", lolhtml.Text)
	}))
	if err == nil {
		t.Error("the raw Latin-1 value was accepted, so the repair is no longer needed")
	}
}

// TestControlCharactersAreStripped, because the library accepts them and nothing
// downstream wants them.
func TestControlCharactersAreStripped(t *testing.T) {
	got, res := greet(t, `<p><span data-greet="X-Name">x</span></p>`, header("a\x00b\x1bc\x7fd"))
	if res.Stripped != 1 {
		t.Errorf("Stripped = %d, want 1", res.Stripped)
	}
	if want := `<p><span data-greet="X-Name">abcd</span></p>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// A newline is a control character too, and a greeting is one line.
	if got, _ := greet(t, `<p><span data-greet="X-Name">x</span></p>`, header("a\nb")); !strings.Contains(got, ">ab<") {
		t.Errorf("got %q", got)
	}
}

// TestALongValueIsCutByRunesNotBytes. Cutting bytes would split a character, and
// a string that ends mid-character is refused by every write path - so the naive
// version of this turns a long header into a failed page.
func TestALongValueIsCutByRunesNotBytes(t *testing.T) {
	long := strings.Repeat("é", 200) // two bytes each
	got, res := greet(t, `<p><span data-greet="X-Name">x</span></p>`, header(long))
	if res.Truncated != 1 {
		t.Errorf("Truncated = %d, want 1", res.Truncated)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the output is not valid UTF-8: %q", got)
	}
	inner := textOf(t, got)
	if n := utf8.RuneCountInString(inner); n != MaxRunes {
		t.Errorf("the value is %d runes, want %d", n, MaxRunes)
	}
	if len(inner) != MaxRunes*2 {
		t.Errorf("the value is %d bytes for %d runes, so it was cut by bytes", len(inner), MaxRunes)
	}
}

// TestAnAttributeValueIsEncodedNotEscaped. SetAttribute takes raw attribute
// source, so a literal value has to be encoded first: without that, a header
// holding "&amp;" sets an attribute a browser reads as "&".
func TestAnAttributeValueIsEncodedNotEscaped(t *testing.T) {
	got, _ := greet(t, `<div data-greet-title="X-Name"></div>`, header("&amp; a"))
	if !strings.Contains(got, `title="&amp;amp; a"`) {
		t.Errorf("got %q, want the ampersand encoded", got)
	}
	// Read back, the attribute is the five characters the header held: the value
	// survives rather than being decoded on the way through.
	attrs := attributes(t, got, "div")
	if attrs["title"] != "&amp;amp; a" {
		t.Errorf("title reads back as %q", attrs["title"])
	}
}

// TestAMissingHeaderGetsTheFallback, so a page never says "Hello, ".
func TestAMissingHeaderGetsTheFallback(t *testing.T) {
	for _, h := range []http.Header{{}, header(""), header("   "), header("\x00\x01")} {
		got, res := greet(t, `<p><span data-greet="X-Name">x</span></p>`, h)
		if !strings.Contains(got, ">"+Fallback+"<") {
			t.Errorf("%v: got %q, want the fallback", h, got)
		}
		if res.Fallbacks == 0 {
			t.Errorf("%v: Fallbacks = 0", h)
		}
	}
	// A marker with no header name at all is a fallback too, not an error.
	got, res := greet(t, `<p><span data-greet="">x</span></p>`, header("Ada"))
	if !strings.Contains(got, ">"+Fallback+"<") || res.Fallbacks != 1 {
		t.Errorf("got %q, %v", got, res)
	}
}

// TestInsideATitleThereAreNoElements, which is why the title marker is an
// attribute on the title itself. Measured rather than assumed: this is the reason
// for the shape of the whole marker vocabulary.
func TestInsideATitleThereAreNoElements(t *testing.T) {
	const doc = `<title>Hi <span data-greet="X-Name">there</span></title>`
	got, res := greet(t, doc, header("Ada"))
	if got != doc {
		t.Errorf("\n got %q\nwant it unchanged: a span in a title is text", got)
	}
	if res.Content != 0 {
		t.Errorf("Content = %d, want 0", res.Content)
	}
	// The same marker as an attribute on the title works.
	got, res = greet(t, `<title data-greet-append="X-Name">Hi </title>`, header("Ada"))
	if want := `<title>Hi Ada</title>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Content != 1 {
		t.Errorf("Content = %d, want 1", res.Content)
	}
}

// TestAContentMarkerInAScriptIsRefused. Text escapes for markup, and a script's
// content is not markup: "a < b" would arrive in the script source as "a &lt; b",
// which is a syntax error rather than an escape.
func TestAContentMarkerInAScriptIsRefused(t *testing.T) {
	for _, tag := range []string{"script", "style", "xmp", "iframe", "noembed", "noscript"} {
		doc := "<" + tag + ` data-greet="X-Name">var a = 1</` + tag + ">"
		got, res := greet(t, doc, header("Ada"))
		if got != doc {
			t.Errorf("<%s>\n got %q\nwant it unchanged", tag, got)
		}
		if res.Refused != 1 {
			t.Errorf("<%s>: Refused = %d, want 1", tag, res.Refused)
		}
	}
	// A textarea does decode references, so Text means what it says there and the
	// marker is honoured.
	got, res := greet(t, `<textarea data-greet="X-Name">x</textarea>`, header("a < b"))
	if want := `<textarea data-greet="X-Name">a &lt; b</textarea>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Refused != 0 {
		t.Errorf("Refused = %d for a textarea, want 0", res.Refused)
	}
}

// TestGreetingTwiceChangesNothing. The replacing markers stay, because filling
// them again gives the same page; the appending one removes itself, because a
// second pass would append the greeting a second time. Running the pass over its
// own output has to be a no-op either way.
func TestGreetingTwiceChangesNothing(t *testing.T) {
	for _, tc := range hostile {
		once, _ := greet(t, page, header(tc.value))
		twice, _ := greet(t, once, header(tc.value))
		if twice != once {
			t.Errorf("%s\n once %q\ntwice %q", tc.what, once, twice)
		}
	}
}

// TestChunkInvariance.
func TestChunkInvariance(t *testing.T) {
	h := header("Ada & <b>friends</b>")
	want, _ := greet(t, page, h)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		g := &greeter{header: h}
		w, err := lolhtml.NewWriter(&out, g.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(page); i += size {
			end := min(i+size, len(page))
			if _, err := w.Write([]byte(page[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// Helpers that read the output back rather than eyeballing it.

func tags(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		if !e.CanHaveContent() {
			return nil
		}
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			b.WriteString("</" + t.Name() + ">")
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func attributes(t *testing.T, doc, tag string) map[string]string {
	t.Helper()
	got := map[string]string{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
		for _, a := range e.AttributeList() {
			got[a.Name] = a.Value
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return got
}

func scriptBody(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnText("script", func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func textOf(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnText("span", func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

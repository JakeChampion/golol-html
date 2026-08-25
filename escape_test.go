package lolhtml_test

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// escapeCorpus is the input to most of these tests. It is deliberately
// unpleasant: the characters that matter, in the combinations that break a
// naive escaper. Everything here is valid UTF-8, because some of the tests put
// it through the library, which rejects anything else.
var escapeCorpus = []string{
	"", "plain", " ", "a b c",
	"&", "<", ">", `"`, "'",
	"&&", "<<", `""`, "''",
	"&amp;", "&#38;", "&lt;", "&notareference;", "&",
	"a<b", "a>b", "a&b", `a"b`, "a'b",
	"<script>alert(1)</script>",
	`" onload="alert(1)`,
	`' onload='alert(1)`,
	`" onload=alert(1) x="`,
	"--><script>x</script><!--",
	"]]>", "-->", "<!--", "<![CDATA[x]]>",
	`<a href="x">y</a>`,
	"\x00", "\n", "\t", "\r\n",
	"\u00a0", "\u2028", "\ufeff", "\u00e9", "\u65e5\u672c\u8a9e", "\U0001d11e",
	strings.Repeat("&", 100),
	strings.Repeat("x", 100) + "<" + strings.Repeat("y", 100),
}

// TestEscapeTextMatchesContentTypeText is the claim that makes EscapeText worth
// having: it is the same transformation the library applies for ContentType
// Text, so a caller who has to build markup keeps the guarantee they would have
// had from passing Text. Compared against the Rust implementation rather than
// against a table of expected strings, so the two cannot drift apart.
func TestEscapeTextMatchesContentTypeText(t *testing.T) {
	for _, s := range escapeCorpus {
		byLibrary, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.SetInnerContent(s, lolhtml.Text)
			}))
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		byHand, err := lolhtml.RewriteString(`<x></x>`,
			lolhtml.OnElement("x", func(e *lolhtml.Element) error {
				return e.SetInnerContent(lolhtml.EscapeText(s), lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if byLibrary != byHand {
			t.Errorf("EscapeText(%q) does not match ContentType Text:\n  Text: %q\n  ours: %q",
				s, byLibrary, byHand)
		}
	}
}

// TestEscapeAttributeCreatesNoAttribute is the property that matters: whatever
// goes in, the element comes out carrying exactly the one attribute that was
// written. The library's own parser decides what an attribute is, which is the
// only judge worth asking.
func TestEscapeAttributeCreatesNoAttribute(t *testing.T) {
	for _, s := range escapeCorpus {
		for _, quote := range []string{`"`, `'`} {
			markup := `<a x=` + quote + lolhtml.EscapeAttribute(s) + quote + `>`

			var names []string
			var tag string
			_, err := lolhtml.RewriteString(markup,
				lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					tag = e.TagName()
					for name := range e.Attributes() {
						names = append(names, name)
					}
					return nil
				}))
			if err != nil {
				t.Fatalf("%q: %v", s, err)
			}
			if tag != "a" {
				t.Errorf("EscapeAttribute(%q) between %s quotes changed the element to %q: %s",
					s, quote, tag, markup)
			}
			if len(names) != 1 || names[0] != "x" {
				t.Errorf("EscapeAttribute(%q) between %s quotes produced attributes %v: %s",
					s, quote, names, markup)
			}
		}
	}
}

// TestEscapeAttributeSurvivesADecode is the other half: not merely that no
// attribute was created, but that the value a parser reads back is the value
// that went in. Decoded with the standard library's html.UnescapeString, so the
// judge of what the references mean is not this package.
//
// Note what this does not claim. SetAttribute is not the same function: its
// argument is raw attribute-value source, so it escapes only the double quote
// and passes "&amp;" through as a reference. EscapeAttribute's argument is a
// literal value. Both are right for their own contract, and confusing them is
// how "&amp;" in a value becomes "&" in the output.
func TestEscapeAttributeSurvivesADecode(t *testing.T) {
	for _, s := range escapeCorpus {
		if strings.HasPrefix(s, "\ufeff") {
			// Documented on Element.Attribute: lol-html decodes on the way out
			// and its decoder drops a leading byte-order mark, so a value that
			// starts with U+FEFF reads back without it. The value is still
			// serialised faithfully, so this is the reader's behaviour and not
			// the escaper's.
			continue
		}
		for _, quote := range []string{`"`, `'`} {
			markup := `<a x=` + quote + lolhtml.EscapeAttribute(s) + quote + `>`

			var raw string
			var ok bool
			if _, err := lolhtml.RewriteString(markup,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					raw, ok = e.Attribute("x")
					return nil
				})); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
			if !ok {
				t.Errorf("EscapeAttribute(%q) between %s quotes lost the attribute: %s",
					s, quote, markup)
				continue
			}
			if got := stdhtml.UnescapeString(raw); got != s {
				t.Errorf("EscapeAttribute(%q) between %s quotes reads back as %q",
					s, quote, got)
			}
		}
	}
}

// TestEscapeTextSurvivesADecode is the same round trip for text. The library
// reports text as raw source too, so the decode is on the same footing.
func TestEscapeTextSurvivesADecode(t *testing.T) {
	for _, s := range escapeCorpus {
		if s == "" {
			continue // no text node to hand to a handler
		}
		var seen strings.Builder
		if _, err := lolhtml.RewriteString(`<x>`+lolhtml.EscapeText(s)+`</x>`,
			lolhtml.OnText("x", func(tc *lolhtml.TextChunk) error {
				seen.WriteString(tc.Text())
				return nil
			})); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got := stdhtml.UnescapeString(seen.String()); got != s {
			t.Errorf("EscapeText(%q) reads back as %q", s, got)
		}
	}
}

// TestEscapeExactly says which bytes each function touches, so a later change
// that quietly widens or narrows the set fails here rather than in somebody's
// output. The set for EscapeText was measured against ContentType Text one code
// point at a time, not chosen.
func TestEscapeExactly(t *testing.T) {
	for _, tt := range []struct {
		in, text, attr string
	}{
		{"&", "&amp;", "&amp;"},
		{"<", "&lt;", "&lt;"},
		{">", "&gt;", "&gt;"},
		{`"`, `"`, "&quot;"},
		{"'", "'", "&#39;"},
		{"`", "`", "`"},
		{"=", "=", "="},
		{"/", "/", "/"},
		{" ", " ", " "},
		{"\x00", "\x00", "\x00"},
		{"a&b<c>d\"e'f", "a&amp;b&lt;c&gt;d\"e'f", "a&amp;b&lt;c&gt;d&quot;e&#39;f"},
	} {
		if got := lolhtml.EscapeText(tt.in); got != tt.text {
			t.Errorf("EscapeText(%q) = %q, want %q", tt.in, got, tt.text)
		}
		if got := lolhtml.EscapeAttribute(tt.in); got != tt.attr {
			t.Errorf("EscapeAttribute(%q) = %q, want %q", tt.in, got, tt.attr)
		}
	}
}

// TestEscapingTwiceIsNotTheSameAsOnce is not a defect, it is the rule. Written
// down because "escape it again, just in case" is a natural instinct and it
// corrupts the text.
func TestEscapingTwiceIsNotTheSameAsOnce(t *testing.T) {
	once := lolhtml.EscapeText("a<b")
	if twice := lolhtml.EscapeText(once); twice != "a&amp;lt;b" {
		t.Errorf("EscapeText(EscapeText(%q)) = %q, want %q", "a<b", twice, "a&amp;lt;b")
	}
}

// TestEscapeIsSafeOnInvalidUTF8: every byte either function rewrites is ASCII,
// so it can never be the continuation byte of a multi-byte sequence, and a
// byte-wise escaper is safe on input that is not valid UTF-8. Pure Go only -
// the library rejects invalid UTF-8, so this input cannot be round-tripped
// through it.
func TestEscapeIsSafeOnInvalidUTF8(t *testing.T) {
	want := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	for _, s := range []string{"\xff&\xfe", "\x80<\x81", "a\xc3", "\xed\xa0\x80&", "\xff", ""} {
		if got := lolhtml.EscapeText(s); got != want.Replace(s) {
			t.Errorf("EscapeText(%q) = %q, want %q", s, got, want.Replace(s))
		}
	}
}

// TestEscapeAllocates is the reason these are worth having over a
// strings.NewReplacer at each call site: nothing when there is nothing to do,
// and one allocation when there is, however many escapes it holds.
func TestEscapeAllocates(t *testing.T) {
	// Same reason as every other allocation assertion here: -asan replaces the
	// allocator, so the counts under it are the sanitizer's rather than this
	// package's. Missing this is what put the sanitize job red on the first run
	// of this branch, two turns after the rule was written down.
	requireRealAllocationCounts(t)

	const clean = "https://example.com/a/b/c?d=1"
	if got := testing.AllocsPerRun(allocRuns, func() { sinkString = lolhtml.EscapeText(clean) }); got != 0 {
		t.Errorf("EscapeText allocated %v times for a string with nothing to escape", got)
	}
	if got := testing.AllocsPerRun(allocRuns, func() { sinkString = lolhtml.EscapeAttribute(clean) }); got != 0 {
		t.Errorf("EscapeAttribute allocated %v times for a string with nothing to escape", got)
	}

	dirty := strings.Repeat(`a&b<c>d"e`, 20)
	escapes := strings.Count(dirty, "&") + strings.Count(dirty, "<") +
		strings.Count(dirty, ">") + strings.Count(dirty, `"`)
	if got := testing.AllocsPerRun(allocRuns, func() { sinkString = lolhtml.EscapeAttribute(dirty) }); got != 1 {
		t.Errorf("EscapeAttribute allocated %v times for %d escapes, want exactly 1", got, escapes)
	}
}

// sinkString keeps the results above from being optimised away: assigning to a
// package variable makes the string escape, which is the point.
var sinkString string

// TestEscapeAttributeIsNotWhatSetAttributeApplies pins the difference between the
// two, which the package documentation used to describe as the same thing.
//
// SetAttribute escapes the double quote alone, because the library writes the
// quotes. EscapeAttribute escapes five characters, because the markup being built
// by hand might use single quotes and an unescaped ampersand in it could begin a
// reference the caller did not write. Both are right for their job, and a value
// moved from one to the other is escaped twice.
func TestEscapeAttributeIsNotWhatSetAttributeApplies(t *testing.T) {
	tests := []struct {
		value string
		set   string // what SetAttribute writes
		esc   string // what EscapeAttribute returns
	}{
		{`plain`, `plain`, `plain`},
		{`a"b`, `a&quot;b`, `a&quot;b`},
		{`a'b`, `a'b`, `a&#39;b`},
		{`a<b`, `a<b`, `a&lt;b`},
		{`a>b`, `a>b`, `a&gt;b`},
		{`a&b`, `a&b`, `a&amp;b`},
		// The row that bites: a value read from a document is already source.
		{`a&amp;b`, `a&amp;b`, `a&amp;amp;b`},
		// Neither escapes whitespace, which is why the value has to be quoted.
		{"a b", "a b", "a b"},
		{"a\nb", "a\nb", "a\nb"},
	}
	differed := 0
	for _, tt := range tests {
		if got := lolhtml.EscapeAttribute(tt.value); got != tt.esc {
			t.Errorf("EscapeAttribute(%q) = %q, want %q", tt.value, got, tt.esc)
		}
		if got := writtenAttributeValue(t, tt.value); got != tt.set {
			t.Errorf("SetAttribute(%q) wrote %q, want %q", tt.value, got, tt.set)
		}
		if tt.set != tt.esc {
			differed++
		}
	}
	if differed < 5 {
		t.Errorf("only %d of the values distinguished the two; this test exists to "+
			"pin the difference", differed)
	}
}

// writtenAttributeValue returns the value SetAttribute put in the output, read
// back out of the markup.
func writtenAttributeValue(t *testing.T, value string) string {
	t.Helper()
	out, err := lolhtml.RewriteString(`<p></p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetAttribute("x", value)
	}))
	if err != nil {
		t.Fatalf("SetAttribute(%q): %v", value, err)
	}
	const open = `x="`
	i := strings.Index(out, open)
	if i < 0 {
		t.Fatalf("no attribute in %q", out)
	}
	rest := out[i+len(open):]
	// The double quote is the one character SetAttribute escapes, so the first
	// one left is the terminator.
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated attribute in %q", out)
	}
	return rest[:j]
}

// And the consequence, as a round trip: through SetAttribute a value survives,
// through EscapeAttribute it gains an escape.
func TestMovingAValueBetweenTheTwoEscapesItTwice(t *testing.T) {
	const doc = `<a href="/x?a=1&amp;b=2">t</a>`

	// SetAttribute: read and write it back, unchanged.
	same, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("href")
		return e.SetAttribute("href", v)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if same != doc {
		t.Errorf("through SetAttribute: got %q, want %q", same, doc)
	}

	// EscapeAttribute on the same value, into markup built by hand.
	built, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("href")
		return e.Replace(`<a href="`+lolhtml.EscapeAttribute(v)+`">t</a>`, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built, "&amp;amp;") {
		t.Errorf("expected the reference to be escaped twice: %q", built)
	}
}

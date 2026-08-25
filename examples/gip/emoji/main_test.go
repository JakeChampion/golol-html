package main

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

func expandDoc(t *testing.T, doc, encoding string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Expand(&b, strings.NewReader(doc), encoding)
	if err != nil {
		t.Fatalf("Expand(%q, %q): %v", doc, encoding, err)
	}
	return b.String(), res
}

func TestExpanding(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		want  string
		total int
	}{
		{"one shortcode", `<p>:wave:</p>`, "<p>\U0001F44B</p>", 1},
		{"in a sentence", `<p>Hi :wave: there</p>`, "<p>Hi \U0001F44B there</p>", 1},
		{"two of them", `<p>:wave::tada:</p>`, "<p>\U0001F44B\U0001F389</p>", 2},
		{"the same twice", `<p>:wave: :wave:</p>`, "<p>\U0001F44B \U0001F44B</p>", 2},
		{"unknown is left alone", `<p>:nosuch:</p>`, `<p>:nosuch:</p>`, 0},
		{"not a shortcode", `<p>a:b</p>`, `<p>a:b</p>`, 0},
		{"empty shortcode", `<p>::</p>`, `<p>::</p>`, 0},
		// A multi-rune emoji is one substitution and several characters.
		{"skin tone", `<p>:thumbsup_t2:</p>`, "<p>\U0001F44D\U0001F3FC</p>", 1},
		{"zero-width joiner", `<p>:family:</p>`, "<p>\U0001F468‍\U0001F469‍\U0001F467</p>", 1},
		{"variation selector", `<p>:heart:</p>`, "<p>❤️</p>", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := expandDoc(t, tt.doc, "")
			if out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
			if res.Total() != tt.total {
				t.Errorf("Total = %d, want %d", res.Total(), tt.total)
			}
			if !utf8.ValidString(out) {
				t.Errorf("the output is not valid UTF-8: %q", out)
			}
		})
	}
}

// In a document whose encoding cannot hold an emoji, the library emits a numeric
// character reference. That is the documented fallback and the reason this
// program reports which form its output is in.
func TestALegacyEncodingGivesReferences(t *testing.T) {
	for _, enc := range []string{"windows-1252", "iso-8859-2", "windows-1251"} {
		out, res := expandDoc(t, `<p>Hi :wave:</p>`, enc)
		if res.Total() != 1 {
			t.Fatalf("%s: expanded %d, want 1", enc, res.Total())
		}
		if !res.AsReferences {
			t.Errorf("%s: AsReferences = false", enc)
		}
		if want := `<p>Hi &#128075;</p>`; out != want {
			t.Errorf("%s: got %q, want %q", enc, out, want)
		}
		// The reference is the character, to anything that decodes references.
		if got := decodeRefs(out); !strings.Contains(got, "\U0001F44B") {
			t.Errorf("%s: %q does not decode to the emoji", enc, out)
		}
	}

	// In UTF-8 it is the character itself.
	out, res := expandDoc(t, `<p>Hi :wave:</p>`, "utf-8")
	if res.AsReferences {
		t.Error("AsReferences = true for utf-8")
	}
	if !strings.Contains(out, "\U0001F44B") {
		t.Errorf("got %q, want the emoji itself", out)
	}
}

func decodeRefs(s string) string {
	// Only the numeric form this test produces, decoded by hand so the test does
	// not depend on a decoder to say what the library wrote.
	return strings.ReplaceAll(s, "&#128075;", "\U0001F44B")
}

// The two positions where an unrepresentable character is refused rather than
// encoded are the ones this program does not touch, which is why it cannot fail
// in a legacy encoding. A shortcode in a comment or in a tag name is left alone.
func TestThePositionsThatWouldFailAreNotTouched(t *testing.T) {
	for _, doc := range []string{
		`<!-- :wave: --><p>ok</p>`,
		`<p title=":wave:">ok</p>`,
	} {
		for _, enc := range []string{"", "windows-1252"} {
			out, res := expandDoc(t, doc, enc)
			if !strings.Contains(out, ":wave:") {
				t.Errorf("%q at %q: the shortcode was expanded: %q", doc, enc, out)
			}
			if res.Total() != 0 {
				t.Errorf("%q at %q: expanded %d", doc, enc, res.Total())
			}
		}
	}
}

func TestSkippedElements(t *testing.T) {
	for _, doc := range []string{
		`<code>:wave:</code>`,
		`<pre>:wave:</pre>`,
		`<script>var s = ":wave:"</script>`,
		`<style>p{content:":wave:"}</style>`,
		`<textarea>:wave:</textarea>`,
		`<title>:wave:</title>`,
	} {
		out, res := expandDoc(t, doc, "")
		if out != doc || res.Total() != 0 {
			t.Errorf("%q became %q (%d expansions)", doc, out, res.Total())
		}
	}
}

// Text with no shortcode comes through byte for byte, references included.
func TestTextWithoutShortcodesIsUnchanged(t *testing.T) {
	for _, doc := range []string{
		`<p>caf&eacute; and &amp; and &lt;script&gt;</p>`,
		`<p>See also <b>this</b> and that.</p>`,
		`<p>ratio 1:2 and time 10:30</p>`,
	} {
		out, res := expandDoc(t, doc, "")
		if res.Total() != 0 {
			t.Errorf("%q expanded %d: %q", doc, res.Total(), out)
			continue
		}
		if out != doc {
			t.Errorf("got %q, want %q", out, doc)
		}
	}
}

// Nothing inserted can become markup, because every insertion is Text.
func TestTheTagsNeverChange(t *testing.T) {
	for _, doc := range []string{
		`<p>:wave:</p>`,
		`<p>&lt;script&gt;:wave:&lt;/script&gt;</p>`,
		`<p>:wave: &amp; :tada:</p>`,
		`<div><p>:wave:</p><span>:tada:</span></div>`,
	} {
		out, _ := expandDoc(t, doc, "")
		if before, after := tagSequence(t, doc), tagSequence(t, out); before != after {
			t.Errorf("%q: tags went from %s to %s: %q", doc, before, after, out)
		}
	}
}

// Running it again changes nothing, because an emoji is not a shortcode.
func TestExpandingTwiceChangesNothing(t *testing.T) {
	const doc = `<p>Hi :wave: and :tada:</p>`
	once, res1 := expandDoc(t, doc, "")
	if res1.Total() != 2 {
		t.Fatalf("first pass expanded %d", res1.Total())
	}
	twice, res2 := expandDoc(t, once, "")
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Total() != 0 {
		t.Errorf("the second pass expanded %d more", res2.Total())
	}
}

// A shortcode split across chunks is still a shortcode, which is why matching is
// over the node.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>Hi :wave: and :heart: and :nosuch:.</p>`,
		`<p>:family:</p>`,
		`<code>:wave:</code><p>:tada:</p>`,
		`<p>caf&eacute; :wave;</p>`,
		`<p>nothing here</p>`,
	}
	for _, enc := range []string{"", "windows-1252"} {
		for _, doc := range docs {
			want, wantRes := expandDoc(t, doc, enc)
			for _, n := range []int{1, 2, 3, 5, 64} {
				var b strings.Builder
				res, err := Expand(&b, &chunked{s: doc, n: n}, enc)
				if err != nil {
					t.Fatalf("writes of %d: %v", n, err)
				}
				if b.String() != want || res.Total() != wantRes.Total() {
					t.Fatalf("%q at %q writes of %d:\n got %q (%d)\nwant %q (%d)",
						doc, enc, n, b.String(), res.Total(), want, wantRes.Total())
				}
			}
		}
	}
}

// Every entry in the table has to be valid UTF-8, because the library takes
// inserted content as UTF-8 and would refuse it otherwise.
func TestTheTableIsValidUTF8(t *testing.T) {
	if len(table) == 0 {
		t.Fatal("the table is empty")
	}
	for name, v := range table {
		if !utf8.ValidString(v) {
			t.Errorf("the entry for %q is not valid UTF-8: % x", name, v)
		}
		if v == "" {
			t.Errorf("the entry for %q is empty", name)
		}
		// And it must not contain a colon, or expanding would produce something
		// that expands again.
		if strings.Contains(v, ":") {
			t.Errorf("the entry for %q contains a colon: %q", name, v)
		}
	}
}

func tagSequence(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

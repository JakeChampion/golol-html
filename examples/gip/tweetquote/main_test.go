package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const embed = `<blockquote class="twitter-tweet"><p lang="en" dir="ltr">` +
	`Text with <a href="https://twitter.com/hashtag/rust">#rust</a> in it.</p>` +
	`&mdash; Some One (@someone) ` +
	`<a href="https://twitter.com/someone/status/1234567890">March 1, 2024</a>` +
	`</blockquote>`

var corpus = []string{
	embed,
	embed + `<script async src="https://platform.twitter.com/widgets.js"></script>`,
	`<blockquote class="twitter-tweet"><p>no permalink</p></blockquote>`,
	`<blockquote>an ordinary quotation</blockquote>`,
	`<blockquote class="twitter-video"><p>v</p>&mdash; V (@vids) <a href="https://x.com/vids/statuses/99">d</a></blockquote>`,
	`<blockquote class="twitter-tweet"/><p>after a self-closing one</p>`,
	`<blockquote class="twitter-tweet"><a href="https://twitter.com/a/status/1">x</a><a href="https://twitter.com/b/status/2">y</a></blockquote>`,
	`<script src="https://example.com/other.js"></script>`,
	`<p>nothing at all</p>`,
	``,
}

func chunked(in string, n int) (string, error) {
	q := &quoter{dropScript: true, klass: "tweetquote"}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, q.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
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
		whole, _, err := quoteString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 17} {
			got, err := chunked(doc, n)
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

// TestIdempotent is not a formality here: the program adds content inside the
// element it matches, so a second pass has to recognise its own work. It does
// that by seeing the footer, which arrives before the end tag that would add
// another.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := quoteString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, q, err := quoteString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if q.converted != 0 {
			t.Errorf("the second pass of %q converted %d", doc, q.converted)
		}
	}
}

func TestTheEmbedBecomesAQuotation(t *testing.T) {
	got, q, err := quoteString(embed +
		`<script async src="https://platform.twitter.com/widgets.js"></script>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.converted != 1 || q.scripts != 1 {
		t.Fatalf("converted=%d scripts=%d, want 1 and 1", q.converted, q.scripts)
	}
	if strings.Contains(got, "widgets.js") {
		t.Errorf("the widget script survived, so the widget will undo this: %s", got)
	}
	for _, want := range []string{
		`class="twitter-tweet tweetquote"`,
		`<span class="tweetquote-name">Some One</span>`,
		`href="https://twitter.com/someone"`,
		`href="https://twitter.com/someone/status/1234567890" rel="noopener nofollow">permalink`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain %q: %s", want, got)
		}
	}
	// The tweet's own text is left exactly as it was.
	if !strings.Contains(got, `Text with <a href="https://twitter.com/hashtag/rust">#rust</a> in it.`) {
		t.Errorf("the tweet text was altered: %s", got)
	}
}

// TestTheNameIsReadFromDecodedText is the mistake this program made first. Text
// arrives as raw source, so the embed's separator is the seven characters
// "&mdash;" and not an em dash; matching on the em dash found nothing and the
// fallback captured the whole tweet as the author's name.
func TestTheNameIsReadFromDecodedText(t *testing.T) {
	for _, tt := range []struct{ text, handle, want string }{
		{"the tweet&mdash; Some One (@someone) date", "someone", "Some One"},
		{"the tweet\u2014 Some One (@someone) date", "someone", "Some One"},
		{"the tweet&#8212; Some One (@someone) date", "someone", "Some One"},
		{"a &mdash; b &mdash; Real Name (@x) date", "x", "Real Name"},

		// No separator, so no name: the alternative is calling the tweet's own
		// text the author's name.
		{"the tweet (@someone) date", "someone", ""},
		// The handle is not in the text at all.
		{"&mdash; Some One (@other) date", "someone", ""},
		{"", "someone", ""},
		{"&mdash; Some One (@someone)", "", ""},
	} {
		if got := readName(tt.text, tt.handle); got != tt.want {
			t.Errorf("readName(%q, %q) = %q, want %q", tt.text, tt.handle, got, tt.want)
		}
	}
}

// TestStatusURL: the permalink is where the handle comes from, and a handle that
// came out wrong is put straight back into a URL.
func TestStatusURL(t *testing.T) {
	for _, tt := range []struct {
		href           string
		handle, id     string
		wantRecognised bool
	}{
		{href: "https://twitter.com/someone/status/123", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://x.com/someone/status/123", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://www.twitter.com/someone/statuses/123", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://mobile.twitter.com/someone/status/123", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://twitter.com/someone/status/123/photo/1", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://twitter.com/someone/status/123?s=20", handle: "someone", id: "123", wantRecognised: true},
		{href: "https://twitter.com/a_b_9/status/1", handle: "a_b_9", id: "1", wantRecognised: true},

		// Not a status permalink.
		{href: "https://twitter.com/someone"},
		{href: "https://twitter.com/hashtag/rust"},
		{href: "https://twitter.com/someone/status/"},
		{href: "https://twitter.com/someone/status/abc"},
		{href: "https://twitter.com/someone/likes/123"},
		{href: "https://example.com/someone/status/123"},
		{href: "https://twitter.com.evil.example/a/status/1"},
		// A handle longer than Twitter allows, or with a character it does not.
		{href: "https://twitter.com/sixteencharacter/status/1"},
		{href: "https://twitter.com/has-a-dash/status/1"},
		{href: ""},
	} {
		handle, id, ok := statusURL(tt.href)
		if ok != tt.wantRecognised {
			t.Errorf("statusURL(%q) recognised=%v, want %v", tt.href, ok, tt.wantRecognised)
			continue
		}
		if ok && (handle != tt.handle || id != tt.id) {
			t.Errorf("statusURL(%q) = %q/%q, want %q/%q", tt.href, handle, id, tt.handle, tt.id)
		}
	}
}

// TestTheLastStatusLinkWins: the embed puts the permalink last, as the date, and
// a link earlier in the blockquote belongs to the tweet's text.
func TestTheLastStatusLinkWins(t *testing.T) {
	got, _, err := quoteString(`<blockquote class="twitter-tweet">` +
		`<p>see <a href="https://twitter.com/other/status/111">this</a></p>` +
		`&mdash; Some One (@someone) <a href="https://twitter.com/someone/status/222">date</a>` +
		`</blockquote>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `class="tweetquote-permalink" href="https://twitter.com/someone/status/222"`) {
		t.Errorf("the wrong link was cited: %s", got)
	}
}

// TestNestedEmbedsGetTheirOwnAttribution: a quote tweet nests one embed inside
// another, and an anchor belongs to the innermost open blockquote.
func TestNestedEmbedsGetTheirOwnAttribution(t *testing.T) {
	got, q, err := quoteString(`<blockquote class="twitter-tweet"><p>outer</p>` +
		`<blockquote class="twitter-tweet"><p>inner</p>` +
		`&mdash; Inner Name (@inner) <a href="https://twitter.com/inner/status/1">d</a>` +
		`</blockquote>` +
		`&mdash; Outer Name (@outer) <a href="https://twitter.com/outer/status/2">d</a>` +
		`</blockquote>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.converted != 2 {
		t.Fatalf("converted=%d, want 2: %s", q.converted, got)
	}
	if !strings.Contains(got, `>Inner Name</span>`) || !strings.Contains(got, `>Outer Name</span>`) {
		t.Errorf("both authors should be named: %s", got)
	}
	if strings.Index(got, "inner/status/1") > strings.Index(got, "outer/status/2") {
		t.Errorf("the inner attribution should close first: %s", got)
	}
}

// TestAnEmbedWithNoPermalinkIsLeftAlone: there is nothing to cite, and inventing
// one would be worse than leaving the fallback as it is.
func TestAnEmbedWithNoPermalinkIsLeftAlone(t *testing.T) {
	const in = `<blockquote class="twitter-tweet"><p>text with no link</p></blockquote>`
	got, q, err := quoteString(in)
	if err != nil {
		t.Fatal(err)
	}
	if q.converted != 0 || total(q.skipped) != 1 {
		t.Errorf("converted=%d skipped=%v", q.converted, q.skipped)
	}
	if strings.Contains(got, "<footer") {
		t.Errorf("an attribution was added with nothing to attribute: %s", got)
	}
}

// TestOnlyTheWidgetScriptGoes: removing every script would break the page.
func TestOnlyTheWidgetScriptGoes(t *testing.T) {
	got, q, err := quoteString(
		`<script src="https://example.com/app.js"></script>` +
			`<script src="https://platform.twitter.com/widgets.js"></script>` +
			`<script src="https://platform.x.com/widgets.js"></script>` +
			`<script>inline()</script>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.scripts != 2 {
		t.Errorf("scripts-removed=%d, want 2", q.scripts)
	}
	if !strings.Contains(got, "app.js") || !strings.Contains(got, "inline()") {
		t.Errorf("a script that is not the widget was removed: %s", got)
	}
	if strings.Contains(got, "widgets.js") {
		t.Errorf("a widget script survived: %s", got)
	}
}

func TestTheScriptCanBeKept(t *testing.T) {
	keep := func(q *quoter) { q.dropScript = false }
	got, q, err := quoteString(
		`<script src="https://platform.twitter.com/widgets.js"></script>`, keep)
	if err != nil {
		t.Fatal(err)
	}
	if q.scripts != 0 || !strings.Contains(got, "widgets.js") {
		t.Errorf("-drop-script=false still removed the script: %s", got)
	}
}

// TestNothingBecomesAnAttribute: the footer is assembled as a string, so this
// program escapes its own values. The parser is asked what the attributes are,
// because searching the serialised output for "onerror=" fails on output that is
// correct.
func TestNothingBecomesAnAttribute(t *testing.T) {
	// A handle cannot hold a quote - validHandle refuses it - so the hostile
	// value has to arrive as the author's name, which is free text.
	got, q, err := quoteString(`<blockquote class="twitter-tweet"><p>x</p>` +
		`&mdash; <b>Name</b>" onmouseover="alert(1)" x=" (@someone) ` +
		`<a href="https://twitter.com/someone/status/1">d</a></blockquote>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.converted != 1 {
		t.Fatalf("converted=%d: %s", q.converted, got)
	}

	var attrs []string
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnElement("footer *, footer", func(e *lolhtml.Element) error {
			for name := range e.Attributes() {
				attrs = append(attrs, name)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	for _, name := range attrs {
		if strings.HasPrefix(name, "on") {
			t.Errorf("the attribution carries %q; attributes were %v", name, attrs)
		}
	}
}

func TestEscapers(t *testing.T) {
	for _, tt := range []struct{ in, text, attr string }{
		{"", "", ""},
		{"plain", "plain", "plain"},
		{"a&b", "a&amp;b", "a&amp;b"},
		{"a<b>c", "a&lt;b&gt;c", "a&lt;b&gt;c"},
		{`a"b`, `a"b`, "a&quot;b"},
		{"a'b", "a'b", "a&#39;b"},
	} {
		if got := escapeText(tt.in); got != tt.text {
			t.Errorf("escapeText(%q) = %q, want %q", tt.in, got, tt.text)
		}
		if got := escapeAttr(tt.in); got != tt.attr {
			t.Errorf("escapeAttr(%q) = %q, want %q", tt.in, got, tt.attr)
		}
	}
}

func TestValidHandle(t *testing.T) {
	for _, good := range []string{"a", "a_b", "A1", "_", "fifteencharact"} {
		if !validHandle(good) {
			t.Errorf("validHandle(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "sixteencharacter", "a-b", "a.b", "a b", "a/b", "é"} {
		if validHandle(bad) {
			t.Errorf("validHandle(%q) = true", bad)
		}
	}
}

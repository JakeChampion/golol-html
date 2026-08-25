package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func linkify(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Linkify(&b, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Linkify(%q): %v", doc, err)
	}
	return b.String(), res
}

func TestLinking(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"plain url", `<p>https://x.example</p>`,
			`<p><a href="https://x.example">https://x.example</a></p>`},
		{"url in a sentence", `<p>See https://x.example today</p>`,
			`<p>See <a href="https://x.example">https://x.example</a> today</p>`},
		{"http", `<p>http://x.example</p>`,
			`<p><a href="http://x.example">http://x.example</a></p>`},
		{"two urls", `<p>https://a.example https://b.example</p>`,
			`<p><a href="https://a.example">https://a.example</a> <a href="https://b.example">https://b.example</a></p>`},
		{"no url", `<p>ordinary text</p>`, `<p>ordinary text</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, _ := linkify(t, tt.doc); out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
		})
	}
}

// The two escapers, on one URL, in their two positions. Getting this wrong is the
// thing the program exists to demonstrate: the attribute needs the ampersand
// escaped and so does the text, but they are different functions and one of them
// escapes the quotes as well.
func TestBothEscapersOnOneURL(t *testing.T) {
	const doc = `<p>https://x.example/a?b=1&amp;c=2</p>`
	out, res := linkify(t, doc)
	if res.Linked != 1 {
		t.Fatalf("linked %d, want 1: %q", res.Linked, out)
	}
	// The ampersand is escaped in both positions, once each.
	if want := `<p><a href="https://x.example/a?b=1&amp;c=2">https://x.example/a?b=1&amp;c=2</a></p>`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if strings.Contains(out, "&amp;amp;") {
		t.Errorf("something was escaped twice: %q", out)
	}
	// And the href a browser would read is the URL that was in the text.
	if got := hrefOf(t, out); got != `https://x.example/a?b=1&c=2` {
		t.Errorf("href reads back as %q", got)
	}
}

// A URL whose text contains a quote must not be able to end the attribute or add
// one. The check is the anchor's attributes, read back: exactly one, named href.
// A substring search for "onload=" is not the check - the characters may perfectly
// well appear in the text, and did.
func TestAnAnchorGainsNothingButItsHref(t *testing.T) {
	for _, doc := range []string{
		`<p>https://x.example/a&quot;onload=alert(1)</p>`,
		`<p>https://x.example/a&#39;onload=alert(1)</p>`,
		`<p>https://x.example/&lt;script&gt;</p>`,
		`<p>https://x.example/a?b=1&amp;c=2</p>`,
		`<p>https://x.example/a b" onload=alert(1)</p>`,
	} {
		out, _ := linkify(t, doc)

		// One anchor added and nothing else.
		if before, after := tagSequence(t, doc), tagSequence(t, out); after != before+"<a>" &&
			after != "<a>"+before {
			t.Errorf("%q: tags went from %s to %s: %q", doc, before, after, out)
		}

		// And that anchor carries exactly one attribute.
		var names []string
		if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			for k := range e.Attributes() {
				names = append(names, k)
			}
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if len(names) != 1 || names[0] != "href" {
			t.Errorf("%q: the anchor has attributes %q, want just href: %q", doc, names, out)
		}
	}
}

// Escaping is not sanitising, so the scheme is what decides.
func TestOnlyHTTPSchemesBecomeLinks(t *testing.T) {
	for _, doc := range []string{
		`<p>javascript:alert(1)</p>`,
		`<p>data:text/html,<script>alert(1)</script></p>`,
		`<p>file:///etc/passwd</p>`,
		`<p>mailto:a@b.example</p>`,
		`<p>ftp://x.example/f</p>`,
	} {
		out, res := linkify(t, doc)
		if res.Linked != 0 {
			t.Errorf("%q was linked: %q", doc, out)
		}
		if strings.Contains(out, "<a ") {
			t.Errorf("%q produced an anchor: %q", doc, out)
		}
	}
}

// Existing anchors are left alone, and no link is nested inside another.
func TestExistingAnchorsAreUntouched(t *testing.T) {
	const doc = `<p><a href="/k">https://inside.example</a> and https://outside.example</p>`
	out, res := linkify(t, doc)
	if res.Linked != 1 {
		t.Errorf("linked %d, want 1: %q", res.Linked, out)
	}
	if !strings.Contains(out, `<a href="/k">https://inside.example</a>`) {
		t.Errorf("the existing anchor changed: %q", out)
	}
	if strings.Count(out, "<a ") != 2 {
		t.Errorf("%d anchors in %q, want 2", strings.Count(out, "<a "), out)
	}
}

func TestWhereNothingIsLinked(t *testing.T) {
	for _, doc := range []string{
		`<code>https://x.example</code>`,
		`<pre>https://x.example</pre>`,
		`<script>var u = "https://x.example"</script>`,
		`<style>a{background:url(https://x.example)}</style>`,
		`<textarea>https://x.example</textarea>`,
		`<title>https://x.example</title>`,
	} {
		out, res := linkify(t, doc)
		if res.Linked != 0 || out != doc {
			t.Errorf("%q became %q (%d linked)", doc, out, res.Linked)
		}
	}
}

// Trailing punctuation belongs to the sentence, and a bracket the URL opened
// belongs to the URL.
func TestTrailingPunctuation(t *testing.T) {
	tests := []struct{ doc, href, after string }{
		{`<p>https://x.example.</p>`, `https://x.example`, `.`},
		{`<p>https://x.example,</p>`, `https://x.example`, `,`},
		{`<p>https://x.example?</p>`, `https://x.example`, `?`},
		{`<p>(https://x.example)</p>`, `https://x.example`, `)`},
		{`<p>(https://x.example/a_(b))</p>`, `https://x.example/a_(b)`, `)`},
		{`<p>[https://x.example/x]</p>`, `https://x.example/x`, `]`},
		{`<p>https://x.example/a).</p>`, `https://x.example/a`, `).`},
	}
	for _, tt := range tests {
		out, res := linkify(t, tt.doc)
		if res.Linked != 1 {
			t.Errorf("%q: linked %d: %q", tt.doc, res.Linked, out)
			continue
		}
		if got := hrefOf(t, out); got != tt.href {
			t.Errorf("%q: href = %q, want %q", tt.doc, got, tt.href)
		}
		if !strings.Contains(out, `</a>`+tt.after) {
			t.Errorf("%q: %q does not end the link before %q", tt.doc, out, tt.after)
		}
	}
}

// Running it again must change nothing: what it inserted is an anchor, and
// anchors are what it leaves alone.
func TestLinkifyingTwiceChangesNothing(t *testing.T) {
	const doc = `<p>See https://x.example/a?b=1&amp;c=2 today</p>`
	once, res1 := linkify(t, doc)
	if res1.Linked != 1 {
		t.Fatalf("first pass linked %d", res1.Linked)
	}
	twice, res2 := linkify(t, once)
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Linked != 0 {
		t.Errorf("the second pass linked %d more", res2.Linked)
	}
}

func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>See https://x.example/a?b=1&amp;c=2 and (https://y.example/p_(q)) today</p>`,
		`<p><a href="/k">https://inside.example</a> and https://outside.example</p>`,
		`<p>javascript:alert(1)</p>`,
		`<code>https://x.example</code><p>https://y.example</p>`,
		`<p>no urls here</p>`,
	}
	for _, doc := range docs {
		want, wantRes := linkify(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Linkify(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Linked != wantRes.Linked {
				t.Fatalf("%q at writes of %d:\n got %q (%d)\nwant %q (%d)",
					doc, n, b.String(), res.Linked, want, wantRes.Linked)
			}
		}
	}
}

// Text that is not a URL comes through unchanged, including its references -
// which is what the write-back-either-way path has to get right.
func TestTextWithoutURLsIsUnchanged(t *testing.T) {
	for _, doc := range []string{
		`<p>caf&eacute; and &amp; and &lt;script&gt;</p>`,
		`<p>See also <b>this</b> and that.</p>`,
		`<p>a &lt; b</p>`,
	} {
		out, _ := linkify(t, doc)
		if out != doc {
			t.Errorf("got %q, want %q", out, doc)
		}
	}
}

// hrefOf reads the first anchor's href back through the library, which is what a
// browser would see.
func hrefOf(t *testing.T, doc string) string {
	t.Helper()
	var href string
	found := false
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		if found {
			return nil
		}
		v, _ := e.Attribute("href")
		href, found = v, true
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	// Attribute reports source, so decode it to get what a browser resolves.
	return decode(href)
}

func decode(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	return r.Replace(s)
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

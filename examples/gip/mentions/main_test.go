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
		{"a mention", `<p>@alice</p>`, `<p><a href="/u/alice">@alice</a></p>`},
		{"a tag", `<p>#golang</p>`, `<p><a href="/t/golang">#golang</a></p>`},
		{"in a sentence", `<p>Hi @alice today</p>`,
			`<p>Hi <a href="/u/alice">@alice</a> today</p>`},
		{"trailing punctuation", `<p>see @alice.</p>`,
			`<p>see <a href="/u/alice">@alice</a>.</p>`},
		{"underscores and digits", `<p>@a_1</p>`, `<p><a href="/u/a_1">@a_1</a></p>`},
		{"two of each", `<p>@a #b @c #d</p>`,
			`<p><a href="/u/a">@a</a> <a href="/t/b">#b</a> <a href="/u/c">@c</a> <a href="/t/d">#d</a></p>`},
		{"nothing to link", `<p>ordinary text</p>`, `<p>ordinary text</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, _ := linkify(t, tt.doc); out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
		})
	}
}

// Validation is the security boundary, not escaping. These are all well-formed
// hrefs after escaping and none of them is a name.
func TestWhatIsRejected(t *testing.T) {
	for _, doc := range []string{
		`<p>@../admin</p>`,
		`<p>@..%2f..%2fadmin</p>`,
		`<p>@a/b</p>`,
		`<p>@a?b=1</p>`,
		`<p>@a#b</p>`,
		`<p>@a:b</p>`,
		`<p>@</p>`,
		`<p>#</p>`,
		`<p>@` + strings.Repeat("a", 31) + `</p>`,
		`<p>@a%2e%2e</p>`,
	} {
		out, res := linkify(t, doc)
		if res.Total() != 0 {
			t.Errorf("%q was linked: %q", doc, out)
		}
		if strings.Contains(out, "<a ") {
			t.Errorf("%q produced an anchor: %q", doc, out)
		}
	}
}

// The version that escapes and does not validate, measured rather than
// described: EscapeAttribute produces a well-formed href that points somewhere
// else entirely, because a path traversal is not a markup problem.
func TestEscapingIsNotValidation(t *testing.T) {
	const body = `../../admin`
	href := `/u/` + lolhtml.EscapeAttribute(body)
	if href != `/u/../../admin` {
		t.Fatalf("EscapeAttribute changed %q to something else: %q", body, href)
	}
	out, err := lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetInnerContent(`<a href="`+href+`">@`+lolhtml.EscapeText(body)+`</a>`, lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Well-formed markup, one anchor, one attribute - and the wrong URL.
	if !strings.Contains(out, `href="/u/../../admin"`) {
		t.Fatalf("expected the traversal to survive escaping: %q", out)
	}

	// This program refuses it instead.
	linked, res := linkify(t, `<p>@../../admin</p>`)
	if res.Total() != 0 || strings.Contains(linked, "<a ") {
		t.Errorf("the traversal was linked: %q", linked)
	}
	if res.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", res.Rejected)
	}
}

// Encoding is applied even though validation has already refused anything that
// needs it, so a change to validation cannot become a traversal on its own.
func TestTheNameIsEncodedAsWellAsValidated(t *testing.T) {
	// Every accepted name is unchanged by encoding, which is what makes the
	// belt-and-braces order safe rather than lossy.
	for _, n := range []string{"alice", "a_1", "ABC", strings.Repeat("a", 30)} {
		href, ok := buildURL('@', n)
		if !ok {
			t.Fatalf("%q was rejected", n)
		}
		if href != "/u/"+n {
			t.Errorf("%q became %q; encoding is meant to be a no-op for valid names", n, href)
		}
	}
	// And a name that would need encoding is refused before it gets there.
	if _, ok := buildURL('@', "a/b"); ok {
		t.Error(`"a/b" was accepted`)
	}
}

func TestWhereNothingIsLinked(t *testing.T) {
	for _, doc := range []string{
		`<p><a href="/x">@alice</a></p>`,
		`<code>@alice</code>`,
		`<pre>@alice</pre>`,
		`<script>var a = "@alice"</script>`,
		`<textarea>@alice</textarea>`,
		`<title>@alice</title>`,
	} {
		out, res := linkify(t, doc)
		if res.Total() != 0 {
			t.Errorf("%q: linked %d: %q", doc, res.Total(), out)
		}
	}
}

// An existing anchor is left alone and no link is nested inside another.
func TestExistingAnchorsAreUntouched(t *testing.T) {
	const doc = `<p><a href="/x">@inside</a> and @outside</p>`
	out, res := linkify(t, doc)
	if res.Total() != 1 {
		t.Errorf("linked %d, want 1: %q", res.Total(), out)
	}
	if !strings.Contains(out, `<a href="/x">@inside</a>`) {
		t.Errorf("the existing anchor changed: %q", out)
	}
	if strings.Count(out, "<a ") != 2 {
		t.Errorf("%d anchors in %q, want 2", strings.Count(out, "<a "), out)
	}
}

// Running it again must change nothing: what it inserted is an anchor, and
// anchors are what it leaves alone.
func TestLinkifyingTwiceChangesNothing(t *testing.T) {
	const doc = `<p>Hi @alice and #golang</p>`
	once, res1 := linkify(t, doc)
	if res1.Total() != 2 {
		t.Fatalf("first pass linked %d", res1.Total())
	}
	twice, res2 := linkify(t, once)
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Total() != 0 {
		t.Errorf("the second pass linked %d more", res2.Total())
	}
}

// Nothing this program writes can add a tag beyond the anchors it means to add.
func TestTheTagsGainOnlyAnchors(t *testing.T) {
	docs := []string{
		`<p>@alice</p>`,
		`<p>&lt;script&gt;@alice&lt;/script&gt;</p>`,
		`<p>@alice &amp; #golang</p>`,
		`<p>caf&eacute; @alice</p>`,
	}
	for _, doc := range docs {
		out, res := linkify(t, doc)
		before, after := tagSequence(t, doc), tagSequence(t, out)
		want := before + strings.Repeat("<a>", res.Total())
		if after != want && after != strings.Repeat("<a>", res.Total())+before {
			t.Errorf("%q: tags went from %s to %s with %d links: %q",
				doc, before, after, res.Total(), out)
		}
	}
}

// Text with nothing to link comes through byte for byte, references included.
func TestTextWithoutMentionsIsUnchanged(t *testing.T) {
	for _, doc := range []string{
		`<p>caf&eacute; and &amp; and &lt;script&gt;</p>`,
		`<p>See also <b>this</b> and that.</p>`,
		`<p>an email a@b.com is not a mention</p>`,
	} {
		out, res := linkify(t, doc)
		if res.Total() != 0 {
			t.Errorf("%q linked %d: %q", doc, res.Total(), out)
			continue
		}
		if out != doc {
			t.Errorf("got %q, want %q", out, doc)
		}
	}
}

func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>Hi @alice and #golang, see @bob. Not @../admin.</p>`,
		`<p><a href="/x">@inside</a> @outside</p>`,
		`<p>caf&eacute; @alice &amp; #tag</p>`,
		`<code>@alice</code><p>@bob</p>`,
		`<p>nothing here</p>`,
	}
	for _, doc := range docs {
		want, wantRes := linkify(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Linkify(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Total() != wantRes.Total() {
				t.Fatalf("%q at writes of %d:\n got %q (%d)\nwant %q (%d)",
					doc, n, b.String(), res.Total(), want, wantRes.Total())
			}
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

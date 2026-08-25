package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestABareURLBecomesALink, with the attributes a comment's link needs.
func TestABareURLBecomesALink(t *testing.T) {
	out, report, err := RenderString(`see https://example.com/x for more`)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linkified != 1 {
		t.Errorf("linkified %d URLs", report.Linkified)
	}
	want := `see <a href="https://example.com/x" rel="nofollow noopener" target="_blank">` +
		`https://example.com/x</a> for more`
	_ = want
	if out != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
}

// TestTheLinkPolicyIsTheRenderersOwn: rel and target are set on every surviving anchor, including
// the commenter's, and a commenter's own values do not survive.
func TestTheLinkPolicyIsTheRenderersOwn(t *testing.T) {
	out, _, err := RenderString(`<a href="https://example.com/" rel="dofollow" target="_self">x</a>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "dofollow") || strings.Contains(out, "_self") {
		t.Errorf("the commenter's link policy survived: %s", out)
	}
	if !strings.Contains(out, `rel="nofollow noopener"`) || !strings.Contains(out, `target="_blank"`) {
		t.Errorf("the renderer's policy was not applied: %s", out)
	}

	// An anchor with no href - the case where the href was refused - gets no policy, since
	// it is not a link.
	out, _, err = RenderString(`<a href="javascript:x()">x</a>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "rel=") || strings.Contains(out, "target=") {
		t.Errorf("an anchor with no href was given a link policy: %s", out)
	}
}

// TestAURLSplitAcrossChunksIsStillFound - the reason the text is accumulated. A per-chunk
// linkifier finds nothing in "https://exa" or in "mple.com/x".
func TestAURLSplitAcrossChunksIsStillFound(t *testing.T) {
	const doc = `see https://example.com/x for more`

	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		report, err := Render(&chunkedReader{s: doc, size: size}, &out, Options{})
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if report.Linkified != 1 {
			t.Errorf("read size %d linkified %d URLs: %s", size, report.Linkified, out.String())
		}
		if !strings.Contains(out.String(), `href="https://example.com/x"`) {
			t.Errorf("read size %d produced %q", size, out.String())
		}
	}
}

// chunkedReader hands out at most size bytes per Read.
type chunkedReader struct {
	s    string
	size int
	at   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestTheOutputDoesNotDependOnTheReadSize - the property. The linkifying is the same however the
// text arrived.
func TestTheOutputDoesNotDependOnTheReadSize(t *testing.T) {
	docs := []string{
		`see https://example.com/x for more`,
		`<b>bold</b> and https://a.example/1 and https://b.example/2`,
		`<a href="https://ok.example/">already https://nested.example/ linked</a>`,
		`no urls here at all`,
		`<script>alert(1)</script>https://after.example/`,
		`a & b < c > d https://example.com/?a=1&amp;b=2`,
	}

	for _, doc := range docs {
		var whole strings.Builder
		if _, err := Render(strings.NewReader(doc), &whole, Options{}); err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{1, 3, 7, 64} {
			var got strings.Builder
			if _, err := Render(&chunkedReader{s: doc, size: size}, &got, Options{}); err != nil {
				t.Fatalf("%q at %d: %v", doc, size, err)
			}
			if got.String() != whole.String() {
				t.Errorf("%q at read size %d:\n got  %q\n want %q",
					doc, size, got.String(), whole.String())
			}
		}
	}
}

// TestTheCommentersTextCanNeverBecomeMarkup - the property that matters. Whatever the comment
// says, the elements in the output are the ones the renderer put there.
func TestTheCommentersTextCanNeverBecomeMarkup(t *testing.T) {
	hostile := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`</a><script>alert(1)</script>`,
		`https://example.com/" onmouseover="alert(1)`,
		`https://example.com/x<script>alert(1)</script>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<a href="&#106;avascript:alert(1)">x</a>`,
		`a < b & c > d`,
		`<!-- <script>alert(1)</script> -->`,
		`<svg><script>alert(1)</script></svg>`,
		`<iframe src="https://evil.example/"></iframe>`,
		`<style>@import url(https://evil.example/)</style>`,
		`<b>bold</b><i>italic</i>`,
		`https://example.com/</a><b>after`,
	}

	for _, comment := range hostile {
		out, _, err := RenderString(comment)
		if err != nil {
			t.Fatalf("%q: %v", comment, err)
		}

		// Every element in the output is on the allow-list, and every attribute is one
		// the renderer wrote.
		if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !Allowed[e.TagName()] {
				t.Errorf("%q produced a <%s>: %s", comment, e.TagName(), out)
			}
			for _, attr := range e.AttributeList() {
				name := strings.ToLower(attr.Name)
				switch name {
				case "href", "rel", "target":
				default:
					t.Errorf("%q produced %s on <%s>: %s", comment, name, e.TagName(), out)
				}
				if name == "href" && !safeURL(attr.Value) {
					t.Errorf("%q produced an unsafe href %q: %s", comment, attr.Value, out)
				}
			}
			return nil
		})); err != nil {
			t.Fatalf("re-reading %q: %v", out, err)
		}
		// Deliberately no substring check for "onmouseover" and the like. A comment
		// saying `" onmouseover="alert(1)` comes out as those characters *as text*,
		// which is correct and which a substring check would call a failure. What
		// matters is whether they are an attribute, and the loop above is what asks
		// that - by re-reading the output rather than by looking at it.
	}
}

// TestAURLInsideALinkIsLeftAlone, because a link inside a link is not a link: a parser ends the
// first one and moves the content out.
func TestAURLInsideALinkIsLeftAlone(t *testing.T) {
	out, report, err := RenderString(`<a href="https://ok.example/">see https://nested.example/x</a>`)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linkified != 0 {
		t.Errorf("linkified %d URLs inside a link: %s", report.Linkified, out)
	}
	if strings.Count(out, "<a") != 1 {
		t.Errorf("%d anchors in the output: %s", strings.Count(out, "<a"), out)
	}
	if !strings.Contains(out, "see https://nested.example/x") {
		t.Errorf("the text was changed: %s", out)
	}

	// And after the link closes, linkifying resumes.
	out, report, err = RenderString(`<a href="https://ok.example/">linked</a> then https://after.example/`)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linkified != 1 {
		t.Errorf("linkified %d URLs after the link closed: %s", report.Linkified, out)
	}
}

// TestOnlySafeSchemesAreLinkedOrKept, in both directions: a href the commenter wrote and a URL
// this program linkified.
func TestOnlySafeSchemesAreLinkedOrKept(t *testing.T) {
	unsafe := []string{
		`<a href="javascript:alert(1)">x</a>`,
		`<a href="&#106;avascript:alert(1)">x</a>`,
		`<a href="JaVaScRiPt:alert(1)">x</a>`,
		`<a href="jav&#x09;ascript:alert(1)">x</a>`,
		`<a href="data:text/html,x">x</a>`,
		`<a href="vbscript:x">x</a>`,
		`<a href="/admin">x</a>`,
		`<a href="relative">x</a>`,
	}
	for _, doc := range unsafe {
		out, report, err := RenderString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if strings.Contains(out, "href") {
			t.Errorf("%q kept its href: %s", doc, out)
		}
		if report.UnsafeHref != 1 {
			t.Errorf("%q reported %d unsafe hrefs", doc, report.UnsafeHref)
		}
	}

	safe := []string{
		`<a href="https://example.com/">x</a>`,
		`<a href="http://example.com/">x</a>`,
		`<a href="mailto:a@b">x</a>`,
		`<a href="https://example.com/?a=1&amp;b=2">x</a>`,
	}
	for _, doc := range safe {
		out, report, err := RenderString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if !strings.Contains(out, "href") {
			t.Errorf("%q lost its href: %s", doc, out)
		}
		if report.UnsafeHref != 0 {
			t.Errorf("%q reported %d unsafe hrefs", doc, report.UnsafeHref)
		}
	}
}

// TestProseSurvivesAndCodeDoesNot, which is the difference between the two removals: a <div>
// holds what the person wrote and a <script> holds code that would otherwise appear as text.
func TestProseSurvivesAndCodeDoesNot(t *testing.T) {
	out, _, err := RenderString(`<div>prose</div><script>alert(1)</script><style>p{color:red}</style>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "prose") {
		t.Errorf("the prose inside a div was removed: %q", out)
	}
	if strings.Contains(out, "alert") || strings.Contains(out, "color:red") {
		t.Errorf("code appeared as text: %q", out)
	}
}

// TestAnEntityInTextSurvivesUnchanged. Reading and writing text back is a round trip, so a
// comment saying "a &amp; b" still says it - and one saying "a & b" is not double-escaped.
func TestAnEntityInTextSurvivesUnchanged(t *testing.T) {
	for _, doc := range []string{
		`a &amp; b`, `a & b`, `a &lt; b`, `a < b`, `&quot;quoted&quot;`, `caf&eacute;`,
	} {
		out, _, err := RenderString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		// The text is written back with Text, which escapes what it is given - so a raw
		// "&" becomes "&amp;" and an existing "&amp;" stays as it is. What must not
		// happen is "&amp;" becoming "&amp;amp;".
		if strings.Contains(out, "&amp;amp;") || strings.Contains(out, "&amp;lt;") {
			t.Errorf("%q was double-escaped: %q", doc, out)
		}
	}
}

// TestRenderingTwiceChangesNothingMore - idempotence, which for this program means the linkifier
// does not linkify its own output.
func TestRenderingTwiceChangesNothingMore(t *testing.T) {
	docs := []string{
		`see https://example.com/x for more`,
		`<b>bold</b> https://a.example/1`,
		`<script>alert(1)</script>plain`,
	}
	for _, doc := range docs {
		once, _, err := RenderString(doc)
		if err != nil {
			t.Fatal(err)
		}
		twice, report, err := RenderString(once)
		if err != nil {
			t.Fatal(err)
		}
		if twice != once {
			t.Errorf("%q\n once:  %q\n twice: %q", doc, once, twice)
		}
		if report.Linkified != 0 {
			t.Errorf("%q: the second pass linkified %d URLs inside existing links",
				doc, report.Linkified)
		}
	}
}

// TestTextIsWrittenBackAsSource, which is the rule that replaced escaping here: a text node's
// contents arrive with their character references intact, so writing them back unchanged is a
// round trip and escaping them again is a bug.
//
// The check goes through the whole program rather than calling linkify directly, because a text
// node is not any string: "<x" in a document is a tag, so a direct call could be handed input the
// rewriter would never produce and prove nothing.
func TestTextIsWrittenBackAsSource(t *testing.T) {
	tests := []struct{ in, want string }{
		{`a &amp; b`, `a &amp; b`},
		{`a &lt; b`, `a &lt; b`},
		{`caf&eacute;`, `caf&eacute;`},
		{`a & b`, `a & b`},
		{`https://example.com/?a=1&amp;b=2`,
			`<a href="https://example.com/?a=1&amp;b=2" rel="nofollow noopener" target="_blank">` +
				`https://example.com/?a=1&amp;b=2</a>`},
		{`see https://example.com/x &amp; more`,
			`see <a href="https://example.com/x" rel="nofollow noopener" target="_blank">` +
				`https://example.com/x</a> &amp; more`},
	}

	for _, tt := range tests {
		out, _, err := RenderString(tt.in)
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if out != tt.want {
			t.Errorf("%q\n got  %q\n want %q", tt.in, out, tt.want)
		}
	}
}

// TestAQuoteInTextStaysText. The pattern excludes quotes from a URL, so a comment that tries to
// end the href writes text after the link instead of an attribute - which is what the re-read in
// TestTheCommentersTextCanNeverBecomeMarkup checks, and this states as its own case.
func TestAQuoteInTextStaysText(t *testing.T) {
	out, _, err := RenderString(`https://example.com/" onmouseover="alert(1)`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="https://example.com/"`) {
		t.Errorf("the URL was not linked as itself: %s", out)
	}

	// The rest is text: re-reading the output finds no such attribute anywhere.
	if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		for _, attr := range e.AttributeList() {
			if strings.HasPrefix(strings.ToLower(attr.Name), "on") {
				t.Errorf("an event handler became an attribute: %s", out)
			}
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
}

// TestThePrefixIsEscaped, which is the one value that arrives from outside the document and so the
// one place Text is the right content type.
func TestThePrefixIsEscaped(t *testing.T) {
	var out strings.Builder
	if _, err := Render(strings.NewReader(`<b>comment</b>`), &out,
		Options{Prefix: `<i>reply</i> & more`}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<i>") {
		t.Errorf("the prefix arrived as markup: %s", out.String())
	}
	if !strings.Contains(out.String(), "&lt;i&gt;reply&lt;/i&gt; &amp; more") {
		t.Errorf("the prefix was not escaped: %s", out.String())
	}
	// And the comment's own markup is untouched by it.
	if !strings.Contains(out.String(), "<b>comment</b>") {
		t.Errorf("the comment was changed: %s", out.String())
	}
}

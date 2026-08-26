package main

import (
	"fmt"
	"html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// hostile is the value a template author never intended and an attacker supplies.
const hostile = `</script></title></textarea><img src=x onerror=alert(1)>`

func resolve(t *testing.T, doc string, values map[string]string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Resolve(strings.NewReader(doc), &out, values)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", doc, err)
	}
	return out.String(), res
}

// elementsIn reports the element names a parser finds, which is the only honest way to ask whether
// an escape held: the output looking reasonable is not the same as it being inert.
func elementsIn(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		out = append(out, e.TagName())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNoPositionLetsAValueBecomeMarkup, which is the property the program exists for. Every
// position gets a hostile value and the output is re-parsed: an img element anywhere is a failure.
func TestNoPositionLetsAValueBecomeMarkup(t *testing.T) {
	values := map[string]string{"v": hostile}
	for _, tt := range []struct{ name, doc string }{
		{"text", `<p>{{ v }}</p>`},
		{"attribute", `<a title="{{ v }}">x</a>`},
		{"url attribute", `<a href="{{ v }}">x</a>`},
		{"title", `<title>{{ v }}</title>`},
		{"textarea", `<textarea>{{ v }}</textarea>`},
		{"script", `<script>var a = "{{ v }}";</script>`},
		{"style", `<style>.a{content:"{{ v }}"}</style>`},
		{"comment", `<!-- {{ v }} -->`},
		{"nested", `<div><p>a {{ v }} b</p><a href="/x" title="{{ v }}">y</a></div>`},
		{"twice in one node", `<p>{{ v }} and {{ v }}</p>`},
	} {
		out, _ := resolve(t, tt.doc, values)

		// The property is that the output has the same elements as the input, with
		// the same attributes. Re-parsing is the only way to ask: bytes that look
		// like markup inside a comment are inert, and bytes that look inert can be
		// markup.
		before, after := shape(t, tt.doc), shape(t, out)
		if before != after {
			t.Errorf("%s: the shape changed\n  in:  %s -> %s\n  out: %s -> %s",
				tt.name, tt.doc, before, out, after)
		}
	}
}

// shape returns every element with its attribute names, which is what a value must not be able to
// change. An injected element shows up as a new name and an injected handler as a new attribute.
func shape(t *testing.T, doc string) string {
	t.Helper()
	var parts []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		names := []string{e.TagName()}
		for _, a := range e.AttributeList() {
			names = append(names, a.Name)
		}
		parts = append(parts, strings.Join(names, "."))
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return strings.Join(parts, " ")
}

// TestAnAttributeValueSurvivesTheRoundTrip, which is what EscapeAttribute buys here. It is not an
// injection guard: SetAttribute takes raw attribute-value source and rewrites the double quote on
// the way out, so an unescaped value cannot end the attribute either way. What it prevents is
// corruption - a value containing "&amp;" being read back as "&" - and the property is that the
// value a browser reads is the value that was supplied.
func TestAnAttributeValueSurvivesTheRoundTrip(t *testing.T) {
	for _, value := range []string{
		`a & b`,
		`&amp;`,
		`&lt;script&gt;`,
		`a "quoted" b`,
		`a 'quoted' b`,
		`<img src=x>`,
		`100% & more`,
		`&#106;`,
		`caf&eacute;`,
	} {
		out, res := resolve(t, `<a title="{{ v }}">x</a>`, map[string]string{"v": value})
		if len(res.Resolved()) != 1 {
			t.Errorf("%q was refused: %v", value, res.Refused())
			continue
		}
		got := readAttribute(t, out, "a", "title")
		if got != value {
			t.Errorf("%q came back as %q (output %s)", value, got, out)
		}
	}
}

// readAttribute reads an attribute back the way a browser would: the library reports raw source,
// so the references in it are decoded here.
func readAttribute(t *testing.T, doc, sel, name string) string {
	t.Helper()
	var out string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
		v, _ := e.Attribute(name)
		out = v
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return html.UnescapeString(out)
}

// TestARawTextBodyIsRefusedRatherThanCorrupted. ContentType Text is safe in a script - the value
// cannot become an element - and it is wrong, because character references are not decoded there,
// so the JavaScript ends up holding the escape rather than the character. Safe and corrupt is not
// resolved, so this refuses.
func TestARawTextBodyIsRefusedRatherThanCorrupted(t *testing.T) {
	values := map[string]string{"v": "a & b"}

	for _, tag := range []string{"script", "style", "iframe", "noembed", "noframes",
		"noscript", "xmp"} {
		doc := "<" + tag + ">x {{ v }} y</" + tag + ">"
		out, res := resolve(t, doc, values)
		if out != doc {
			t.Errorf("%s: the body was rewritten: %s", tag, out)
		}
		if len(res.Resolved()) != 0 {
			t.Errorf("%s: resolved %v", tag, res.Resolved())
		}
		if len(res.Refused()) != 1 {
			t.Fatalf("%s: %d refusals", tag, len(res.Refused()))
		}
		if got := res.Refused()[0].Position; got != InRawText {
			t.Errorf("%s: position %v", tag, got)
		}
	}

	// The measurement behind the refusal: Text in a script is safe and corrupt.
	out, err := lolhtml.RewriteString(`<script>var a = "P";</script>`,
		lolhtml.OnText("script", func(t *lolhtml.TextChunk) error {
			if t.IsLastInTextNode() {
				return nil
			}
			return t.Replace(`var a = "a & b";`, lolhtml.Text)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(elementsIn(t, out)) != 1 {
		t.Errorf("more than the script: %s", out)
	}
	if !strings.Contains(out, "&amp;") {
		t.Errorf("Text did not escape the ampersand, so the corruption claim is wrong: %s", out)
	}
	// Which is the corruption: the script source now holds five characters where one was
	// meant, and nothing decodes them.
	if strings.Contains(out, `"a & b"`) {
		t.Errorf("the value survived intact, so Text is not corrupting here: %s", out)
	}
}

// TestATitleAndATextareaAreTheOppositeCase, because references are decoded there, so an HTML
// escape is a real escape and the value arrives as written.
func TestATitleAndATextareaAreTheOppositeCase(t *testing.T) {
	values := map[string]string{"v": "a & <b>"}
	for _, tag := range []string{"title", "textarea"} {
		doc := "<" + tag + ">{{ v }}</" + tag + ">"
		out, res := resolve(t, doc, values)
		if len(res.Resolved()) != 1 {
			t.Fatalf("%s: %d resolved, %d refused: %s",
				tag, len(res.Resolved()), len(res.Refused()), out)
		}
		if got := res.Resolved()[0].Position; got != InEscapableRawText {
			t.Errorf("%s: position %v", tag, got)
		}
		want := "<" + tag + ">a &amp; &lt;b&gt;</" + tag + ">"
		if out != want {
			t.Errorf("%s: got %q, want %q", tag, out, want)
		}
		// The value a browser reads back is the value that went in, which is what makes
		// this escaping rather than corruption.
		if got := decodeText(t, out, tag); got != "a & <b>" {
			t.Errorf("%s: the decoded value is %q", tag, got)
		}
	}
}

// decodeText reads the text of the named element back as a browser would, by asking the library
// for the text and decoding the references that the element's content model decodes.
func decodeText(t *testing.T, doc, tag string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnText(tag, func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	// title and textarea decode references, so this is what the element holds.
	return unescape(b.String())
}

func unescape(s string) string {
	for from, to := range map[string]string{"&amp;": "&", "&lt;": "<", "&gt;": ">",
		"&quot;": `"`, "&#39;": "'"} {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

// TestAURLSchemeIsCheckedOnTheDecodedValue, since &#106;avascript: is javascript: to a browser and
// is not to a string comparison. Every one of these was a real bypass in some sanitiser.
func TestAURLSchemeIsCheckedOnTheDecodedValue(t *testing.T) {
	for _, payload := range []string{
		`javascript:alert(1)`,
		`JAVASCRIPT:alert(1)`,
		`&#106;avascript:alert(1)`,
		`&#x6a;avascript:alert(1)`,
		`&#0000106;avascript:alert(1)`,
		`jav&#x09;ascript:alert(1)`,
		`jav&#x0A;ascript:alert(1)`,
		`&Tab;javascript:alert(1)`,
		` javascript:alert(1)`,
		"\tjavascript:alert(1)",
		`data:text/html,<script>alert(1)</script>`,
		`vbscript:msgbox(1)`,
	} {
		out, res := resolve(t, `<a href="{{ v }}">x</a>`, map[string]string{"v": payload})
		if len(res.Resolved()) != 0 {
			t.Errorf("%q was resolved into %s", payload, out)
		}
		if len(res.Refused()) != 1 {
			t.Errorf("%q: %d refusals", payload, len(res.Refused()))
			continue
		}
		if why := res.Refused()[0].Why; !strings.Contains(why, "scheme") {
			t.Errorf("%q: why = %q", payload, why)
		}
		if out != `<a href="{{ v }}">x</a>` {
			t.Errorf("%q: the attribute was changed: %s", payload, out)
		}
	}

	// And the schemes a page needs are resolved, so the check is not just a refusal.
	for _, ok := range []string{"/tools", "https://example.com/x", "#frag", "mailto:a@b.c",
		"tel:+1", "?q=1", "//cdn.example.com/x"} {
		_, res := resolve(t, `<a href="{{ v }}">x</a>`, map[string]string{"v": ok})
		if len(res.Resolved()) != 1 {
			t.Errorf("%q was refused: %v", ok, res.Refused())
		}
	}

	// A non-URL attribute is not scheme-checked, because there is nothing to fetch.
	_, res := resolve(t, `<a title="{{ v }}">x</a>`,
		map[string]string{"v": "javascript:alert(1)"})
	if len(res.Resolved()) != 1 {
		t.Errorf("a title was scheme-checked: %v", res.Refused())
	}
}

// TestACommentValueThatWouldEndTheCommentIsRefused, which Comment.SetText would refuse anyway -
// this refuses first so the report can say which value did it rather than failing the run.
func TestACommentValueThatWouldEndTheCommentIsRefused(t *testing.T) {
	for _, payload := range []string{`a --> b`, `a --!> b`, `x --`} {
		out, res := resolve(t, `<!-- {{ v }} -->`, map[string]string{"v": payload})
		if len(res.Refused()) != 1 {
			t.Errorf("%q: %d refusals, out=%s", payload, len(res.Refused()), out)
			continue
		}
		if why := res.Refused()[0].Why; !strings.Contains(why, "end the comment") {
			t.Errorf("%q: why = %q", payload, why)
		}
		if out != `<!-- {{ v }} -->` {
			t.Errorf("%q: %s", payload, out)
		}
	}

	// A value with a lone hyphen or one "-" is fine, and the run does not fail.
	for _, ok := range []string{"a - b", "a-b", "-", "a--b"} {
		out, res := resolve(t, `<!-- {{ v }} -->`, map[string]string{"v": ok})
		if len(res.Resolved()) != 1 {
			t.Errorf("%q was refused: %v", ok, res.Refused())
		}
		if !strings.Contains(out, ok) {
			t.Errorf("%q: %s", ok, out)
		}
		// And what came out is still one comment, not a comment plus markup.
		comments := 0
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				comments++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if comments != 1 {
			t.Errorf("%q produced %d comments: %s", ok, comments, out)
		}
	}
}

// TestAPlaceholderStraddlingAChunkBoundaryStillResolves, at every byte offset, because text
// arrives in whatever pieces the network chose and "{{ na" is not a placeholder on its own.
func TestAPlaceholderStraddlingAChunkBoundaryStillResolves(t *testing.T) {
	const doc = `<p>before {{ name }} after</p>`
	values := map[string]string{"name": "Ada"}
	whole, _ := resolve(t, doc, values)
	if !strings.Contains(whole, "before Ada after") {
		t.Fatalf("the whole-document case is wrong: %s", whole)
	}

	for size := 1; size <= len(doc); size++ {
		var out strings.Builder
		res, err := Resolve(&chunked{s: doc, n: size}, &out, values)
		if err != nil {
			t.Fatalf("chunk %d: %v", size, err)
		}
		if out.String() != whole {
			t.Errorf("chunk %d: %q", size, out.String())
		}
		if len(res.Resolved()) != 1 {
			t.Errorf("chunk %d: %d resolved", size, len(res.Resolved()))
		}
	}
}

// TestAPlaceholderSplitByMarkupIsNotAPlaceholder, which is the right answer rather than a
// problem: a comment or an element ends the text node, so the accumulator sees two nodes.
func TestAPlaceholderSplitByMarkupIsNotAPlaceholder(t *testing.T) {
	values := map[string]string{"name": "Ada"}
	for _, doc := range []string{
		`<p>{{ na<!-- c -->me }}</p>`,
		`<p>{{ na<b>x</b>me }}</p>`,
		`<p>{{ na<br>me }}</p>`,
	} {
		out, res := resolve(t, doc, values)
		if len(res.Resolved()) != 0 {
			t.Errorf("%s resolved %v", doc, res.Resolved())
		}
		if out != doc {
			t.Errorf("%s became %s", doc, out)
		}
	}

	// Two placeholders in one node both resolve, which is what makes the above a boundary
	// question rather than a parsing one.
	out, res := resolve(t, `<p>{{ name }} and {{ name }}</p>`, values)
	if len(res.Resolved()) != 2 {
		t.Errorf("%d resolved: %s", len(res.Resolved()), out)
	}
	if want := `<p>Ada and Ada</p>`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// TestThePositionsThatAreGoneBeforeTheProgramSeesThem, both measured. A rewrite that says nothing
// about these has processed a different document from the one it was given.
func TestThePositionsThatAreGoneBeforeTheProgramSeesThem(t *testing.T) {
	// An attribute-name placeholder with spaces is three attributes.
	var names []string
	if _, err := lolhtml.RewriteString(`<div {{ attr }}="1">x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				names = append(names, fmt.Sprintf("%s=%q", a.NamePreserveCase, a.Value))
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if want := `{{="" attr="" }}="1"`; strings.Join(names, " ") != want {
		t.Errorf("attributes %v, want %s", names, want)
	}
	_, res := resolve(t, `<div {{ attr }}="1">x</div>`, map[string]string{"attr": "data-x"})
	if len(res.Refused()) == 0 {
		t.Error("the split form was not reported")
	} else if res.Refused()[0].Position != InSplitAttributeName {
		t.Errorf("position %v", res.Refused()[0].Position)
	}

	// Without spaces it is one attribute, recognisable and still not rewritable.
	names = nil
	if _, err := lolhtml.RewriteString(`<div {{attr}}="1">x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				names = append(names, a.NamePreserveCase)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "{{attr}}" {
		t.Errorf("attributes %v", names)
	}
	_, res = resolve(t, `<div {{attr}}="1">x</div>`, map[string]string{"attr": "data-x"})
	found := false
	for _, f := range res.Refused() {
		if f.Position == InAttributeName {
			found = true
		}
	}
	if !found {
		t.Errorf("the name form was not reported: %v", res.Refused())
	}

	// A tag-name placeholder is not an element: the opening half is text and the closing
	// half is a bogus comment.
	const tagDoc = `<{{ tag }}>x</{{ tag }}>`
	if got := elementsIn(t, tagDoc); len(got) != 0 {
		t.Errorf("elements %v, want none", got)
	}
	comments := 0
	if _, err := lolhtml.RewriteString(tagDoc,
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			comments++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Errorf("%d comments, want 1", comments)
	}
	_, res = resolve(t, tagDoc, map[string]string{})
	found = false
	for _, f := range res.Refused() {
		if f.Position == InTagName {
			found = true
		}
	}
	if !found {
		t.Errorf("the tag-name form was not reported: %v", res.Refused())
	}
}

// TestAnUnresolvedPlaceholderIsLeftAloneAndReported, so a partial data set does not silently
// blank the page.
func TestAnUnresolvedPlaceholderIsLeftAloneAndReported(t *testing.T) {
	out, res := resolve(t, `<p>{{ a }} {{ b }}</p>`, map[string]string{"a": "A"})
	if want := `<p>A {{ b }}</p>`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if len(res.Resolved()) != 1 || len(res.Refused()) != 1 {
		t.Errorf("%d resolved, %d refused", len(res.Resolved()), len(res.Refused()))
	}
	if why := res.Refused()[0].Why; !strings.Contains(why, "no value") {
		t.Errorf("why = %q", why)
	}
}

// TestADocumentWithNoPlaceholdersIsUnchanged, which is the case that proves the accumulator puts
// text back exactly as it found it.
func TestADocumentWithNoPlaceholdersIsUnchanged(t *testing.T) {
	for _, doc := range []string{
		`<main><p>text &amp; more</p><a href="/x" title="t">y</a></main>`,
		`<p>a &lt; b</p><script>var a = 1 < 2;</script><style>.a > .b{}</style>`,
		`<!-- a comment --><title>t</title><textarea>ta</textarea>`,
		`<p>{ not a placeholder }</p><p>{{ }}</p><p>{{}}</p>`,
	} {
		out, res := resolve(t, doc, map[string]string{"a": "A"})
		if out != doc {
			t.Errorf("the document changed:\n  in:  %s\n  out: %s", doc, out)
		}
		if len(res.Found) != 0 {
			t.Errorf("%s: found %v", doc, res.Found)
		}
	}
}

type chunked struct {
	s     string
	n     int
	reads int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	c.reads++
	return n, nil
}

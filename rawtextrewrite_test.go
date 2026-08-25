package lolhtml_test

// Rewriting the text of a raw-text element, which is what a rewrite has to do to
// edit a stylesheet or a script body. Two things are the wrong way round from the
// insertion paths: the content type that escapes is the wrong one, and the guard
// that refuses a breakout is not applied.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// rewriteText accumulates a text node under the selector and replaces it with fn's
// result, which is the shape any whole-text rewrite takes.
func rewriteText(t *testing.T, doc, selector string, ct lolhtml.ContentType, fn func(string) string) (string, error) {
	t.Helper()
	var acc strings.Builder
	return lolhtml.RewriteString(doc, lolhtml.OnText(selector, func(c *lolhtml.TextChunk) error {
		acc.WriteString(c.Text())
		if !c.IsLastInTextNode() {
			c.Remove()
			return nil
		}
		s := acc.String()
		acc.Reset()
		return c.Replace(fn(s), ct)
	}))
}

// TestTextEscapesWhatRawTextMustNotHaveEscaped.
func TestTextEscapesWhatRawTextMustNotHaveEscaped(t *testing.T) {
	for _, tc := range []struct{ doc, selector, want string }{
		{`<style>.a > .b{color:red}</style>`, "style", `<style>.a &gt; .b{color:red}</style>`},
		{`<script>if (a < b && c > d) f()</script>`, "script",
			`<script>if (a &lt; b &amp;&amp; c &gt; d) f()</script>`},
		// The two that do decode references get their references escaped again.
		{`<title>a &amp; b</title>`, "title", `<title>a &amp;amp; b</title>`},
		{`<textarea>a > b</textarea>`, "textarea", `<textarea>a &gt; b</textarea>`},
	} {
		got, err := rewriteText(t, tc.doc, tc.selector, lolhtml.Text, func(s string) string { return s })
		if err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
		if got != tc.want {
			t.Errorf("%q with Text\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		// HTML puts the same text back unchanged, which is what raw text needs.
		got, err = rewriteText(t, tc.doc, tc.selector, lolhtml.HTML, func(s string) string { return s })
		if err != nil {
			t.Fatalf("%q: %v", tc.doc, err)
		}
		if got != tc.doc {
			t.Errorf("%q with HTML\n got %q\nwant it unchanged", tc.doc, got)
		}
	}
	// An element whose content is markup is the other way round, which is why this
	// is a trap rather than an obvious rule: there Text is right.
	got, err := rewriteText(t, `<p>a > b</p>`, "p", lolhtml.Text, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>a &gt; b</p>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestARealRewriteNeedsHTML: a URL rewrite inside a stylesheet, done both ways, where
// only one of them leaves working CSS.
func TestARealRewriteNeedsHTML(t *testing.T) {
	const doc = `<style>.a > .b{background:url(x.png)}</style>`
	rebase := func(s string) string { return strings.ReplaceAll(s, "url(x.png)", "url(/base/x.png)") }

	got, err := rewriteText(t, doc, "style", lolhtml.HTML, rebase)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<style>.a > .b{background:url(/base/x.png)}</style>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	got, err = rewriteText(t, doc, "style", lolhtml.Text, rebase)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "&gt;") {
		t.Errorf("Text was expected to escape the child combinator: %q", got)
	}
}

// TestTheBreakoutGuardCoversTheElementPathsAndNotTheTextPaths, which is the asymmetry
// worth pinning: the same content is refused through one API and written through the
// other.
func TestTheBreakoutGuardCoversTheElementPathsAndNotTheTextPaths(t *testing.T) {
	const doc = `<script>var b=1</script>`
	const payload = `var a="</script><img src=x onerror=alert(1)>"`

	for _, tc := range []struct {
		name string
		opt  lolhtml.Option
	}{
		{"SetInnerContent", lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.SetInnerContent(payload, lolhtml.HTML)
		})},
		{"Append", lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.Append(payload, lolhtml.HTML)
		})},
		{"Prepend", lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.Prepend(payload, lolhtml.HTML)
		})},
	} {
		out, err := lolhtml.RewriteString(doc, tc.opt)
		if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
			t.Errorf("Element.%s: err = %v, want ErrRawTextBreakout", tc.name, err)
		}
		if out != "" {
			t.Errorf("Element.%s wrote %q", tc.name, out)
		}
	}

	for _, tc := range []struct {
		name string
		fn   func(*lolhtml.TextChunk) error
	}{
		{"Replace", func(c *lolhtml.TextChunk) error { return c.Replace(payload, lolhtml.HTML) }},
		{"Before", func(c *lolhtml.TextChunk) error { return c.Before(payload, lolhtml.HTML) }},
		{"After", func(c *lolhtml.TextChunk) error { return c.After(payload, lolhtml.HTML) }},
	} {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnText("script", func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				return nil
			}
			return tc.fn(c)
		}))
		if err != nil {
			t.Errorf("TextChunk.%s: %v", tc.name, err)
		}
		if !strings.Contains(out, `</script><img src=x onerror=alert(1)>`) {
			t.Errorf("TextChunk.%s: the payload did not reach the output: %q", tc.name, out)
		}
		// The element is over where the content said, which is the hazard: what
		// follows is an element and not script source.
		var imgs int
		if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("img", func(*lolhtml.Element) error {
			imgs++
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if imgs != 1 {
			t.Errorf("TextChunk.%s: %d img elements in the output, want 1", tc.name, imgs)
		}
	}

	// Every raw-text element the guard knows about behaves the same way on the text
	// path.
	for _, tag := range []string{"script", "style", "title", "textarea", "iframe", "noembed", "noframes", "noscript", "xmp"} {
		d := "<" + tag + ">x</" + tag + ">"
		p := "a</" + tag + "><b>y</b>"
		out, err := lolhtml.RewriteString(d, lolhtml.OnText(tag, func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				return nil
			}
			return c.Replace(p, lolhtml.HTML)
		}))
		if err != nil {
			t.Errorf("<%s>: %v", tag, err)
			continue
		}
		if !strings.Contains(out, "</"+tag+"><b>") {
			t.Errorf("<%s>: %q", tag, out)
		}
	}
}

// TestCheckRawTextIsWhatTheElementPathsApply, so a caller on the text path gets the
// same answer rather than a re-implementation of the tokenizer's rule.
func TestCheckRawTextIsWhatTheElementPathsApply(t *testing.T) {
	for _, tc := range []struct {
		tag, content string
		refused      bool
	}{
		{"script", `var a="</script>"`, true},
		{"script", `var a="</SCRIPT >"`, true},
		{"script", `var a="</script`, true},    // the end of the content is a terminator
		{"script", `var a="</script"`, false},  // a quote is not a terminator
		{"script", `var a="</scriptx"`, false}, // not a terminator
		{"script", `var a="<\/script>"`, false},
		{"style", `.a{content:"</style>"}`, true},
		{"style", `.a > .b{color:red}`, false},
		{"title", `a</title>b`, true},
		{"textarea", `a</textarea>b`, true},
		{"iframe", `a</iframe>b`, true},
		{"xmp", `a</xmp>b`, true},
		// Not a raw-text element, so never refused.
		{"p", `a</p>b`, false},
		{"div", `</script>`, false},
		// plaintext cannot be closed, so there is nothing to refuse.
		{"plaintext", `a</plaintext>b`, false},
		// Case of the tag name does not matter.
		{"SCRIPT", `</script>`, true},
	} {
		err := lolhtml.CheckRawText(tc.tag, tc.content)
		if refused := errors.Is(err, lolhtml.ErrRawTextBreakout); refused != tc.refused {
			t.Errorf("CheckRawText(%q, %q) = %v, want refused = %v", tc.tag, tc.content, err, tc.refused)
		}
	}

	// The answer matches what the element path gives for the same content, error
	// text and all, which is the point of exporting it rather than describing it.
	const payload = `var a="</script>"`
	_, elemErr := lolhtml.RewriteString(`<script>x</script>`, lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		return e.SetInnerContent(payload, lolhtml.HTML)
	}))
	checkErr := lolhtml.CheckRawText("script", payload)
	if checkErr == nil || elemErr == nil {
		t.Fatalf("expected both to fail: %v, %v", checkErr, elemErr)
	}
	if !strings.Contains(elemErr.Error(), checkErr.Error()) {
		t.Errorf("the element path says %q\nCheckRawText says          %q", elemErr, checkErr)
	}
	// And it says what to write instead, per element.
	for _, tc := range []struct{ tag, advice string }{
		{"script", `<\/script`},
		{"style", `\3c /style`},
		{"title", "ContentType Text"},
		{"iframe", "cannot appear inside it"},
	} {
		err := lolhtml.CheckRawText(tc.tag, "a</"+tc.tag+">b")
		if err == nil || !strings.Contains(err.Error(), tc.advice) {
			t.Errorf("CheckRawText(%q, …) = %v, want it to mention %q", tc.tag, err, tc.advice)
		}
	}
}

// TestTheGuardedPathIsUsableFromTheTextPath: the whole point is that a rewrite can be
// safe, so this is what a caller should write.
func TestTheGuardedPathIsUsableFromTheTextPath(t *testing.T) {
	safe := func(doc, tag string, fn func(string) string) (string, error) {
		var acc strings.Builder
		return lolhtml.RewriteString(doc, lolhtml.OnText(tag, func(c *lolhtml.TextChunk) error {
			acc.WriteString(c.Text())
			if !c.IsLastInTextNode() {
				c.Remove()
				return nil
			}
			s := fn(acc.String())
			acc.Reset()
			if err := lolhtml.CheckRawText(tag, s); err != nil {
				return err
			}
			return c.Replace(s, lolhtml.HTML)
		}))
	}

	out, err := safe(`<style>.a > .b{background:url(x.png)}</style>`, "style", func(s string) string {
		return strings.ReplaceAll(s, "x.png", "/base/x.png")
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<style>.a > .b{background:url(/base/x.png)}</style>`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}

	out, err = safe(`<script>var a=1</script>`, "script", func(string) string {
		return `var a="</script><img src=x>"`
	})
	if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
		t.Errorf("err = %v, want ErrRawTextBreakout", err)
	}
	if strings.Contains(out, "<img") {
		t.Errorf("the payload reached the output: %q", out)
	}
}

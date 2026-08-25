package lolhtml_test

// The refusal to close a raw-text element from inside it.
//
// The rule this implements is the tokenizer's, and it was measured against
// golang.org/x/net/html rather than read off the specification, because being
// wrong in either direction is bad: too strict refuses content that was fine,
// too loose lets an injection through.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// rawTextTags are the nine the guard covers. plaintext is raw text too and is
// deliberately not here; see TestPlaintextCannotBeBrokenOutOf.
var rawTextTags = []string{"iframe", "noembed", "noframes", "noscript",
	"script", "style", "textarea", "title", "xmp"}

// TestWhatCountsAsClosingARawTextElement. The measured rule: "</" then the tag
// name without regard to case, then something that can end a tag name - ">",
// "/", ASCII whitespace, or the end of the content.
func TestWhatCountsAsClosingARawTextElement(t *testing.T) {
	for _, tt := range []struct {
		content string
		refused bool
	}{
		// Closes it.
		{`a</script>b`, true},
		{`a</script b`, true},
		{`a</script/b`, true},
		{"a</script\tb", true},
		{"a</script\nb", true},
		{"a</script\rb", true},
		{"a</script\fb", true},
		{`a</SCRIPT>b`, true},
		{`a</Script>b`, true},
		{`a</script`, true}, // the rest of the document can finish it
		{`</script>`, true},
		{`x</script>y</script>z`, true},

		// Does not.
		{`a</scriptx>b`, false},
		{`a</scripts>b`, false},
		{`a</scrip>b`, false},
		{`a</ script>b`, false},
		{`a< /script>b`, false},
		{`a<\/script>b`, false},
		{`a</style>b`, false}, // wrong tag for a script
		{`plain content`, false},
		{``, false},
		{`a<script>b`, false}, // an opening tag is not a closing one
	} {
		_, err := lolhtml.RewriteString(`<script></script>`,
			lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				return e.SetInnerContent(tt.content, lolhtml.HTML)
			}))
		refused := errors.Is(err, lolhtml.ErrRawTextBreakout)
		if refused != tt.refused {
			t.Errorf("SetInnerContent(%q): refused=%v, want %v (err=%v)",
				tt.content, refused, tt.refused, err)
		}
	}
}

// TestTheRuleMatchesWhatActuallyClosesTheElement is the other side of the same
// coin: for each content that is allowed through, the element it was inserted
// into must still be there afterwards.
func TestTheRuleMatchesWhatActuallyClosesTheElement(t *testing.T) {
	allowed := []string{
		`a</scriptx>b`, `a</scrip>b`, `a</ script>b`, `a< /script>b`,
		`a<\/script>b`, `a</style>b`, `plain content`, `a<script>b`,
	}
	for _, content := range allowed {
		out, err := lolhtml.RewriteString(`<script></script><p>after</p>`,
			lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				return e.SetInnerContent(content, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		// If the script had in fact been closed, the content after it would
		// parse as markup and produce elements beyond script and p.
		var tags []string
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				tags = append(tags, e.TagName())
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if strings.Join(tags, ",") != "script,p" {
			t.Errorf("%q was allowed but the document parsed as %v: %s",
				content, tags, out)
		}
	}
}

// TestEveryRawTextElementIsChecked, and with the closing tag that belongs to it.
func TestEveryRawTextElementIsChecked(t *testing.T) {
	for _, tag := range rawTextTags {
		doc := "<" + tag + "></" + tag + ">"

		_, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("x</"+tag+">y", lolhtml.HTML)
			}))
		if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
			t.Errorf("<%s> accepted its own closing tag: %v", tag, err)
		}

		// Another element's closing tag is ordinary content.
		other := "script"
		if tag == "script" {
			other = "style"
		}
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("x</"+other+">y", lolhtml.HTML)
			})); err != nil {
			t.Errorf("<%s> refused </%s>, which does not close it: %v", tag, other, err)
		}
	}
}

// TestEveryInsertionIntoTheContentIsChecked: the four methods that write inside
// the element.
func TestEveryInsertionIntoTheContentIsChecked(t *testing.T) {
	payload := `x</script>y`

	for name, insert := range map[string]func(*lolhtml.Element) error{
		"Prepend":         func(e *lolhtml.Element) error { return e.Prepend(payload, lolhtml.HTML) },
		"Append":          func(e *lolhtml.Element) error { return e.Append(payload, lolhtml.HTML) },
		"SetInnerContent": func(e *lolhtml.Element) error { return e.SetInnerContent(payload, lolhtml.HTML) },
		"EndTag.Before": func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.Before(payload, lolhtml.HTML)
			})
		},
	} {
		_, err := lolhtml.RewriteString(`<script>old</script>`,
			lolhtml.OnElement("script", insert))
		if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
			t.Errorf("%s was not checked: %v", name, err)
		}
	}
}

// TestWritingOutsideTheElementIsNotChecked. Before, After and Replace put markup
// in the document, where a closing tag is just a tag - and Replace in particular
// is how a caller legitimately swaps a script for something else.
func TestWritingOutsideTheElementIsNotChecked(t *testing.T) {
	payload := `<script>a</script>`

	for name, insert := range map[string]func(*lolhtml.Element) error{
		"Before":  func(e *lolhtml.Element) error { return e.Before(payload, lolhtml.HTML) },
		"After":   func(e *lolhtml.Element) error { return e.After(payload, lolhtml.HTML) },
		"Replace": func(e *lolhtml.Element) error { return e.Replace(payload, lolhtml.HTML) },
		"EndTag.After": func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.After(payload, lolhtml.HTML)
			})
		},
	} {
		if _, err := lolhtml.RewriteString(`<script>old</script>`,
			lolhtml.OnElement("script", insert)); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

// TestTextIsNotChecked, because it escapes the "<" and so cannot close anything.
// It corrupts the content instead, which is a different problem with its own
// section in the documentation.
func TestTextIsNotChecked(t *testing.T) {
	out, err := lolhtml.RewriteString(`<script></script>`,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`x</script>y`, lolhtml.Text)
		}))
	if err != nil {
		t.Fatalf("Text was refused: %v", err)
	}
	if !strings.Contains(out, "&lt;/script&gt;") {
		t.Errorf("expected the escaped form: %s", out)
	}
}

// TestOrdinaryElementsAreNotChecked: a div may contain a script, and inserting
// one is a normal thing to do.
func TestOrdinaryElementsAreNotChecked(t *testing.T) {
	for _, tag := range []string{"div", "p", "span", "head", "body"} {
		if _, err := lolhtml.RewriteString("<"+tag+"></"+tag+">",
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent(`<script>a</script><style>b</style>`, lolhtml.HTML)
			})); err != nil {
			t.Errorf("<%s> was checked: %v", tag, err)
		}
	}
}

// TestTheErrorNamesTheElementAndThePosition, because the content is usually
// assembled somewhere other than the call that inserts it.
func TestTheErrorNamesTheElementAndThePosition(t *testing.T) {
	_, err := lolhtml.RewriteString(`<style></style>`,
		lolhtml.OnElement("style", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`a{color:red}</style>`, lolhtml.HTML)
		}))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"style", "</style>", "byte 12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestAJSONLDBlockIsUnaffected: the intended way to get data into a page, and it
// has to keep working. JSON escapes "/" as "\/" precisely so that a payload
// cannot close the script.
func TestAJSONLDBlockIsUnaffected(t *testing.T) {
	const payload = `{"name":"a<\/script>b","url":"https://x.example/p"}`
	out, err := lolhtml.RewriteString(`<script type="application/ld+json"></script>`,
		lolhtml.OnElement(`script[type="application/ld+json"]`, func(e *lolhtml.Element) error {
			return e.SetInnerContent(payload, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatalf("a correctly escaped JSON payload was refused: %v", err)
	}
	if !strings.Contains(out, payload) {
		t.Errorf("the payload was altered: %s", out)
	}
}

// htmlElementNames is every element name in the HTML specification's index of
// elements, plus the obsolete ones a parser still recognises. It is long on
// purpose: the point of the test below is to ask the parser which elements hold
// content that is not markup, rather than to assert that the ones the guard
// knows about are the ones that exist.
var htmlElementNames = strings.Fields(`a abbr acronym address applet area article aside audio
b base basefont bdi bdo bgsound big blink blockquote body br button canvas caption center cite
code col colgroup data datalist dd del details dfn dialog dir div dl dt em embed fieldset
figcaption figure font footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html
i iframe image img input ins isindex kbd keygen label legend li link listing main map mark
marquee menu menuitem meta meter multicol nav nextid nobr noembed noframes noscript object ol
optgroup option output p param picture plaintext portal pre progress q rb rp rt rtc ruby s samp
script search section select selectedcontent shadow slot small source spacer span strike strong
style sub summary sup table tbody td template textarea tfoot th thead time title tr track tt u
ul var video wbr xmp`)

// TestTheGuardCoversEveryRawTextElement is the test that was missing, and its
// absence is why the guard shipped covering four elements out of ten. The old
// version iterated the package's own list of raw-text elements, so it could only
// ever confirm what the code already believed.
//
// This one asks the parser instead: for every element name, is an element inside
// it an element? If it is not, the content is raw text and an insertion into it
// can end it, so the guard has to cover it. Both directions are checked, because
// a guard that is too wide refuses content that was fine.
func TestTheGuardCoversEveryRawTextElement(t *testing.T) {
	for _, tag := range htmlElementNames {
		// Is the content parsed as markup? If a <b> inside is reported as an
		// element, yes.
		inner := 0
		if _, err := lolhtml.RewriteString("<"+tag+"><b>x</b></"+tag+">",
			lolhtml.OnElement("b", func(*lolhtml.Element) error { inner++; return nil })); err != nil {
			t.Fatalf("<%s>: %v", tag, err)
		}
		rawText := inner == 0

		// Is an insertion of the element's own closing tag refused?
		_, err := lolhtml.RewriteString("<"+tag+">x</"+tag+">",
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("y</"+tag+">z", lolhtml.HTML)
			}))
		guarded := errors.Is(err, lolhtml.ErrRawTextBreakout)

		switch {
		case tag == "plaintext":
			// The one raw-text element that cannot be closed at all.
			if guarded {
				t.Errorf("<plaintext> was refused, but nothing can close it")
			}
		case rawText && !guarded:
			t.Errorf("<%s> holds raw text and an insertion closed it: %v", tag, err)
		case !rawText && guarded:
			t.Errorf("<%s> parses its content as markup, so </%s> in an insertion "+
				"is ordinary markup and should not be refused: %v", tag, tag, err)
		}
	}
}

// TestPlaintextCannotBeBrokenOutOf is the exception, and it is an exception
// because of how plaintext ends, which is: it does not. Everything after the
// start tag is its content, to the end of the input, so its own closing tag is
// text like anything else and there is nothing to break out of.
//
// Two consequences are worth pinning, because CanHaveContent is true here and
// the usual advice - check CanHaveContent before OnEndTag - does not help.
func TestPlaintextCannotBeBrokenOutOf(t *testing.T) {
	out, err := lolhtml.RewriteString(`<plaintext>a</plaintext>b`,
		lolhtml.OnElement("plaintext", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`x</plaintext>y`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	// The inserted closing tag is content, so nothing followed it out.
	if want := `<plaintext>x</plaintext>y`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}

	// The closing tag in the document is content too.
	var chunks []string
	if _, err := lolhtml.RewriteString(`<plaintext>a</plaintext>b`,
		lolhtml.OnText("plaintext", func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				chunks = append(chunks, c.Text())
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0] != `a</plaintext>b` {
		t.Errorf("text chunks = %q, want one chunk of the whole rest of the input", chunks)
	}

	// And there is no end tag, so Append has nowhere to go and OnEndTag never
	// runs - even though CanHaveContent is true, which is what makes this worth
	// a test rather than a sentence.
	endTags, canHaveContent := 0, false
	out, err = lolhtml.RewriteString(`<plaintext>a</plaintext>b`,
		lolhtml.OnElement("plaintext", func(e *lolhtml.Element) error {
			canHaveContent = e.CanHaveContent()
			if err := e.Append("[appended]", lolhtml.HTML); err != nil {
				return err
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				endTags++
				return nil
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !canHaveContent {
		t.Error("CanHaveContent is false for <plaintext>; the rest of this test assumed it was true")
	}
	if endTags != 0 {
		t.Errorf("the end-tag handler ran %d times", endTags)
	}
	if strings.Contains(out, "[appended]") {
		t.Errorf("Append reached the output: %q", out)
	}
}

// TestForeignContentIsRefusedConservatively. The check is by tag name, like
// selectors, so it does not know that inside SVG or MathML none of these
// elements is raw text. The refusal is still defensible: an inserted "</title>"
// ends an <svg><title> too, by tree construction rather than by the tokenizer.
// Text goes through, which is the way out.
func TestForeignContentIsRefusedConservatively(t *testing.T) {
	const doc = `<svg><title>a</title></svg>`
	_, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`b</title>c`, lolhtml.HTML)
		}))
	if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
		t.Errorf("an <svg><title> insertion was not refused: %v", err)
	}
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`b</title>c`, lolhtml.Text)
		})); err != nil {
		t.Errorf("Text was refused in <svg><title>: %v", err)
	}
}

// TestTheErrorSaysWhatToDoInstead. Refusing an insertion is only half an answer
// if the caller cannot tell what would have worked, and the answer is different
// for every group: a JavaScript escape, a CSS escape, a different ContentType,
// or nothing at all.
func TestTheErrorSaysWhatToDoInstead(t *testing.T) {
	for _, tt := range []struct{ tag, want string }{
		{"script", `<\/script`},
		{"style", `\3c /style`},
		{"textarea", "ContentType Text"},
		{"title", "ContentType Text"},
		{"xmp", "cannot appear inside it"},
		{"iframe", "cannot appear inside it"},
		{"noembed", "cannot appear inside it"},
		{"noframes", "cannot appear inside it"},
		{"noscript", "cannot appear inside it"},
	} {
		_, err := lolhtml.RewriteString("<"+tt.tag+">a</"+tt.tag+">",
			lolhtml.OnElement(tt.tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("b</"+tt.tag+">c", lolhtml.HTML)
			}))
		if err == nil {
			t.Errorf("<%s>: accepted", tt.tag)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("<%s>: error does not suggest %q: %v", tt.tag, tt.want, err)
		}
	}
}

// TestWhatIsStillNotChecked pins the boundary of the check, because a partial
// guard is worse than none if nobody knows where it stops: a caller who has seen
// ErrRawTextBreakout once will assume the next insertion is checked too.
//
// If one of these starts returning ErrRawTextBreakout, that is an improvement,
// not a regression - update the case and the documentation on the error.
func TestWhatIsStillNotChecked(t *testing.T) {
	const bad = `</script><img src=x onerror=alert(1)>`
	const doc = `<script>var x = 1</script><p>after`

	// A text chunk cannot name the element it is inside, so there is nothing for
	// the check to look up. This is the gap that matters, because editing a
	// script through a text handler is the obvious way to do it.
	textChunk := map[string]func(*lolhtml.TextChunk) error{
		"TextChunk.Before":  func(c *lolhtml.TextChunk) error { return c.Before(bad, lolhtml.HTML) },
		"TextChunk.After":   func(c *lolhtml.TextChunk) error { return c.After(bad, lolhtml.HTML) },
		"TextChunk.Replace": func(c *lolhtml.TextChunk) error { return c.Replace(bad, lolhtml.HTML) },
		"TextChunk.StreamReplace": func(c *lolhtml.TextChunk) error {
			return c.StreamReplace(func(s *lolhtml.Sink) error { return s.WriteString(bad, lolhtml.HTML) })
		},
	}
	for name, edit := range textChunk {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnText("script", func(c *lolhtml.TextChunk) error {
			if c.Text() == "" {
				return nil
			}
			return edit(c)
		}))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(out, "onerror") || strings.Count(out, "<img") != 1 {
			t.Errorf("%s: expected the unchecked insertion in the output: %q", name, out)
		}
	}

	// The streaming insertions write in pieces, and a closing tag can straddle
	// two of them, so the check has nothing whole to look at.
	streaming := map[string]func(*lolhtml.Element) error{
		"Element.StreamPrepend": func(e *lolhtml.Element) error {
			return e.StreamPrepend(func(s *lolhtml.Sink) error { return s.WriteString(bad, lolhtml.HTML) })
		},
		"Element.StreamAppend": func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error { return s.WriteString(bad, lolhtml.HTML) })
		},
		"Element.StreamSetInnerContent": func(e *lolhtml.Element) error {
			return e.StreamSetInnerContent(func(s *lolhtml.Sink) error { return s.WriteString(bad, lolhtml.HTML) })
		},
		"EndTag.StreamBefore": func(e *lolhtml.Element) error {
			return e.OnEndTag(func(x *lolhtml.EndTag) error {
				return x.StreamBefore(func(s *lolhtml.Sink) error { return s.WriteString(bad, lolhtml.HTML) })
			})
		},
	}
	for name, edit := range streaming {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("script", edit))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(out, "onerror") {
			t.Errorf("%s: expected the unchecked insertion in the output: %q", name, out)
		}
	}
}

// TestIsRawTextIsMeasuredAgainstTheParser is the same measurement the guard is
// held to, applied to the exported answer: for every element name in the HTML
// index, feed a <b> inside it and see whether the parser reports an element.
// That is the definition - content is not markup - so IsRawText has to agree with
// it name for name, including plaintext, which the guard skips because nothing
// can close it.
func TestIsRawTextIsMeasuredAgainstTheParser(t *testing.T) {
	raw := 0
	for _, tag := range htmlElementNames {
		inner := 0
		if _, err := lolhtml.RewriteString("<"+tag+"><b>x</b></"+tag+">",
			lolhtml.OnElement("b", func(*lolhtml.Element) error { inner++; return nil })); err != nil {
			t.Fatalf("<%s>: %v", tag, err)
		}
		want := inner == 0
		if got := lolhtml.IsRawText(tag); got != want {
			t.Errorf("IsRawText(%q) = %v, but the parser %s report a <b> inside one",
				tag, got, map[bool]string{true: "does", false: "does not"}[!want])
		}
		if want {
			raw++
		}
	}
	// Not vacuous: the index has to contain the names, or agreement means nothing.
	if raw != 10 {
		t.Errorf("the parser reported raw text for %d of the %d names in the index, want 10",
			raw, len(htmlElementNames))
	}
}

// TestIsRawTextCoversThePlaintextCase. plaintext is the one name where the
// exported answer and the guard differ on purpose: its content is not markup, so
// a rename or an unwrap reinterprets it, and no insertion can close it so the
// guard has nothing to refuse.
func TestIsRawTextCoversThePlaintextCase(t *testing.T) {
	if !lolhtml.IsRawText("plaintext") {
		t.Error("IsRawText(\"plaintext\") = false, but its content is not markup")
	}
	_, err := lolhtml.RewriteString("<plaintext>x",
		lolhtml.OnElement("plaintext", func(e *lolhtml.Element) error {
			return e.SetInnerContent("y</plaintext>z", lolhtml.HTML)
		}))
	if errors.Is(err, lolhtml.ErrRawTextBreakout) {
		t.Error("the guard refused an insertion into a plaintext, which nothing can close")
	}
}

// TestIsRawTextIgnoresCase, so it takes what either accessor reports.
// TagNamePreserveCase keeps the spelling from the document, which is where a
// caller deciding whether to unwrap is most likely to get its name.
func TestIsRawTextIgnoresCase(t *testing.T) {
	for _, tag := range []string{"SCRIPT", "Script", "sCrIpT", "PLAINTEXT", "TeXtArEa"} {
		if !lolhtml.IsRawText(tag) {
			t.Errorf("IsRawText(%q) = false", tag)
		}
	}
	for _, tag := range []string{"DIV", "Span", "scriptx", "", "  script  ", "script "} {
		if lolhtml.IsRawText(tag) {
			t.Errorf("IsRawText(%q) = true; it is a tag name, not a search", tag)
		}
	}
}

// TestIsRawTextAnswersTheQuestionRemoveAndKeepContentAsks. The two hazards the
// guard does not cover: a rename and an unwrap turn raw text into markup without
// inserting anything. Guarding on IsRawText is the fix, and this is the
// measurement that it is one.
func TestIsRawTextAnswersTheQuestionRemoveAndKeepContentAsks(t *testing.T) {
	const doc = `<noembed><img src=x onerror=alert(1)></noembed>`

	unguarded, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if e.TagName() != "img" {
				e.RemoveAndKeepContent()
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unguarded, "noembed") || !strings.Contains(unguarded, "onerror") {
		t.Fatalf("unwrapping the noembed should have left its content bare: %q", unguarded)
	}
	images := 0
	if _, err := lolhtml.RewriteString(unguarded,
		lolhtml.OnElement("img", func(*lolhtml.Element) error { images++; return nil })); err != nil {
		t.Fatal(err)
	}
	if images != 1 {
		t.Fatalf("the unwrapped content parses as %d images, want 1 - that is the hazard", images)
	}

	guarded, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if lolhtml.IsRawText(e.TagName()) {
				e.Remove()
				return nil
			}
			if e.TagName() != "img" {
				e.RemoveAndKeepContent()
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if guarded != "" {
		t.Errorf("guarded on IsRawText the noembed should have gone entirely, got %q", guarded)
	}
}

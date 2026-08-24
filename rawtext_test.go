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

var rawTextTags = []string{"script", "style", "textarea", "title"}

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

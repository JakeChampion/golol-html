package lolhtml_test

// IsSelfClosing against CanHaveContent.
//
// One is about how the tag was written and the other about what the element can
// hold, and in HTML they disagree: a trailing slash is ignored by the parser and
// still reported here. Anything using IsSelfClosing to decide whether an element
// is empty is wrong for <div/>, which authors write out of habit.

import (
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func TestSelfClosingIsAboutTheSourceNotTheElement(t *testing.T) {
	for _, tt := range []struct {
		doc                         string
		selector                    string
		selfClosing, canHaveContent bool
	}{
		// HTML: the slash is ignored by the parser and reported here.
		{`<div/>`, "div", true, true},
		{`<div></div>`, "div", false, true},
		{`<p/>`, "p", true, true},
		{`<span/>`, "span", true, true},
		{`<custom-el/>`, "custom-el", true, true},

		// Void elements cannot hold content either way.
		{`<br>`, "br", false, false},
		{`<br/>`, "br", true, false},
		{`<br />`, "br", true, false},
		{`<img src="x">`, "img", false, false},
		{`<img src="x"/>`, "img", true, false},
		{`<input>`, "input", false, false},
		{`<input/>`, "input", true, false},

		// Foreign content: the slash is what closes the element, so the two
		// agree and the answer differs between the two spellings.
		{`<svg><rect/></svg>`, "rect", true, false},
		{`<svg><rect></rect></svg>`, "rect", false, true},
		{`<svg><rect>`, "rect", false, true},
		{`<svg/>`, "svg", true, false},
		{`<svg></svg>`, "svg", false, true},
		{`<math><mi/></math>`, "mi", true, false},
	} {
		var sc, cc bool
		var seen int
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.selector, func(e *lolhtml.Element) error {
				seen++
				sc, cc = e.IsSelfClosing(), e.CanHaveContent()
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.doc, err)
		}
		if seen != 1 {
			t.Errorf("%s: matched %d times", tt.doc, seen)
			continue
		}
		if sc != tt.selfClosing || cc != tt.canHaveContent {
			t.Errorf("%s: IsSelfClosing=%v CanHaveContent=%v, want %v and %v",
				tt.doc, sc, cc, tt.selfClosing, tt.canHaveContent)
		}
	}
}

// TestASelfClosingHTMLElementStillHasContent is the consequence: the div reaches
// an end-tag handler and takes an Append, so a rewrite that skipped it on the
// strength of IsSelfClosing would have skipped a live element.
func TestASelfClosingHTMLElementStillHasContent(t *testing.T) {
	endTags := 0
	out, err := lolhtml.RewriteString(`<div/>text</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if !e.IsSelfClosing() {
				t.Error("the div does not report itself self-closing, so this test " +
					"is measuring nothing")
			}
			if err := e.Append("[appended]", lolhtml.Text); err != nil {
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
	if out != `<div/>text[appended]</div>` {
		t.Errorf("got %q; the Append did not land inside the element", out)
	}
	if endTags != 1 {
		t.Errorf("the end-tag handler ran %d times, want 1", endTags)
	}
}

// TestASelfClosingForeignElementHasNeither, which is the case IsSelfClosing is
// for: Append does nothing and OnEndTag is an error.
func TestASelfClosingForeignElementHasNeither(t *testing.T) {
	out, err := lolhtml.RewriteString(`<svg><rect/>after</svg>`,
		lolhtml.OnElement("rect", func(e *lolhtml.Element) error {
			if !e.IsSelfClosing() || e.CanHaveContent() {
				t.Errorf("rect reports self-closing=%v content=%v",
					e.IsSelfClosing(), e.CanHaveContent())
			}
			// Silently does nothing, per CanHaveContent's documentation.
			return e.Append("[appended]", lolhtml.Text)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<svg><rect/>after</svg>` {
		t.Errorf("got %q, want the document unchanged", out)
	}

	// And OnEndTag is the one that fails rather than doing nothing.
	_, err = lolhtml.RewriteString(`<svg><rect/></svg>`,
		lolhtml.OnElement("rect", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		}))
	if err == nil {
		t.Error("OnEndTag on a self-closing foreign element succeeded")
	}
}

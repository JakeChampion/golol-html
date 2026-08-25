package lolhtml_test

// What user data is for, and the one thing it was documented for and cannot do.
//
// lol-html provides user data on elements, comments, text chunks and the
// doctype. Not on end tags - there is no lol_html_end_tag_user_data_get - so the
// use this was documented for, reading it in an end-tag handler, was never
// possible.

import (
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestUserDataReachesAnotherHandlerForTheSameElement is what it is for: two
// selectors matching one element.
func TestUserDataReachesAnotherHandlerForTheSameElement(t *testing.T) {
	var got any
	if _, err := lolhtml.RewriteString(`<div class="a" id="b">x</div>`,
		lolhtml.OnElement(".a", func(e *lolhtml.Element) error {
			return e.SetUserData("from .a")
		}),
		lolhtml.OnElement("#b", func(e *lolhtml.Element) error {
			got = e.UserData()
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	if got != "from .a" {
		t.Errorf("the second handler read %v, want %q", got, "from .a")
	}

	// And back in the same handler, which is the degenerate case.
	var same any
	if _, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if err := e.SetUserData(42); err != nil {
				return err
			}
			same = e.UserData()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if same != 42 {
		t.Errorf("read back %v, want 42", same)
	}
}

// TestAnEndTagHandlerCannotReadTheElementsUserData is the claim the
// documentation used to make. EndTag has no user data of its own, and the
// element is detached by then, so the captured Element reads nil.
func TestAnEndTagHandlerCannotReadTheElementsUserData(t *testing.T) {
	var fromEndTag any
	ran := 0
	if _, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if err := e.SetUserData("hello"); err != nil {
				return err
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				ran++
				fromEndTag = e.UserData()
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("the end-tag handler ran %d times", ran)
	}
	if fromEndTag != nil {
		t.Errorf("the end-tag handler read %v; the element is detached by then and "+
			"this is expected to be nil", fromEndTag)
	}

	// Closing over a Go variable is the answer, and it works.
	var closedOver string
	if _, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			tag := e.TagName()
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				closedOver = tag
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if closedOver != "div" {
		t.Errorf("closing over the value gave %q", closedOver)
	}
}

// It is per unit. Two elements do not share it, and neither do two chunks of one
// text node - which matters because accumulating across chunks is the documented
// pattern for text and this looks like somewhere to put the accumulator.
func TestUserDataIsPerUnit(t *testing.T) {
	var divs []any
	if _, err := lolhtml.RewriteString(`<div>a</div><div>b</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			divs = append(divs, e.UserData())
			return e.SetUserData("set")
		})); err != nil {
		t.Fatal(err)
	}
	if len(divs) != 2 || divs[0] != nil || divs[1] != nil {
		t.Errorf("successive elements saw %v, want two nils", divs)
	}

	var chunks []any
	if _, err := lolhtml.RewriteString(`<p>hello</p>`,
		lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
			chunks = append(chunks, c.UserData())
			return c.SetUserData("mark")
		})); err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("a text node reported %d chunks", len(chunks))
	}
	for i, v := range chunks {
		if v != nil {
			t.Errorf("chunk %d read %v; each chunk is its own unit", i, v)
		}
	}
}

// A comment is seen by both a selector handler and a document handler, so this
// is the one place user data crosses handlers without needing two selectors.
func TestUserDataOnAComment(t *testing.T) {
	var fromDocument any
	if _, err := lolhtml.RewriteString(`<div><!-- c --></div>`,
		lolhtml.OnComment("div", func(c *lolhtml.Comment) error {
			return c.SetUserData("from the selector handler")
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			fromDocument = c.UserData()
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	if fromDocument != "from the selector handler" {
		t.Errorf("the document handler read %v", fromDocument)
	}
}

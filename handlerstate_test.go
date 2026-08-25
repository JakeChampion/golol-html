package lolhtml_test

// Handlers on the same element share the element. Which handlers fire is settled
// before any of them runs - an edit never changes that, in either direction - and
// what they read is not: the second handler sees the first one's attribute values and
// tag name. So there is order-dependence after all, in the results rather than in the
// firing, and the shape it takes in a real rewrite is an attribute rewritten twice.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestASecondHandlerReadsTheFirstOnesEdit.
func TestASecondHandlerReadsTheFirstOnesEdit(t *testing.T) {
	const doc = `<img src="/a.js" alt="x">`
	var reads []string
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("img[src]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("src")
			reads = append(reads, v)
			return e.SetAttribute("src", v+"?v=1")
		}),
		lolhtml.OnElement("[src]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("src")
			reads = append(reads, v)
			return e.SetAttribute("src", v+"?v=2")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/a.js", "/a.js?v=1"}; strings.Join(reads, " ") != strings.Join(want, " ") {
		t.Errorf("the handlers read %v, want %v", reads, want)
	}
	if want := `<img src="/a.js?v=1?v=2" alt="x">`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
}

// TestRegistrationOrderDecidesTheResult, which is the part "no order-dependence"
// does not cover: the firing is order-independent and the outcome is not.
func TestRegistrationOrderDecidesTheResult(t *testing.T) {
	const doc = `<img src="/a.js">`
	first := lolhtml.OnElement("img[src]", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("src")
		return e.SetAttribute("src", v+"?one")
	})
	second := lolhtml.OnElement("[src]", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("src")
		return e.SetAttribute("src", v+"?two")
	})
	a, err := lolhtml.RewriteString(doc, first, second)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lolhtml.RewriteString(doc, second, first)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("both orders gave %q; the order should show", a)
	}
	if want := `<img src="/a.js?one?two">`; a != want {
		t.Errorf("\n got %q\nwant %q", a, want)
	}
	if want := `<img src="/a.js?two?one">`; b != want {
		t.Errorf("\n got %q\nwant %q", b, want)
	}
}

// TestATagRenameIsVisibleToALaterHandlerAndNotToTheSelectors: the two halves of the
// rule in one document.
func TestATagRenameIsVisibleToALaterHandlerAndNotToTheSelectors(t *testing.T) {
	const doc = `<img src="/a.js">`
	var seen []string
	var pictures int
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			return e.SetTagName("picture")
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			seen = append(seen, e.TagName())
			return nil
		}),
		lolhtml.OnElement("picture", func(*lolhtml.Element) error {
			pictures++
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, " ") != "picture" {
		t.Errorf("the later handler saw %v, want the new name", seen)
	}
	if pictures != 0 {
		t.Errorf("a handler on the new name fired %d times, want 0: matching was settled first", pictures)
	}
	if want := `<picture src="/a.js">`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
}

// TestAClassAddedByAHandlerDoesNotMatch, which is the documented half and the reason
// the other half is surprising.
func TestAClassAddedByAHandlerDoesNotMatch(t *testing.T) {
	var fired int
	out, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("class", "new")
		}),
		lolhtml.OnElement(".new", func(*lolhtml.Element) error {
			fired++
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fired != 0 {
		t.Errorf(".new fired %d times, want 0", fired)
	}
	if want := `<p class="new">x</p>`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
	// And a handler can still read the class it did not match on.
	var read string
	if _, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("class", "new")
		}),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			read, _ = e.Attribute("class")
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	if read != "new" {
		t.Errorf("the second handler read %q, want %q", read, "new")
	}
}

// TestSettingTheSameAttributeTwiceInOneHandlerKeepsTheLast, and reading between the
// two calls gives the first: the element is state, not a queue of edits.
func TestSettingTheSameAttributeTwiceInOneHandlerKeepsTheLast(t *testing.T) {
	var between string
	out, err := lolhtml.RewriteString(`<img src="/a.js" alt="x">`,
		lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			if err := e.SetAttribute("src", "one"); err != nil {
				return err
			}
			between, _ = e.Attribute("src")
			return e.SetAttribute("src", "two")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if between != "one" {
		t.Errorf("between the calls the value read %q, want %q", between, "one")
	}
	if want := `<img src="two" alt="x">`; out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
}

// TestRemovingAnAttributeAndSettingItAgainMovesIt, which matters to anything
// comparing output byte for byte.
func TestRemovingAnAttributeAndSettingItAgainMovesIt(t *testing.T) {
	out, err := lolhtml.RewriteString(`<img src="/a.js" alt="x">`,
		lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			if err := e.RemoveAttribute("src"); err != nil {
				return err
			}
			if v, ok := e.Attribute("src"); ok || v != "" {
				t.Errorf("after removing it, Attribute gave %q, %v", v, ok)
			}
			return e.SetAttribute("src", "back")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<img alt="x" src="back">`; out != want {
		t.Errorf("\n got %q\nwant %q - the attribute goes to the end", out, want)
	}
	// Setting one and removing it again leaves the tag as it was, byte for byte.
	const doc = `<img src="/a.js" alt="x">`
	out, err = lolhtml.RewriteString(doc, lolhtml.OnElement("img", func(e *lolhtml.Element) error {
		if err := e.SetAttribute("data-x", "1"); err != nil {
			return err
		}
		return e.RemoveAttribute("data-x")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out != doc {
		t.Errorf("\n got %q\nwant %q", out, doc)
	}
}

package lolhtml_test

// An attribute that appears twice, and which copy each part of the API acts on.
//
// The HTML parsing specification calls a repeat a parse error and drops all but
// the first, so a browser's DOM never has two. lol-html keeps them, and the API
// is split: matching, reading and writing take the first, while iteration and
// removal take all of them. Two of those halves were recorded in comments in the
// properties module; the selector half was not recorded anywhere, which is what
// this file is mainly for.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestASelectorMatchesTheFirstCopyOnly. This is the one that had not been
// measured, and it is the one a rewrite is most likely to depend on without
// knowing: a filter keyed on an attribute value silently skips an element whose
// first copy of that attribute says something else.
func TestASelectorMatchesTheFirstCopyOnly(t *testing.T) {
	for _, tt := range []struct {
		doc, selector string
		want          int
	}{
		{`<p a="x" a="v">t</p>`, `[a="x"]`, 1},
		{`<p a="x" a="v">t</p>`, `[a="v"]`, 0},
		{`<p a="v" a="x">t</p>`, `[a="v"]`, 1},
		{`<p a="v" a="x">t</p>`, `[a="x"]`, 0},

		// The shorthands and the other operators follow it too.
		{`<p class="one" class="two">t</p>`, `.one`, 1},
		{`<p class="one" class="two">t</p>`, `.two`, 0},
		{`<p id="a" id="b">t</p>`, `#a`, 1},
		{`<p id="a" id="b">t</p>`, `#b`, 0},
		{`<p a="x" a="v">t</p>`, `[a~="v"]`, 0},
		{`<p a="x" a="v">t</p>`, `[a^="v"]`, 0},
		{`<p a="x" a="v">t</p>`, `[a*="v"]`, 0},

		// Presence is presence either way.
		{`<p a="x" a="v">t</p>`, `[a]`, 1},
	} {
		n := 0
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement(tt.selector, func(*lolhtml.Element) error {
				n++
				return nil
			})); err != nil {
			t.Fatalf("%s on %s: %v", tt.selector, tt.doc, err)
		}
		if n != tt.want {
			t.Errorf("%s on %s matched %d times, want %d", tt.selector, tt.doc, n, tt.want)
		}
	}
}

// TestReadingAndWritingUseTheFirstCopy, so a read-decide-write rewrite is
// self-consistent.
func TestReadingAndWritingUseTheFirstCopy(t *testing.T) {
	var read string
	out, err := lolhtml.RewriteString(`<p a="1" a="2">t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			read, _ = e.Attribute("a")
			return e.SetAttribute("a", "3")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if read != "1" {
		t.Errorf("Attribute read %q, want the first copy %q", read, "1")
	}
	if out != `<p a="3" a="2">t</p>` {
		t.Errorf("SetAttribute produced %q; the first copy should carry the new value", out)
	}
}

// TestIterationYieldsEveryCopy, which is the opposite choice, and the one that
// matters for a program reporting on a document rather than rewriting it.
func TestIterationYieldsEveryCopy(t *testing.T) {
	var iterated, listed []string
	if _, err := lolhtml.RewriteString(`<p a="1" b="2" a="3">t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			for name, value := range e.Attributes() {
				iterated = append(iterated, name+"="+value)
			}
			for _, a := range e.AttributeList() {
				listed = append(listed, a.Name+"="+a.Value)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	want := "a=1,b=2,a=3"
	if strings.Join(iterated, ",") != want {
		t.Errorf("Attributes yielded %v, want %s", iterated, want)
	}
	if strings.Join(listed, ",") != want {
		t.Errorf("AttributeList gave %v, want %s", listed, want)
	}
}

// TestRemovalTakesEveryCopy. Already pinned in the properties module; repeated
// here so the whole rule is legible in one file.
func TestRemovalTakesEveryCopy(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p a="1" b="2" a="3">t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.RemoveAttribute("a")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p b="2">t</p>` {
		t.Errorf("got %q, want both copies of a gone", out)
	}
}

// TestHasAttributeAgreesWithAttribute, so the two ways of asking do not disagree
// about a repeated attribute.
func TestHasAttributeAgreesWithAttribute(t *testing.T) {
	for _, doc := range []string{
		`<p a="1" a="2">t</p>`,
		`<p a="" a="2">t</p>`,
		`<p a a="2">t</p>`,
		`<p b="1">t</p>`,
	} {
		var has, present bool
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				var err error
				has, err = e.HasAttribute("a")
				if err != nil {
					return err
				}
				_, present = e.Attribute("a")
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if has != present {
			t.Errorf("%s: HasAttribute=%v but Attribute present=%v", doc, has, present)
		}
	}
}

// TestSettingAnAbsentAttributeAddsOne, for completeness of the write side: the
// first-copy rule is about a repeat that already exists.
func TestSettingAnAbsentAttributeAddsOne(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p b="2">t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("a", "1")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p b="2" a="1">t</p>` {
		t.Errorf("got %q", out)
	}
}

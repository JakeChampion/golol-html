package lolhtml_test

// What a handler inside a removed element can find out, and what it cannot.
//
// Removal suppresses output and not handler calls, so handlers run over content
// that is on its way out. Whether they can tell depends on which handler: an
// element knows, because IsRemoved answers for an ancestor, and a text chunk does
// not.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const removedDoc = `<div id="outer"><p id="inner">x<!--c--></p></div><p id="after">y</p>`

// TestIsRemovedAnswersForAnAncestor, for the two operations that take the content
// with them and not for the one that keeps it.
func TestIsRemovedAnswersForAnAncestor(t *testing.T) {
	for _, tc := range []struct {
		what        string
		remove      func(*lolhtml.Element) error
		innerWant   bool
		outputWants string
	}{
		{"Remove", func(e *lolhtml.Element) error { e.Remove(); return nil }, true,
			`<p id="after">y</p>`},
		{"Replace", func(e *lolhtml.Element) error { return e.Replace("[R]", lolhtml.HTML) }, true,
			`[R]<p id="after">y</p>`},
		{"RemoveAndKeepContent", func(e *lolhtml.Element) error { e.RemoveAndKeepContent(); return nil }, false,
			`<p id="inner">x<!--c--></p><p id="after">y</p>`},
		{"nothing", func(*lolhtml.Element) error { return nil }, false, removedDoc},
	} {
		got := map[string]bool{}
		out, err := lolhtml.RewriteString(removedDoc,
			lolhtml.OnElement("#outer", tc.remove),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				id, _ := e.Attribute("id")
				got[id] = e.IsRemoved()
				return nil
			}))
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if got["inner"] != tc.innerWant {
			t.Errorf("%s: the inner element reports removed=%v, want %v",
				tc.what, got["inner"], tc.innerWant)
		}
		if got["after"] {
			t.Errorf("%s: an element after the removed one reports removed", tc.what)
		}
		if out != tc.outputWants {
			t.Errorf("%s\n got %q\nwant %q", tc.what, out, tc.outputWants)
		}
	}
}

// TestATextChunkDoesNotKnow, which is the asymmetry worth documenting: the
// handler that most often accumulates is the one that cannot tell.
func TestATextChunkDoesNotKnow(t *testing.T) {
	texts := map[string]bool{}
	comments := map[string]bool{}
	if _, err := lolhtml.RewriteString(removedDoc,
		lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				texts[c.Text()] = c.IsRemoved()
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			comments[c.Text()] = c.IsRemoved()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if _, ok := texts["x"]; !ok {
		t.Fatal("the text of the removed element was not reported at all")
	}
	if texts["x"] {
		t.Error(`the text of a removed element reports removed=true; if that is the ` +
			"new behaviour, the documentation and the depth-counter recipe should go")
	}
	if comments["c"] {
		t.Error("the comment of a removed element reports removed=true")
	}
}

// TestAnInsertionInsideARemovedSubtreeIsDiscarded, for every position, so the
// "insert first, remove last" hazard is about one element's own handlers rather
// than about a subtree.
func TestAnInsertionInsideARemovedSubtreeIsDiscarded(t *testing.T) {
	for _, tc := range []struct {
		what string
		fn   func(*lolhtml.Element) error
	}{
		{"Before", func(e *lolhtml.Element) error { return e.Before("[X]", lolhtml.HTML) }},
		{"After", func(e *lolhtml.Element) error { return e.After("[X]", lolhtml.HTML) }},
		{"Prepend", func(e *lolhtml.Element) error { return e.Prepend("[X]", lolhtml.HTML) }},
		{"Append", func(e *lolhtml.Element) error { return e.Append("[X]", lolhtml.HTML) }},
		{"Replace", func(e *lolhtml.Element) error { return e.Replace("[X]", lolhtml.HTML) }},
		{"SetInnerContent", func(e *lolhtml.Element) error { return e.SetInnerContent("[X]", lolhtml.HTML) }},
		{"SetAttribute", func(e *lolhtml.Element) error { return e.SetAttribute("data-x", "[X]") }},
	} {
		out, err := lolhtml.RewriteString(removedDoc,
			lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
				e.Remove()
				return nil
			}),
			lolhtml.OnElement("#inner", tc.fn))
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if strings.Contains(out, "[X]") {
			t.Errorf("%s: the insertion escaped the removed subtree: %q", tc.what, out)
		}
		if out != `<p id="after">y</p>` {
			t.Errorf("%s: got %q", tc.what, out)
		}
	}

	// The same from a text handler, and from as deep as a grandchild.
	out, err := lolhtml.RewriteString(removedDoc,
		lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() == "x" {
				return c.Before("[X]", lolhtml.HTML)
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "[X]") {
		t.Errorf("a text handler's insertion escaped: %q", out)
	}

	out, err = lolhtml.RewriteString(`<div id="outer"><p><b id="deep">x</b></p></div><i>y</i>`,
		lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("#deep", func(e *lolhtml.Element) error {
			return e.Before("[X]", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != "<i>y</i>" {
		t.Errorf("a grandchild's insertion escaped: %q", out)
	}
}

// TestEndTagHandlersRunInsideARemovedSubtree, which is what makes the depth
// counter in the documentation work: the counter has to come back down.
func TestEndTagHandlersRunInsideARemovedSubtree(t *testing.T) {
	var log []string
	if _, err := lolhtml.RewriteString(removedDoc,
		lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			name := e.TagName()
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				log = append(log, name+"/"+t.Name())
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if want := "p/p div/div p/p"; strings.Join(log, " ") != want {
		t.Errorf("end tags seen: %q, want %q", strings.Join(log, " "), want)
	}
}

// TestTheDepthCounterRecipeWorks. The documentation's answer for a text handler
// that accumulates, run on a document where half the text is on its way out.
func TestTheDepthCounterRecipeWorks(t *testing.T) {
	const doc = `<p>keep one</p><div class="gone"><p>drop this</p><p>and this</p></div><p>keep two</p>`

	var counted, all strings.Builder
	depth := 0
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(".gone", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !e.IsRemoved() || !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			all.WriteString(c.Text())
			if depth > 0 {
				return nil
			}
			counted.WriteString(c.Text())
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p>keep one</p><p>keep two</p>` {
		t.Fatalf("got %q", out)
	}
	// Without the guard a text handler counts what the output does not contain.
	if got := all.String(); got != "keep onedrop thisand thiskeep two" {
		t.Errorf("the handler saw %q", got)
	}
	if got := counted.String(); got != "keep onekeep two" {
		t.Errorf("with the guard it counted %q, want %q", got, "keep onekeep two")
	}
	if depth != 0 {
		t.Errorf("the counter ended at %d", depth)
	}
}

// TestTheRecipeSurvivesAMissingEndTag, which is where a depth counter usually
// goes wrong: the element that was removed has no end tag of its own, so the
// callback arrives on someone else's.
func TestTheRecipeSurvivesAMissingEndTag(t *testing.T) {
	// The removed <li> is closed by </ul>, so the counter comes back down there -
	// after the second item, whose text is inside the removal anyway.
	const doc = `<ul><li class="gone">drop<li>also dropped</ul><p>keep</p>`
	var counted strings.Builder
	depth := 0
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(".gone", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !e.IsRemoved() || !e.CanHaveContent() {
				return nil
			}
			depth++
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				depth--
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if depth == 0 {
				counted.WriteString(c.Text())
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := counted.String(), "keep"; got != want {
		t.Errorf("counted %q, want %q - and the output was %q", got, want, out)
	}
	if depth != 0 {
		t.Errorf("the counter ended at %d", depth)
	}
	if !strings.Contains(out, "keep") || strings.Contains(out, "drop") {
		t.Errorf("got %q", out)
	}
}

// TestTheAncestorAnswerIsNotJustTheFirstChild, so the propagation is real rather
// than a coincidence of shape.
func TestTheAncestorAnswerIsNotJustTheFirstChild(t *testing.T) {
	const doc = `<div id="outer"><a>1</a><b>2</b><i><s>3</s></i></div><em>4</em>`
	got := map[string]bool{}
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("#outer", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("a,b,i,s,em", func(e *lolhtml.Element) error {
			got[e.TagName()] = e.IsRemoved()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"a", "b", "i", "s"} {
		if !got[tag] {
			t.Errorf("<%s> inside the removed element reports removed=false", tag)
		}
	}
	if got["em"] {
		t.Error("<em> after the removed element reports removed=true")
	}
	if len(got) != 5 {
		t.Errorf("saw %v, want all five elements", got)
	}
}

// TestTheDocumentedCornerIsStillTheCorner: the one place an insertion does escape
// is the removed element's own handler, inserting after the removal.
func TestTheDocumentedCornerIsStillTheCorner(t *testing.T) {
	for _, tc := range []struct {
		what, want string
		fn         func(*lolhtml.Element) error
	}{
		{"append after remove", "<div>x</div>", func(e *lolhtml.Element) error {
			e.Remove()
			return e.Append("x", lolhtml.HTML)
		}},
		{"remove after append", "<div></div>", func(e *lolhtml.Element) error {
			if err := e.Append("x", lolhtml.HTML); err != nil {
				return err
			}
			e.Remove()
			return nil
		}},
	} {
		out, err := lolhtml.RewriteString(`<div><p>gone</p></div>`,
			lolhtml.OnElement("p", tc.fn))
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if out != tc.want {
			t.Errorf("%s: got %q, want %q", tc.what, out, tc.want)
		}
	}
}

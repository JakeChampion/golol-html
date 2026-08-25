package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// annotate is the whole pipeline over a string, which is what most of these tests want.
func annotate(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Annotate(strings.NewReader(doc), "data-island", "visible")
	if err != nil {
		t.Fatalf("Annotate(%q): %v", doc, err)
	}
	return res
}

// nest builds a document with islands nested depth deep and count siblings at each level, so a
// property has something with structure in it rather than a fixed case.
func nest(depth, count int) string {
	if depth == 0 {
		return "text"
	}
	var b strings.Builder
	for i := range count {
		fmt.Fprintf(&b, `<div data-island="d%dn%d">%s</div>`, depth, i, nest(depth-1, count))
	}
	return b.String()
}

// TestASelectorCannotSayNotInsideAnotherIsland, which is why this program keeps a stack. The
// negation is rejected while the plain descendant selector works, so the rejection is not the
// rule the library documents - and this test would notice upstream changing its mind.
func TestASelectorCannotSayNotInsideAnotherIsland(t *testing.T) {
	doc := `<div data-island="a"><div data-island="b">x</div></div>`

	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`[data-island]:not([data-island] [data-island])`,
			func(*lolhtml.Element) error { return nil })); err == nil {
		t.Error("a descendant combinator inside :not() was accepted")
	}

	// Every combinator, inside :not() and outside it. Inside is rejected; outside is not.
	for _, combinator := range []string{" ", " > ", " + ", " ~ "} {
		inside := fmt.Sprintf(`div:not(div%sp)`, combinator)
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(inside, func(*lolhtml.Element) error { return nil })); err == nil {
			t.Errorf("%q was accepted", inside)
		}
	}
	for _, sel := range []string{`div p`, `div > p`} {
		if _, err := lolhtml.RewriteString(`<div><p>x</p></div>`,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%q: %v", sel, err)
		}
	}

	// And what :not() does accept, so the boundary is pinned on both sides.
	for _, sel := range []string{`:not(div)`, `:not(.a)`, `:not([x])`, `:not(*)`,
		`:not(div.a)`, `:not(div, span)`, `:not(:first-child)`, `:not(:not(div))`} {
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%q was rejected: %v", sel, err)
		}
	}
}

// TestTheStackAgreesWithTheDescendantSelector: the nesting is computed by a stack, and the one
// question a selector *can* answer is "is this inside another island". Two methods, and a
// property over documents with structure - if they ever disagree the stack is wrong.
func TestTheStackAgreesWithTheDescendantSelector(t *testing.T) {
	for depth := 1; depth <= 5; depth++ {
		for count := 1; count <= 3; count++ {
			doc := nest(depth, count)
			res := annotate(t, doc)
			bySelector, err := NestedBySelector(doc, "data-island")
			if err != nil {
				t.Fatal(err)
			}
			if res.Nested() != bySelector {
				t.Errorf("depth %d count %d: the stack found %d nested islands and the "+
					"selector %d", depth, count, res.Nested(), bySelector)
			}
			if got := res.Deepest(); got != depth {
				t.Errorf("depth %d count %d: deepest is %d", depth, count, got)
			}
			if res.DepthAtEnd != 0 {
				t.Errorf("depth %d count %d: %d islands left open",
					depth, count, res.DepthAtEnd)
			}
		}
	}
}

// TestEveryIslandGetsOneUniqueIdThatIsInTheDocument, over the same generated documents: the
// manifest and the markup have to describe the same set, or a bundler hydrates something that
// is not there.
func TestEveryIslandGetsOneUniqueIdThatIsInTheDocument(t *testing.T) {
	for depth := 1; depth <= 5; depth++ {
		for count := 1; count <= 3; count++ {
			res := annotate(t, nest(depth, count))
			seen := map[string]bool{}
			for _, is := range res.Islands {
				if seen[is.ID] {
					t.Errorf("depth %d count %d: %s twice", depth, count, is.ID)
				}
				seen[is.ID] = true

				want := fmt.Sprintf(`data-island-id="%s"`, is.ID)
				if n := strings.Count(res.Doc, want); n != 1 {
					t.Errorf("depth %d count %d: %s appears %d times in the document",
						depth, count, is.ID, n)
				}
			}
			if n := strings.Count(res.Doc, "data-island-id="); n != len(res.Islands) {
				t.Errorf("depth %d count %d: %d ids in the document and %d in the "+
					"manifest", depth, count, n, len(res.Islands))
			}
		}
	}
}

// TestEveryParentIsAnIslandThatEnclosesIt, which is the claim the annotation makes. A parent id
// must belong to an island that opened earlier and had not closed.
func TestEveryParentIsAnIslandThatEnclosesIt(t *testing.T) {
	res := annotate(t, nest(4, 2))
	byID := map[string]Island{}
	for _, is := range res.Islands {
		byID[is.ID] = is
	}
	for _, is := range res.Islands {
		if is.Parent == "" {
			if is.Depth != 0 {
				t.Errorf("%s has no parent but depth %d", is.ID, is.Depth)
			}
			continue
		}
		parent, ok := byID[is.Parent]
		if !ok {
			t.Errorf("%s names %s, which is not an island", is.ID, is.Parent)
			continue
		}
		if parent.Depth != is.Depth-1 {
			t.Errorf("%s at depth %d names %s at depth %d",
				is.ID, is.Depth, parent.ID, parent.Depth)
		}
		// The parent's markup has to enclose the child's, which the offsets of the two
		// ids in the output show.
		pi := strings.Index(res.Doc, fmt.Sprintf(`data-island-id="%s"`, parent.ID))
		ci := strings.Index(res.Doc, fmt.Sprintf(`data-island-id="%s"`, is.ID))
		if pi < 0 || ci < 0 || pi > ci {
			t.Errorf("%s does not appear before its child %s", parent.ID, is.ID)
		}
	}
}

// TestAVoidIslandDoesNotAbortTheRewrite. OnEndTag on an element with no end tag returns an error
// that fails the whole rewrite, so an <img data-island> would produce no output at all without
// the CanHaveContent check. This is the test that would notice the check going away.
func TestAVoidIslandDoesNotAbortTheRewrite(t *testing.T) {
	for _, doc := range []string{
		`<img data-island="Hero" src="/h.png">`,
		`<div data-island="Outer"><img data-island="Hero"><br data-island="Rule"></div>`,
		`<form data-island="F"><input data-island="I" name="q"></form>`,
	} {
		res := annotate(t, doc)
		if res.Doc == "" {
			t.Errorf("%s: the rewrite produced nothing", doc)
		}
		if len(res.Islands) == 0 {
			t.Errorf("%s: no islands", doc)
		}
		voids := countVoid(res.Islands)
		if voids == 0 {
			t.Errorf("%s: no island was reported as void", doc)
		}
		if !strings.Contains(res.String(), "cannot contain anything") {
			t.Errorf("%s: the report does not mention it:\n%s", doc, res)
		}
		// A void island is on the stack for nothing, so nothing claims it as a parent.
		for _, is := range res.Islands {
			if !is.Void {
				continue
			}
			for _, other := range res.Islands {
				if other.Parent == is.ID {
					t.Errorf("%s is inside void island %s", other.ID, is.ID)
				}
			}
		}
	}

	// And the guard is the only thing standing between this and a failed rewrite: asking a
	// void element for its end tag really does fail the run.
	_, err := lolhtml.RewriteString(`<img data-island="Hero">`,
		lolhtml.OnElement("[data-island]", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		}))
	if err == nil {
		t.Error("OnEndTag on a void element succeeded; the guard is no longer needed")
	}
}

// TestPropsAreDecodedForTheManifestAndLeftAloneInTheDocument, which is the rule the whole library
// runs on: decide on the decoded form, write back the raw one. Writing the decoded value into the
// document would double-escape it.
func TestPropsAreDecodedForTheManifestAndLeftAloneInTheDocument(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`a &amp; b`, `a & b`},
		{`&lt;p&gt;`, `<p>`},
		{`&quot;q&quot;`, `"q"`},
		{`&#39;`, `'`},
		{`caf&eacute;`, `café`},
		{`plain`, `plain`},
	}
	for _, tt := range cases {
		doc := fmt.Sprintf(`<div data-island="X" data-prop-v="%s">t</div>`, tt.raw)
		res := annotate(t, doc)
		if len(res.Islands) != 1 {
			t.Fatalf("%s: %d islands", doc, len(res.Islands))
		}
		if got := res.Islands[0].Props["v"]; got != tt.want {
			t.Errorf("%s: the manifest says %q, want %q", tt.raw, got, tt.want)
		}
		if want := fmt.Sprintf(`data-prop-v="%s"`, tt.raw); !strings.Contains(res.Doc, want) {
			t.Errorf("%s: the document does not still say %s:\n%s", tt.raw, want, res.Doc)
		}
	}

	// The island's own name gets the same treatment.
	res := annotate(t, `<div data-island="Cart &amp; Checkout">t</div>`)
	if got := res.Islands[0].Name; got != "Cart & Checkout" {
		t.Errorf("the name is %q", got)
	}
	if !strings.Contains(res.Doc, `data-island="Cart &amp; Checkout"`) {
		t.Errorf("the document lost the original spelling:\n%s", res.Doc)
	}
}

// TestTheAnnotationDoesNotDependOnTheReadSize, since the stack is built while streaming and a
// document arrives in whatever pieces the network chose.
func TestTheAnnotationDoesNotDependOnTheReadSize(t *testing.T) {
	doc := nest(4, 2) + `<img data-island="Void">` + nest(2, 2)
	whole := annotate(t, doc)

	for _, size := range []int{1, 2, 3, 7, 64, 4096} {
		src := &chunked{s: doc, n: size}
		res, err := Annotate(src, "data-island", "visible")
		if err != nil {
			t.Fatalf("chunk %d: %v", size, err)
		}
		if size < len(doc) && src.reads < 2 {
			t.Errorf("chunk %d: the reader was asked once, so the size did nothing",
				size)
		}
		if res.Doc != whole.Doc {
			t.Errorf("chunk %d: the document differs", size)
		}
		if len(res.Islands) != len(whole.Islands) {
			t.Fatalf("chunk %d: %d islands against %d",
				size, len(res.Islands), len(whole.Islands))
		}
		for i := range res.Islands {
			if fmt.Sprintf("%+v", res.Islands[i]) !=
				fmt.Sprintf("%+v", whole.Islands[i]) {
				t.Errorf("chunk %d: island %d is %+v, want %+v",
					size, i, res.Islands[i], whole.Islands[i])
			}
		}
	}
}

// chunked hands out a string a few bytes at a time, and counts how many reads it took, so the
// test above cannot pass by accident on a reader that ignores the size.
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

// TestAPageThatSaysHowAnIslandHydratesKeepsItsAnswer, including for a nested island, where the
// default would otherwise be "parent".
func TestAPageThatSaysHowAnIslandHydratesKeepsItsAnswer(t *testing.T) {
	res := annotate(t, `<div data-island="A" data-hydrate="idle">`+
		`<div data-island="B" data-hydrate="eager">x</div>`+
		`<div data-island="C">y</div></div>`)
	want := map[string]string{"A": "idle", "B": "eager", "C": "parent"}
	for _, is := range res.Islands {
		if got := is.Hydrate; got != want[is.Name] {
			t.Errorf("%s hydrates %q, want %q", is.Name, got, want[is.Name])
		}
		if n := strings.Count(res.Doc, fmt.Sprintf(`data-island="%s" data-hydrate="%s"`,
			is.Name, is.Hydrate)); n != 1 {
			// Only the ones that named their own strategy keep the attribute in
			// place; the rest have it appended, which is checked below.
			if !strings.Contains(res.Doc, fmt.Sprintf(`data-hydrate="%s"`, is.Hydrate)) {
				t.Errorf("%s: the document does not say how it hydrates:\n%s",
					is.Name, res.Doc)
			}
		}
	}
	// And the attribute is not written twice when the page already had one.
	if n := strings.Count(res.Doc, "data-hydrate="); n != 3 {
		t.Errorf("%d data-hydrate attributes for three islands:\n%s", n, res.Doc)
	}
}

// TestADocumentThatEndsInsideAnIslandSaysSo. An end-tag handler for an element nothing closes
// never runs, so a truncated document leaves the stack deep - and that is the only signal
// available, so the report carries it rather than pretending the nesting is complete.
func TestADocumentThatEndsInsideAnIslandSaysSo(t *testing.T) {
	res := annotate(t, `<div data-island="A"><div data-island="B">text`)
	if res.DepthAtEnd != 2 {
		t.Errorf("the document ended inside %d islands, want 2", res.DepthAtEnd)
	}
	if !strings.Contains(res.String(), "never closed") {
		t.Errorf("the report does not mention it:\n%s", res)
	}

	// A closed document leaves nothing open, which is what makes the signal meaningful.
	if got := annotate(t, `<div data-island="A">x</div>`).DepthAtEnd; got != 0 {
		t.Errorf("a closed island left %d open", got)
	}

	// An omitted end tag still closes the element eventually - the enclosing end tag does
	// it - so nothing is left open.
	for _, doc := range []string{
		`<ul><li data-island="A">x<li data-island="B">y</ul>`,
		`<div><p data-island="A">x<p data-island="B">y</div>`,
	} {
		if got := annotate(t, doc).DepthAtEnd; got != 0 {
			t.Errorf("%s: %d islands left open", doc, got)
		}
	}
}

// TestAnIslandWithNoEndTagOfItsOwnIsMarked. A rewriter does not apply the parser's implied end
// tags, so in <ul><li>A<li>B</ul> the handlers run start A, start B, end B, end A: A is still
// open when B arrives, and both the stack and the descendant selector make B a child of A where
// the HTML tree has them as siblings. Neither answer is available to a streaming rewriter, so
// what this asserts is that the program says the question arose.
func TestAnIslandWithNoEndTagOfItsOwnIsMarked(t *testing.T) {
	for _, tt := range []struct {
		doc            string
		omitted        int
		nested         int
		orderOfHandler []string
	}{
		{`<ul><li data-island="A">x<li data-island="B">y</ul>`, 2, 1,
			[]string{"start:A", "start:B", "end:B", "end:A"}},
		{`<div><p data-island="A">x<p data-island="B">y</div>`, 2, 1,
			[]string{"start:A", "start:B", "end:B", "end:A"}},
		{`<table><tr data-island="A"><td>x<tr data-island="B"><td>y</table>`, 2, 1,
			[]string{"start:A", "start:B", "end:B", "end:A"}},
		{`<ul><li data-island="A">x</li><li data-island="B">y</li></ul>`, 0, 0,
			[]string{"start:A", "end:A", "start:B", "end:B"}},
	} {
		res := annotate(t, tt.doc)
		if got := countOmitted(res.Islands); got != tt.omitted {
			t.Errorf("%s: %d islands closed by another element's end tag, want %d",
				tt.doc, got, tt.omitted)
		}
		if got := res.Nested(); got != tt.nested {
			t.Errorf("%s: %d nested, want %d", tt.doc, got, tt.nested)
		}
		if tt.omitted > 0 && !strings.Contains(res.String(), "may be a following sibling") {
			t.Errorf("%s: the report does not warn:\n%s", tt.doc, res)
		}

		// And the handler order the marking rests on, measured rather than assumed.
		var order []string
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement("[data-island]", func(e *lolhtml.Element) error {
				name, _ := e.Attribute("data-island")
				order = append(order, "start:"+name)
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					order = append(order, "end:"+name)
					return nil
				})
			})); err != nil {
			t.Fatal(err)
		}
		if strings.Join(order, " ") != strings.Join(tt.orderOfHandler, " ") {
			t.Errorf("%s: handlers ran %v, want %v", tt.doc, order, tt.orderOfHandler)
		}
	}

	// The descendant selector agrees with the stack here, which is the point worth being
	// explicit about: the two methods are not independent of this behaviour, they share it.
	doc := `<ul><li data-island="A">x<li data-island="B">y</ul>`
	bySelector, err := NestedBySelector(doc, "data-island")
	if err != nil {
		t.Fatal(err)
	}
	if bySelector != 1 {
		t.Errorf("the descendant selector found %d nested islands, want 1", bySelector)
	}
}

// TestTheMarkerAttributeIsConfigurableAndCannotBeEmpty, since a project that spells it
// data-hydrate-island should not have to patch this.
func TestTheMarkerAttributeIsConfigurableAndCannotBeEmpty(t *testing.T) {
	res, err := Annotate(strings.NewReader(`<div x-island="A"><div x-island="B">y</div></div>`),
		"x-island", "eager")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Islands) != 2 || res.Nested() != 1 {
		t.Errorf("%d islands, %d nested", len(res.Islands), res.Nested())
	}
	if res.Islands[0].Hydrate != "eager" {
		t.Errorf("the default strategy was not used: %q", res.Islands[0].Hydrate)
	}
	if _, err := Annotate(strings.NewReader("<p>x</p>"), "", "visible"); err == nil {
		t.Error("an empty marker attribute was accepted")
	}
	if _, err := Annotate(strings.NewReader("<p>x</p>"), "data-island", ""); err == nil {
		t.Error("an empty default strategy was accepted")
	}
}

// TestADocumentWithNoIslandsIsUnchanged, which is the case a build step hits most often.
func TestADocumentWithNoIslandsIsUnchanged(t *testing.T) {
	doc := `<main><p>text &amp; more</p><img src="/x.png"></main>`
	res := annotate(t, doc)
	if res.Doc != doc {
		t.Errorf("the document changed:\n%s\n%s", doc, res.Doc)
	}
	if len(res.Islands) != 0 {
		t.Errorf("%d islands in a document with none", len(res.Islands))
	}
	if !strings.Contains(res.String(), "0 islands") {
		t.Errorf("the report does not say so:\n%s", res)
	}
}

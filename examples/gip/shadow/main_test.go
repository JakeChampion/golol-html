package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var oneTemplate = Templates{"my-card": `<slot></slot>`}

func insert(t *testing.T, doc string, templates Templates) Result {
	t.Helper()
	res, err := Insert(strings.NewReader(doc), templates, "open")
	if err != nil {
		t.Fatalf("Insert(%q): %v", doc, err)
	}
	return res
}

// hosts builds a document with count hosts nested depth deep, so the properties have structure
// rather than a fixed case.
func hosts(depth, count int) string {
	if depth == 0 {
		return "<p>leaf</p>"
	}
	var b strings.Builder
	for i := range count {
		fmt.Fprintf(&b, `<my-card data-i="%d">%s</my-card>`, i, hosts(depth-1, count))
	}
	return b.String()
}

// TestTwiceIsTheSameAsOnce, which is the property the whole design is for: a page that has
// already been through gains nothing on a second pass. Insert at the start tag instead and this
// fails, because the decision would come before the evidence.
func TestTwiceIsTheSameAsOnce(t *testing.T) {
	for depth := 1; depth <= 4; depth++ {
		for count := 1; count <= 3; count++ {
			doc := hosts(depth, count)
			once := insert(t, doc, oneTemplate)
			twice := insert(t, once.Doc, oneTemplate)

			if twice.Doc != once.Doc {
				t.Errorf("depth %d count %d: the second pass changed the document\n%s\n%s",
					depth, count, once.Doc, twice.Doc)
			}
			if got := twice.Total(func(c *Count) int { return c.Given }); got != 0 {
				t.Errorf("depth %d count %d: the second pass gave %d shadow roots",
					depth, count, got)
			}
			if want := once.Total(func(c *Count) int { return c.Given }); want !=
				twice.Total(func(c *Count) int { return c.Had }) {
				t.Errorf("depth %d count %d: gave %d and then found %d", depth, count,
					want, twice.Total(func(c *Count) int { return c.Had }))
			}
			// And every host really did get exactly one.
			if got, want := strings.Count(once.Doc, "shadowrootmode"),
				strings.Count(doc, "my-card data-i"); got != want {
				t.Errorf("depth %d count %d: %d shadow roots for %d hosts",
					depth, count, got, want)
			}
		}
	}
}

// TestOnlyADirectChildCountsAsAShadowRoot. A declarative shadow root is a child of its host; a
// template deeper inside is an ordinary template and does not stop this from inserting one.
func TestOnlyADirectChildCountsAsAShadowRoot(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		given int
		had   int
	}{
		{"nothing there", `<my-card><p>a</p></my-card>`, 1, 0},
		{"direct child", `<my-card><template shadowrootmode="open"></template></my-card>`, 0, 1},
		{"direct child, closed mode",
			`<my-card><template shadowrootmode="closed"></template></my-card>`, 0, 1},
		{"deeper", `<my-card><div><template shadowrootmode="open"></template></div></my-card>`, 1, 0},
		{"plain template", `<my-card><template><p>t</p></template></my-card>`, 1, 0},
		{"template after content",
			`<my-card><p>a</p><template shadowrootmode="open"></template></my-card>`, 0, 1},
	}
	for _, tt := range cases {
		res := insert(t, tt.doc, oneTemplate)
		if got := res.Total(func(c *Count) int { return c.Given }); got != tt.given {
			t.Errorf("%s: gave %d, want %d\n%s", tt.name, got, tt.given, res.Doc)
		}
		if got := res.Total(func(c *Count) int { return c.Had }); got != tt.had {
			t.Errorf("%s: found %d existing, want %d", tt.name, got, tt.had)
		}
	}

	// The selector that makes the distinction, measured on its own.
	doc := `<my-card><div><template shadowrootmode="open"></template></div></my-card>`
	for _, tt := range []struct {
		sel  string
		want int
	}{
		{"my-card > template[shadowrootmode]", 0},
		{"template[shadowrootmode]", 1},
	} {
		n := 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(tt.sel, func(*lolhtml.Element) error { n++; return nil })); err != nil {
			t.Fatal(err)
		}
		if n != tt.want {
			t.Errorf("%s matched %d, want %d", tt.sel, n, tt.want)
		}
	}
}

// TestAppendAndEndTagBeforeDoNotAgreeWhenTheEndTagIsOmitted, which is the second reason this
// program inserts at the end tag. Both put the content in the wrong place - there is no right
// place in the source - but Append keeps one insertion of three and EndTag.Before keeps all
// three. A rewrite whose job is to add something should be the kind that does not lose it.
func TestAppendAndEndTagBeforeDoNotAgreeWhenTheEndTagIsOmitted(t *testing.T) {
	const doc = `<ul><li>a<li>b<li>c</ul>`

	appended := mark(t, doc, func(e *lolhtml.Element, s string) error {
		return e.Append(s, lolhtml.Text)
	})
	if want := `<ul><li>a<li>b<li>c[1]</ul>`; appended != want {
		t.Errorf("Append gave  %s\nwant         %s", appended, want)
	}

	after := mark(t, doc, func(e *lolhtml.Element, s string) error {
		return e.After(s, lolhtml.Text)
	})
	if want := `<ul><li>a<li>b<li>c</ul>[1]`; after != want {
		t.Errorf("After gave   %s\nwant         %s", after, want)
	}

	before := mark(t, doc, func(e *lolhtml.Element, s string) error {
		return e.OnEndTag(func(end *lolhtml.EndTag) error {
			return end.Before(s, lolhtml.Text)
		})
	})
	if want := `<ul><li>a<li>b<li>c[3][2][1]</ul>`; before != want {
		t.Errorf("EndTag gave  %s\nwant         %s", before, want)
	}

	// With the end tags spelled out, all three are correct and per-item.
	const closed = `<ul><li>a</li><li>b</li><li>c</li></ul>`
	appended = mark(t, closed, func(e *lolhtml.Element, s string) error {
		return e.Append(s, lolhtml.Text)
	})
	before = mark(t, closed, func(e *lolhtml.Element, s string) error {
		return e.OnEndTag(func(end *lolhtml.EndTag) error {
			return end.Before(s, lolhtml.Text)
		})
	})
	if want := `<ul><li>a[1]</li><li>b[2]</li><li>c[3]</li></ul>`; appended != want {
		t.Errorf("Append on closed tags gave %s", appended)
	}
	if appended != before {
		t.Errorf("with end tags spelled out the two differ:\n%s\n%s", appended, before)
	}
}

// mark numbers each matched element and hands the marker to op, which decides where it goes.
func mark(t *testing.T, doc string, op func(*lolhtml.Element, string) error) string {
	t.Helper()
	n := 0
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		n++
		return op(e, fmt.Sprintf("[%d]", n))
	}))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAHostWithNoEndTagIsCountedRatherThanSilentlySkipped. An end-tag handler for an element
// nothing closes never runs, and <my-card/> is that case: HTML ignores the slash on an element
// that is neither void nor foreign, so the host runs to the end of the document.
func TestAHostWithNoEndTagIsCountedRatherThanSilentlySkipped(t *testing.T) {
	for _, tt := range []struct {
		doc      string
		unclosed int
		given    int
	}{
		{`<my-card/>`, 1, 0},
		{`<my-card/><p>after</p>`, 1, 0},
		{`<my-card><p>truncated`, 1, 0},
		{`<my-card></my-card>`, 0, 1},
		{`<my-card/></my-card>`, 0, 1},
	} {
		res := insert(t, tt.doc, oneTemplate)
		if got := res.Total(func(c *Count) int { return c.Unclosed }); got != tt.unclosed {
			t.Errorf("%s: %d unclosed, want %d", tt.doc, got, tt.unclosed)
		}
		if got := res.Total(func(c *Count) int { return c.Given }); got != tt.given {
			t.Errorf("%s: %d given, want %d\n%s", tt.doc, got, tt.given, res.Doc)
		}
		if tt.unclosed > 0 {
			if strings.Contains(res.Doc, "shadowrootmode") {
				t.Errorf("%s: something was inserted anyway:\n%s", tt.doc, res.Doc)
			}
			if !strings.Contains(res.String(), "no end tag") {
				t.Errorf("%s: the report does not say so:\n%s", tt.doc, res)
			}
		}
	}

	// Neither IsSelfClosing nor CanHaveContent is a test for it, which is why the report has
	// to come from the end tag not arriving.
	var selfClosing, canHold bool
	if _, err := lolhtml.RewriteString(`<my-card/>`,
		lolhtml.OnElement("my-card", func(e *lolhtml.Element) error {
			selfClosing, canHold = e.IsSelfClosing(), e.CanHaveContent()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if !selfClosing {
		t.Error("IsSelfClosing() = false for <my-card/>")
	}
	if !canHold {
		t.Error("CanHaveContent() = false for <my-card/>; it is a container either way")
	}
}

// TestTheInsertedTemplateIsNotSeenByThisPass, so the program cannot recurse into what it just
// wrote - which is also why the second pass in TestTwiceIsTheSameAsOnce has to be a second pass.
func TestTheInsertedTemplateIsNotSeenByThisPass(t *testing.T) {
	res := insert(t, `<my-card><p>a</p></my-card>`, oneTemplate)
	if got := res.Total(func(c *Count) int { return c.Given }); got != 1 {
		t.Fatalf("gave %d shadow roots", got)
	}
	if got := res.Total(func(c *Count) int { return c.Had }); got != 0 {
		t.Errorf("the pass saw its own insertion as an existing shadow root %d times", got)
	}
	if n := strings.Count(res.Doc, "shadowrootmode"); n != 1 {
		t.Errorf("%d shadow roots in the output:\n%s", n, res.Doc)
	}
}

// TestSeveralHostTagsAtOnce, each with its own markup, since a page has more than one component.
func TestSeveralHostTagsAtOnce(t *testing.T) {
	templates := Templates{
		"my-card":  `<slot name="card"></slot>`,
		"my-badge": `<slot name="badge"></slot>`,
	}
	res := insert(t, `<my-card><my-badge>b</my-badge></my-card><my-badge>c</my-badge>`, templates)
	if got := res.Total(func(c *Count) int { return c.Given }); got != 3 {
		t.Errorf("gave %d, want 3\n%s", got, res.Doc)
	}
	if got := res.Counts["my-badge"].Given; got != 2 {
		t.Errorf("my-badge got %d", got)
	}
	if got := res.Counts["my-card"].Given; got != 1 {
		t.Errorf("my-card got %d", got)
	}
	// Each host gets its own markup, not the other's.
	if n := strings.Count(res.Doc, `name="badge"`); n != 2 {
		t.Errorf(`%d badge slots:\n%s`, n, res.Doc)
	}
	if n := strings.Count(res.Doc, `name="card"`); n != 1 {
		t.Errorf(`%d card slots:\n%s`, n, res.Doc)
	}
	// And the nested one is inside its parent's host, before the parent's own root.
	badge := strings.Index(res.Doc, `name="badge"`)
	card := strings.Index(res.Doc, `name="card"`)
	if badge > card {
		t.Errorf("the outer host's root came before the inner host's:\n%s", res.Doc)
	}
}

// TestTheResultDoesNotDependOnTheReadSize, since the detection is a stack built while streaming.
func TestTheResultDoesNotDependOnTheReadSize(t *testing.T) {
	doc := hosts(3, 2) + `<my-card><template shadowrootmode="open"></template></my-card>`
	whole := insert(t, doc, oneTemplate)

	for _, size := range []int{1, 2, 5, 13, 512} {
		src := &chunked{s: doc, n: size}
		res, err := Insert(src, oneTemplate, "open")
		if err != nil {
			t.Fatalf("chunk %d: %v", size, err)
		}
		if src.reads < 2 {
			t.Errorf("chunk %d: the reader was asked once, so the size did nothing", size)
		}
		if res.Doc != whole.Doc {
			t.Errorf("chunk %d: the document differs", size)
		}
		if res.String() != whole.String() {
			t.Errorf("chunk %d: the report differs\n%s\n%s", size, res, whole)
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

// TestTheModeIsCheckedAndUsed, since "closed" is a real choice and anything else is a typo that
// would produce a template the parser ignores.
func TestTheModeIsCheckedAndUsed(t *testing.T) {
	for _, mode := range []string{"open", "closed"} {
		res, err := Insert(strings.NewReader(`<my-card>a</my-card>`), oneTemplate, mode)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf(`shadowrootmode="%s"`, mode); !strings.Contains(res.Doc, want) {
			t.Errorf("mode %s: %s", mode, res.Doc)
		}
	}
	for _, mode := range []string{"", "OPEN", "opened", "true"} {
		if _, err := Insert(strings.NewReader("<p>x</p>"), oneTemplate, mode); err == nil {
			t.Errorf("mode %q was accepted", mode)
		}
	}
	if _, err := Insert(strings.NewReader("<p>x</p>"), nil, "open"); err == nil {
		t.Error("no templates was accepted")
	}
}

// TestADocumentWithNoHostsIsUnchanged, which is most of any real page.
func TestADocumentWithNoHostsIsUnchanged(t *testing.T) {
	doc := `<main><p>text &amp; more</p><template><p>t</p></template></main>`
	res := insert(t, doc, oneTemplate)
	if res.Doc != doc {
		t.Errorf("the document changed:\n%s\n%s", doc, res.Doc)
	}
	if len(res.Counts) != 0 {
		t.Errorf("%d host tags counted", len(res.Counts))
	}
}

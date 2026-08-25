package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func take(t *testing.T, doc string, defined ...string) Report {
	t.Helper()
	r, err := Take(strings.NewReader(doc), "doc", defined)
	if err != nil {
		t.Fatalf("Take(%q): %v", doc, err)
	}
	return r
}

// TestTheSpecificationsRuleNotJustAHyphen, in both directions. A name with a hyphen is not
// necessarily a custom element and a name without one is not necessarily a built-in.
func TestTheSpecificationsRuleNotJustAHyphen(t *testing.T) {
	cases := []struct {
		doc  string
		name string
		want Kind
	}{
		{`<my-card></my-card>`, "my-card", Custom},
		{`<MY-CARD></MY-CARD>`, "my-card", Custom},
		{`<a-></a->`, "a-", Custom},
		{`<a--b></a--b>`, "a--b", Custom},
		{`<x-1></x-1>`, "x-1", Custom},

		// The eight the specification reserves. They have hyphens and are not custom
		// element names.
		{`<font-face></font-face>`, "font-face", Reserved},
		{`<font-face-src></font-face-src>`, "font-face-src", Reserved},
		{`<font-face-uri></font-face-uri>`, "font-face-uri", Reserved},
		{`<font-face-format></font-face-format>`, "font-face-format", Reserved},
		{`<font-face-name></font-face-name>`, "font-face-name", Reserved},
		{`<annotation-xml></annotation-xml>`, "annotation-xml", Reserved},
		{`<color-profile></color-profile>`, "color-profile", Reserved},
		{`<missing-glyph></missing-glyph>`, "missing-glyph", Reserved},

		// Meant as a component and can never be one.
		{`<myCard></myCard>`, "mycard", Impossible},
		{`<MyCard></MyCard>`, "mycard", Impossible},
		{`<my_card></my_card>`, "my_card", Impossible},

		// Ordinary, and not worth reporting.
		{`<div></div>`, "div", Builtin},
		{`<fancybox></fancybox>`, "fancybox", Builtin},
		{`<p></p>`, "p", Builtin},
	}
	for _, tt := range cases {
		r := take(t, tt.doc)
		got, ok := r.Kinds[tt.name]
		if !ok {
			t.Errorf("%s: no name %q in %v", tt.doc, tt.name, r.Kinds)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: %q is %v, want %v", tt.doc, tt.name, got, tt.want)
		}
	}
}

// TestTheTokenizerNameIsWhatADefinitionHasToMatch, which is the reason the inventory keeps two
// names per element: <MY-CARD> and <my-card> are one component, and <myCard> is not one at all.
func TestTheTokenizerNameIsWhatADefinitionHasToMatch(t *testing.T) {
	r := take(t, `<my-card></my-card><MY-CARD></MY-CARD><My-Card></My-Card>`)
	if n := len(r.Custom()); n != 1 {
		t.Errorf("%d custom elements for three spellings of one: %v", n, r.Custom())
	}
	u := r.Uses["my-card"]
	if u.Count != 3 {
		t.Errorf("counted %d uses", u.Count)
	}
	if len(u.Spellings) != 3 {
		t.Errorf("kept %d spellings: %v", len(u.Spellings), u.Spellings)
	}

	// And the spelling is what explains an impossible name, so it has to be kept for those
	// too - the lower-case name alone does not show what the author wrote.
	r = take(t, `<myCard></myCard>`)
	if got := why("mycard", r.Uses["mycard"]); !strings.Contains(got, "<myCard>") {
		t.Errorf("why() = %q, and does not quote the source spelling", got)
	}
}

// TestADefinitionIsFoundInScriptTextHoweverItIsChunked. Script text arrives in pieces and a
// define call can straddle two of them, so the text is accumulated to the end of the node before
// it is searched. Feeding the document one byte at a time is the test.
func TestADefinitionIsFoundInScriptTextHoweverItIsChunked(t *testing.T) {
	doc := `<my-card></my-card><my-badge></my-badge>` +
		`<script>customElements.define("my-card", C); customElements.define('my-badge', B)</script>`

	for _, size := range []int{1, 2, 3, 7, 40, 4096} {
		src := &chunked{s: doc, n: size}
		r, err := Take(src, "doc", nil)
		if err != nil {
			t.Fatalf("chunk %d: %v", size, err)
		}
		if size < len(doc) && src.reads < 2 {
			t.Errorf("chunk %d: the reader was asked once, so the size did nothing", size)
		}
		if got := r.Undefined(); len(got) != 0 {
			t.Errorf("chunk %d: %v undefined", size, got)
		}
		for _, name := range []string{"my-card", "my-badge"} {
			if at := r.Uses[name].DefinedAt; at != "doc" {
				t.Errorf("chunk %d: %s defined at %q", size, name, at)
			}
		}
	}

	// A single chunk boundary in the middle of the name itself is the case that would fail
	// without the accumulation, so it is worth its own assertion.
	cut := strings.Index(doc, "my-card\", C")
	if cut < 0 {
		t.Fatal("the test document changed")
	}
	var sb strings.Builder
	r, err := Take(io.MultiReader(
		strings.NewReader(doc[:cut+3]),
		strings.NewReader(doc[cut+3:]),
	), "doc", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = sb
	if len(r.Undefined()) != 0 {
		t.Errorf("a boundary inside the name lost the definition: %v", r.Undefined())
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

// TestOneRewriterPerDocumentNotOnePerFragment, which is the trap this program documents. A
// fragment that ends inside a tag is invisible to every handler and is emitted verbatim, so
// rewriting fragments separately and joining the outputs is not the same as rewriting the whole.
func TestOneRewriterPerDocumentNotOnePerFragment(t *testing.T) {
	const doc = `<p>a</p><script>alert(1)</script><p>b</p>`

	// One rewriter, however the input is chunked: the script is always seen and removed.
	for _, size := range []int{1, 3, 9, 17, 4096} {
		var out strings.Builder
		var seen []string
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			seen = append(seen, e.TagName())
			if e.TagName() == "script" {
				e.Remove()
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, &chunked{s: doc, n: size}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != `<p>a</p><p>b</p>` {
			t.Errorf("chunk %d: %q", size, got)
		}
		if strings.Join(seen, " ") != "p script p" {
			t.Errorf("chunk %d: saw %v", size, seen)
		}
	}

	// Two rewriters over two fragments, outputs joined. Two different failures, and the
	// difference matters: a live script element is not the same as its text.
	var element, textOnly []int
	for i := 1; i < len(doc); i++ {
		a, seenA := sanitise(t, doc[:i])
		b, seenB := sanitise(t, doc[i:])
		joined := a + b
		switch {
		case hasScript(t, joined):
			element = append(element, i)
			// Neither pass saw a script, which is what makes it silent.
			for _, seen := range [][]string{seenA, seenB} {
				for _, name := range seen {
					if name == "script" {
						t.Errorf("cut at %d: a pass saw the script and "+
							"left the element anyway", i)
					}
				}
			}
		case strings.Contains(joined, "alert(1)"):
			textOnly = append(textOnly, i)
		}
	}

	// The cuts that reassemble an element are exactly the ones strictly inside the start
	// tag: after the "<" and before the ">".
	start := strings.Index(doc, "<script>")
	var want []int
	for i := start + 1; i < start+len("<script>"); i++ {
		want = append(want, i)
	}
	if fmt.Sprint(element) != fmt.Sprint(want) {
		t.Errorf("a script element survives at cuts %v, want %v", element, want)
	}

	// And the one cut immediately after the complete start tag leaves the payload as text
	// beside a stray end tag, which is a different failure from the one above.
	if fmt.Sprint(textOnly) != fmt.Sprint([]int{start + len("<script>")}) {
		t.Errorf("the payload survives as text at cuts %v, want [%d]",
			textOnly, start+len("<script>"))
	}
	at := start + len("<script>")
	a, _ := sanitise(t, doc[:at])
	b, _ := sanitise(t, doc[at:])
	if got, want := a+b, `<p>a</p>alert(1)</script><p>b</p>`; got != want {
		t.Errorf("cut at %d gave %q, want %q", at, got, want)
	}
}

// hasScript re-parses a document and reports whether it holds a script element, which is the
// question the joined output raises: text that looks like a script is not one.
func hasScript(t *testing.T, doc string) bool {
	t.Helper()
	found := false
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("script", func(*lolhtml.Element) error {
			found = true
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return found
}

func sanitise(t *testing.T, doc string) (string, []string) {
	t.Helper()
	var seen []string
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		seen = append(seen, e.TagName())
		if e.TagName() == "script" {
			e.Remove()
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return out, seen
}

// TestACustomizedBuiltInCounts, since <div is="my-div"> is a use of a custom element and the name
// is in the attribute. On a custom element the specification says to ignore is=, so this does.
func TestACustomizedBuiltInCounts(t *testing.T) {
	r := take(t, `<div is="my-div"></div><button is="fancy-button"></button>`)
	for _, name := range []string{"my-div", "fancy-button"} {
		u, ok := r.Uses[name]
		if !ok {
			t.Fatalf("%s not found in %v", name, r.Uses)
		}
		if !u.Is {
			t.Errorf("%s is not marked as an is= use", name)
		}
		if r.Kinds[name] != Custom {
			t.Errorf("%s is %v", name, r.Kinds[name])
		}
	}
	if !strings.Contains(r.String(), "(is=)") {
		t.Errorf("the report does not say which came from is=:\n%s", r)
	}

	// is= on a custom element is ignored, so nothing is invented for it.
	r = take(t, `<my-card is="other-card"></my-card>`)
	if _, ok := r.Uses["other-card"]; ok {
		t.Errorf("is= on a custom element was counted: %v", r.Uses)
	}
	// And an empty is= is not a name.
	r = take(t, `<div is=""></div>`)
	if len(r.Custom()) != 0 {
		t.Errorf("an empty is= produced %v", r.Custom())
	}
}

// TestUndefinedIsWhatTheExitStatusIsFor, and -defined is how a bundle's registrations get in.
func TestUndefinedIsWhatTheExitStatusIsFor(t *testing.T) {
	doc := `<my-card></my-card><site-header></site-header>`

	r := take(t, doc)
	if got := r.Undefined(); len(got) != 2 {
		t.Errorf("undefined = %v, want both", got)
	}

	r = take(t, doc, "my-card")
	if got := r.Undefined(); len(got) != 1 || got[0] != "site-header" {
		t.Errorf("undefined = %v, want [site-header]", got)
	}
	if at := r.Uses["my-card"].DefinedAt; at != "-defined" {
		t.Errorf("my-card defined at %q", at)
	}

	// A script in the document wins over nothing, and -defined does not overwrite it.
	r = take(t, doc+`<script>customElements.define("site-header", H)</script>`, "my-card")
	if got := r.Undefined(); len(got) != 0 {
		t.Errorf("undefined = %v, want none", got)
	}
	if at := r.Uses["site-header"].DefinedAt; at != "doc" {
		t.Errorf("site-header defined at %q", at)
	}
}

// TestElementsInsideRawTextAreNotElements, so an inventory does not count the contents of a
// script or a style as components. They are text to the tokenizer, which is the right answer.
func TestElementsInsideRawTextAreNotElements(t *testing.T) {
	for _, doc := range []string{
		`<script><my-in-script></my-in-script></script>`,
		`<style>my-in-style{color:red}</style>`,
		`<textarea><my-in-textarea></my-in-textarea></textarea>`,
		`<title><my-in-title></my-in-title></title>`,
		`<!-- <my-in-comment></my-in-comment> -->`,
	} {
		r := take(t, doc)
		if got := r.Custom(); len(got) != 0 {
			t.Errorf("%s: counted %v", doc, got)
		}
	}

	// A template is the other way round: its content is markup, handlers run inside it, and
	// a component used only there is still used once something clones it.
	r := take(t, `<template><my-card></my-card></template>`)
	if got := r.Custom(); len(got) != 1 || got[0] != "my-card" {
		t.Errorf("inside a template: %v", got)
	}
}

// TestATagTheDocumentNeverFinishesIsInvisible, which is the mechanism behind the fragment trap
// and worth pinning on its own: no element handler, no text handler, and the bytes come out.
func TestATagTheDocumentNeverFinishesIsInvisible(t *testing.T) {
	for _, doc := range []string{
		`<my-card`,
		`<my-card `,
		`<my-card attr`,
		`<my-card attr="v`,
		`<my-card/`,
		`</my-card`,
	} {
		var elements, text []string
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				elements = append(elements, e.TagName())
				return nil
			}),
			lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
				if s := t.Text(); s != "" {
					text = append(text, s)
				}
				return nil
			}))
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		if len(elements) != 0 {
			t.Errorf("%s: element handlers ran for %v", doc, elements)
		}
		if len(text) != 0 {
			t.Errorf("%s: text handlers ran for %v", doc, text)
		}
		if out != doc {
			t.Errorf("%s: output %q", doc, out)
		}
	}

	// The inventory therefore misses it, and that is a limit rather than a bug: there is
	// nothing to report.
	r := take(t, `<my-card>a</my-card><my-badge`)
	if got := r.Custom(); len(got) != 1 || got[0] != "my-card" {
		t.Errorf("%v", got)
	}
}

// TestADocumentWithNoComponentsSaysSo, which is the answer a build step wants most often.
func TestADocumentWithNoComponentsSaysSo(t *testing.T) {
	r := take(t, `<main><p>text</p><div class="x"></div></main>`)
	if len(r.Custom()) != 0 || len(r.Undefined()) != 0 {
		t.Errorf("%v %v", r.Custom(), r.Undefined())
	}
	if !strings.Contains(r.String(), "0 custom elements used") {
		t.Errorf("%s", r)
	}
	if strings.Contains(r.String(), "can never be one") {
		t.Errorf("a clean document got a warning:\n%s", r)
	}
}

// TestTheReportIsStableAcrossRuns, since a map is iterated to build it.
func TestTheReportIsStableAcrossRuns(t *testing.T) {
	doc := `<z-one></z-one><a-two></a-two><m-three></m-three><myCard></myCard><font-face></font-face>`
	first := take(t, doc).String()
	for i := range 20 {
		if got := take(t, doc).String(); got != first {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, got)
		}
	}
	if !strings.Contains(first, fmt.Sprintf("%-20s", "a-two")) {
		t.Errorf("the table is not aligned as expected:\n%s", first)
	}
}

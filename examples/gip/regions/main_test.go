package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// elements returns what a rewriter reports for doc, which is the question a region asks.
func elements(t *testing.T, doc string) string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		out = append(out, e.TagName())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, " ")
}

// TestATokenDoesNotDependOnWhatEnclosesIt: a rewriter is a tokenizer, so the elements a region
// reports are the same alone as inside the document, even where a tree builder would disagree - a
// `td` outside a table is still a `td` token. This is what makes per-region handlers possible at
// all, and it is not the same as the boundary being safe.
func TestATokenDoesNotDependOnWhatEnclosesIt(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		at   int
		tail string
	}{
		{"between divs", `<div>A</div><div>B</div>`, 12, "div"},
		{"a table cell", `<table><tr><td>A</td><td>B</td></tr></table>`, 21, "td"},
		{"a select option", `<select><option>A</option><option>B</option></select>`, 26, "option"},
		{"a list item", `<ul><li>A</li><li>B</li></ul>`, 14, "li"},
		{"after a template", `<template><p>A</p></template><p>B</p>`, 29, "p"},
	} {
		if got := elements(t, tt.doc[tt.at:]); got != tt.tail {
			t.Errorf("%s: the region alone reports %q, want %q", tt.name, got, tt.tail)
		}
		// And the two halves together report what the whole does.
		head := elements(t, tt.doc[:tt.at])
		whole := elements(t, tt.doc)
		if joined := strings.TrimSpace(head + " " + tt.tail); joined != whole {
			t.Errorf("%s: halves report %q, whole reports %q", tt.name, joined, whole)
		}
	}
}

// TestABoundaryIsSafeWhenNothingIsOpenAtIt, which is the rule, and the reason is end tags: an
// element that spans a boundary is split, so its end-tag handler runs in neither half. Reporting
// the same tokens is not the same as being a safe place to cut.
func TestABoundaryIsSafeWhenNothingIsOpenAtIt(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		at   int
		safe bool
	}{
		{"between two top-level divs", `<div>A</div><div>B</div>`, 12, true},
		{"between two paragraphs", `<p>A</p><p>B</p>`, 8, true},
		{"after a template", `<template><p>A</p></template><p>B</p>`, 29, true},

		// The tokens are the same either side and something is open, so an end-tag
		// handler for it runs in neither half.
		{"inside a table row", `<table><tr><td>A</td><td>B</td></tr></table>`, 21, false},
		{"inside a select", `<select><option>A</option><option>B</option></select>`, 26, false},
		{"inside a list", `<ul><li>A</li><li>B</li></ul>`, 14, false},
		{"inside a template", `<template><p>A</p><p>B</p></template>`, 18, false},
		{"inside a div", `<div><p>A</p><p>B</p></div>`, 13, false},
	} {
		if got := SafeBoundary(tt.doc, tt.at); got != tt.safe {
			t.Errorf("%s: SafeBoundary(%d) = %v, want %v", tt.name, tt.at, got, tt.safe)
		}
	}

	// Why: the end tag of an element spanning the boundary reaches no handler.
	const doc = `<div><p>A</p><p>B</p></div>`
	var whole, head, tail []string
	collect := func(into *[]string) lolhtml.Option {
		return lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				*into = append(*into, "div end tag")
				return nil
			})
		})
	}
	for _, c := range []struct {
		doc  string
		into *[]string
	}{{doc, &whole}, {doc[:13], &head}, {doc[13:], &tail}} {
		if _, err := lolhtml.RewriteString(c.doc, collect(c.into)); err != nil {
			t.Fatal(err)
		}
	}
	if len(whole) != 1 {
		t.Errorf("the whole document ran the end-tag handler %d times", len(whole))
	}
	if len(head) != 0 || len(tail) != 0 {
		t.Errorf("the halves ran it %d and %d times, so nothing was lost", len(head), len(tail))
	}
}

// TestABoundaryInsideAStateChangeIsRefused, which is the exception: a start tag puts the tokenizer
// into another state for four elements, and `<!--` does the same, so a region that has not seen
// that token reads the content as markup.
func TestABoundaryInsideAStateChangeIsRefused(t *testing.T) {
	// The documents matter: a boundary inside raw text is only unsafe when the raw text holds
	// something the tail would tokenise differently, which means a complete tag or comment.
	// `<script>var a = "<im` split from `g src=x>"` produces the same bytes either way, and
	// is safe - so a test built on that would assert nothing.
	for _, tt := range []struct {
		name string
		doc  string
		at   int
	}{
		{"inside a script holding a tag", `<script>a<b>c</b>d</script><p>B</p>`, 9},
		{"inside a style holding a tag", `<style>.a{}<b>x</b></style><p>B</p>`, 11},
		{"inside a textarea holding a tag", `<textarea>a<b>c</b></textarea><p>B</p>`, 11},
		{"inside a title holding a tag", `<title>a<b>c</b></title><p>B</p>`, 8},
		{"inside a comment holding a tag", `<!-- a<b>c</b> --><p>B</p>`, 6},
		{"inside a start tag", `<div class="a b"><p>B</p>`, 8},
		{"inside an end tag", `<div>A</div><p>B</p>`, 8},
		{"inside a doctype", `<!DOCTYPE html><p>B</p>`, 8},
	} {
		if SafeBoundary(tt.doc, tt.at) {
			t.Errorf("%s: the boundary at %d was called safe", tt.name, tt.at)
		}
		// And Rewrite refuses it before writing anything.
		var out strings.Builder
		_, err := Rewrite(tt.doc, []Region{
			{Name: "a", Start: 0, End: tt.at, Opts: headRules()},
			{Name: "b", Start: tt.at, Opts: bodyRules()},
		}, &out)
		var boundary *ErrBoundary
		if !errors.As(err, &boundary) {
			t.Errorf("%s: err = %v, want ErrBoundary", tt.name, err)
		}
		if out.Len() != 0 {
			t.Errorf("%s: %d bytes were written before the refusal", tt.name, out.Len())
		}
	}
}

// TestEachRegionGetsItsOwnHandlers, which is the point, and the output is the concatenation.
func TestEachRegionGetsItsOwnHandlers(t *testing.T) {
	const doc = `<a href="/1">one</a><a href="/2">two</a><a href="/3">three</a>`
	var out strings.Builder
	done, err := Rewrite(doc, []Region{
		{Name: "head", Start: 0, End: 20, Opts: headRules()},
		{Name: "body", Start: 20, End: 40, Opts: bodyRules()},
		{Name: "footer", Start: 40, Opts: footerRules()},
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`<a href="/1" data-region="head">`,
		`<a href="/2" rel="noopener">`,
		`<a href="/3" data-region="footer">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not contain %s:\n%s", want, got)
		}
	}
	// Each region's bytes account for the output, and the input for the document.
	var in, outN int
	for _, r := range done {
		in += r.In
		outN += r.Out
	}
	if in != len(doc) {
		t.Errorf("the regions cover %d bytes of %d", in, len(doc))
	}
	if outN != len(got) {
		t.Errorf("the regions wrote %d bytes and the output is %d", outN, len(got))
	}
	// A handler from one region does not act on another's content.
	if strings.Count(got, `data-region="head"`) != 1 {
		t.Errorf("head rules applied more than once:\n%s", got)
	}
	if !strings.Contains(Report(done, len(doc)), "3 regions") {
		t.Errorf("report:\n%s", Report(done, len(doc)))
	}
}

// TestOneRegionIsAnOrdinaryRewrite, since the degenerate case has to behave.
func TestOneRegionIsAnOrdinaryRewrite(t *testing.T) {
	const doc = `<a href="/1">one</a><script>var a = "<img>";</script>`
	var out strings.Builder
	if _, err := Rewrite(doc, []Region{{Name: "all", Start: 0, Opts: bodyRules()}}, &out); err != nil {
		t.Fatal(err)
	}
	want, err := lolhtml.RewriteString(doc, bodyRules()...)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Errorf("one region differs from a plain rewrite:\n  %q\n  %q", out.String(), want)
	}
	// The script's content is untouched, which it would not be if the region had begun
	// inside it.
	if !strings.Contains(out.String(), `var a = "<img>";`) {
		t.Errorf("the script was changed: %s", out.String())
	}
}

// TestTheBoundariesAtTheEdgesAreSafe, since 0 and the length are not really splits.
func TestTheBoundariesAtTheEdgesAreSafe(t *testing.T) {
	const doc = `<script>var a = 1;</script>`
	for _, at := range []int{0, len(doc), len(doc) + 10, -1} {
		if !SafeBoundary(doc, at) {
			t.Errorf("the boundary at %d was called unsafe", at)
		}
	}

	// An empty document has no unsafe boundary either.
	for _, at := range []int{0, 1} {
		if !SafeBoundary("", at) {
			t.Errorf("the boundary at %d in an empty document was called unsafe", at)
		}
	}

	// And no regions at all is an error rather than an empty success.
	var out strings.Builder
	if _, err := Rewrite(doc, nil, &out); err == nil {
		t.Error("no regions was accepted")
	}
}

// TestASafeBoundaryIsSafeForAnyHandlers, which is the property the check has to have: it is used
// to approve a boundary before the regions' own handlers - which differ from each other - are
// applied, so "safe" has to mean safe for whatever they do.
func TestASafeBoundaryIsSafeForAnyHandlers(t *testing.T) {
	const doc = `<p>a</p><script>var x = "<img>";</script><!-- c --><div class="k">b</div><p>d</p>`

	// Several handler sets, each touching a different kind of unit, so a boundary approved
	// by the check is tested against more than the probe it was checked with.
	sets := map[string][]lolhtml.Option{
		"elements": {lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-a", "1")
		})},
		"end tags": {lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.Before("<!--x-->", lolhtml.HTML)
			})
		})},
		"text": {lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				return c.After("<!--t-->", lolhtml.HTML)
			}
			return nil
		})},
		"comments": {lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText(c.Text() + "~")
		})},
		"raw text": {lolhtml.OnText("script", func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				return nil
			}
			return c.Replace(c.Text()+"/*s*/", lolhtml.Text)
		})},
	}

	approved := 0
	for at := 1; at < len(doc); at++ {
		if !SafeBoundary(doc, at) {
			continue
		}
		approved++
		for name, opts := range sets {
			whole, err := lolhtml.RewriteString(doc, opts...)
			if err != nil {
				t.Fatal(err)
			}
			head, err := lolhtml.RewriteString(doc[:at], opts...)
			if err != nil {
				t.Fatal(err)
			}
			tail, err := lolhtml.RewriteString(doc[at:], opts...)
			if err != nil {
				t.Fatal(err)
			}
			if head+tail != whole {
				t.Errorf("the boundary at %d was approved and the %s handlers "+
					"disagree:\n  split: %q\n  whole: %q", at, name, head+tail, whole)
			}
		}
	}
	if approved == 0 {
		t.Fatal("no boundary was approved, so this test asserts nothing")
	}
	t.Logf("%d of %d offsets approved, each checked against %d handler sets",
		approved, len(doc)-1, len(sets))
}

// TestTheCheapTestIsNotEnough, which is the finding: "does the prefix swallow what follows" and
// "is this a token boundary" look like one question and are two. A prefix ending in a bare "<"
// swallows nothing and is still a bad place to split.
func TestTheCheapTestIsNotEnough(t *testing.T) {
	const doc = `<p>a</p><script>var x = "<img>";</script><!-- c --><div class="k">b</div>`

	var falseSafe []int
	for at := 1; at < len(doc); at++ {
		if !Absorbs(doc, at) && !SafeBoundary(doc, at) {
			falseSafe = append(falseSafe, at)
		}
	}
	if len(falseSafe) == 0 {
		t.Fatal("the cheap test agreed with the exact one everywhere, so this document " +
			"no longer shows the difference")
	}
	// Two things the cheap test cannot see, and between them they account for all of it: a
	// prefix ending in a bare "<" or "</", which swallows nothing and still orphans the
	// tail's tag name, and a prefix with an element still open, which the cheap test has no
	// way to know about at all.
	for _, at := range falseSafe {
		p := doc[:at]
		bare := strings.HasSuffix(p, "<") || strings.HasSuffix(p, "</")
		open := elementLeftOpen(t, p)
		if !bare && !open {
			t.Errorf("offset %d passes the cheap test and is unsafe, and is neither a "+
				"bare < nor an open element: %q", at, p[max(0, at-8):])
		}
	}
	t.Logf("%d offsets pass the cheap test and are unsafe: %v", len(falseSafe), falseSafe)

	// And the cheap test is still worth having, because it is right about the cases it is
	// for: a prefix ending inside a script or a comment does swallow.
	for _, tt := range []struct {
		doc string
		at  int
	}{
		{`<script>var a = 1`, 12},
		{`<!-- c`, 4},
		{`<div attr="v`, 8},
	} {
		if !Absorbs(tt.doc, tt.at) {
			t.Errorf("%q at %d: the cheap test says it does not swallow", tt.doc, tt.at)
		}
	}
}

// elementLeftOpen reports whether doc leaves an element open, by counting the end tags that
// arrive against the elements that can have content.
func elementLeftOpen(t *testing.T, doc string) bool {
	t.Helper()
	opened, closed := 0, 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if !e.CanHaveContent() {
			return nil
		}
		opened++
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			closed++
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return closed < opened
}

// TestAnOffsetFlagIsReadStrictly, since a region boundary read from a string that was not a number
// would split somewhere nobody asked for.
func TestAnOffsetFlagIsReadStrictly(t *testing.T) {
	var o offsets
	for _, good := range []string{"0", "12", "4096"} {
		if err := o.Set(good); err != nil {
			t.Errorf("Set(%q): %v", good, err)
		}
	}
	if fmt.Sprint([]int(o)) != "[0 12 4096]" {
		t.Errorf("offsets are %v", o)
	}
	for _, bad := range []string{"", "x", "-1", "1.5", "12x", " 1", "0x10", "1_0"} {
		if err := o.Set(bad); err == nil {
			t.Errorf("Set(%q) was accepted", bad)
		}
	}
}

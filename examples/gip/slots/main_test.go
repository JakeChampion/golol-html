package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func fill(t *testing.T, doc string, frags map[string]string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Fill(&out, strings.NewReader(doc), frags, opts)
	if err != nil {
		t.Fatalf("Fill(%q): %v", doc, err)
	}
	return out.String(), res
}

var frags = map[string]string{
	"title":  "The <em>real</em> title",
	"body":   "<p>body</p>",
	"outer":  `<div><slot name="inner">inner default</slot></div>`,
	"inner":  "<b>inner</b>",
	"loop":   `<x><slot name="loop">stop</slot></x>`,
	"chart":  `<svg viewBox="0 0 10 10" preserveAspectRatio="none"><rect/></svg>`,
	"deep1":  `<d1><slot name="deep2">d1 default</slot></d1>`,
	"deep2":  `<d2><slot name="deep3">d2 default</slot></d2>`,
	"deep3":  `<d3><slot name="deep4">d3 default</slot></d3>`,
	"deep4":  `<d4>bottom</d4>`,
	"quoted": `<a title='say "hi"' href="/x?a=1&amp;b=2">link</a>`,
}

// TestASlotWithAFragmentGetsItAndOneWithoutKeepsItsDefault.
func TestASlotWithAFragmentGetsItAndOneWithoutKeepsItsDefault(t *testing.T) {
	for _, tc := range []struct {
		in, want          string
		filled, defaulted int
	}{
		{`<h1><slot name="title">Untitled</slot></h1>`,
			`<h1><slot name="title">The <em>real</em> title</slot></h1>`, 1, 0},
		{`<h1><slot name="nope">Untitled</slot></h1>`,
			`<h1><slot name="nope">Untitled</slot></h1>`, 0, 1},
		{`<slot name="title"></slot>`,
			`<slot name="title">The <em>real</em> title</slot>`, 1, 0},
		// Two slots with the same name both get it: a fragment is a string, not a
		// reader, so using it twice costs nothing.
		{`<slot name="body">a</slot><slot name="body">b</slot>`,
			`<slot name="body"><p>body</p></slot><slot name="body"><p>body</p></slot>`, 2, 0},
	} {
		got, res := fill(t, tc.in, frags, DefaultOptions)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Filled != tc.filled || res.Defaults != tc.defaulted {
			t.Errorf("%q: filled=%d defaults=%d, want %d and %d",
				tc.in, res.Filled, res.Defaults, tc.filled, tc.defaulted)
		}
	}
}

// TestAnUnnamedSlotIsLeftAlone, since there is nothing it could be asking for.
func TestAnUnnamedSlotIsLeftAlone(t *testing.T) {
	for _, in := range []string{"<slot>default</slot>", `<slot name="">default</slot>`} {
		got, res := fill(t, in, frags, DefaultOptions)
		if got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
		if res.Unnamed != 1 {
			t.Errorf("%q: Unnamed = %d, want 1", in, res.Unnamed)
		}
	}
}

// TestADefinitionFillsTheSlotsBelowIt.
func TestADefinitionFillsTheSlotsBelowIt(t *testing.T) {
	const doc = `<template data-fill="x">a <em>b</em> c</template><p><slot name="x">d</slot></p>`
	got, res := fill(t, doc, nil, DefaultOptions)
	const want = `<p><slot name="x">a <em>b</em> c</slot></p>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Definitions != 1 || res.Filled != 1 || res.Late != 0 {
		t.Errorf("%v: want 1 definition, 1 filled, 0 late", res)
	}
}

// TestADefinitionBelowItsSlotIsTooLate. This is the ordering constraint, and the
// program's answer is to say so rather than to appear to work.
func TestADefinitionBelowItsSlotIsTooLate(t *testing.T) {
	const doc = `<p><slot name="x">default</slot></p><template data-fill="x">too late</template>`
	got, res := fill(t, doc, nil, DefaultOptions)
	const want = `<p><slot name="x">default</slot></p>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Late != 1 {
		t.Errorf("Late = %d, want 1 - a caller cannot tell otherwise", res.Late)
	}
	if res.Defaults != 1 {
		t.Errorf("Defaults = %d, want 1", res.Defaults)
	}
	// A definition below a slot of a different name is not late.
	_, res = fill(t, `<p><slot name="y">d</slot></p><template data-fill="x">fine</template>`, nil, DefaultOptions)
	if res.Late != 0 {
		t.Errorf("Late = %d for an unrelated definition, want 0", res.Late)
	}
	// And reading the document twice is what fixes it: the second pass has the
	// definition before it starts.
	twice, res := fill(t, doc, map[string]string{"x": "too late"}, DefaultOptions)
	if !strings.Contains(twice, "too late") || res.Filled != 1 {
		t.Errorf("supplying the fragment did not fill the slot: %q", twice)
	}
}

// TestSlotsInsideADefinitionAreNotFilledThere: the slots of a definition belong
// to whoever uses it.
func TestSlotsInsideADefinitionAreNotFilledThere(t *testing.T) {
	const doc = `<template data-fill="x">a <slot name="title">t</slot> b</template>` +
		`<p><slot name="x">d</slot></p>`
	got, res := fill(t, doc, frags, DefaultOptions)
	// The definition's slot is filled when the definition is used, not when it is
	// collected, which is why the title text appears exactly once.
	const want = `<p><slot name="x">a <slot name="title">The <em>real</em> title</slot> b</slot></p>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Filled != 2 {
		t.Errorf("Filled = %d, want 2", res.Filled)
	}
}

// TestADefinitionKeepsWhatTheDocumentWrote. Collecting means rebuilding, and a
// rebuild that normalises is a rebuild that changes the page.
func TestADefinitionKeepsWhatTheDocumentWrote(t *testing.T) {
	for _, tc := range []struct{ def, want string }{
		// SVG attribute names keep their capitals: viewbox is not viewBox.
		{`<svg viewBox="0 0 1 1" preserveAspectRatio="none"></svg>`,
			`<svg viewBox="0 0 1 1" preserveAspectRatio="none"></svg>`},
		// Values are source, so a reference stays a reference.
		{`<a href="/x?a=1&amp;b=2">l</a>`, `<a href="/x?a=1&amp;b=2">l</a>`},
		// A single-quoted value holding a double quote is rewritten to survive
		// being written back inside double quotes.
		{`<a title='say "hi"'>l</a>`, `<a title="say &quot;hi&quot;">l</a>`},
		{"a &amp; b", "a &amp; b"},
		{"<!-- a note -->x", "<!-- a note -->x"},
		// Quoting is not recoverable - the value is reported without it - so a
		// rebuild normalises it. A valueless attribute stays valueless, which is
		// the part that would change meaning.
		{"<img src=x alt>", `<img src="x" alt>`},
		{"<p>a<p>b", "<p>a<p>b"},
	} {
		doc := `<template data-fill="d">` + tc.def + `</template><slot name="d">x</slot>`
		got, res := fill(t, doc, nil, DefaultOptions)
		want := `<slot name="d">` + tc.want + `</slot>`
		if got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.def, got, want)
		}
		if res.Filled != 1 {
			t.Errorf("%q: Filled = %d, want 1", tc.def, res.Filled)
		}
	}
}

// TestUnwrappingKeepsTheDocumentClosed. Taking a slot's tags away takes the token
// that closed it, and where the document left the slot open that token belongs to
// an enclosing element.
func TestUnwrappingKeepsTheDocumentClosed(t *testing.T) {
	opts := DefaultOptions
	opts.Unwrap = true
	for _, tc := range []struct{ in, want string }{
		{`<h1><slot name="title">d</slot></h1>`, `<h1>The <em>real</em> title</h1>`},
		// The slot is left open by the document, so </h1> is what closed it.
		{`<h1><slot name="title">d</h1><p>after</p>`,
			`<h1>The <em>real</em> title</h1><p>after</p>`},
		{`<div><slot name="body">d</div>tail`, `<div><p>body</p></div>tail`},
	} {
		got, _ := fill(t, tc.in, frags, opts)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}

	// Without the guard the closing tag would be gone. Measured here so the guard
	// is not decoration: this is the same operation with the guard left out.
	unguarded, err := lolhtml.RewriteString(`<div><slot name="body">d</div>tail`,
		lolhtml.OnElement("slot", func(e *lolhtml.Element) error {
			if err := e.SetInnerContent("<p>body</p>", lolhtml.HTML); err != nil {
				return err
			}
			e.RemoveAndKeepContent()
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unguarded, "</div>") {
		t.Errorf("without the guard the </div> survived (%q), so the guard is no "+
			"longer needed", unguarded)
	}
}

// TestADefinitionThatCannotBeRebuiltIsDropped. A script inside a definition holds
// content that is not markup, and rebuilding it as markup would turn its text into
// elements.
func TestADefinitionThatCannotBeRebuiltIsDropped(t *testing.T) {
	const doc = `<template data-fill="x">a<script>if (a<b) c()</script></template>` +
		`<slot name="x">default</slot>`
	got, res := fill(t, doc, nil, DefaultOptions)
	if !strings.Contains(got, "default") {
		t.Errorf("got %q, want the slot's default", got)
	}
	if res.TooBig != 1 || res.Definitions != 0 {
		t.Errorf("%v: want the definition dropped", res)
	}
}

// TestADefinitionLargerThanTheLimitIsDropped, and the page does not get half of
// it.
func TestADefinitionLargerThanTheLimitIsDropped(t *testing.T) {
	opts := Options{MaxDepth: 3, MaxDefinition: 64}
	long := strings.Repeat("<b>x</b>", 40)
	doc := `<template data-fill="x">` + long + `</template><slot name="x">default</slot>`
	got, res := fill(t, doc, nil, opts)
	if res.TooBig != 1 {
		t.Errorf("TooBig = %d, want 1", res.TooBig)
	}
	if !strings.Contains(got, "default") {
		t.Errorf("got %q, want the default kept", got)
	}
	if strings.Count(got, "<b>") > 40 {
		t.Errorf("the abandoned definition was emitted twice: %q", got)
	}
}

// TestSlotsInAFragmentAreFilledBeforeItGoesIn, because inserted content is not
// re-parsed: a slot in a fragment would otherwise arrive on the page as a slot.
func TestSlotsInAFragmentAreFilledBeforeItGoesIn(t *testing.T) {
	got, res := fill(t, `<slot name="outer">d</slot>`, frags, DefaultOptions)
	const want = `<slot name="outer"><div><slot name="inner"><b>inner</b></slot></div></slot>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Filled != 2 {
		t.Errorf("Filled = %d, want 2", res.Filled)
	}
}

// TestTheDepthLimitAndTheCycleCheckLeaveTheDefault. Neither makes a slot
// disappear: an unfilled slot keeps what the template wrote.
func TestTheDepthLimitAndTheCycleCheckLeaveTheDefault(t *testing.T) {
	got, res := fill(t, `<slot name="loop">d</slot>`, frags, DefaultOptions)
	if want := `<slot name="loop"><x><slot name="loop">stop</slot></x></slot>`; got != want {
		t.Errorf("cycle\n got %q\nwant %q", got, want)
	}
	if res.Cycles != 1 {
		t.Errorf("Cycles = %d, want 1", res.Cycles)
	}

	for _, tc := range []struct {
		depth   int
		want    string
		tooDeep int
	}{
		{0, `<slot name="deep1"><d1><slot name="deep2">d1 default</slot></d1></slot>`, 1},
		{1, `<slot name="deep1"><d1><slot name="deep2"><d2><slot name="deep3">d2 default</slot></d2></slot></d1></slot>`, 1},
	} {
		got, res := fill(t, `<slot name="deep1">d</slot>`, frags,
			Options{MaxDepth: tc.depth, MaxDefinition: 1 << 20})
		if got != tc.want {
			t.Errorf("depth %d\n got %q\nwant %q", tc.depth, got, tc.want)
		}
		if res.TooDeep != tc.tooDeep {
			t.Errorf("depth %d: TooDeep = %d, want %d", tc.depth, res.TooDeep, tc.tooDeep)
		}
	}
}

// TestAnSVGFragmentSurvives, which is the finding this turn turned up: an
// attribute name is part of the attribute in SVG.
func TestAnSVGFragmentSurvives(t *testing.T) {
	got, _ := fill(t, `<figure><slot name="chart">no chart</slot></figure>`, frags, DefaultOptions)
	if !strings.Contains(got, `viewBox="0 0 10 10"`) || !strings.Contains(got, `preserveAspectRatio="none"`) {
		t.Errorf("got %q, want the SVG attributes with their capitals", got)
	}
	// A supplied fragment is inserted as markup and is not rebuilt, so this is
	// about the definition path too.
	got, _ = fill(t, `<template data-fill="c"><svg viewBox="0 0 4 4"></svg></template>`+
		`<slot name="c">none</slot>`, nil, DefaultOptions)
	if !strings.Contains(got, `viewBox="0 0 4 4"`) {
		t.Errorf("a collected definition lost the case: %q", got)
	}
}

// TestChunkInvariance.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><template data-fill="x">a <em>b</em><!--c--></template>` +
		`<h1><slot name="title">t</slot></h1><main><slot name="x">d</slot>` +
		`<slot name="outer">o</slot><slot>anon</slot></main>` +
		`<template data-fill="late">l</template><p><slot name="late">p</slot></p></html>`
	want, _ := fill(t, doc, frags, DefaultOptions)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		f := newFiller(frags, DefaultOptions)
		w, err := lolhtml.NewWriter(&out, f.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// TestFillingTwiceIsTheSameAsFillingOnce with the tags kept: the slot is still
// there, and its content is what the fragment says either way. With -unwrap there
// is no slot left to fill, which is the same output for a different reason.
func TestFillingTwiceIsTheSameAsFillingOnce(t *testing.T) {
	for _, unwrap := range []bool{false, true} {
		opts := DefaultOptions
		opts.Unwrap = unwrap
		const doc = `<h1><slot name="title">t</slot></h1><main><slot name="outer">o</slot></main>`
		once, _ := fill(t, doc, frags, opts)
		twice, _ := fill(t, once, frags, opts)
		if twice != once {
			t.Errorf("unwrap=%v\n once %q\ntwice %q", unwrap, once, twice)
		}
	}
}

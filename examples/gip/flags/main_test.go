package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var flags = map[string]bool{"on": true, "also-on": true, "off": false, "also-off": false}

func run(t *testing.T, doc string, opts Options) (string, Result, error) {
	t.Helper()
	var out strings.Builder
	res, err := Gate(&out, strings.NewReader(doc), flags, opts)
	return out.String(), res, err
}

// TestTheExpressionsEvaluate, including the precedence, which is the part a
// reader has to be able to trust without looking at the parser.
func TestTheExpressionsEvaluate(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{"on", true},
		{"off", false},
		{"!on", false},
		{"!off", true},
		{"!!on", true},
		{"on && also-on", true},
		{"on && off", false},
		{"off || on", true},
		{"off || also-off", false},
		{"on && !off", true},
		// && binds tighter than ||, so this is off || (on && on) rather than
		// (off || on) && on - both true here, so the discriminating case follows.
		{"on || off && off", true},
		{"off && off || on", true},
		{"(on || off) && off", false},
		{"(off || on) && on", true},
		{"!(on && off)", true},
		{"!(off || off)", true},
		{"((on))", true},
		// A name nobody has heard of is off rather than an error.
		{"ghost", false},
		{"ghost || on", true},
		{"!ghost", true},
	} {
		g := &gate{flags: flags}
		got, err := g.eval(tc.expr)
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// TestAMalformedExpressionIsOffAndCounted, and an error under -strict. Failing
// closed is the only safe direction: the alternative shows an unreleased feature.
func TestAMalformedExpressionIsOffAndCounted(t *testing.T) {
	for _, expr := range []string{
		"", "  ", "&&", "on &&", "|| on", "!", "(", ")", "(on", "on)", "on off",
		"on & off", "on | off", "!()", "on && && off",
	} {
		doc := `<div data-flag="` + expr + `">gated</div><p>always</p>`
		got, res, err := run(t, doc, Options{})
		if err != nil {
			t.Errorf("%q: %v", expr, err)
			continue
		}
		if strings.Contains(got, "gated") {
			t.Errorf("%q: the block was kept: %q", expr, got)
		}
		if res.Malformed != 1 {
			t.Errorf("%q: Malformed = %d, want 1", expr, res.Malformed)
		}
		if !strings.Contains(got, "always") {
			t.Errorf("%q: the rest of the page went too: %q", expr, got)
		}

		if _, _, err := run(t, doc, Options{Strict: true}); err == nil {
			t.Errorf("%q: strict mode accepted it", expr)
		}
	}
}

// TestAnUnknownFlagIsOffAndCounted. Distinct from malformed: the expression is
// fine and the configuration does not have the name.
func TestAnUnknownFlagIsOffAndCounted(t *testing.T) {
	got, res, err := run(t, `<div data-flag="ghost">gated</div><p>always</p>`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "gated") {
		t.Errorf("got %q", got)
	}
	if res.Unknown != 1 || res.Malformed != 0 || res.Dropped != 1 {
		t.Errorf("%v: want 1 unknown, 0 malformed, 1 dropped", res)
	}
	// Even under -strict, an unknown flag is a page saying something about the
	// world rather than a page that is wrong.
	if _, _, err := run(t, `<div data-flag="ghost">g</div>`, Options{Strict: true}); err != nil {
		t.Errorf("strict mode refused an unknown flag: %v", err)
	}
}

// TestTheGatedBlocksGoAndTheRestStays.
func TestTheGatedBlocksGoAndTheRestStays(t *testing.T) {
	const doc = `<p>before</p><div data-flag="on">keep</div>` +
		`<div data-flag="off">drop</div><p>after</p>`
	got, res, err := run(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	const want = `<p>before</p><div data-flag="on">keep</div><p>after</p>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Gates != 2 || res.Kept != 1 || res.Dropped != 1 {
		t.Errorf("%v: want 2 gates, 1 kept, 1 dropped", res)
	}
}

// TestANestedGateInsideADroppedBlockIsNotCountedTwice. The inner handler still
// runs - removal suppresses output and not handler calls - and Element.IsRemoved
// is how it tells "I dropped this" from "this was already gone".
func TestANestedGateInsideADroppedBlockIsNotCountedTwice(t *testing.T) {
	const doc = `<div data-flag="off">outer` +
		`<div data-flag="on">inner on</div>` +
		`<div data-flag="off">inner off</div>` +
		`</div><p>after</p>`
	got, res, err := run(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got != `<p>after</p>` {
		t.Errorf("got %q", got)
	}
	if res.Gates != 3 {
		t.Errorf("Gates = %d, want 3", res.Gates)
	}
	if res.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1 - the outer block only", res.Dropped)
	}
	if res.AlreadyGone != 2 {
		t.Errorf("AlreadyGone = %d, want 2", res.AlreadyGone)
	}
	if res.Kept != 0 {
		t.Errorf("Kept = %d, want 0 - the inner block was kept by nothing", res.Kept)
	}
}

// TestTheTextAccountingMatchesTheOutput is the property that makes the numbers
// worth printing: the bytes reported kept are the bytes of text in the output.
func TestTheTextAccountingMatchesTheOutput(t *testing.T) {
	for _, doc := range []string{
		`<p>before</p><div data-flag="off">drop this</div><p>after</p>`,
		`<div data-flag="off">a<span>b</span>c</div><p>d</p>`,
		`<div data-flag="off">outer<div data-flag="off">inner</div>end</div><p>x</p>`,
		`<div data-flag="on">kept<div data-flag="off">dropped</div>more</div>`,
		`<ul><li data-flag="off">one<li data-flag="on">two</ul>`,
		`<p>no gates here</p>`,
		`<div data-flag="off">only</div>`,
		``,
	} {
		got, res, err := run(t, doc, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(textOf(t, got)); n != res.TextKept {
			t.Errorf("%q: reported %d bytes kept and the output has %d (%q)",
				doc, res.TextKept, n, got)
		}
		if total := len(textOf(t, doc)); total != res.TextKept+res.TextDropped {
			t.Errorf("%q: %d kept + %d dropped != %d in the input",
				doc, res.TextKept, res.TextDropped, total)
		}
	}
}

// TestWithoutTheGuardTheAccountingIsWrong. A text handler is handed the text of a
// removed element with nothing on the chunk to say so, which is why the element
// handler keeps the counter. Measured here so the counter is not decoration.
func TestWithoutTheGuardTheAccountingIsWrong(t *testing.T) {
	const doc = `<div data-flag="off">dropped</div><p>kept</p>`
	naive := 0
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("[data-flag]", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			// The obvious guard, which does not work: the chunk was not removed,
			// the element around it was.
			if c.IsRemoved() {
				return nil
			}
			naive += len(c.Text())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if naive != len("dropped")+len("kept") {
		t.Errorf("the naive count is %d; if it is now %d the library changed and "+
			"the counter can go", naive, len("kept"))
	}

	_, res, err := run(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TextKept != len("kept") {
		t.Errorf("TextKept = %d, want %d", res.TextKept, len("kept"))
	}
}

// TestARemovalThatReachesTooFarIsCounted, the hazard this shares with
// examples/gip/abtest: the removal is decided at the start tag and the end tag
// arrives later, so all that can be done is to notice.
func TestARemovalThatReachesTooFarIsCounted(t *testing.T) {
	// The first item has no end tag of its own, so </ul> closed it and the removal
	// ran to there - taking the second item with it.
	const doc = `<ul><li data-flag="off">gone<li data-flag="on">also gone</ul><p>after</p>`
	got, res, err := run(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Overreach != 1 {
		t.Errorf("Overreach = %d, want 1", res.Overreach)
	}
	if strings.Contains(got, "also gone") {
		t.Errorf("got %q - the second item survived, so this no longer measures the "+
			"hazard", got)
	}
	if !strings.Contains(res.String(), "WARNING") {
		t.Errorf("the report does not mention it: %s", res)
	}
	// Closed properly, the same document loses only the gated item.
	closed := `<ul><li data-flag="off">gone</li><li data-flag="on">stays</li></ul>`
	got, res, err = run(t, closed, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Overreach != 0 || !strings.Contains(got, "stays") || strings.Contains(got, "gone") {
		t.Errorf("%v: got %q", res, got)
	}
}

// TestGatingTwiceChangesNothing: the dropped blocks are gone and the kept ones
// still carry their attribute, so a second pass keeps them again.
func TestGatingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<div data-flag="on">a</div><div data-flag="off">b</div>`,
		`<div data-flag="on && !off">a</div>`,
		`<div data-flag="ghost">a</div><p>b</p>`,
		`<div data-flag="(">a</div><p>b</p>`,
	} {
		once, _, err := run(t, doc, Options{})
		if err != nil {
			t.Fatal(err)
		}
		twice, _, err := run(t, once, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
	}
}

// TestChunkInvariance.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><p>before</p>` +
		`<div data-flag="on">kept<div data-flag="off">nested</div>more</div>` +
		`<div data-flag="off || also-off">gone</div>` +
		`<div data-flag="ghost">unknown</div><p>after</p></body></html>`
	want, _, err := run(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		g := &gate{flags: flags}
		w, err := lolhtml.NewWriter(&out, g.options()...)
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

// textOf is the visible text of a document, which is what the accounting is
// counting.
func textOf(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

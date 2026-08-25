package main

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var hero = Experiment{Name: "hero", Variants: []Variant{{"a", 5000}, {"b", 5000}}}
var price = Experiment{Name: "price", Variants: []Variant{{"control", 9000}, {"test", 1000}}}

func rewrite(t *testing.T, doc, key string, exps []Experiment, opts Options) (string, Result, error) {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(&out, strings.NewReader(doc), key, exps, opts)
	return out.String(), res, err
}

// TestTheSameKeyAlwaysGetsTheSameVariant. A visitor who sees b and then a has
// been shown an experiment rather than been in one, so this is the property the
// bucketing exists for.
func TestTheSameKeyAlwaysGetsTheSameVariant(t *testing.T) {
	for _, key := range []string{"", "a", "visitor-42", "🎉", strings.Repeat("x", 1000)} {
		want := hero.Choose(key)
		for range 100 {
			if got := hero.Choose(key); got != want {
				t.Fatalf("Choose(%q) returned %q then %q", key, want, got)
			}
		}
	}
}

// TestTwoExperimentsDoNotBucketTogether. Without the experiment name in the hash,
// two 50/50 experiments are one experiment: every visitor in a of the first is in
// a of the second. Measured as a correlation over 10000 keys.
func TestTwoExperimentsDoNotBucketTogether(t *testing.T) {
	other := Experiment{Name: "other", Variants: []Variant{{"a", 5000}, {"b", 5000}}}
	same := 0
	const n = 10000
	for i := range n {
		key := fmt.Sprintf("visitor-%d", i)
		if hero.Choose(key) == other.Choose(key) {
			same++
		}
	}
	// Two independent coins agree half the time. The measured figure for these
	// 10000 keys is recorded rather than a target: what is asserted is that it is
	// near a half rather than near one.
	if same < 4700 || same > 5300 {
		t.Errorf("the two experiments agreed on %d of %d keys, want about half - "+
			"if this is near %d they are the same experiment", same, n, n)
	}
}

// TestTheWeightsAreRespected. The distribution is deterministic, so the numbers
// here are measurements rather than targets; the assertion is that each arm is
// within a point of its weight, which is what the hash has to buy.
func TestTheWeightsAreRespected(t *testing.T) {
	const n = 10000
	for _, e := range []Experiment{hero, price} {
		counts := map[string]int{}
		for i := range n {
			counts[e.Choose(fmt.Sprintf("visitor-%d", i))]++
		}
		for _, v := range e.Variants {
			want := v.Weight * n / Buckets
			if got := counts[v.Name]; got < want-100 || got > want+100 {
				t.Errorf("%s/%s got %d of %d keys, want about %d",
					e.Name, v.Name, got, n, want)
			}
		}
		if len(counts) != len(e.Variants) {
			t.Errorf("%s produced %d distinct variants, want %d", e.Name, len(counts), len(e.Variants))
		}
	}
}

// TestTheLastVariantTakesTheRemainder, so weights that do not add up cannot round
// an arm to nothing or leave a key with no variant at all.
func TestTheLastVariantTakesTheRemainder(t *testing.T) {
	short := Experiment{Name: "short", Variants: []Variant{{"a", 10}, {"b", 10}}}
	counts := map[string]int{}
	for i := range 1000 {
		counts[short.Choose(fmt.Sprintf("k%d", i))]++
	}
	if counts["a"] == 0 || counts["b"] == 0 {
		t.Errorf("counts = %v, want both arms used", counts)
	}
	if counts[""] != 0 {
		t.Errorf("%d keys got no variant", counts[""])
	}
	// An experiment with no arms has nothing to choose.
	if got := (Experiment{Name: "empty"}).Choose("k"); got != "" {
		t.Errorf("an empty experiment chose %q", got)
	}
}

// TestTheChosenVariantStaysAndTheOthersGo.
func TestTheChosenVariantStaysAndTheOthersGo(t *testing.T) {
	const doc = `<html><body>` +
		`<div data-experiment="hero" data-variant="a">A</div>` +
		`<div data-experiment="hero" data-variant="b">B</div>` +
		`<p>always</p></body></html>`
	for _, key := range []string{"visitor-1", "visitor-2", "visitor-3", "visitor-4"} {
		got, res, err := rewrite(t, doc, key, []Experiment{hero}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		chosen := res.Chosen["hero"]
		other := map[string]string{"a": "B", "b": "A"}[chosen]
		if !strings.Contains(got, `data-variant="`+chosen+`"`) {
			t.Errorf("%s: chose %q and the output does not have it: %q", key, chosen, got)
		}
		if strings.Contains(got, ">"+other+"<") {
			t.Errorf("%s: chose %q and the losing content is still there: %q", key, chosen, got)
		}
		if !strings.Contains(got, "<p>always</p>") {
			t.Errorf("%s: content outside the experiment went missing: %q", key, got)
		}
		if res.Kept != 1 || res.Removed != 1 {
			t.Errorf("%s: kept=%d removed=%d, want 1 and 1", key, res.Kept, res.Removed)
		}
	}
}

// TestTheDocumentIsMarkedWhereverItCanBe. Four answers in order, because "every
// document has an <html> tag" is true of documents from a browser and not of
// documents from a template.
func TestTheDocumentIsMarkedWhereverItCanBe(t *testing.T) {
	for _, tc := range []struct {
		doc, where, want string
	}{
		{`<html><body><p>x</p></body></html>`, "<html>", `<html data-ab="hero=`},
		{`<body><p>x</p></body>`, "<body>", `<body data-ab="hero=`},
		{`<head><title>t</title></head><body>x</body>`, "a <meta> in <head>",
			`<head><meta name="ab-bucket" content="hero=`},
		{`<p>x</p>`, "an inserted <meta>", `<meta name="ab-bucket" content="hero=`},
		{`<!DOCTYPE html><p>x</p>`, "an inserted <meta>", `<meta name="ab-bucket" content="hero=`},
		{`just text`, "the document end", `<meta name="ab-bucket" content="hero=`},
		{``, "the document end", `<meta name="ab-bucket" content="hero=`},
	} {
		got, res, err := rewrite(t, tc.doc, "visitor-1", []Experiment{hero}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Marked != tc.where {
			t.Errorf("%q: marked on %s, want %s", tc.doc, res.Marked, tc.where)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q\n got %q\nwant it to contain %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheDocumentIsMarkedOnce, however many elements it has.
func TestTheDocumentIsMarkedOnce(t *testing.T) {
	const doc = `<html><body><div><p>x</p></div></body></html>`
	got, _, err := rewrite(t, doc, "visitor-1", []Experiment{hero, price}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "data-ab="); n != 1 {
		t.Errorf("marked %d times: %q", n, got)
	}
	if n := strings.Count(got, "ab-bucket"); n != 0 {
		t.Errorf("both the attribute and the meta were written: %q", got)
	}
	// Every experiment is in the mark, sorted, so identical buckets give identical
	// bytes.
	if !strings.Contains(got, `data-ab="hero=`) || !strings.Contains(got, ` price=`) {
		t.Errorf("the mark does not name both experiments: %q", got)
	}
}

// TestARemovalThatReachesTooFarIsReported. This is B122 in an application: the
// removal is decided at the start tag, the end tag arrives later, and by then the
// content has gone. It cannot be prevented, so it is counted - and -strict makes
// it an error, because a page that has silently lost half its content is worse
// than a failed request.
func TestARemovalThatReachesTooFarIsReported(t *testing.T) {
	// The first item has no end tag of its own, so </ul> is what closes it and the
	// removal runs to there - taking the second item, which was the other variant.
	const doc = `<ul><li data-experiment="hero" data-variant="LOSER">lose` +
		`<li data-experiment="hero" data-variant="WINNER">keep</ul>`
	exp := Experiment{Name: "hero", Variants: []Variant{{"LOSER", 0}, {"WINNER", Buckets}}}

	got, res, err := rewrite(t, doc, "any", []Experiment{exp}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Chosen["hero"] != "WINNER" {
		t.Fatalf("chose %q, want WINNER - the weights say so", res.Chosen["hero"])
	}
	if res.Overreach != 1 {
		t.Errorf("Overreach = %d, want 1", res.Overreach)
	}
	if strings.Contains(got, "keep") {
		t.Errorf("the winning content survived (%q), so this test no longer "+
			"measures the hazard", got)
	}
	if !strings.Contains(res.String(), "WARNING") {
		t.Errorf("the report does not mention it: %s", res)
	}

	// Strict mode turns it into an error.
	if _, _, err := rewrite(t, doc, "any", []Experiment{exp}, Options{Strict: true}); err == nil {
		t.Error("strict mode accepted the overreaching removal")
	}

	// A well-formed document has none of this: the same variants, closed.
	const closed = `<ul><li data-experiment="hero" data-variant="LOSER">lose</li>` +
		`<li data-experiment="hero" data-variant="WINNER">keep</li></ul>`
	got, res, err = rewrite(t, closed, "any", []Experiment{exp}, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Overreach != 0 {
		t.Errorf("Overreach = %d on a well-formed document", res.Overreach)
	}
	if !strings.Contains(got, "keep") || strings.Contains(got, "lose") {
		t.Errorf("got %q, want the winner only", got)
	}
}

// TestAnUnknownExperimentOrArmIsLeftAlone. A page and a configuration that
// disagree should be visible; markup that vanishes is not.
func TestAnUnknownExperimentOrArmIsLeftAlone(t *testing.T) {
	for _, doc := range []string{
		`<div data-experiment="gone" data-variant="a">x</div>`,
		`<div data-experiment="hero" data-variant="c">x</div>`,
		`<div data-experiment="hero">x</div>`,
		`<div data-variant="a">x</div>`,
	} {
		got, res, err := rewrite(t, doc, "visitor-1", []Experiment{hero}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, ">x<") {
			t.Errorf("%q: the content was removed: %q", doc, got)
		}
		if res.Removed != 0 {
			t.Errorf("%q: Removed = %d", doc, res.Removed)
		}
	}
	// The first two are counted as a mismatch; the last two are not markers at all.
	_, res, _ := rewrite(t, `<div data-experiment="gone" data-variant="a">x</div>`,
		"visitor-1", []Experiment{hero}, Options{})
	if res.Unknown != 1 {
		t.Errorf("Unknown = %d, want 1", res.Unknown)
	}
	_, res, _ = rewrite(t, `<div data-variant="a">x</div>`, "visitor-1", []Experiment{hero}, Options{})
	if res.Unknown != 0 {
		t.Errorf("Unknown = %d for an element with no experiment, want 0", res.Unknown)
	}
}

// TestRewritingTwiceChangesNothing. The losers are gone, and the mark is an
// attribute set to the same value, so a second pass is a no-op - which is what
// makes this safe to run in front of a cache that may have already run it.
func TestRewritingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<html><body><div data-experiment="hero" data-variant="a">A</div>` +
			`<div data-experiment="hero" data-variant="b">B</div></body></html>`,
		`<p>x</p>`,
		`just text`,
	} {
		once, _, err := rewrite(t, doc, "visitor-7", []Experiment{hero, price}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		twice, _, err := rewrite(t, once, "visitor-7", []Experiment{hero, price}, Options{})
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
	const doc = `<html><head><title>t</title></head><body>` +
		`<div data-experiment="hero" data-variant="a">A</div>` +
		`<div data-experiment="hero" data-variant="b">B</div>` +
		`<div data-experiment="price" data-variant="test">T</div>` +
		`<p>always</p></body></html>`
	want, _, err := rewrite(t, doc, "visitor-3", []Experiment{hero, price}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		a := &abtest{res: Result{Chosen: map[string]string{}, Marked: "nothing"}, arms: map[string]map[string]bool{}}
		for _, e := range []Experiment{hero, price} {
			a.res.Chosen[e.Name] = e.Choose("visitor-3")
			a.arms[e.Name] = map[string]bool{}
			for _, v := range e.Variants {
				a.arms[e.Name][v.Name] = true
			}
		}
		w, err := lolhtml.NewWriter(&out, a.options()...)
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

// TestTheMarkIsEscaped, because an experiment name is configuration and
// configuration is a string somebody typed.
func TestTheMarkIsEscaped(t *testing.T) {
	nasty := Experiment{Name: `x" onload="alert(1)`, Variants: []Variant{{"a", Buckets}}}
	for _, doc := range []string{`<html><body>x</body></html>`, `<p>x</p>`} {
		got, _, err := rewrite(t, doc, "k", []Experiment{nasty}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		// Read back: the marked element has exactly the attributes it should, and
		// the value is the mark rather than a mark plus an event handler.
		var attrs []lolhtml.Attribute
		if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("html,meta", func(e *lolhtml.Element) error {
			attrs = e.AttributeList()
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		want := 1 // <html data-ab="...">
		if strings.Contains(got, "ab-bucket") {
			want = 2 // <meta name content>
		}
		if len(attrs) != want {
			t.Errorf("%q -> %q: the marked element has %v, want %d attributes",
				doc, got, attrs, want)
		}
		for _, a := range attrs {
			if strings.Contains(a.Name, "onload") {
				t.Errorf("%q -> %q broke out into an attribute", doc, got)
			}
		}
	}
}

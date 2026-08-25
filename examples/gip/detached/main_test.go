package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestEveryMutatorReportsTheDetachment, over the whole surface: that is the half a
// caller can rely on.
func TestEveryMutatorReportsTheDetachment(t *testing.T) {
	r, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Calls {
		if c.Kind != Mutator {
			continue
		}
		if !c.Reports() {
			t.Errorf("%s.%s answered %q, want ErrDetached", c.Unit, c.Method, c)
		}
	}
	// And there are enough of them for that to mean something.
	var mutators int
	for _, c := range r.Calls {
		if c.Kind == Mutator {
			mutators++
		}
	}
	if mutators < 20 {
		t.Errorf("only %d mutators were asked; the table is meant to be the whole surface", mutators)
	}
}

// TestGettersAnswerSilently, which is the half that surprises: a retained unit describes
// an empty document rather than reporting a problem.
func TestGettersAnswerSilently(t *testing.T) {
	r, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	silent := r.Silent()
	if len(silent) < 10 {
		t.Errorf("%d getters answered silently; the table is meant to show a lot of them",
			len(silent))
	}
	// The two exceptions report instead, and both have room for the answer.
	loud := map[string]bool{}
	for _, c := range r.Calls {
		if c.Kind == Getter && c.Reports() {
			loud[c.Unit+"."+c.Method] = true
		}
	}
	for _, want := range []string{"Element.HasAttribute", "Sink.Err"} {
		if !loud[want] {
			t.Errorf("%s did not report the detachment, and it is one of the two that can", want)
		}
	}
	if len(loud) != 2 {
		t.Errorf("getters reporting the detachment: %v, want exactly the two", loud)
	}
}

// TestTheAmbiguityIsReal: a detached element's Attribute is indistinguishable from an
// absent attribute, which is why HasAttribute is worth reaching for.
func TestTheAmbiguityIsReal(t *testing.T) {
	u, err := Capture()
	if err != nil {
		t.Fatal(err)
	}
	// The element had class="c" while it was alive.
	v, ok := u.Element.Attribute("class")
	if v != "" || ok {
		t.Errorf("a detached element reported class=%q %v, want the zero answer", v, ok)
	}
	// An attached element with no such attribute answers identically.
	var live string
	var liveOK bool
	if _, err := lolhtml.RewriteString(`<a href="/x">t</a>`, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		live, liveOK = e.Attribute("class")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if live != v || liveOK != ok {
		t.Errorf("an absent attribute answers %q %v and a detached element %q %v; the "+
			"point is that they are the same", live, liveOK, v, ok)
	}
	// HasAttribute tells them apart.
	if _, err := u.Element.HasAttribute("class"); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("HasAttribute on a detached element = %v, want ErrDetached", err)
	}
}

// TestDetachedAnswersDirectly, on every unit, which is the cheap way to ask.
func TestDetachedAnswersDirectly(t *testing.T) {
	r, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	var answers int
	for _, c := range r.Calls {
		if c.Method != "Detached" {
			continue
		}
		answers++
		if c.Value != "true" {
			t.Errorf("%s.Detached answered %q, want true", c.Unit, c.Value)
		}
	}
	if answers < 6 {
		t.Errorf("only %d units were asked whether they are detached", answers)
	}
}

// TestNothingTheRetainedUnitsWereToldReachedTheOutput, which is the guarantee underneath
// all of it.
func TestNothingTheRetainedUnitsWereToldReachedTheOutput(t *testing.T) {
	u, err := Capture()
	if err != nil {
		t.Fatal(err)
	}
	before := u.Rewrote
	u.Ask() // every mutator, on every unit, after the fact
	after, err := Capture()
	if err != nil {
		t.Fatal(err)
	}
	if before != after.Rewrote {
		t.Errorf("the rewrite is not reproducible: %q then %q", before, after.Rewrote)
	}
	for _, gone := range []string{"<b", "x</", "data-x"} {
		if strings.Contains(before, gone) {
			t.Errorf("the output holds %q, so something a retained unit was told reached it: %q",
				gone, before)
		}
	}
}

// TestTheTableIsReadable, since the program exists to be read.
func TestTheTableIsReadable(t *testing.T) {
	r, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	s := r.String()
	for _, want := range []string{"unit", "method", "kind", "answer", "Element", "TextChunk",
		"Comment", "Doctype", "EndTag", "DocumentEnd", "Sink", "ErrDetached"} {
		if !strings.Contains(s, want) {
			t.Errorf("the table is missing %q", want)
		}
	}
	if lines := strings.Count(strings.TrimSpace(s), "\n"); lines != len(r.Calls) {
		t.Errorf("the table has %d rows for %d calls", lines, len(r.Calls))
	}
}

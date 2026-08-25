package lolhtml_test

// A class or id that starts with a digit.
//
// CSS cannot write one after "#" or "." - the grammar says an identifier does not
// start with a digit - and lol-html reports the refusal as "The selector is
// empty", which describes what its parser had left rather than what the caller
// wrote. Generated ids and utility class names ("2xl:hidden") land here, so the
// error now carries the two ways out, and this file checks that both of them work.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// refusedSelector runs a selector and returns the error, which every case here expects.
func refusedSelector(t *testing.T, selector string) *lolhtml.SelectorError {
	t.Helper()
	_, err := lolhtml.RewriteString("<p>x</p>", lolhtml.OnElement(selector, func(*lolhtml.Element) error {
		return nil
	}))
	if err == nil {
		t.Fatalf("%s was accepted", selector)
	}
	var se *lolhtml.SelectorError
	if !errors.As(err, &se) {
		t.Fatalf("%s: %v is not a *SelectorError", selector, err)
	}
	return se
}

// TestALeadingDigitIsRefusedWithAnAnswer. The message lol-html gives says nothing
// useful, so the error adds what to write instead.
func TestALeadingDigitIsRefusedWithAnAnswer(t *testing.T) {
	for _, tc := range []struct {
		selector string
		escape   string // the hex-escaped form the message should suggest
		attr     string // the attribute-selector form it should suggest
	}{
		{"#1a", `#\31 a`, `[id="1a"]`},
		{"#1", `#\31 `, `[id="1"]`},
		{"#-1a", `#-\31 a`, `[id="-1a"]`},
		{".1col", `.\31 col`, `[class~="1col"]`},
		{".2xl", `.\32 xl`, `[class~="2xl"]`},
		// A utility class with both problems at once: a leading digit and a colon.
		{`.2xl\:hidden`, `.\32 xl\:hidden`, `[class~="2xl:hidden"]`},
		{"div#9lives", `#\39 lives`, `[id="9lives"]`},
	} {
		se := refusedSelector(t, tc.selector)
		msg := se.Error()
		if !strings.Contains(msg, tc.escape) {
			t.Errorf("%s: the message does not suggest %q:\n%s", tc.selector, tc.escape, msg)
		}
		if !strings.Contains(msg, tc.attr) {
			t.Errorf("%s: the message does not suggest %q:\n%s", tc.selector, tc.attr, msg)
		}
		if se.Selector != tc.selector {
			t.Errorf("Selector = %q, want %q", se.Selector, tc.selector)
		}
	}
}

// TestBothSuggestionsWork is the test that makes the hint worth having: whatever
// it tells a caller to write has to match the element they were trying to reach.
func TestBothSuggestionsWork(t *testing.T) {
	for _, tc := range []struct{ doc, escape, attr string }{
		{`<p id="1a">x</p>`, `#\31 a`, `[id="1a"]`},
		{`<p id="1">x</p>`, `#\31 `, `[id="1"]`},
		{`<p id="-1a">x</p>`, `#-\31 a`, `[id="-1a"]`},
		{`<p class="1col">x</p>`, `.\31 col`, `[class~="1col"]`},
		{`<p class="a 2xl:hidden b">x</p>`, `.\32 xl\:hidden`, `[class~="2xl:hidden"]`},
		{`<p id="9lives">x</p>`, `#\39 lives`, `[id="9lives"]`},
	} {
		for _, selector := range []string{tc.escape, tc.attr} {
			matched := 0
			if _, err := lolhtml.RewriteString(tc.doc, lolhtml.OnElement(selector,
				func(*lolhtml.Element) error {
					matched++
					return nil
				})); err != nil {
				t.Errorf("%s on %q: %v", selector, tc.doc, err)
				continue
			}
			if matched != 1 {
				t.Errorf("%s matched %d elements of %q, want 1", selector, matched, tc.doc)
			}
		}
	}
}

// TestTheSpaceInTheEscapeMatters, which is the trap the suggestion exists to avoid:
// "\31a" is one character, U+031A, and not a "1" followed by an "a".
func TestTheSpaceInTheEscapeMatters(t *testing.T) {
	const doc = `<p id="1a">x</p>`
	matched := 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(`#\31a`, func(*lolhtml.Element) error {
		matched++
		return nil
	})); err != nil {
		t.Fatalf(`#\31a: %v`, err)
	}
	if matched != 0 {
		t.Errorf(`#\31a matched %d elements; if it now matches, the suggestion could drop the space`, matched)
	}
	// The six-digit form needs no space, and works.
	matched = 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(`#\000031a`, func(*lolhtml.Element) error {
		matched++
		return nil
	})); err != nil {
		t.Fatalf(`#\000031a: %v`, err)
	}
	if matched != 1 {
		t.Errorf(`#\000031a matched %d elements, want 1`, matched)
	}
}

// TestTheHintDoesNotFireOnSelectorsThatAreFine, because a hint on a selector whose
// problem is something else sends the reader the wrong way.
func TestTheHintDoesNotFireOnSelectorsThatAreFine(t *testing.T) {
	// These parse, so there is no error to carry a hint.
	for _, selector := range []string{`#a1`, `#_1`, `#--a`, `.a\.b`, `#a\.b`, `#\31 a`, `[id="1a"]`} {
		if _, err := lolhtml.RewriteString("<p>x</p>", lolhtml.OnElement(selector,
			func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%s: %v", selector, err)
		}
	}
	// And a selector that fails for another reason keeps its own hint.
	se := refusedSelector(t, "esi:include")
	if strings.Contains(se.Error(), "starts with a digit") {
		t.Errorf("the digit hint fired on a colon problem:\n%s", se.Error())
	}
	if !strings.Contains(se.Error(), `esi\:include`) {
		t.Errorf("the colon hint is missing:\n%s", se.Error())
	}
	// An unsupported pseudo-class keeps the colon hint, which is deliberate: the
	// hint is worded as a condition, because a single colon may be either a
	// pseudo-class or part of a name.
	se = refusedSelector(t, ":last-child")
	if strings.Contains(se.Error(), "starts with a digit") {
		t.Errorf("the digit hint fired on a pseudo-class:\n%s", se.Error())
	}
}

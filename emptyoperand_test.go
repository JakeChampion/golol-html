package lolhtml_test

// An attribute selector whose operand is the empty string.
//
// CSS says a substring operator with an empty value matches nothing. Three of the
// six operators here do something else, and two of those match most of a page - so
// a selector built by interpolating a value that turns out to be empty does the
// opposite of nothing. The library cannot refuse it, because it does not parse
// selectors; what it can do is say so.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// matched returns the attribute values of the elements a selector reached, so a
// failure says which elements rather than how many.
func matched(t *testing.T, doc, selector, attr string) []string {
	t.Helper()
	var got []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(selector, func(e *lolhtml.Element) error {
		v, _ := e.Attribute(attr)
		got = append(got, v)
		return nil
	})); err != nil {
		t.Fatalf("%s: %v", selector, err)
	}
	return got
}

// hrefPage has every shape of the attribute: a value, an empty value, a
// whitespace value, and no attribute at all.
const hrefPage = `<a href="/a">1</a><a href="">2</a><a href=" ">3</a><a>4</a><p>5</p>`

// TestAnEmptyOperandDoesNotMatchNothing is the finding: two of the operators match
// most of the page.
func TestAnEmptyOperandDoesNotMatchNothing(t *testing.T) {
	for _, tc := range []struct {
		selector string
		want     []string
		why      string
	}{
		// As the specification says.
		{`a[href=""]`, []string{""}, "an empty value"},
		{`a[href*=""]`, nil, "nothing"},
		// Not as the specification says.
		{`a[href^=""]`, []string{"/a", " "}, "every non-empty value"},
		{`a[href$=""]`, []string{"/a", " "}, "every non-empty value"},
		{`a[href~=""]`, []string{"", " "}, "every value with no words in it"},
	} {
		got := matched(t, hrefPage, tc.selector, "href")
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s matched %q, want %q (%s)", tc.selector, got, tc.want, tc.why)
		}
	}
}

// TestTheDashMatchIsRightWithAnEmptyOperand, which is the one where matching
// something is what the specification asks for: an empty value, or one that starts
// with a hyphen.
func TestTheDashMatchIsRightWithAnEmptyOperand(t *testing.T) {
	const page = `<p lang="">1</p><p lang="-x">2</p><p lang="en">3</p><p lang="en-GB">4</p><p>5</p>`
	if got := matched(t, page, `p[lang|=""]`, "lang"); strings.Join(got, "|") != "|-x" {
		t.Errorf(`p[lang|=""] matched %q, want the empty value and the one starting "-"`, got)
	}
	// And with a real operand, for contrast.
	if got := matched(t, page, `p[lang|="en"]`, "lang"); strings.Join(got, "|") != "en|en-GB" {
		t.Errorf(`p[lang|="en"] matched %q`, got)
	}
}

// TestTheFlagsDoNotChangeIt, since the case flags are what a caller reaches for
// when a match is not behaving.
func TestTheFlagsDoNotChangeIt(t *testing.T) {
	for _, selector := range []string{`a[href^="" i]`, `a[href^="" s]`} {
		if got := matched(t, hrefPage, selector, "href"); strings.Join(got, "|") != "/a| " {
			t.Errorf("%s matched %q, want the two non-empty values", selector, got)
		}
	}
}

// TestAnOmittedOperandIsARefusal, which is the shape that fails loudly - and the
// difference between it and the empty string is one keystroke.
func TestAnOmittedOperandIsARefusal(t *testing.T) {
	for _, selector := range []string{`a[href^=]`, `a[href$=]`, `a[href~=]`, `a[href=]`} {
		_, err := lolhtml.RewriteString(hrefPage, lolhtml.OnElement(selector, func(*lolhtml.Element) error {
			return nil
		}))
		if err == nil {
			t.Errorf("%s was accepted", selector)
			continue
		}
		var se *lolhtml.SelectorError
		if !errors.As(err, &se) {
			t.Errorf("%s: %v is not a *SelectorError", selector, err)
		}
	}
	// Quoting makes no difference to the empty operand: both forms match.
	for _, selector := range []string{`a[href^=""]`, `a[href^='']`} {
		if got := matched(t, hrefPage, selector, "href"); len(got) != 2 {
			t.Errorf("%s matched %d elements, want 2", selector, len(got))
		}
	}
}

// TestTheOnlyThingThatHelpsIsCheckingFirst, which is what the documentation
// recommends: the guard is a line of code and there is nothing else.
func TestTheOnlyThingThatHelpsIsCheckingFirst(t *testing.T) {
	build := func(prefix string) []lolhtml.Option {
		if prefix == "" {
			// Nothing to match, so nothing is registered - rather than a selector
			// that matches everything.
			return nil
		}
		return []lolhtml.Option{lolhtml.OnElement(`a[href^="`+prefix+`"]`, func(*lolhtml.Element) error {
			return nil
		})}
	}
	count := 0
	opts := build("")
	if len(opts) != 0 {
		t.Fatal("the guard registered a handler for an empty prefix")
	}
	// And with a prefix, the selector does what it says.
	if _, err := lolhtml.RewriteString(hrefPage, lolhtml.OnElement(`a[href^="/"]`,
		func(*lolhtml.Element) error {
			count++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("a real prefix matched %d elements, want 1", count)
	}
}

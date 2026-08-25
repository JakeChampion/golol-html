package lolhtml_test

// A selector list against separate handlers.
//
// They look interchangeable and they are not: a list is one selector, so an
// element that matches several of its parts is handled once, while separate
// handlers each run. A rewrite that appends, counts or increments is a different
// program depending on which spelling it used.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// listDoc has one element that every rule below matches.
const listDoc = `<a href="/x" class="t">1</a>`

// TestASelectorListHandlesAnElementOnce, however many of its parts match.
func TestASelectorListHandlesAnElementOnce(t *testing.T) {
	for _, selector := range []string{
		`a`,
		`a, a`,
		`a, a, a`,
		`a[href], a.t`,
		`a[href], a.t, a`,
		`a[href="/x"], a[class~="t"], a:first-child`,
		strings.Repeat("a, ", 200) + "a",
	} {
		calls := 0
		if _, err := lolhtml.RewriteString(listDoc, lolhtml.OnElement(selector,
			func(*lolhtml.Element) error {
				calls++
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", short(selector), err)
		}
		if calls != 1 {
			t.Errorf("%s: %d calls, want 1", short(selector), calls)
		}
	}
}

// TestSeparateHandlersEachRun, which is the contrast and the reason the two
// spellings are not interchangeable.
func TestSeparateHandlersEachRun(t *testing.T) {
	for _, tc := range []struct {
		what      string
		selectors []string
		want      int
	}{
		{"two different selectors", []string{`a[href]`, `a.t`}, 2},
		{"the same selector twice", []string{`a`, `a`}, 2},
		{"three overlapping", []string{`a[href]`, `a.t`, `a`}, 3},
		{"one that does not match", []string{`a`, `p`}, 1},
	} {
		calls := 0
		inc := func(*lolhtml.Element) error {
			calls++
			return nil
		}
		opts := make([]lolhtml.Option, 0, len(tc.selectors))
		for _, s := range tc.selectors {
			opts = append(opts, lolhtml.OnElement(s, inc))
		}
		if _, err := lolhtml.RewriteString(listDoc, opts...); err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if calls != tc.want {
			t.Errorf("%s: %d calls, want %d", tc.what, calls, tc.want)
		}
	}
}

// TestTheDifferenceIsVisibleInTheOutput, which is the reason to care: the same
// rules in two spellings produce two documents.
func TestTheDifferenceIsVisibleInTheOutput(t *testing.T) {
	append_ := func(e *lolhtml.Element) error {
		v, _ := e.Attribute("data-n")
		return e.SetAttribute("data-n", v+"x")
	}

	list, err := lolhtml.RewriteString(listDoc, lolhtml.OnElement(`a[href], a.t`, append_))
	if err != nil {
		t.Fatal(err)
	}
	separate, err := lolhtml.RewriteString(listDoc,
		lolhtml.OnElement(`a[href]`, append_),
		lolhtml.OnElement(`a.t`, append_))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, `data-n="x"`) {
		t.Errorf("the list produced %q, want one append", list)
	}
	if !strings.Contains(separate, `data-n="xx"`) {
		t.Errorf("the separate handlers produced %q, want two appends", separate)
	}
	if list == separate {
		t.Error("the two spellings produced the same document, which is the whole point")
	}
}

// TestAnInvalidPartFailsTheWholeList, and the error carries the list rather than
// the part - which is worth knowing when the list is long.
func TestAnInvalidPartFailsTheWholeList(t *testing.T) {
	for _, selector := range []string{`a, :bogus`, `:bogus, a`, `a, esi:include`} {
		_, err := lolhtml.RewriteString(listDoc, lolhtml.OnElement(selector,
			func(*lolhtml.Element) error { return nil }))
		if err == nil {
			t.Errorf("%s was accepted", selector)
			continue
		}
		var se *lolhtml.SelectorError
		if !errors.As(err, &se) {
			t.Errorf("%s: %v is not a *SelectorError", selector, err)
			continue
		}
		if se.Selector != selector {
			t.Errorf("Selector = %q, want the whole list %q", se.Selector, selector)
		}
	}
}

// TestAnEmptyPartIsRefused, where the message means what it says for once: a part
// of the list really is empty.
func TestAnEmptyPartIsRefused(t *testing.T) {
	for _, selector := range []string{`a,,p`, `a, p,`, `,a`, `,`} {
		_, err := lolhtml.RewriteString(listDoc, lolhtml.OnElement(selector,
			func(*lolhtml.Element) error { return nil }))
		if err == nil {
			t.Errorf("%s was accepted", selector)
		}
	}
	// Whitespace around the commas is fine.
	calls := 0
	if _, err := lolhtml.RewriteString(listDoc+"<p>2</p>", lolhtml.OnElement(" a ,\t p \n",
		func(*lolhtml.Element) error {
			calls++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("%d calls, want 2", calls)
	}
}

func short(s string) string {
	if len(s) <= 30 {
		return s
	}
	return s[:12] + "..." + s[len(s)-8:]
}

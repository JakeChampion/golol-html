package lolhtml_test

// Mutating attributes: what happens to a duplicate, and what happens if you do it
// while iterating.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const duplicated = `<a href="first" href="second" title="t">x</a>`

func onA(t *testing.T, doc string, fn func(*lolhtml.Element) error) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", fn))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// TestSetAttributeWritesTheFirstCopyOnly is the asymmetry: remove takes every
// copy and set takes one, so a rewrite that sanitises by changing a value leaves
// the value it was sanitising in the bytes.
func TestSetAttributeWritesTheFirstCopyOnly(t *testing.T) {
	got := onA(t, duplicated, func(e *lolhtml.Element) error {
		return e.SetAttribute("href", "safe")
	})
	if want := `<a href="safe" href="second" title="t">x</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Setting twice still only touches the first.
	got = onA(t, duplicated, func(e *lolhtml.Element) error {
		if err := e.SetAttribute("href", "one"); err != nil {
			return err
		}
		return e.SetAttribute("href", "two")
	})
	if want := `<a href="two" href="second" title="t">x</a>`; got != want {
		t.Errorf("setting twice: got %q, want %q", got, want)
	}
}

// RemoveAttribute takes every copy, which its documentation says and this pins.
func TestRemoveAttributeTakesEveryCopy(t *testing.T) {
	got := onA(t, duplicated, func(e *lolhtml.Element) error {
		return e.RemoveAttribute("href")
	})
	if want := `<a title="t">x</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// And removing again is not an error.
	got = onA(t, duplicated, func(e *lolhtml.Element) error {
		for range 3 {
			if err := e.RemoveAttribute("href"); err != nil {
				return err
			}
		}
		return nil
	})
	if want := `<a title="t">x</a>`; got != want {
		t.Errorf("removing three times: got %q, want %q", got, want)
	}
}

// The recipe the documentation gives, and what it costs: one copy, at the end.
func TestRemoveThenSetLeavesOneCopy(t *testing.T) {
	got := onA(t, duplicated, func(e *lolhtml.Element) error {
		if err := e.RemoveAttribute("href"); err != nil {
			return err
		}
		return e.SetAttribute("href", "safe")
	})
	if want := `<a title="t" href="safe">x</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Count(got, "href") != 1 {
		t.Errorf("%d copies of href in %q", strings.Count(got, "href"), got)
	}
}

// Mutating while iterating is safe, and the walk is over the attributes as they
// were.
func TestMutatingWhileIterating(t *testing.T) {
	const doc = `<a href="x" title="y" data-z="w">t</a>`

	// Setting inside the loop takes effect and does not disturb the walk.
	var seen []string
	got := onA(t, doc, func(e *lolhtml.Element) error {
		for k, v := range e.Attributes() {
			seen = append(seen, k)
			if err := e.SetAttribute(k, strings.ToUpper(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if want := "href,title,data-z"; strings.Join(seen, ",") != want {
		t.Errorf("visited %q, want %q", seen, want)
	}
	if want := `<a href="X" title="Y" data-z="W">t</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Removing inside the loop likewise.
	got = onA(t, doc, func(e *lolhtml.Element) error {
		for k := range e.Attributes() {
			if err := e.RemoveAttribute(k); err != nil {
				return err
			}
		}
		return nil
	})
	if want := `<a>t</a>`; got != want {
		t.Errorf("removing inside the loop: got %q, want %q", got, want)
	}

	// An attribute added inside the loop is not visited, so the loop
	// terminates. Without that, this test would not finish.
	iterations := 0
	got = onA(t, doc, func(e *lolhtml.Element) error {
		for range e.Attributes() {
			iterations++
			if iterations > 100 {
				t.Fatal("the iteration is following its own additions")
			}
			if err := e.SetAttribute("added"+strings.Repeat("x", iterations), "1"); err != nil {
				return err
			}
		}
		return nil
	})
	if iterations != 3 {
		t.Errorf("iterated %d times over 3 attributes", iterations)
	}
	if !strings.Contains(got, "addedxxx") {
		t.Errorf("the additions did not reach the output: %q", got)
	}
}

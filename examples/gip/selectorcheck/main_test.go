package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestNewWriterNamesOneBadSelectorAndThisNamesThemAll, which is why the program exists: a list with
// five bad selectors costs five round trips if the library is asked once.
func TestNewWriterNamesOneBadSelectorAndThisNamesThemAll(t *testing.T) {
	good := []string{"a[href]", "div.x", "#id", "p", "*"}
	bad := []string{":has(p)", "li + li", "::before", "p:empty", ":not(div p)"}
	all := append(append([]string{}, good...), bad...)

	// One call names one.
	err := FirstRejection(all)
	if err == nil {
		t.Fatal("a list with five bad selectors was accepted")
	}
	named := 0
	for _, s := range bad {
		if strings.Contains(err.Error(), s) {
			named++
		}
	}
	if named != 1 {
		t.Errorf("one NewWriter named %d of the %d bad selectors", named, len(bad))
	}
	var se *lolhtml.SelectorError
	if !errors.As(err, &se) {
		t.Errorf("err = %T, want *SelectorError", err)
	}

	// Checking each names all of them, and the usable ones stay usable.
	res := CheckAll(all)
	if len(res.Checks) != len(all) {
		t.Fatalf("%d checks for %d selectors", len(res.Checks), len(all))
	}
	if res.Usable() != len(good) {
		t.Errorf("%d usable, want %d", res.Usable(), len(good))
	}
	rejected := res.Rejected()
	if len(rejected) != len(bad) {
		t.Fatalf("%d rejected, want %d", len(rejected), len(bad))
	}
	for _, c := range rejected {
		if c.Reason() == "" {
			t.Errorf("%s was rejected with no reason", c.Selector)
		}
		if !strings.Contains(res.String(), c.Selector) {
			t.Errorf("the report does not name %s:\n%s", c.Selector, res)
		}
	}
	// Every good one is reported usable, which is the half a validator gets wrong by being
	// too strict.
	for _, c := range res.Checks {
		want := true
		for _, b := range bad {
			if c.Selector == b {
				want = false
			}
		}
		if c.OK() != want {
			t.Errorf("%s: usable = %v, want %v (%v)", c.Selector, c.OK(), want, c.Err)
		}
	}
}

// TestTheAdviceOnlyReplacesAMisleadingMessage, since the library's wording is better for the
// combinators and a validator that talked over it would be noise.
func TestTheAdviceOnlyReplacesAMisleadingMessage(t *testing.T) {
	for _, tt := range []struct {
		selector string
		advises  bool
		says     string
	}{
		// The library words these well, so there is nothing to add.
		{"li + li", false, ""},
		{"li ~ li", false, ""},

		// These it words misleadingly or vaguely.
		{":not(div p)", true, "combinator inside :not()"},
		{":not(div > p)", true, "combinator inside :not()"},
		{":has(p)", true, ":has, :is and :where"},
		{":is(p)", true, ":has, :is and :where"},
		{"::before", true, "pseudo-element"},
		{"p:empty", true, "what follows the element"},
		{"p:last-child", true, "what follows the element"},
		{":root", true, "tree or a state"},
		{"esi:include", true, "has to be escaped"},

		// A :not() without a combinator is accepted, so there is nothing to advise.
		{":not(div)", false, ""},
	} {
		c := check(tt.selector)
		if tt.selector == ":not(div)" {
			if !c.OK() {
				t.Errorf("%s was rejected: %v", tt.selector, c.Err)
			}
			continue
		}
		if c.OK() {
			t.Errorf("%s was accepted", tt.selector)
			continue
		}
		if (c.Advice != "") != tt.advises {
			t.Errorf("%s: advice = %q, want any = %v", tt.selector, c.Advice, tt.advises)
		}
		if tt.says != "" && !strings.Contains(c.Advice, tt.says) {
			t.Errorf("%s: advice %q does not mention %q", tt.selector, c.Advice, tt.says)
		}
		// The library's own words are still available, because that is what a search
		// will match.
		if c.Reason() == "" {
			t.Errorf("%s: the library's message was dropped", tt.selector)
		}
	}
}

// TestRegisteringSelectorsTogetherIsSuperlinear, which is why checking them one at a time is not a
// cost paid for better errors. The assertion is on the shape rather than on a duration: the
// per-selector cost at 4000 has to be several times the per-selector cost at 100.
func TestRegisteringSelectorsTogetherIsSuperlinear(t *testing.T) {
	build := func(n int) func() {
		opts := make([]lolhtml.Option, n)
		for i := range opts {
			opts[i] = lolhtml.OnElement(fmt.Sprintf(".c%d", i),
				func(*lolhtml.Element) error { return nil })
		}
		return func() {
			w, err := lolhtml.NewWriter(io.Discard, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}

	small, large := 100, 2000
	// Allocations are the host-independent measure, and they are linear - which is B172's
	// figure and half the story.
	perSmall := allocsPerSelector(t, small, build(small))
	perLarge := allocsPerSelector(t, large, build(large))
	if perLarge > perSmall*2 {
		t.Errorf("allocations per selector went from %.1f at %d to %.1f at %d, which is "+
			"not the linear figure B172 records", perSmall, small, perLarge, large)
	}

	// The time is not linear, and this is the half that cannot be gated on every machine: the
	// smaller build is a hundred microseconds, and the Windows runner's clock ticks every
	// 340µs or so, where that reads as zero. So the assertion is made only where the clock
	// can resolve both figures, and skipped out loud where it cannot - the rule
	// docs/gip/GIP.md gives after examples/gip/queue paid for it.
	tick := clockTick()
	tSmall := fastest(t, 10, build(small))
	tLarge := fastest(t, 10, build(large))
	perSelSmall := float64(tSmall.Nanoseconds()) / float64(small)
	perSelLarge := float64(tLarge.Nanoseconds()) / float64(large)
	t.Logf("clock tick %v; %d selectors: %v (%.0f ns each); %d selectors: %v (%.0f ns each)",
		tick, small, tSmall, perSelSmall, large, tLarge, perSelLarge)

	if tSmall < 20*tick || tLarge < 20*tick {
		t.Logf("not checking the ratio: %v and %v against a tick of %v is too few ticks "+
			"to compare", tSmall, tLarge, tick)
		return
	}
	if perSelLarge < perSelSmall*1.5 {
		t.Errorf("per-selector build cost went from %.0f ns at %d to %.0f ns at %d, which "+
			"does not show the superlinearity this documents", perSelSmall, small,
			perSelLarge, large)
	}
}

// TestCheckingIndividuallyIsNotSlowerAtScale, which is the consequence: a validator that asks about
// each selector separately is cheaper than one big registration once the list is long.
func TestCheckingIndividuallyIsNotSlowerAtScale(t *testing.T) {
	const n = 1000
	list := make([]string, n)
	for i := range list {
		list[i] = fmt.Sprintf(".c%d", i)
	}

	tick := clockTick()
	together := fastest(t, 5, func() {
		if err := FirstRejection(list); err != nil {
			t.Fatal(err)
		}
	})
	individually := fastest(t, 5, func() {
		if res := CheckAll(list); len(res.Rejected()) != 0 {
			t.Fatalf("%d rejected", len(res.Rejected()))
		}
	})
	t.Logf("clock tick %v; %d selectors: together %v, individually %v",
		tick, n, together, individually)
	if together < 20*tick || individually < 20*tick {
		t.Logf("not checking the ratio: %v and %v against a tick of %v", together,
			individually, tick)
		return
	}
	// A wide margin: the claim is that checking each is not several times worse, not that it
	// is always faster.
	if float64(individually) > float64(together)*2 {
		t.Errorf("individually %v against %v together, which is more than twice",
			individually, together)
	}
}

// TestACommentMarkerThatIsNotASelectorCharacter, because "#" begins an id selector and a reader
// that ate #id would report a shorter list than it was given.
func TestACommentMarkerThatIsNotASelectorCharacter(t *testing.T) {
	res := CheckAll([]string{"#id", "#main .x", "a[href]"})
	if len(res.Checks) != 3 {
		t.Errorf("%d checks for 3 selectors", len(res.Checks))
	}
	if res.Usable() != 3 {
		t.Errorf("%d usable of 3: %v", res.Usable(), res.Rejected())
	}
}

// TestAnEmptySelectorIsRejectedRatherThanSkipped, since an empty line in a rule file is not a rule
// and an empty selector is not valid.
func TestAnEmptySelectorIsRejectedRatherThanSkipped(t *testing.T) {
	res := CheckAll([]string{"a", "", "   ", "b"})
	if len(res.Checks) != 2 {
		t.Errorf("%d checks, want 2 - blank lines are not selectors", len(res.Checks))
	}
	// An explicitly empty selector, not a blank line.
	if c := check(""); c.OK() {
		t.Error("the empty selector was accepted")
	}
}

func allocsPerSelector(t *testing.T, n int, f func()) float64 {
	t.Helper()
	return allocsPer(3, f) / float64(n)
}

// clockTick measures the smallest interval this platform's clock reports, so a timing assertion can
// be skipped where it would be comparing noise.
func clockTick() time.Duration {
	best := time.Duration(0)
	for range 5 {
		start := time.Now()
		var d time.Duration
		for d == 0 {
			d = time.Since(start)
		}
		if best == 0 || d < best {
			best = d
		}
	}
	return best
}

// fastest and allocsPer are the measuring helpers, kept here because they are test-only.
func fastest(t *testing.T, runs int, f func()) time.Duration {
	t.Helper()
	best := time.Duration(-1)
	for range runs {
		start := time.Now()
		f()
		if d := time.Since(start); best < 0 || d < best {
			best = d
		}
	}
	return best
}

func allocsPer(runs int, f func()) float64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return math.Round(float64(after.Mallocs-before.Mallocs) / float64(runs))
}

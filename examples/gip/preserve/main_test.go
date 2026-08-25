package main

import (
	"strings"
	"testing"
)

// TestThePreservingRewritesPreserveEveryShape, at the token level.
func TestThePreservingRewritesPreserveEveryShape(t *testing.T) {
	for _, doc := range []string{
		`<div><p>x</p></div>`,
		`<table><tr><td>a</td></tr></table>`,
		`<select><option>a</option></select>`,
		`<ul><li>a</li><li>b</li></ul>`,
		`<p>a<b>b</b>c</p>`,
		`<dl><dt>a</dt><dd>b</dd></dl>`,
		`<template><tr><td>x</td></tr></template>`,
		`<svg><circle r="1"/></svg>`,
		`<p>a<img src="x">b</p>`,
		`<form><input name="a"></form>`,
		`<!DOCTYPE html><html><body><p>x</p></body></html>`,
		``,
	} {
		outcomes, err := Check([]byte(doc))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, o := range outcomes {
			if o.Hazard {
				continue
			}
			if o.Err != nil {
				t.Errorf("%q: %s failed: %v", doc, o.Rewrite, o.Err)
			}
			if !o.Preserved {
				t.Errorf("%q: %s changed the tokens: %s", doc, o.Rewrite, o.Diff)
			}
		}
	}
}

// TestTheHazardIsOnlyAHazardWhereAnEndTagIsMissing, which is the whole point of running
// the check over a caller's own document.
func TestTheHazardIsOnlyAHazardWhereAnEndTagIsMissing(t *testing.T) {
	hazard := func(doc string) Outcome {
		t.Helper()
		outcomes, err := Check([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range outcomes {
			if o.Hazard {
				return o
			}
		}
		t.Fatal("no hazard in the set")
		return Outcome{}
	}

	// Closed items: replacing their content is exactly what it says.
	if o := hazard(`<ul><li>a</li><li>b</li></ul>`); !o.Preserved {
		t.Errorf("on a closed list the hazard changed the tokens: %s", o.Diff)
	}
	// Implied end tags: it takes the items after it as well.
	o := hazard(`<ul><li>a<li>b</ul>`)
	if o.Preserved {
		t.Error("on a list with implied end tags the hazard changed nothing")
	}
	if !strings.Contains(o.Diff, "token") {
		t.Errorf("the difference is %q, want the token it happened at", o.Diff)
	}
	// And a document with no list at all is untouched by it.
	if o := hazard(`<p>x</p>`); !o.Preserved {
		t.Errorf("a document with no list: %s", o.Diff)
	}
}

// TestTheRewriteSetIsWhatItSaysItIs, so the list and the checks cannot drift apart.
func TestTheRewriteSetIsWhatItSaysItIs(t *testing.T) {
	rs := Rewrites()
	if len(rs) != 6 {
		t.Errorf("%d rewrites, want 6", len(rs))
	}
	var hazards int
	for _, r := range rs {
		if r.Hazard {
			hazards++
		}
		if r.Name == "" || r.Opts == nil {
			t.Errorf("%+v is incomplete", r)
		}
	}
	if hazards != 1 {
		t.Errorf("%d hazards, want 1", hazards)
	}
}

// TestTheReportNamesTheFirstDifference, since "changed" is not something a caller can act
// on.
func TestTheReportNamesTheFirstDifference(t *testing.T) {
	outcomes, err := Check([]byte(`<ul><li>a<li>b</ul>`))
	if err != nil {
		t.Fatal(err)
	}
	s := report(outcomes)
	for _, want := range []string{"rewrite", "tokens", "first difference", "CHANGED", "token"} {
		if !strings.Contains(s, want) {
			t.Errorf("the report is missing %q:\n%s", want, s)
		}
	}
	if lines := strings.Count(strings.TrimSpace(s), "\n"); lines != len(outcomes) {
		t.Errorf("%d rows for %d outcomes", lines, len(outcomes))
	}
}

// TestTheDifferenceFunctionSaysWhatHappened, over the three shapes a difference takes.
func TestTheDifferenceFunctionSaysWhatHappened(t *testing.T) {
	a := []token{{Kind: "el", Name: "p"}, {Kind: "text", Name: "x"}, {Kind: "end", Name: "p"}}
	if got := difference(a, a); got != "" {
		t.Errorf("identical token lists differ: %q", got)
	}
	if got := difference(a, a[:2]); !strings.Contains(got, "disappeared") {
		t.Errorf("a shorter list: %q", got)
	}
	if got := difference(a[:2], a); !strings.Contains(got, "appeared") {
		t.Errorf("a longer list: %q", got)
	}
	b := []token{{Kind: "el", Name: "div"}, {Kind: "text", Name: "x"}, {Kind: "end", Name: "p"}}
	if got := difference(a, b); !strings.Contains(got, "became") {
		t.Errorf("a changed token: %q", got)
	}
	// An element that ended early is named as such, because that is the common failure.
	c := []token{{Kind: "end", Name: "li"}}
	d := []token{{Kind: "el", Name: "li"}}
	if got := difference(c, d); !strings.Contains(got, "ended early") {
		t.Errorf("an early end: %q", got)
	}
}

package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func fix(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Fix(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Fix(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheTwoAttributesGoWhereTheyBelong.
func TestTheTwoAttributesGoWhereTheyBelong(t *testing.T) {
	for _, tc := range []struct {
		doc, want         string
		controls, preload int
	}{
		{`<video src="a"></video>`, `<video src="a" controls="" preload="none"></video>`, 1, 1},
		{`<audio src="a"></audio>`, `<audio src="a" controls="" preload="none"></audio>`, 1, 1},
		{`<video controls src="a"></video>`, `<video controls src="a" preload="none"></video>`, 0, 1},
		{`<video preload="auto" src="a"></video>`, `<video preload="auto" src="a" controls=""></video>`, 1, 0},
		{`<video CONTROLS src="a"></video>`, `<video CONTROLS src="a" preload="none"></video>`, 0, 1},
		// The page decided, either way: preload="" is a preload attribute.
		{`<video preload="" src="a"></video>`, `<video preload="" src="a" controls=""></video>`, 1, 0},
		// Nothing to do.
		{`<video controls preload="metadata"></video>`, `<video controls preload="metadata"></video>`, 0, 0},
		// A video with source children is still one element to decide about.
		{`<video><source src="a"></video>`, `<video controls="" preload="none"><source src="a"></video>`, 1, 1},
	} {
		got, res := fix(t, tc.doc, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if res.Controls != tc.controls || res.Preload != tc.preload {
			t.Errorf("%q: Controls=%d Preload=%d, want %d and %d",
				tc.doc, res.Controls, res.Preload, tc.controls, tc.preload)
		}
	}
}

// TestAnAutoplayingElementKeepsItsPreload, because preload="none" and autoplay
// contradict each other and the page asked for the autoplay.
func TestAnAutoplayingElementKeepsItsPreload(t *testing.T) {
	got, res := fix(t, `<video autoplay src="a"></video>`, Options{})
	if strings.Contains(got, "preload") {
		t.Errorf("got %q, want no preload", got)
	}
	if !strings.Contains(got, `controls=""`) {
		t.Errorf("got %q, want controls", got)
	}
	if res.Autoplaying != 1 || res.Preload != 0 {
		t.Errorf("%v", res)
	}
	// A page that set both is left with both.
	got, _ = fix(t, `<video autoplay preload="none" src="a"></video>`, Options{})
	if got != `<video autoplay preload="none" src="a" controls=""></video>` {
		t.Errorf("got %q", got)
	}
}

// TestADecorativeBackgroundIsLeftBare: autoplay and muted together is the shape a
// background video takes, and a control bar sits on top of the design.
func TestADecorativeBackgroundIsLeftBare(t *testing.T) {
	const doc = `<video autoplay muted src="a"></video>`
	got, res := fix(t, doc, Options{})
	if got != doc {
		t.Errorf("got %q, want it untouched", got)
	}
	if res.Decorative != 1 || res.Controls != 0 {
		t.Errorf("%v", res)
	}
	// -all is for a caller who would rather have the bar.
	got, res = fix(t, doc, Options{All: true})
	if got != `<video autoplay muted src="a" controls=""></video>` {
		t.Errorf("got %q", got)
	}
	if res.Controls != 1 || res.Decorative != 0 {
		t.Errorf("%v", res)
	}
	// Muted without autoplay is not a background: it is a video someone will press
	// play on, and it needs the button.
	got, _ = fix(t, `<video muted src="a"></video>`, Options{})
	if !strings.Contains(got, `controls=""`) {
		t.Errorf("got %q", got)
	}
}

// TestMediaInATemplateIsCountedSeparately, since handlers fire in there and the
// content is inert until a script clones it.
func TestMediaInATemplateIsCountedSeparately(t *testing.T) {
	const doc = `<video src="a"></video><template><video src="b"></video></template>`
	got, res := fix(t, doc, Options{})
	if strings.Count(got, `controls=""`) != 2 {
		t.Errorf("got %q, want both rewritten", got)
	}
	if res.Controls != 2 || res.InTemplate != 1 {
		t.Errorf("%v", res)
	}
	// The count is a number a caller can act on, not two numbers added together.
	got, res = fix(t, doc, Options{SkipTemplates: true})
	if want := `<video src="a" controls="" preload="none"></video><template><video src="b"></video></template>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Controls != 1 || res.InTemplate != 1 {
		t.Errorf("%v", res)
	}
}

// TestTheTemplateCountIsADepth, not a flag: a nested template still ends, and media
// after a template is not in one.
func TestTheTemplateCountIsADepth(t *testing.T) {
	for _, tc := range []struct {
		doc              string
		inTemplate, rest int
	}{
		{`<template><video></video></template><video></video>`, 1, 1},
		{`<template><template><video></video></template></template>`, 1, 0},
		{`<template><video></video></template><template><video></video></template>`, 2, 0},
		{`<template></template><video></video>`, 0, 1},
		// An unclosed template runs to the end of the document, so everything after
		// it is inside it.
		{`<template><video></video><video></video>`, 2, 0},
	} {
		_, res := fix(t, tc.doc, Options{SkipTemplates: true})
		if res.InTemplate != tc.inTemplate || res.Controls != tc.rest {
			t.Errorf("%q: InTemplate=%d Controls=%d, want %d and %d",
				tc.doc, res.InTemplate, res.Controls, tc.inTemplate, tc.rest)
		}
	}
}

// TestFixingTwiceChangesNothing.
func TestFixingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<video src="a"></video>`,
		`<audio autoplay src="a"></audio>`,
		`<video autoplay muted src="a"></video>`,
		`<template><video src="a"></video></template>`,
		`<video controls preload="auto"></video>`,
	} {
		for _, opts := range []Options{{}, {All: true}, {SkipTemplates: true}} {
			once, _ := fix(t, doc, opts)
			twice, res := fix(t, once, opts)
			if twice != once {
				t.Errorf("%q (%+v)\n once %q\ntwice %q", doc, opts, once, twice)
			}
			if res.Controls+res.Preload != 0 {
				t.Errorf("%q: the second pass added something: %v", doc, res)
			}
		}
	}
}

// TestOnlyMediaIsTouched: an iframe holding a player is somebody else's document,
// and a source or track element has no controls of its own.
func TestOnlyMediaIsTouched(t *testing.T) {
	for _, doc := range []string{
		`<iframe src="a"></iframe>`,
		`<source src="a">`,
		`<track src="a">`,
		`<object data="a"></object>`,
		`<div>x</div>`,
	} {
		if got, _ := fix(t, doc, Options{}); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries, including a start tag split down the
// middle: what the element says is read from the tag, not from a chunk.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<template><video autoplay muted src="a"></video></template><video src="b"></video>`
	want, _ := fix(t, doc, Options{})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		f := &fixer{}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, f.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if f.res.InTemplate != 1 || f.res.Controls != 1 {
			t.Errorf("chunks of %d: %v", size, f.res)
		}
	}
}

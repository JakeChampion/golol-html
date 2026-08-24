package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<h1>One</h1>`,
	`<h1>One</h1><h2>Two</h2><h3>Three</h3>`,
	`<h1>A</h1><h2>B</h2><h2>C</h2><h1>D</h1><h2>E</h2>`,
	`<h2>Starts deep</h2><h1>Then shallow</h1>`,
	`<h1>A</h1><h4>Jumped</h4>`,
	`<h2>1. Already</h2>`,
	`<h2>IV. Roman already</h2>`,
	`<h2>Ignore</h2>`,
	`<h2><em>Marked</em> up</h2>`,
	`<h2>Caf&eacute;</h2>`,
	`<h2></h2>`,
	`<!DOCTYPE html><html><body><h1>Doc</h1></body></html>`,
	`<p>no headings</p>`,
	``,
}

func chunked(in string, n int, num *numberer) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, num.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := numberString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, newNumberer())
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

// TestOutlineNumbering is the arithmetic: a deeper level nests, a shallower one
// resets what is below it.
func TestOutlineNumbering(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`<h1>A</h1>`, `<h1>1. A</h1>`},
		{`<h1>A</h1><h1>B</h1>`, `<h1>1. A</h1><h1>2. B</h1>`},
		{`<h1>A</h1><h2>B</h2>`, `<h1>1. A</h1><h2>1.1. B</h2>`},
		{`<h1>A</h1><h2>B</h2><h2>C</h2>`, `<h1>1. A</h1><h2>1.1. B</h2><h2>1.2. C</h2>`},
		// The reset: after 1.1 and 1.2, the next h1 is 2 and its child is 2.1,
		// not 2.3.
		{`<h1>A</h1><h2>B</h2><h2>C</h2><h1>D</h1><h2>E</h2>`,
			`<h1>1. A</h1><h2>1.1. B</h2><h2>1.2. C</h2><h1>2. D</h1><h2>2.1. E</h2>`},
		{`<h1>A</h1><h2>B</h2><h3>C</h3><h2>D</h2>`,
			`<h1>1. A</h1><h2>1.1. B</h2><h3>1.1.1. C</h3><h2>1.2. D</h2>`},
	} {
		got, _, err := numberString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("%s\n got: %s\nwant: %s", tt.in, got, tt.want)
		}
	}
}

// TestSkippedLevelsShowAsZeroAndAreReported. An h2 followed by an h4 has no h3
// to count, so the label carries a zero. That is the document's outline being
// wrong, and smoothing it over would hide it.
func TestSkippedLevelsShowAsZeroAndAreReported(t *testing.T) {
	// A document that starts below -min counts too: an h2 with no h1 above it
	// numbers as 0.1, which is the outline being wrong rather than the label.
	if _, n, err := numberString(`<h2>Starts deep</h2>`); err != nil {
		t.Fatal(err)
	} else if n.jumped != 1 {
		t.Errorf("a document starting at h2 with -min 1: jumped=%d, want 1", n.jumped)
	}

	got, n, err := numberString(`<h1>A</h1><h4>Jumped</h4>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1.0.0.1.") {
		t.Errorf("expected a zero in the label: %s", got)
	}
	if n.jumped != 1 {
		t.Errorf("jumped=%d, want 1", n.jumped)
	}
	if !strings.Contains(n.report(), "skipped-a-level=1") {
		t.Errorf("the report does not mention it:\n%s", n.report())
	}
}

// TestTheLabelIsNotFedBackIn is why this program can be a single pass: inserted
// content is not dispatched, so the text handler sees the document's text and
// not the label just prepended. If it were fed back, the accumulator would see
// "1. Intro" and every re-run would compound.
func TestTheLabelIsNotFedBackIn(t *testing.T) {
	// Numbered from h2, so the label is 1 rather than 0.1: with -min 1 an h2
	// with no h1 above it has skipped a level and says so.
	in := `<h2>Intro</h2>`
	got, n, err := numberString(in, func(n *numberer) { n.minLevel = 2 })
	if err != nil {
		t.Fatal(err)
	}
	if want := `<h2>1. Intro</h2>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	// The accumulated text is what decides whether a heading counts as already
	// numbered, so it has to be the document's.
	if n.skipped != 0 {
		t.Errorf("skipped=%d: the program saw its own label as a pre-existing number", n.skipped)
	}
}

// TestAlreadyNumberedHeadingsAreReported. The label goes in at the start tag,
// before the text is known, so it cannot be withdrawn once the text turns out to
// start with a number. Reporting it is the only honest option, and the report
// says why.
func TestAlreadyNumberedHeadingsAreReported(t *testing.T) {
	got, n, err := numberString(`<h2>1. Already</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1. 1. Already") {
		t.Errorf("expected the double label to be visible rather than hidden: %s", got)
	}
	if n.skipped != 1 {
		t.Errorf("skipped=%d, want 1", n.skipped)
	}
	if !strings.Contains(n.report(), "cannot be undone mid-stream") {
		t.Errorf("the report does not explain itself:\n%s", n.report())
	}
}

func TestStartsWithNumber(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"1. Intro", true},
		{"1.2.3 Intro", true},
		{"12 Intro", true},
		{"IV. Roman", true},
		{"IV Roman", true},
		{"I. One", true},
		{"Intro", false},
		{"", false},
		{"Introduction", false},
		// A leading capital that is a Roman letter but part of a word.
		{"Ideas", false},
		{"Various things", false},
	} {
		if got := startsWithNumber(tt.in); got != tt.want {
			t.Errorf("startsWithNumber(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRoman(t *testing.T) {
	for _, tt := range []struct {
		in   int
		want string
	}{
		{1, "I"}, {4, "IV"}, {9, "IX"}, {14, "XIV"}, {40, "XL"},
		{90, "XC"}, {400, "CD"}, {900, "CM"}, {1987, "MCMLXXXVII"},
		{0, "0"}, {-1, "-1"},
	} {
		if got := roman(tt.in); got != tt.want {
			t.Errorf("roman(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}

	got, _, err := numberString(`<h1>A</h1><h2>B</h2><h1>C</h1>`,
		func(n *numberer) { n.style = "roman" })
	if err != nil {
		t.Fatal(err)
	}
	if want := `<h1>I. A</h1><h2>I.1. B</h2><h1>II. C</h1>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestLevelRange(t *testing.T) {
	got, _, err := numberString(`<h1>A</h1><h2>B</h2><h3>C</h3><h4>D</h4>`,
		func(n *numberer) { n.minLevel, n.maxLevel = 2, 3 })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, ">1. A") {
		t.Errorf("h1 was numbered despite -min 2: %s", got)
	}
	if !strings.Contains(got, ">1. B") || !strings.Contains(got, ">1.1. C") {
		t.Errorf("h2 and h3 were not numbered from the range base: %s", got)
	}
	if strings.Contains(got, "D</h4>") && strings.Contains(got, ". D") {
		t.Errorf("h4 was numbered despite -max 3: %s", got)
	}
}

// TestSeparatorCannotBecomeMarkup: the separator is inserted as Text, so even a
// hostile one is inert.
func TestSeparatorCannotBecomeMarkup(t *testing.T) {
	got, _, err := numberString(`<h2>Intro</h2>`, func(n *numberer) {
		n.sep = ` <script>alert(1)</script> `
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("the separator became markup: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("the separator was not escaped: %s", got)
	}
}

package main

import (
	"io"
	"strings"
	"testing"
)

func count(t *testing.T, doc string) Report {
	t.Helper()
	rep, err := Count(strings.NewReader(doc), DefaultWPM)
	if err != nil {
		t.Fatalf("Count(%q): %v", doc, err)
	}
	return rep
}

func TestWordCount(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		words int
		chars int
	}{
		{"empty", ``, 0, 0},
		{"one word", `<p>hello</p>`, 1, 5},
		{"two words", `<p>hello world</p>`, 2, 10},
		{"runs of space", `<p>hello    world</p>`, 2, 10},
		{"newlines and tabs", "<p>hello\n\tworld</p>", 2, 10},
		{"leading and trailing space", `<p>   hello   </p>`, 1, 5},
		{"bare text", `hello world`, 2, 10},

		// Inline markup does not separate words; a block does.
		{"inline markup inside a word", `<p>three<b>four</b></p>`, 1, 9},
		{"inline markup between words", `<p>one <b>two</b></p>`, 2, 6},
		{"block between words", `<p>one</p><p>two</p>`, 2, 6},
		{"br separates", `<p>one<br>two</p>`, 2, 6},
		{"list items separate", `<ul><li>one<li>two</ul>`, 2, 6},
		{"span does not separate", `<p>one<span>two</span></p>`, 1, 6},

		// References are decoded first, so what they decode to decides.
		{"an ampersand is a word", `<p>a &amp; b</p>`, 3, 3},
		{"an escaped space separates", `<p>a&#32;b</p>`, 2, 2},
		{"a non-breaking space separates", `<p>a&nbsp;b</p>`, 2, 2},
		{"an accented word is one word", `<p>caf&eacute;</p>`, 1, 4},

		// Not prose.
		{"script", `<p>one</p><script>var x = 1</script>`, 1, 3},
		{"style", `<style>p{color:red}</style><p>one</p>`, 1, 3},
		{"title", `<html><head><title>a b c</title></head><body><p>one</p></body></html>`, 1, 3},
		{"template", `<template><p>a b c</p></template><p>one</p>`, 1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := count(t, tt.doc)
			if rep.Words != tt.words {
				t.Errorf("Words = %d, want %d", rep.Words, tt.words)
			}
			if rep.Characters != tt.chars {
				t.Errorf("Characters = %d, want %d", rep.Characters, tt.chars)
			}
		})
	}
}

// The count must not depend on how the input was written. This is the whole
// reason the state is one bit outside the handler.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>hello world</p>`,
		`<p>three<b>four</b> five</p>`,
		`<h1>Hello &amp; welcome</h1><p>` + strings.Repeat("word ", 50) + `</p>`,
		`<ul><li>one<li>two<li>three</ul>`,
		`<p>a&#32;b&nbsp;c</p>`,
		`<p>one</p><script>var x = "two three"</script><p>four</p>`,
	}
	for _, doc := range docs {
		want := count(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			got, err := Count(&chunked{s: doc, n: n}, DefaultWPM)
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if got != want {
				t.Fatalf("%q at writes of %d: got %+v, want %+v", doc, n, got, want)
			}
		}
	}
}

// The version that looks right: strings.Fields per chunk. It counts a word once
// for every chunk it is split across, and the difference is not small.
func TestPerChunkCountingOvercounts(t *testing.T) {
	doc := `<p>` + strings.Repeat("word ", 200) + `</p>`

	correct := count(t, doc)
	if correct.Words != 200 {
		t.Fatalf("the correct count is %d, want 200", correct.Words)
	}

	// Written whole, the naive version agrees.
	whole, err := CountPerChunk(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if whole != 200 {
		t.Errorf("written whole, per-chunk counting gave %d, want 200", whole)
	}

	// A byte at a time, it does not. The exact number depends on the chunking
	// and is not the point; being several times too large is.
	split, err := CountPerChunk(&chunked{s: doc, n: 1})
	if err != nil {
		t.Fatal(err)
	}
	if split <= 200*2 {
		t.Errorf("per-chunk counting of one-byte writes gave %d; this test exists "+
			"because it should be several times the real count of 200", split)
	}

	// And the correct counter is unmoved.
	got, err := Count(&chunked{s: doc, n: 1}, DefaultWPM)
	if err != nil {
		t.Fatal(err)
	}
	if got.Words != 200 {
		t.Errorf("the streaming counter gave %d for one-byte writes, want 200", got.Words)
	}
}

func TestReadingTime(t *testing.T) {
	tests := []struct {
		words, wpm, minutes int
	}{
		{0, 200, 0},
		{1, 200, 1},
		{200, 200, 1},
		{201, 200, 2},
		{400, 200, 2},
		{401, 200, 3},
		{1000, 250, 4},
	}
	for _, tt := range tests {
		doc := `<p>` + strings.Repeat("word ", tt.words) + `</p>`
		rep, err := Count(strings.NewReader(doc), tt.wpm)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Words != tt.words {
			t.Fatalf("%d words counted as %d", tt.words, rep.Words)
		}
		if rep.Minutes != tt.minutes {
			t.Errorf("%d words at %d wpm = %d minutes, want %d",
				tt.words, tt.wpm, rep.Minutes, tt.minutes)
		}
	}
}

func TestReportString(t *testing.T) {
	if got := count(t, ``).String(); got != "no words" {
		t.Errorf("empty document reported %q", got)
	}
	got := count(t, `<p>one</p>`).String()
	for _, want := range []string{"1 words", "3 characters", "1 minute", "220 words a minute"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not mention %q", got, want)
		}
	}
	// "1 minute", not "1 minutes".
	if strings.Contains(got, "1 minutes") {
		t.Errorf("%q pluralises one minute", got)
	}
}

// A skipped element nested inside another has to count in and out, or a script
// in the head turns off the rest of the document.
func TestSkippingNests(t *testing.T) {
	tests := []struct {
		doc   string
		words int
	}{
		{`<head><style>a b</style></head><body><p>one</p></body>`, 1},
		{`<template><script>a b</script></template><p>one</p>`, 1},
		{`<p>one</p><script>x</script><p>two</p><style>y</style><p>three</p>`, 3},
	}
	for _, tt := range tests {
		if got := count(t, tt.doc).Words; got != tt.words {
			t.Errorf("%q: %d words, want %d", tt.doc, got, tt.words)
		}
	}
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

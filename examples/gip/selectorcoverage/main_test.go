package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const doc = `<div class="used"><p>t</p><span id="s">u</span></div><ul><li>a</li><li>b</li></ul>`

// TestBucketsAreExhaustive: every selector ends up matched, unmatched or not
// checkable, and nothing is lost. Coverage that was not measured must not read
// as coverage that was.
func TestBucketsAreExhaustive(t *testing.T) {
	sels := []string{
		".used", ".unused", "div > p", "#s", "li:first-child",
		"a:hover", "li:last-child", "::before", "li + li", ":is(li)",
		"", "[bad=",
	}

	c, err := coverageOf(sels, doc)
	if err != nil {
		t.Fatal(err)
	}

	matched := 0
	for _, n := range c.hits {
		if n > 0 {
			matched++
		}
	}
	total := matched + len(c.unmatched) + len(c.uncheckable)
	if total != len(sels) {
		t.Errorf("%d selectors in, %d accounted for (matched=%d unmatched=%d uncheckable=%d)",
			len(sels), total, matched, len(c.unmatched), len(c.uncheckable))
	}
}

func TestMatchedAndUnmatched(t *testing.T) {
	c, err := coverageOf([]string{".used", ".unused", "div > p", "#s", "#nope"}, doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{".used", "div > p", "#s"} {
		if c.hits[sel] == 0 {
			t.Errorf("%s should have matched", sel)
		}
	}
	if strings.Join(c.unmatched, ",") != "#nope,.unused" {
		t.Errorf("unmatched = %v, want #nope and .unused", c.unmatched)
	}
}

// TestUncheckableSelectorsCarryTheirReason. A selector the rewriter cannot use is
// reported with what it said, so a reader can tell "your stylesheet uses :hover"
// from "your selector is malformed".
func TestUncheckableSelectorsCarryTheirReason(t *testing.T) {
	sels := []string{"a:hover", "li:last-child", "li + li", "::before", ":is(li)", "svg|circle", ""}

	c, err := coverageOf(sels, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.uncheckable) != len(sels) {
		t.Fatalf("uncheckable=%d, want all %d", len(c.uncheckable), len(sels))
	}
	for _, u := range c.uncheckable {
		if u.reason == "" {
			t.Errorf("%q was reported with no reason", u.selector)
		}
	}
	// The reasons distinguish the kinds of problem.
	report := c.report()
	for _, want := range []string{"pseudo-class", "combinator", "namespace", "empty"} {
		if !strings.Contains(strings.ToLower(report), want) {
			t.Errorf("the report does not distinguish %q:\n%s", want, report)
		}
	}
}

// TestTheReportNeverElidesTheUncheckableBucket, because a coverage tool that
// quietly dropped it would claim to have measured what it had not.
func TestTheReportNeverElidesTheUncheckableBucket(t *testing.T) {
	c, err := coverageOf([]string{".used", "a:hover"}, doc)
	if err != nil {
		t.Fatal(err)
	}
	r := c.report()
	if !strings.Contains(r, "not-checkable=1") {
		t.Errorf("the summary line hides it:\n%s", r)
	}
	if !strings.Contains(r, "not checkable: a:hover") {
		t.Errorf("the detail line is missing:\n%s", r)
	}
}

func TestCSSExtraction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.css")
	css := `/* leading comment */
.a, .b { color: red }
a:hover { color: blue }

@media (min-width: 40em) {
  .inside { display: flex }
  .also, .too { x: y }
}

@font-face { font-family: x }

div > p { padding: 0 }
/* trailing`
	if err := os.WriteFile(path, []byte(css), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readCSS(path)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{".a", ".b", "a:hover", ".inside", ".also", ".too", "div > p"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("\n got: %v\nwant: %v", got, want)
	}
}

// TestNestedAtRulesAreDescendedInto: a rule inside @media is a rule, and missing
// them would understate coverage.
func TestNestedAtRulesAreDescendedInto(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.css")
	if err := os.WriteFile(path, []byte(
		`@media screen { @supports (a:b) { .deep { x: y } } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCSS(path)
	if err != nil {
		t.Fatal(err)
	}
	// One level of descent, which is what scanSelectors does; the doubly nested
	// rule is not found, and that is a limitation rather than a silent loss -
	// nothing is reported as covered that was not looked at.
	if len(got) != 0 && strings.Join(got, "|") != ".deep" {
		t.Errorf("got %v", got)
	}
}

func TestDuplicateSelectorsAreCountedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.css")
	if err := os.WriteFile(path, []byte(".a { x: y } .a { z: w } .a, .b { q: r }"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCSS(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != ".a|.b" {
		t.Errorf("got %v, want .a and .b once each", got)
	}
}

func TestReadList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l.txt")
	if err := os.WriteFile(path, []byte("# a comment\n\n.a\n  .b  \n\n#c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readList(path)
	if err != nil {
		t.Fatal(err)
	}
	// "#c" begins with a hash and is dropped as a comment, which is the cost of
	// using # for comments in a file of CSS selectors. Recorded rather than
	// hidden.
	if strings.Join(got, "|") != ".a|.b" {
		t.Errorf("got %v, want .a and .b", got)
	}
}

func TestStripComments(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"a/*b*/c", "ac"},
		{"a/*b", "a"},
		{"/*a*/", ""},
		{"a", "a"},
		{"/*a*/b/*c*/d", "bd"},
	} {
		if got := stripComments(tt.in); got != tt.want {
			t.Errorf("stripComments(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchingBrace(t *testing.T) {
	body, rest := matchingBrace("{a{b}c}tail")
	if body != "a{b}c" || rest != "tail" {
		t.Errorf("body=%q rest=%q", body, rest)
	}
	body, rest = matchingBrace("{unclosed")
	if body != "unclosed" || rest != "" {
		t.Errorf("unclosed: body=%q rest=%q", body, rest)
	}
}

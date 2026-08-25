package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func check(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Check([]byte(doc))
	if err != nil {
		t.Fatalf("Check(%q): %v", doc, err)
	}
	return res
}

func kinds(res Result) string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, string(f.Kind))
	}
	return strings.Join(out, ",")
}

// TestADuplicateIsReportedOnceWithEveryPlaceItIsUsed, because a person fixing it
// needs both ends.
func TestADuplicateIsReportedOnceWithEveryPlaceItIsUsed(t *testing.T) {
	res := check(t, "<p id=\"a\">1</p>\n<p id=\"a\">2</p>\n<p id=\"a\">3</p>")
	if got := kinds(res); got != "duplicate" {
		t.Fatalf("findings %q, want one duplicate", got)
	}
	msg := res.Findings[0].Message
	for _, want := range []string{`id="a"`, "3 times", "2:1", "3:1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not contain %q: %s", want, msg)
		}
	}
	if res.Findings[0].Line != 1 {
		t.Errorf("reported at line %d, want the first occurrence", res.Findings[0].Line)
	}
	// One id used once is nothing.
	if res := check(t, `<p id="a">1</p><p id="b">2</p>`); !res.OK() {
		t.Errorf("findings on distinct ids: %v", res.Findings)
	}
}

// TestTheReferencesThatBreakAreNamed, which is the point of the report: a
// duplicate id is a list of broken things rather than one.
func TestTheReferencesThatBreakAreNamed(t *testing.T) {
	doc := `<h2 id="a">A</h2><div id="a">B</div>` +
		`<a href="#a">link</a><label for="a">L</label>` +
		`<input aria-labelledby="a"><button aria-controls="a">x</button>`
	res := check(t, doc)
	if n := strings.Count(kinds(res), "duplicate"); n != 2 {
		t.Fatalf("findings %q, want the duplicate and its references", kinds(res))
	}
	msg := res.Findings[1].Message
	for _, want := range []string{"fragment link", "for=", "aria-labelledby=", "aria-controls="} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not name %s: %s", want, msg)
		}
	}
	// And the reason it survives is in the message, because that is the useful part.
	if !strings.Contains(msg, "matches all of them") {
		t.Errorf("the message does not say what CSS does: %s", msg)
	}
	// A duplicate nothing points at gets the first line only.
	res = check(t, `<p id="a">1</p><p id="a">2</p>`)
	if got := kinds(res); got != "duplicate" {
		t.Errorf("findings %q, want just the duplicate", got)
	}
}

// TestEveryAttributeThatNamesAnIdIsFollowed, because the list is a list and a
// program that knows half of it reports half the problem.
func TestEveryAttributeThatNamesAnIdIsFollowed(t *testing.T) {
	for _, tc := range []struct{ doc, attr string }{
		{`<label for="gone">x</label>`, "for"},
		{`<input form="gone">`, "form"},
		{`<input list="gone">`, "list"},
		{`<td headers="gone">x</td>`, "headers"},
		{`<div aria-activedescendant="gone">x</div>`, "aria-activedescendant"},
		{`<div aria-controls="gone">x</div>`, "aria-controls"},
		{`<div aria-describedby="gone">x</div>`, "aria-describedby"},
		{`<div aria-details="gone">x</div>`, "aria-details"},
		{`<div aria-errormessage="gone">x</div>`, "aria-errormessage"},
		{`<div aria-flowto="gone">x</div>`, "aria-flowto"},
		{`<div aria-labelledby="gone">x</div>`, "aria-labelledby"},
		{`<div aria-owns="gone">x</div>`, "aria-owns"},
		{`<a href="#gone">x</a>`, "fragment link"},
	} {
		res := check(t, tc.doc)
		if got := kinds(res); got != "broken-reference" {
			t.Errorf("%q: findings %q, want a broken reference", tc.doc, got)
			continue
		}
		if !strings.Contains(res.Findings[0].Message, tc.attr) {
			t.Errorf("%q: the message does not name %s: %s", tc.doc, tc.attr,
				res.Findings[0].Message)
		}
	}
	// The list-valued ones hold several ids, and each is followed separately.
	res := check(t, `<h2 id="one">1</h2><div aria-labelledby="one two three">x</div>`)
	if n := strings.Count(kinds(res), "broken-reference"); n != 2 {
		t.Errorf("findings %q, want two of the three ids reported", kinds(res))
	}
	if res.References != 3 {
		t.Errorf("References = %d, want 3", res.References)
	}
	// An href that is not a fragment is not a reference.
	if res := check(t, `<a href="/gone">x</a><a href="#">x</a>`); !res.OK() {
		t.Errorf("findings on non-fragment hrefs: %v", res.Findings)
	}
}

// TestAnIdThatCannotBeAddressed. The reason to report these is concrete: a
// fragment link and a CSS selector cannot name them.
func TestAnIdThatCannotBeAddressed(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<p id="">x</p>`, "invalid-id"},
		{`<p id="a b">x</p>`, "invalid-id"},
		{"<p id=\"a\tb\">x</p>", "invalid-id"},
		{`<p id="a-b_c.d">x</p>`, ""},
		{`<p id="1a">x</p>`, ""}, // legal, and a selector needs an escape for it
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestIdsDifferingOnlyInCase, which is legal and is reported as advice because a
// selector is case-sensitive: this is the same rule as the one that makes
// "#Main" miss id="main".
func TestIdsDifferingOnlyInCase(t *testing.T) {
	res := check(t, `<p id="main">1</p><p id="Main">2</p>`)
	if got := kinds(res); got != "case-only" {
		t.Fatalf("findings %q, want the case difference", got)
	}
	if !strings.Contains(res.Findings[0].Message, "legal") {
		t.Errorf("the message does not say it is legal: %s", res.Findings[0].Message)
	}
	// Measured rather than claimed: a selector really does match only one.
	for _, sel := range []string{"#main", "#Main"} {
		matched := 0
		if _, err := lolhtml.RewriteString(`<p id="main">1</p><p id="Main">2</p>`,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error {
				matched++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if matched != 1 {
			t.Errorf("%s matched %d elements, want 1", sel, matched)
		}
	}
	// Three spellings are reported once, from the first.
	res = check(t, `<p id="a">1</p><p id="A">2</p><p id="a">3</p>`)
	if !strings.Contains(kinds(res), "duplicate") || !strings.Contains(kinds(res), "case-only") {
		t.Errorf("findings %q, want both the duplicate and the case difference", kinds(res))
	}
}

// TestACleanDocumentIsQuiet.
func TestACleanDocumentIsQuiet(t *testing.T) {
	doc := `<body>
	  <h1 id="top">Title</h1>
	  <nav><a href="#section-one">One</a><a href="#section-two">Two</a></nav>
	  <h2 id="section-one">One</h2><p aria-describedby="note">a</p>
	  <h2 id="section-two">Two</h2><p id="note">A note</p>
	  <label for="email">Email</label><input id="email" list="providers">
	  <datalist id="providers"><option>a</option></datalist>
	  <table><tr><th id="h-name">Name</th><td headers="h-name">Ada</td></tr></table>
	</body>`
	res := check(t, doc)
	if !res.OK() {
		t.Errorf("findings on a clean document: %v", res.Findings)
	}
	if res.Ids != 7 || res.Unique != 7 {
		t.Errorf("%v: want seven distinct ids", res)
	}
	if res.References != 6 {
		t.Errorf("References = %d, want 6", res.References)
	}
}

// TestTheReportIsStableAcrossWritePatterns.
func TestTheReportIsStableAcrossWritePatterns(t *testing.T) {
	doc := "<body>\n<p id=\"a\">1</p>\n<p id=\"a\">2</p>\n<a href=\"#a\">l</a>\n" +
		"<p id=\"A\">3</p>\n<p id=\"b c\">4</p>\n<label for=\"gone\">x</label>\n</body>"
	want := check(t, doc)
	if len(want.Findings) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		c := &checker{ids: map[string][]occurrence{}}
		w, err := lolhtml.NewWriter(io.Discard, c.options()...)
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
		got := c.report([]byte(doc))
		if len(got.Findings) != len(want.Findings) {
			t.Errorf("chunks of %d: %d findings, want %d", size, len(got.Findings), len(want.Findings))
			continue
		}
		for i := range got.Findings {
			if got.Findings[i] != want.Findings[i] {
				t.Errorf("chunks of %d: finding %d is %+v, want %+v", size, i,
					got.Findings[i], want.Findings[i])
			}
		}
	}
}

// TestADuplicateAttributeOnOneElementIsNotADuplicateId: the parser reports both
// copies and a browser uses the first, so this is one id, twice written.
func TestADuplicateAttributeOnOneElementIsNotADuplicateId(t *testing.T) {
	res := check(t, `<p id="a" id="b">x</p>`)
	if !res.OK() {
		t.Errorf("findings %v, want none: the element has one id as far as a parser is "+
			"concerned", res.Findings)
	}
	if res.Ids != 1 {
		t.Errorf("Ids = %d, want 1", res.Ids)
	}
	// And a reference to the second copy's value finds nothing, which is what a
	// browser does too.
	res = check(t, `<p id="a" id="b">x</p><a href="#b">l</a>`)
	if got := kinds(res); got != "broken-reference" {
		t.Errorf("findings %q, want the reference reported", got)
	}
}

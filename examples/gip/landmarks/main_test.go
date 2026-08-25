package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func add(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Add(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Add(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestADocumentThatAlreadyHasLandmarksIsLeftAlone, which is the whole-document
// question the first pass exists to answer: a second banner is worse than none.
func TestADocumentThatAlreadyHasLandmarksIsLeftAlone(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"a main element", `<main>x</main><div id="masthead">h</div>`},
		{"a nav element", `<nav>n</nav><div id="masthead">h</div>`},
		{"an aside", `<aside>a</aside><div id="masthead">h</div>`},
		{"a header outside a section", `<header>h</header><div class="sidebar">s</div>`},
		{"a footer outside a section", `<footer>f</footer><div class="sidebar">s</div>`},
		{"an explicit role", `<div role="banner">h</div><div class="sidebar">s</div>`},
		{"a role among others", `<div role="foo banner">h</div><div class="sidebar">s</div>`},
		{"a role on something else", `<div role="navigation">n</div><div id="masthead">h</div>`},
	} {
		got, res := add(t, tc.doc)
		if got != tc.doc {
			t.Errorf("%s: %q was rewritten to %q", tc.what, tc.doc, got)
		}
		if res.Reason == "" {
			t.Errorf("%s: no reason given", tc.what)
		}
		if res.OK() {
			t.Errorf("%s: %v", tc.what, res)
		}
	}
}

// TestAHeaderInsideASectioningElementIsNotALandmark, which is the rule that
// decides whether a document has one: a header in an article is that article's
// header, not the page's banner.
func TestAHeaderInsideASectioningElementIsNotALandmark(t *testing.T) {
	// article and section only: an aside and a nav are landmarks themselves, so a
	// document containing one has been marked up already.
	for _, tag := range []string{"article", "section"} {
		doc := "<" + tag + "><header>h</header></" + tag + `><div id="masthead">m</div>`
		got, res := add(t, doc)
		if !strings.Contains(got, `role="banner"`) {
			t.Errorf("<%s>: the masthead was not promoted: %q (%v)", tag, got, res)
		}
	}
	// A footer in an article is the same, and the page's own footer still counts.
	doc := `<article><footer>a</footer></article><footer>page</footer><div class="sidebar">s</div>`
	got, _ := add(t, doc)
	if got != doc {
		t.Errorf("the page footer should have stopped this: %q", got)
	}
}

// TestTheEvidenceIsNames, and the names it will act on are the ones that mean one
// thing.
func TestTheEvidenceIsNames(t *testing.T) {
	for _, tc := range []struct {
		doc, want string
	}{
		{`<div id="masthead">x</div>`, "banner"},
		{`<div class="banner">x</div>`, "banner"},
		{`<div id="site-header">x</div>`, "banner"},
		{`<div id="siteHeader">x</div>`, "banner"},
		{`<ul class="navbar">x</ul>`, "navigation"},
		{`<div class="breadcrumbs">x</div>`, "navigation"},
		{`<div id="maincontent">x</div>`, "main"},
		{`<div id="content">x</div>`, "main"},
		{`<div class="sidebar">x</div>`, "complementary"},
		{`<div class="related">x</div>`, "complementary"},
		{`<div class="site-footer">x</div>`, "contentinfo"},
		{`<div id="colophon">x</div>`, "contentinfo"},
		{`<form class="search-form">x</form>`, "search"},
		{`<form id="search">x</form>`, "search"},
	} {
		got, res := add(t, tc.doc)
		if !strings.Contains(got, `role="`+tc.want+`"`) {
			t.Errorf("%q\n got %q\nwant role=%q (%v)", tc.doc, got, tc.want, res)
		}
	}
}

// TestAnAmbiguousNameIsNoEvidence. A "header" may be a page banner or the head of a
// table, and guessing is what makes a landmark list worse than none.
func TestAnAmbiguousNameIsNoEvidence(t *testing.T) {
	for _, doc := range []string{
		`<div class="header">x</div>`,
		`<div class="wrapper">x</div>`,
		`<div id="container">x</div>`,
		`<div class="inner box">x</div>`,
		`<div id="top">x</div>`,
		// No name at all.
		`<div>x</div>`,
		// A name that means nothing here.
		`<div class="promo">x</div>`,
	} {
		got, res := add(t, doc)
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.OK() {
			t.Errorf("%q: %v", doc, res)
		}
	}
	// And the ambiguous ones are counted, so a page full of "wrapper" divs shows up
	// as something other than silence.
	_, res := add(t, `<div class="header">x</div><div class="sidebar">y</div>`)
	if res.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1", res.Ambiguous)
	}
}

// TestTheUniqueRolesAreChosenRatherThanApplied, which is what makes the first pass
// a choice: three candidates for main, one role.
func TestTheUniqueRolesAreChosenRatherThanApplied(t *testing.T) {
	doc := `<div class="content">a</div><div id="maincontent">b</div><div class="primary">c</div>`
	got, res := add(t, doc)
	if n := strings.Count(got, `role="main"`); n != 1 {
		t.Errorf("%d mains, want 1: %q", n, got)
	}
	// The id is better evidence than a class, so the second one wins.
	if !strings.Contains(got, `<div id="maincontent" role="main">`) {
		t.Errorf("the best candidate was not chosen: %q (%v)", got, res)
	}
	// Navigation may repeat, so every candidate gets it.
	doc = `<ul class="navbar">a</ul><div class="breadcrumbs">b</div>`
	got, _ = add(t, doc)
	if n := strings.Count(got, `role="navigation"`); n != 2 {
		t.Errorf("%d navigations, want 2: %q", n, got)
	}
	// The last plausible footer is the page's, not the first.
	doc = `<div class="site-footer">a</div><div class="colophon">b</div>`
	got, _ = add(t, doc)
	if !strings.Contains(got, `<div class="colophon" role="contentinfo">`) {
		t.Errorf("the last contentinfo candidate should win: %q", got)
	}
	if n := strings.Count(got, "contentinfo"); n != 1 {
		t.Errorf("%d contentinfos, want 1: %q", n, got)
	}
}

// TestALandmarkInsideAnotherIsDropped, because a banner inside main reads worse
// than the page it came from.
func TestALandmarkInsideAnotherIsDropped(t *testing.T) {
	doc := `<div id="maincontent">a<div class="sidebar">s</div></div>`
	got, res := add(t, doc)
	if !strings.Contains(got, `role="main"`) {
		t.Errorf("the outer landmark is missing: %q", got)
	}
	if strings.Contains(got, "complementary") {
		t.Errorf("the nested landmark survived: %q", got)
	}
	if res.Nested != 1 {
		t.Errorf("Nested = %d, want 1", res.Nested)
	}
	// Siblings are not nested.
	doc = `<div id="maincontent">a</div><div class="sidebar">s</div>`
	got, res = add(t, doc)
	if !strings.Contains(got, `role="main"`) || !strings.Contains(got, `role="complementary"`) {
		t.Errorf("both should be marked: %q", got)
	}
	if res.Nested != 0 {
		t.Errorf("Nested = %d, want 0", res.Nested)
	}
}

// TestAnElementThatAlreadyHasARoleIsNotACandidate, whatever its name says - and
// because any landmark role stops the program, this is about the non-landmark ones.
func TestAnElementThatAlreadyHasARoleIsNotACandidate(t *testing.T) {
	doc := `<div class="sidebar" role="note">s</div><div id="masthead">h</div>`
	got, _ := add(t, doc)
	if strings.Contains(got, "complementary") {
		t.Errorf("the element with a role was rewritten: %q", got)
	}
	if !strings.Contains(got, `role="banner"`) {
		t.Errorf("the masthead should still be promoted: %q", got)
	}
}

// TestNoTagIsRenamed, because a div with role="main" is second best to a <main>
// and turning one into the other changes the parse of everything inside it.
func TestNoTagIsRenamed(t *testing.T) {
	doc := `<div id="masthead">h</div><ul class="navbar"><li>a</ul><div class="sidebar">s</div>`
	got, _ := add(t, doc)
	if tags(t, got) != tags(t, doc) {
		t.Errorf("the tags changed\n got %q\nwant %q", tags(t, got), tags(t, doc))
	}
}

func tags(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		if !e.CanHaveContent() {
			return nil
		}
		return e.OnEndTag(func(tag *lolhtml.EndTag) error {
			b.WriteString("</" + tag.Name() + ">")
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestAddingTwiceChangesNothing: the roles it added are landmarks, so the second
// pass finds a document that already has them and stops.
func TestAddingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<div id="masthead">h</div><div class="sidebar">s</div>`,
		`<div class="header">x</div>`,
		`<main>x</main>`,
	} {
		once, _ := add(t, doc)
		twice, res := add(t, once)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.OK() {
			t.Errorf("%q: the second pass added %v", doc, res.Added)
		}
	}
}

// TestTheFirstPassIsChunkInvariant, which is what makes the offsets identity.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := `<body><div id="masthead">h</div><ul class="navbar">n</ul>` +
		`<div id="maincontent"><div class="sidebar">s</div></div>` +
		`<form class="search-form">f</form><div class="site-footer">f</div></body>`
	want, _, err := Scan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{res: Result{Added: map[Role]int{}}}
		w, err := lolhtml.NewWriter(io.Discard, s.options()...)
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
		got := s.decide()
		if len(got) != len(want) {
			t.Errorf("chunks of %d: %v, want %v", size, got, want)
			continue
		}
		for at, role := range want {
			if got[at] != role {
				t.Errorf("chunks of %d: offset %d is %q, want %q", size, at, got[at], role)
			}
		}
	}
}

// TestOnlyPlausibleContainersAreCandidates: a name on a span or a table cell says
// nothing about landmarks.
func TestOnlyPlausibleContainersAreCandidates(t *testing.T) {
	for _, doc := range []string{
		`<span class="sidebar">x</span>`,
		`<td class="sidebar">x</td>`,
		`<a class="navbar" href="/x">x</a>`,
		`<img class="banner" src="/b.png">`,
	} {
		got, res := add(t, doc)
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Candidates != 0 {
			t.Errorf("%q: Candidates = %d, want 0", doc, res.Candidates)
		}
	}
}

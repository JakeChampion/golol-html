package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const doc = `<html><head><title>t</title></head><body>` +
	`<h1>Doc</h1><h2>One</h2><p>a</p><h2 class="x">Two</h2><h2>Three</h2>` +
	`</body></html>`

func withSheet(s *styler) { s.stylesheet = "/print.css" }

var corpus = []string{
	doc,
	`<html><head></head><body><h2>Only one</h2></body></html>`,
	`<html><head></head><body><h2>a</h2><h2 class="page-break-before">b</h2></body></html>`,
	`<html><head><link rel="stylesheet" href="/p.css" media="print"></head><body><h2>a</h2></body></html>`,
	`<html><head><link rel="stylesheet" href="/s.css"></head><body><h2>a</h2><h2>b</h2></body></html>`,
	`<html><body><h2>a</h2><h2>b</h2></body></html>`,
	`<html><head></head><body><p>no headings</p></body></html>`,
	`<p>fragment</p>`,
	``,
}

// hinted returns the class attribute of every h2, in document order.
func hinted(t *testing.T, out string) []string {
	t.Helper()
	var classes []string
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
			classes = append(classes, attr(e, "class"))
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return classes
}

func chunked(in string, n int, opts ...func(*styler)) (string, *styler, error) {
	s := defaults()
	for _, o := range opts {
		o(s)
	}
	if err := s.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, s.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), s, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, in := range corpus {
		whole, _, err := chunked(in, len(in)+1, withSheet)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, n := range []int{1, 2, 3, 23} {
			got, _, err := chunked(in, n, withSheet)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, in, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range corpus {
		once, _, err := styleString(in, withSheet)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		twice, s, err := styleString(once, withSheet)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", in, once, twice)
		}
		if s.linked != 0 || s.hinted != 0 {
			t.Errorf("the second pass of %q linked=%d hinted=%d", in, s.linked, s.hinted)
		}
	}
}

// TestTheFirstHeadingIsSkipped. A break before the first section pushes the
// article's own title onto a page of its own.
func TestTheFirstHeadingIsSkipped(t *testing.T) {
	out, s, err := styleString(doc, withSheet)
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 2 {
		t.Errorf("hinted=%d, want 2 of the three h2s", s.hinted)
	}
	classes := hinted(t, out)
	if len(classes) != 3 {
		t.Fatalf("%d h2s", len(classes))
	}
	if classes[0] != "" {
		t.Errorf("the first h2 was hinted: %q", classes[0])
	}
	if classes[1] != "x page-break-before" {
		t.Errorf("the second h2 is %q, want its own class kept and the hint added",
			classes[1])
	}
	if classes[2] != "page-break-before" {
		t.Errorf("the third h2 is %q", classes[2])
	}
}

// TestSkipFirstCanBeTurnedOff, for a document that is a section rather than a
// whole article.
func TestSkipFirstCanBeTurnedOff(t *testing.T) {
	_, s, err := styleString(doc, withSheet, func(s *styler) { s.skipFirst = false })
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 3 {
		t.Errorf("hinted=%d, want all three", s.hinted)
	}
}

// TestAnExistingClassIsKept, because the page's own classes are load-bearing.
func TestAnExistingClassIsKept(t *testing.T) {
	out, _, err := styleString(
		`<html><head></head><body><h2>a</h2><h2 class="lead intro">b</h2></body></html>`,
		withSheet)
	if err != nil {
		t.Fatal(err)
	}
	classes := hinted(t, out)
	if len(classes) != 2 || classes[1] != "lead intro page-break-before" {
		t.Errorf("classes = %v", classes)
	}
}

// TestTheHintIsNotAddedTwice, compared exactly because a class is
// case-sensitive.
func TestTheHintIsNotAddedTwice(t *testing.T) {
	out, s, err := styleString(
		`<html><head></head><body><h2>a</h2><h2 class="page-break-before">b</h2></body></html>`,
		withSheet)
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 0 {
		t.Errorf("hinted=%d, want 0 - the only candidate already had it", s.hinted)
	}
	if total(s.skipped) != 1 {
		t.Errorf("skipped=%v", s.skipped)
	}
	if n := strings.Count(out, "page-break-before"); n != 1 {
		t.Errorf("%d occurrences of the class: %s", n, out)
	}

	// A different case is a different class, so it is added.
	out, s, err = styleString(
		`<html><head></head><body><h2>a</h2><h2 class="Page-Break-Before">b</h2></body></html>`,
		withSheet)
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 1 {
		t.Errorf("hinted=%d: a class differing in case is a different class", s.hinted)
	}
}

// TestAPrintStylesheetAlreadyThereIsNotDuplicated, recognised by its media query
// rather than its href.
func TestAPrintStylesheetAlreadyThereIsNotDuplicated(t *testing.T) {
	out, s, err := styleString(
		`<html><head><link rel="stylesheet" href="/p.css" media="print"></head>`+
			`<body><h2>a</h2></body></html>`, withSheet)
	if err != nil {
		t.Fatal(err)
	}
	if s.linked != 0 || total(s.skipped) == 0 {
		t.Errorf("linked=%d skipped=%v", s.linked, s.skipped)
	}
	if n := strings.Count(out, "stylesheet"); n != 1 {
		t.Errorf("%d stylesheets: %s", n, out)
	}

	// A stylesheet with no print query is a different thing.
	_, s, err = styleString(
		`<html><head><link rel="stylesheet" href="/s.css"></head><body><h2>a</h2></body></html>`,
		withSheet)
	if err != nil {
		t.Fatal(err)
	}
	if s.linked != 1 {
		t.Errorf("linked=%d, want 1", s.linked)
	}
}

// TestEitherHalfAlone: the stylesheet and the hints are independent.
func TestEitherHalfAlone(t *testing.T) {
	out, s, err := styleString(doc, withSheet, func(s *styler) { s.class = "" })
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 0 || strings.Contains(out, "page-break") {
		t.Errorf("hints were added with -class empty: %s", out)
	}
	// With no stylesheet, only hints.
	out, s, err = styleString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if s.linked != 0 || strings.Contains(out, "media=\"print\"") {
		t.Errorf("a stylesheet was linked without -stylesheet: %s", out)
	}
	if s.hinted != 2 {
		t.Errorf("hinted=%d", s.hinted)
	}
}

// TestTheSelectorCanBeChanged, for a document whose sections are h3s.
func TestTheSelectorCanBeChanged(t *testing.T) {
	_, s, err := styleString(
		`<html><head></head><body><h3>a</h3><h3>b</h3><h2>not this</h2></body></html>`,
		func(s *styler) { s.selector = "h3" })
	if err != nil {
		t.Fatal(err)
	}
	if s.hinted != 1 {
		t.Errorf("hinted=%d, want 1 - the second h3 only", s.hinted)
	}
}

// TestTheHrefIsEscaped: the link is assembled as markup.
func TestTheHrefIsEscaped(t *testing.T) {
	out, _, err := styleString(`<html><head></head><body>x</body></html>`,
		func(s *styler) { s.stylesheet = "/p.css?v=1&x=2" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/p.css?v=1&amp;x=2"`) {
		t.Errorf("the href was not escaped: %s", out)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*styler)
	}{
		{"nothing to do", func(s *styler) { s.class = ""; s.stylesheet = "" }},
		{"an empty selector", func(s *styler) { s.selector = "" }},
		{"a class with a quote", func(s *styler) { s.class = `x" onload="y` }},
		{"a class starting with a digit", func(s *styler) { s.class = "1x" }},
	} {
		if _, _, err := styleString(doc, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

func TestValidClass(t *testing.T) {
	for _, good := range []string{"page-break-before", "a", "A_1", "_x", "x-y_z9"} {
		if !validClass(good) {
			t.Errorf("validClass(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "1x", "a b", "a.b", `a"b`, "a#b", "a>b"} {
		if validClass(bad) {
			t.Errorf("validClass(%q) = true", bad)
		}
	}
}

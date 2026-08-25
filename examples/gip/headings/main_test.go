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

// kinds is the findings' kinds in order, which is what most of these tests are
// about.
func kinds(res Result) string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, string(f.Kind))
	}
	return strings.Join(out, ",")
}

// TestASkippedLevelIsReported, and a level going back down is not.
func TestASkippedLevelIsReported(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"<h1>a</h1><h2>b</h2>", ""},
		{"<h1>a</h1><h3>b</h3>", "skips"},
		{"<h1>a</h1><h2>b</h2><h4>c</h4>", "skips"},
		{"<h1>a</h1><h2>b</h2><h3>c</h3><h2>d</h2><h3>e</h3>", ""},
		// Going back up several levels at once is not a skip: an h2 after an h4 is
		// the next section.
		{"<h1>a</h1><h2>b</h2><h3>c</h3><h4>d</h4><h2>e</h2>", ""},
		// But going down again from there is measured from where it is.
		{"<h1>a</h1><h2>b</h2><h3>c</h3><h4>d</h4><h2>e</h2><h4>f</h4>", "skips"},
		// A document that starts below h1.
		{"<h3>a</h3>", "skips,no-h1"},
		{"<h2>a</h2><h3>b</h3>", "skips,no-h1"},
		{"<h1>a</h1><h1>b</h1>", "many-h1"},
		{"<h1>a</h1><h1>b</h1><h3>c</h3>", "skips,many-h1"},
		// No headings at all is not a finding: a fragment need not have one.
		{"<p>a</p>", ""},
		{"", ""},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestAHeadingWithNoNameIsReported, because a heading list entry with nothing to
// say is a dead entry.
func TestAHeadingWithNoNameIsReported(t *testing.T) {
	for _, tc := range []struct {
		doc   string
		empty bool
	}{
		{"<h1>a</h1>", false},
		{"<h1></h1>", true},
		{"<h1>   </h1>", true},
		{"<h1><span> </span></h1>", true},
		{"<h1><span>a</span></h1>", false},
		{"<h1><img alt=\"a\"></h1>", true}, // this program reads text, and says so
		{`<h1 aria-label="a"></h1>`, false},
		{`<h1 aria-label="   "></h1>`, true},
	} {
		got := strings.Contains(kinds(check(t, tc.doc)), "empty")
		if got != tc.empty {
			t.Errorf("%q: empty = %v, want %v", tc.doc, got, tc.empty)
		}
	}
}

// TestARIAHeadingsCount, and an aria-level overrides the tag because that is what
// a screen reader does with it.
func TestARIAHeadingsCount(t *testing.T) {
	for _, tc := range []struct {
		doc, want string
		headings  int
	}{
		{`<h1>a</h1><div role="heading" aria-level="2">b</div>`, "", 2},
		{`<h1>a</h1><div role="heading" aria-level="3">b</div>`, "skips", 2},
		// role="heading" with no level is level 2 by ARIA's default.
		{`<h1>a</h1><div role="heading">b</div>`, "", 2},
		{`<div role="heading">b</div>`, "skips,no-h1", 1},
		// An aria-level on an h-tag wins: this is an h2 that is really a level 4.
		{`<h1>a</h1><h2 aria-level="4">b</h2>`, "skips", 2},
		{`<h1>a</h1><h4 aria-level="2">b</h4>`, "", 2},
		// A level that does not parse is not a heading: guessing what a typo meant
		// would be this program inventing a document.
		{`<h1>a</h1><h2 aria-level="two">b</h2>`, "", 1},
		{`<h1>a</h1><div role="heading" aria-level="0">b</div>`, "", 1},
		// A role that is not heading is not a heading.
		{`<h1>a</h1><div role="banner">b</div>`, "", 1},
	} {
		res := check(t, tc.doc)
		if got := kinds(res); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
		if res.Headings != tc.headings {
			t.Errorf("%q: Headings = %d, want %d", tc.doc, res.Headings, tc.headings)
		}
	}
}

// TestWhatIsNotInTheAccessibilityTreeIsNotAHeading.
func TestWhatIsNotInTheAccessibilityTreeIsNotAHeading(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		hidden   int
		findings string
	}{
		{`<h1>a</h1><div hidden><h3>b</h3></div>`, 1, ""},
		{`<h1>a</h1><div aria-hidden="true"><h3>b</h3></div>`, 1, ""},
		{`<h1>a</h1><h3 hidden>b</h3>`, 1, ""},
		// aria-hidden="false" is not hidden, whatever the selector says.
		{`<h1>a</h1><div aria-hidden="false"><h3>b</h3></div>`, 0, "skips"},
		// And the region ends with its element.
		{`<div hidden><h1>a</h1></div><h3>b</h3>`, 1, "skips,no-h1"},
	} {
		res := check(t, tc.doc)
		if res.Hidden != tc.hidden {
			t.Errorf("%q: Hidden = %d, want %d", tc.doc, res.Hidden, tc.hidden)
		}
		if got := kinds(res); got != tc.findings {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.findings)
		}
	}
}

// TestTheLocationsAreWhereAPersonWouldLook. The library reports byte offsets and
// this program converts them, in characters rather than bytes, because a column is
// something a person counts in an editor.
func TestTheLocationsAreWhereAPersonWouldLook(t *testing.T) {
	doc := "<html>\n<body>\n  <h1>a</h1>\n  <h3>b</h3>\n</body>\n</html>"
	res := check(t, doc)
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v", res.Findings)
	}
	f := res.Findings[0]
	if f.Line != 4 || f.Column != 3 {
		t.Errorf("finding at %d:%d, want 4:3", f.Line, f.Column)
	}

	// A column counted in characters, not bytes: the é before it is two bytes.
	doc = "<p>café</p><h3>a</h3>"
	res = check(t, doc)
	if len(res.Findings) < 1 {
		t.Fatal("no findings")
	}
	if got := res.Findings[0]; got.Line != 1 || got.Column != 12 {
		t.Errorf("finding at %d:%d, want 1:12 (the h3 is the twelfth character)",
			got.Line, got.Column)
	}

	// CRLF line endings: the line is the line, and the CR is a byte on it.
	doc = "<h1>a</h1>\r\n<h3>b</h3>"
	res = check(t, doc)
	if got := res.Findings[0]; got.Line != 2 || got.Column != 1 {
		t.Errorf("finding at %d:%d, want 2:1", got.Line, got.Column)
	}

	// A finding on the first line is 1:1.
	res = check(t, "<h3>a</h3>")
	if got := res.Findings[0]; got.Line != 1 || got.Column != 1 {
		t.Errorf("finding at %d:%d, want 1:1", got.Line, got.Column)
	}
}

// TestTheReportIsStableAcrossWritePatterns, which the offsets make free: they are
// absolute, so how the document arrived cannot move a finding.
func TestTheReportIsStableAcrossWritePatterns(t *testing.T) {
	doc := "<html>\n<body>\n<h1>a</h1>\n<h3>b</h3>\n<h2></h2>\n</body>\n</html>"
	want := check(t, doc)
	for _, size := range []int{1, 2, 3, 7, 64} {
		c := &checker{}
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
				t.Errorf("chunks of %d: finding %d is %+v, want %+v",
					size, i, got.Findings[i], want.Findings[i])
			}
		}
	}
}

// TestNothingIsWritten, which is the point of writing to io.Discard: this program
// reports and does not rewrite, so it does not have to care that a text handler
// re-encodes undecodable bytes.
func TestNothingIsWritten(t *testing.T) {
	// The document holds a byte no encoding could decode. A rewrite with a text
	// handler would change it; this program does not produce a document at all.
	doc := "<h1>caf\xe9</h1><h3>b</h3>"
	res := check(t, doc)
	if len(res.Findings) != 1 || res.Findings[0].Kind != Skipped {
		t.Errorf("findings = %v", res.Findings)
	}
	// And the same document through a rewrite that does emit one comes out
	// different, which is what this program is avoiding having to explain.
	out, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if out == doc {
		t.Skip("a text handler no longer re-encodes undecodable bytes; the note in " +
			"this program's documentation can go")
	}
}

// TestAHeadingInsideAHeading is measured rather than assumed: HTML does not allow
// it, the parser closes the first one, and this program counts what it is told.
func TestAHeadingInsideAHeading(t *testing.T) {
	res := check(t, "<h1>a<h2>b</h2>")
	if res.Headings != 2 {
		t.Errorf("Headings = %d, want 2", res.Headings)
	}
	if got := kinds(res); got != "" {
		t.Errorf("findings %q, want none - h1 then h2 skips nothing", got)
	}
}

// TestTheOutlineAlgorithmIsNotUsed, stated as a test because it is a decision:
// a section does not renumber the headings inside it, since no browser or screen
// reader ever implemented that.
func TestTheOutlineAlgorithmIsNotUsed(t *testing.T) {
	// Under the abandoned outline algorithm this would be a well-formed document:
	// each section's h1 is a level of its own. What a screen reader shows is four
	// level-1 headings.
	doc := "<h1>a</h1><section><h1>b</h1></section><section><h1>c</h1><section><h1>d</h1></section></section>"
	res := check(t, doc)
	if !strings.Contains(kinds(res), "many-h1") {
		t.Errorf("findings %q, want the four h1s reported", kinds(res))
	}
	if res.Headings != 4 {
		t.Errorf("Headings = %d, want 4", res.Headings)
	}
}

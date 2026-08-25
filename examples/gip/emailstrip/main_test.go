package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const dirty = `<!doctype html><html><head>` +
	`<!--[if mso]><style>p{color:red}</style><![endif]-->` +
	`<!-- a tracking pixel could hide here -->` +
	`</head><body class="page" id="top">` +
	`<p style="color:red" onclick="x()">text</p>` +
	`<script>var x = 1;</script>` +
	`<form action="/post"><p class="inside">in a form</p><b>bold</b></form>` +
	`<a href="javascript:x()">bad</a><a href="https://example.com/">good</a>` +
	`<a href="mailto:a@b">mail</a><a href="/relative">relative</a>` +
	`<img src="data:image/png;base64,AAA" alt="pixel">` +
	`</body></html>`

// TestWhatSurvivesIsOnTheAllowList - the property over the output: every element and every
// attribute in the result is one the list allows.
func TestWhatSurvivesIsOnTheAllowList(t *testing.T) {
	out, _, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		tag := e.TagName()
		if !AllowedElements[tag] {
			t.Errorf("<%s> survived", tag)
			return nil
		}
		for _, attr := range e.AttributeList() {
			name := strings.ToLower(attr.Name)
			if !allowedAttribute(tag, name) {
				t.Errorf("%s survived on <%s>", name, tag)
			}
			if (name == "href" || name == "src") && !allowedURL(attr.Value) {
				t.Errorf("a %s URL survived on <%s>: %q", scheme(attr.Value), tag, attr.Value)
			}
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
}

// TestTheContentsOfARemovedSubtreeAreNotReportedAsRemovals, which is what Element.IsRemoved is
// for and the reason the report's numbers mean anything.
func TestTheContentsOfARemovedSubtreeAreNotReportedAsRemovals(t *testing.T) {
	doc := `<body><form action="/x"><p class="inside" id="y">text</p><b>bold</b></form><p>kept</p></body>`
	_, report, err := StripString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if got := report.Removed(); got != 1 {
		t.Errorf("reported %d removals, want the form alone:\n%s", got, report)
	}
	if report.InsideRemoved != 2 {
		t.Errorf("counted %d things inside the removed subtree, want the p and the b",
			report.InsideRemoved)
	}
	for _, rem := range report.Sorted() {
		if rem.Kind == "attribute" {
			t.Errorf("an attribute inside a removed element was reported: %+v", rem)
		}
	}
	if report.Kept != 2 {
		t.Errorf("kept %d elements, want the body and the surviving p", report.Kept)
	}
}

// TestTextInsideARemovedSubtreeIsNotCollected. A text chunk cannot tell that an ancestor was
// removed - TextChunk.IsRemoved answers for the chunk - so the depth counter is what makes this
// work, and this is the test that would fail without it.
func TestTextInsideARemovedSubtreeIsNotCollected(t *testing.T) {
	doc := `<body><form><p>hidden text</p></form><p>visible text</p></body>`
	_, report, err := StripString(doc, Options{KeepText: true})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(report.Text, "hidden") {
		t.Errorf("the text of a removed element was collected: %q", report.Text)
	}
	if !strings.Contains(report.Text, "visible text") {
		t.Errorf("the visible text was not collected: %q", report.Text)
	}
}

// TestConditionalCommentsSurviveAndOrdinaryOnesDoNot.
func TestConditionalCommentsSurviveAndOrdinaryOnesDoNot(t *testing.T) {
	out, report, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "[if mso]") {
		t.Errorf("the conditional comment was removed:\n%s", out)
	}
	if strings.Contains(out, "tracking pixel") {
		t.Errorf("an ordinary comment survived:\n%s", out)
	}
	if report.ConditionalComments != 1 {
		t.Errorf("counted %d conditional comments", report.ConditionalComments)
	}

	// The spellings a template actually uses.
	for _, text := range []string{"[if mso]>", "[if (gte mso 9)]>", "<![endif]", "[endif]"} {
		if !isConditional(text) {
			t.Errorf("%q was not recognised as conditional", text)
		}
	}
	for _, text := range []string{" a note ", "if this were code", "[not a condition"} {
		if isConditional(text) {
			t.Errorf("%q was treated as conditional", text)
		}
	}
}

// TestOnlySafeURLSchemesSurvive, including the spellings that try to look safe.
func TestOnlySafeURLSchemesSurvive(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://example.com/", true},
		{"http://example.com/", true},
		{"mailto:a@b", true},
		{"/relative", true},
		{"relative", true},
		{"#fragment", true},
		{"//example.com/protocol-relative", true},
		{"path/with:colon", true},
		{"javascript:alert(1)", false},
		{" javascript:alert(1)", false},
		{"JavaScript:alert(1)", false},
		{"data:image/png;base64,AAA", false},
		{"vbscript:x", false},
		{"file:///etc/passwd", false},
	}

	for _, tt := range tests {
		if got := allowedURL(tt.url); got != tt.ok {
			t.Errorf("allowedURL(%q) = %v, want %v (scheme %q)", tt.url, got, tt.ok, scheme(tt.url))
		}
	}
}

// TestAnEncodedSchemeIsStillTheScheme - the vectors that made the first version of this program
// wrong, and the reason scheme() decodes before it decides.
//
// An attribute value arrives as raw source, so a check on the raw string sees a scheme called
// "&#106;avascript" and lets it through, while a browser decodes first and runs it. The library
// documents this and gives the rule - decide on the decoded form, rewrite the raw one - which is
// what the second half of this test checks: a legitimate URL keeps its entities.
func TestAnEncodedSchemeIsStillTheScheme(t *testing.T) {
	dangerous := []string{
		`javascript:alert(1)`,
		`&#106;avascript:alert(1)`,
		`&#x6a;avascript:alert(1)`,
		`&#0000106;avascript:alert(1)`,
		`&NewLine;javascript:alert(1)`,
		`jav&#x09;ascript:alert(1)`,
		`&Tab;javascript:alert(1)`,
		`JAV&#x09;ASCRIPT:alert(1)`,
		`  javascript:alert(1)`,
		`data:text/html;base64,AAA`,
		`vbscript:x`,
	}
	for _, v := range dangerous {
		out, report, err := StripString(`<body><a href="`+v+`">x</a></body>`, Options{})
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if strings.Contains(out, "href") {
			t.Errorf("%q survived: %s", v, out)
		}
		if report.Removed() == 0 {
			t.Errorf("%q was not reported as removed", v)
		}
	}

	// And the raw form is what is written back for a URL that stays: decoding it here
	// would change the URL, since "&amp;" is one character to a parser and five to a
	// server that never sees the decoding.
	keep := []string{
		`https://example.com/?a=1&amp;b=2`,
		`/relative?a=1&amp;b=2`,
		`mailto:a@b?subject=x&amp;body=y`,
		`#fragment`,
		`//example.com/protocol-relative`,
	}
	for _, v := range keep {
		out, _, err := StripString(`<body><a href="`+v+`">x</a></body>`, Options{})
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if !strings.Contains(out, `href="`+v+`"`) {
			t.Errorf("%q did not survive unchanged: %s", v, out)
		}
	}
}

// TestTheDecoderIsAllowedToBeStricterThanAParser. html.UnescapeString decodes more of an
// attribute value than a browser does, and for a filter that is the safe direction: it can only
// reject a URL a browser would have accepted, never the reverse. This pins the direction rather
// than the particular case, since the case is the standard library's business.
func TestTheDecoderIsAllowedToBeStricterThanAParser(t *testing.T) {
	// "&copy=2" keeps its parameter in a browser and grows a copyright sign in the standard
	// library. Either way this is not a scheme, so the URL survives - what matters is that
	// the disagreement cannot turn a rejection into an acceptance.
	out, _, err := StripString(`<body><a href="/x?a=1&copy=2">l</a></body>`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/x?a=1&copy=2"`) {
		t.Errorf("the URL was changed: %s", out)
	}
}

// TestAStyleAttributeIsCheckedToo, since CSS has had two ways to run code and a style attribute
// is as attacker-chosen as an href.
func TestAStyleAttributeIsCheckedToo(t *testing.T) {
	unsafe := []struct{ value, marker string }{
		{"background:url(javascript:x())", "javascript:"},
		{"background:url(&#106;avascript:x())", "javascript:"},
		{"background:url(JavaScript:x())", "javascript:"},
		{"width:expression(alert(1))", "expression("},
		{"width:expr/**/ession(alert(1))", "expression("},
		{"width:expression (alert(1))", "expression("},
		{"@import url(http://e.com/x.css)", "@import"},
		{"behavior:url(x.htc)", "behavior:"},
		{"-moz-binding:url(x.xml)", "-moz-binding"},
		{"background:url(data:image/png;base64,AAA)", "url(data:"},
	}
	for _, tt := range unsafe {
		out, report, err := StripString(`<body><p style="`+tt.value+`">x</p></body>`, Options{})
		if err != nil {
			t.Fatalf("%q: %v", tt.value, err)
		}
		if strings.Contains(out, "style") {
			t.Errorf("%q survived: %s", tt.value, out)
		}
		var found bool
		for _, rem := range report.Sorted() {
			if rem.Kind == "style" && rem.Name == tt.marker {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was removed but reported as %+v", tt.value, report.Sorted())
		}
	}

	// Ordinary declarations survive, entities and all - the raw value is written back.
	for _, value := range []string{
		"color:red", "background:url(https://e.com/i.png)", "font-family:'Arial'",
		"background:url(/i.png)", "width:100%25",
	} {
		out, _, err := StripString(`<body><p style="`+value+`">x</p></body>`, Options{})
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if !strings.Contains(out, `style="`+value+`"`) {
			t.Errorf("%q did not survive unchanged: %s", value, out)
		}
	}
}

// TestTheMarkerCheckIsNotAPromise. A marker check cannot be complete, and the program says so
// rather than implying otherwise - this test is here to keep that honest by naming a thing it
// does not catch.
func TestTheMarkerCheckIsNotAPromise(t *testing.T) {
	// A CSS variable indirection is not caught, and cannot be by a marker check: the value
	// arrives somewhere else.
	out, _, err := StripString(`<body><p style="--x:javascript">y</p></body>`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// It is caught here only because the marker appears literally. The point of the test is
	// the comment above: this is a filter, not a parser.
	if strings.Contains(out, "style") {
		t.Log("caught by the literal marker, which is luck rather than analysis")
	}
	if allowedStyle("--x:var(--y)") != true {
		t.Error("an indirection was reported as unsafe, which the check cannot know")
	}
}

// TestStrippingTwiceStripsNothing - the idempotence property. The first pass leaves nothing the
// second could object to.
func TestStrippingTwiceStripsNothing(t *testing.T) {
	once, first, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	twice, second, err := StripString(once, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if second.Removed() != 0 {
		t.Errorf("the second pass removed %d things:\n%s", second.Removed(), second)
	}
	if twice != once {
		t.Errorf("the second pass changed the document:\n once:  %s\n twice: %s", once, twice)
	}
	if first.Removed() == 0 {
		t.Error("the first pass removed nothing, so this proves nothing")
	}
}

// TestTheOutputDoesNotDependOnHowTheInputWasChunked - the property over the streaming path.
func TestTheOutputDoesNotDependOnHowTheInputWasChunked(t *testing.T) {
	opts := Options{Note: "sent to you & yours", KeepText: true}

	var whole strings.Builder
	wholeReport, err := Strip(strings.NewReader(dirty), &whole, opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{1, 2, 3, 7, 64, 4096} {
		var got strings.Builder
		report, err := Strip(&chunkedReader{s: dirty, size: size}, &got, opts)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if got.String() != whole.String() {
			t.Errorf("read in %d-byte chunks gave:\n got  %s\n want %s",
				size, got.String(), whole.String())
		}
		if report.Removed() != wholeReport.Removed() || report.Text != wholeReport.Text {
			t.Errorf("read size %d reported %d removals and text %q, want %d and %q",
				size, report.Removed(), report.Text, wholeReport.Removed(), wholeReport.Text)
		}
	}
}

// chunkedReader hands out at most size bytes per Read.
type chunkedReader struct {
	s    string
	size int
	at   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestANoteCanNeverBecomeMarkup - the third property, over the notes worth worrying about.
func TestANoteCanNeverBecomeMarkup(t *testing.T) {
	const doc = `<body><p>x</p></body>`
	want := strings.Join(tagNames(t, doc), ",")

	for _, note := range []string{
		"plain", "you & yours", "<script>alert(1)</script>",
		`</body></html><script>alert(1)</script>`, "a < b", "&amp;", "</p><form>",
	} {
		out, report, err := StripString(doc, Options{Note: note})
		if err != nil {
			t.Fatalf("%q: %v", note, err)
		}
		if got := strings.Join(tagNames(t, out), ","); got != want {
			t.Errorf("%q turned the elements from %s into %s", note, want, got)
		}
		if report.Removed() != 0 {
			t.Errorf("%q caused %d removals", note, report.Removed())
		}
	}
}

// tagNames lists a document's element names, with a rewriter of its own so the program under
// test is not the measuring instrument.
func tagNames(t *testing.T, doc string) []string {
	t.Helper()

	var names []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		names = append(names, e.TagName())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return names
}

// TestTheSummaryCommentIsMarkupAndTheNoteIsNot, which is the two content types side by side.
func TestTheSummaryCommentIsMarkupAndTheNoteIsNot(t *testing.T) {
	out, _, err := StripString(`<html><head></head><body><p>x</p></body></html>`,
		Options{Summary: true, Note: "<b>not bold</b>"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "<!-- emailstrip removed") {
		t.Errorf("the summary comment is not a comment:\n%s", out)
	}
	if strings.Contains(out, "<b>not bold</b>") {
		t.Errorf("the note arrived as markup:\n%s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;not bold&lt;/b&gt;") {
		t.Errorf("the note was not escaped:\n%s", out)
	}
}

// TestTheReportIsStable, since a report that reorders itself between runs is a report nobody can
// diff.
func TestTheReportIsStable(t *testing.T) {
	_, a, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("two runs reported differently:\n%s\n%s", a, b)
	}

	// And the order is by kind, then by count descending.
	sorted := a.Sorted()
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Kind == sorted[i].Kind && sorted[i-1].Count < sorted[i].Count {
			t.Errorf("the removals are not ordered by count within a kind: %+v", sorted)
		}
	}
}

// TestTheDoctypeIsLeftAlone, because replacing it would change the document's mode and that
// decides where a table wrapper lands - B174.
func TestTheDoctypeIsLeftAlone(t *testing.T) {
	out, _, err := StripString(dirty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Errorf("the doctype was changed:\n%s", out[:40])
	}
}

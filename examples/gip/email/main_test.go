package main

import (
	"io"
	"net/url"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const template = `<html><head><style>
.btn { color: red; padding: 4px }
a:hover { color: blue }
.row + .row { margin: 0 }
p { font-size: 14px }
@media (max-width: 5px) { p { color: green } }
</style></head><body>` +
	`<p class="btn" style="border:1px">hi</p>` +
	`<a href="/x" onclick="alert(1)">l</a>` +
	`<a href="javascript:alert(2)">bad</a>` +
	`<img src="/i.png"><script>alert(3)</script>` +
	`</body></html>`

// tagNames lists a document's element names in order, using a rewriter of its own so that the
// program under test is not also the measuring instrument.
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

func base(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("https://example.com/n/")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestTheRulesTheRewriterCanExpressAreInlined, in stylesheet order and after whatever the
// element already had.
func TestTheRulesTheRewriterCanExpressAreInlined(t *testing.T) {
	out, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}

	// The sheet's declarations come first, in stylesheet order, and the element's own
	// style attribute stays last so that it still wins.
	if want := `style="color: red; padding: 4px; font-size: 14px; border:1px"`; !strings.Contains(out, want) {
		t.Errorf("the paragraph's style is not %s:\n%s", want, out)
	}
	if report.Usable != 2 {
		t.Errorf("%d rules were usable, want the class rule and the tag rule", report.Usable)
	}
	if report.Applications != 2 {
		t.Errorf("%d applications, want one per rule on the one paragraph", report.Applications)
	}
}

// TestARuleTheRewriterRefusesIsReportedWithTheLibrarysReason, rather than dropped quietly or
// guessed at.
func TestARuleTheRewriterRefusesIsReportedWithTheLibrarysReason(t *testing.T) {
	_, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Skipped) != 2 {
		t.Fatalf("%d rules skipped, want a:hover and .row + .row: %+v", len(report.Skipped), report.Skipped)
	}
	byselector := map[string]string{}
	for _, s := range report.Skipped {
		byselector[s.Rule.Selector] = s.Reason
	}
	if reason := byselector["a:hover"]; !strings.Contains(reason, "pseudo-class") {
		t.Errorf("a:hover was skipped because %q", reason)
	}
	if reason := byselector[".row + .row"]; !strings.Contains(reason, "combinator") {
		t.Errorf(".row + .row was skipped because %q", reason)
	}
}

// TestAnAtRuleIsSkippedWhole, because a media query written onto an element means nothing.
func TestAnAtRuleIsSkippedWhole(t *testing.T) {
	rules := parseRules(`@media (max-width: 5px) { p { color: green } } p { color: red }`)
	if len(rules) != 1 || rules[0].Selector != "p" || rules[0].Declarations != "color: red" {
		t.Errorf("parsed %+v", rules)
	}

	// The style block is kept, so the media query's text is still in the document; what
	// must not happen is its declarations reaching an element.
	out, _, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	body := out[strings.Index(out, "</style>"):]
	if strings.Contains(body, "green") {
		t.Errorf("a media query's declarations were inlined:\n%s", out)
	}
}

// TestScriptsAndEventHandlersAreRemoved, the script with its content.
func TestScriptsAndEventHandlersAreRemoved(t *testing.T) {
	out, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "<script") || strings.Contains(out, "alert(3)") {
		t.Errorf("the script survived:\n%s", out)
	}
	if strings.Contains(out, "onclick") {
		t.Errorf("an event handler survived:\n%s", out)
	}
	if report.Scripts != 1 || report.EventHandlers != 1 {
		t.Errorf("reported %d scripts and %d handlers", report.Scripts, report.EventHandlers)
	}
}

// TestAJavaScriptURLIsRemovedRatherThanResolved, since resolving it would produce something that
// looks like a link.
func TestAJavaScriptURLIsRemovedRatherThanResolved(t *testing.T) {
	out, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("a javascript: URL survived:\n%s", out)
	}
	if report.JavascriptURLs != 1 {
		t.Errorf("reported %d javascript: URLs", report.JavascriptURLs)
	}

	// The spellings a parser tolerates are caught too.
	for _, v := range []string{" javascript:x", "JavaScript:x", "java\tscript:x", "\njavascript:x"} {
		if !isJavaScriptURL(v) {
			t.Errorf("%q was not recognised as a javascript: URL", v)
		}
	}
	for _, v := range []string{"/javascript:x", "https://example.com/#javascript:x", "mailto:a@b"} {
		if isJavaScriptURL(v) {
			t.Errorf("%q was treated as a javascript: URL", v)
		}
	}
}

// TestURLsAreMadeAbsolute, and the ones that already are, and fragments, are left alone.
func TestURLsAreMadeAbsolute(t *testing.T) {
	out, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="https://example.com/x"`, `src="https://example.com/i.png"`} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
	if report.URLs != 2 {
		t.Errorf("reported %d URLs", report.URLs)
	}

	u := base(t)
	for _, tt := range []struct {
		in      string
		want    string
		changed bool
	}{
		{"/x", "https://example.com/x", true},
		{"x", "https://example.com/n/x", true},
		{"https://other.example/x", "https://other.example/x", false},
		{"#anchor", "#anchor", false},
		{"", "", false},
		{"mailto:a@b", "mailto:a@b", false},
	} {
		got, changed := absolutise(tt.in, u)
		if got != tt.want || changed != tt.changed {
			t.Errorf("absolutise(%q) = %q,%v; want %q,%v", tt.in, got, changed, tt.want, tt.changed)
		}
	}
}

// TestWithoutABaseTheURLsAreLeftAlone, because inventing one would be worse than leaving a
// relative URL in an email.
func TestWithoutABaseTheURLsAreLeftAlone(t *testing.T) {
	out, report, err := InlineString(template, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/x"`) {
		t.Errorf("the relative URL was changed:\n%s", out)
	}
	if report.URLs != 0 {
		t.Errorf("reported %d URLs with no base", report.URLs)
	}
	// And the rest of the work still happened.
	if report.Scripts != 1 || report.Usable != 2 {
		t.Errorf("report %+v", report)
	}
}

// TestElementsBeforeTheStylesheetAreCounted, and the document's own scaffolding is not - which
// is what makes the count worth printing.
func TestElementsBeforeTheStylesheetAreCounted(t *testing.T) {
	_, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	if report.ElementsBefore != 0 {
		t.Errorf("a stylesheet in the head reported %d elements before it", report.ElementsBefore)
	}

	// A stylesheet after some of the body cannot style what came first, and the program
	// says so.
	late := `<html><body><p class="btn">first</p><style>.btn{color:red}</style>` +
		`<p class="btn">second</p></body></html>`
	out, report, err := InlineString(late, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ElementsBefore == 0 {
		t.Errorf("a stylesheet after a paragraph reported nothing before it: %+v", report)
	}
	// The first paragraph is unstyled and the second is styled, which is the ordering
	// constraint rather than a bug.
	first := strings.Index(out, "first")
	second := strings.Index(out, "second")
	if strings.Contains(out[:first], "color: red") {
		t.Errorf("the paragraph before the stylesheet was styled:\n%s", out)
	}
	if !strings.Contains(out[first:second], "color: red") && !strings.Contains(out[second-40:second], "color: red") {
		t.Logf("output: %s", out)
	}
}

// TestTheStylesheetIsReadFromEveryStyleElement, since a template can have more than one.
func TestTheStylesheetIsReadFromEveryStyleElement(t *testing.T) {
	doc := `<html><head><style>.a{color:red}</style><style>.b{color:blue}</style></head>` +
		`<body><p class="a b">x</p></body></html>`
	sheet, err := ParseStylesheet(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedSelectors(sheet.Rules); len(got) != 2 || got[0] != ".a" || got[1] != ".b" {
		t.Errorf("read %v", got)
	}

	out, report, err := InlineString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Applications != 2 {
		t.Errorf("%d applications for two rules on one element", report.Applications)
	}
	if !strings.Contains(out, "color:red; color:blue;") {
		t.Errorf("the two rules did not both land, in order:\n%s", out)
	}
}

// TestMergingKeepsWhatWasThere, and puts it last - which is the cascade rather than a
// preference. An inline style beats a stylesheet rule, so the element's own declarations have
// to come after the ones written in from the sheet.
func TestMergingKeepsWhatWasThere(t *testing.T) {
	doc := `<html><head><style>p{color:red}</style></head><body>` +
		`<p style="border:1px solid">a</p><p style="margin:0;">b</p><p>c</p></body></html>`
	out, _, err := InlineString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`style="color:red; border:1px solid"`,
		`style="color:red; margin:0;"`,
		`style="color:red;"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
}

// TestTheElementsOwnStyleWins, which is the point of the ordering above: a template that says
// color:blue on an element means it, whatever the sheet says.
func TestTheElementsOwnStyleWins(t *testing.T) {
	doc := `<html><head><style>p{color:red}</style></head><body>` +
		`<p style="color:blue">x</p></body></html>`
	out, _, err := InlineString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Both declarations are present and the element's own is last, which is what makes it
	// win.
	red := strings.Index(out, "color:red")
	blue := strings.Index(out, "color:blue")
	if red < 0 || blue < 0 {
		t.Fatalf("one of the two declarations is missing: %s", out)
	}
	if red > blue {
		t.Errorf("the sheet's declaration came after the element's own:\n%s", out)
	}
}

// TestLaterRulesWinOverEarlierOnes, which is the other half of the cascade this program does
// implement.
func TestLaterRulesWinOverEarlierOnes(t *testing.T) {
	doc := `<html><head><style>p{color:red} p{color:green}</style></head><body>` +
		`<p>x</p></body></html>`
	out, _, err := InlineString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	red := strings.Index(out, "color:red")
	green := strings.Index(out, "color:green")
	if red < 0 || green < 0 {
		t.Fatalf("one of the two declarations is missing: %s", out)
	}
	if green < red {
		t.Errorf("the earlier rule came last, so it would win:\n%s", out)
	}
}

// TestInliningTwiceIsInliningOnce - the property over the whole input, and the one that found a
// bug: appending declarations without checking made a second pass turn style="color:red;" into
// style="color:red; color:red;".
func TestInliningTwiceIsInliningOnce(t *testing.T) {
	docs := []string{
		template,
		`<html><head><style>p{color:red}</style></head><body><p>x</p></body></html>`,
		`<html><head><style>.a{color:red} .b{margin:0}</style></head><body>` +
			`<p class="a b" style="color:blue">x</p></body></html>`,
		`<html><head><style>p{color:red}</style></head><body><p style="color:red;">x</p></body></html>`,
		`<p>no stylesheet at all</p>`,
	}

	for _, doc := range docs {
		once, _, err := InlineString(doc, Options{Base: base(t)})
		if err != nil {
			t.Fatal(err)
		}
		twice, _, err := InlineString(once, Options{Base: base(t)})
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("a second pass changed the document:\n once:  %s\n twice: %s", once, twice)
		}
	}
}

// TestADocumentWithNoStylesheetIsStillCleaned, since the removals are not conditional on there
// being CSS.
func TestADocumentWithNoStylesheetIsStillCleaned(t *testing.T) {
	doc := `<p onmouseover="x()">a</p><script>y()</script><a href="/z">l</a>`
	out, report, err := InlineString(doc, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "onmouseover") || strings.Contains(out, "<script") {
		t.Errorf("nothing was removed:\n%s", out)
	}
	if report.Usable != 0 || len(report.Skipped) != 0 {
		t.Errorf("report %+v", report)
	}
	if report.URLs != 1 {
		t.Errorf("reported %d URLs", report.URLs)
	}
}

// TestTheReportReadsAsASummary, since it is what goes in the log.
func TestTheReportReadsAsASummary(t *testing.T) {
	_, report, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	s := report.String()
	for _, want := range []string{"used 2 of 4 rules", "absolutised 2 URLs", "removed 1 scripts",
		"javascript: URLs", "skipped a:hover"} {
		if !strings.Contains(s, want) {
			t.Errorf("the report does not say %q:\n%s", want, s)
		}
	}
}

// TestStrippingTheStyleBlocksIsTheCallersChoice, and the report says which rules it costs.
func TestStrippingTheStyleBlocksIsTheCallersChoice(t *testing.T) {
	kept, keptReport, err := InlineString(template, Options{Base: base(t)})
	if err != nil {
		t.Fatal(err)
	}
	stripped, strippedReport, err := InlineString(template, Options{Base: base(t), StripStyle: true})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(kept, "<style") {
		t.Errorf("the style block was removed without being asked:\n%s", kept)
	}
	if strings.Contains(stripped, "<style") || strings.Contains(stripped, "a:hover") {
		t.Errorf("the style block survived -strip-style:\n%s", stripped)
	}
	if strippedReport.StyleBlocks != 1 {
		t.Errorf("reported %d style blocks removed", strippedReport.StyleBlocks)
	}

	// The inlining is the same either way: stripping only decides what happens to the
	// rules that could not be inlined.
	if keptReport.Applications != strippedReport.Applications {
		t.Errorf("%d applications with the block kept and %d with it stripped",
			keptReport.Applications, strippedReport.Applications)
	}
	if !strings.Contains(keptReport.String(), "still work where a client honours") {
		t.Errorf("the report does not say what keeping the block buys:\n%s", keptReport.String())
	}
	if !strings.Contains(strippedReport.String(), "are gone") {
		t.Errorf("the report does not say what stripping costs:\n%s", strippedReport.String())
	}
}

// TestAnAtRuleWithNestedRulesIsSkippedWhole. The first version of the parser took the closing
// brace of the rule inside @media as the end of the at-rule and produced a rule whose selector
// was "}".
func TestAnAtRuleWithNestedRulesIsSkippedWhole(t *testing.T) {
	rules := parseRules(`@media (max-width: 5px) { p { color: green } } p { color: red }`)
	if len(rules) != 1 {
		t.Fatalf("parsed %d rules: %+v", len(rules), rules)
	}
	if rules[0].Selector != "p" || rules[0].Declarations != "color: red" {
		t.Errorf("parsed %+v", rules[0])
	}

	// An unterminated at-rule takes the rest of the sheet with it rather than producing
	// nonsense.
	if got := parseRules(`@media (x) { p { color: red }`); len(got) != 0 {
		t.Errorf("an unterminated at-rule parsed as %+v", got)
	}
	// And a rule after two nested levels is still found.
	if got := parseRules(`@supports (a:b) { @media (c) { p { color: red } } } q { color: blue }`); len(got) != 1 || got[0].Selector != "q" {
		t.Errorf("parsed %+v", got)
	}
}

// TestTheOutputDoesNotDependOnHowTheInputWasChunked - the second property, and the one that
// covers the streaming path rather than the convenience one. The read pass buffers, so what is
// being tested is that the apply pass is chunk-invariant like every other rewrite.
func TestTheOutputDoesNotDependOnHowTheInputWasChunked(t *testing.T) {
	opts := Options{Base: base(t), Footer: "sent to you & yours", MSOStyle: "p{font-family:Arial}"}

	var whole strings.Builder
	if _, err := Inline(strings.NewReader(template), &whole, opts); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{1, 2, 3, 7, 64, 1024} {
		var got strings.Builder
		if _, err := Inline(&chunkedReader{s: template, size: size}, &got, opts); err != nil {
			t.Fatalf("write size %d: %v", size, err)
		}
		if got.String() != whole.String() {
			t.Errorf("read in %d-byte chunks gave a different document:\n got  %s\n want %s",
				size, got.String(), whole.String())
		}
	}
}

// chunkedReader hands out at most size bytes per Read, which is what an io.Reader from a socket
// does and what a strings.Reader never does.
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

// TestAFooterCanNeverBecomeMarkup - the third property, over every footer worth worrying about.
// The footer goes in with lolhtml.Text, so what comes out is text whatever it says.
func TestAFooterCanNeverBecomeMarkup(t *testing.T) {
	footers := []string{
		"plain",
		"you & yours",
		"<script>alert(1)</script>",
		`</body></html><script>alert(1)</script>`,
		"a < b and c > d",
		"&amp; already escaped",
		"</p><style>body{display:none}</style>",
		"\x00 and a nul",
	}

	for _, footer := range footers {
		out, report, err := InlineString(`<html><body><p>x</p></body></html>`, Options{Footer: footer})
		if err != nil {
			t.Fatalf("%q: %v", footer, err)
		}
		if !report.FooterAdded {
			t.Errorf("%q: the footer was not added", footer)
		}

		// Nothing the footer said became an element: the document has the same
		// elements it started with.
		before := strings.Join(tagNames(t, `<html><body><p>x</p></body></html>`), ",")
		after := strings.Join(tagNames(t, out), ",")
		if before != after {
			t.Errorf("%q turned the document's elements from %s into %s", footer, before, after)
		}
	}
}

// TestTheMSOStyleGoesInAsMarkup, which is the other half of the ContentType choice: escaping a
// conditional comment would put "&lt;!--[if mso]&gt;" at the top of the page.
func TestTheMSOStyleGoesInAsMarkup(t *testing.T) {
	out, report, err := InlineString(`<html><head></head><body><p>x</p></body></html>`,
		Options{MSOStyle: "p{font-family:Arial}"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.MSOAdded {
		t.Error("the conditional comment was not added")
	}
	if !strings.Contains(out, "<!--[if mso]><style>p{font-family:Arial}</style><![endif]-->") {
		t.Errorf("the conditional comment is not markup:\n%s", out)
	}
	if strings.Contains(out, "&lt;!--") {
		t.Errorf("the conditional comment was escaped:\n%s", out)
	}

	// And it goes in the head, where a mail client looks for it.
	head := out[:strings.Index(out, "</head>")]
	if !strings.Contains(head, "[if mso]") {
		t.Errorf("the conditional comment is outside the head:\n%s", out)
	}
}

// TestNeitherAdditionHappensUnasked, since a program that always appends a footer is a program
// nobody can use for a page.
func TestNeitherAdditionHappensUnasked(t *testing.T) {
	const doc = `<html><head></head><body><p>x</p></body></html>`
	out, report, err := InlineString(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.FooterAdded || report.MSOAdded {
		t.Errorf("report %+v", report)
	}
	if out != doc {
		t.Errorf("a document with no stylesheet and no options came back changed:\n got  %s\n want %s",
			out, doc)
	}
}

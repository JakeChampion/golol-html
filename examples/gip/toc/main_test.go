package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<h2>One</h2><h2>Two</h2>`,
	`<h2>One</h2><h3>Under</h3><h2>Two</h2>`,
	`<h2>One</h2><h4>Skipped a level</h4>`,
	`<h2 id="fixed">Has an id</h2>`,
	`<h2>Same</h2><h2>Same</h2><h2>Same</h2>`,
	`<h2>Configure &amp; run</h2>`,
	`<h2><em>Marked</em> up</h2>`,
	`<h2>   spaced   out   </h2>`,
	`<h2></h2><h2>Real</h2>`,
	`<h1>Too shallow</h1><h5>Too deep</h5><h2>Just right</h2>`,
	`<nav id="toc"></nav><h2>One</h2>`,
	`<p>no headings</p>`,
	``,
}

func chunkedOnePass(in string, n int, b *tocBuilder) (string, error) {
	var out bytes.Buffer
	opts := append(b.collect(), lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		if len(b.entries) == 0 {
			return nil
		}
		b.found = true
		return d.Append("\n<nav class=\"toc\">"+renderList(b.entries)+"</nav>\n", lolhtml.HTML)
	}))
	w, err := lolhtml.NewWriter(&out, opts...)
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

// TestChunkInvariance is load-bearing here: heading text is accumulated across
// chunks and finished at the end tag, so a boundary inside a heading is exactly
// what would break it.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := onePassString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 7} {
			got, err := chunkedOnePass(doc, n, &tocBuilder{marker: "#toc", minLevel: 2, maxLevel: 4})
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

// TestHeadingTextIsNotDoubleEscaped: the text arrives as raw source, with
// character references still encoded, so escaping it again on the way into the
// contents turns "&amp;" into "&amp;amp;". An earlier version of this program
// did exactly that.
func TestHeadingTextIsNotDoubleEscaped(t *testing.T) {
	got, _, err := onePassString(`<h2>Configure &amp; run</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Errorf("the heading text was escaped twice: %s", got)
	}
	if !strings.Contains(got, ">Configure &amp; run<") {
		t.Errorf("the heading text did not survive: %s", got)
	}
	// And the slug is made from the decoded form, so it does not contain "amp".
	if !strings.Contains(got, `href="#configure-run"`) {
		t.Errorf("the slug was built from the encoded text: %s", got)
	}
}

// TestListIsWellFormed checks the nesting rather than the exact string: a
// sublist belongs inside the item it hangs off, and a skipped level needs a
// placeholder item to hold it rather than a <ul> directly inside a <ul>.
func TestListIsWellFormed(t *testing.T) {
	for _, doc := range corpus {
		got, b, err := onePassString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if len(b.entries) == 0 {
			continue
		}

		list := got[strings.Index(got, "<nav"):]
		if open, close := strings.Count(list, "<ul>"), strings.Count(list, "</ul>"); open != close {
			t.Errorf("%q: %d <ul> against %d </ul>: %s", doc, open, close, list)
		}
		if open, close := strings.Count(list, "<li>"), strings.Count(list, "</li>"); open != close {
			t.Errorf("%q: %d <li> against %d </li>: %s", doc, open, close, list)
		}
		if strings.Contains(list, "<ul><ul>") {
			t.Errorf("%q: a list directly inside a list: %s", doc, list)
		}
		if strings.Contains(list, "</li><ul>") {
			t.Errorf("%q: a sublist beside its item rather than inside it: %s", doc, list)
		}
	}
}

// TestIDsAreUniqueAndStable: two headings with the same text must not claim the
// same anchor, and a heading that already has an id keeps it.
func TestIDsAreUniqueAndStable(t *testing.T) {
	got, b, err := onePassString(`<h2>Same</h2><h2>Same</h2><h2 id="mine">Same</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 3 {
		t.Fatalf("entries=%d, want 3", len(b.entries))
	}

	seen := map[string]bool{}
	for _, e := range b.entries {
		if seen[e.id] {
			t.Errorf("id %q was used twice", e.id)
		}
		seen[e.id] = true
	}
	if !seen["mine"] {
		t.Errorf("the document's own id was replaced: %v", b.entries)
	}
	if !strings.Contains(got, `href="#same"`) || !strings.Contains(got, `href="#same-2"`) {
		t.Errorf("repeats were not disambiguated: %s", got)
	}
}

// TestLevelRangeIsHonoured: a heading outside the requested range is not in the
// contents at all.
func TestLevelRangeIsHonoured(t *testing.T) {
	doc := `<h1>One</h1><h2>Two</h2><h3>Three</h3><h4>Four</h4><h5>Five</h5>`

	_, b, err := onePassString(doc, func(b *tocBuilder) { b.minLevel, b.maxLevel = 2, 3 })
	if err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 2 {
		t.Fatalf("entries=%d, want 2: %v", len(b.entries), b.entries)
	}
	for _, e := range b.entries {
		if e.level < 2 || e.level > 3 {
			t.Errorf("level %d is outside the requested range", e.level)
		}
	}
}

// TestEmptyHeadingsAreSkipped: a heading with no text has nothing to link.
func TestEmptyHeadingsAreSkipped(t *testing.T) {
	_, b, err := onePassString(`<h2></h2><h2>   </h2><h2>Real</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 1 || b.entries[0].text != "Real" {
		t.Errorf("entries = %v, want just the one with text", b.entries)
	}
}

// TestMarkupInsideAHeadingIsFlattened: the contents entry is text, so nested
// markup contributes its text and nothing else.
func TestMarkupInsideAHeadingIsFlattened(t *testing.T) {
	_, b, err := onePassString(`<h2><em>Marked</em> up</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 1 || b.entries[0].text != "Marked up" {
		t.Errorf("entries = %v, want text \"Marked up\"", b.entries)
	}
}

// TestTwoPassInsertsAtTheMarker is the whole reason this program reads the file
// twice. One pass cannot do it: a streaming insertion at the marker runs before
// the headings after it are parsed.
func TestTwoPassInsertsAtTheMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	doc := `<html><body><nav id="toc"></nav><h2>One</h2><h3>Under</h3><h2>Two</h2></body></html>`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	b := &tocBuilder{marker: "#toc", minLevel: 2, maxLevel: 4}
	var out bytes.Buffer
	if err := b.twoPass(path, &out); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !b.found {
		t.Error("the marker was not found")
	}
	navAt := strings.Index(got, `id="toc"`)
	firstHeadingAt := strings.Index(got, "<h2")
	if navAt < 0 || firstHeadingAt < 0 || navAt > firstHeadingAt {
		t.Fatalf("expected the contents before the first heading: %s", got)
	}
	// The contents must list headings that come after it in the document.
	for _, want := range []string{`href="#one"`, `href="#under"`, `href="#two"`} {
		if !strings.Contains(got[:firstHeadingAt], want) {
			t.Errorf("%s is missing from the contents: %s", want, got[:firstHeadingAt])
		}
	}
	// And the ids in the links exist on the headings.
	for _, want := range []string{`id="one"`, `id="under"`, `id="two"`} {
		if !strings.Contains(got[firstHeadingAt:], want) {
			t.Errorf("%s was not assigned to a heading: %s", want, got[firstHeadingAt:])
		}
	}
}

// TestTwoPassLeavesTheDocumentAloneWhenThereIsNoMarker.
func TestTwoPassLeavesTheDocumentAloneWhenThereIsNoMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	doc := `<html><body><h2 id="one">One</h2></body></html>`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	b := &tocBuilder{marker: "#toc", minLevel: 2, maxLevel: 4}
	var out bytes.Buffer
	if err := b.twoPass(path, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != doc {
		t.Errorf("\n got: %s\nwant: %s", got, doc)
	}
	if b.found {
		t.Error("found is true with no marker in the document")
	}
	if !strings.Contains(b.report(), "no element matched") {
		t.Errorf("the report does not mention the missing marker:\n%s", b.report())
	}
}

func TestOnePassIsIdempotentInItsHeadings(t *testing.T) {
	// The appended nav contains no headings, so a second pass finds the same
	// set and appends an identical block.
	for _, doc := range corpus {
		_, first, err := onePassString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		once, _, err := onePassString(doc)
		if err != nil {
			t.Fatal(err)
		}
		_, second, err := onePassString(once)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.entries) != len(second.entries) {
			t.Errorf("%q: %d headings on the first pass, %d on the second",
				doc, len(first.entries), len(second.entries))
		}
	}
}

// TestAnIdCannotEscapeItsAttribute is a regression: the ids and the text both
// arrive as raw source, and the id goes inside quotes this program chose. A
// single-quoted id in the document may hold a bare double quote, and for a while
// that put a working event handler in the table of contents.
func TestAnIdCannotEscapeItsAttribute(t *testing.T) {
	const in = `<div id="toc"></div>` +
		`<h2 id='a" onmouseover="alert(1)'>Heading</h2>`
	got, _, err := onePassString(in)
	if err != nil {
		t.Fatal(err)
	}

	// The parser decides what an attribute is. Searching the output for
	// "onmouseover=" would fail on output that is correct, because the escaped
	// text legitimately contains it.
	var attrs []string
	if _, err := lolhtml.RewriteString(got,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			for name := range e.Attributes() {
				attrs = append(attrs, name)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 1 || attrs[0] != "href" {
		t.Errorf("the contents link carries %v, want just href: %s", attrs, got)
	}
}

// TestAnIdIsNotDoubleEscaped is the other half of the same rule: an id that
// already holds a character reference must not gain another layer, or the
// fragment stops matching the heading it points at.
func TestAnIdIsNotDoubleEscaped(t *testing.T) {
	got, _, err := onePassString(`<div id="toc"></div><h2 id="a&amp;b">Configure &amp; run</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `href="#a&amp;b"`) {
		t.Errorf("the id was not round-tripped: %s", got)
	}
	if !strings.Contains(got, `>Configure &amp; run</a>`) {
		t.Errorf("the heading text was altered: %s", got)
	}
}

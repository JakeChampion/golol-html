package main

import (
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const book = `<html><body><article class="post" id="main">` +
	`<h1>Book</h1><p>front matter</p>` +
	`<h2>One</h2><p>first chapter</p><p>more of it</p>` +
	`<h2>Two</h2><p>second chapter</p><ul><li>a</li><li>b</li></ul>` +
	`<h2>Three</h2><p>third chapter</p>` +
	`</article></body></html>`

func splitString(t *testing.T, doc string, level, max int) *Splitter {
	t.Helper()
	s, err := Split(strings.NewReader(doc), level, max)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// textOf returns a document's text, which is what has to survive a split.
func textOf(t *testing.T, doc string) string {
	t.Helper()

	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// tagsOf returns a document's tag sequence, which is how a part is checked for being balanced.
func tagsOf(t *testing.T, doc string) []string {
	t.Helper()

	var tags []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		name := e.TagName()
		tags = append(tags, name)
		if !e.CanHaveContent() {
			return nil
		}
		// The name is captured rather than read in the callback: the element is
		// detached by the time its end tag arrives, so e.TagName() there is empty. The
		// first version of this helper read it from the element and reported every
		// part as unbalanced.
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			tags = append(tags, "/"+name)
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return tags
}

// TestNoTextIsLostOrDuplicated - the property. Every character of the document's text is in
// exactly one part, in order.
func TestNoTextIsLostOrDuplicated(t *testing.T) {
	docs := []string{
		book,
		`<p>no headings at all</p>`,
		`<h2>starts with a heading</h2><p>x</p>`,
		`<html><body><h2>a</h2><div><h2>nested in a div</h2><p>y</p></div></body></html>`,
	}

	for _, doc := range docs {
		s := splitString(t, doc, 2, 0)

		var joined strings.Builder
		for _, p := range s.Parts() {
			joined.WriteString(textOf(t, p.Content()))
		}
		if got, want := joined.String(), textOf(t, doc); got != want {
			t.Errorf("%q\n got  %q\n want %q", doc, got, want)
		}
	}
}

// TestEveryPartIsBalanced: what a part reopens, it closes. A part whose <article> is left open is
// not a part.
func TestEveryPartIsBalanced(t *testing.T) {
	s := splitString(t, book, 2, 0)

	for _, p := range s.Parts() {
		open := map[string]int{}
		for _, tag := range tagsOf(t, p.Content()) {
			if strings.HasPrefix(tag, "/") {
				open[tag[1:]]--
				continue
			}
			open[tag]++
		}
		for tag, n := range open {
			if n != 0 {
				t.Errorf("part %d leaves <%s> unbalanced by %d:\n%s",
					p.Index, tag, n, p.Content())
			}
		}
	}
}

// TestTheAncestorsAreReopenedWithTheirAttributes, because a part whose article has lost its class
// styles differently from the page it came out of.
func TestTheAncestorsAreReopenedWithTheirAttributes(t *testing.T) {
	s := splitString(t, book, 2, 0)
	parts := s.Parts()
	if len(parts) < 2 {
		t.Fatalf("%d parts", len(parts))
	}

	for _, p := range parts[1:] {
		if !strings.Contains(p.Content(), `<article class="post" id="main">`) {
			t.Errorf("part %d did not reopen the article with its attributes:\n%s",
				p.Index, p.Content())
		}
		if !strings.HasPrefix(p.Content(), "<html><body><article") {
			t.Errorf("part %d does not begin with the ancestor chain:\n%s", p.Index, p.Content())
		}
	}
}

// TestAReopenedTagIsTheSameTag, which needs the same care as any other write-back: an attribute
// value from AttributeList is raw source, so escaping it again produces a different value.
func TestAReopenedTagIsTheSameTag(t *testing.T) {
	doc := `<html><body><article class="a&amp;b" data-q='say "hi"' id="x">` +
		`<h2>One</h2><p>first</p><h2>Two</h2><p>second</p></article></body></html>`

	s := splitString(t, doc, 2, 0)
	parts := s.Parts()
	if len(parts) != 2 {
		t.Fatalf("%d parts", len(parts))
	}

	// The entity survives as it was written, rather than becoming "&amp;amp;".
	if !strings.Contains(parts[1].Content(), `class="a&amp;b"`) {
		t.Errorf("the reopened class is not the source's:\n%s", parts[1].Content())
	}
	if strings.Contains(parts[1].Content(), "&amp;amp;") {
		t.Errorf("the reopened tag was double-escaped:\n%s", parts[1].Content())
	}

	// And a value containing a double quote is escaped, because the reopened tag writes it
	// inside double quotes.
	if !strings.Contains(parts[1].Content(), `data-q="say &quot;hi&quot;"`) {
		t.Errorf("a value containing a quote was not escaped:\n%s", parts[1].Content())
	}

	// The reopened tag has to parse back to the same attribute *values* the source's did.
	// Not the same spelling: a source value written in single quotes can hold a literal
	// double quote, and there is no way to write that inside double quotes - so the
	// comparison is on the decoded values, which is what a parser and a stylesheet see.
	want := attributesOf(t, doc, "article")
	got := attributesOf(t, parts[1].Content(), "article")
	if len(want) != len(got) {
		t.Fatalf("the reopened article has %d attributes and the source's had %d", len(got), len(want))
	}
	for name, value := range want {
		if stdhtml.UnescapeString(got[name]) != stdhtml.UnescapeString(value) {
			t.Errorf("%s decodes to %q in the reopened tag and %q in the source",
				name, stdhtml.UnescapeString(got[name]), stdhtml.UnescapeString(value))
		}
	}
}

// attributesOf returns an element's attributes as a parser sees them, which is how the test above
// compares a reopened tag with the one it stands in for.
func attributesOf(t *testing.T, doc, tag string) map[string]string {
	t.Helper()

	out := map[string]string{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
		if len(out) > 0 {
			return nil // the first one only
		}
		for _, attr := range e.AttributeList() {
			out[attr.Name] = attr.Value
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEachPartTakesItsTitleFromItsHeading, accumulated rather than read per chunk.
func TestEachPartTakesItsTitleFromItsHeading(t *testing.T) {
	s := splitString(t, book, 2, 0)
	want := []string{"", "One", "Two", "Three"}
	if len(s.Parts()) != len(want) {
		t.Fatalf("%d parts, want %d", len(s.Parts()), len(want))
	}
	for i, p := range s.Parts() {
		if p.Title != want[i] {
			t.Errorf("part %d is titled %q, want %q", p.Index, p.Title, want[i])
		}
	}

	// A heading whose text arrives in pieces still gives a whole title.
	long := strings.Repeat("chapter title words ", 20)
	doc := `<body><h2>` + long + `</h2><p>x</p></body>`
	for _, size := range []int{1, 3, 64} {
		got, err := Split(&chunkedReader{s: doc, size: size}, 2, 0)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		parts := got.Parts()
		if parts[len(parts)-1].Title != strings.TrimSpace(long) {
			t.Errorf("read size %d gave the title %q", size, parts[len(parts)-1].Title)
		}
	}
}

// chunkedReader hands out at most size bytes per Read, and counts the reads so a test can tell
// whether the input really arrived in pieces.
type chunkedReader struct {
	s     string
	size  int
	at    int
	reads int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	r.reads++
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestThePartsDoNotDependOnTheReadSize - the property over the streaming path.
func TestThePartsDoNotDependOnTheReadSize(t *testing.T) {
	whole := splitString(t, book, 2, 0)

	for _, size := range []int{1, 2, 3, 7, 64} {
		reader := &chunkedReader{s: book, size: size}
		got, err := Split(reader, 2, 0)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if want := (len(book) + size - 1) / size; reader.reads < want {
			t.Errorf("read size %d: the reader was read %d times, want at least %d",
				size, reader.reads, want)
		}
		if len(got.Parts()) != len(whole.Parts()) {
			t.Fatalf("read size %d gave %d parts, want %d",
				size, len(got.Parts()), len(whole.Parts()))
		}
		for i, p := range got.Parts() {
			if p.Content() != whole.Parts()[i].Content() {
				t.Errorf("read size %d, part %d:\n got  %q\n want %q",
					size, i+1, p.Content(), whole.Parts()[i].Content())
			}
		}
	}
}

// TestTheBudgetMovesTheCutRatherThanBreakingAnElement: with -max, a part grows past the budget
// until the next heading, because a part that ends inside a tag is not a part.
func TestTheBudgetMovesTheCutRatherThanBreakingAnElement(t *testing.T) {
	// Every chapter is small, so a large budget produces fewer parts than there are
	// headings.
	all := splitString(t, book, 2, 0)
	budgeted := splitString(t, book, 2, 200)

	if len(budgeted.Parts()) >= len(all.Parts()) {
		t.Errorf("a budget of 200 gave %d parts and no budget gave %d",
			len(budgeted.Parts()), len(all.Parts()))
	}
	if len(budgeted.Parts()) == 0 {
		t.Fatal("no parts")
	}
	// Every part but the last reached the budget.
	for _, p := range budgeted.Parts()[:len(budgeted.Parts())-1] {
		if p.Bytes < 200 {
			t.Errorf("part %d is %d bytes, under the budget", p.Index, p.Bytes)
		}
	}
	// And the text still survives.
	var joined strings.Builder
	for _, p := range budgeted.Parts() {
		joined.WriteString(textOf(t, p.Content()))
	}
	if got, want := joined.String(), textOf(t, book); got != want {
		t.Errorf("the budgeted split lost text\n got  %q\n want %q", got, want)
	}
}

// TestADocumentWithNoHeadingsIsOnePart, which is the case a splitter has to get right before any
// of the others matter.
func TestADocumentWithNoHeadingsIsOnePart(t *testing.T) {
	s := splitString(t, `<html><body><p>one</p><p>two</p></body></html>`, 2, 0)
	if len(s.Parts()) != 1 {
		t.Fatalf("%d parts", len(s.Parts()))
	}
	if got := s.Parts()[0].Content(); got != `<html><body><p>one</p><p>two</p></body></html>` {
		t.Errorf("the one part is %q", got)
	}
}

// TestCuttingAtADifferentLevel, since the level is the caller's choice and nothing else should
// change with it.
func TestCuttingAtADifferentLevel(t *testing.T) {
	doc := `<body><h1>A</h1><p>a</p><h1>B</h1><p>b</p><h2>b1</h2><p>c</p></body>`

	atOne := splitString(t, doc, 1, 0)
	atTwo := splitString(t, doc, 2, 0)

	if len(atOne.Parts()) != 2 {
		t.Errorf("cutting at h1 gave %d parts, want 2", len(atOne.Parts()))
	}
	if len(atTwo.Parts()) != 2 {
		t.Errorf("cutting at h2 gave %d parts, want 2", len(atTwo.Parts()))
	}
	// The text survives either way.
	for _, s := range []*Splitter{atOne, atTwo} {
		var joined strings.Builder
		for _, p := range s.Parts() {
			joined.WriteString(textOf(t, p.Content()))
		}
		if got, want := joined.String(), textOf(t, doc); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// TestTagNameReadsWhatThisProgramWrote, since it is applied to tags this program built rather than
// to anything parsed - and a wrong answer there would leave a part unbalanced.
func TestTagNameReadsWhatThisProgramWrote(t *testing.T) {
	tests := map[string]string{
		"<div>":                   "div",
		`<article class="post">`:  "article",
		`<a href="/x" title="y">`: "a",
		"<p\n\tclass=\"multi\">":  "p",
		"":                        "",
		"not a tag":               "",
	}
	for in, want := range tests {
		if got := tagName(in); got != want {
			t.Errorf("tagName(%q) = %q, want %q", in, got, want)
		}
	}
}

package main

import (
	"errors"
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var docA = `<h2 id="intro">One</h2><a href="#intro">up</a>` +
	`<label for="field">Name</label><input id="field">` +
	`<table><td headers="intro summary">x</td></table><p id="summary">s</p>`

var docB = `<h2 id="intro">Two</h2><a href="#intro">up</a>` +
	`<div aria-labelledby="intro summary" id="wrap">w</div><p id="summary">s</p>` +
	`<img src="x" usemap="#m"><map name="m"><area href="#intro"></map>`

var docC = `<h2 id="intro">Three</h2><a href="#nowhere">missing</a>`

func mergeAll(t *testing.T, inputs ...Input) (string, *Merger) {
	t.Helper()

	m := NewMerger()
	var out strings.Builder
	if err := m.Merge(inputs, &out); err != nil {
		t.Fatal(err)
	}
	return out.String(), m
}

// idsOf returns the ids and map names an output defines, in order, using a rewriter of its own so
// the program under test is not the measuring instrument.
func idsOf(t *testing.T, doc string) []string {
	t.Helper()

	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("[id]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("id")
			out = append(out, stdhtml.UnescapeString(v))
			return nil
		}),
		lolhtml.OnElement("map[name]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("name")
			out = append(out, stdhtml.UnescapeString(v))
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	return out
}

// referencesOf returns every id an output points at, from every attribute that names one.
func referencesOf(t *testing.T, doc string) []string {
	t.Helper()

	var out []string
	handlers := []lolhtml.Option{
		lolhtml.OnElement("[href]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("href")
			if strings.HasPrefix(v, "#") {
				out = append(out, stdhtml.UnescapeString(v[1:]))
			}
			return nil
		}),
		lolhtml.OnElement("[usemap]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("usemap")
			if strings.HasPrefix(v, "#") {
				out = append(out, stdhtml.UnescapeString(v[1:]))
			}
			return nil
		}),
	}
	for attr, list := range referenceAttributes {
		attr, list := attr, list
		handlers = append(handlers, lolhtml.OnElement("["+attr+"]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute(attr)
			if v == "" {
				return nil
			}
			if list {
				out = append(out, strings.Fields(stdhtml.UnescapeString(v))...)
				return nil
			}
			out = append(out, stdhtml.UnescapeString(v))
			return nil
		}))
	}
	if _, err := lolhtml.RewriteString(doc, handlers...); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEveryIDInTheOutputIsUnique - the property the program exists for.
func TestEveryIDInTheOutputIsUnique(t *testing.T) {
	out, _ := mergeAll(t,
		Input{"a", docA}, Input{"b", docB}, Input{"c", docC},
		Input{"d", docA}, Input{"e", docB})

	seen := map[string]int{}
	for _, id := range idsOf(t, out) {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%q appears %d times in the merged output", id, n)
		}
	}
	if len(seen) < 10 {
		t.Errorf("only %d ids in the output, so this test is not seeing the documents", len(seen))
	}
}

// TestEveryReferenceStillResolves, except the ones that pointed at nothing to begin with. This is
// the half a program that renames ids usually gets wrong.
func TestEveryReferenceStillResolves(t *testing.T) {
	out, m := mergeAll(t, Input{"a", docA}, Input{"b", docB}, Input{"c", docC})

	defined := map[string]bool{}
	for _, id := range idsOf(t, out) {
		defined[id] = true
	}

	dangling := map[string]bool{}
	for _, d := range m.Documents {
		for _, ref := range d.Dangling {
			// The report's form is "attr=value" or "#value"; the id is the tail.
			if i := strings.LastIndexAny(ref, "=#"); i >= 0 {
				dangling[ref[i+1:]] = true
			}
		}
	}

	for _, ref := range referencesOf(t, out) {
		if defined[ref] {
			continue
		}
		if dangling[ref] {
			continue // it pointed at nothing before the merge too
		}
		t.Errorf("%q is referenced in the output and defined nowhere", ref)
	}
}

// TestAReferenceBeforeItsTargetIsStillRewritten - the case that makes this two passes. A table of
// contents at the top of a page points at headings further down, and the mapping is not known
// when the reference arrives.
func TestAReferenceBeforeItsTargetIsStillRewritten(t *testing.T) {
	first := `<h2 id="chapter">First document's chapter</h2>`
	second := `<nav><a href="#chapter">Go to chapter</a></nav><h2 id="chapter">Second</h2>`

	out, m := mergeAll(t, Input{"first", first}, Input{"second", second})

	if len(m.Documents[1].Renames) != 1 {
		t.Fatalf("the second document's renames are %+v", m.Documents[1].Renames)
	}
	renamed := m.Documents[1].Renames["chapter"]
	if !strings.Contains(out, `href="#`+renamed+`"`) {
		t.Errorf("the reference that came before its target was not rewritten to %q:\n%s",
			renamed, out)
	}
	if strings.Count(out, `href="#chapter"`) != 0 {
		t.Errorf("a reference to the old id survived:\n%s", out)
	}
}

// TestASpaceSeparatedListIsRewrittenEntryByEntry, which is why this is not a search and replace.
func TestASpaceSeparatedListIsRewrittenEntryByEntry(t *testing.T) {
	first := `<p id="intro">i</p><p id="summary">s</p>`
	second := `<p id="intro">i</p><div aria-labelledby="intro summary keep">d</div><p id="keep2">k</p>`

	out, m := mergeAll(t, Input{"first", first}, Input{"second", second})

	renamed := m.Documents[1].Renames["intro"]
	if renamed == "" {
		t.Fatalf("intro was not renamed: %+v", m.Documents[1].Renames)
	}
	// The renamed entry changed, the one defined in the other document did not, and the one
	// that points at nothing is left as it was.
	want := renamed + " summary keep"
	if !strings.Contains(out, `aria-labelledby="`+want+`"`) {
		t.Errorf("the list was not rewritten entry by entry, want %q:\n%s", want, out)
	}
}

// TestMergingOneDocumentChangesNothing, which is the identity case: nothing collides, so nothing
// is renamed and the only difference is the wrapper.
func TestMergingOneDocumentChangesNothing(t *testing.T) {
	out, m := mergeAll(t, Input{"only", docA})

	if len(m.Documents[0].Renames) != 0 {
		t.Errorf("a single document had renames: %+v", m.Documents[0].Renames)
	}
	want := `<section data-source="only">` + docA + `</section>`
	if out != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
}

// TestTheRewriteDoesNotDependOnTheReadSize - the property over the streaming path.
func TestTheRewriteDoesNotDependOnTheReadSize(t *testing.T) {
	// The rename map is decided by the collect pass, so the same map is used at every read
	// size; what is being tested is the rewrite pass.
	m := NewMerger()
	if _, err := m.Collect("a", docA); err != nil {
		t.Fatal(err)
	}
	d, err := m.Collect("b", docB)
	if err != nil {
		t.Fatal(err)
	}

	var whole strings.Builder
	if err := m.Rewrite(d, strings.NewReader(docB), &whole); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{1, 2, 3, 7, 64} {
		var out strings.Builder
		reader := &chunkedReader{s: docB, size: size}
		if err := m.Rewrite(d, reader, &out); err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if want := (len(docB) + size - 1) / size; reader.reads < want {
			t.Errorf("read size %d: the reader was read %d times, want at least %d - the "+
				"rewrite is not streaming", size, reader.reads, want)
		}
		if out.String() != whole.String() {
			t.Errorf("read size %d:\n got  %q\n want %q", size, out.String(), whole.String())
		}
	}
}

// chunkedReader hands out at most size bytes per Read, which is what a socket does and what a
// strings.Reader never does - so a test that does not use one is not testing the streaming path.
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

// TestAnIDSpelledWithACharacterReferenceIsTheSameID, because two ids are the same id when they are
// the same after decoding - and a document can spell one either way.
func TestAnIDSpelledWithACharacterReferenceIsTheSameID(t *testing.T) {
	first := `<p id="a&amp;b">one</p>`
	second := `<p id="a&b">two</p><a href="#a&amp;b">link</a>`

	_, m := mergeAll(t, Input{"first", first}, Input{"second", second})

	if len(m.Documents[1].Renames) != 1 {
		t.Errorf("an id spelled with a reference did not collide with the same id spelled "+
			"literally: %+v", m.Documents[1].Renames)
	}
}

// TestADanglingReferenceIsReportedRatherThanRewritten. A reference to an id no document defines is
// a fact about the input, and inventing a target for it would be worse than saying so.
func TestADanglingReferenceIsReportedRatherThanRewritten(t *testing.T) {
	out, m := mergeAll(t, Input{"c", docC})

	if !strings.Contains(out, `href="#nowhere"`) {
		t.Errorf("the dangling reference was changed: %s", out)
	}
	if len(m.Documents[0].Dangling) == 0 {
		t.Errorf("the dangling reference was not reported: %+v", m.Documents[0])
	}
	if !strings.Contains(m.Report(), "no document defines") {
		t.Errorf("the report does not mention it:\n%s", m.Report())
	}
}

// TestAMapIsMatchedByNameRatherThanID, which is why the name is in the same namespace here.
func TestAMapIsMatchedByNameRatherThanID(t *testing.T) {
	first := `<map name="m"><area href="#x"></map><p id="x">x</p>`
	second := `<img src="i" usemap="#m"><map name="m"><area href="#y"></map><p id="y">y</p>`

	out, m := mergeAll(t, Input{"first", first}, Input{"second", second})

	renamed := m.Documents[1].Renames["m"]
	if renamed == "" {
		t.Fatalf("the second map's name did not collide: %+v", m.Documents[1].Renames)
	}
	if !strings.Contains(out, `usemap="#`+renamed+`"`) {
		t.Errorf("the usemap was not rewritten to %q:\n%s", renamed, out)
	}
	if !strings.Contains(out, `<map name="`+renamed+`">`) {
		t.Errorf("the map's name was not rewritten:\n%s", out)
	}
}

// TestTheReportSaysWhatHappened, since it is what a reader acts on.
func TestTheReportSaysWhatHappened(t *testing.T) {
	_, m := mergeAll(t, Input{"a", docA}, Input{"b", docB}, Input{"c", docC})
	report := m.Report()

	for _, want := range []string{"merged 3 documents", "renamed", "references seen", "#intro -> #intro-"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}

// TestAPartThatDoesNotEndCleanlyIsRefused, which is the check that stops this program being the
// silent corruption the package documentation warns about. A part that ends inside an unfinished
// construct swallows whatever is joined after it, so the wrapper and the next part vanish into
// it - and nothing errors unless something looks.
func TestAPartThatDoesNotEndCleanlyIsRefused(t *testing.T) {
	for _, doc := range []string{
		`<p id="p1">a</p><div class="c`, // inside an attribute value
		`<p id="p1">a</p><div`,          // inside a start tag
		`<p id="p1">a</p></div`,         // inside an end tag
		`<p id="p1">a</p><!-- note`,     // inside a comment
		`<p id="p1">a</p><!`,            // inside a bogus comment
		`<!DOCTYPE`,                     // inside a doctype
		`<p id="p1">a</p><script>var x`, // inside raw text
		`<p id="p1">a</p><style>p{`,
		`<p id="p1">a</p><textarea>x`,
		`<p id="p1">a</p><title>t`,
	} {
		m := NewMerger()
		_, err := m.Collect("part.html", doc)
		if err == nil {
			t.Errorf("%q was accepted", doc)
			continue
		}
		var unfinished *UnfinishedError
		if !errors.As(err, &unfinished) {
			t.Errorf("%q: err = %T (%v), want *UnfinishedError", doc, err, err)
			continue
		}
		if unfinished.Name != "part.html" {
			t.Errorf("%q: the error names %q", doc, unfinished.Name)
		}
		if !strings.Contains(err.Error(), "part.html") {
			t.Errorf("%q: the message does not name the part: %v", doc, err)
		}
	}

	// And the shapes that do end cleanly are accepted, including the ones that look
	// unfinished and are not: an element left open is fine, because more content is simply
	// more content, and a stray end tag absorbs nothing.
	for _, doc := range []string{
		`<p id="p1">a</p>`,
		`<p id="p1">a`,
		`<ul><li id="i1">a<li id="i2">b`,
		`</div>`,
		`<p id="p1">a &lt; b</p>`,
		`<!DOCTYPE html><p id="p1">a</p>`,
		`<script>var x = "<b>"</script><p id="p1">a</p>`,
		`text`,
		``,
	} {
		m := NewMerger()
		if _, err := m.Collect("part.html", doc); err != nil {
			t.Errorf("%q: %v", doc, err)
		}
	}
}

// TestMergeRefusesBeforeItWritesAnything, since a partial merge on standard output is worse than
// no merge: the caller cannot tell how much of it to trust.
func TestMergeRefusesBeforeItWritesAnything(t *testing.T) {
	var out strings.Builder
	m := NewMerger()
	err := m.Merge([]Input{
		{Name: "good.html", Doc: `<p id="p1">a</p>`},
		{Name: "bad.html", Doc: `<p id="p2">b</p><div class="c`},
	}, &out)
	if err == nil {
		t.Fatal("the merge succeeded")
	}
	var unfinished *UnfinishedError
	if !errors.As(err, &unfinished) {
		t.Errorf("err = %T (%v)", err, err)
	}
	if unfinished != nil && unfinished.Name != "bad.html" {
		t.Errorf("the error names %q", unfinished.Name)
	}
	if out.Len() != 0 {
		t.Errorf("%d bytes were written before the refusal: %q", out.Len(), out.String())
	}
}

// TestWhatTheCheckPrevents measures the corruption itself, so the check is known to be worth its
// selector rather than assumed to be. It joins the two parts the way Merge would and asks a
// parser what it finds.
func TestWhatTheCheckPrevents(t *testing.T) {
	const truncated = `<p id="p1">a</p><div class="c`
	const second = `<p id="p2">b</p>`

	joined := `<section data-source="a.html">` + truncated + `</section>` +
		`<section data-source="b.html">` + second + `</section>`

	var found []string
	if _, err := lolhtml.RewriteString(joined,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			found = append(found, e.TagName())
			return nil
		})); err != nil {
		t.Fatal(err)
	}

	// Six elements were meant: two sections, two paragraphs, the div, and nothing else.
	// What a parser finds is four, because the unfinished attribute ate the rest.
	if len(found) != 4 {
		t.Errorf("a parser found %v, and the corruption this check prevents is that it "+
			"finds four elements where six were meant", found)
	}
	if n := strings.Count(joined, "<section"); n != 2 {
		t.Fatalf("the test document has %d sections", n)
	}
	sections := 0
	for _, name := range found {
		if name == "section" {
			sections++
		}
	}
	if sections != 1 {
		t.Errorf("a parser found %d sections in a document that spells two", sections)
	}

	// And the second part's wrapper became an attribute of the div, which is the part that
	// makes it silent: the output is a well-formed document that says something else.
	var divAttrs []string
	if _, err := lolhtml.RewriteString(joined,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				divAttrs = append(divAttrs, a.Name)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(divAttrs) < 2 {
		t.Errorf("the div has attributes %v, and the point is that it absorbed the "+
			"wrapper's", divAttrs)
	}
}

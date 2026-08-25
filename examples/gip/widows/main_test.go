package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const nb = "\u00a0"

func run(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Join(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Join(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheLastGapIsTheOneThatMoves. The gap is chosen at the end tag, which is
// the whole reason the heading is buffered.
func TestTheLastGapIsTheOneThatMoves(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<h1>The quick brown fox</h1>", "<h1>The quick brown" + nb + "fox</h1>"},
		{"<h2>One two three</h2>", "<h2>One two" + nb + "three</h2>"},
		{"<h6>a b c d e</h6>", "<h6>a b c d" + nb + "e</h6>"},
		// Trailing whitespace is not a gap: there is no word after it.
		{"<h1>a b c   </h1>", "<h1>a b" + nb + "c   </h1>"},
		// The gap can be more than one character, and all of it has to go.
		{"<h1>a b c \n  d</h1>", "<h1>a b c" + nb + nb + nb + nb + "d</h1>"},
		// Not a heading.
		{"<p>The quick brown fox</p>", "<p>The quick brown fox</p>"},
	} {
		got, _ := run(t, tc.in)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestAGapBeforeMarkupIsStillAGap, because markup inside a heading is inline: a
// browser breaks the line at the space whether or not there is a tag after it.
// This is the case a single-pass program cannot reach - the space has been
// written out by the time the <em> arrives.
func TestAGapBeforeMarkupIsStillAGap(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<h2>A long <em>title</em></h2>", "<h2>A long" + nb + "<em>title</em></h2>"},
		{"<h2>A <b>very</b> long title</h2>", "<h2>A <b>very</b> long" + nb + "title</h2>"},
		// The last word starts before the tag and finishes after it, so it is one
		// word and the gap is the one before it.
		{"<h1>one two th<i>ree</i></h1>", "<h1>one two" + nb + "th<i>ree</i></h1>"},
		// Whitespace on both sides of a tag is all one gap.
		{"<h1>a b c <em> d</em></h1>", "<h1>a b c" + nb + "<em>" + nb + "d</em></h1>"},
	} {
		got, _ := run(t, tc.in)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestShortHeadingsAreLeftAlone. A two-word heading that wraps puts one word on
// each line, which is not a widow, and joining it can only make it overflow.
func TestShortHeadingsAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		"<h1>Two words</h1>",
		"<h1>Word</h1>",
		"<h1>   </h1>",
		"<h1></h1>",
		"<h1><em>one</em> two</h1>",
	} {
		got, res := run(t, in)
		if got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
		if res.Joined != 0 {
			t.Errorf("%q was counted as joined", in)
		}
	}
}

// TestRawTextInsideAHeadingIsLeftAlone. This program rebuilds markup, and an
// element whose content is not markup cannot be rebuilt that way: unwrapping it
// turns its text into elements. lolhtml.IsRawText is how the program knows,
// without keeping its own copy of the list.
func TestRawTextInsideAHeadingIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<h1>Some long heading <script>if (a<b) x()</script></h1>`,
		`<h1>Some long heading <style>a{content:"<b>"}</style></h1>`,
		`<h1>Some long heading <noembed><img src=x onerror=alert(1)></noembed></h1>`,
		`<h1>Some long heading <textarea>a<b</textarea></h1>`,
	} {
		got, res := run(t, in)
		if got != in {
			t.Errorf("%q\n got %q\nwant it unchanged", in, got)
		}
		if res.RawText != 1 {
			t.Errorf("%q: RawText = %d, want 1", in, res.RawText)
		}
		// The content is still not markup after the round trip, which is the
		// property the guard exists for.
		elements := 0
		if _, err := lolhtml.RewriteString(got,
			lolhtml.OnElement("img,b", func(*lolhtml.Element) error { elements++; return nil })); err != nil {
			t.Fatal(err)
		}
		if elements != 0 {
			t.Errorf("%q: the raw text became %d elements", in, elements)
		}
	}
}

// TestTheBufferIsBounded. A document does not get to choose how much memory this
// program uses: past MaxBuffer the heading is written out as it came in.
func TestTheBufferIsBounded(t *testing.T) {
	long := "<h1>" + strings.Repeat("word ", MaxBuffer/5+10) + "last</h1>"
	got, res := run(t, long)
	if got != long {
		t.Errorf("an overlong heading was rewritten:\n got %q...\nwant it unchanged", got[:min(80, len(got))])
	}
	if res.TooLong != 1 || res.Joined != 0 {
		t.Errorf("TooLong = %d, Joined = %d, want 1 and 0", res.TooLong, res.Joined)
	}
	// One byte under the limit still works, so the limit is the limit and not a
	// coincidence somewhere else.
	short := "<h1>" + strings.Repeat("word ", 100) + "one two last</h1>"
	if got, res := run(t, short); res.Joined != 1 || !strings.Contains(got, "two"+nb+"last") {
		t.Errorf("a heading inside the limit was not joined: Joined = %d", res.Joined)
	}
}

// TestAnElementLeftOpenByTheDocumentTakesTheHeadingsEndTagWithIt. The tags of an
// inner element are taken away and written back from the buffer, and in
// "<h1>a <em>b</h1>" the em's end tag *is* the </h1> - so removing the em removes
// the heading's closing tag. Written back, the heading closes where it did.
func TestAnElementLeftOpenByTheDocumentIsClosed(t *testing.T) {
	got, res := run(t, "<h1>one two <em>three</h1><p>after</p>")
	// The </em> is not added: it was never in the document, so it was never
	// removed. The </h1> was, and is written back.
	want := "<h1>one two" + nb + "<em>three</h1><p>after</p>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Joined != 1 {
		t.Errorf("Joined = %d, want 1", res.Joined)
	}
}

// TestAHeadingInsideAHeading is measured rather than assumed: the parser closes
// the first heading and this program does not see a document end tag for it, so
// the outer heading is abandoned with its content intact and the inner one is
// processed.
func TestAHeadingInsideAHeading(t *testing.T) {
	got, res := run(t, "<h1>one two three<h2>four five six</h2>")
	if !strings.Contains(got, "one two three") {
		t.Errorf("the outer heading lost its content: %q", got)
	}
	if !strings.Contains(got, "five"+nb+"six") {
		t.Errorf("the inner heading was not joined: %q", got)
	}
	if res.Headings != 2 {
		t.Errorf("Headings = %d, want 2", res.Headings)
	}
}

// TestAttributesAndCommentsSurvive. Rebuilding markup makes this program the
// serialiser, so what it writes has to read back the same.
func TestAttributesAndCommentsSurvive(t *testing.T) {
	const doc = `<h2>A <a href="/x?a=1&amp;b=2" title='say "hi"' data-flag>link right here</a></h2>`
	got, _ := run(t, doc)
	if !strings.Contains(got, "right"+nb+"here") {
		t.Fatalf("not joined: %q", got)
	}
	attrs := map[string]string{}
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		for name, value := range e.Attributes() {
			attrs[name] = value
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	// The values are the document's spelling, because that is how they are
	// reported. The one change is the quote inside the single-quoted attribute:
	// this program writes double-quoted attributes, so a bare " in a value would
	// end one.
	want := map[string]string{"href": "/x?a=1&amp;b=2", "title": "say &quot;hi&quot;", "data-flag": ""}
	if len(attrs) != len(want) {
		t.Fatalf("attributes = %v, want %v", attrs, want)
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("attribute %q = %q, want %q", k, attrs[k], v)
		}
	}

	got, _ = run(t, "<h1>one <!-- a note --> two three</h1>")
	if !strings.Contains(got, "<!-- a note -->") {
		t.Errorf("the comment did not survive: %q", got)
	}
	if !strings.Contains(got, "two"+nb+"three") {
		t.Errorf("not joined: %q", got)
	}
}

// TestReferencesSurvive. Text is reported as the document spells it - references
// are not decoded - so writing it back verbatim is what round-trips, and
// escaping it would double every ampersand on the page.
func TestReferencesSurvive(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<h1>a &amp; b c</h1>", "<h1>a &amp; b" + nb + "c</h1>"},
		{"<h1>a &lt;b&gt; c d</h1>", "<h1>a &lt;b&gt; c" + nb + "d</h1>"},
		{"<h1>say &quot;a b c&quot;</h1>", "<h1>say &quot;a b" + nb + "c&quot;</h1>"},
		// A reference is characters, not a character, and none of them is a break
		// opportunity - so &nbsp; between the last two words means this has
		// already been done, by hand.
		{"<h1>a b c&nbsp;d</h1>", "<h1>a b c&nbsp;d</h1>"},
		{"<h1>a b c&#160;d</h1>", "<h1>a b c&#160;d</h1>"},
	} {
		if got, _ := run(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestRunningItTwiceChangesNothing. The second run has to find the heading
// already joined rather than joining the gap before it, which is why a
// non-breaking space counts as part of the word it is in.
func TestRunningItTwiceChangesNothing(t *testing.T) {
	for _, in := range []string{
		"<h1>The quick brown fox</h1>",
		"<h2>A long <em>title</em></h2>",
		"<h1>one two <em>three</h1>",
		"<h1>a &amp; b c</h1>",
		"<h1>Some long heading <script>if (a<b) x()</script></h1>",
	} {
		once, _ := run(t, in)
		twice, res := run(t, once)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", in, once, twice)
		}
		if res.Joined != 0 && !strings.Contains(in, "script") {
			t.Errorf("%q was joined again on the second run", in)
		}
	}
}

// TestTheTextIsUnchangedApartFromTheGap. Everything this program writes is a
// space becoming a different space, so reading the text back with the
// non-breaking spaces turned into ordinary ones has to give the input's text.
func TestTheTextIsUnchangedApartFromTheGap(t *testing.T) {
	const doc = `<html><body><h1>The quick brown fox</h1><p>jumps over</p>
		<h2>A <b>bold</b> and long title</h2><h3>Two words</h3>
		<h4>one two <em>three</h4><h1>x <!--c--> y z</h1></body></html>`
	got, _ := run(t, doc)
	if text(t, got) != text(t, doc) {
		t.Errorf("the text changed:\n got %q\nwant %q",
			text(t, got), text(t, doc))
	}
	if got == doc {
		t.Error("nothing changed at all, so this proves nothing")
	}
}

func text(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(b.String(), nb, " ")
}

// TestChunkInvariance. A text node arrives in as many pieces as the writes it
// was fed in, and the heading's buffer is the thing that must not care.
func TestChunkInvariance(t *testing.T) {
	const doc = `<h1>The quick brown fox</h1><h2>A <em>long</em> title here</h2>` +
		`<h3>one two <em>three</h3><h1>a &amp; b c</h1><h1>x <script>a<b</script></h1>`
	want, _ := run(t, doc)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		j := &joiner{}
		w, err := lolhtml.NewWriter(&out,
			lolhtml.OnElement("*", j.element),
			lolhtml.OnDocumentText(j.text),
			lolhtml.OnDocumentComment(j.comment))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// TestAHeadingTheDocumentNeverClosesIsNotLost. Nothing closes it, so no end tag
// callback runs: OnDocumentEnd is the only place left to write the buffer.
func TestAHeadingTheDocumentNeverClosesIsNotLost(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<h1>one two three", "<h1>one two" + nb + "three"},
		{"<h1>one two <em>three", "<h1>one two" + nb + "<em>three"},
		{"<h1>one two", "<h1>one two"},
	} {
		got, res := run(t, tc.in)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Headings != 1 {
			t.Errorf("%q: Headings = %d, want 1", tc.in, res.Headings)
		}
	}
}

// TestEveryHeadingIsCountedOnce, so the report adds up and a heading cannot fall
// between two outcomes.
func TestEveryHeadingIsCountedOnce(t *testing.T) {
	doc := "<h1>The quick brown fox</h1>" + // joined
		"<h2>Two words</h2>" + // too few
		"<h3>a b" + nb + "c</h3>" + // already
		"<h4>x y <script>a</script></h4>" + // raw text
		"<h5>" + strings.Repeat("word ", MaxBuffer/5+10) + "z</h5>" // too long
	_, res := run(t, doc)
	sum := res.Joined + res.AlreadyJoined + res.TooFewWords + res.RawText + res.TooLong
	if res.Headings != 5 || sum != 5 {
		t.Errorf("%v: %d headings, %d outcomes, want 5 and 5", res, res.Headings, sum)
	}
	if res.Joined != 1 || res.AlreadyJoined != 1 || res.TooFewWords != 1 || res.RawText != 1 || res.TooLong != 1 {
		t.Errorf("%v: want one of each", res)
	}
}

// TestWhyTheEndTagHasToBeWrittenBack is the measurement behind that: it is not
// this program's arithmetic, it is what the library does. Unwrapping an element
// whose end tag is implied removes the token that closed it, which belongs to an
// enclosing element - so a document that leaves an <em> open loses its </h1>, and
// everything after the heading is inside it.
func TestWhyTheEndTagHasToBeWrittenBack(t *testing.T) {
	const doc = "<h1>a <em>b</h1><p>after</p>"
	var name string
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("em", func(e *lolhtml.Element) error {
		if err := e.OnEndTag(func(t *lolhtml.EndTag) error { name = t.Name(); return nil }); err != nil {
			return err
		}
		e.RemoveAndKeepContent()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if name != "h1" {
		t.Errorf("the em's end tag was reported as %q, want %q", name, "h1")
	}
	if strings.Contains(out, "</h1>") {
		t.Errorf("the </h1> survived: %q - then this program's write-back is wrong", out)
	}
	// And that is a change of meaning, not of bytes: the paragraph is now inside
	// the heading.
	if out != "<h1>a b<p>after</p>" {
		t.Errorf("got %q, want %q", out, "<h1>a b<p>after</p>")
	}
}

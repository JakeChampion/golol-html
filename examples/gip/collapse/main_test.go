package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func run(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Collapse(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Collapse(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestARunBecomesOneSpace, whatever the run is made of.
func TestARunBecomesOneSpace(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>a  b</p>", "<p>a b</p>"},
		{"<p>a \t\n\r\f b</p>", "<p>a b</p>"},
		{"<p>a\nb</p>", "<p>a b</p>"},
		{"<p>a b</p>", "<p>a b</p>"},
		{"<div>\n  <p>x</p>\n</div>", "<div> <p>x</p> </div>"},
		// A space is never deleted, only shortened: whether the one that is left
		// shows depends on CSS this program cannot see.
		{"<p>a</p>   <p>b</p>", "<p>a</p> <p>b</p>"},
		{"   <p>a</p>", " <p>a</p>"},
	} {
		if got, _ := run(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestARunSplitByMarkupIsStillARun. A tag is not a character, so the state has to
// outlive the text node - there is no selector for "the text on both sides of an
// inline tag".
func TestARunSplitByMarkupIsStillARun(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>a  <b>  b</b></p>", "<p>a <b>b</b></p>"},
		{"<p>a <b> </b> b</p>", "<p>a <b></b>b</p>"},
		{"<p>a\n<b>\nb\n</b>\nc</p>", "<p>a <b>b </b>c</p>"},
		{"<p>a <br> <br> b</p>", "<p>a <br><br>b</p>"},
	} {
		if got, _ := run(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestWhitespaceStaysWhereItIsSignificant. pre and textarea render it; the
// elements whose content is not markup hold something that is not prose, and
// reflowing a script or a selector is a change of meaning.
func TestWhitespaceStaysWhereItIsSignificant(t *testing.T) {
	for _, in := range []string{
		"<pre>  a\n   b  </pre>",
		"<listing>  a\n  b</listing>",
		"<textarea>  a\n  b  </textarea>",
		"<script>if (a  <  b)  x()</script>",
		"<style>p  {  color : red  }</style>",
		"<xmp>  a  b  </xmp>",
		"<iframe>  a  b  </iframe>",
		"<title>  a  b  </title>",
		// Nested markup inside a pre does not turn the whitespace back on.
		"<pre>a  <b>  b  </b>  c</pre>",
		// A plaintext runs to the end of the input, so nothing after it is prose.
		"<plaintext>  a  b  </plaintext>  c  d",
	} {
		got, res := run(t, in)
		if got != in {
			t.Errorf("%q\n got %q\nwant it unchanged", in, got)
		}
		if res.Regions == 0 {
			t.Errorf("%q: no verbatim region was counted", in)
		}
		if res.Runs != 0 {
			t.Errorf("%q: %d runs were collapsed", in, res.Runs)
		}
	}
}

// TestProseAroundAVerbatimRegionIsStillCollapsed, which is the whole reason the
// depth counter exists rather than a flag.
func TestProseAroundAVerbatimRegionIsStillCollapsed(t *testing.T) {
	got, res := run(t, "<p>a  b</p>\n\n<pre>  keep  me  </pre>\n\n<p>c  d</p>")
	const want = "<p>a b</p> <pre>  keep  me  </pre> <p>c d</p>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Regions != 1 {
		t.Errorf("Regions = %d, want 1", res.Regions)
	}
	// Nesting: a pre inside a pre must not turn collapsing back on when the inner
	// one closes.
	if got, _ := run(t, "<pre>a  <pre>b  c</pre>  d</pre>e  f"); got != "<pre>a  <pre>b  c</pre>  d</pre>e f" {
		t.Errorf("nested pre: got %q", got)
	}
}

// TestAPreClosedByASiblingIsOverpaid pins the cheap version of "when did the
// region end". The pre is closed by the second <li> and the callback arrives at
// </ul>, so the second item is treated as preformatted too. Late, in the
// direction that leaves whitespace alone, and counted so a caller can see it.
func TestAPreClosedByASiblingIsOverpaid(t *testing.T) {
	const doc = "<ul><li><pre>a  b<li>c  d</ul><p>e  f</p>"
	got, res := run(t, doc)
	const want = "<ul><li><pre>a  b<li>c  d</ul><p>e f</p>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.LateRegions != 1 {
		t.Errorf("LateRegions = %d, want 1 - the caller cannot see it otherwise", res.LateRegions)
	}
	// And the ancestor case, which is not late at all: the pre ends exactly where
	// the </div> is.
	if got, _ := run(t, "<div><pre>a  b</div>c  d"); got != "<div><pre>a  b</div>c d" {
		t.Errorf("closed by an ancestor: got %q", got)
	}
}

// TestReferencesAreNotWhitespace. Text is reported as the document spells it, so
// a reference for a space is characters, not whitespace, and collapsing it would
// mean decoding text this program has to write back verbatim.
func TestReferencesAreNotWhitespace(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>a&#32;&#32;b</p>", "<p>a&#32;&#32;b</p>"},
		{"<p>a&nbsp;&nbsp;b</p>", "<p>a&nbsp;&nbsp;b</p>"},
		// The ampersands survive: writing the chunk back as text would escape
		// them again.
		{"<p>a  &amp;  b</p>", "<p>a &amp; b</p>"},
	} {
		if got, _ := run(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestAttributesAndCommentsAreNotText.
func TestAttributesAndCommentsAreNotText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<a title="a  b"  href="/x">c  d</a>`, `<a title="a  b"  href="/x">c d</a>`},
		// A comment is not a character either: it renders as nothing, so the
		// whitespace on both sides of it is one run and the space after it goes.
		// Its own whitespace is left alone - a comment can be a licence banner or
		// a conditional, and neither is prose.
		{"<p>a  <!-- b  c -->  d</p>", "<p>a <!-- b  c -->d</p>"},
	} {
		if got, _ := run(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestTheWordsAreUnchanged. Everything this program does is delete whitespace, so
// the text with its whitespace normalised has to be the same before and after.
func TestTheWordsAreUnchanged(t *testing.T) {
	const doc = `<html><body>
		<h1>A   heading</h1>
		<p>Some   text with <b>  bold  </b> in it.</p>
		<pre>  kept
   exactly  </pre>
		<script>var a =  1</script>
		<p>&amp;  more   text</p>
	</body></html>`
	got, res := run(t, doc)
	if words(t, got) != words(t, doc) {
		t.Errorf("the words changed:\n got %q\nwant %q", words(t, got), words(t, doc))
	}
	if res.BytesOut >= res.BytesIn {
		t.Errorf("%v: nothing was saved, so this proves nothing", res)
	}
}

func words(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// TestRunningItTwiceChangesNothing: a run of one space is already what it should
// be, so the second pass has nothing to do.
func TestRunningItTwiceChangesNothing(t *testing.T) {
	for _, in := range []string{
		"<p>a  b</p>",
		"<div>\n <p>a  <b>  b</b></p>\n <pre>  c  </pre>\n</div>",
		"<ul><li><pre>a  b<li>c  d</ul>",
		"<p>a&#32;&#32;b</p>",
	} {
		once, _ := run(t, in)
		twice, res := run(t, once)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", in, once, twice)
		}
		if res.Runs != 0 {
			t.Errorf("%q: the second pass collapsed %d runs", in, res.Runs)
		}
	}
}

// TestChunkInvariance. A run can be split across two writes, which is the other
// reason the state is a field rather than a local.
func TestChunkInvariance(t *testing.T) {
	const doc = "<div>\n  <p>a  <b>  b</b>   c</p>\n  <pre>  keep  </pre>\n  " +
		"<script>a  =  1</script>\n  <p>d\t\te</p>\n</div>"
	want, _ := run(t, doc)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		c := &collapser{}
		w, err := lolhtml.NewWriter(&out, c.options()...)
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

// TestTheTagsNeverChange. The only edits are to text, so a tag-by-tag reading of
// the output has to match the input exactly - including the tags inside a
// verbatim region, which are still tags.
func TestTheTagsNeverChange(t *testing.T) {
	const doc = "<div>\n <p>a  b</p>\n <pre>c  <b>d</b>  e</pre>\n <img src=x>\n</div>"
	got, _ := run(t, doc)
	if tags(t, got) != tags(t, doc) {
		t.Errorf("\n got %q\nwant %q", tags(t, got), tags(t, doc))
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
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			b.WriteString("</" + t.Name() + ">")
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

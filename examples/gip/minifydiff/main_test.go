package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Corpus is the documents the pass has to survive: the ordinary shapes and the
// ones where a minifier's assumptions are wrong.
var Corpus = []string{
	"<html><body><p>a   b</p></body></html>",
	"<div>\n  <p>a</p>\n  <p>b</p>\n</div>",
	"<p>a  <b>  b</b>   c</p>",
	"<pre>  keep\n   me  </pre>",
	"<textarea>\n  a  b\n</textarea>",
	"<script>if (a  <  b)  x()</script>",
	"<style>p  {  color : red  }</style>",
	"<!-- a note --><p>a</p><!--another-->",
	"<?php echo 1; ?><p>a</p>",
	"<!bogus><p>a</p>",
	"<![CDATA[x]]><p>a</p>",
	"<!--[if IE]>a<![endif]--><p>b</p>",
	"<!--! licence --><p>a</p>",
	"<!DOCTYPE html><html><body>  a  </body></html>",
	// Shapes where end tags are missing, which is where a rewrite that edits tags
	// gets into trouble. This one edits text, and has to stay out of that.
	"<ul><li>a  b<li>c  d</ul>",
	"<h1>a  <em>b</h1><p>c  d</p>",
	"<p>a<!--c-->b</p>",
	"<a title='say  \"hi\"'  href=\"/x?a=1&amp;b=2\">  link  </a>",
	"<svg><![CDATA[x]]><text>  a  </text></svg>",
	"<table>  <tr><td>  a  </td></tr>  </table>",
	"<p>a&#32;&#32;b</p>",
	`<svg viewBox="0 0 1 1" preserveAspectRatio="none">  a  </svg>`,
	"<plaintext>  a  b  ",
	"",
	"   ",
	"<p>",
}

func minify(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Run(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Run(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheEditsAreTheEditsClaimed.
func TestTheEditsAreTheEditsClaimed(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>a   b</p>", "<p>a b</p>"},
		{"<div>\n <p>a</p>\n</div>", "<div> <p>a</p> </div>"},
		{"<p>a  <b>  b</b></p>", "<p>a <b>b</b></p>"},
		{"<pre>  a  </pre>", "<pre>  a  </pre>"},
		{"<script>a  =  1</script>", "<script>a  =  1</script>"},
		{"<!-- note --><p>a</p>", "<p>a</p>"},
		{"<p>a<!--c-->b</p>", "<p>ab</p>"},
	} {
		got, res := minify(t, tc.in, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Rejected {
			t.Errorf("%q was rejected: %s", tc.in, res.Difference)
		}
	}
}

// TestOnlyCommentsSpelledAsCommentsAreRemoved. The delimiters are gone by the
// time the handler sees the token, and the text does not say which syntax it was:
// the arithmetic does.
func TestOnlyCommentsSpelledAsCommentsAreRemoved(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		removed  int
	}{
		{"<!--a--><p>x</p>", "<p>x</p>", 1},
		{"<!--a--!><p>x</p>", "<!--a--!><p>x</p>", 0}, // 8 bytes of delimiters
		{"<?php echo 1; ?><p>x</p>", "<?php echo 1; ?><p>x</p>", 0},
		{"<!bogus><p>x</p>", "<!bogus><p>x</p>", 0},
		{"<![CDATA[x]]><p>x</p>", "<![CDATA[x]]><p>x</p>", 0},
		{"<!--[if IE]>a<![endif]--><p>x</p>", "<!--[if IE]>a<![endif]--><p>x</p>", 0},
		{"<!--! licence --><p>x</p>", "<!--! licence --><p>x</p>", 0},
		// The text of a comment can look exactly like a processing instruction,
		// which is why the text is not the test.
		{"<!--?php echo 1; ?--><p>x</p>", "<p>x</p>", 1},
	} {
		got, res := minify(t, tc.in, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Comments != tc.removed {
			t.Errorf("%q: removed %d comments, want %d", tc.in, res.Comments, tc.removed)
		}
	}
}

// TestTheCheckerCatchesEachKindOfHarm. The checker is the reason to trust the
// minifier, so it needs its own evidence: each of these is an output a broken
// minifier could produce, handed to the checker directly.
func TestTheCheckerCatchesEachKindOfHarm(t *testing.T) {
	for _, tc := range []struct {
		what, in, out string
	}{
		{"whitespace inside a pre", "<pre>  a  </pre>", "<pre> a </pre>"},
		{"whitespace inside a script", "<script>a  =  1</script>", "<script>a = 1</script>"},
		{"whitespace inside a textarea", "<textarea>  a  </textarea>", "<textarea> a </textarea>"},
		{"a dropped tag", "<p>a</p><p>b</p>", "<p>a</p>b"},
		{"a changed attribute", `<a href="/x">a</a>`, `<a href="/y">a</a>`},
		{"a dropped attribute", `<a href="/x" rel="me">a</a>`, `<a href="/x">a</a>`},
		{"a lost character", "<p>abc</p>", "<p>ab</p>"},
		{"a lost word", "<p>a b</p>", "<p>a</p>"},
		{"a processing instruction removed", "<?php a ?><p>x</p>", "<p>x</p>"},
		{"a CDATA section removed", "<![CDATA[x]]><p>y</p>", "<p>y</p>"},
		{"a bogus comment removed", "<!bogus><p>x</p>", "<p>x</p>"},
		{"half a conditional comment removed", "<!--[if !IE]><!--><p>a</p><!--<![endif]-->",
			"<!--[if !IE]><p>a</p><!--<![endif]-->"},
		{"a comment invented", "<p>a</p>", "<p>a</p><!--x-->"},
		{"a space deleted rather than shortened", "<p>a  b</p>", "<p>ab</p>"},
		{"a doctype dropped", "<!DOCTYPE html><p>a</p>", "<p>a</p>"},
		// In SVG the case is part of the attribute name, so a minifier that
		// lower-cased one has changed the document even though an HTML parser
		// would not care.
		{"an SVG attribute lower-cased", `<svg viewBox="0 0 1 1"></svg>`,
			`<svg viewbox="0 0 1 1"></svg>`},
	} {
		before, err := Project(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		after, err := Project(tc.out)
		if err != nil {
			t.Fatal(err)
		}
		if diff := Diff(before, after); diff == "" {
			t.Errorf("%s: %q -> %q was not caught", tc.what, tc.in, tc.out)
		}
	}
}

// TestTheCheckerAllowsWhatItShould, which matters as much: a checker that
// rejects everything is not a checker.
func TestTheCheckerAllowsWhatItShould(t *testing.T) {
	for _, tc := range []struct {
		what, in, out string
	}{
		{"a collapsed run", "<p>a   b</p>", "<p>a b</p>"},
		{"a newline collapsed", "<div>\n<p>a</p>\n</div>", "<div> <p>a</p> </div>"},
		{"whitespace between tags", "<div>   <p>a</p>   </div>", "<div><p>a</p></div>"},
		{"a comment removed", "<!--a--><p>x</p>", "<p>x</p>"},
		{"a comment removed between text", "<p>a<!--c-->b</p>", "<p>ab</p>"},
		{"attributes reordered", `<a href="/x" rel="me">a</a>`, `<a rel="me" href="/x">a</a>`},
		{"a run split by a tag", "<p>a  <b>  b</b></p>", "<p>a <b>b</b></p>"},
	} {
		before, err := Project(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		after, err := Project(tc.out)
		if err != nil {
			t.Fatal(err)
		}
		if diff := Diff(before, after); diff != "" {
			t.Errorf("%s: %q -> %q was rejected: %s", tc.what, tc.in, tc.out, diff)
		}
	}
}

// TestUnsafeModeIsRejectedAndTheInputIsWritten. The point of the checker is what
// happens when the minifier is wrong, so there has to be a way to be wrong.
func TestUnsafeModeIsRejectedAndTheInputIsWritten(t *testing.T) {
	for _, in := range []string{
		"<pre>  a  </pre>",
		"<script>a  =  1</script>",
		"<?php echo 1; ?><p>a</p>",
		"<![CDATA[x]]><p>a</p>",
	} {
		got, res := minify(t, in, Options{CollapseEverywhere: true, StripEveryCommentToken: true})
		if !res.Rejected {
			t.Errorf("%q was not rejected", in)
		}
		if got != in {
			t.Errorf("%q: wrote %q, want the input unchanged", in, got)
		}
		if res.BytesOut != res.BytesIn {
			t.Errorf("%q: reported %d bytes out for %d in", in, res.BytesOut, res.BytesIn)
		}
		if res.Difference == "" {
			t.Errorf("%q: rejected with no reason given", in)
		}
	}
}

// TestTheCorpusIsMinifiedAndVerified, and never grows.
func TestTheCorpusIsMinifiedAndVerified(t *testing.T) {
	for _, in := range Corpus {
		got, res := minify(t, in, Options{})
		if res.Rejected {
			t.Errorf("%q was rejected: %s", in, res.Difference)
			continue
		}
		if len(got) > len(in) {
			t.Errorf("%q grew to %q", in, got)
		}
		// Minifying the output again changes nothing: a run of one space is
		// already one space, and the comments that are left are the ones being
		// kept on purpose.
		again, res2 := minify(t, got, Options{})
		if again != got {
			t.Errorf("%q\n once %q\ntwice %q", in, got, again)
		}
		if res2.Comments != 0 {
			t.Errorf("%q: the second pass removed %d more comments", in, res2.Comments)
		}
	}
}

// TestTheCheckIsNotVacuous. Every corpus document has to give the checker
// something to compare, or a passing check means nothing - and a mutation of each
// output has to be caught.
func TestTheCheckIsNotVacuous(t *testing.T) {
	for _, in := range Corpus {
		if strings.TrimSpace(in) == "" {
			continue
		}
		got, res := minify(t, in, Options{})
		if res.Tokens == 0 {
			t.Errorf("%q: the checker compared nothing", in)
		}
		// Deleting the last character of the output is a change a parser can see
		// in every one of these documents.
		if len(got) == 0 {
			continue
		}
		before, err := Project(in)
		if err != nil {
			t.Fatal(err)
		}
		after, err := Project(got[:len(got)-1])
		if err != nil {
			t.Fatal(err)
		}
		if Diff(before, after) == "" {
			t.Errorf("%q: truncating the output to %q was not caught", in, got[:len(got)-1])
		}
	}
}

// TestMinifyingIsChunkInvariant. The edits are a streaming pass, so a run split
// across two writes has to come out the same as a run that was not.
func TestMinifyingIsChunkInvariant(t *testing.T) {
	const doc = "<div>\n  <p>a  <b>  b</b>   c</p>\n  <!-- note -->\n  <pre>  d  </pre>\n" +
		"  <?php echo 1; ?>\n  <p>e\t\tf</p>\n</div>"
	want, _, err := Minify(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		m := &minifier{}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out,
			lolhtml.OnElement("*", m.element),
			lolhtml.OnDocumentText(m.text),
			lolhtml.OnDocumentComment(m.comment))
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

// TestTheProjectionIgnoresWhatItSaysItIgnores, stated as tests rather than as a
// comment: two documents that differ only in insignificant whitespace project the
// same, and one that differs inside a pre does not.
func TestTheProjectionIgnoresWhatItSaysItIgnores(t *testing.T) {
	same := []string{
		"<p>a b</p>",
		"<p>a  b</p>",
		"<p>a\n\tb</p>",
		"<p>\n a  b \n</p>",
	}
	first, err := Project(same[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range same[1:] {
		got, err := Project(doc)
		if err != nil {
			t.Fatal(err)
		}
		if diff := Diff(first, got); diff != "" {
			t.Errorf("%q and %q project differently: %s", same[0], doc, diff)
		}
	}
	a, err := Project("<pre>a b</pre>")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Project("<pre>a  b</pre>")
	if err != nil {
		t.Fatal(err)
	}
	if Diff(a, b) == "" {
		t.Error("two pres differing in whitespace projected the same")
	}
}

package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestAnOrdinaryDocumentIsTheSameInBothModes, which is the baseline: strict mode is not
// stricter about anything else.
func TestAnOrdinaryDocumentIsTheSameInBothModes(t *testing.T) {
	for _, doc := range []string{
		`<p>text</p><img src="a.png"><script>var a=1</script>`,
		`<ul><li>a<li>b</ul>`,
		`<select><option>a</option></select>`,
		`<table><tr><td>x</table>`,
		`<div><!--c--><span>y</span></div>`,
		``,
	} {
		res := Compare([]byte(doc), true)
		if res.Ambiguous {
			t.Errorf("%q: strict mode refused it", doc)
		}
		if res.Strict.Err != nil || res.Permissive.Err != nil {
			t.Errorf("%q: strict=%v permissive=%v", doc, res.Strict.Err, res.Permissive.Err)
		}
		if strings.Join(res.Strict.Elements, " ") != strings.Join(res.Permissive.Elements, " ") {
			t.Errorf("%q: the two passes saw %v and %v", doc, res.Strict.Elements, res.Permissive.Elements)
		}
		if len(res.OnlySeenBy) != 0 {
			t.Errorf("%q: only the permissive pass saw %v", doc, res.OnlySeenBy)
		}
		if res.Refused != nil {
			t.Errorf("%q: the refusing pass failed: %v", doc, res.Refused)
		}
		if !res.OK() {
			t.Errorf("%q: %v", doc, res)
		}
	}
}

// TestTheAmbiguousShapesAreTheDocumentedEight, in a select and in a frameset, which is
// what the comparison exists to show.
func TestTheAmbiguousShapesAreTheDocumentedEight(t *testing.T) {
	inSelect := []string{"title", "style", "iframe", "xmp", "plaintext", "noembed", "noframes", "noscript"}
	for _, tag := range inSelect {
		doc := "<select><" + tag + ">x"
		res := Compare([]byte(doc), false)
		if !res.Ambiguous {
			t.Errorf("<%s> inside a select was accepted by strict mode", tag)
		}
		if res.Permissive.Err != nil {
			t.Errorf("<%s>: permissive mode failed: %v", tag, res.Permissive.Err)
		}
	}
	// A frameset has the same list bar noframes, which is allowed there.
	for _, tag := range inSelect {
		doc := "<frameset><" + tag + ">x"
		res := Compare([]byte(doc), false)
		if tag == "noframes" {
			if res.Ambiguous {
				t.Errorf("<noframes> inside a frameset was refused, and it is allowed there")
			}
			continue
		}
		if !res.Ambiguous {
			t.Errorf("<%s> inside a frameset was accepted by strict mode", tag)
		}
	}
	// And nothing else does it: an ordinary nesting of the same tags is fine.
	for _, tag := range inSelect {
		doc := "<div><" + tag + ">x"
		if res := Compare([]byte(doc), false); res.Ambiguous {
			t.Errorf("<%s> inside a div was refused", tag)
		}
	}
}

// TestPermissiveModeIsNotSilence: the difference the two passes report is the point of
// the program.
func TestPermissiveModeIsNotSilence(t *testing.T) {
	const doc = `<p>ok</p><select><xmp><script>alert(1)</script></xmp></select><p>after</p><img src="a.png">`
	res := Compare([]byte(doc), true)

	if !res.Ambiguous {
		t.Fatal("strict mode accepted the document")
	}
	// The strict pass stopped early, so the permissive pass saw elements it never
	// reached - including the img after the region.
	if !contains(res.OnlySeenBy, "img") {
		t.Errorf("only the permissive pass saw %v, want the img among them", res.OnlySeenBy)
	}
	// The ambiguous element itself is an element to the permissive pass.
	if !contains(res.Permissive.Elements, "xmp") {
		t.Errorf("the permissive pass saw %v, want the xmp", res.Permissive.Elements)
	}
	// And the script's markup arrived as text, which is the signal to look for.
	sus := res.Permissive.Suspicious()
	if len(sus) != 1 {
		t.Fatalf("%d suspicious runs, want 1: %v", len(sus), res.Permissive.TextRuns)
	}
	if !strings.Contains(sus[0].Text, "<script>alert(1)</script>") {
		t.Errorf("the run holds %q", sus[0].Text)
	}
	// The offsets point at the region in the document.
	if got := doc[sus[0].Start:sus[0].End]; got != sus[0].Text {
		t.Errorf("the run claims bytes %d-%d, where the document has %q",
			sus[0].Start, sus[0].End, got)
	}
	// The refusing pass stops on it.
	var refusal ErrMarkupAsText
	if !errors.As(res.Refused, &refusal) {
		t.Errorf("the refusing pass returned %v, want ErrMarkupAsText", res.Refused)
	}
	if refusal.Start != sus[0].Start {
		t.Errorf("the refusal is at byte %d and the run at %d", refusal.Start, sus[0].Start)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestARunIsCheckedWholeRatherThanPerChunk, which is the mistake this program would
// otherwise make: the tokenizer splits a text node around a "<" that does not begin a
// tag, so "<script" is never in one chunk.
func TestARunIsCheckedWholeRatherThanPerChunk(t *testing.T) {
	const doc = `<select><xmp><script>alert(1)</script>`
	var chunks []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.WithStrict(false),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if s := c.Text(); s != "" {
				chunks = append(chunks, s)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("the region arrived as %d chunks; this test is about the split", len(chunks))
	}
	for _, c := range chunks {
		if strings.Contains(strings.ToLower(c), "<script") {
			t.Fatalf("one chunk held %q, so the accumulation is not what makes this work", c)
		}
	}
	// Whole, it is found.
	res := Compare([]byte(doc), true)
	if len(res.Permissive.Suspicious()) != 1 {
		t.Errorf("%d suspicious runs, want 1", len(res.Permissive.Suspicious()))
	}
}

// TestTheStrictPassOutputStopsWhereItFailed, which is the cost of strict mode: what had
// already been written is a truncated document.
func TestTheStrictPassOutputStopsWhereItFailed(t *testing.T) {
	const doc = `<p>ok</p><select><xmp>x</xmp></select><p>after</p>`
	res := Compare([]byte(doc), false)
	if !res.Ambiguous {
		t.Fatal("strict mode accepted the document")
	}
	if res.Strict.Output == 0 {
		t.Error("the strict pass wrote nothing; the truncation is the thing to show")
	}
	if res.Strict.Output >= len(doc) {
		t.Errorf("the strict pass wrote %d of %d bytes, want a prefix", res.Strict.Output, len(doc))
	}
	if res.Permissive.Output != len(doc) {
		t.Errorf("the permissive pass wrote %d of %d bytes", res.Permissive.Output, len(doc))
	}
}

// TestTheReportSaysWhichModeSawWhat, so a caller can act on it without reading the code.
func TestTheReportSaysWhichModeSawWhat(t *testing.T) {
	res := Compare([]byte(`<select><xmp><img src=x onerror=alert(1)></xmp></select>`), true)
	s := res.String()
	for _, want := range []string{"strict:", "permissive:", "markup as text", "onerror"} {
		if !strings.Contains(s, want) {
			t.Errorf("the report is missing %q:\n%s", want, s)
		}
	}
}

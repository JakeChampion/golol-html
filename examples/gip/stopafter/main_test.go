package main

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func rewrite(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(doc, "STOP", &out, mark())
	if err != nil {
		t.Fatalf("Rewrite(%.60q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheOutputIsThePrefixPlusTheRestOfTheInput, for every kind of marker. This is the property the
// whole design rests on: the prefix is what a fresh rewriter produces from that much input, so the
// untouched input resumes at a known offset and joining the two is exact.
func TestTheOutputIsThePrefixPlusTheRestOfTheInput(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		kind Kind
	}{
		{"a comment", `<a href="/a">1</a><!-- STOP --><a href="/b">2</a>`, Comment},
		{"an element", `<a href="/a">1</a><div data-STOP><a href="/b">2</a></div>`, Element},
		{"text", `<p>before STOP here</p><a href="/b">2</a>`, Text},
		{"no marker", `<a href="/a">1</a><a href="/b">2</a>`, NotFound},
	} {
		out, res := rewrite(t, tt.doc)
		if res.Kind != tt.kind {
			t.Errorf("%s: stopped at %v, want %v", tt.name, res.Kind, tt.kind)
			continue
		}
		if tt.kind == NotFound {
			continue
		}
		// Everything from the resume offset is the input, byte for byte.
		if !strings.HasSuffix(out, tt.doc[res.ResumeAt:]) {
			t.Errorf("%s: the tail is not the input:\n  out:  %q\n  tail: %q",
				tt.name, out, tt.doc[res.ResumeAt:])
		}
		// And the two halves account for the whole output, with nothing repeated.
		if res.Rewritten+res.Copied != len(out) {
			t.Errorf("%s: %d rewritten + %d copied is not %d bytes",
				tt.name, res.Rewritten, res.Copied, len(out))
		}
		// The part after the marker was not rewritten, which is the point.
		tail := tt.doc[res.ResumeAt:]
		if strings.Contains(tail, `href="/b"`) && strings.Contains(out[res.Rewritten:], `rel=`) {
			t.Errorf("%s: the tail was rewritten: %q", tt.name, out[res.Rewritten:])
		}
	}
}

// TestATextMarkerResumesAtTheEndOfTheNode, which is the row that does not follow the pattern: the
// earlier chunks of the node have already been written, so resuming at its start emits them twice.
func TestATextMarkerResumesAtTheEndOfTheNode(t *testing.T) {
	const doc = `<p>before STOP here</p><p>after</p>`
	out, res := rewrite(t, doc)
	if res.Kind != Text {
		t.Fatalf("stopped at %v", res.Kind)
	}
	// The prefix holds the whole text node, so the resume point is its end.
	if res.ResumeAt != len(`<p>before STOP here`) {
		t.Errorf("resumed at %d, want %d", res.ResumeAt, len(`<p>before STOP here`))
	}
	if out != doc {
		t.Errorf("the output is not the input:\n  got  %q\n  want %q", out, doc)
	}
	if strings.Count(out, "before STOP here") != 1 {
		t.Errorf("the text appears %d times: %q",
			strings.Count(out, "before STOP here"), out)
	}

	// What resuming at the node's start would do, so the choice is measured rather than
	// asserted. The prefix is the same; only the offset differs.
	start := strings.Index(doc, "before")
	wrong := out[:res.Rewritten] + doc[start:]
	if strings.Count(wrong, "before STOP here") != 2 {
		t.Errorf("resuming at the node's start did not duplicate the text, so this case "+
			"does not show why the end is the right offset: %q", wrong)
	}

	// The other three kinds do resume at their own start, which is what makes text the
	// exception rather than the rule.
	for _, tt := range []struct {
		doc    string
		marker string
	}{
		{`<p>a</p><!-- STOP --><p>b</p>`, `<!-- STOP -->`},
		{`<p>a</p><div data-STOP>b</div>`, `<div data-STOP>`},
	} {
		_, res := rewrite(t, tt.doc)
		if want := strings.Index(tt.doc, tt.marker); res.ResumeAt != want {
			t.Errorf("%q: resumed at %d, want %d", tt.doc, res.ResumeAt, want)
		}
	}
}

// TestAMarkerInsideRawTextIsNotAMarker, for all four raw-text elements and both paths. The comment
// path gets this for free; the text path has to be told, because a document-level text handler is
// handed raw text like any other text.
func TestAMarkerInsideRawTextIsNotAMarker(t *testing.T) {
	for _, tag := range []string{"script", "style", "textarea", "title"} {
		// As a comment's bytes inside the element.
		doc := fmt.Sprintf(`<a href="/a">1</a><%s><!-- STOP --></%s><a href="/b">2</a>`,
			tag, tag)
		out, res := rewrite(t, doc)
		if res.Kind != NotFound {
			t.Errorf("%s: a comment inside it stopped the rewrite at %v", tag, res.Kind)
		}
		if res.InRawText != 1 {
			t.Errorf("%s: %d raw-text sightings", tag, res.InRawText)
		}
		// Both links were rewritten, because nothing stopped.
		if n := strings.Count(out, `rel="noopener"`); n != 2 {
			t.Errorf("%s: %d links rewritten, want 2: %s", tag, n, out)
		}

		// And as a bare word, which is the path that needed telling.
		doc = fmt.Sprintf(`<a href="/a">1</a><%s>var m = "STOP";</%s><a href="/b">2</a>`,
			tag, tag)
		out, res = rewrite(t, doc)
		if res.Kind != NotFound {
			t.Errorf("%s: a bare marker inside it stopped the rewrite at %v",
				tag, res.Kind)
		}
		if res.InRawText != 1 {
			t.Errorf("%s: %d raw-text sightings for a bare marker", tag, res.InRawText)
		}
		if n := strings.Count(out, `rel="noopener"`); n != 2 {
			t.Errorf("%s: %d links rewritten for a bare marker, want 2: %s", tag, n, out)
		}
		if !strings.Contains(res.String(), "rather than a marker") {
			t.Errorf("%s: the report does not mention it:\n%s", tag, res)
		}
	}

	// The document-level text handler really is handed raw text, which is why the exclusion
	// is needed rather than incidental.
	seen := 0
	if _, err := lolhtml.RewriteString(`<script>var m = "STOP";</script>`,
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if strings.Contains(c.Text(), "STOP") {
				seen++
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Error("a document-level text handler did not see script text, so the exclusion " +
			"this program needs is not needed")
	}
}

// TestTheFirstMarkerWins, since a document can hold several and the rewrite stops once.
func TestTheFirstMarkerWins(t *testing.T) {
	out, res := rewrite(t, `<a href="/a">1</a><!-- STOP --><div data-STOP>x</div><!-- STOP -->`)
	if res.Kind != Comment {
		t.Errorf("stopped at %v, want the first comment", res.Kind)
	}
	if n := strings.Count(out, `rel="noopener"`); n != 1 {
		t.Errorf("%d links rewritten, want 1: %s", n, out)
	}
	// Everything after the first marker is verbatim, including the later markers.
	if strings.Count(out, "STOP") != 3 {
		t.Errorf("%d markers survived: %s", strings.Count(out, "STOP"), out)
	}
}

// TestAnEmptyMarkerIsRefused, and a document with nothing in it is not a special case.
func TestAnEmptyMarkerIsRefused(t *testing.T) {
	var out strings.Builder
	if _, err := Rewrite(`<p>x</p>`, "", &out, mark()); err == nil {
		t.Error("an empty marker was accepted")
	}
	if out.Len() != 0 {
		t.Errorf("%d bytes were written", out.Len())
	}

	for _, doc := range []string{``, `just text`, `<!-- a comment -->`} {
		got, res := rewrite(t, doc)
		if got != doc {
			t.Errorf("%q became %q", doc, got)
		}
		if res.Kind != NotFound {
			t.Errorf("%q stopped at %v", doc, res.Kind)
		}
	}
}

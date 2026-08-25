package main

import (
	"strings"
	"testing"
)

// twowaysDoc is a page with something for both rewriters: anchors to transform and count,
// comments, text, and one insecure URL for the audit to find. The anchors are written with
// their attributes on separate lines, which is what makes the size change visible.
const twowaysDoc = "<p>intro</p>\n" +
	"<a\n    href=\"/one\"\n    class=\"link\">one</a>\n" +
	"<a\n    href=\"http://example.com/two\"\n    class=\"link\">two</a>\n" +
	"<!-- a comment -->\n" +
	"<img src=\"http://example.com/i.png\">\n" +
	"<p>outro</p>\n"

// TestTheTwoRunsAgree is the property that makes running them concurrently a decision about
// time rather than about answers.
func TestTheTwoRunsAgree(t *testing.T) {
	for _, write := range []int{0, 4096, 64, 1} {
		concurrent, err := Concurrent([]byte(twowaysDoc), write)
		if err != nil {
			t.Fatalf("write size %d: %v", write, err)
		}
		sequential, err := Sequential([]byte(twowaysDoc), write)
		if err != nil {
			t.Fatalf("write size %d: %v", write, err)
		}
		if !Agree(concurrent, sequential) {
			t.Errorf("write size %d: concurrent %d bytes/%d rewritten/%v against "+
				"sequential %d/%d/%v", write,
				len(concurrent.Output), concurrent.Rewritten, concurrent.Audit,
				len(sequential.Output), sequential.Rewritten, sequential.Audit)
		}
	}
}

// TestTheAuditSeesTheInputNotTheTransformsOutput, which is the point of running them side by
// side rather than chaining them: the transform adds rel="noopener", and the audit's count of
// links is a fact about the page as it arrived.
func TestTheAuditSeesTheInputNotTheTransformsOutput(t *testing.T) {
	res, err := Concurrent([]byte(twowaysDoc), 0)
	if err != nil {
		t.Fatal(err)
	}

	if res.Audit.Links != 3 {
		t.Errorf("the audit counted %d links, want the two anchors and the image", res.Audit.Links)
	}
	if res.Audit.Insecure != 2 {
		t.Errorf("the audit found %d insecure URLs, want 2", res.Audit.Insecure)
	}
	if res.Audit.Comments != 1 {
		t.Errorf("the audit counted %d comments, want 1", res.Audit.Comments)
	}
	if res.Rewritten != 2 {
		t.Errorf("the transform rewrote %d anchors, want 2", res.Rewritten)
	}
	if strings.Count(res.Output, `rel="noopener"`) != 2 {
		t.Errorf("the output has %d rel attributes:\n%s",
			strings.Count(res.Output, `rel="noopener"`), res.Output)
	}
}

// TestTheTransformShrinksThePage, which is what this program surfaced: the edit adds fifteen
// bytes per anchor and the output is smaller, because mutating a start tag re-serialises it and
// the separators between attributes are regenerated.
func TestTheTransformShrinksThePage(t *testing.T) {
	res, err := Concurrent([]byte(twowaysDoc), 0)
	if err != nil {
		t.Fatal(err)
	}

	added := res.Rewritten * len(` rel="noopener"`)
	if len(res.Output) >= len(twowaysDoc)+added {
		t.Errorf("the output is %d bytes, the input %d, the additions %d: nothing was "+
			"reformatted away, which this document was written to show",
			len(res.Output), len(twowaysDoc), added)
	}
	if !strings.Contains(res.Output, `<a href="/one" class="link" rel="noopener">`) {
		t.Errorf("the first anchor came back as something else:\n%s", res.Output)
	}
	// The untouched paragraphs keep their bytes.
	if !strings.Contains(res.Output, "<p>intro</p>") || !strings.Contains(res.Output, "<p>outro</p>") {
		t.Errorf("an untouched element was changed:\n%s", res.Output)
	}
}

// TestTheInputCanBeSharedBetweenRewriters. The two rewriters read the same backing array at the
// same time, which is safe because Write reads the slice and does not keep it. This test is
// worth having under -race, where the suite runs it.
func TestTheInputCanBeSharedBetweenRewriters(t *testing.T) {
	doc := []byte(strings.Repeat(twowaysDoc, 20))

	for i := 0; i < 4; i++ {
		res, err := Concurrent(doc, 512)
		if err != nil {
			t.Fatal(err)
		}
		want, err := Sequential(doc, 512)
		if err != nil {
			t.Fatal(err)
		}
		if !Agree(res, want) {
			t.Fatalf("run %d disagreed with the sequential answer", i)
		}
	}
}

// TestTheHandlersAreBuiltWithTheirState, which is the discipline that makes the rewriters
// independent: two calls to the option builders share nothing.
func TestTheHandlersAreBuiltWithTheirState(t *testing.T) {
	_, first := auditOptions()
	_, second := auditOptions()
	if first == second {
		t.Fatal("two calls returned the same Audit")
	}

	optsA, a := transformOptions()
	optsB, b := transformOptions()
	if a == b {
		t.Fatal("two calls returned the same counter")
	}
	if len(optsA) != len(optsB) {
		t.Errorf("%d options and %d", len(optsA), len(optsB))
	}
}

// TestAnEmptyDocumentIsNotAFailure, since a report of nothing is a legitimate answer.
func TestAnEmptyDocumentIsNotAFailure(t *testing.T) {
	res, err := Concurrent([]byte("<p></p>"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Audit.Links != 0 || res.Rewritten != 0 {
		t.Errorf("a document with no links reported %+v and %d rewritten", res.Audit, res.Rewritten)
	}
	if res.Output != "<p></p>" {
		t.Errorf("the output is %q", res.Output)
	}
}

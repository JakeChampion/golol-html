package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func run(t *testing.T, doc string, mode Mode) (string, Summary, error) {
	t.Helper()
	var out strings.Builder
	s, err := Run(strings.NewReader(doc), &out, mode)
	return out.String(), s, err
}

// commentsIn reports the text of every comment in a document, which is how to tell whether the
// summary stayed one comment.
func commentsIn(t *testing.T, doc string) []string {
	t.Helper()
	var got []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
		got = append(got, c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestASummaryHoldingAHrefLandsInOneComment is the ordinary case: the rewrite happens, the
// summary describes it, and the whole summary comes back as a single comment.
func TestASummaryHoldingAHrefLandsInOneComment(t *testing.T) {
	const doc = `<a target="_blank" href="/one">a</a><a target="_blank" href="/two">b</a>`
	got, summary, err := run(t, doc, ModeSafe)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Changed["a"] != 2 {
		t.Errorf("changed %v, want two anchors", summary.Changed)
	}
	if !strings.Contains(got, `rel="noopener"`) {
		t.Errorf("the rewrite did not happen: %s", got)
	}
	comments := commentsIn(t, got)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1: %q", len(comments), comments)
	}
	if !strings.Contains(comments[0], "a=2") || !strings.Contains(comments[0], "/one") {
		t.Errorf("the comment does not say what happened: %q", comments[0])
	}
}

// TestAHrefThatWouldEndTheCommentIsTheWholePoint. A URL is document content, and a document can
// contain one holding "-->" - a literal ">" is legal inside a quoted attribute value, and
// Attribute returns the raw source, so it arrives in the summary as those three characters.
// Written "&gt;" it is harmless, which is worth knowing: the first draft of this test used the
// entity and proved nothing, because the raw value the handler sees is what matters.
func TestAHrefThatWouldEndTheCommentIsTheWholePoint(t *testing.T) {
	const doc = `<a target="_blank" href="/x?q=a-->b">a</a>`

	// What the summary would be, unguarded.
	_, summary, err := run(t, doc, ModeSafe)
	if err != nil {
		t.Fatal(err)
	}
	raw := summary.Text()
	if lolhtml.CheckComment(raw) == nil {
		t.Fatalf("the summary %q is comment-safe as written, so this document does not "+
			"exercise the check - the href has to survive into it", raw)
	}

	// Appending it unguarded puts markup in the document: more than one comment, or text
	// that is not in a comment at all.
	unguarded := `<p>d</p>` + "<!--" + raw + "-->"
	if got := commentsIn(t, unguarded); len(got) == 1 && got[0] == raw {
		t.Fatalf("the unguarded comment survived intact, so there is nothing to guard: %q", got)
	} else {
		t.Logf("unguarded, %q comes back as %d comments: %q", raw, len(got), got)
	}

	// Guarded, it is one comment again, and it still says what happened.
	out, _, err := run(t, doc, ModeSafe)
	if err != nil {
		t.Fatal(err)
	}
	comments := commentsIn(t, out)
	if len(comments) != 1 {
		t.Errorf("%d comments after the guard, want 1: %q", len(comments), comments)
	}
	if !strings.Contains(out, "- ->") {
		t.Errorf("the sequence was not rewritten: %s", out)
	}
	if strings.Contains(comments[0], "-->") {
		t.Errorf("the comment still holds the closing sequence: %q", comments[0])
	}
}

// TestStrictRefusesInsteadOfAltering, for a caller that would rather fail than have its summary
// changed under it. No comment is emitted, and the document is still rewritten.
func TestStrictRefusesInsteadOfAltering(t *testing.T) {
	const doc = `<a target="_blank" href="/x?q=a-->b">a</a>`
	out, _, err := run(t, doc, ModeStrict)
	if err == nil {
		t.Fatal("strict mode accepted a summary that cannot be a comment")
	}
	if !errors.Is(err, lolhtml.ErrCommentBreakout) {
		t.Errorf("error does not wrap ErrCommentBreakout: %v", err)
	}
	if strings.Contains(out, "<!--") {
		t.Errorf("strict mode emitted a comment anyway: %s", out)
	}
	if !strings.Contains(out, `rel="noopener"`) {
		t.Errorf("strict mode lost the rewrite as well: %s", out)
	}
}

// TestSafeFixesEverythingTheCheckRefuses. Safe is only sound if it is total: any text the check
// refuses has to come back accepted, or the program would emit a comment that leaks.
func TestSafeFixesEverythingTheCheckRefuses(t *testing.T) {
	alphabet := []string{"-", "!", ">", "<", "a", " "}
	checked, fixed := 0, 0
	for n := 1; n <= 4; n++ {
		idx := make([]int, n)
		for {
			var b strings.Builder
			for _, i := range idx {
				b.WriteString(alphabet[i])
			}
			text := b.String()
			checked++
			if lolhtml.CheckComment(text) != nil {
				fixed++
				if err := lolhtml.CheckComment(Safe(text)); err != nil {
					t.Errorf("Safe(%q) is %q, still refused: %v", text, Safe(text), err)
				}
			} else if got := Safe(text); got != text {
				t.Errorf("Safe changed %q to %q, which the check already accepted",
					text, got)
			}
			p := n - 1
			for ; p >= 0; p-- {
				idx[p]++
				if idx[p] < len(alphabet) {
					break
				}
				idx[p] = 0
			}
			if p < 0 {
				break
			}
		}
	}
	if fixed == 0 {
		t.Fatal("nothing in the corpus needed fixing, so this proves nothing")
	}
	t.Logf("%d strings, %d needed rewriting and all of them came back accepted", checked, fixed)
}

// TestNoAnchorsStillReportsNothingChanged, rather than emitting no comment: "I ran and changed
// nothing" and "I did not run" are different facts.
func TestNoAnchorsStillReportsNothingChanged(t *testing.T) {
	out, summary, err := run(t, `<p>nothing here</p>`, ModeSafe)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Changed) != 0 {
		t.Errorf("changed %v, want nothing", summary.Changed)
	}
	comments := commentsIn(t, out)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want one saying nothing changed: %q", len(comments), comments)
	}
	if !strings.Contains(comments[0], "rewritten:") {
		t.Errorf("comment %q does not name the rewrite", comments[0])
	}
}

// TestAnAnchorThatAlreadyHasRelIsNotCounted - the summary has to be about what changed, not about
// what matched.
func TestAnAnchorThatAlreadyHasRelIsNotCounted(t *testing.T) {
	_, summary, err := run(t, `<a target="_blank" rel="noreferrer" href="/x">a</a>`, ModeSafe)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Changed) != 0 {
		t.Errorf("changed %v, want nothing: the anchor already had rel", summary.Changed)
	}
}

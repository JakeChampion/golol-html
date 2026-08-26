package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func stampTop(t *testing.T, doc string, s Stamp) (string, Stamp) {
	t.Helper()
	var out strings.Builder
	got, err := AtTop(strings.NewReader(doc), &out, s)
	if err != nil {
		t.Fatalf("AtTop(%q): %v", doc, err)
	}
	return out.String(), got
}

func stampEnd(t *testing.T, doc string, s Stamp) (string, Stamp) {
	t.Helper()
	var out strings.Builder
	got, err := AtEnd(strings.NewReader(doc), &out, s)
	if err != nil {
		t.Fatalf("AtEnd(%q): %v", doc, err)
	}
	return out.String(), got
}

var abc = Stamp{Commit: "9f3a1c2"}

// TestTheAnchorIsWhicheverElementCameFirst, because a rewriter reports the elements the source
// contains and not the ones a tree builder adds. A rewrite that prepends to <head> does nothing
// on a document that does not spell one, which is most fragments.
func TestTheAnchorIsWhicheverElementCameFirst(t *testing.T) {
	for _, tt := range []struct {
		doc    string
		anchor string
		where  Placement
	}{
		{`<!doctype html><html><head></head><body><p>x</p></body></html>`, "html", BeforeFirstElement},
		{`<!doctype html><p>x</p>`, "p", BeforeFirstElement},
		{`<p>x</p>`, "p", BeforeFirstElement},
		{`text then <b>bold</b>`, "b", BeforeFirstElement},
		{`<!-- a comment --><section>x</section>`, "section", BeforeFirstElement},

		// Nothing to be before.
		{``, "", AtDocumentEnd},
		{`just text`, "", AtDocumentEnd},
		{`<!-- only a comment -->`, "", AtDocumentEnd},
		{`<!doctype html>`, "", AtDocumentEnd},
	} {
		out, got := stampTop(t, tt.doc, abc)
		if got.Where != tt.where {
			t.Errorf("%q: placed %v, want %v", tt.doc, got.Where, tt.where)
		}
		if got.FirstElement != tt.anchor {
			t.Errorf("%q: anchor %q, want %q", tt.doc, got.FirstElement, tt.anchor)
		}
		if !strings.Contains(out, "build 9f3a1c2") {
			t.Errorf("%q: no stamp in %q", tt.doc, out)
		}
		// The stamp is a comment wherever it went, so the document stays valid.
		if n := commentsIn(t, out); n < 1 {
			t.Errorf("%q: %d comments in %q", tt.doc, n, out)
		}
	}

	// The specific claim about <head>: a selector for it matches nothing on a document that
	// does not spell it, so anchoring there would silently do nothing.
	n := 0
	if _, err := lolhtml.RewriteString(`<!doctype html><p>x</p>`,
		lolhtml.OnElement("head", func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("head matched %d times on a document without one", n)
	}
}

func commentsIn(t *testing.T, doc string) int {
	t.Helper()
	n := 0
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			n++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTheStampLandsAfterTheDoctype, which matters because a doctype has to be first to count -
// and this is the position Doctype itself cannot offer, having Remove and no insertions.
func TestTheStampLandsAfterTheDoctype(t *testing.T) {
	out, _ := stampTop(t, `<!doctype html><html><body>x</body></html>`, abc)
	if !strings.HasPrefix(out, "<!doctype html><!-- build") {
		t.Errorf("the stamp is not between the doctype and the html: %s", out)
	}

	// A doctype is still the first thing in the output, so the document is still standards
	// mode. The test for that from the outside is B174's: a table start tag closes an open
	// paragraph in standards mode and not in quirks mode - measured in
	// differential/buildinfo_test.go, where x/net/html lives.
	if strings.Index(out, "<!doctype") != 0 {
		t.Errorf("the doctype is no longer first: %s", out)
	}
}

// TestTheTopIsNotIdempotentAndTheEndIs, which is the trade the program exists to show. At the
// first element the answer to "is it already stamped" is not known yet, because a stamp already
// in the document could be anywhere in it.
func TestTheTopIsNotIdempotentAndTheEndIs(t *testing.T) {
	const doc = `<!doctype html><html><body><p>x</p></body></html>`

	// Top: twice gives two stamps, and the second run knows it - too late.
	once, first := stampTop(t, doc, abc)
	if first.Already {
		t.Error("the first run found an existing stamp")
	}
	twice, second := stampTop(t, once, abc)
	if !second.Already {
		t.Error("the second run did not notice the existing stamp")
	}
	if got := strings.Count(twice, "build 9f3a1c2"); got != 2 {
		t.Errorf("%d stamps after two passes, want 2: %s", got, twice)
	}
	if second.Where != BeforeFirstElement {
		t.Errorf("the second run placed %v", second.Where)
	}

	// End: twice gives one, because by the end every comment has gone past.
	onceEnd, firstEnd := stampEnd(t, doc, abc)
	if firstEnd.Where != AtDocumentEnd || firstEnd.Already {
		t.Errorf("%+v", firstEnd)
	}
	twiceEnd, secondEnd := stampEnd(t, onceEnd, abc)
	if !secondEnd.Already {
		t.Error("the second run did not notice the existing stamp")
	}
	if secondEnd.Where != NotPlaced {
		t.Errorf("the second run placed %v", secondEnd.Where)
	}
	if got := strings.Count(twiceEnd, "build 9f3a1c2"); got != 1 {
		t.Errorf("%d stamps after two passes, want 1: %s", got, twiceEnd)
	}
	if twiceEnd != onceEnd {
		t.Errorf("the second pass changed the document:\n%s\n%s", onceEnd, twiceEnd)
	}

	// And the end placement is idempotent however many times it runs.
	current := doc
	for i := range 5 {
		next, _ := stampEnd(t, current, abc)
		if i > 0 && next != current {
			t.Errorf("pass %d changed the document", i)
		}
		current = next
	}
	if got := strings.Count(current, "build 9f3a1c2"); got != 1 {
		t.Errorf("%d stamps after six passes", got)
	}
}

// TestAValueThatCannotGoInACommentIsRefused, since a stamp is a comment and Comment.SetText's
// refusal is not available to an insertion - this is the caller's job.
func TestAValueThatCannotGoInACommentIsRefused(t *testing.T) {
	for _, s := range []Stamp{
		{Commit: "a-->b"},
		{Commit: "a--b"},
		{Commit: "a>b"},
		{Commit: "a<b"},
		{Commit: "ok", Built: "2026 --> now"},
		{Commit: ""},
	} {
		if err := s.Valid(); err == nil {
			t.Errorf("%+v was accepted", s)
		}
		var out strings.Builder
		if _, err := AtTop(strings.NewReader(`<p>x</p>`), &out, s); err == nil {
			t.Errorf("%+v was stamped: %s", s, out.String())
		}
		if out.Len() != 0 {
			t.Errorf("%+v wrote %d bytes before refusing", s, out.Len())
		}
	}

	for _, s := range []Stamp{
		{Commit: "9f3a1c2"},
		{Commit: "9f3a1c2", Built: "2026-08-26T02:00:00Z"},
		{Commit: "v1.2.3-rc1"},
		{Commit: "branch/name"},
	} {
		if err := s.Valid(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
		out, got := stampTop(t, `<p>x</p>`, s)
		if !strings.Contains(out, s.Commit) {
			t.Errorf("%+v: %s", s, out)
		}
		if commentsIn(t, out) != 1 {
			t.Errorf("%+v produced %d comments: %s", s, commentsIn(t, out), out)
		}
		if got.Where != BeforeFirstElement {
			t.Errorf("%+v placed %v", s, got.Where)
		}
	}
}

// TestTheStampIsTheOnlyChange, since a build stamp that reformatted the page would be worse than
// none.
func TestTheStampIsTheOnlyChange(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<div><ul><li>a<li>b</ul><img src="/x"></div>`,
		`<p>x</p><script>var a = 1 < 2;</script><style>.a > .b{}</style>`,
		`<!-- a --><table><tr><td>x</table>`,
	} {
		out, got := stampTop(t, doc, abc)
		if got.Where != BeforeFirstElement {
			t.Errorf("%q placed %v", doc, got.Where)
		}
		without := strings.Replace(out, got.Comment(), "", 1)
		if without != doc {
			t.Errorf("more than the stamp changed:\n  in:  %s\n  out: %s", doc, without)
		}
	}
}

package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func atPage2(n *nav) { n.current = 2 }

const paged = `<!DOCTYPE html><html><head><title>t</title></head><body><p>x</p>` +
	`<nav class="pagination"><a href="/p/1">1</a><a href="/p/2">2</a>` +
	`<a href="/p/3">3</a><a href="/p/4">Next</a></nav></body></html>`

var corpus = []string{
	paged,
	`<html><head><link rel="next" href="/stale"></head><body><nav class="pagination"><a href="/p/1">1</a><a href="/p/3">3</a></nav></body></html>`,
	`<html><head></head><body><nav class="pagination"><a href="/p/3">3</a></nav></body></html>`,
	`<html><head></head><body><p>no pagination</p></body></html>`,
	`<html><body><nav class="pagination"><a href="/p/1">1</a><a href="/p/3">3</a></nav></body></html>`,
	`<html><head><link rel="prev" href="/a"><link rel="prev" href="/b"></head><body><nav class="pagination"><a href="/p/1">1</a></nav></body></html>`,
	`<p>fragment</p>`,
	``,
}

// relLinks asks the parser what the document declares.
func relLinks(t *testing.T, doc string) [][2]string {
	t.Helper()
	var out [][2]string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`link[rel~="next"], link[rel~="prev"]`, func(e *lolhtml.Element) error {
			out = append(out, [2]string{attr(e, "rel"), attr(e, "href")})
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestChunkInvarianceOfTheWritePass. The reading pass takes the whole document
// by construction, so what is worth checking is the writing pass, driven through
// its own handler set with the input split up.
func TestChunkInvarianceOfTheWritePass(t *testing.T) {
	for _, doc := range corpus {
		whole, err := writeWithChunks(t, doc, len(doc)+1)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, size := range []int{1, 2, 3, 47} {
			got, err := writeWithChunks(t, doc, size)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", size, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					size, doc, whole, got)
			}
		}
	}
}

// writeWithChunks runs a reading pass, then the writing pass with the input
// delivered in pieces of the given size.
func writeWithChunks(t *testing.T, doc string, size int) (string, error) {
	t.Helper()
	n := defaults()
	atPage2(n)
	if err := n.validate(); err != nil {
		return "", err
	}
	if err := n.readPass([]byte(doc)); err != nil {
		return "", err
	}

	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, n.writeOptions()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := navString(doc, atPage2)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, n, err := navString(once, atPage2)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if n.inserted != 0 {
			t.Errorf("the second pass of %q inserted %d", doc, n.inserted)
		}
	}
}

// TestTheNeighboursComeFromTheNav is the two-pass path: the links are in the
// head and the evidence for them is in the body.
func TestTheNeighboursComeFromTheNav(t *testing.T) {
	out, n, err := navString(paged, atPage2)
	if err != nil {
		t.Fatal(err)
	}
	if n.passes != 2 {
		t.Errorf("passes=%d, want 2", n.passes)
	}
	if n.inserted != 2 {
		t.Errorf("inserted=%d, want 2", n.inserted)
	}
	want := [][2]string{{"prev", "/p/1"}, {"next", "/p/3"}}
	got := relLinks(t, out)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("declares %v, want %v", got, want)
	}
	// And they are in the head, before the nav they came from.
	if i, j := strings.Index(out, `rel="prev"`), strings.Index(out, "</head>"); i < 0 || i > j {
		t.Errorf("the links are not in the head: %s", out)
	}
}

// TestGivingTheNeighboursStaysOnOnePass, which is the point of the flags: a
// caller who already knows should not pay for a second pass.
func TestGivingTheNeighboursStaysOnOnePass(t *testing.T) {
	out, n, err := navString(paged, func(n *nav) {
		n.prev, n.next = "/a", "/b"
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.passes != 1 {
		t.Errorf("passes=%d, want 1", n.passes)
	}
	if len(n.found) != 0 {
		t.Errorf("the reading pass ran anyway: found %v", n.found)
	}
	got := relLinks(t, out)
	if len(got) != 2 || got[0][1] != "/a" || got[1][1] != "/b" {
		t.Errorf("declares %v", got)
	}
}

// TestOnlyNumberedLinksArePages: "Next", "..." and "Last" are furniture, and
// treating one as a page number points rel=next at the wrong place.
func TestPageNumber(t *testing.T) {
	for _, tt := range []struct {
		text string
		page int
		ok   bool
	}{
		{"1", 1, true},
		{" 2 ", 2, true},
		{"10", 10, true},
		{"&#49;", 1, true}, // a reference, decoded first
		{"Next", 0, false},
		{"next", 0, false},
		{"...", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"-1", 0, false},
		{"1a", 0, false},
		{"1.0", 0, false},
		{"page 2", 0, false},
	} {
		page, ok := pageNumber(tt.text)
		if ok != tt.ok || (ok && page != tt.page) {
			t.Errorf("pageNumber(%q) = %d/%v, want %d/%v", tt.text, page, ok, tt.page, tt.ok)
		}
	}
}

// TestAStaleLinkIsRewrittenNotJoined.
func TestAStaleLinkIsRewrittenNotJoined(t *testing.T) {
	out, n, err := navString(
		`<html><head><link rel="next" href="/stale"></head><body>`+
			`<nav class="pagination"><a href="/p/1">1</a><a href="/p/3">3</a></nav></body></html>`,
		atPage2)
	if err != nil {
		t.Fatal(err)
	}
	if n.rewrote != 1 || n.inserted != 1 {
		t.Errorf("rewrote=%d inserted=%d, want 1 and 1", n.rewrote, n.inserted)
	}
	for _, l := range relLinks(t, out) {
		if l[1] == "/stale" {
			t.Errorf("the stale link survived: %s", out)
		}
	}
	if n := len(relLinks(t, out)); n != 2 {
		t.Errorf("%d links, want 2: %s", n, out)
	}
}

// TestASecondLinkOfTheSameKindIsRemoved: two rel=prev contradict each other.
func TestASecondLinkOfTheSameKindIsRemoved(t *testing.T) {
	out, _, err := navString(
		`<html><head><link rel="prev" href="/a"><link rel="prev" href="/b"></head><body>`+
			`<nav class="pagination"><a href="/p/1">1</a></nav></body></html>`,
		atPage2)
	if err != nil {
		t.Fatal(err)
	}
	prevs := 0
	for _, l := range relLinks(t, out) {
		if strings.EqualFold(l[0], "prev") {
			prevs++
		}
	}
	if prevs != 1 {
		t.Errorf("%d rel=prev links, want 1: %s", prevs, out)
	}
}

// TestNoNeighboursIsReported rather than silently doing nothing.
func TestNoNeighboursIsReported(t *testing.T) {
	out, n, err := navString(`<html><head></head><body><p>no pagination</p></body></html>`, atPage2)
	if err != nil {
		t.Fatal(err)
	}
	if n.inserted != 0 || total(n.skipped) == 0 {
		t.Errorf("inserted=%d skipped=%v", n.inserted, n.skipped)
	}
	if len(relLinks(t, out)) != 0 {
		t.Errorf("links were invented: %s", out)
	}
}

// TestTheFirstPageHasNoPrev and the last has no next: an edge is not an error.
func TestAnEdgePageGetsOneLink(t *testing.T) {
	for _, tt := range []struct {
		current  int
		wantRels []string
	}{
		{1, []string{"next"}},
		{2, []string{"prev", "next"}},
		{3, []string{"prev"}},
		{9, nil},
	} {
		out, _, err := navString(paged, func(n *nav) { n.current = tt.current })
		if err != nil {
			t.Fatalf("current=%d: %v", tt.current, err)
		}
		var rels []string
		for _, l := range relLinks(t, out) {
			rels = append(rels, strings.ToLower(l[0]))
		}
		if strings.Join(rels, ",") != strings.Join(tt.wantRels, ",") {
			t.Errorf("current=%d declares %v, want %v", tt.current, rels, tt.wantRels)
		}
	}
}

// TestTheHrefIsEscaped, since the link is assembled as markup.
func TestTheHrefIsEscaped(t *testing.T) {
	out, _, err := navString(`<html><head></head><body>x</body></html>`, func(n *nav) {
		n.next = "/p?page=3&sort=asc"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/p?page=3&amp;sort=asc"`) {
		t.Errorf("the href was not escaped: %s", out)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*nav)
	}{
		{"nothing given", func(n *nav) {}},
		{"an empty selector", func(n *nav) { n.current = 2; n.selector = "" }},
		{"an unparseable next", func(n *nav) { n.next = "http://[::1" }},
	} {
		if _, _, err := navString(paged, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

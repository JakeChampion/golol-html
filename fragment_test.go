package lolhtml_test

// Splitting a document between two rewriters is not the same as chunking it into
// one. A fragment that ends inside a tag is invisible to every handler and is
// emitted verbatim, so joining the outputs can reassemble markup that neither
// pass inspected.
//
// This is the gate on the package documentation's claim, and the claim is exact:
// which cut points fail, how many, and in which of two ways. A change upstream
// that made the tail of a truncated document visible to handlers would fail here,
// which is the point - the documentation would then be wrong.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// stripScripts removes every script element and reports the tag names it saw.
func stripScripts(t *testing.T, doc string) (string, []string) {
	t.Helper()
	var seen []string
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		seen = append(seen, e.TagName())
		if e.TagName() == "script" {
			e.Remove()
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return out, seen
}

// holdsScript re-parses a document and reports whether it contains a script
// element, because text that looks like one is not one.
func holdsScript(t *testing.T, doc string) bool {
	t.Helper()
	found := false
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("script", func(*lolhtml.Element) error {
			found = true
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestOneRewriterIsSafeAtEveryChunkBoundary(t *testing.T) {
	const doc = `<p>a</p><script>alert(1)</script><p>b</p>`

	for size := 1; size <= len(doc)+1; size++ {
		var out strings.Builder
		var seen []string
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			seen = append(seen, e.TagName())
			if e.TagName() == "script" {
				e.Remove()
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != `<p>a</p><p>b</p>` {
			t.Errorf("chunk %d: %q", size, got)
		}
		if strings.Join(seen, " ") != "p script p" {
			t.Errorf("chunk %d: saw %v", size, seen)
		}
	}
}

func TestTwoRewritersOverTwoFragmentsAreNot(t *testing.T) {
	const doc = `<p>a</p><script>alert(1)</script><p>b</p>`
	start := strings.Index(doc, "<script>")
	end := start + len("<script>")

	var element, textOnly, clean []int
	for i := 1; i < len(doc); i++ {
		a, seenA := stripScripts(t, doc[:i])
		b, seenB := stripScripts(t, doc[i:])
		joined := a + b
		switch {
		case holdsScript(t, joined):
			element = append(element, i)
			// The failure is silent: neither pass saw a script to remove.
			for _, seen := range [][]string{seenA, seenB} {
				for _, name := range seen {
					if name == "script" {
						t.Errorf("cut %d: a pass saw the script and "+
							"left the element anyway", i)
					}
				}
			}
		case strings.Contains(joined, "alert(1)"):
			textOnly = append(textOnly, i)
		default:
			clean = append(clean, i)
		}
	}

	// Strictly inside the start tag, and only there.
	for _, i := range element {
		if i <= start || i >= end {
			t.Errorf("cut %d reassembled an element, and it is not inside the start "+
				"tag at %d..%d", i, start, end)
		}
	}
	if len(element) != end-start-1 {
		t.Errorf("%d cuts reassembled an element, want %d (%v)",
			len(element), end-start-1, element)
	}

	// The cut immediately after the start tag leaves the payload as text.
	if len(textOnly) != 1 || textOnly[0] != end {
		t.Errorf("the payload survived as text at %v, want [%d]", textOnly, end)
	}
	a, _ := stripScripts(t, doc[:end])
	b, _ := stripScripts(t, doc[end:])
	if got, want := a+b, `<p>a</p>alert(1)</script><p>b</p>`; got != want {
		t.Errorf("cut %d gave %q, want %q", end, got, want)
	}

	// Everything else is clean, so the hazard is the tag boundary and nothing wider.
	if want := len(doc) - 1 - len(element) - len(textOnly); len(clean) != want {
		t.Errorf("%d clean cuts, want %d", len(clean), want)
	}
}

func TestATagTheDocumentNeverFinishesReachesNoHandler(t *testing.T) {
	// A tag is the only construct a document can end inside that no handler sees.
	for _, doc := range []string{
		"<p",
		"<p ",
		"<p attr",
		`<p attr="v`,
		"<p/",
		"</p",
		"<script",
	} {
		if elements, text, comments, doctypes, out := count(t, doc); elements+text+comments+doctypes != 0 {
			t.Errorf("%q: %d elements, %d text chunks, %d comments, %d doctypes reached "+
				"a handler", doc, elements, text, comments, doctypes)
			_ = out
		} else if out != doc {
			t.Errorf("%q: output %q", doc, out)
		}
	}

	// Everything else a document can end inside still arrives. This is what makes the tag
	// case worth documenting: it is the exception, not the rule.
	for _, tt := range []struct {
		doc                                string
		elements, text, comments, doctypes int
	}{
		{"<!-", 0, 0, 1, 0},
		{"<!--", 0, 0, 1, 0},
		{"<!-- x", 0, 0, 1, 0},
		{"<!-- x --", 0, 0, 1, 0},
		{"<!", 0, 0, 1, 0},
		{"<?php", 0, 0, 1, 0},
		{"<![CDATA[x", 0, 0, 1, 0},
		{"<!DOCTYPE", 0, 0, 0, 1},
		{"<script>", 1, 0, 0, 0},
		{"<script>var a", 1, 1, 0, 0},
		{"<style>p{", 1, 1, 0, 0},
		{"<textarea>x", 1, 1, 0, 0},
		{"text", 0, 1, 0, 0},
	} {
		elements, text, comments, doctypes, out := count(t, tt.doc)
		if elements != tt.elements || text != tt.text || comments != tt.comments ||
			doctypes != tt.doctypes {
			t.Errorf("%q: %d/%d/%d/%d elements/text/comments/doctypes, want %d/%d/%d/%d",
				tt.doc, elements, text, comments, doctypes,
				tt.elements, tt.text, tt.comments, tt.doctypes)
		}
		if out != tt.doc {
			t.Errorf("%q: output %q", tt.doc, out)
		}
	}

	// And the same bytes in a finished document are seen normally, so this is about the
	// document ending rather than about the bytes.
	if elements, _, _, _, _ := count(t, `<p attr="v">x</p>`); elements != 1 {
		t.Errorf("%d elements for a finished tag", elements)
	}
}

// count reports how many of each handler kind ran, and the output.
func count(t *testing.T, doc string) (elements, text, comments, doctypes int, out string) {
	t.Helper()
	var err error
	out, err = lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(*lolhtml.Element) error { elements++; return nil }),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				text++
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { comments++; return nil }),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error { doctypes++; return nil }))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return elements, text, comments, doctypes, out
}

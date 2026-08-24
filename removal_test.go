package lolhtml_test

// What removal suppresses, and what it does not.
//
// Element.Remove documents itself as removing the element and everything inside
// it. That is true of the document's own content, but not of content a handler
// inserts afterwards: lol-html decides to drop the inner content at the moment
// remove() is called, and an insertion made after that is still emitted, with
// the element's tags no longer around it. Inserting first and removing second
// discards it. So the same two operations disagree depending on their order.
//
// None of it is visible in a rewrite that does only one thing to an element,
// which is why it is pinned here: this file is the specification of the corner,
// and the day upstream makes the two orders agree, these tests are what notices.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestInsertionAfterRemoveIsStillEmitted is the defect, at its smallest.
func TestInsertionAfterRemoveIsStillEmitted(t *testing.T) {
	tests := []struct {
		name   string
		insert func(*lolhtml.Element) error
		want   string
	}{
		// Before and After position content outside the element, so surviving
		// the removal is what they should do.
		{"Before", func(e *lolhtml.Element) error {
			return e.Before("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
		{"After", func(e *lolhtml.Element) error {
			return e.After("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
		// Replace means "this instead of the element", so it also survives.
		{"Replace", func(e *lolhtml.Element) error {
			return e.Replace("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
		// These three target the inside of an element that will not be emitted.
		// The content reaches the output anyway, without the tags that scoped
		// it. This is the part that is wrong.
		{"Append", func(e *lolhtml.Element) error {
			return e.Append("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
		{"Prepend", func(e *lolhtml.Element) error {
			return e.Prepend("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
		{"SetInnerContent", func(e *lolhtml.Element) error {
			return e.SetInnerContent("[X]", lolhtml.HTML)
		}, `<div>[X]</div>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(`<div><p>original</p></div>`,
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					e.Remove()
					return tt.insert(e)
				}))
			if err != nil {
				t.Fatal(err)
			}
			if out != tt.want {
				t.Errorf("\n got: %s\nwant: %s", out, tt.want)
			}
		})
	}
}

// TestInsertionBeforeRemoveIsDiscarded is the other order, and it disagrees.
func TestInsertionBeforeRemoveIsDiscarded(t *testing.T) {
	inserts := map[string]func(*lolhtml.Element) error{
		"Append":          func(e *lolhtml.Element) error { return e.Append("[X]", lolhtml.HTML) },
		"Prepend":         func(e *lolhtml.Element) error { return e.Prepend("[X]", lolhtml.HTML) },
		"SetInnerContent": func(e *lolhtml.Element) error { return e.SetInnerContent("[X]", lolhtml.HTML) },
	}
	for name, insert := range inserts {
		t.Run(name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(`<div><p>original</p></div>`,
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					if err := insert(e); err != nil {
						return err
					}
					e.Remove()
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if want := `<div></div>`; out != want {
				t.Errorf("\n got: %s\nwant: %s", out, want)
			}
		})
	}
}

// TestRemovalOrderAcrossTwoHandlers is why this matters in practice. Neither
// handler is wrong on its own; which one is registered first decides whether
// content escapes the element it was scoped to. Here the escaped content is a
// comment that only reads as a comment inside a script.
func TestRemovalOrderAcrossTwoHandlers(t *testing.T) {
	remover := lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		e.Remove()
		return nil
	})
	annotator := lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		return e.Append("/* audited */", lolhtml.HTML)
	})

	removerFirst, err := lolhtml.RewriteString(`<script>var a=1;</script><p>keep</p>`, remover, annotator)
	if err != nil {
		t.Fatal(err)
	}
	annotatorFirst, err := lolhtml.RewriteString(`<script>var a=1;</script><p>keep</p>`, annotator, remover)
	if err != nil {
		t.Fatal(err)
	}

	if want := `/* audited */<p>keep</p>`; removerFirst != want {
		t.Errorf("remover first:\n got: %s\nwant: %s", removerFirst, want)
	}
	if want := `<p>keep</p>`; annotatorFirst != want {
		t.Errorf("annotator first:\n got: %s\nwant: %s", annotatorFirst, want)
	}
	if removerFirst == annotatorFirst {
		t.Error("the two orders agree; the known divergence has been fixed and this test should be inverted")
	}
}

// TestHandlersInsideRemovedContentStillRun: removal suppresses output, not
// dispatch. A handler that accumulates - collecting a document's visible text,
// counting what it rewrote - keeps being called for content that will not be
// emitted, and has to check for itself.
func TestHandlersInsideRemovedContentStillRun(t *testing.T) {
	var text []string
	var elements []string

	out, err := lolhtml.RewriteString(`<div><p>secret<a href="/x">link</a></p></div><p>keep</p>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			elements = append(elements, e.TagName())
			// Discarded along with the rest of the removed subtree.
			return e.SetAttribute("href", "/CHANGED")
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			if s := tc.Text(); s != "" {
				text = append(text, s)
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	if want := `<p>keep</p>`; out != want {
		t.Errorf("output\n got: %s\nwant: %s", out, want)
	}
	if len(elements) != 1 {
		t.Errorf("element handlers inside removed content ran %d times, want 1", len(elements))
	}
	if got := strings.Join(text, "|"); got != "secret|link|keep" {
		t.Errorf("text handler saw %q, want %q", got, "secret|link|keep")
	}
	if strings.Contains(out, "/CHANGED") {
		t.Error("an edit made inside removed content reached the output")
	}
}

// TestIsRemovedIsTrueForBothRemovals records why IsRemoved cannot be used to
// decide whether inserting inside the element is safe: it is true after
// RemoveAndKeepContent too, and appending there is both legal and useful.
func TestIsRemovedIsTrueForBothRemovals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		remove func(*lolhtml.Element)
		want   string
	}{
		{"Remove", (*lolhtml.Element).Remove, `<div>[X]</div>`},
		{"RemoveAndKeepContent", (*lolhtml.Element).RemoveAndKeepContent, `<div>kept[X]</div>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var seen bool
			out, err := lolhtml.RewriteString(`<div><p>kept</p></div>`,
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					tt.remove(e)
					seen = e.IsRemoved()
					return e.Append("[X]", lolhtml.HTML)
				}))
			if err != nil {
				t.Fatal(err)
			}
			if !seen {
				t.Error("IsRemoved() is false after removal")
			}
			if out != tt.want {
				t.Errorf("\n got: %s\nwant: %s", out, tt.want)
			}
		})
	}
}

// TestRemovedElementEndTagHandlerStillInserts: the end tag of a removed element
// is gone from the output, but a handler registered on it can still put content
// where it was.
func TestRemovedElementEndTagHandlerStillInserts(t *testing.T) {
	out, err := lolhtml.RewriteString(`<div><p>original</p></div>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			e.Remove()
			return e.OnEndTag(func(tag *lolhtml.EndTag) error {
				return tag.Before("[END]", lolhtml.HTML)
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div>[END]</div>`; out != want {
		t.Errorf("\n got: %s\nwant: %s", out, want)
	}
}

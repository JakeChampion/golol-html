package lolhtml_test

// Inserted content is emitted, not re-parsed.
//
// Nothing a handler inserts is dispatched to any handler, including the one that
// inserted it and including handlers on other selectors in the same rewrite. It
// goes into the output verbatim.
//
// Three things follow, and they pull in different directions, which is why they
// are pinned together:
//
//   - There is no loop hazard. A handler that inserts an element matching its
//     own selector fires once, not forever.
//   - An accumulator is safe. A text handler collecting a heading's text does
//     not also collect a label an element handler prepended, so a rewrite that
//     reads and writes the same element is not self-compounding.
//   - A sanitiser does not sanitise its own insertions. A rewrite that strips
//     every <script> and separately inserts untrusted markup emits that markup
//     unexamined. The insertion has to be safe before it goes in; no other
//     handler will look at it.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestInsertedElementsAreNotDispatched: the injected <span> reaches the output
// and no handler sees it.
func TestInsertedElementsAreNotDispatched(t *testing.T) {
	var seen []string

	got, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.Prepend(`<span>injected</span>`, lolhtml.HTML)
		}),
		lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			seen = append(seen, "span")
			return e.SetAttribute("data-seen", "1")
		}))
	if err != nil {
		t.Fatal(err)
	}

	if want := `<div><span>injected</span>x</div>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if len(seen) != 0 {
		t.Errorf("the span handler ran %d times, want 0", len(seen))
	}
	if strings.Contains(got, "data-seen") {
		t.Error("an inserted element was rewritten by a handler")
	}
}

// TestInsertedCommentsAndTextAreNotDispatched covers the other two unit kinds.
func TestInsertedCommentsAndTextAreNotDispatched(t *testing.T) {
	t.Run("comment", func(t *testing.T) {
		var seen []string
		got, err := lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.Prepend(`<!--injected-->`, lolhtml.HTML)
			}),
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				seen = append(seen, c.Text())
				return c.SetText("rewritten")
			}))
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) != 0 {
			t.Errorf("the comment handler saw %v, want nothing", seen)
		}
		if !strings.Contains(got, "<!--injected-->") {
			t.Errorf("the inserted comment was changed: %s", got)
		}
	})

	t.Run("text", func(t *testing.T) {
		var seen []string
		got, err := lolhtml.RewriteString(`<h2>Intro</h2>`,
			lolhtml.OnElement("h2", func(e *lolhtml.Element) error {
				return e.Prepend("1. ", lolhtml.Text)
			}),
			lolhtml.OnText("h2", func(tc *lolhtml.TextChunk) error {
				if tc.Text() != "" {
					seen = append(seen, tc.Text())
				}
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if want := `<h2>1. Intro</h2>`; got != want {
			t.Errorf("\n got: %s\nwant: %s", got, want)
		}
		if len(seen) != 1 || seen[0] != "Intro" {
			t.Errorf("the text handler saw %v, want just the document's own text", seen)
		}
	})
}

// TestASelfInsertingHandlerRunsOnce is the loop hazard that does not exist. If
// inserted content were re-parsed this would not terminate.
func TestASelfInsertingHandlerRunsOnce(t *testing.T) {
	calls := 0

	got, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			calls++
			if calls > 100 {
				t.Fatal("the handler is being re-entered for its own insertion")
			}
			return e.Prepend(`<div>nested</div>`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("the handler ran %d times, want 1", calls)
	}
	if want := `<div><div>nested</div>x</div>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

// TestASanitiserDoesNotSanitiseItsOwnInsertions is the dangerous half. Both
// handlers are correct in isolation; together, the script reaches the output
// because the remover never sees it.
func TestASanitiserDoesNotSanitiseItsOwnInsertions(t *testing.T) {
	// Registration order is irrelevant: try both, so nobody concludes this is
	// an ordering problem with an ordering fix.
	remover := lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		e.Remove()
		return nil
	})
	injector := lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		// Stand-in for untrusted content reaching an insertion.
		return e.Prepend(`<script>alert(1)</script>`, lolhtml.HTML)
	})

	for _, tt := range []struct {
		name string
		opts []lolhtml.Option
	}{
		{"remover first", []lolhtml.Option{remover, injector}},
		{"injector first", []lolhtml.Option{injector, remover}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(`<div>x</div><script>original</script>`, tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			// The document's own script is removed.
			if strings.Contains(got, "original") {
				t.Errorf("the document's script survived: %s", got)
			}
			// The inserted one is not.
			if !strings.Contains(got, "alert(1)") {
				t.Errorf("the inserted script was removed, so insertions are now "+
					"dispatched and this documentation needs revisiting: %s", got)
			}
		})
	}
}

// TestInsertedContentIsNotReparsedEvenWhenMalformed: it is not tokenised on the
// way in either, so an insertion that does not close its tags is emitted as
// written and becomes the following content's problem.
func TestInsertedContentIsNotReparsedEvenWhenMalformed(t *testing.T) {
	got, err := lolhtml.RewriteString(`<div>x</div><p>after</p>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.Prepend(`<b>unclosed`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div><b>unclosedx</div><p>after</p>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

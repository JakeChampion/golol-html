package lolhtml_test

// What happens to the token that closed an element, when the element is removed,
// unwrapped or renamed and the source left its end tag out.
//
// An end tag is a token, not a fact about the element: where a document omits
// one, the callback runs against the tag that did close the element, which
// belongs to something else. Writing at that position is documented and guarded
// by the name test. Removing and renaming act on the same token, and this is the
// measurement of what that does.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestActingOnAnElementActsOnTheTokenThatClosedIt is the whole matrix: four
// documents, six operations. The point is not that any one line is wrong - the
// library has no tree and cannot invent a token the document did not write - it
// is that the same cause has six different symptoms, and the documentation has to
// name it in each place.
func TestActingOnAnElementActsOnTheTokenThatClosedIt(t *testing.T) {
	ops := []struct {
		name string
		opt  func() lolhtml.Option
	}{
		{"unwrap", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				e.RemoveAndKeepContent()
				return nil
			})
		}},
		{"remove", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				e.Remove()
				return nil
			})
		}},
		{"replace", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				return e.Replace("[R]", lolhtml.HTML)
			})
		}},
		{"rename", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				return e.SetTagName("i")
			})
		}},
		{"end tag remove", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					t.Remove()
					return nil
				})
			})
		}},
		{"end tag insert", func() lolhtml.Option {
			return lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					return t.After("[A]", lolhtml.HTML)
				})
			})
		}},
	}

	for _, tc := range []struct {
		doc  string
		want map[string]string
	}{{
		// The em has no end tag of its own: </h1> closed it.
		doc: "<h1>a <em>b</h1><p>after</p>",
		want: map[string]string{
			// The heading never closes, so the paragraph is inside it.
			"unwrap": "<h1>a b<p>after</p>",
			// The content up to that token goes with the element.
			"remove":  "<h1>a <p>after</p>",
			"replace": "<h1>a [R]<p>after</p>",
			// The rename writes over the heading's closing tag.
			"rename":         "<h1>a <i>b</i><p>after</p>",
			"end tag remove": "<h1>a <em>b<p>after</p>",
			// Only the insertions are documented as landing elsewhere, and they
			// are the ones that leave the document well formed.
			"end tag insert": "<h1>a <em>b</h1>[A]<p>after</p>",
		},
	}, {
		// The element that loses its tag is not the one the handler named and not
		// the one a reader would think of: the span is untouched by the selector.
		doc: "<h1><span>a <em>b</span> c</h1>",
		want: map[string]string{
			"unwrap":         "<h1><span>a b c</h1>",
			"remove":         "<h1><span>a  c</h1>",
			"replace":        "<h1><span>a [R] c</h1>",
			"rename":         "<h1><span>a <i>b</i> c</h1>",
			"end tag remove": "<h1><span>a <em>b c</h1>",
			"end tag insert": "<h1><span>a <em>b</span>[A] c</h1>",
		},
	}, {
		// A sibling's start tag closed the em, so the token arrives after the
		// second item's text has already been reported.
		doc: "<ul><li><em>a<li>b</ul><p>after</p>",
		want: map[string]string{
			"unwrap": "<ul><li>a<li>b<p>after</p>",
			// The second list item is inside the removal.
			"remove":  "<ul><li><p>after</p>",
			"replace": "<ul><li>[R]<p>after</p>",
			// "b" was not emphasised and now is.
			"rename":         "<ul><li><i>a<li>b</i><p>after</p>",
			"end tag remove": "<ul><li><em>a<li>b<p>after</p>",
			"end tag insert": "<ul><li><em>a<li>b</ul>[A]<p>after</p>",
		},
	}, {
		// The same operations where the document did write the end tag: every one
		// of them stays inside the element.
		doc: "<h1>a <em>b</em></h1><p>after</p>",
		want: map[string]string{
			"unwrap":         "<h1>a b</h1><p>after</p>",
			"remove":         "<h1>a </h1><p>after</p>",
			"replace":        "<h1>a [R]</h1><p>after</p>",
			"rename":         "<h1>a <i>b</i></h1><p>after</p>",
			"end tag remove": "<h1>a <em>b</h1><p>after</p>",
			"end tag insert": "<h1>a <em>b</em>[A]</h1><p>after</p>",
		},
	}} {
		for _, op := range ops {
			got, err := lolhtml.RewriteString(tc.doc, op.opt())
			if err != nil {
				t.Fatalf("%s on %q: %v", op.name, tc.doc, err)
			}
			if want := tc.want[op.name]; got != want {
				t.Errorf("%s on %q\n got %q\nwant %q", op.name, tc.doc, got, want)
			}
		}
	}
}

// TestTheNameGuardRepairsTheBorrowedToken. The recipe the documentation gives:
// register the end tag handler before removing, and write the token back when its
// name is not this element's. The document then has the same shape it had.
func TestTheNameGuardRepairsTheBorrowedToken(t *testing.T) {
	repair := lolhtml.OnElement("em", func(e *lolhtml.Element) error {
		name := e.TagName()
		if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
			if t.Name() == name {
				return nil // its own end tag; nothing borrowed
			}
			return t.Before("</"+t.Name()+">", lolhtml.HTML)
		}); err != nil {
			return err
		}
		e.RemoveAndKeepContent()
		return nil
	})

	for _, tc := range []struct{ doc, want string }{
		{"<h1>a <em>b</h1><p>after</p>", "<h1>a b</h1><p>after</p>"},
		{"<h1><span>a <em>b</span> c</h1>", "<h1><span>a b</span> c</h1>"},
		// Where the end tag is the element's own there is nothing to repair, and
		// the guard has to know that or it doubles the tag.
		{"<h1>a <em>b</em></h1>", "<h1>a b</h1>"},
	} {
		got, err := lolhtml.RewriteString(tc.doc, repair)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheRepairedDocumentClosesItsElements reads the output back rather than
// comparing bytes: the failure this is about is a shape, so the check that means
// anything is that the paragraph is no longer inside the heading.
func TestTheRepairedDocumentClosesItsElements(t *testing.T) {
	const doc = "<h1>a <em>b</h1><p>after</p>"

	unwrapped, err := lolhtml.RewriteString(doc, lolhtml.OnElement("em", func(e *lolhtml.Element) error {
		e.RemoveAndKeepContent()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if depth := depthOfParagraph(t, unwrapped); depth != 1 {
		t.Errorf("without the guard the paragraph is at depth %d in %q, want 1 - "+
			"inside the heading", depth, unwrapped)
	}
	if depthOfParagraph(t, doc) != 0 {
		t.Fatal("the paragraph was not at the top level to begin with")
	}
}

// depthOfParagraph counts how many headings are open when the paragraph starts,
// which is 0 in a document whose heading closed.
func depthOfParagraph(t *testing.T, doc string) int {
	t.Helper()
	open, depth := 0, -1
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("h1", func(e *lolhtml.Element) error {
			open++
			return e.OnEndTag(func(*lolhtml.EndTag) error { open--; return nil })
		}),
		lolhtml.OnElement("p", func(*lolhtml.Element) error {
			if depth < 0 {
				depth = open
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if depth < 0 {
		t.Fatalf("no paragraph in %q", doc)
	}
	return depth
}

// TestTheDocumentedExamplesAreWhatHappens, so the three doc comments cannot drift
// from the library. Each is the exact shape written in the comment.
func TestTheDocumentedExamplesAreWhatHappens(t *testing.T) {
	for _, tc := range []struct {
		where, doc, want string
		opt              lolhtml.Option
	}{
		{"RemoveAndKeepContent", "<h1>a <em>b</h1><p>after</p>", "<h1>a b<p>after</p>",
			lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				e.RemoveAndKeepContent()
				return nil
			})},
		{"RemoveAndKeepContent", "<h1><span>a <em>b</span> c</h1>", "<h1><span>a b c</h1>",
			lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				e.RemoveAndKeepContent()
				return nil
			})},
		{"SetTagName", "<h1>a <em>b</h1>", "<h1>a <i>b</i>",
			lolhtml.OnElement("em", func(e *lolhtml.Element) error { return e.SetTagName("i") })},
		{"SetTagName", "<ul><li><em>a<li>b</ul>", "<ul><li><i>a<li>b</i>",
			lolhtml.OnElement("em", func(e *lolhtml.Element) error { return e.SetTagName("i") })},
		{"EndTag.Remove", "<h1>a <em>b</h1><p>after</p>", "<h1>a <em>b<p>after</p>",
			lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					t.Remove()
					return nil
				})
			})},
	} {
		got, err := lolhtml.RewriteString(tc.doc, tc.opt)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("the %s comment says %q\n         and it is %q", tc.where, tc.want, got)
		}
	}
}

// TestNothingBorrowedWhenTheDocumentIsWellFormed. The hazard needs an omitted end
// tag, so a document that writes them all is unaffected by any of this - which is
// why it is easy to ship a rewrite that is only tested on well-formed input.
func TestNothingBorrowedWhenTheDocumentIsWellFormed(t *testing.T) {
	const doc = "<div><h1>a <em>b</em></h1><ul><li><em>c</em></li><li>d</li></ul></div>"
	got, err := lolhtml.RewriteString(doc, lolhtml.OnElement("em", func(e *lolhtml.Element) error {
		e.RemoveAndKeepContent()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	const want = "<div><h1>a b</h1><ul><li>c</li><li>d</li></ul></div>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if strings.Count(got, "</") != strings.Count(want, "</") {
		t.Error("closing tags were lost from a well-formed document")
	}
}

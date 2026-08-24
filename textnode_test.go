package lolhtml_test

// A text node is not an element's text.
//
// OnText fires for every text chunk inside the matched element, descendants
// included, and IsLastInTextNode marks the end of a text node rather than the end
// of the element's content. Those coincide only when the element contains no
// markup - which is exactly what a first test case looks like, so the difference
// is easy to ship.
//
// This file pins the wrong-looking-right behaviour and the three recipes that
// actually work, because the choice between them is a real one: they differ in
// what happens to the descendant markup.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// textDocs are the shapes that tell the two boundaries apart.
var textDocs = []struct{ name, doc string }{
	{"no markup", `<a href="/x">click here</a>`},
	{"trailing markup", `<a href="/x">click <b>here</b></a>`},
	{"markup only", `<a href="/x"><b>click here</b></a>`},
	{"markup in the middle", `<a href="/x">click <b>here</b> now</a>`},
	{"void element first", `<a href="/x"><img src="/i" alt="a"> click here</a>`},
	{"alternating", `<a href="/x">a<b>b</b>c<i>d</i>e</a>`},
}

// TestTextNodeBoundaryIsPerNode is the behaviour, stated so that it cannot be
// mistaken for a bug in the recipes below. One replacement per text node.
func TestTextNodeBoundaryIsPerNode(t *testing.T) {
	want := map[string]string{
		"no markup":            `<a href="/x">REPLACED</a>`,
		"trailing markup":      `<a href="/x">REPLACED<b>REPLACED</b></a>`,
		"markup only":          `<a href="/x"><b>REPLACED</b></a>`,
		"markup in the middle": `<a href="/x">REPLACED<b>REPLACED</b>REPLACED</a>`,
		"void element first":   `<a href="/x"><img src="/i" alt="a">REPLACED</a>`,
		"alternating":          `<a href="/x">REPLACED<b>REPLACED</b>REPLACED<i>REPLACED</i>REPLACED</a>`,
	}

	for _, tt := range textDocs {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(tt.doc,
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					if !tc.IsLastInTextNode() {
						tc.Remove()
						return nil
					}
					return tc.Replace("REPLACED", lolhtml.Text)
				}))
			if err != nil {
				t.Fatal(err)
			}
			if got != want[tt.name] {
				t.Errorf("\n got: %s\nwant: %s", got, want[tt.name])
			}
		})
	}
}

// TestAccumulateAndFinishAtTheEndTag is the recipe for an element's text. The
// text comes out right; descendant elements are left behind as empty shells,
// because removing text does not remove markup.
func TestAccumulateAndFinishAtTheEndTag(t *testing.T) {
	want := map[string]string{
		"no markup":            `<a href="/x">[click here]</a>`,
		"trailing markup":      `<a href="/x"><b></b>[click here]</a>`,
		"markup only":          `<a href="/x"><b></b>[click here]</a>`,
		"markup in the middle": `<a href="/x"><b></b>[click here now]</a>`,
		"void element first":   `<a href="/x"><img src="/i" alt="a">[click here]</a>`,
		"alternating":          `<a href="/x"><b></b><i></i>[abcde]</a>`,
	}

	for _, tt := range textDocs {
		t.Run(tt.name, func(t *testing.T) {
			var acc strings.Builder
			got, err := lolhtml.RewriteString(tt.doc,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					acc.Reset()
					return e.OnEndTag(func(tag *lolhtml.EndTag) error {
						return tag.Before("["+strings.TrimSpace(acc.String())+"]", lolhtml.Text)
					})
				}),
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					acc.WriteString(tc.Text())
					tc.Remove()
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if got != want[tt.name] {
				t.Errorf("\n got: %s\nwant: %s", got, want[tt.name])
			}
		})
	}
}

// TestRemovingDescendantMarkupToo is the recipe when the whole content is to be
// replaced rather than only its text. RemoveAndKeepContent on the descendants
// takes their tags, and removing the text takes what was inside them.
func TestRemovingDescendantMarkupToo(t *testing.T) {
	want := map[string]string{
		"no markup":            `<a href="/x">[click here]</a>`,
		"trailing markup":      `<a href="/x">[click here]</a>`,
		"markup only":          `<a href="/x">[click here]</a>`,
		"markup in the middle": `<a href="/x">[click here now]</a>`,
		"void element first":   `<a href="/x">[click here]</a>`,
		"alternating":          `<a href="/x">[abcde]</a>`,
	}

	for _, tt := range textDocs {
		t.Run(tt.name, func(t *testing.T) {
			var acc strings.Builder
			got, err := lolhtml.RewriteString(tt.doc,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					acc.Reset()
					return e.OnEndTag(func(tag *lolhtml.EndTag) error {
						return tag.Before("["+strings.TrimSpace(acc.String())+"]", lolhtml.Text)
					})
				}),
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					acc.WriteString(tc.Text())
					tc.Remove()
					return nil
				}),
				lolhtml.OnElement("a *", func(e *lolhtml.Element) error {
					e.RemoveAndKeepContent()
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if got != want[tt.name] {
				t.Errorf("\n got: %s\nwant: %s", got, want[tt.name])
			}
		})
	}
}

// TestRebuildingTheElementAtItsEndTag is the third recipe: remove the element and
// write a new one where it was. It can change the tag and the attributes as well
// as the text, at the cost of re-serialising the attributes - and therefore of
// escaping them - by hand.
func TestRebuildingTheElementAtItsEndTag(t *testing.T) {
	for _, tt := range textDocs {
		t.Run(tt.name, func(t *testing.T) {
			var acc strings.Builder
			var href string

			got, err := lolhtml.RewriteString(tt.doc,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					acc.Reset()
					href, _ = e.Attribute("href")
					e.Remove()
					return e.OnEndTag(func(tag *lolhtml.EndTag) error {
						return tag.Before(fmt.Sprintf(`<a href="%s">[%s]</a>`,
							href, strings.TrimSpace(acc.String())), lolhtml.HTML)
					})
				}),
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					acc.WriteString(tc.Text())
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			// Every shape collapses to one clean anchor.
			if strings.Count(got, "<a ") != 1 || strings.Count(got, "</a>") != 1 {
				t.Errorf("expected exactly one anchor: %s", got)
			}
			if strings.Contains(got, "<b>") || strings.Contains(got, "<img") {
				t.Errorf("descendant markup survived: %s", got)
			}
		})
	}
}

// TestTheRecipesAreChunkInvariant: all three accumulate, and accumulation is
// where a chunk boundary would show.
func TestTheRecipesAreChunkInvariant(t *testing.T) {
	recipes := map[string]func(acc *strings.Builder) []lolhtml.Option{
		"per text node": func(acc *strings.Builder) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					if !tc.IsLastInTextNode() {
						tc.Remove()
						return nil
					}
					return tc.Replace("REPLACED", lolhtml.Text)
				}),
			}
		},
		"end tag": func(acc *strings.Builder) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					acc.Reset()
					return e.OnEndTag(func(tag *lolhtml.EndTag) error {
						return tag.Before("["+strings.TrimSpace(acc.String())+"]", lolhtml.Text)
					})
				}),
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					acc.WriteString(tc.Text())
					tc.Remove()
					return nil
				}),
			}
		},
	}

	for name, build := range recipes {
		t.Run(name, func(t *testing.T) {
			for _, tt := range textDocs {
				var acc strings.Builder
				whole, err := lolhtml.RewriteString(tt.doc, build(&acc)...)
				if err != nil {
					t.Fatal(err)
				}

				for _, chunk := range []int{1, 2, 3, 7} {
					var acc2 strings.Builder
					var out strings.Builder
					w, err := lolhtml.NewWriter(&out, build(&acc2)...)
					if err != nil {
						t.Fatal(err)
					}
					for i := 0; i < len(tt.doc); i += chunk {
						end := min(i+chunk, len(tt.doc))
						if _, err := w.Write([]byte(tt.doc[i:end])); err != nil {
							t.Fatal(err)
						}
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					if out.String() != whole {
						t.Errorf("%s at chunk %d:\n whole: %s\nchunks: %s",
							tt.name, chunk, whole, out.String())
					}
				}
			}
		})
	}
}

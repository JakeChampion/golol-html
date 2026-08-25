// Inserting as Text in the contexts where markup rules change.
//
// text_structure_test.go states that inserting with [lolhtml.Text] never changes a document's
// tags, "for any value, at any position, in any document". The documents it says that over are
// built from nine ordinary elements: div, p, span, a, b, i, ul, li, section. Every context where
// a parser's rules change - raw text, escapable raw text, foreign content, a template - was
// outside the space the property was checked on, which is to say outside the part of the promise
// anybody would doubt.
//
// So this file states the same property over those contexts. A value is inserted inside
// <script>, <style>, <title>, <textarea>, <xmp>, <svg>, <math>, <template>, a table, a select and
// a plaintext, at every position that takes a content type, and the tags have to come out the
// same.
//
// The tests that follow are the property, its converse - HTML in the same places can change the
// tags, so the first is not vacuous - and the documented asymmetry between the element paths,
// which refuse a raw-text breakout, and the text paths, which do not.
package properties

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"pgregory.net/rapid"
)

// hostileContext is a document with a marked element to insert into.
type hostileContext struct {
	Name string
	// Doc contains one element with class="target", which is where the insertion goes.
	Doc string
	// Tag is that element's name.
	Tag string
	// RawText says whether the target is a raw-text element, where an insertion could end
	// the element rather than merely sit in it.
	RawText bool
}

// hostileContexts are the places a parser's rules are not the ordinary ones.
var hostileContexts = []hostileContext{
	{"a script", `<p>before</p><script class="target">var a = 1;</script><p>after</p>`, "script", true},
	{"a style", `<p>before</p><style class="target">p{color:red}</style><p>after</p>`, "style", true},
	{"a title", `<head><title class="target">t</title></head><body><p>after</p></body>`, "title", true},
	{"a textarea", `<p>before</p><textarea class="target">t</textarea><p>after</p>`, "textarea", true},
	{"an xmp", `<p>before</p><xmp class="target">t</xmp><p>after</p>`, "xmp", true},
	{"svg", `<svg class="target"><circle r="1"/></svg><p>after</p>`, "svg", false},
	{"a foreignObject", `<svg><foreignObject class="target"><p>in</p></foreignObject></svg>`, "foreignObject", false},
	{"mathml", `<math class="target"><mi>x</mi></math><p>after</p>`, "math", false},
	{"a template", `<template class="target"><tr><td>x</td></tr></template><p>after</p>`, "template", false},
	{"a table", `<table class="target"><tr><td>x</td></tr></table><p>after</p>`, "table", false},
	{"a table cell", `<table><tr><td class="target">x</td></tr></table>`, "td", false},
	{"a paragraph", `<p class="target">text</p><p>after</p>`, "p", false},
	{"a select", `<select class="target"><option>x</option></select><p>after</p>`, "select", false},
	// plaintext is raw text and has no end tag at all, so nothing can break out of it -
	// which is why the breakout test skips it rather than expecting a refusal.
	{"a plaintext", `<p>before</p><plaintext class="target">t`, "plaintext", true},
}

// insertionKind is one of the positions that takes a content type.
type insertionKind struct {
	Name string
	Do   func(*lolhtml.Element, string, lolhtml.ContentType) error
}

var insertionKinds = []insertionKind{
	{"Before", func(e *lolhtml.Element, v string, ct lolhtml.ContentType) error { return e.Before(v, ct) }},
	{"After", func(e *lolhtml.Element, v string, ct lolhtml.ContentType) error { return e.After(v, ct) }},
	{"Prepend", func(e *lolhtml.Element, v string, ct lolhtml.ContentType) error { return e.Prepend(v, ct) }},
	{"Append", func(e *lolhtml.Element, v string, ct lolhtml.ContentType) error { return e.Append(v, ct) }},
}

// genInsertedValue produces the values most likely to end an element or begin markup, including
// the raw-text terminators.
func genInsertedValue() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{
			"", "x", "<", ">", "&", "<b>", "</b>", "<script>", "</script>", "</script >",
			"</style>", "</title>", "</textarea>", "</xmp>", "</plaintext>",
			"<!--", "-->", "]]>", "<![CDATA[x]]>", "<svg>", "</svg>", "<td>", "</td>",
			`" onmouseover="x`, "&lt;script&gt;", "&amp;", " ", "</p><table><tr><td>",
			"<template>", "</template>",
		}),
		rapid.StringN(0, 10, 10),
	)
}

// tagsOf reports the sequence of tags the rewriter sees, which is the level the library is
// answerable for - a tree is built by rules that respond to text, and that is a parser
// behaviour rather than a library one.
func tagsOf(doc string) (string, error) {
	var sb strings.Builder
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			sb.WriteString("<" + e.TagName() + ">")
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			sb.WriteString("!")
			return nil
		}),
	); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// insertAt inserts value into the target element of doc and returns the output and any refusal.
// A refusal is returned rather than failed on: whether a path refuses is a subject in itself.
func insertAt(doc string, kind insertionKind, value string, ct lolhtml.ContentType) (string, error) {
	var out strings.Builder
	var insertErr error
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement(".target", func(e *lolhtml.Element) error {
		if err := kind.Do(e, value, ct); err != nil {
			insertErr = err
		}
		return nil
	}))
	if err != nil {
		return "", err
	}
	if _, err := strings.NewReader(doc).WriteTo(w); err != nil {
		w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), insertErr
}

// TestInsertingAsTextNeverChangesTheTagsInAHostileContext - the property, over the contexts the
// original one left out.
func TestInsertingAsTextNeverChangesTheTagsInAHostileContext(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := rapid.SampledFrom(hostileContexts).Draw(t, "context")
		kind := rapid.SampledFrom(insertionKinds).Draw(t, "kind")
		value := genInsertedValue().Draw(t, "value")

		before, err := tagsOf(ctx.Doc)
		if err != nil {
			t.Fatalf("%s: reading the tags of %q: %v", ctx.Name, ctx.Doc, err)
		}

		out, insertErr := insertAt(ctx.Doc, kind, value, lolhtml.Text)
		if insertErr != nil {
			// Text is never refused: escaping means the content cannot close the
			// element, so the raw-text guard has nothing to object to.
			t.Fatalf("%s: %s of %q as Text was refused: %v",
				ctx.Name, kind.Name, value, insertErr)
		}
		after, err := tagsOf(out)
		if err != nil {
			t.Fatalf("%s: reading the tags of %q: %v", ctx.Name, out, err)
		}
		if after != before {
			t.Fatalf("%s: %s of %q as Text changed the tags\n before: %s\n after:  %s\n out: %q",
				ctx.Name, kind.Name, value, before, after, out)
		}
	})
}

// TestInsertingAsHTMLInAHostileContextCanChangeTheTags, which is what makes the property above
// worth stating: the escaping is doing the work rather than the position being harmless.
//
// A search rather than a property: it looks for the cases where HTML changes the tags and where
// it is refused, and fails if it finds none of either - which would mean these contexts are not
// hostile after all.
func TestInsertingAsHTMLInAHostileContextCanChangeTheTags(t *testing.T) {
	var changed, refused int

	for _, ctx := range hostileContexts {
		before, err := tagsOf(ctx.Doc)
		if err != nil {
			t.Fatalf("%s: %v", ctx.Name, err)
		}
		for _, kind := range insertionKinds {
			for _, value := range []string{"<b>x</b>", "</script><b>x</b>", "<td>x", "<svg>"} {
				out, insertErr := insertAt(ctx.Doc, kind, value, lolhtml.HTML)
				if insertErr != nil {
					refused++
					continue
				}
				after, err := tagsOf(out)
				if err != nil {
					t.Fatalf("%s: %v", ctx.Name, err)
				}
				if after != before {
					changed++
				}
			}
		}
	}

	if changed == 0 {
		t.Error("no HTML insertion changed the tags in any hostile context, so the Text " +
			"property above is not saying anything")
	}
	if refused == 0 {
		t.Error("no HTML insertion was refused, so the raw-text guard is not being reached")
	}
	t.Logf("HTML insertions: %d changed the tags, %d were refused", changed, refused)
}

// TestTheRawTextGuardIsOnTheElementPathsOnly, which is documented and is the one asymmetry a
// program has to know about: an insertion through an Element method is refused when it would
// close a raw-text element, and the same content through a TextChunk method is not.
func TestTheRawTextGuardIsOnTheElementPathsOnly(t *testing.T) {
	for _, ctx := range hostileContexts {
		if !ctx.RawText {
			continue
		}
		// plaintext has no end tag, so no content can close it and there is nothing for
		// the guard to refuse. The library reports it as raw text all the same, which is
		// right: its content is not markup.
		if ctx.Tag == "plaintext" {
			continue
		}
		// The payload has to end *this* element: "</script>" does nothing inside a
		// <style>, which is what made the first version of this test wrong.
		payload := `var a = "</` + ctx.Tag + `><img src=x onerror=alert(1)>";`
		t.Run(ctx.Name, func(t *testing.T) {
			// Through the element: refused, because the content would end the element.
			appendKind := insertionKinds[3]
			_, insertErr := insertAt(ctx.Doc, appendKind, payload, lolhtml.HTML)
			if !errors.Is(insertErr, lolhtml.ErrRawTextBreakout) {
				t.Errorf("appending a breakout through the element reported %v, want "+
					"ErrRawTextBreakout", insertErr)
			}

			// Through the text chunk: not refused. The library documents this and
			// exports CheckRawText for a caller to apply itself.
			var out strings.Builder
			w, err := lolhtml.NewWriter(&out,
				lolhtml.OnText(".target", func(c *lolhtml.TextChunk) error {
					if c.Text() == "" {
						return nil
					}
					return c.Replace(payload, lolhtml.HTML)
				}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := strings.NewReader(ctx.Doc).WriteTo(w); err != nil {
				w.Close()
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "onerror") {
				t.Errorf("the text path refused or altered the payload: %q", out.String())
			}

			// And CheckRawText is what closes the gap, which is why it is exported.
			if err := lolhtml.CheckRawText(ctx.Tag, payload); err == nil {
				t.Errorf("CheckRawText(%q) accepted a payload that ends the element", ctx.Tag)
			}
		})
	}
}

// TestEveryHostileContextHasATargetThatMatches, so a typo in the table cannot quietly remove a
// context from the property.
func TestEveryHostileContextHasATargetThatMatches(t *testing.T) {
	for _, ctx := range hostileContexts {
		matched := 0
		var tags []string
		if _, err := lolhtml.RewriteString(ctx.Doc,
			lolhtml.OnElement(".target", func(e *lolhtml.Element) error {
				matched++
				tags = append(tags, e.TagName())
				return nil
			})); err != nil {
			t.Errorf("%s: %v", ctx.Name, err)
			continue
		}
		if matched != 1 {
			t.Errorf("%s: the target matched %d times in %q", ctx.Name, matched, ctx.Doc)
			continue
		}
		if tags[0] != strings.ToLower(ctx.Tag) {
			t.Errorf("%s: the target is <%s>, and the table says <%s>", ctx.Name, tags[0], ctx.Tag)
		}
		if ctx.RawText != lolhtml.IsRawText(tags[0]) {
			t.Errorf("%s: the table says RawText=%v and the library says %v",
				ctx.Name, ctx.RawText, lolhtml.IsRawText(tags[0]))
		}
	}
	if len(hostileContexts) < 10 {
		t.Errorf("%d contexts: the point of this file is breadth", len(hostileContexts))
	}
}

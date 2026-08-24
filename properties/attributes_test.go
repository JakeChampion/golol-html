package properties

// Properties of an attribute-only rewrite.
//
// Adding, changing or removing attributes is the commonest thing done with this
// library, and two claims about it are worth checking over generated documents
// rather than a handful of examples.
//
// The first is about duplicates. The shared generator deliberately avoids them,
// with a comment saying they are not round-trippable, so the claim needs its own
// document builder: a name planted several times on one element, and every copy
// gone afterwards. Getting that wrong leaves a stale value behind that a browser
// may prefer to the new one, which is a filter that does not filter.
//
// The second is that an attribute-only rewrite does not move anything. Attributes
// carry no structural meaning, so parsing the input and the output should give
// the same tree - and the generator produces invalid nesting on purpose, where a
// parser's error recovery relocates nodes, which is exactly where a rewrite that
// disturbed the markup would show.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
	"pgregory.net/rapid"
)

// genDuplicateAttrDoc builds one element carrying the same attribute name
// several times, which the shared generator will not produce.
func genDuplicateAttrDoc() *rapid.Generator[struct {
	doc   string
	name  string
	count int
}] {
	return rapid.Custom(func(t *rapid.T) struct {
		doc   string
		name  string
		count int
	} {
		tag := rapid.SampledFrom([]string{"div", "p", "span", "a"}).Draw(t, "tag")
		name := rapid.SampledFrom(attrNames).Draw(t, "name")
		count := rapid.IntRange(1, 4).Draw(t, "count")

		var b strings.Builder
		fmt.Fprintf(&b, "<%s", tag)
		for i := 0; i < count; i++ {
			// Values differ so that a rewrite keeping the wrong copy is visible.
			fmt.Fprintf(&b, ` %s="v%d"`, name, i)
		}
		fmt.Fprintf(&b, ">text</%s>", tag)

		return struct {
			doc   string
			name  string
			count int
		}{b.String(), name, count}
	})
}

// TestRemoveAttributeRemovesEveryCopy. A browser uses the first of a repeated
// attribute, so leaving any copy behind leaves the old value in force.
func TestRemoveAttributeRemovesEveryCopy(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := genDuplicateAttrDoc().Draw(t, "case")

		out, err := lolhtml.RewriteString(c.doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.RemoveAttribute(c.name)
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}

		if strings.Contains(out, c.name+"=") {
			t.Fatalf("%d copies of %q, and at least one survived\n in: %q\nout: %q",
				c.count, c.name, c.doc, out)
		}

		// And the element itself is still there: removing an attribute must not
		// remove content.
		if !strings.Contains(out, ">text<") {
			t.Fatalf("the element's content was lost\n in: %q\nout: %q", c.doc, out)
		}
	})
}

// TestSetAttributeReplacesTheFirstCopyOnly records the other half, which is
// asymmetric and worth pinning as a property rather than an example: a browser
// reads the first, so the rewrite is effective, but the later copies remain in
// the markup.
func TestSetAttributeReplacesTheFirstCopyOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := genDuplicateAttrDoc().Draw(t, "case")

		out, err := lolhtml.RewriteString(c.doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.SetAttribute(c.name, "new")
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}

		// The first copy carries the new value.
		first := strings.Index(out, c.name+"=")
		if first < 0 {
			t.Fatalf("the attribute is gone entirely\n in: %q\nout: %q", c.doc, out)
		}
		if !strings.HasPrefix(out[first:], c.name+`="new"`) {
			t.Fatalf("the first copy was not the one replaced\n in: %q\nout: %q", c.doc, out)
		}
		// And exactly as many copies remain as there were.
		if got := strings.Count(out, c.name+"="); got != c.count {
			t.Fatalf("%d copies before, %d after\n in: %q\nout: %q",
				c.count, got, c.doc, out)
		}
	})
}

// shape renders a parsed tree's structure with attributes omitted, so two
// documents that differ only in attributes compare equal.
func shape(n *html.Node, sb *strings.Builder) {
	switch n.Type {
	case html.ElementNode:
		sb.WriteString("<" + n.Data + ">")
	case html.TextNode:
		// Presence and content, not attributes.
		sb.WriteString("#" + n.Data)
	case html.CommentNode:
		sb.WriteString("!" + n.Data)
	case html.DoctypeNode:
		sb.WriteString("D" + n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		shape(c, sb)
	}
	if n.Type == html.ElementNode {
		sb.WriteString("</" + n.Data + ">")
	}
}

func shapeOf(t *rapid.T, doc string) string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var sb strings.Builder
	shape(root, &sb)
	return sb.String()
}

// TestAttributeOnlyRewritePreservesStructure: attributes carry no structural
// meaning, so a rewrite that only touches them must leave the tree an
// independent parser sees exactly as it was - including the parts the parser
// relocated through error recovery, which the generator produces on purpose.
func TestAttributeOnlyRewritePreservesStructure(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")
		val := genString().Draw(t, "value")

		for _, tt := range []struct {
			name string
			opt  lolhtml.Option
		}{{
			name: "set",
			opt: lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-mark", val)
			}),
		}, {
			name: "remove",
			opt: lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				for _, n := range attrNames {
					if err := e.RemoveAttribute(n); err != nil {
						return err
					}
				}
				return nil
			}),
		}, {
			name: "read only",
			opt: lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				for range e.Attributes() {
				}
				return nil
			}),
		}} {
			out, err := lolhtml.RewriteString(doc, tt.opt)
			if err != nil {
				t.Fatalf("%s: rewrite: %v", tt.name, err)
			}
			if before, after := shapeOf(t, doc), shapeOf(t, out); before != after {
				t.Fatalf("%s changed the document's structure\n   in: %q\n  out: %q\nbefore: %s\n after: %s",
					tt.name, doc, out, before, after)
			}
		}
	})
}

// TestRemovingAnAbsentAttributeIsANoOp: a filter runs over every element and
// most of them do not have the attribute it is removing, so this is the common
// case rather than an edge one.
func TestRemovingAnAbsentAttributeIsANoOp(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")

		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				// A name the generator never produces.
				return e.RemoveAttribute("data-not-present-anywhere")
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if out != doc {
			t.Fatalf("removing an absent attribute changed the document\n in: %q\nout: %q", doc, out)
		}
	})
}

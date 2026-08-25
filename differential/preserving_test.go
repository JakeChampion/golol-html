package differential

// Which rewrites preserve meaning, and which do not.
//
// A rewrite that only means to add a class or an attribute should leave the tree exactly
// as it was. Some do, on every shape a document can take - and some cannot, for reasons
// this suite documents one at a time: foster parenting, an implied end tag, a content
// model that rejects what it is given. This file is the two halves side by side, so a
// rewrite that moved from one to the other would fail a test rather than a page.
//
// The comparison is the tree from golang.org/x/net/html with the intended change taken
// out: the attribute the rewrite adds is ignored, a renamed element is equated with its
// old name, and inserted comments are dropped. What is left has to match, node for node.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// shapes are the documents worth trying, chosen because each is a place a streaming
// rewrite can go wrong: implied end tags, foster parenting, a template's own parse rules,
// foreign content, a form in a table.
var shapes = []string{
	`<div><p>x</p></div>`,
	`<table><tr><td>a</td></tr></table>`,
	`<table><tbody><tr><td>a</td></tr></tbody></table>`,
	`<select><option>a</option></select>`,
	`<ul><li>a<li>b</ul>`,
	`<p>a<b>b</b>c</p>`,
	`<head><title>t</title></head><body><p>x</p></body>`,
	`<dl><dt>a<dd>b</dl>`,
	`<template><tr><td>x</td></tr></template>`,
	`<svg><circle r="1"/></svg>`,
	`<p>a<img src="x">b</p>`,
	`<table>stray<tr><td>a</table>`,
	`<form><input name="a"></form>`,
	`<table><form><tr><td>a</table></form>`,
}

// comparison is how to ignore what a rewrite meant to do.
type comparison struct {
	ignoreAttrs    map[string]bool
	ignoreComments bool
	equate         map[string]string
}

// treeOf renders the tree with the intended change taken out.
func treeOf(t *testing.T, doc string, c comparison) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var b strings.Builder
	var walk func(*html.Node, int)
	walk = func(n *html.Node, d int) {
		switch n.Type {
		case html.ElementNode:
			name := n.Data
			if to, ok := c.equate[name]; ok {
				name = to
			}
			var attrs []string
			for _, a := range n.Attr {
				if c.ignoreAttrs[a.Key] {
					continue
				}
				attrs = append(attrs, a.Key+"="+a.Val)
			}
			sort.Strings(attrs)
			fmt.Fprintf(&b, "%s%s[%s]\n", strings.Repeat(" ", d), name, strings.Join(attrs, " "))
		case html.TextNode:
			if s := strings.TrimSpace(n.Data); s != "" {
				fmt.Fprintf(&b, "%s#%s\n", strings.Repeat(" ", d), s)
			}
		case html.CommentNode:
			if !c.ignoreComments {
				fmt.Fprintf(&b, "%s<!--%s-->\n", strings.Repeat(" ", d), n.Data)
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch, d+1)
		}
	}
	walk(root, 0)
	return b.String()
}

// preserving are the rewrites that leave the tree alone on every shape above.
var preserving = []struct {
	name string
	opts func() []lolhtml.Option
	cmp  comparison
}{
	{
		name: "set an attribute on every element",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-seen", "1")
			})}
		},
		cmp: comparison{ignoreAttrs: map[string]bool{"data-seen": true}},
	},
	{
		name: "add a class to every element",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				c, _ := e.Attribute("class")
				return e.SetAttribute("class", strings.TrimSpace(c+" m"))
			})}
		},
		cmp: comparison{ignoreAttrs: map[string]bool{"class": true}},
	},
	{
		name: "rename b to strong",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
				return e.SetTagName("strong")
			})}
		},
		cmp: comparison{equate: map[string]string{"strong": "b"}},
	},
	{
		name: "insert a comment before every element",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.Before("<!--m-->", lolhtml.HTML)
			})}
		},
		cmp: comparison{ignoreComments: true},
	},
	{
		name: "insert a comment after every element, guarded by the end-tag name",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				if !e.CanHaveContent() {
					return e.After("<!--m-->", lolhtml.HTML)
				}
				tag := e.TagName()
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					if x.Name() != tag {
						return nil // not this element's end: see the end-tag rule
					}
					return x.After("<!--m-->", lolhtml.HTML)
				})
			})}
		},
		cmp: comparison{ignoreComments: true},
	},
	{
		name: "read everything and change nothing",
		opts: func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				_ = e.AttributeList()
				_, _ = e.Attribute("class")
				return nil
			})}
		},
		cmp: comparison{},
	},
}

// TestThePreservingRewritesPreserveTheTree, on every shape.
func TestThePreservingRewritesPreserveTheTree(t *testing.T) {
	for _, r := range preserving {
		for _, doc := range shapes {
			out, err := lolhtml.RewriteString(doc, r.opts()...)
			if err != nil {
				t.Errorf("%s on %q: %v", r.name, doc, err)
				continue
			}
			before, after := treeOf(t, doc, r.cmp), treeOf(t, out, r.cmp)
			if before != after {
				t.Errorf("%s changed the tree of %q\noutput: %s\nbefore:\n%safter:\n%s",
					r.name, doc, out, before, after)
			}
		}
	}
}

// TestTheHazardsStillChangeTheTree is the other half, and the reason the first half is
// worth asserting: each of these is a documented way for a rewrite to mean more than it
// says, and if one stopped doing it the documentation would be wrong.
func TestTheHazardsStillChangeTheTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		opts func() []lolhtml.Option
		cmp  comparison
	}{
		{
			name: "a div wrapper inside a paragraph",
			doc:  `<p>text <img src="a"> more</p>`,
			opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("img", func(e *lolhtml.Element) error {
					if err := e.Before(`<div class="w">`, lolhtml.HTML); err != nil {
						return err
					}
					return e.After(`</div>`, lolhtml.HTML)
				})}
			},
			cmp: comparison{ignoreAttrs: map[string]bool{"class": true}},
		},
		{
			name: "prepending an element into a template holding rows",
			doc:  `<table><template><tr><td>x</td></tr></template></table>`,
			opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("template", func(e *lolhtml.Element) error {
					return e.Prepend(`<input hidden="">`, lolhtml.HTML)
				})}
			},
		},
		{
			name: "renaming a div to a table",
			doc:  `<div><p>x</p></div>`,
			opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
					return e.SetTagName("table")
				})}
			},
			cmp: comparison{equate: map[string]string{"table": "div"}},
		},
		{
			name: "appending to a list item whose end tag was omitted",
			doc:  `<ul><li>a<li>b</ul>`,
			opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("li", func(e *lolhtml.Element) error {
					return e.Append("<span>m</span>", lolhtml.HTML)
				})}
			},
		},
		{
			name: "prepending an element into a table",
			doc:  `<table><tbody><tr><td>a</td></tr></tbody></table>`,
			opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("tbody", func(e *lolhtml.Element) error {
					return e.Prepend(`<input hidden="">`, lolhtml.HTML)
				})}
			},
		},
	} {
		out, err := lolhtml.RewriteString(tc.doc, tc.opts()...)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		before, after := treeOf(t, tc.doc, tc.cmp), treeOf(t, out, tc.cmp)
		if before == after {
			t.Errorf("%s no longer changes the tree of %q, so the documentation about it "+
				"needs revisiting\noutput: %s", tc.name, tc.doc, out)
		}
	}
}

// TestTheGuardIsWhatMakesTheAfterInsertionSafe: the same rewrite without the end-tag name
// check does change the tree, which is the difference the end-tag rule is about.
func TestTheGuardIsWhatMakesTheAfterInsertionSafe(t *testing.T) {
	const doc = `<ul><li>a<li>b</ul>`
	cmp := comparison{ignoreComments: true}

	guarded, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		tag := e.TagName()
		return e.OnEndTag(func(x *lolhtml.EndTag) error {
			if x.Name() != tag {
				return nil
			}
			return x.After("<!--m-->", lolhtml.HTML)
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if before, after := treeOf(t, doc, cmp), treeOf(t, guarded, cmp); before != after {
		t.Errorf("the guarded insertion changed the tree\nbefore:\n%safter:\n%s", before, after)
	}

	unguarded, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		return e.After("<span>m</span>", lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if before, after := treeOf(t, doc, comparison{}), treeOf(t, unguarded, comparison{}); before == after {
		t.Errorf("the unguarded insertion left the tree alone, which the end-tag rule says "+
			"it should not: %s", unguarded)
	}
}

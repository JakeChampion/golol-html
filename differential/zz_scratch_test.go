package differential

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// zzShape renders the tree, optionally ignoring what a rewrite was meant to add.
func zzShape(t *testing.T, doc string, ignoreAttrs map[string]bool, ignoreComments bool, equate map[string]string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var walk func(*html.Node, int)
	walk = func(n *html.Node, d int) {
		switch n.Type {
		case html.ElementNode:
			name := n.Data
			if to, ok := equate[name]; ok {
				name = to
			}
			var attrs []string
			for _, a := range n.Attr {
				if ignoreAttrs[a.Key] {
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
			if !ignoreComments {
				fmt.Fprintf(&b, "%s<!--%s-->\n", strings.Repeat(" ", d), n.Data)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, d+1)
		}
	}
	walk(root, 0)
	return b.String()
}

func TestZZScratchMeaningPreserving(t *testing.T) {
	docs := []string{
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
	rewrites := []struct {
		name    string
		opts    func() []lolhtml.Option
		ignore  map[string]bool
		comment bool
		equate  map[string]string
	}{
		{"set a data attribute on every element", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-seen", "1")
			})}
		}, map[string]bool{"data-seen": true}, false, nil},
		{"add a class to every element", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				c, _ := e.Attribute("class")
				return e.SetAttribute("class", strings.TrimSpace(c+" m"))
			})}
		}, map[string]bool{"class": true}, false, nil},
		{"rename b to strong", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
				return e.SetTagName("strong")
			})}
		}, nil, false, map[string]string{"strong": "b"}},
		{"insert a comment before every element", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.Before("<!--m-->", lolhtml.HTML)
			})}
		}, nil, true, nil},
		{"insert a comment after every element", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				if !e.CanHaveContent() {
					return e.After("<!--m-->", lolhtml.HTML)
				}
				tag := e.TagName()
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					if x.Name() != tag {
						return nil
					}
					return x.After("<!--m-->", lolhtml.HTML)
				})
			})}
		}, nil, true, nil},
		{"remove nothing, read everything", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				_ = e.AttributeList()
				return nil
			})}
		}, nil, false, nil},
	}
	for _, r := range rewrites {
		for _, doc := range docs {
			out, err := lolhtml.RewriteString(doc, r.opts()...)
			if err != nil {
				t.Errorf("%s on %q: %v", r.name, doc, err)
				continue
			}
			before := zzShape(t, doc, r.ignore, r.comment, r.equate)
			after := zzShape(t, out, r.ignore, r.comment, r.equate)
			if before != after {
				t.Errorf("!! %s changed the tree of %q\n out: %s\nbefore:\n%safter:\n%s",
					r.name, doc, out, before, after)
			}
		}
	}
}

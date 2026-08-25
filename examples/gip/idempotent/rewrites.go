package main

import (
	stdhtml "html"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Documents is the corpus. Small on purpose: what matters is whether a second
// pass changes anything, and that shows up in a paragraph as well as in a page.
func Documents() map[string]string {
	return map[string]string{
		"empty":          ``,
		"one link":       `<a href="/x">l</a>`,
		"two links":      `<a href="/x">one</a><a href="/y">two</a>`,
		"external link":  `<a href="https://elsewhere.example/x" target="_blank">l</a>`,
		"page":           `<!DOCTYPE html><html><head><title>t</title></head><body><p>text</p><a href="/x">l</a></body></html>`,
		"text with lt":   `<p>a < b &amp; c</p>`,
		"nested markup":  `<p>one <b>two</b> three</p>`,
		"already banner": `<body><div class="banner">hi</div><p>x</p></body>`,
	}
}

// Rewrites are the shapes worth measuring: one of each kind of operation, and
// both the guarded and unguarded version of the ones that add.
func Rewrites() []Rewrite {
	return []Rewrite{
		{
			Name:       "SetAttribute",
			Idempotent: true,
			Why:        "writing the same value twice is writing it once",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
					return e.SetAttribute("rel", "noopener")
				})}
			},
		},
		{
			Name:       "RemoveAttribute",
			Idempotent: true,
			Why:        "the attribute is gone after the first pass",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("a[target]", func(e *lolhtml.Element) error {
					return e.RemoveAttribute("target")
				})}
			},
		},
		{
			Name:       "SetTagName",
			Idempotent: true,
			Why:        "the second pass does not match the renamed element",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
					return e.SetTagName("strong")
				})}
			},
		},
		{
			Name:       "Remove",
			Idempotent: true,
			Why:        "there is nothing left to remove",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("title", func(e *lolhtml.Element) error {
					e.Remove()
					return nil
				})}
			},
		},
		{
			Name:       "SetInnerContent",
			Idempotent: true,
			Why:        "the content is replaced with the same thing",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					return e.SetInnerContent("replaced", lolhtml.Text)
				})}
			},
		},
		{
			Name:       "Append, unguarded",
			Idempotent: false,
			Why:        "each pass adds another copy",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("body", func(e *lolhtml.Element) error {
					return e.Append(`<div class="banner">hi</div>`, lolhtml.HTML)
				})}
			},
		},
		{
			Name:       "Append, guarded",
			Idempotent: true,
			Why:        "the marker attribute is on the element being appended to, so it is known before the position",
			Opts:       guardedAppend,
		},
		{
			Name:       "Before, unguarded",
			Idempotent: false,
			Why:        "each pass adds another comment",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
					return e.Before("<!-- link -->", lolhtml.HTML)
				})}
			},
		},
		{
			Name:       "text through Text",
			Idempotent: false,
			Why:        "TextChunk.Text is source, so the second pass escapes what the first pass escaped",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
					if len(c.Bytes()) == 0 {
						return nil
					}
					return c.Replace(strings.ToUpper(c.Text()), lolhtml.Text)
				})}
			},
		},
		{
			Name:       "text decoded first",
			Idempotent: true,
			Why:        "decode, transform, insert as Text: the only round trip that is right on the first pass as well as the second",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
					if len(c.Bytes()) == 0 {
						return nil
					}
					return c.Replace(strings.ToUpper(stdhtml.UnescapeString(c.Text())), lolhtml.Text)
				})}
			},
		},
		{
			Name:       "text through HTML",
			Idempotent: true,
			Why:        "source in, source out, so nothing is escaped twice",
			Opts: func() []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
					if len(c.Bytes()) == 0 {
						return nil
					}
					return c.Replace(strings.ToUpper(c.Text()), lolhtml.HTML)
				})}
			},
		},
	}
}

// guardedAppend is the interesting one. The guard cannot look ahead for the
// banner it inserted last time, because by the time the banner would be seen the
// position to insert at has gone past. So the marker goes on the element that
// owns the position - the body - where it is readable at the start tag, before
// anything is written.
func guardedAppend() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("body", func(e *lolhtml.Element) error {
		if _, ok := e.Attribute("data-banner"); ok {
			return nil
		}
		if err := e.SetAttribute("data-banner", "1"); err != nil {
			return err
		}
		return e.Append(`<div class="banner">hi</div>`, lolhtml.HTML)
	})}
}

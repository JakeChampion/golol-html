// Package differential compares golol-html's rewriting against an independent
// HTML parser.
//
// The rewriter and golang.org/x/net/html share no code: lol-html is Rust built
// on its own tokenizer, x/net/html is a separate Go implementation of the same
// WHATWG spec. So if a rewrite that should preserve meaning produces a document
// that x/net/html reads differently, one of them is wrong - and that is worth
// knowing about, whichever it is.
package differential

import (
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// canonical renders a parsed tree to a normalised string, so two documents that
// mean the same thing compare equal even when their markup differs.
//
// Attributes are sorted, because attribute order carries no meaning and a
// rewriter is free to change it. Everything else is kept verbatim: whitespace in
// text is significant in <pre>, so normalising it would hide real damage.
func canonical(n *html.Node) string {
	var sb strings.Builder
	writeCanonical(&sb, n)
	return sb.String()
}

func writeCanonical(sb *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeCanonical(sb, c)
		}

	case html.DoctypeNode:
		sb.WriteString("<!DOCTYPE ")
		sb.WriteString(n.Data)
		sb.WriteString(">")

	case html.ElementNode:
		sb.WriteString("<")
		if n.Namespace != "" {
			// Foreign content: an <svg><a> is not an HTML <a>.
			sb.WriteString(n.Namespace)
			sb.WriteString(":")
		}
		sb.WriteString(n.Data)
		for _, a := range sortedAttrs(n.Attr) {
			sb.WriteString(" ")
			if a.Namespace != "" {
				sb.WriteString(a.Namespace)
				sb.WriteString(":")
			}
			sb.WriteString(a.Key)
			sb.WriteString("=")
			sb.WriteString(strconv.Quote(a.Val))
		}
		sb.WriteString(">")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeCanonical(sb, c)
		}
		sb.WriteString("</")
		sb.WriteString(n.Data)
		sb.WriteString(">")

	case html.TextNode:
		sb.WriteString(n.Data)

	case html.CommentNode:
		sb.WriteString("<!--")
		sb.WriteString(n.Data)
		sb.WriteString("-->")
	}
}

func sortedAttrs(attrs []html.Attribute) []html.Attribute {
	out := make([]html.Attribute, len(attrs))
	copy(out, attrs)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// parseCanonical parses doc and returns its normalised form.
func parseCanonical(doc string) (string, error) {
	n, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return "", err
	}
	return canonical(n), nil
}

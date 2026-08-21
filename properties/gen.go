// Package properties states things that should be true of every document,
// rather than of the handful in a test table.
//
// The generator builds well-formed HTML trees and serialises them, rather than
// producing random bytes. Random bytes mostly exercise the parser's error
// recovery; generated trees get past parsing and into the rewriting, which is
// the part these bindings are responsible for. rapid shrinks a failure to a
// minimal document, which is what makes a counter-example readable.
package properties

import (
	"fmt"
	stdhtml "html"
	"strings"

	"pgregory.net/rapid"
)

// node is a small HTML tree. Exactly one of tag, text or comment is set.
type node struct {
	tag     string
	attrs   [][2]string
	kids    []node
	text    string
	comment string
}

// Only non-void elements, so serialisation is uniform: a void element would
// need to skip its closing tag, and that is not what these properties are
// about.
var tags = []string{"div", "p", "span", "a", "b", "i", "ul", "li", "section"}

var attrNames = []string{"id", "class", "href", "title", "data-x", "rel"}

// genString produces values that include the characters most likely to break
// escaping: quotes, angle brackets, ampersands, and things that look like
// markup or entities.
func genString() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{
			"", "x", "hello world", `"`, `'`, "<", ">", "&",
			"&amp;", "&lt;", `<script>`, `"><script>`, "-->", "]]>",
			"café", "☃", "日本語", "a\nb", "a\tb", "  ",
		}),
		rapid.StringN(0, 12, 12),
	)
}

func genAttrs() *rapid.Generator[[][2]string] {
	return rapid.Custom(func(t *rapid.T) [][2]string {
		n := rapid.IntRange(0, 3).Draw(t, "attrCount")
		seen := map[string]bool{}
		var out [][2]string
		for i := range n {
			name := rapid.SampledFrom(attrNames).Draw(t, fmt.Sprintf("attrName%d", i))
			if seen[name] {
				continue // duplicate attributes are not round-trippable
			}
			seen[name] = true
			out = append(out, [2]string{name, genString().Draw(t, fmt.Sprintf("attrValue%d", i))})
		}
		return out
	})
}

// genNode builds a tree, narrowing the branching as depth increases so
// documents stay a readable size.
func genNode(depth int) *rapid.Generator[node] {
	return rapid.Custom(func(t *rapid.T) node {
		kind := rapid.IntRange(0, 9).Draw(t, "kind")
		switch {
		case kind <= 1 || depth <= 0:
			return node{text: genString().Draw(t, "text")}
		case kind == 2:
			// Comments cannot contain "--", so use a restricted alphabet.
			return node{comment: rapid.StringMatching(`[a-z ]{0,10}`).Draw(t, "comment")}
		default:
			n := node{
				tag:   rapid.SampledFrom(tags).Draw(t, "tag"),
				attrs: genAttrs().Draw(t, "attrs"),
			}
			kids := rapid.IntRange(0, 3).Draw(t, "kidCount")
			for range kids {
				n.kids = append(n.kids, genNode(depth-1).Draw(t, "kid"))
			}
			return n
		}
	})
}

// genDocument returns a serialised document and its tree.
func genDocument() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		roots := rapid.IntRange(1, 3).Draw(t, "rootCount")
		var sb strings.Builder
		for range roots {
			genNode(3).Draw(t, "root").writeTo(&sb)
		}
		return sb.String()
	})
}

func (n node) writeTo(sb *strings.Builder) {
	switch {
	case n.tag == "" && n.comment != "":
		sb.WriteString("<!--")
		sb.WriteString(n.comment)
		sb.WriteString("-->")
	case n.tag == "":
		// Escaped, so the document parses back to this exact text.
		sb.WriteString(stdhtml.EscapeString(n.text))
	default:
		sb.WriteString("<")
		sb.WriteString(n.tag)
		for _, a := range n.attrs {
			sb.WriteString(" ")
			sb.WriteString(a[0])
			sb.WriteString(`="`)
			sb.WriteString(stdhtml.EscapeString(a[1]))
			sb.WriteString(`"`)
		}
		sb.WriteString(">")
		for _, k := range n.kids {
			k.writeTo(sb)
		}
		sb.WriteString("</")
		sb.WriteString(n.tag)
		sb.WriteString(">")
	}
}

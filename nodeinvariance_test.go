package lolhtml_test

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// nodeInvarianceDocs are shapes where a write boundary has something to damage: text beside
// markup, text the tokenizer splits on its own, foreign content, raw text, a document that
// ends inside a construct.
var nodeInvarianceDocs = map[string]string{
	"plain":         `<!doctype html><html><body><p>hello world</p><!-- note --><div id="x">text</div></body></html>`,
	"implied":       `<ul><li>one<li>two<li>three</ul>`,
	"entities":      `<p>a &amp; b &lt; c &#65; d</p>`,
	"bare less":     `<p>3 < 4 and 5 < 6</p>`,
	"raw text":      `<script>var a = 1;</script><style>p{color:red}</style>`,
	"table foster":  `<table>stray<tr><td>cell</td></tr></table>`,
	"template":      `<template><tr><td>x</td></tr></template>`,
	"foreign":       `<svg><circle r="1"/><foreignObject><p>x</p></foreignObject></svg><p>after</p>`,
	"unclosed":      `<div><p>text`,
	"unclosed text": `<p>trailing text with no end`,
	"adjacent":      `<p>a</p>b<p>c</p>d`,
	"whitespace":    "<p>\n  \n</p>\n\n<div>\t</div>",
	"nul":           "<p>a\x00b</p>",
	"multibyte":     `<p>naïve — ünïcödé 日本語</p>`,
	"cr":            "<p>a\r\nb\rc</p>",
	"just text":     `bare text with no elements at all`,
	"empty":         `<p></p><p></p>`,
	"long text":     `<p>` + strings.Repeat("word ", 100) + `</p>`,
}

// seen is what one rewrite told its handlers.
type seen struct {
	elements, endTags, comments, doctypes, ends int
	chunks                                      int
	nodes                                       []string
	output                                      string
}

// observe rewrites doc chunk bytes at a time and reports what the handlers were told. A
// chunk of zero is one write of everything.
func observe(t *testing.T, doc string, chunk int) seen {
	t.Helper()

	var s seen
	var out, node strings.Builder
	opts := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			s.elements++
			// An end-tag handler is an error on an element that cannot have
			// content, and in foreign content that is decided per instance.
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error { s.endTags++; return nil })
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { s.comments++; return nil }),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error { s.doctypes++; return nil }),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { s.ends++; return nil }),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			s.chunks++
			node.WriteString(c.Text())
			if c.IsLastInTextNode() {
				s.nodes = append(s.nodes, node.String())
				node.Reset()
			}
			return nil
		}),
	}

	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		t.Fatal(err)
	}
	step := chunk
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, err := w.Write([]byte(doc[i:min(i+step, len(doc))])); err != nil {
			t.Fatalf("write size %d: %v", chunk, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("write size %d: %v", chunk, err)
	}
	if node.Len() > 0 {
		t.Errorf("write size %d: %d bytes of text arrived with no chunk marked last: %q",
			chunk, node.Len(), node.String())
	}
	s.output = out.String()
	return s
}

// TestOnlyTheChunkCountDependsOnTheWrites. The documented behaviour is that text arrives in
// chunks with no guaranteed boundaries and that chunk counts depend on how the input was
// split. Both halves of that are easy to over-read, so this pins what does not move: the
// text *nodes*, their contents, every other handler's invocation count, and the output.
//
// examples/gip/chunkinvariance makes the same comparison over a larger corpus and records
// more per call - attribute values, source locations, partial runes. What this adds is
// density and a worse corpus: every write size from one to forty rather than seven chosen
// ones, so a boundary lands at every offset inside every construct, and documents that end
// in the middle of one, where the last chunk of a node arrives during Close rather than
// during Write.
func TestOnlyTheChunkCountDependsOnTheWrites(t *testing.T) {
	for name, doc := range nodeInvarianceDocs {
		t.Run(name, func(t *testing.T) {
			base := observe(t, doc, 0)
			for chunk := 1; chunk <= 40; chunk++ {
				got := observe(t, doc, chunk)

				if got.output != base.output {
					t.Fatalf("write size %d changed the output:\n whole: %q\n got:   %q",
						chunk, base.output, got.output)
				}
				if got.elements != base.elements || got.endTags != base.endTags ||
					got.comments != base.comments || got.doctypes != base.doctypes ||
					got.ends != base.ends {
					t.Fatalf("write size %d changed the invocation counts: "+
						"elements %d/%d, end tags %d/%d, comments %d/%d, doctypes %d/%d, "+
						"document ends %d/%d", chunk,
						got.elements, base.elements, got.endTags, base.endTags,
						got.comments, base.comments, got.doctypes, base.doctypes,
						got.ends, base.ends)
				}
				if len(got.nodes) != len(base.nodes) {
					t.Fatalf("write size %d gave %d text nodes against %d:\n whole: %q\n got:   %q",
						chunk, len(got.nodes), len(base.nodes), base.nodes, got.nodes)
				}
				for i := range base.nodes {
					if got.nodes[i] != base.nodes[i] {
						t.Fatalf("write size %d moved a boundary: node %d is %q against %q",
							chunk, i, got.nodes[i], base.nodes[i])
					}
				}
			}
		})
	}
}

// TestTheChunkCountItselfDoesMove, so that the test above is not passing because the
// splitting stopped happening.
func TestTheChunkCountItselfDoesMove(t *testing.T) {
	doc := nodeInvarianceDocs["long text"]
	whole, byteAtATime := observe(t, doc, 0), observe(t, doc, 1)

	if byteAtATime.chunks <= whole.chunks {
		t.Errorf("a 500-byte text node arrived in %d chunks written one byte at a time and "+
			"%d written whole", byteAtATime.chunks, whole.chunks)
	}
	if len(byteAtATime.nodes) != len(whole.nodes) || byteAtATime.nodes[0] != whole.nodes[0] {
		t.Errorf("and the node did not survive it: %d nodes against %d",
			len(byteAtATime.nodes), len(whole.nodes))
	}
}

// TestTheLastChunkArrivesEvenWhenTheDocumentDoesNot. A caller accumulating to
// IsLastInTextNode is relying on that chunk existing, and the documents that end inside a
// construct are where it would be easy for it not to - the boundary chunk of an unclosed
// element arrives during Close rather than during Write.
func TestTheLastChunkArrivesEvenWhenTheDocumentDoesNot(t *testing.T) {
	for _, doc := range []string{
		`<p>trailing text with no end`,
		`<div><p>text`,
		`<script>var a = 1;`,
		`<!--`,
		`<p>text</p`,
		`bare text`,
	} {
		t.Run(doc, func(t *testing.T) {
			for _, chunk := range []int{1, 3, 0} {
				got := observe(t, doc, chunk)
				if len(got.nodes) == 0 && got.chunks > 0 {
					t.Errorf("write size %d: %d chunks arrived and none was the last",
						chunk, got.chunks)
				}
			}
		})
	}
}

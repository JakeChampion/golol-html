package differential

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// corpus is ordinary, well-formed-ish HTML: the kind of document a rewriter is
// actually pointed at. Adversarial markup lives in the root package's fuzz
// target, where the property under test is chunk-invariance rather than
// agreement with a second parser - two independent implementations are entitled
// to differ on markup the spec resolves through error recovery.
var corpus = map[string]string{
	"minimal":  `<!DOCTYPE html><html><head><title>t</title></head><body><p>hi</p></body></html>`,
	"fragment": `<div class="a"><span>x</span></div>`,
	"links": `<!DOCTYPE html><html><body>` +
		`<a href="/one">One</a><a href="sub/two" rel="next">Two</a>` +
		`<a href="https://example.com/x" target="_blank">Three</a>` +
		`</body></html>`,
	"nested":               `<div><ul><li>a</li><li>b<ul><li>c</li></ul></li></ul></div>`,
	"attributes":           `<img src="/a.png" alt="an image" width="10" height="20" loading="lazy">`,
	"entities":             `<p>caf&eacute; &amp; cr&egrave;me &lt;not-a-tag&gt;</p>`,
	"unicode":              `<p>café ☃ 日本語 🎉</p>`,
	"comments":             `<div><!-- a comment --><p>x</p><!--another--></div>`,
	"svg":                  `<div><svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg></div>`,
	"table":                `<table><thead><tr><th>h</th></tr></thead><tbody><tr><td>d</td></tr></tbody></table>`,
	"pre":                  "<pre>  leading\n  and newlines\n</pre>",
	"script":               `<div><script>var x = 1 < 2;</script><p>after</p></div>`,
	"style":                `<head><style>body { color: red }</style></head>`,
	"textarea":             `<textarea>  <b>not markup</b>  </textarea>`,
	"self closing foreign": `<svg><path d="M0 0"/><g><rect/></g></svg>`,
	"boolean attributes":   `<input type="checkbox" checked disabled>`,
	"empty attribute":      `<span foo bar="">x</span>`,
	"mixed case tags":      `<DIV CLASS="x"><SPAN>y</SPAN></DIV>`,
	"deep text":            `<p>a<b>b<i>c<u>d</u>c</i>b</b>a</p>`,
	"head and body split":  `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body><h1>T</h1></body></html>`,
}

// TestPassthroughPreservesMeaning is the core property: with no handlers, the
// rewriter is a very elaborate copy. Any difference an independent parser can
// see is a bug.
func TestPassthroughPreservesMeaning(t *testing.T) {
	for name, doc := range corpus {
		t.Run(name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(doc)
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			want, err := parseCanonical(doc)
			if err != nil {
				t.Fatalf("parsing input: %v", err)
			}
			got, err := parseCanonical(out)
			if err != nil {
				t.Fatalf("parsing output: %v", err)
			}
			if got != want {
				t.Errorf("passthrough changed the document\ninput:  %q\noutput: %q\nwant tree: %s\ngot tree:  %s",
					doc, out, want, got)
			}
		})
	}
}

// TestPassthroughIsByteIdentical is a stricter claim than the one above, and it
// is the one the README implies by calling a no-handler rewrite a passthrough.
// If lol-html ever starts normalising markup, this is where it shows up.
func TestPassthroughIsByteIdentical(t *testing.T) {
	for name, doc := range corpus {
		t.Run(name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(doc)
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if out != doc {
				t.Errorf("no-handler rewrite is not byte-identical\n in: %q\nout: %q", doc, out)
			}
		})
	}
}

// TestRemoveScriptsMatchesTreeSurgery checks a real rewrite against the same
// edit performed on an independently parsed tree.
func TestRemoveScriptsMatchesTreeSurgery(t *testing.T) {
	for name, doc := range corpus {
		t.Run(name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				e.Remove()
				return nil
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			root, err := html.Parse(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("parsing input: %v", err)
			}
			removeElements(root, "script")
			want := canonical(root)

			got, err := parseCanonical(out)
			if err != nil {
				t.Fatalf("parsing output: %v", err)
			}
			if got != want {
				t.Errorf("script removal disagrees with tree surgery\noutput: %q\nwant tree: %s\ngot tree:  %s",
					out, want, got)
			}
		})
	}
}

// TestSetAttributeMatchesTreeSurgery does the same for a mutation rather than a
// removal, which exercises the escaping path.
func TestSetAttributeMatchesTreeSurgery(t *testing.T) {
	const key, val = "data-marked", `yes "quoted" & <angled>`

	for name, doc := range corpus {
		t.Run(name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a, p, div", func(e *lolhtml.Element) error {
				return e.SetAttribute(key, val)
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			root, err := html.Parse(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("parsing input: %v", err)
			}
			setAttribute(root, map[string]bool{"a": true, "p": true, "div": true}, key, val)
			want := canonical(root)

			got, err := parseCanonical(out)
			if err != nil {
				t.Fatalf("parsing output: %v", err)
			}
			if got != want {
				t.Errorf("attribute rewrite disagrees with tree surgery\noutput: %q\nwant tree: %s\ngot tree:  %s",
					out, want, got)
			}
		})
	}
}

// TestTextHandlerSeesAllText checks that concatenating the text chunks the
// rewriter reports reproduces the text an independent parser finds. Text
// arriving in arbitrary chunks is the easiest thing to get wrong.
//
// The chunks are unescaped first, because lol-html reports raw source text
// while x/net/html reports decoded text. This test is what caught that
// difference, at which point the binding's documentation was wrong rather than
// its behaviour.
//
// Caveat: unescaping is wrong inside raw-text elements, where a literal
// "&amp;" is five characters rather than an entity. The corpus deliberately
// keeps entities out of <script> and <style> so the two sides stay comparable.
func TestTextHandlerSeesAllText(t *testing.T) {
	for name, doc := range corpus {
		t.Run(name, func(t *testing.T) {
			var seen strings.Builder
			_, err := lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				seen.WriteString(c.Text())
				return nil
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			root, err := html.Parse(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("parsing input: %v", err)
			}
			var want strings.Builder
			collectText(root, &want)

			if stdhtml.UnescapeString(seen.String()) != want.String() {
				t.Errorf("text chunks do not reproduce the document text\nchunks:    %q\nunescaped: %q\nparser:    %q",
					seen.String(), stdhtml.UnescapeString(seen.String()), want.String())
			}
		})
	}
}

func removeElements(n *html.Node, tag string) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.ElementNode && c.Data == tag && c.Namespace == "" {
			n.RemoveChild(c)
			continue
		}
		removeElements(c, tag)
	}
}

func setAttribute(n *html.Node, tags map[string]bool, key, val string) {
	if n.Type == html.ElementNode && n.Namespace == "" && tags[n.Data] {
		replaced := false
		for i := range n.Attr {
			if n.Attr[i].Key == key && n.Attr[i].Namespace == "" {
				n.Attr[i].Val = val
				replaced = true
				break
			}
		}
		if !replaced {
			n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		setAttribute(c, tags, key, val)
	}
}

func collectText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, sb)
	}
}

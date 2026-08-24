package differential

// Link extraction, compared against an independent parser.
//
// Passthrough byte-identity says the rewriter can copy. This says something
// harder: that what a rewriter *reads* out of a document matches what a real
// parser reads out of it. Extracting every anchor's target and text exercises
// the three things a rewriter is actually responsible for - attribute reading,
// text accumulation across nested markup, and chunk boundaries - and compares
// them against x/net/html, which shares no code with lol-html.
//
// The comparison has to account for one documented difference: lol-html reports
// raw source, so character references are still encoded, while x/net/html decodes
// them. Unescaping the rewriter's side is the whole of the adjustment, and doing
// it here rather than hiding it in a helper is deliberate - if any further
// adjustment were ever needed, that would itself be the finding.

import (
	"bytes"
	stdhtml "html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
)

// linkCorpus is the link shapes worth comparing, on top of the shared corpus.
var linkCorpus = map[string]string{
	"plain":              `<a href="/one">One</a>`,
	"two":                `<a href="/one">One</a><a href="/two">Two</a>`,
	"nested markup":      `<a href="/x">plain <b>bold <i>italic</i></b> tail</a>`,
	"entity in text":     `<a href="/x">caf&eacute; &amp; cr&egrave;me</a>`,
	"entity in href":     `<a href="/x?a=1&amp;b=2">q</a>`,
	"numeric entity":     `<a href="/x">&#65;&#x42;</a>`,
	"empty text":         `<a href="/x"></a>`,
	"whitespace text":    `<a href="/x">   </a>`,
	"no href":            `<a>no target</a>`,
	"empty href":         `<a href="">empty</a>`,
	"unquoted href":      `<a href=/x>unquoted</a>`,
	"single quoted":      `<a href='/x'>single</a>`,
	"uppercase":          `<A HREF="/X">Upper</A>`,
	"image inside":       `<a href="/x"><img src="/i" alt="alt text">caption</a>`,
	"nested in lists":    `<ul><li><a href="/a">A</a></li><li><a href="/b">B</a></li></ul>`,
	"in a table":         `<table><tr><td><a href="/t">T</a></td></tr></table>`,
	"unicode text":       `<a href="/x">café ☃ 日本語 🎉</a>`,
	"newline in text":    "<a href=\"/x\">line one\nline two</a>",
	"tab in text":        "<a href=\"/x\">a\tb</a>",
	"comment inside":     `<a href="/x">before<!--c-->after</a>`,
	"pre inside":         "<a href=\"/x\"><pre>  spaced  </pre></a>",
	"many":               strings.Repeat(`<a href="/n">N</a>`, 40),
	"fragment target":    `<a href="#top">Top</a>`,
	"mailto":             `<a href="mailto:a@b.c">Mail</a>`,
	"protocol relative":  `<a href="//cdn.example/x">CDN</a>`,
	"query and fragment": `<a href="/x?a=1#f">QF</a>`,
	"trailing space":     `<a href="  /x  ">Spaced target</a>`,
}

type link struct {
	href string
	text string
}

// viaRewriter extracts links with golol-html, in writes of chunk bytes.
func viaRewriter(t *testing.T, doc string, chunk int) []link {
	t.Helper()

	var links []link
	var open bool
	var text strings.Builder

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			// The href is raw source; decode it to compare with a parser that
			// decodes.
			l := link{href: stdhtml.UnescapeString(href)}
			links = append(links, l)
			open = true
			text.Reset()
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				open = false
				links[len(links)-1].text = stdhtml.UnescapeString(text.String())
				return nil
			})
		}),
		lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
			if open {
				text.WriteString(tc.Text())
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	step := chunk
	if step <= 0 {
		step = len(doc)
		if step == 0 {
			step = 1
		}
	}
	for i := 0; i < len(doc); i += step {
		end := min(i+step, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return links
}

// viaParser extracts the same links from x/net/html's tree.
func viaParser(t *testing.T, doc string) []link {
	t.Helper()

	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("x/net/html: %v", err)
	}

	var links []link
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			l := link{}
			for _, a := range n.Attr {
				if a.Key == "href" {
					l.href = a.Val
					break
				}
			}
			l.text = textOf(n)
			links = append(links, l)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return links
}

// textOf concatenates the text of a subtree, which is what a rewriter's text
// handler sees for the same element.
func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// TestLinkExtractionMatchesAnIndependentParser is the claim. Chunk sizes are
// included because text accumulation is the part most likely to depend on them,
// and a rewriter that agreed with a parser only when handed the whole document
// at once would be no use.
func TestLinkExtractionMatchesAnIndependentParser(t *testing.T) {
	docs := map[string]string{}
	for name, doc := range corpus {
		docs["corpus/"+name] = doc
	}
	for name, doc := range linkCorpus {
		docs["links/"+name] = doc
	}

	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			want := viaParser(t, doc)

			for _, chunk := range []int{0, 1, 3, 16} {
				got := viaRewriter(t, doc, chunk)

				if len(got) != len(want) {
					t.Fatalf("chunk=%d: found %d links, the parser found %d\n got: %+v\nwant: %+v",
						chunk, len(got), len(want), got, want)
				}
				for i := range got {
					if got[i].href != want[i].href {
						t.Errorf("chunk=%d: link %d href = %q, the parser says %q",
							chunk, i, got[i].href, want[i].href)
					}
					if got[i].text != want[i].text {
						t.Errorf("chunk=%d: link %d text = %q, the parser says %q",
							chunk, i, got[i].text, want[i].text)
					}
				}
			}
		})
	}
}

// TestLinkTextSurvivesRewriting: extracting is one thing, and extracting from a
// document being rewritten at the same time is another. The links must be the
// same either way, and the rewritten document must still say the same thing to
// the parser.
func TestLinkTextSurvivesRewriting(t *testing.T) {
	for name, doc := range linkCorpus {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			var links []link
			var text strings.Builder

			w, err := lolhtml.NewWriter(&out,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					href, _ := e.Attribute("href")
					links = append(links, link{href: stdhtml.UnescapeString(href)})
					text.Reset()
					// Rewrite while extracting: add an attribute, which must
					// not disturb either the text or the href.
					if err := e.SetAttribute("data-seen", "1"); err != nil {
						return err
					}
					return e.OnEndTag(func(*lolhtml.EndTag) error {
						links[len(links)-1].text = stdhtml.UnescapeString(text.String())
						return nil
					})
				}),
				lolhtml.OnText("a", func(tc *lolhtml.TextChunk) error {
					text.WriteString(tc.Text())
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(doc)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}

			// The extraction agrees with the parser reading the original.
			want := viaParser(t, doc)
			if len(links) != len(want) {
				t.Fatalf("found %d links, the parser found %d", len(links), len(want))
			}
			for i := range links {
				if links[i] != want[i] {
					t.Errorf("link %d = %+v, the parser says %+v", i, links[i], want[i])
				}
			}

			// And the rewritten document still yields the same links, plus the
			// attribute that was added.
			after := viaParser(t, out.String())
			if len(after) != len(want) {
				t.Fatalf("after rewriting, the parser finds %d links, want %d",
					len(after), len(want))
			}
			for i := range after {
				if after[i] != want[i] {
					t.Errorf("after rewriting, link %d = %+v, want %+v", i, after[i], want[i])
				}
			}
		})
	}
}

// TestDocumentTextIsReconstructedUnderChunkedWrites widens an existing claim.
//
// TestTextHandlerSeesAllText already compares the concatenated text chunks
// against the parser's text, but it writes the document in one call. Text chunk
// boundaries are the one thing lol-html explicitly does not promise to reproduce
// - a byte-at-a-time write produces more chunks than a single write - so the
// interesting version of the claim is the chunked one, and that is the only way
// a document arrives in production.
func TestDocumentTextIsReconstructedUnderChunkedWrites(t *testing.T) {
	docs := map[string]string{}
	for name, doc := range corpus {
		docs["corpus/"+name] = doc
	}
	for name, doc := range linkCorpus {
		docs["links/"+name] = doc
	}

	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("x/net/html: %v", err)
			}
			var want strings.Builder
			collectText(root, &want)

			for _, chunk := range []int{0, 1, 2, 3, 16} {
				var seen strings.Builder
				w, err := lolhtml.NewWriter(io.Discard,
					lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
						seen.WriteString(tc.Text())
						return nil
					}))
				if err != nil {
					t.Fatal(err)
				}

				step := chunk
				if step <= 0 {
					step = len(doc)
					if step == 0 {
						step = 1
					}
				}
				for i := 0; i < len(doc); i += step {
					end := min(i+step, len(doc))
					if _, err := w.Write([]byte(doc[i:end])); err != nil {
						t.Fatal(err)
					}
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}

				if got := stdhtml.UnescapeString(seen.String()); got != want.String() {
					t.Errorf("chunk=%d: text chunks do not reproduce the document text\n got: %q\nwant: %q",
						chunk, got, want.String())
				}
			}
		})
	}
}

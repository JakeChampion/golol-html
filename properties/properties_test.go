package properties

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"golang.org/x/net/html"
	"pgregory.net/rapid"
)

// TestPassthroughIsIdentity: with no handlers the rewriter is an elaborate
// copy. Table tests assert this for twenty documents; this asserts it for
// whatever rapid can build, and shrinks any counter-example to something small
// enough to read.
func TestPassthroughIsIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")

		out, err := lolhtml.RewriteString(doc)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if out != doc {
			t.Fatalf("passthrough changed the document\n in: %q\nout: %q", doc, out)
		}
	})
}

// TestAReadOnlyRewriteIsIdentity is the property a report-only tool rests on: a
// rewrite whose handlers observe and change nothing has to give back the document
// it was given.
//
// Stronger than TestPassthroughIsIdentity, which registers nothing at all. The
// exception is deliberate and measured in the root package: a text handler decodes
// and re-encodes, so a document holding bytes that are not valid UTF-8 comes out
// different for having had one registered. The generator produces valid UTF-8, so
// the text handler is in here too and the property holds for it - the exception is
// about the document rather than about the handler.
func TestAReadOnlyRewriteIsIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")

		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				_ = e.TagName()
				_ = e.AttributeList()
				_ = e.SourceLocation()
				_ = e.IsSelfClosing()
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(tag *lolhtml.EndTag) error {
					_ = tag.Name()
					_ = tag.SourceLocation()
					return nil
				})
			}),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				_ = c.Text()
				_ = c.IsLastInTextNode()
				return nil
			}),
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				_ = c.Text()
				_ = c.SourceLocation()
				return nil
			}),
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				_, _ = d.Name()
				_, _ = d.PublicID()
				_, _ = d.SystemID()
				return nil
			}),
			lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return nil }),
		)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if out != doc {
			t.Fatalf("a read-only rewrite changed the document\n in: %q\nout: %q", doc, out)
		}
	})
}

// TestRewriteIsIdempotent: rewriting an already-rewritten document must not
// keep changing it. A rewrite that is not idempotent is usually one that
// double-escapes.
func TestRewriteIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")
		val := genString().Draw(t, "value")

		mark := func(s string) (string, error) {
			return lolhtml.RewriteString(s, lolhtml.OnElement("div, p, span", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-mark", val)
			}))
		}

		once, err := mark(doc)
		if err != nil {
			t.Fatalf("first rewrite: %v", err)
		}
		twice, err := mark(once)
		if err != nil {
			t.Fatalf("second rewrite: %v", err)
		}
		if once != twice {
			t.Fatalf("rewriting twice differs from once\nonce:  %q\ntwice: %q", once, twice)
		}
	})
}

// TestTextInsertionCannotInjectMarkup is the safety property that matters
// most: content inserted as lolhtml.Text must never become markup, whatever it
// contains. A hole here is an injection vulnerability in anything built on
// these bindings.
//
// The comparison is against the same document with a deliberately innocuous
// payload, not against the original document. Element count on its own is a
// bad proxy: the generator produces invalid nesting such as a <div> inside a
// <p>, where the parser's error recovery relocates nodes, so inserting *any*
// text - even a bare "x" - can change the tree shape. Comparing two insertions
// that differ only in payload content isolates the payload's own effect, which
// is the thing actually under test.
func TestTextInsertionCannotInjectMarkup(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")
		payload := rapid.OneOf(
			genString(),
			rapid.SampledFrom([]string{
				`<script>alert(1)</script>`,
				`"><script>alert(1)</script>`,
				`<img src=x onerror=alert(1)>`,
				`</div><div>`,
				`<!--`, `-->`, `</p>`, `<`, `>`, `&`,
			}),
		).Filter(func(s string) bool {
			// An empty payload inserts nothing, so it cannot be compared against
			// a non-empty benign baseline - and it cannot inject anything either.
			return s != "" && noUnrepresentable(s)
		}).Draw(t, "payload")

		insert := func(s string) (string, error) {
			return lolhtml.RewriteString(doc, lolhtml.OnElement("div, p, span", func(e *lolhtml.Element) error {
				return e.Append(s, lolhtml.Text)
			}))
		}

		const benign = "SAFE"
		baseline, err := insert(benign)
		if err != nil {
			t.Fatalf("baseline rewrite: %v", err)
		}
		out, err := insert(payload)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}

		if got, want := countElements(t, out), countElements(t, baseline); got != want {
			t.Fatalf("inserting %q as Text changed the element count to %d, where the benign payload gives %d\nout:      %q\nbaseline: %q",
				payload, got, want, out, baseline)
		}

		// And the payload must survive as text rather than being interpreted:
		// whatever was inserted should appear in the document's text content.
		//
		// Guarded on the benign payload having appeared, because a generated
		// document may contain no matching element at all, in which case
		// nothing was inserted and there is nothing to find.
		inserted := strings.Contains(textContent(t, baseline), benign)
		if inserted && payload != "" && !strings.Contains(textContent(t, out), payload) {
			t.Fatalf("inserting %q as Text did not appear verbatim in the text content\nout: %q",
				payload, out)
		}
	})
}

// textContent returns the concatenated text of a parsed document.
func textContent(t *rapid.T, doc string) string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
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
	walk(root)
	return sb.String()
}

// TestAttributeRoundTrip: a value written with SetAttribute reads back exactly,
// byte for byte. lol-html stores what it was given, so no escaping round trip
// is involved - which is why this asserts equality rather than unescaping
// first. Getting that wrong is what this test caught in its own first draft.
func TestAttributeRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// A leading U+FEFF is excluded: lol-html decodes on read and its
		// decoder removes a byte-order mark, so such a value reads back
		// without it. That is upstream behaviour rather than a marshalling
		// bug here - the value is serialised faithfully - and it is pinned by
		// TestLeadingBOMIsStrippedOnRead in the root package.
		value := genString().Filter(func(s string) bool {
			return !strings.HasPrefix(s, string(rune(0xFEFF)))
		}).Draw(t, "value")

		var got string
		var found bool
		_, err := lolhtml.RewriteString(`<div></div>`, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if err := e.SetAttribute("data-v", value); err != nil {
				return err
			}
			got, found = e.Attribute("data-v")
			return nil
		}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if !found {
			t.Fatal("the attribute was not present after SetAttribute")
		}
		if got != value {
			t.Fatalf("attribute did not round-trip\nwrote: %q\nread:  %q", value, got)
		}
	})
}

// TestSetAttributeTakesRawSourceText pins down an asymmetry that is easy to
// get wrong, and that this test caught the documentation getting wrong.
//
// SetAttribute is given raw source text, not a literal value: only the double
// quote is escaped, because only the double quote would break the attribute
// syntax. Ampersands and angle brackets go through untouched, so writing the
// five characters "&amp;" means the single character "&" to any parser that
// reads the result.
//
// Content insertion is the opposite: Append(s, lolhtml.Text) escapes s fully,
// which is what makes it safe for untrusted values. Attribute values are safe
// from injection either way, since the quote is escaped, but their meaning is
// not preserved unless the caller escapes ampersands itself.
func TestSetAttributeTakesRawSourceText(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// NUL and CR are excluded because HTML cannot carry them, whatever the
		// implementation: a parser must replace U+0000 with U+FFFD, and input
		// preprocessing normalises CR and CRLF to LF, both before any markup is
		// interpreted. Asserting they survive would be a claim about the
		// format, and a false one, rather than a claim about these bindings.
		// Property testing found both; neither is a bug here.
		value := genString().Filter(noUnrepresentable).Draw(t, "value")

		out, err := lolhtml.RewriteString(`<div></div>`, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-v", value)
		}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}

		root, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatalf("parsing output: %v", err)
		}
		got, ok := findAttr(root, "div", "data-v")
		if !ok {
			t.Fatalf("attribute missing from %q", out)
		}
		// An independent parser decodes the entities in the raw text, so the
		// value it sees is the unescaped form of what was written.
		if want := stdhtml.UnescapeString(value); got != want {
			t.Fatalf("attribute did not survive serialisation as raw source\nwrote: %q\nparsed as: %q\nwant: %q\nout: %q",
				value, got, want, out)
		}
	})
}

// TestDetachedUnitsAlwaysRefuse: whatever a document contains, a unit retained
// past its handler must refuse every operation rather than reach into memory
// lol-html has reused.
func TestDetachedUnitsAlwaysRefuse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "doc")

		var elements []*lolhtml.Element
		var comments []*lolhtml.Comment
		var texts []*lolhtml.TextChunk

		_, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				elements = append(elements, e)
				return nil
			}),
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				comments = append(comments, c)
				return nil
			}),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				texts = append(texts, c)
				return nil
			}),
		)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}

		for _, e := range elements {
			if !e.Detached() {
				t.Fatal("element still attached after its handler returned")
			}
			if err := e.SetAttribute("a", "b"); err == nil {
				t.Fatal("SetAttribute succeeded on a detached element")
			}
			// Reads must be inert rather than faulting.
			_ = e.TagName()
			_ = e.AttributeList()
		}
		for _, c := range comments {
			if !c.Detached() {
				t.Fatal("comment still attached after its handler returned")
			}
			_ = c.Text()
		}
		for _, c := range texts {
			if !c.Detached() {
				t.Fatal("text chunk still attached after its handler returned")
			}
			_ = c.Text()
		}
	})
}

// noUnrepresentable rejects the two characters HTML cannot carry verbatim,
// whatever the implementation: a parser must replace U+0000 with U+FFFD, and
// input preprocessing normalises CR and CRLF to LF, both before any markup is
// interpreted. Asserting they survive would be a false claim about the format
// rather than about these bindings. Property testing found both - once in an
// attribute value, and again in text content.
func noUnrepresentable(s string) bool {
	return !strings.ContainsAny(s, "\x00\r")
}

func countElements(t *rapid.T, doc string) int {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parsing %q: %v", doc, err)
	}
	var n int
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			n++
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return n
}

func findAttr(n *html.Node, tag, key string) (string, bool) {
	if n.Type == html.ElementNode && n.Data == tag {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val, true
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if v, ok := findAttr(c, tag, key); ok {
			return v, true
		}
	}
	return "", false
}

// A sanitiser as a property, over documents built to break it.
//
// Every other file here states something about one call. This one states something about a
// composition: an allow-list sanitiser is selectors, removal, attribute iteration and decoding
// used together, and the interesting failures are in how they interact rather than in any of
// them. So the generator produces hostile documents - script and iframe and svg, event handlers,
// entity-encoded schemes, raw-text elements, unclosed tags, duplicate attributes, <image> - and
// the properties say what has to be true of the output whatever the input was.
//
// The sanitiser here is deliberately small and deliberately not an example: examples/gip/emailstrip
// is the one with a report and options, and this is the shortest thing that can be checked. What
// it shares with that one is the two rules that matter, both of which the library's documentation
// gives: an allow-list rather than a block-list, and decide on the decoded form of a value while
// writing back the raw one.
package properties

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"pgregory.net/rapid"
)

// safeElements is the allow-list. Everything else goes, with its content.
var safeElements = map[string]bool{
	"html": true, "head": true, "body": true,
	"p": true, "div": true, "span": true, "a": true, "b": true, "i": true, "em": true,
	"strong": true, "ul": true, "ol": true, "li": true, "br": true, "table": true,
	"tr": true, "td": true, "th": true, "tbody": true, "img": true,
}

// safeAttributes is the allow-list for attributes, by element. The empty key applies everywhere.
var safeAttributes = map[string]map[string]bool{
	"":    {"title": true, "dir": true, "lang": true},
	"a":   {"href": true},
	"img": {"src": true, "alt": true},
	"td":  {"colspan": true, "rowspan": true},
	"th":  {"colspan": true, "rowspan": true},
}

// safeSchemes are the URL schemes an href or src may use. No scheme at all - a relative or
// fragment URL - is safe too.
var safeSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// hostileTags are what the generator puts in a document. The list is half safe and half not, so
// a property that only ever saw dangerous elements would not notice a sanitiser that removed
// everything.
var hostileTags = []string{
	"p", "div", "span", "a", "b", "img", "table", "td",
	"script", "iframe", "object", "embed", "form", "input", "style", "svg", "math",
	"noscript", "template", "textarea", "title", "image", "base", "link", "meta",
}

// hostileAttrs are attribute names, again mixed.
var hostileAttrs = []string{
	"title", "href", "src", "alt", "colspan", "dir",
	"onclick", "onerror", "onload", "onmouseover", "style", "class", "id", "srcset",
	"formaction", "xlink:href", "data-x",
}

// hostileValues are attribute values, including every spelling of a javascript URL that a
// browser decodes and a naive check does not.
var hostileValues = []string{
	"", "x", "https://example.com/", "/relative", "#fragment", "mailto:a@b",
	"javascript:alert(1)", "&#106;avascript:alert(1)", "&#x6a;avascript:alert(1)",
	"&#0000106;avascript:alert(1)", "jav&#x09;ascript:alert(1)", "&Tab;javascript:alert(1)",
	"JaVaScRiPt:alert(1)", " javascript:alert(1)", "data:text/html,<script>x</script>",
	"vbscript:x", "expression(alert(1))", "url(javascript:x)",
	`" onmouseover="alert(1)`, "<script>alert(1)</script>", "&quot;&gt;&lt;script&gt;",
}

// attributeValue writes a value into an attribute without destroying the thing being tested.
//
// The first version of this used html.EscapeString, which is right for a generator that wants the
// document to parse back to the exact text - and wrong here, because it turns "&#106;avascript:"
// into "&amp;#106;avascript:", which a parser reads as the literal characters and no browser ever
// executes. The vectors then never reached the sanitiser in their dangerous form and the property
// passed against a sanitiser with the hole deliberately put back. Only the quote that would end
// the attribute is escaped; the entities are what the test is about.
func attributeValue(v string) string {
	return strings.ReplaceAll(v, `"`, "&quot;")
}

// genHostileDocument builds a document out of the pieces above, including the shapes that a
// tokenizer treats specially: raw text, unclosed tags, duplicate attributes and foreign content.
func genHostileDocument() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var sb strings.Builder
		pieces := rapid.IntRange(1, 5).Draw(t, "pieces")
		for i := range pieces {
			switch rapid.IntRange(0, 6).Draw(t, "kind") {
			case 0:
				// A well-formed element with attributes.
				writeHostileElement(t, &sb, true)
			case 1:
				// An element left unclosed, which is where a tokenizer's error
				// recovery starts.
				writeHostileElement(t, &sb, false)
			case 2:
				sb.WriteString(stdhtml.EscapeString(
					rapid.SampledFrom(hostileValues).Draw(t, "text")))
			case 3:
				sb.WriteString("<!--")
				sb.WriteString(rapid.StringMatching(`[a-z ]{0,8}`).Draw(t, "comment"))
				sb.WriteString("-->")
			case 4:
				// Raw text, where the content is not markup and an insertion could
				// end the element.
				raw := rapid.SampledFrom([]string{"script", "style", "textarea", "title"}).
					Draw(t, "rawTag")
				sb.WriteString("<" + raw + ">")
				sb.WriteString(rapid.SampledFrom([]string{
					"x", "</p>", "alert(1)", "a<b", "</scr" + "ipt>x",
				}).Draw(t, "rawText"))
				sb.WriteString("</" + raw + ">")
			case 5:
				// Foreign content, where the parser's rules change.
				sb.WriteString(`<svg><circle r="1"/>`)
				writeHostileElement(t, &sb, true)
				sb.WriteString(`</svg>`)
			default:
				// A duplicate attribute, which selectors and AttributeList disagree
				// about - B57.
				sb.WriteString(`<a href="/one" href="`)
				sb.WriteString(attributeValue(
					rapid.SampledFrom(hostileValues).Draw(t, "dupValue")))
				sb.WriteString(`">x</a>`)
			}
			_ = i
		}
		return sb.String()
	})
}

func writeHostileElement(t *rapid.T, sb *strings.Builder, closed bool) {
	tag := rapid.SampledFrom(hostileTags).Draw(t, "tag")
	sb.WriteString("<" + tag)
	attrs := rapid.IntRange(0, 3).Draw(t, "attrCount")
	for range attrs {
		sb.WriteString(" ")
		sb.WriteString(rapid.SampledFrom(hostileAttrs).Draw(t, "attr"))
		sb.WriteString(`="`)
		sb.WriteString(attributeValue(rapid.SampledFrom(hostileValues).Draw(t, "value")))
		sb.WriteString(`"`)
	}
	sb.WriteString(">")
	sb.WriteString(rapid.SampledFrom([]string{"", "x", "text", "<b>bold</b>"}).Draw(t, "content"))
	if closed {
		sb.WriteString("</" + tag + ">")
	}
}

// sanitise is the composition under test: remove every element not on the allow-list, remove
// every attribute not on it, and remove a URL whose decoded scheme is not safe.
//
// It is the streaming path, because that is the one a caller uses and the one where a bug would
// be about chunking.
func sanitise(doc string) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if e.IsRemoved() {
			return nil
		}
		if !safeElements[e.TagName()] {
			e.Remove()
			return nil
		}
		for _, attr := range e.AttributeList() {
			name := strings.ToLower(attr.Name)
			if !attributeIsSafe(e.TagName(), name) || !urlIsSafe(name, attr.Value) {
				if err := e.RemoveAttribute(attr.Name); err != nil {
					return err
				}
			}
		}
		return nil
	}))
	if err != nil {
		return "", err
	}
	if _, err := strings.NewReader(doc).WriteTo(w); err != nil {
		w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func attributeIsSafe(tag, name string) bool {
	return safeAttributes[""][name] || safeAttributes[tag][name]
}

// urlIsSafe decides on the decoded value, which is the rule: a browser decodes before it acts, so
// a check on the raw source sees a scheme called "&#106;avascript" and lets it through.
func urlIsSafe(name, value string) bool {
	if name != "href" && name != "src" {
		return true
	}
	decoded := strings.ToLower(strings.TrimLeft(stdhtml.UnescapeString(value), " \t\r\n\f\v\x00"))
	decoded = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r', 0:
			return -1
		}
		return r
	}, decoded)

	colon := strings.IndexByte(decoded, ':')
	if colon < 0 {
		return true
	}
	if i := strings.IndexAny(decoded[:colon], "/?#"); i >= 0 {
		return true
	}
	return safeSchemes[decoded[:colon]]
}

// dangerousSchemes are the ones that execute or that a filter is expected to refuse. The list is
// written out here rather than derived from safeSchemes so that a property checking it is not
// checking the code under test against itself.
var dangerousSchemes = []string{"javascript:", "data:", "vbscript:", "file:", "about:"}

// dangerousScheme decodes a URL the way a browser does and returns the dangerous scheme it starts
// with, or "" if it starts with none.
func dangerousScheme(value string) string {
	decoded := strings.ToLower(stdhtml.UnescapeString(value))
	decoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v', 0:
			return -1
		}
		return r
	}, decoded)
	for _, scheme := range dangerousSchemes {
		if strings.HasPrefix(decoded, scheme) {
			return scheme
		}
	}
	return ""
}

// TestSanitisingLeavesOnlyAllowedElements over generated hostile documents. The check re-reads the
// output with a rewriter of its own, so what is asserted is what a parser would see rather than
// what the sanitiser believes it did.
func TestSanitisingLeavesOnlyAllowedElements(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genHostileDocument().Draw(t, "doc")

		out, err := sanitise(doc)
		if err != nil {
			t.Fatalf("sanitise(%q): %v", doc, err)
		}
		if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !safeElements[e.TagName()] {
				t.Fatalf("<%s> survived sanitising %q: %q", e.TagName(), doc, out)
			}
			for _, attr := range e.AttributeList() {
				name := strings.ToLower(attr.Name)
				if !attributeIsSafe(e.TagName(), name) {
					t.Fatalf("%s survived on <%s> sanitising %q: %q",
						name, e.TagName(), doc, out)
				}
				// Deliberately not urlIsSafe: a checker that calls the code under
				// test agrees with it by construction, including when both are
				// wrong. This decodes the value the way a browser does and looks
				// for the schemes that execute.
				if name == "href" || name == "src" {
					if scheme := dangerousScheme(attr.Value); scheme != "" {
						t.Fatalf("a %s URL survived on <%s> sanitising %q: %q",
							scheme, e.TagName(), doc, out)
					}
				}
			}
			return nil
		})); err != nil {
			t.Fatalf("re-reading %q: %v", out, err)
		}
	})
}

// TestSanitisingIsIdempotent: the output of a sanitiser is a document it has nothing left to do
// to. A failure here means the first pass produced something the second objects to, which is
// either a bug or a document that changes shape when it is re-parsed.
func TestSanitisingIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genHostileDocument().Draw(t, "doc")

		once, err := sanitise(doc)
		if err != nil {
			t.Fatalf("sanitise(%q): %v", doc, err)
		}
		twice, err := sanitise(once)
		if err != nil {
			t.Fatalf("sanitise(%q): %v", once, err)
		}
		if once != twice {
			t.Fatalf("sanitising twice differs\n in:    %q\n once:  %q\n twice: %q",
				doc, once, twice)
		}
	})
}

// TestSanitisingDoesNotDependOnTheWriteSize, since a caller streaming from a socket does not
// choose its chunk boundaries and a sanitiser that depended on them would be a hole.
func TestSanitisingDoesNotDependOnTheWriteSize(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genHostileDocument().Draw(t, "doc")
		size := rapid.IntRange(1, 16).Draw(t, "writeSize")

		whole, err := sanitise(doc)
		if err != nil {
			t.Fatalf("sanitise(%q): %v", doc, err)
		}

		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if e.IsRemoved() {
				return nil
			}
			if !safeElements[e.TagName()] {
				e.Remove()
				return nil
			}
			for _, attr := range e.AttributeList() {
				name := strings.ToLower(attr.Name)
				if !attributeIsSafe(e.TagName(), name) || !urlIsSafe(name, attr.Value) {
					if err := e.RemoveAttribute(attr.Name); err != nil {
						return err
					}
				}
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				w.Close()
				t.Fatalf("write: %v", err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if out.String() != whole {
			t.Fatalf("write size %d changed the result\n in:   %q\n whole: %q\n split: %q",
				size, doc, whole, out.String())
		}
	})
}

// TestTheGeneratorProducesSomethingToRemove. A property suite whose inputs are all harmless
// passes for the wrong reason, so this asserts that the corpus the other three draw from does
// contain elements and attributes a sanitiser has to take out.
func TestTheGeneratorProducesSomethingToRemove(t *testing.T) {
	var withRemovals int
	const runs = 200

	for i := 0; i < runs; i++ {
		doc := rapid.Custom(func(t *rapid.T) string {
			return genHostileDocument().Draw(t, "doc")
		}).Example(i)

		out, err := sanitise(doc)
		if err != nil {
			t.Fatalf("sanitise(%q): %v", doc, err)
		}
		if out != doc {
			withRemovals++
		}
	}
	if withRemovals < runs/4 {
		t.Errorf("only %d of %d generated documents needed sanitising, so the generator has "+
			"stopped being hostile", withRemovals, runs)
	}
}

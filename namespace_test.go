package lolhtml_test

// Namespaces, and the two things that do not do what their names suggest.
//
// Selectors match a tag name in any namespace, and NamespaceURI reports the
// namespace an element's children are parsed in rather than the element's own.
// Between them, the obvious way to tell a document title from an SVG tooltip
// does not work, and the reason is not visible from either method's name.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const (
	htmlNS   = "http://www.w3.org/1999/xhtml"
	svgNS    = "http://www.w3.org/2000/svg"
	mathmlNS = "http://www.w3.org/1998/Math/MathML"
)

// TestNamespaceURIReportsTheContentNamespace is the rule, measured element by
// element. The integration points - where foreign content switches back to HTML
// parsing - report the HTML namespace despite being SVG or MathML elements.
func TestNamespaceURIReportsTheContentNamespace(t *testing.T) {
	for _, tt := range []struct {
		doc  string
		want string
	}{
		// SVG, switching back to HTML.
		{`<svg><foreignObject>x</foreignObject></svg>`, htmlNS},
		{`<svg><desc>x</desc></svg>`, htmlNS},
		{`<svg><title>x</title></svg>`, htmlNS},
		// SVG, staying in SVG.
		{`<svg><g>x</g></svg>`, svgNS},
		{`<svg><a>x</a></svg>`, svgNS},
		{`<svg><script>x</script></svg>`, svgNS},
		{`<svg><style>x</style></svg>`, svgNS},
		{`<svg><text>x</text></svg>`, svgNS},
		{`<svg><textPath>x</textPath></svg>`, svgNS},
		{`<svg><metadata>x</metadata></svg>`, svgNS},

		// MathML, switching back to HTML: the text integration points.
		{`<math><mi>x</mi></math>`, htmlNS},
		{`<math><mo>x</mo></math>`, htmlNS},
		{`<math><mn>x</mn></math>`, htmlNS},
		{`<math><ms>x</ms></math>`, htmlNS},
		{`<math><mtext>x</mtext></math>`, htmlNS},
		// MathML, staying in MathML.
		{`<math><mrow>x</mrow></math>`, mathmlNS},
		{`<math><msqrt>x</msqrt></math>`, mathmlNS},
		{`<math><semantics>x</semantics></math>`, mathmlNS},
		{`<math><a>x</a></math>`, mathmlNS},
	} {
		var got string
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnElement("svg > *, math > *", func(e *lolhtml.Element) error {
				got = e.NamespaceURI()
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.doc, err)
		}
		if got != tt.want {
			t.Errorf("%s: namespace %q, want %q", tt.doc, got, tt.want)
		}
	}
}

// TestAnnotationXMLDependsOnItsEncoding: it is an HTML integration point only
// for two encodings, and the reported namespace follows that exactly - which is
// the clearest evidence that what is reported is the content's namespace and not
// the element's, since the element is MathML in all four cases.
func TestAnnotationXMLDependsOnItsEncoding(t *testing.T) {
	for _, tt := range []struct {
		attrs string
		want  string
	}{
		{``, mathmlNS},
		{` encoding="text/html"`, htmlNS},
		{` encoding="application/xhtml+xml"`, htmlNS},
		{` encoding="other"`, mathmlNS},
		{` encoding=""`, mathmlNS},
	} {
		doc := `<math><annotation-xml` + tt.attrs + `>x</annotation-xml></math>`
		var got string
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("annotation-xml", func(e *lolhtml.Element) error {
				got = e.NamespaceURI()
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		if got != tt.want {
			t.Errorf("annotation-xml%s: namespace %q, want %q", tt.attrs, got, tt.want)
		}
	}
}

// mixedDoc has the same tag names in three namespaces.
const mixedDoc = `<html><head><title>page</title></head><body>` +
	`<p><a href="h">html</a></p><script>hs</script><style>hst</style>` +
	`<svg><a href="s">svg</a><title>tooltip</title><script>ss</script><style>sst</style></svg>` +
	`<math><a href="m">math</a><mi>mi</mi></math>` +
	`</body></html>`

// TestASelectorMatchesEveryNamespace: a bare tag name is namespace-blind, so a
// rewrite keyed on one hits foreign content too.
func TestASelectorMatchesEveryNamespace(t *testing.T) {
	for _, tt := range []struct {
		selector string
		want     []string // "tag/namespace"
	}{
		{"a[href]", []string{"a/" + htmlNS, "a/" + svgNS, "a/" + mathmlNS}},
		{"script", []string{"script/" + htmlNS, "script/" + svgNS}},
		{"style", []string{"style/" + htmlNS, "style/" + svgNS}},
		{"title", []string{"title/" + htmlNS, "title/" + htmlNS}},

		// Naming the context is exact.
		{"svg a", []string{"a/" + svgNS}},
		{"svg title", []string{"title/" + htmlNS}},
		{"math a", []string{"a/" + mathmlNS}},
		{"p a", []string{"a/" + htmlNS}},
	} {
		var got []string
		if _, err := lolhtml.RewriteString(mixedDoc,
			lolhtml.OnElement(tt.selector, func(e *lolhtml.Element) error {
				got = append(got, e.TagName()+"/"+e.NamespaceURI())
				return nil
			})); err != nil {
			t.Fatalf("%s: %v", tt.selector, err)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s matched %v, want %v", tt.selector, got, tt.want)
		}
	}
}

// TestNeitherSelectorNorNamespaceSeparatesTheTwoTitles is the consequence in one
// test: both titles match "title", and both report the HTML namespace, so a
// handler has nothing to discriminate on.
func TestNeitherSelectorNorNamespaceSeparatesTheTwoTitles(t *testing.T) {
	type hit struct{ text, ns string }
	var hits []hit

	if _, err := lolhtml.RewriteString(mixedDoc,
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			hits = append(hits, hit{ns: e.NamespaceURI()})
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("%d titles matched, want 2", len(hits))
	}
	if hits[0].ns != htmlNS || hits[1].ns != htmlNS {
		t.Errorf("the two titles report %q and %q; if these now differ, the "+
			"workaround in examples/gip/envbadge is no longer necessary",
			hits[0].ns, hits[1].ns)
	}
}

// TestContextSelectorsNeedTheContextToBePresent is the trap in the other
// direction. "head title" is exact when <head> is in the input, and <head> is
// optional, so it silently matches nothing when the head is implied.
func TestContextSelectorsNeedTheContextToBePresent(t *testing.T) {
	for _, tt := range []struct {
		doc      string
		selector string
		want     []string
	}{
		{`<html><head><title>page</title></head><body><svg><title>tip</title></svg></body></html>`,
			"head title", []string{"page"}},
		{`<html><head><title>page</title></head><body><svg><title>tip</title></svg></body></html>`,
			"head > title", []string{"page"}},

		// No <head> in the source. The document still has a title; the selector
		// does not find it.
		{`<title>page</title><p><svg><title>tip</title></svg></p>`, "head title", nil},
		{`<title>page</title><p><svg><title>tip</title></svg></p>`, "head > title", nil},
		{`<title>page</title><p><svg><title>tip</title></svg></p>`, ":not(svg) > title", nil},

		// Whereas naming the foreign context works in both documents.
		{`<title>page</title><p><svg><title>tip</title></svg></p>`, "svg title", []string{"tip"}},
		{`<html><head><title>page</title></head><body><svg><title>tip</title></svg></body></html>`,
			"svg title", []string{"tip"}},
	} {
		var got []string
		if _, err := lolhtml.RewriteString(tt.doc,
			lolhtml.OnText(tt.selector, func(tc *lolhtml.TextChunk) error {
				if tc.Text() != "" {
					got = append(got, tc.Text())
				}
				return nil
			})); err != nil {
			t.Fatalf("%s on %s: %v", tt.selector, tt.doc, err)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s on %.48s matched %v, want %v", tt.selector, tt.doc, got, tt.want)
		}
	}
}

package main

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func normalise(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Normalise(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Normalise(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTextReferencesAreDecodedExceptTheOnesThatMean Something.
func TestTextReferencesAreDecodedExceptTheOnesThatMeanSomething(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<p>Caf&eacute;</p>`, "<p>Caf\u00e9</p>"},
		{`<p>&#8212;</p>`, "<p>\u2014</p>"},
		{`<p>&#x2014;</p>`, "<p>\u2014</p>"},
		{`<p>&#39;quoted&#39;</p>`, `<p>'quoted'</p>`},
		{`<p>&quot;quoted&quot;</p>`, `<p>"quoted"</p>`},
		{`<p>&gt;</p>`, `<p>></p>`},
		// These carry meaning in text and stay.
		{`<p>&amp;</p>`, `<p>&amp;</p>`},
		{`<p>&lt;</p>`, `<p>&lt;</p>`},
		// A reference without its semicolon is still a reference in text.
		{`<p>&eacute</p>`, "<p>\u00e9</p>"},
		{`<p>&amp</p>`, `<p>&amp</p>`},
		// The longest match wins, as the specification says.
		{`<p>&notit;</p>`, "<p>\u00acit;</p>"},
		// Not references at all: left exactly as they are.
		{`<p>a & b</p>`, `<p>a & b</p>`},
		{`<p>&unknown;</p>`, `<p>&unknown;</p>`},
		{`<p>&#;</p>`, `<p>&#;</p>`},
		{`<p>&#x;</p>`, `<p>&#x;</p>`},
	} {
		got, _ := normalise(t, tc.doc, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheDocumentStillSaysTheSameThing, which is the whole claim: decode the reference
// and a parser reads the same characters.
func TestTheDocumentStillSaysTheSameThing(t *testing.T) {
	for _, doc := range []string{
		`<p>Caf&eacute; &amp; bar &#8212; open &lt;now&gt;</p>`,
		`<p>&notit; &amp; &#39;q&#39; &nbsp;x</p>`,
		`<p>a & b &unknown; &#x2014;</p>`,
	} {
		got, _ := normalise(t, doc, Options{})
		if before, after := stdhtml.UnescapeString(text(t, doc)), stdhtml.UnescapeString(text(t, got)); before != after {
			t.Errorf("%q\n before %q\n  after %q", doc, before, after)
		}
	}
}

// text returns the text of the one paragraph in doc, as the library reports it.
func text(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
		b.WriteString(c.Text())
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestTheInvisibleOnesStayEncoded, because the reference is the only thing that shows
// them in the source.
func TestTheInvisibleOnesStayEncoded(t *testing.T) {
	for _, ref := range []string{"&nbsp;", "&shy;", "&zwj;", "&thinsp;", "&#160;", "&#8203;"} {
		doc := `<p>a` + ref + `b</p>`
		got, res := normalise(t, doc, Options{})
		if got != doc {
			t.Errorf("%s was decoded: %q", ref, got)
		}
		if res.Kept != 1 {
			t.Errorf("%s: %v", ref, res)
		}
	}
	// A caller can keep more, by the spelling the document used.
	got, _ := normalise(t, `<p>&eacute; &mdash;</p>`, Options{Keep: map[string]bool{"&mdash;": true}})
	if want := "<p>\u00e9 &mdash;</p>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestGtCanBeKept, since a bare ">" is valid character data but a diff full of them is
// noise for some callers.
func TestGtCanBeKept(t *testing.T) {
	got, _ := normalise(t, `<p>a &gt; b</p>`, Options{GT: true})
	if want := `<p>a &gt; b</p>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	got, _ = normalise(t, `<p>a &gt; b</p>`, Options{})
	if want := `<p>a > b</p>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestTheAttributeRuleIsTheParsersRuleAndNotTheStandardLibrarys, which is the
// difference between normalising a URL and changing it.
func TestTheAttributeRuleIsTheParsersRuleAndNotTheStandardLibrarys(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// No semicolon and "=" or alphanumeric after: not a reference, left alone.
		{`?a=1&copy=2`, `?a=1&copy=2`},
		{`?a=1&notit=2`, `?a=1&notit=2`},
		{`?a=1&amp=2`, `?a=1&amp=2`},
		{`?a=1&lt=2`, `?a=1&lt=2`},
		{`?a=1&copy2`, `?a=1&copy2`},
		// With the semicolon it is a reference: kept where it is markup, decoded
		// where it is not.
		{`?a=1&amp;b=2`, `?a=1&amp;b=2`},
		{`?a=1&copy;=2`, "?a=1\u00a9=2"},
		{`?x=&#39;q&#39;`, `?x='q'`},
		// A numeric reference has no semicolon rule of its own here, and decodes.
		{`?x=&#8212;`, "?x=\u2014"},
		// The double quote stays, because the library writes the quotes.
		{`?x=&quot;q&quot;`, `?x=&quot;q&quot;`},
		// At the end of the value, or before something that is neither "=" nor
		// alphanumeric, a name without its semicolon is a reference.
		{`?x=&gt`, `?x=>`},
		{`?x=&gt.`, `?x=>.`},
	} {
		doc := `<a href="` + tc.in + `">l</a>`
		got, _ := normalise(t, doc, Options{Attributes: true})
		if want := `<a href="` + tc.want + `">l</a>`; got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, want)
		}
	}
	// The standard library disagrees about the first group, which is why this program
	// does not call it on a whole value.
	if stdhtml.UnescapeString(`?a=1&copy=2`) == `?a=1&copy=2` {
		t.Error("html.UnescapeString has started leaving that alone; the rule this " +
			"program implements may no longer be needed")
	}
}

// TestAttributesAreOptOut... in: off by default, since a URL is the value most likely
// to be read by something other than a browser.
func TestAttributesAreOptIn(t *testing.T) {
	const doc = `<a href="?a=1&#39;q&#39;" title="Caf&eacute;">l</a>`
	got, res := normalise(t, doc, Options{})
	if got != doc {
		t.Errorf("without -attributes the document changed: %q", got)
	}
	if res.Attrs != 0 {
		t.Errorf("%v", res)
	}
	got, res = normalise(t, doc, Options{Attributes: true})
	if want := "<a href=\"?a=1'q'\" title=\"Caf\u00e9\">l</a>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Attrs != 3 || res.Elements != 1 {
		t.Errorf("%v", res)
	}
}

// TestAnUnpairedSurrogateOrAnImpossibleNumberIsLeftAlone: the decoder gives the
// replacement character for those, and writing it would change what the document says.
func TestAnUnpairedSurrogateOrAnImpossibleNumberIsLeftAlone(t *testing.T) {
	for _, ref := range []string{"&#xD800;", "&#x110000;", "&#0;", "&#xFFFFFF;"} {
		doc := `<p>a` + ref + `b</p>`
		got, res := normalise(t, doc, Options{})
		if got != doc {
			t.Errorf("%s was rewritten: %q", ref, got)
		}
		if res.Kept != 1 {
			t.Errorf("%s: %v", ref, res)
		}
	}
}

// TestRawTextIsNotText: a script or a style holds no references, so nothing in it is
// needlessly escaped and nothing should change.
func TestRawTextIsNotText(t *testing.T) {
	for _, doc := range []string{
		`<script>var a = "&amp;" + b &lt; c;</script>`,
		`<style>.a::after{content:"&eacute;"}</style>`,
	} {
		got, res := normalise(t, doc, Options{})
		if got != doc {
			t.Errorf("%q\n got %q", doc, got)
		}
		if res.Text != 0 {
			t.Errorf("%q: %v", doc, res)
		}
	}
	// A textarea and a title do decode references, so they are ordinary text here.
	got, _ := normalise(t, `<textarea>Caf&eacute;</textarea>`, Options{})
	if want := "<textarea>Caf\u00e9</textarea>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestNormalisingTwiceChangesNothing.
func TestNormalisingTwiceChangesNothing(t *testing.T) {
	for _, opts := range []Options{{}, {Attributes: true}, {GT: true}} {
		for _, doc := range []string{
			`<p>Caf&eacute; &amp; &#8212; &lt;x&gt; &nbsp;y</p>`,
			`<a href="?a=1&copy=2&amp;b=&#39;q&#39;">l</a>`,
			`<p>a & b &unknown;</p>`,
			`<script>a &lt; b</script>`,
		} {
			once, _ := normalise(t, doc, opts)
			twice, res := normalise(t, once, opts)
			if twice != once {
				t.Errorf("%q (%+v)\n once %q\ntwice %q", doc, opts, once, twice)
			}
			if res.Text != 0 || res.Attrs != 0 {
				t.Errorf("%q: the second pass decoded %v", doc, res)
			}
		}
	}
}

// TestAReferenceSplitAcrossWritesIsStillOneReference, which is why the text is
// accumulated to the end of the node.
func TestAReferenceSplitAcrossWritesIsStillOneReference(t *testing.T) {
	const doc = `<p>Caf&eacute; &amp; bar &#8212; &lt;x&gt;</p><a href="?a=1&copy=2&amp;b=&#39;q&#39;">l</a>`
	want, wantRes := normalise(t, doc, Options{Attributes: true})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		n := &normaliser{opts: Options{Keep: DefaultKeep, Attributes: true}}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, n.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if n.res.Text != wantRes.Text || n.res.Attrs != wantRes.Attrs {
			t.Errorf("chunks of %d: %v, want %v", size, n.res, wantRes)
		}
	}
}

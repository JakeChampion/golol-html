package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func convert(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Convert(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Convert(%q): %v", doc, err)
	}
	return out.String(), res
}

// span is the conversion markup, so the tests say what they mean.
func span(to, title, text string) string {
	return `<span class="unit" data-unit="` + to + `" title="` + title + `">` + text + `</span>`
}

// TestTheQuantitiesAreConverted.
func TestTheQuantitiesAreConverted(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<p>12 miles</p>", "<p>" + span("km", "12 miles", "19.3 km") + "</p>"},
		{"<p>1 mile</p>", "<p>" + span("km", "1 mile", "1.6 km") + "</p>"},
		{"<p>12mi</p>", "<p>" + span("km", "12mi", "19.3 km") + "</p>"},
		{"<p>6 ft</p>", "<p>" + span("m", "6 ft", "1.83 m") + "</p>"},
		{"<p>100 yards</p>", "<p>" + span("m", "100 yards", "91.4 m") + "</p>"},
		{"<p>5.5 inches</p>", "<p>" + span("cm", "5.5 inches", "14 cm") + "</p>"},
		{"<p>1,234 lbs</p>", "<p>" + span("kg", "1,234 lbs", "559.73 kg") + "</p>"},
		{"<p>60 mph</p>", "<p>" + span("km/h", "60 mph", "97 km/h") + "</p>"},
		{"<p>72°F</p>", "<p>" + span("°C", "72°F", "22.2 °C") + "</p>"},
		{"<p>32 °F</p>", "<p>" + span("°C", "32 °F", "0 °C") + "</p>"},
		{"<p>1 gallon</p>", "<p>" + span("L", "1 gallon", "3.8 L") + "</p>"},
		// The rest of the node is untouched, and a reference in it stays a
		// reference.
		{"<p>a &amp; 12 miles &amp; b</p>",
			"<p>a &amp; " + span("km", "12 miles", "19.3 km") + " &amp; b</p>"},
		{"<p>up to 3 miles or 4 miles</p>",
			"<p>up to " + span("km", "3 miles", "4.8 km") + " or " + span("km", "4 miles", "6.4 km") + "</p>"},
	} {
		if got, _ := convert(t, tc.in); got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// TestTheBoundariesHold. A unit name is a word, and a number followed by
// something that is not a unit is a number.
func TestTheBoundariesHold(t *testing.T) {
	for _, in := range []string{
		"<p>3 milestones</p>",
		"<p>3 miley</p>",
		"<p>12 million</p>",
		"<p>3 feetball</p>",
		"<p>7 lbsx</p>",
		"<p>2026-08-25</p>",
		"<p>version 12</p>",
		"<p>12</p>",
		"<p>miles</p>",
		"<p>a12 miles</p>", // the number is part of a word
		"<p>12.5.3 miles</p>",
	} {
		got, res := convert(t, in)
		if got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
		if len(res.Converted) != 0 {
			t.Errorf("%q: %v", in, res)
		}
	}
}

// TestTheAmbiguousSpellingsAreLeftAloneAndCounted. Converting "3 in 5 people"
// would be worse than not converting it, and a page that meant inches should be
// visible rather than silently ignored.
func TestTheAmbiguousSpellingsAreLeftAloneAndCounted(t *testing.T) {
	for _, tc := range []struct{ in, name string }{
		{"<p>3 in 5 people</p>", "in"},
		{"<p>8 oz of it</p>", "oz"},
		{"<p>2 gal</p>", "gal"},
		{"<p>72 F</p>", "f"},
		{"<p>3 pt</p>", "pt"},
		{"<p>2 tons</p>", "tons"},
	} {
		got, res := convert(t, tc.in)
		if got != tc.in {
			t.Errorf("%q was rewritten to %q", tc.in, got)
		}
		if res.Ambiguous[tc.name] != 1 {
			t.Errorf("%q: Ambiguous = %v, want one %q", tc.in, res.Ambiguous, tc.name)
		}
		if !strings.Contains(res.String(), "ambiguous") {
			t.Errorf("%q: the report does not mention it: %s", tc.in, res)
		}
	}
}

// TestTheRegionsThatAreNotProseAreSkipped.
func TestTheRegionsThatAreNotProseAreSkipped(t *testing.T) {
	for _, in := range []string{
		"<code>12 miles</code>",
		"<kbd>12 miles</kbd>",
		"<samp>12 miles</samp>",
		"<var>12 miles</var>",
		"<pre>12 miles</pre>",
		"<script>var d = 12 miles</script>",
		"<style>/* 12 miles */</style>",
		"<textarea>12 miles</textarea>",
		"<title>12 miles</title>",
		"<code>a <b>12 miles</b> b</code>",
	} {
		got, res := convert(t, in)
		if got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
		if res.Regions == 0 {
			t.Errorf("%q: no region counted", in)
		}
	}
	// And prose around a skipped region is still converted.
	got, _ := convert(t, "<p>12 miles <code>12 miles</code> 12 miles</p>")
	if n := strings.Count(got, "19.3 km"); n != 2 {
		t.Errorf("got %q, want the two prose conversions and not the code one", got)
	}
}

// TestTheTitleSaysWhatThePageSaid, which is the point of the normalisation: the
// original is bytes, and a parser does not read all of those bytes the way they
// were written.
func TestTheTitleSaysWhatThePageSaid(t *testing.T) {
	for _, tc := range []struct {
		in, wantTitle string
		normalised    int
	}{
		{"<p>12 miles</p>", "12 miles", 0},
		// A CR is a LF to every parser, so a title holding one quotes something
		// nobody could have seen.
		{"<p>12\rmiles</p>", "12\nmiles", 1},
		{"<p>12\r\nmiles</p>", "12\nmiles", 1},
		{"<p>12\nmiles</p>", "12\nmiles", 0},
		// A NUL is dropped from text by a parser and kept in an attribute value,
		// so copying it into the title would put back what the reader never had.
		{"<p>12\x00 miles</p>", "12 miles", 1},
	} {
		got, res := convert(t, tc.in)
		title := ""
		if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			title, _ = e.Attribute("title")
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if title != tc.wantTitle {
			t.Errorf("%q: title = %q, want %q", tc.in, title, tc.wantTitle)
		}
		if res.Normalised != tc.normalised {
			t.Errorf("%q: Normalised = %d, want %d", tc.in, res.Normalised, tc.normalised)
		}
	}
}

// TestANodeWithNoConversionIsUntouched, so the normalisation cannot reach text
// this program was not already rewriting.
func TestANodeWithNoConversionIsUntouched(t *testing.T) {
	for _, doc := range []string{
		"<p>a\rb</p>",
		"<p>a\x00b</p>",
		"<p>3 milestones\rand 8 oz</p>",
		"<p>a\r\nb &amp; c</p>",
	} {
		if got, _ := convert(t, doc); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
	// And a node that does get a conversion comes out normalised, which is the
	// trade this makes deliberately.
	got, res := convert(t, "<p>a\rb and 12 miles</p>")
	if !strings.Contains(got, "a\nb") {
		t.Errorf("got %q, want the CR normalised in a node that was rewritten", got)
	}
	if res.Normalised != 1 {
		t.Errorf("Normalised = %d, want 1", res.Normalised)
	}
}

// TestAQuoteInTheOriginalCannotEndTheAttribute. The program writes the span
// itself, so it is the serialiser for that attribute.
func TestAQuoteInTheOriginalCannotEndTheAttribute(t *testing.T) {
	// A double quote cannot appear between a number and a unit name, so the case
	// is constructed: the original is the matched text, and the matched text is
	// what goes in the title. attrValue is what protects the attribute, and this
	// tests it directly along with the whole shape.
	if got := attrValue(`a"b`); got != "a&quot;b" {
		t.Errorf("attrValue(%q) = %q", `a"b`, got)
	}
	if got := attrValue("a&amp;b"); got != "a&amp;b" {
		t.Errorf("attrValue kept a reference as a reference: %q", got)
	}

	// And end to end, the span the program writes reads back as one element with
	// exactly three attributes.
	out, _ := convert(t, `<p>12 miles</p>`)
	attrs := map[string]string{}
	if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		for _, a := range e.AttributeList() {
			attrs[a.Name] = a.Value
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 3 || attrs["class"] != "unit" || attrs["title"] != "12 miles" {
		t.Errorf("attributes = %v", attrs)
	}
}

// TestOnlyTheSpansAreAdded: a tag-by-tag reading of the output is the input's tags
// with one span per conversion, which is the check that matters when everything
// else is text.
func TestOnlyTheSpansAreAdded(t *testing.T) {
	const doc = `<div><p>a 12 mile walk and <b>60 mph</b></p><code>5 ft</code><ul><li>3 lbs</ul></div>`
	got, res := convert(t, doc)
	want := tags(t, doc)
	for range res.Converted["km"] + res.Converted["km/h"] + res.Converted["kg"] {
		_ = want
	}
	gotTags := tags(t, got)
	// Remove the spans the program added and the sequences have to be equal.
	stripped := strings.ReplaceAll(gotTags, "<span></span>", "")
	if stripped != want {
		t.Errorf("\n got %q\nwant %q", stripped, want)
	}
	if strings.Count(gotTags, "<span>") != 3 {
		t.Errorf("%q has %d spans, want 3", gotTags, strings.Count(gotTags, "<span>"))
	}
}

func tags(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		if !e.CanHaveContent() {
			return nil
		}
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			b.WriteString("</" + t.Name() + ">")
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestConvertingTwiceChangesNothing, because a conversion is marked and the mark
// is skipped.
func TestConvertingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		"<p>12 miles</p>",
		"<p>a 12 mile walk at 60 mph in 72°F</p>",
		"<p>3 in 5 people</p>",
		"<code>12 miles</code>",
		"<p>12\rmiles</p>",
	} {
		once, _ := convert(t, doc)
		twice, res := convert(t, once)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if len(res.Converted) != 0 {
			t.Errorf("%q: the second pass converted %v", doc, res.Converted)
		}
	}
}

// TestAQuantityCanStraddleChunks, which is why the node is accumulated.
func TestAQuantityCanStraddleChunks(t *testing.T) {
	const doc = `<div><p>a 12 mile walk, 1,234 lbs, 72°F</p><code>9 ft</code><p>60 mph</p></div>`
	want, _ := convert(t, doc)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		c := &converter{res: Result{Converted: map[string]int{}, Ambiguous: map[string]int{}}}
		w, err := lolhtml.NewWriter(&out, c.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// TestAQuantityInAnAttributeIsNotText, so nothing in an attribute is converted.
func TestAQuantityInAnAttributeIsNotText(t *testing.T) {
	for _, doc := range []string{
		`<p title="12 miles">x</p>`,
		`<img alt="60 mph" src="/x">`,
		`<a href="/12-miles">y</a>`,
	} {
		if got, _ := convert(t, doc); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
}

// TestTheConversionsAreRight, checked against the factors rather than against the
// program: a conversion that is consistent with itself and wrong is the failure
// this catches.
func TestTheConversionsAreRight(t *testing.T) {
	for _, tc := range []struct {
		value    float64
		unit     string
		expected string
	}{
		{1, "mile", "1.6 km"},      // 1.609344
		{26.2, "miles", "42.2 km"}, // a marathon
		{1, "foot", "0.3 m"},
		{6, "feet", "1.83 m"},
		{1, "inch", "2.5 cm"},
		{1, "pound", "0.45 kg"},
		{220, "lbs", "99.79 kg"},
		{0, "°F", "-17.8 °C"},
		{100, "°F", "37.8 °C"},
		{212, "°F", "100 °C"},
	} {
		var u Unit
		for _, candidate := range Units {
			for _, n := range candidate.Names {
				if n == strings.ToLower(tc.unit) {
					u = candidate
				}
			}
		}
		if u.To == "" {
			t.Fatalf("no unit named %q", tc.unit)
		}
		if got := u.convert(tc.value); got != tc.expected {
			t.Errorf("%v %s = %q, want %q", tc.value, tc.unit, got, tc.expected)
		}
	}
}

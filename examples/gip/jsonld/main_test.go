package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const doc = `<html><head>` +
	`<script type="application/ld+json">{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}</script>` +
	`</head><body>` +
	`<script type="APPLICATION/LD+JSON">{"@type":"Product","offers":null}</script>` +
	`<script type="application/ld+json">not json</script>` +
	`<script type="application/ld+json"></script>` +
	`<script>var a = 1</script>` +
	`</body></html>`

var corpus = []string{
	doc,
	`<script type="application/ld+json">{"@context":"https://schema.org","@type":"Thing"}</script>`,
	`<script type="application/ld+json">[{"@context":"https://schema.org","@type":"A"},{"@type":"B"}]</script>`,
	`<script type="application/ld+json">[]</script>`,
	`<script type="application/ld+json">"a string"</script>`,
	`<script type="application/ld+json">{"@context":"https://example.com","@type":"X"}</script>`,
	`<script type="application/ld+json">{"@context":"https://schema.org","@type":[]}</script>`,
	`<script type="application/ld+json">{"@context":"https://schema.org","@type":7}</script>`,
	`<script type="application/ld+json">   </script>`,
	`<script type="application/ld+json"/><p>self-closing</p>`,
	`<p>no blocks at all</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*extractor)) (string, *extractor, error) {
	x := defaults()
	for _, o := range opts {
		o(x)
	}
	if err := x.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, x.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), x, nil
}

// TestTheDocumentIsUnchanged: this program reads and reports, so every byte has
// to come out as it went in, at every chunk size.
func TestTheDocumentIsUnchanged(t *testing.T) {
	for _, in := range corpus {
		for _, n := range []int{len(in) + 1, 1, 2, 3, 17} {
			out, _, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if out != in {
				t.Errorf("chunk %d changed the document:\n in: %q\nout: %q", n, in, out)
			}
		}
	}
}

// TestTheReportIsChunkInvariant. The blocks are read from text chunks, which
// chunking does split, so this is the property that says the reassembly works.
func TestTheReportIsChunkInvariant(t *testing.T) {
	for _, in := range corpus {
		_, whole, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		want := whole.report()
		for _, n := range []int{1, 2, 3, 17} {
			_, got, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got.report() != want {
				t.Errorf("chunk %d changed the report for %q:\n whole:\n%s got:\n%s",
					n, in, want, got.report())
			}
		}
	}
}

// TestTheReportedRangeSlicesToTheBlock is what makes the offsets worth
// reporting: a caller holding the input can cut out exactly the bytes complained
// about. It holds at every chunk size, because the offsets are counted from the
// first byte fed in rather than from the current write.
func TestTheReportedRangeSlicesToTheBlock(t *testing.T) {
	for _, n := range []int{len(doc) + 1, 1, 5, 23} {
		_, x, err := chunked(doc, n)
		if err != nil {
			t.Fatalf("chunk %d: %v", n, err)
		}
		if len(x.blocks) != 4 {
			t.Fatalf("chunk %d: %d blocks, want 4", n, len(x.blocks))
		}
		for i, b := range x.blocks {
			if b.raw == "" {
				continue // an empty block reports its start tag instead
			}
			if got := doc[b.loc.Start:b.loc.End]; got != b.raw {
				t.Errorf("chunk %d: block %d at %v slices to %q, want %q",
					n, i+1, b.loc, got, b.raw)
			}
		}
	}
}

// TestAnEmptyBlockReportsItsOwnPosition. It has no text, so there is no text
// chunk to take a location from - and the first version of this program reported
// the previous block's offsets instead, which is worse than reporting none.
func TestAnEmptyBlockReportsItsOwnPosition(t *testing.T) {
	_, x, err := chunked(doc, len(doc)+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.blocks) != 4 {
		t.Fatalf("%d blocks, want 4", len(x.blocks))
	}
	third, fourth := x.blocks[2], x.blocks[3]
	if fourth.loc == third.loc {
		t.Errorf("the empty block reports %v, the same as the block before it", fourth.loc)
	}
	if !strings.HasPrefix(doc[fourth.loc.Start:fourth.loc.End], "<script") {
		t.Errorf("the empty block's range %v is not its start tag: %q",
			fourth.loc, doc[fourth.loc.Start:fourth.loc.End])
	}
}

// TestTheTypeAttributeIsMatchedWithoutRegardToCase, because type is on the HTML
// list of attributes whose values are matched that way. The second block in the
// corpus is APPLICATION/LD+JSON.
func TestTheTypeAttributeIsMatchedWithoutRegardToCase(t *testing.T) {
	_, x, err := chunked(doc, len(doc)+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.blocks) != 4 {
		t.Errorf("%d blocks, want 4 - an upper-case type was missed", len(x.blocks))
	}
}

// TestOnlyJSONLDScriptsAreRead: an ordinary script is not a JSON-LD block.
func TestOnlyJSONLDScriptsAreRead(t *testing.T) {
	_, x, err := chunked(`<script>var a = 1</script><script type="text/javascript">b</script>`+
		`<script type="application/json">{"a":1}</script>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.blocks) != 0 {
		t.Errorf("%d blocks, want 0: %v", len(x.blocks), x.blocks)
	}
}

// TestWhatCountsAsAProblem.
func TestWhatCountsAsAProblem(t *testing.T) {
	for _, tt := range []struct {
		json     string
		problems []string
	}{
		{`{"@context":"https://schema.org","@type":"Thing"}`, nil},
		{`{"@type":"Thing"}`, []string{`missing "@context"`}},
		{`{"@context":"https://example.com","@type":"Thing"}`,
			[]string{`"https://example.com" is not a schema.org context`}},
		{`{"@context":"https://schema.org","@type":""}`, []string{`"@type" is empty`}},
		{`{"@context":"https://schema.org","@type":[]}`, []string{`"@type" is an empty array`}},
		{`{"@context":"https://schema.org","@type":7}`,
			[]string{`"@type" is a number, not a string or an array`}},
		{`{"@context":"https://schema.org","@type":"T","a":null}`, []string{`"a" is null`}},
		{`"a string"`, []string{"the top level is a string, not an object or an array"}},
		{`[]`, []string{"the array is empty"}},
		{`[1]`, []string{"item 0 is a number, not an object"}},
		{`   `, []string{"the block is empty"}},
	} {
		x := defaults()
		b := x.check(lolhtml.SourceLocation{}, tt.json)
		if len(b.problems) != len(tt.problems) {
			t.Errorf("%s -> %v, want %v", tt.json, b.problems, tt.problems)
			continue
		}
		for i := range tt.problems {
			if b.problems[i] != tt.problems[i] {
				t.Errorf("%s -> problem %d is %q, want %q",
					tt.json, i, b.problems[i], tt.problems[i])
			}
		}
	}
}

// TestANestedNodeNeedsNoContextOfItsOwn, since it inherits the outer one.
func TestANestedNodeNeedsNoContextOfItsOwn(t *testing.T) {
	x := defaults()
	b := x.check(lolhtml.SourceLocation{},
		`[{"@context":"https://schema.org","@type":"A"},{"@type":"B"}]`)
	for _, p := range b.problems {
		if strings.Contains(p, "@context") {
			t.Errorf("a nested node was asked for a context: %v", b.problems)
		}
	}
}

// TestStrictAsksForAType, which is not a problem by default because a node can
// legitimately be typed by its position in an outer node.
func TestStrictAsksForAType(t *testing.T) {
	const json = `{"@context":"https://schema.org","name":"x"}`

	x := defaults()
	if b := x.check(lolhtml.SourceLocation{}, json); len(b.problems) != 0 {
		t.Errorf("default reported %v", b.problems)
	}

	x = defaults()
	x.strict = true
	b := x.check(lolhtml.SourceLocation{}, json)
	if len(b.problems) != 1 || b.problems[0] != `missing "@type"` {
		t.Errorf("strict reported %v", b.problems)
	}
}

// TestReferencesAreNotDecodedInAScriptBody: a script's content is raw text, so
// "&amp;" in it is five characters of JSON and decoding it here would change the
// document's meaning.
func TestReferencesAreNotDecodedInAScriptBody(t *testing.T) {
	_, x, err := chunked(
		`<script type="application/ld+json">{"@context":"https://schema.org","@type":"T","name":"a&amp;b"}</script>`,
		1)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.blocks) != 1 {
		t.Fatalf("%d blocks", len(x.blocks))
	}
	if !strings.Contains(x.blocks[0].raw, `a&amp;b`) {
		t.Errorf("the raw block was decoded: %q", x.blocks[0].raw)
	}
	if len(x.blocks[0].problems) != 0 {
		t.Errorf("valid JSON was reported as a problem: %v", x.blocks[0].problems)
	}
}

// TestTypeOfReadsTheRawJSON, so a block that failed to parse can still be
// identified in the report.
func TestTypeOf(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{`{"@type":"Product"}`, "Product"},
		{`{"@type": "Product" }`, "Product"},
		{`{"a":1,"@type":"A","b":2}`, "A"},
		{`{"@type":"Product"`, "Product"}, // truncated but legible
		{`{"@type":`, "?"},
		{`{}`, "?"},
		{``, "?"},
		// A script body is not decoded by a parser, so "&amp;" here is five
		// characters of the JSON string and reporting "a&b" would name a value
		// the document does not contain.
		{`{"@type":"a&amp;b"}`, "a&amp;b"},
	} {
		if got := typeOf(tt.raw); got != tt.want {
			t.Errorf("typeOf(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// TestABlockLargerThanTheLimitIsReportedNotChecked.
func TestABlockLargerThanTheLimitIsReportedNotChecked(t *testing.T) {
	big := `<script type="application/ld+json">{"a":"` + strings.Repeat("x", 500) + `"}</script>`
	_, x, err := chunked(big, len(big)+1, func(x *extractor) { x.maxBytes = 100 })
	if err != nil {
		t.Fatal(err)
	}
	if len(x.blocks) != 0 || total(x.skipped) != 1 {
		t.Errorf("blocks=%d skipped=%v", len(x.blocks), x.skipped)
	}
}

// TestTheReportIsStable, so two runs over the same document can be diffed.
func TestTheReportIsStable(t *testing.T) {
	_, x, err := chunked(doc, len(doc)+1)
	if err != nil {
		t.Fatal(err)
	}
	first := x.report()
	if second := x.report(); first != second {
		t.Errorf("the report changed between calls:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "blocks=4 problems=4") {
		t.Errorf("unexpected summary:\n%s", first)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	if _, _, err := extractString(doc, func(x *extractor) { x.maxBytes = 0 }); err == nil {
		t.Error("-max-bytes 0 was accepted")
	}
}

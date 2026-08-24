package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const product = `<html><body>` +
	`<div itemscope itemtype="https://schema.org/Product">` +
	`<h1 itemprop="name">Widget &amp; Co</h1>` +
	`<meta itemprop="sku" content="W-1">` +
	`<div itemprop="offers" itemscope itemtype="https://schema.org/Offer">` +
	`<span itemprop="price">9.99</span><meta itemprop="priceCurrency" content="GBP">` +
	`</div>` +
	`<a itemprop="url" href="/w">link</a>` +
	`<time itemprop="released" datetime="2024-01-02">Jan</time>` +
	`</div><span itemprop="stray">outside</span></body></html>`

var corpus = []string{
	product,
	`<div itemscope itemtype="/T"><div itemprop="outer">before <span itemprop="inner">in</span> after</div></div>`,
	`<div itemscope><span itemprop="a">1</span></div>`,
	`<div itemscope itemtype="https://schema.org/Thing#x"><span itemprop="a">1</span></div>`,
	`<div itemscope itemtype="/T"><div itemprop="p" itemscope><span itemprop="q">1</span></div></div>`,
	`<div itemscope itemtype="/T"><meta itemscope itemprop="p" content="x"></div>`,
	`<div itemscope itemtype="/T"><span itemprop="empty">   </span></div>`,
	`<div itemscope itemtype="/T"><span itemprop="a">1</span></div><div itemscope itemtype="/U"><span itemprop="b">2</span></div>`,
	`<span itemprop="lonely">x</span>`,
	`<p>no microdata</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*reader)) (string, *reader, error) {
	r := defaults()
	for _, o := range opts {
		o(r)
	}
	if err := r.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, r.options()...)
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
	return out.String(), r, nil
}

// TestTheDocumentIsUnchanged: this program reports and changes nothing.
func TestTheDocumentIsUnchanged(t *testing.T) {
	for _, in := range corpus {
		for _, n := range []int{len(in) + 1, 1, 2, 3, 19} {
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

func TestTheReportIsChunkInvariant(t *testing.T) {
	for _, in := range corpus {
		_, whole, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		want := whole.report()
		for _, n := range []int{1, 2, 3, 19} {
			_, got, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got.report() != want {
				t.Errorf("chunk %d changed the report for %q:\nwant:\n%s got:\n%s",
					n, in, want, got.report())
			}
		}
	}
}

// TestTheNestingIsRecovered is the point of the stack: an itemprop belongs to
// the nearest enclosing itemscope, and a rewriter has no tree to ask.
func TestTheNestingIsRecovered(t *testing.T) {
	_, r, err := chunked(product, len(product)+1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Product.name = Widget & Co",
		"Product.sku = W-1",
		"Product.offers.price = 9.99",
		"Product.offers.priceCurrency = GBP",
		"Product.url = /w",
		"Product.released = 2024-01-02",
		"stray = outside",
	}
	var got []string
	for _, p := range r.pairs {
		got = append(got, p.key+" = "+p.value)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestANestedItempropGetsItsOwnTextAndTheOuterGetsAll. This was wrong first: one
// variable for the property being gathered produced the inner value twice and
// lost the outer one. An itemprop's value is all of its text, so the collection
// has to be a stack.
func TestANestedItempropGetsItsOwnTextAndTheOuterGetsAll(t *testing.T) {
	_, r, err := chunked(
		`<div itemscope itemtype="/T"><div itemprop="outer">before <span itemprop="inner">in</span> after</div></div>`,
		1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range r.pairs {
		if _, seen := got[p.key]; seen {
			t.Errorf("%s was reported twice", p.key)
		}
		got[p.key] = p.value
	}
	if got["T.inner"] != "in" {
		t.Errorf("T.inner = %q, want %q", got["T.inner"], "in")
	}
	if got["T.outer"] != "before in after" {
		t.Errorf("T.outer = %q, want %q", got["T.outer"], "before in after")
	}
}

// TestValueAttributesFollowTheSpecification: a value can come from an attribute
// rather than from text, and which attribute depends on the element.
func TestValueAttributesFollowTheSpecification(t *testing.T) {
	for _, tt := range []struct{ markup, want string }{
		{`<meta itemprop="p" content="c">`, "c"},
		{`<img itemprop="p" src="s">`, "s"},
		{`<iframe itemprop="p" src="s"></iframe>`, "s"},
		{`<a itemprop="p" href="h">text</a>`, "h"},
		{`<link itemprop="p" href="h">`, "h"},
		{`<area itemprop="p" href="h">`, "h"},
		{`<object itemprop="p" data="d"></object>`, "d"},
		{`<data itemprop="p" value="v">text</data>`, "v"},
		{`<meter itemprop="p" value="v"></meter>`, "v"},
		{`<time itemprop="p" datetime="t">text</time>`, "t"},
		{`<span itemprop="p">text</span>`, "text"},
		{`<div itemprop="p">text</div>`, "text"},
		// The value attribute is absent, so the text is used instead.
		{`<a itemprop="p">text</a>`, "text"},
	} {
		doc := `<div itemscope itemtype="/T">` + tt.markup + `</div>`
		_, r, err := chunked(doc, len(doc)+1)
		if err != nil {
			t.Fatalf("%s: %v", tt.markup, err)
		}
		if len(r.pairs) != 1 {
			t.Errorf("%s: %d values, want 1: %v", tt.markup, len(r.pairs), r.pairs)
			continue
		}
		if r.pairs[0].value != tt.want {
			t.Errorf("%s: value %q, want %q", tt.markup, r.pairs[0].value, tt.want)
		}
	}
}

// TestTheFirstCopyOfARepeatedAttributeWins, which is what a parser keeps and
// therefore what a browser acts on. Reading through Attribute rather than through
// the iterator is what makes that true here.
func TestTheFirstCopyOfARepeatedAttributeWins(t *testing.T) {
	for _, tt := range []struct{ markup, wantKey, wantValue string }{
		{`<span itemprop="first" itemprop="second">v</span>`, "T.first", "v"},
		{`<meta itemprop="p" content="one" content="two">`, "T.p", "one"},
	} {
		doc := `<div itemscope itemtype="/T">` + tt.markup + `</div>`
		_, r, err := chunked(doc, len(doc)+1)
		if err != nil {
			t.Fatalf("%s: %v", tt.markup, err)
		}
		if len(r.pairs) != 1 {
			t.Fatalf("%s: %v", tt.markup, r.pairs)
		}
		if r.pairs[0].key != tt.wantKey || r.pairs[0].value != tt.wantValue {
			t.Errorf("%s: %s = %s, want %s = %s", tt.markup,
				r.pairs[0].key, r.pairs[0].value, tt.wantKey, tt.wantValue)
		}
	}
}

// TestAnItemscopeWithNoContentIsNotPushed. A void element has no end tag, so
// pushing it would leave the stack permanently deeper and every later property
// would hang off it.
func TestAnItemscopeWithNoContentIsNotPushed(t *testing.T) {
	_, r, err := chunked(
		`<div itemscope itemtype="/T"><meta itemscope itemprop="p" content="x">`+
			`<span itemprop="after">y</span></div>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range r.pairs {
		keys = append(keys, p.key)
	}
	// "after" must still be a property of T, not of the meta.
	if !contains(keys, "T.after") {
		t.Errorf("keys %v; the stack did not come back down", keys)
	}
	if total(r.skipped) == 0 {
		t.Error("the skipped itemscope was not reported")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestTypeName reads the last segment of an itemtype, which is what a report
// wants.
func TestTypeName(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"https://schema.org/Product", "Product"},
		{"https://schema.org/Product/", "Product"},
		{"https://schema.org/Thing#Extra", "Extra"},
		{"Product", "Product"},
		{"", "item"},
		{"/", "item"},
		{"https://schema.org/A https://schema.org/B", "A"},
	} {
		if got := typeName(tt.in); got != tt.want {
			t.Errorf("typeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestValuesAreDecoded, because a report is read by a person and everything the
// library hands over is raw source.
func TestValuesAreDecoded(t *testing.T) {
	_, r, err := chunked(
		`<div itemscope itemtype="/T"><span itemprop="p">a &amp; b &lt;c&gt;</span>`+
			`<meta itemprop="q" content="x &amp; y"></div>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"T.p": "a & b <c>", "T.q": "x & y"}
	for _, p := range r.pairs {
		if want[p.key] != p.value {
			t.Errorf("%s = %q, want %q", p.key, p.value, want[p.key])
		}
	}
}

// TestAnEmptyValueIsNotReported, since a key with nothing behind it is noise.
func TestAnEmptyValueIsNotReported(t *testing.T) {
	_, r, err := chunked(`<div itemscope itemtype="/T"><span itemprop="empty">   </span></div>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.pairs) != 0 {
		t.Errorf("reported %v", r.pairs)
	}
}

// TestTwoItemsAreCountedSeparately.
func TestTwoItemsAreCountedSeparately(t *testing.T) {
	_, r, err := chunked(
		`<div itemscope itemtype="/T"><span itemprop="a">1</span></div>`+
			`<div itemscope itemtype="/U"><span itemprop="b">2</span></div>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.items != 2 {
		t.Errorf("items=%d, want 2", r.items)
	}
	if len(r.pairs) != 2 || r.pairs[0].item == r.pairs[1].item {
		t.Errorf("pairs %v are not in separate items", r.pairs)
	}
}

// TestLimitsAreHonoured.
func TestLimitsAreHonoured(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<div itemscope itemtype="/T">`)
	for i := 0; i < 20; i++ {
		sb.WriteString(`<span itemprop="p">v</span>`)
	}
	sb.WriteString(`</div>`)
	_, r, err := chunked(sb.String(), len(sb.String())+1, func(r *reader) { r.maxPairs = 3 })
	if err != nil {
		t.Fatal(err)
	}
	if len(r.pairs) != 3 || total(r.skipped) == 0 {
		t.Errorf("pairs=%d skipped=%v", len(r.pairs), r.skipped)
	}

	deep := strings.Repeat(`<div itemscope itemtype="/T" itemprop="p">`, 8) +
		strings.Repeat(`</div>`, 8)
	_, r, err = chunked(deep, len(deep)+1, func(r *reader) { r.maxDepth = 3 })
	if err != nil {
		t.Fatal(err)
	}
	if total(r.skipped) == 0 {
		t.Error("the depth limit was not reported")
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, opt := range []func(*reader){
		func(r *reader) { r.maxDepth = 0 },
		func(r *reader) { r.maxPairs = 0 },
	} {
		if _, _, err := readString(product, opt); err == nil {
			t.Error("an unusable limit was accepted")
		}
	}
}

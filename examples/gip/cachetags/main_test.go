package main

import (
	"fmt"
	"strings"
	"testing"
)

func collect(t *testing.T, doc string) Tags {
	t.Helper()
	tags, err := Collect(strings.NewReader(doc), "data-cache-tag", "cache-tag", 4096)
	if err != nil {
		t.Fatalf("Collect(%q): %v", doc, err)
	}
	return tags
}

// TestATagCannotEndTheHeader, which is the one thing that must not happen: a cache tag is exactly
// the sort of value a template interpolates from a database, and a line feed in a header value
// begins another header.
func TestATagCannotEndTheHeader(t *testing.T) {
	for _, spelling := range []string{
		"a\nb",
		"a\rb",
		"a\r\nb",
		"a&#10;b",
		"a&#13;b",
		"a&#xa;b",
		"a&#x0d;b",
		"a&NewLine;b",
		"a&#010;b",
		"\nSet-Cookie: x=1",
		"a&#10;Set-Cookie: x=1",
	} {
		doc := fmt.Sprintf(`<div data-cache-tag="%s">x</div>`, spelling)
		tags := collect(t, doc)
		if len(tags.Names) != 0 {
			t.Errorf("%q was accepted as %v", spelling, tags.Names)
		}
		if len(tags.Refused) == 0 {
			t.Errorf("%q was not refused", spelling)
			continue
		}
		// The header it would have built has no newline in it, whatever happened.
		if h := tags.Header("Cache-Tag"); strings.ContainsAny(h, "\r\n") {
			t.Errorf("%q produced a header containing a newline: %q", spelling, h)
		}
	}

	// The same through the meta path, which splits on commas and so has its own way in.
	for _, spelling := range []string{"a&#10;b", "ok, a&#13;b"} {
		doc := fmt.Sprintf(`<meta name="cache-tag" content="%s">`, spelling)
		tags := collect(t, doc)
		if h := tags.Header("Cache-Tag"); strings.ContainsAny(h, "\r\n") {
			t.Errorf("%q produced %q", spelling, h)
		}
		if len(tags.Refused) == 0 {
			t.Errorf("%q was not refused", spelling)
		}
	}
}

// TestOtherThingsAHeaderValueCannotHold, since the refusal is about what a header is rather than
// about newlines alone.
func TestOtherThingsAHeaderValueCannotHold(t *testing.T) {
	for _, tt := range []struct{ tag, why string }{
		{"a,b", "comma"},
		{"a\x00b", "not a header value"},
		{"a\x1fb", "not a header value"},
		{"a\x7fb", "not a header value"},
		{"caf&eacute;", "outside ASCII"},
		{"a&#233;b", "outside ASCII"},
	} {
		doc := fmt.Sprintf(`<div data-cache-tag="%s">x</div>`, tt.tag)
		tags := collect(t, doc)
		if len(tags.Names) != 0 {
			t.Errorf("%q was accepted as %v", tt.tag, tags.Names)
		}
		if len(tags.Refused) == 0 {
			t.Fatalf("%q was not refused", tt.tag)
		}
		if !strings.Contains(tags.Refused[0].Why, tt.why) {
			t.Errorf("%q refused because %q, want %q", tt.tag, tags.Refused[0].Why, tt.why)
		}
	}

	// And what a tag may hold, so the refusal is not a blanket one.
	for _, tag := range []string{"product-42", "category_shoes", "page.home", "a+b", "A1",
		"tag:with:colons", "tag/with/slashes"} {
		doc := fmt.Sprintf(`<div data-cache-tag="%s">x</div>`, tag)
		tags := collect(t, doc)
		if len(tags.Names) != 1 || tags.Names[0] != tag {
			t.Errorf("%q gave %v and refused %v", tag, tags.Names, tags.Refused)
		}
	}
}

// TestATagIsDecodedBeforeItIsJudgedAndUsed, which is the rule the library runs on: the attribute
// value is source, and the tag is what it decodes to.
func TestATagIsDecodedBeforeItIsJudgedAndUsed(t *testing.T) {
	for _, tt := range []struct{ source, want string }{
		{`a&amp;b`, `a&b`},
		{`a&lt;b`, `a<b`},
		{`&quot;q&quot;`, `"q"`},
		{`a&#45;b`, `a-b`},
		{`plain`, `plain`},
	} {
		doc := fmt.Sprintf(`<div data-cache-tag="%s">x</div>`, tt.source)
		tags := collect(t, doc)
		if len(tags.Names) != 1 {
			t.Fatalf("%q gave %v, refused %v", tt.source, tags.Names, tags.Refused)
		}
		if tags.Names[0] != tt.want {
			t.Errorf("%q became %q, want %q", tt.source, tags.Names[0], tt.want)
		}
	}

	// A tag spelled two ways is one tag, which is the point of decoding before deduplicating.
	tags := collect(t, `<div data-cache-tag="a&#45;b">x</div><div data-cache-tag="a-b">y</div>`)
	if len(tags.Names) != 1 {
		t.Errorf("two spellings of one tag gave %v", tags.Names)
	}
	if tags.Elements != 2 {
		t.Errorf("%d elements, want 2", tags.Elements)
	}
}

// TestTagsAreDeduplicatedInFirstSeenOrder, since a cache key that changes when the page is
// reordered is a cache miss.
func TestTagsAreDeduplicatedInFirstSeenOrder(t *testing.T) {
	tags := collect(t, `<div data-cache-tag="b">1</div><div data-cache-tag="a">2</div>`+
		`<div data-cache-tag="b">3</div><div data-cache-tag="c a">4</div>`)
	if got := strings.Join(tags.Names, ","); got != "b,a,c" {
		t.Errorf("tags %q, want b,a,c", got)
	}
	if tags.Elements != 4 {
		t.Errorf("%d elements, want 4", tags.Elements)
	}
	if got := tags.Header("Cache-Tag"); got != "Cache-Tag: b, a, c" {
		t.Errorf("header %q", got)
	}

	// One attribute carrying several tags is several tags, space-separated like a class.
	tags = collect(t, `<div data-cache-tag="  a   b  c ">x</div>`)
	if got := strings.Join(tags.Names, ","); got != "a,b,c" {
		t.Errorf("tags %q", got)
	}
	if tags.Elements != 1 {
		t.Errorf("%d elements, want 1", tags.Elements)
	}
}

// TestTheBudgetDropsTagsAndSaysWhich, because a header longer than the origin accepts is a request
// that fails, and dropping quietly would make the cache wrong rather than the request.
func TestTheBudgetDropsTagsAndSaysWhich(t *testing.T) {
	doc := `<div data-cache-tag="aaaa bbbb cccc dddd eeee">x</div>`

	full, err := Collect(strings.NewReader(doc), "data-cache-tag", "cache-tag", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Names) != 5 || len(full.Truncated) != 0 {
		t.Fatalf("%v truncated %v", full.Names, full.Truncated)
	}

	// "Cache-Tag: aaaa, bbbb" is 21 bytes.
	tight, err := Collect(strings.NewReader(doc), "data-cache-tag", "cache-tag", 21)
	if err != nil {
		t.Fatal(err)
	}
	if got := tight.Header("Cache-Tag"); got != "Cache-Tag: aaaa, bbbb" {
		t.Errorf("header %q (%d bytes)", got, len(got))
	}
	if len(tight.Header("Cache-Tag")) > 21 {
		t.Errorf("the header is %d bytes over budget", len(tight.Header("Cache-Tag"))-21)
	}
	if got := strings.Join(tight.Truncated, ","); got != "eeee,dddd,cccc" {
		t.Errorf("truncated %q", got)
	}
	if !strings.Contains(tight.String(), "dropped to fit") {
		t.Errorf("the report does not say so:\n%s", tight)
	}

	// A budget too small for even one tag leaves none, and says so rather than emitting a
	// header that will be rejected.
	none, err := Collect(strings.NewReader(doc), "data-cache-tag", "cache-tag", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Names) != 0 || none.Header("Cache-Tag") != "" {
		t.Errorf("%v gave %q", none.Names, none.Header("Cache-Tag"))
	}
	if len(none.Truncated) != 5 {
		t.Errorf("%d truncated, want 5", len(none.Truncated))
	}
}

// TestTheCollectPassWritesNothing, which is what makes it the cheap half of two passes: no output
// means nothing held and nothing copied.
func TestTheCollectPassWritesNothing(t *testing.T) {
	// Collect writes to io.Discard by construction, so what is asserted here is the
	// consequence: it can be run over a document of any size without the caller providing
	// anywhere to put it.
	doc := strings.Repeat(`<div data-cache-tag="t"><p>text</p></div>`, 2000)
	tags := collect(t, doc)
	if len(tags.Names) != 1 || tags.Elements != 2000 {
		t.Errorf("%v over %d elements", tags.Names, tags.Elements)
	}
}

// TestTheStreamPassIsByteIdentical, since the whole point of registering no handlers is that the
// document goes through untouched - and that is what makes it cost eight per cent of a pass with
// handlers.
func TestTheStreamPassIsByteIdentical(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<div data-cache-tag="a"><ul><li>x<li>y</ul></div>`,
		`<p>x</p><script>var a = 1 < 2;</script><style>.a > .b{}</style>`,
		`<!-- c --><table><tr><td>x</table>`,
		``,
		`just text`,
		`<p attr="unfinished`,
	} {
		var out strings.Builder
		if err := Stream(strings.NewReader(doc), &out); err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if out.String() != doc {
			t.Errorf("the stream pass changed the document:\n  in:  %q\n  out: %q",
				doc, out.String())
		}
	}
}

// TestADocumentWithNoTagsProducesNoHeader, since an empty header is worse than none - it caches
// everything under one key.
func TestADocumentWithNoTagsProducesNoHeader(t *testing.T) {
	for _, doc := range []string{
		`<p>x</p>`,
		`<div data-other="a">x</div>`,
		`<meta name="description" content="a">`,
		`<div data-cache-tag="">x</div>`,
		`<div data-cache-tag="   ">x</div>`,
		``,
	} {
		tags := collect(t, doc)
		if h := tags.Header("Cache-Tag"); h != "" {
			t.Errorf("%q produced %q", doc, h)
		}
		if len(tags.Names) != 0 {
			t.Errorf("%q produced %v", doc, tags.Names)
		}
	}
}

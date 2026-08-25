package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func redact(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var b strings.Builder
	res, err := Redact(&b, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Redact(%q): %v", doc, err)
	}
	return b.String(), res
}

func TestRedactingText(t *testing.T) {
	tests := []struct {
		name           string
		doc            string
		want           string
		emails, phones int
	}{
		{"an email", `<p>a@b.com</p>`, `<p>[email removed]</p>`, 1, 0},
		{"two emails", `<p>a@b.com and c@d.org</p>`,
			`<p>[email removed] and [email removed]</p>`, 2, 0},
		{"a phone number", `<p>+44 20 7946 0958</p>`, `<p>[phone removed]</p>`, 0, 1},
		{"both", `<p>a@b.com or 020 7946 0958</p>`,
			`<p>[email removed] or [phone removed]</p>`, 1, 1},
		{"nothing to remove", `<p>ordinary text</p>`, `<p>ordinary text</p>`, 0, 0},
		{"a short number is not a phone", `<p>call 123</p>`, `<p>call 123</p>`, 0, 0},
		{"an address written with references", `<p>a&#64;b.com</p>`,
			`<p>[email removed]</p>`, 1, 0},
		{"text either side is kept", `<p>Mail a@b.com today</p>`,
			`<p>Mail [email removed] today</p>`, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, res := redact(t, tt.doc)
			if out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
			if res.TextEmails != tt.emails || res.TextPhones != tt.phones {
				t.Errorf("counted %d emails and %d phones, want %d and %d",
					res.TextEmails, res.TextPhones, tt.emails, tt.phones)
			}
		})
	}
}

func TestRedactingAttributes(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"title", `<a title="a@b.com">x</a>`, `<a title="[email removed]">x</a>`},
		{"mailto", `<a href="mailto:a@b.com">x</a>`, `<a href="mailto:[email removed]">x</a>`},
		{"data attribute", `<p data-x="a@b.com">y</p>`, `<p data-x="[email removed]">y</p>`},
		{"several attributes", `<a href="mailto:a@b.com" title="a@b.com">x</a>`,
			`<a href="mailto:[email removed]" title="[email removed]">x</a>`},
		{"nothing to remove", `<a href="/x" title="y">z</a>`, `<a href="/x" title="y">z</a>`},
		{"a phone in an attribute", `<a title="+44 20 7946 0958">x</a>`,
			`<a title="[phone removed]">x</a>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, _ := redact(t, tt.doc); out != tt.want {
				t.Errorf("got %q, want %q", out, tt.want)
			}
		})
	}
}

// A duplicated attribute cannot be sanitised by writing over it, because
// SetAttribute writes the first copy and leaves the rest. This is the test that
// says the program does the removal properly, and the second half measures the
// version that does not.
func TestADuplicatedAttributeLeavesNoCopyBehind(t *testing.T) {
	const doc = `<a href="mailto:a@b.com" href="mailto:a@b.com">x</a>`

	out, res := redact(t, doc)
	if strings.Contains(out, "a@b.com") {
		t.Errorf("the address survived: %q", out)
	}
	if n := strings.Count(out, "href"); n != 1 {
		t.Errorf("%d copies of href in %q, want 1", n, out)
	}
	if res.Duplicated != 1 {
		t.Errorf("Duplicated = %d, want 1", res.Duplicated)
	}

	// The version that only sets: the first copy is sanitised and the second is
	// the address, still there.
	naive, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		v, ok := e.Attribute("href")
		if !ok {
			return nil
		}
		clean, _, _ := scrub(v)
		return e.SetAttribute("href", clean)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(naive, "a@b.com") {
		t.Fatalf("the naive version removed it after all, so this test is stale: %q", naive)
	}
}

// Attribute values are source in and source out, so a reference in one is not
// decoded and re-encoded - which would double the escaping on a second pass.
func TestAttributeValuesStaySource(t *testing.T) {
	const doc = `<a href="/x?a=1&amp;b=2" title="a@b.com">t</a>`
	out, _ := redact(t, doc)
	if !strings.Contains(out, `href="/x?a=1&amp;b=2"`) {
		t.Errorf("the href was altered: %q", out)
	}
	if strings.Contains(out, "&amp;amp;") {
		t.Errorf("a reference was escaped twice: %q", out)
	}
}

func TestSkippedElements(t *testing.T) {
	for _, doc := range []string{
		`<script>var e = "a@b.com"</script>`,
		`<style>p{content:"a@b.com"}</style>`,
		`<template><p>a@b.com</p></template>`,
		`<xmp>a@b.com</xmp>`,
	} {
		out, res := redact(t, doc)
		if out != doc {
			t.Errorf("%q changed to %q", doc, out)
		}
		if res.Total() != 0 {
			t.Errorf("%q: %d removals", doc, res.Total())
		}
	}
}

// Running it again must change nothing: the masks contain no address and no
// number, which is the property that makes them usable as masks.
func TestRedactingTwiceChangesNothing(t *testing.T) {
	const doc = `<p>a@b.com and 020 7946 0958</p><a href="mailto:a@b.com" title="a@b.com">x</a>`
	once, res1 := redact(t, doc)
	if res1.Total() == 0 {
		t.Fatal("the first pass removed nothing")
	}
	twice, res2 := redact(t, once)
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %q\n twice %q", once, twice)
	}
	if res2.Total() != 0 {
		t.Errorf("the second pass removed %d more", res2.Total())
	}
}

// The output must not depend on how the input was written, including when an
// address is split across chunks.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<p>Mail a@b.com or call +44 20 7946 0958.</p>`,
		`<a href="mailto:a@b.com" title="a@b.com" href="mailto:a@b.com">c</a>`,
		`<p>a&#64;b.com</p>`,
		`<script>var e="a@b.com"</script><p>a@b.com</p>`,
		`<p>nothing to remove here</p>`,
	}
	for _, doc := range docs {
		want, wantRes := redact(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			var b strings.Builder
			res, err := Redact(&b, &chunked{s: doc, n: n})
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if b.String() != want || res.Total() != wantRes.Total() {
				t.Fatalf("%q at writes of %d:\n got %q (%d)\nwant %q (%d)",
					doc, n, b.String(), res.Total(), want, wantRes.Total())
			}
		}
	}
}

// Nothing this program writes can become markup, because every text insertion is
// Text and every attribute is written through SetAttribute.
func TestTheTagsNeverChange(t *testing.T) {
	docs := []string{
		`<p>a@b.com</p>`,
		`<a href="mailto:a@b.com">x</a>`,
		`<p>&lt;script&gt;a@b.com&lt;/script&gt;</p>`,
		`<a title="&quot; onload=alert(1) x=&quot;a@b.com">x</a>`,
	}
	for _, doc := range docs {
		out, _ := redact(t, doc)
		if before, after := tagSequence(t, doc), tagSequence(t, out); before != after {
			t.Errorf("%q changed the tags from %s to %s: %q", doc, before, after, out)
		}
	}
}

func tagSequence(t *testing.T, doc string) string {
	t.Helper()
	var b strings.Builder
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		b.WriteString("<" + e.TagName() + ">")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

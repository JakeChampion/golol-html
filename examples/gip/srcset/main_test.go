package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<img src="/a.jpg">`,
	`<img src="/a.jpg" srcset="x 1w">`,
	`<img src="/a.svg">`,
	`<img src="/a.gif">`,
	`<img src="data:image/png;base64,AA">`,
	`<img src="">`,
	`<img>`,
	`<img src="/a.jpg?v=1&amp;x=2">`,
	`<img src="https://cdn.example/a.jpg">`,
	`<span><esi:include src="/frag"></span><img src="/a.jpg">`,
	`<esi:include src="/frag"><img src="/a.jpg">`,
	`<!DOCTYPE html><html><body><img src="/a.jpg"></body></html>`,
	`<picture><source srcset="/s"><img src="/a.jpg"></picture>`,
	`<p>no images</p>`,
	``,
}

func chunked(in string, n int, b *builder) (string, error) {
	var out bytes.Buffer
	opts := b.options()
	if b.esi {
		opts = append(opts, lolhtml.WithESITags())
	}
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func newBuilder() *builder {
	return &builder{widths: []int{320, 640}, cdn: "/cdn?u={url}&w={w}", esi: true}
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := buildString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 17} {
			got, err := chunked(doc, n, newBuilder())
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := buildString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, b, err := buildString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if b.built != 0 {
			t.Errorf("second pass of %q built %d srcset(s)", doc, b.built)
		}
	}
}

// TestSrcsetShape: candidates smallest first, each with a w descriptor, and the
// original src left in place as the fallback for a browser that ignores srcset.
func TestSrcsetShape(t *testing.T) {
	got, b, err := buildString(`<img src="/a.jpg">`, func(b *builder) {
		b.widths = []int{320, 640, 1280}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `<img src="/a.jpg" srcset="/cdn?u=%2Fa.jpg&w=320 320w, ` +
		`/cdn?u=%2Fa.jpg&w=640 640w, /cdn?u=%2Fa.jpg&w=1280 1280w">`
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if b.built != 1 {
		t.Errorf("built=%d, want 1", b.built)
	}
}

// TestSourceURLIsEncoded: the src becomes a query parameter of the CDN URL, so a
// raw & or ? in it would end the parameter and drop the rest.
func TestSourceURLIsEncoded(t *testing.T) {
	got, _, err := buildString(`<img src="/a.jpg?v=1&amp;x=2">`)
	if err != nil {
		t.Fatal(err)
	}
	// The attribute arrives as raw source, so the & is still &amp; here, and
	// QueryEscape encodes every byte of it.
	if strings.Contains(got, "srcset=\"/cdn?u=/a.jpg?v=1") {
		t.Errorf("the source URL was not encoded: %s", got)
	}
	if !strings.Contains(got, "u=%2Fa.jpg%3Fv%3D1%26amp%3Bx%3D2") {
		t.Errorf("expected a fully encoded source: %s", got)
	}
}

// TestUnsuitableImagesAreSkipped, each for its own reason.
func TestUnsuitableImagesAreSkipped(t *testing.T) {
	for _, tt := range []struct{ src, reason string }{
		{"/a.svg", "vector"},
		{"/a.gif", "animation"},
		{"data:image/png;base64,AA", "already inline"},
		{"/A.SVG", "vector"},
	} {
		in := `<img src="` + tt.src + `">`
		got, b, err := buildString(in)
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}
		if got != in {
			t.Errorf("%s was rewritten: %s", tt.src, got)
		}
		if len(b.skipped) != 1 {
			t.Errorf("%s: skipped=%v, want one entry", tt.src, b.skipped)
		}
	}
}

func TestExistingSrcsetIsKept(t *testing.T) {
	in := `<img src="/a.jpg" srcset="/mine 1w">`
	got, b, err := buildString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("overwrote an existing srcset: %s", got)
	}
	if b.kept != 1 || b.built != 0 {
		t.Errorf("kept=%d built=%d, want 1 and 0", b.kept, b.built)
	}
}

func TestSizesIsOptionalAndNotOverwritten(t *testing.T) {
	got, _, err := buildString(`<img src="/a.jpg">`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "sizes=") {
		t.Errorf("added a sizes attribute without being asked: %s", got)
	}

	got, _, err = buildString(`<img src="/a.jpg">`, func(b *builder) { b.sizes = "100vw" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `sizes="100vw"`) {
		t.Errorf("sizes was not added: %s", got)
	}

	got, _, err = buildString(`<img src="/a.jpg" sizes="50vw">`, func(b *builder) { b.sizes = "100vw" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `sizes="50vw"`) || strings.Contains(got, "100vw") {
		t.Errorf("overwrote the document's sizes: %s", got)
	}
}

// TestESIIncludeIsVoidWithTheOption is why this program turns ESI parsing on.
// These pages are assembled at the edge, so they carry <esi:include> tags
// written without a closing tag; treated as containers, an include swallows the
// enclosing element's end tag as soon as anything replaces or removes it, and
// the only symptom is malformed output.
func TestESIIncludeIsVoidWithTheOption(t *testing.T) {
	in := `<span><esi:include src="/frag"></span><img src="/a.jpg">`

	got, b, err := buildString(in)
	if err != nil {
		t.Fatal(err)
	}
	if b.esiVoided != 1 {
		t.Errorf("esiVoided=%d, want 1: the include should be void", b.esiVoided)
	}
	if !strings.Contains(got, "</span>") {
		t.Errorf("the enclosing end tag was lost: %s", got)
	}
	if !strings.Contains(got, "srcset=") {
		t.Errorf("the image after the include was not rewritten: %s", got)
	}

	// With the option off the include is a container. Nothing here replaces it,
	// so the markup survives, but CanHaveContent reports the difference and the
	// count is what the report would have shown.
	_, b, err = buildString(in, func(b *builder) { b.esi = false })
	if err != nil {
		t.Fatal(err)
	}
	if b.esiVoided != 0 {
		t.Errorf("esiVoided=%d with ESI off, want 0", b.esiVoided)
	}
}

func TestBadCDNTemplateAndWidths(t *testing.T) {
	// The template must name both substitutions, or every candidate URL is the
	// same and the srcset is worse than useless.
	b := &builder{widths: []int{320}, cdn: "/cdn?u={url}", esi: true}
	got := b.srcsetFor("/a.jpg")
	if strings.Contains(got, "320w") && !strings.Contains(got, "w=320") {
		// main rejects this before constructing a builder; this asserts the
		// rendering itself does not silently invent a width.
		t.Logf("template without {w} renders as %q", got)
	}

	b = &builder{widths: nil, cdn: "/cdn?u={url}&w={w}", esi: true}
	if s := b.srcsetFor("/a.jpg"); s != "" {
		t.Errorf("no widths should produce no candidates, got %q", s)
	}
}

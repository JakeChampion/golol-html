package main

import (
	"bytes"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<img src="/1">`,
	`<img src="/1"><img src="/2"><img src="/3">`,
	`<img src="/1" loading="eager">`,
	`<img src="/1" loading="lazy">`,
	`<img src="/1" decoding="sync">`,
	`<iframe src="/i"></iframe>`,
	`<iframe src="/i" loading="eager"></iframe>`,
	`<picture><source srcset="/a"><img src="/1"></picture>`,
	`<noscript><img src="/1"></noscript>`,
	`<!DOCTYPE html><html><body><img src="/1"><img src="/2"></body></html>`,
	`<p>no images</p>`,
	``,
}

func chunked(in string, n int, l *lazifier) (string, error) {
	var out bytes.Buffer
	opts := append(l.options(), lolhtml.WithStrict(l.strict))
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

// TestChunkInvariance also covers the counter: the eager threshold is decided by
// how many images have been seen, so a handler that fired a different number of
// times would defer a different set.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := lazyString(doc, func(l *lazifier) { l.eager = 2 })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 13} {
			got, err := chunked(doc, n, &lazifier{eager: 2, strict: true})
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
		once, _, err := lazyString(doc, func(l *lazifier) { l.eager = 2 })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, _, err := lazyString(once, func(l *lazifier) { l.eager = 2 })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
	}
}

// TestLeadingImagesStayEager is the whole point of the -eager threshold:
// deferring an above-the-fold image delays the largest contentful paint rather
// than helping it.
func TestLeadingImagesStayEager(t *testing.T) {
	in := `<img src="/1"><img src="/2"><img src="/3"><img src="/4">`
	for eager, wantDeferred := range map[int]int{0: 4, 1: 3, 2: 2, 4: 0, 10: 0} {
		got, l, err := lazyString(in, func(l *lazifier) { l.eager = eager })
		if err != nil {
			t.Fatalf("eager=%d: %v", eager, err)
		}
		if n := strings.Count(got, `loading="lazy"`); n != wantDeferred {
			t.Errorf("eager=%d: deferred %d, want %d (%s)", eager, n, wantDeferred, got)
		}
		if l.deferred != wantDeferred {
			t.Errorf("eager=%d: report says %d, want %d", eager, l.deferred, wantDeferred)
		}
	}
}

// TestExistingLoadingIsLeftAlone: the author of the document knew something this
// program does not, and overriding it would undo a deliberate choice.
func TestExistingLoadingIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<img src="/1"><img src="/2" loading="eager">`,
		`<img src="/1"><img src="/2" loading="lazy">`,
		`<iframe src="/i" loading="eager"></iframe>`,
	} {
		got, _, err := lazyString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if strings.Count(got, "loading=") != strings.Count(in, "loading=") {
			t.Errorf("%s\n got: %s\nthe loading attribute count changed", in, got)
		}
	}
}

// TestDecodingIsAddedToEveryImage, including the eager ones: decoding is
// orthogonal to loading and says only that the decode may happen off the main
// thread.
func TestDecodingIsAddedToEveryImage(t *testing.T) {
	got, _, err := lazyString(`<img src="/1"><img src="/2">`, func(l *lazifier) { l.eager = 1 })
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, `decoding="async"`); n != 2 {
		t.Errorf("decoding added %d times, want 2: %s", n, got)
	}
	// An explicit decoding is kept.
	got, _, err = lazyString(`<img src="/1" decoding="sync">`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "async") {
		t.Errorf("overrode an explicit decoding: %s", got)
	}
}

// TestImagesInNoscriptAreNotSeen records a limitation rather than a bug, because
// it will look like a bug to whoever notices it first. noscript content is
// parsed as raw text when scripting is enabled, so a fallback image inside one is
// invisible to a handler and cannot be deferred.
func TestImagesInNoscriptAreNotSeen(t *testing.T) {
	in := `<noscript><img src="/1"></noscript><img src="/2">`
	got, l, err := lazyString(in, func(l *lazifier) { l.eager = 0 })
	if err != nil {
		t.Fatal(err)
	}
	if l.images != 1 {
		t.Errorf("saw %d images, want 1: the one inside noscript is text", l.images)
	}
	if strings.Count(got, `loading="lazy"`) != 1 {
		t.Errorf("expected only the image outside noscript to be deferred: %s", got)
	}
}

// TestAmbiguousDocumentIsRefusedNotGuessed. Strict mode is on, so a document
// that trips the parsing ambiguity guard fails, and the app has to say the
// output is unusable rather than reporting success over a truncated response.
func TestAmbiguousDocumentIsRefusedNotGuessed(t *testing.T) {
	in := `<img src="/1"><select><xmp></xmp></select><img src="/2">`

	got, l, err := lazyString(in, func(l *lazifier) { l.eager = 0 })
	if err == nil {
		t.Fatal("expected the ambiguous document to be refused")
	}
	if !l.ambiguous {
		t.Error("the ambiguity was not recognised, so the report will not warn about it")
	}
	if !strings.Contains(l.report(), "must not be served") {
		t.Errorf("the report does not say the output is unusable:\n%s", l.report())
	}
	if len(got) >= len(in) {
		t.Errorf("expected a truncated prefix, got %d of %d bytes", len(got), len(in))
	}

	// With strict off the same document succeeds. The closed <xmp> ends the
	// raw-text region, so both images are still seen here.
	_, l, err = lazyString(in, func(l *lazifier) { l.eager = 0; l.strict = false })
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if l.ambiguous {
		t.Error("lenient mode should not report an ambiguity")
	}

	// Leave the ambiguous tag unclosed - which a document malformed enough to
	// trip the guard often does - and everything after it is text. Strict mode
	// refuses; lenient mode reports success over a document it never looked at.
	unclosed := `<img src="/1"><select><xmp><img src="/2"><img src="/3">`

	if _, l, err = lazyString(unclosed, func(l *lazifier) { l.eager = 0 }); err == nil {
		t.Error("strict mode accepted the unclosed document")
	}

	_, l, err = lazyString(unclosed, func(l *lazifier) { l.eager = 0; l.strict = false })
	if err != nil {
		t.Fatalf("lenient mode should not fail: %v", err)
	}
	if l.images != 1 {
		t.Errorf("lenient mode saw %d images; only the one before the ambiguous tag "+
			"is markup, and the report would claim the rest were considered", l.images)
	}
}

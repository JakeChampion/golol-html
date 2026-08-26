package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func page(n int) string {
	return strings.Repeat(`<div><a href="/x">link</a></div>`, n)
}

// TestTheLimitBoundsTheDecompressedSize, which is the whole reason the limit is where it is: a
// gzipped body is a size claim the sender controls twice.
func TestTheLimitBoundsTheDecompressedSize(t *testing.T) {
	// 8 MB of one byte, which compresses to a few kilobytes.
	bomb := gz(t, strings.Repeat("a", 8<<20))
	if len(bomb) > 100<<10 {
		t.Fatalf("the bomb did not compress: %d bytes", len(bomb))
	}

	var out strings.Builder
	res, err := Rewrite(bytes.NewReader(bomb), &out, 1<<20, annotate())
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	if !res.LimitHit {
		t.Error("the limit was not reported as reached")
	}
	if res.Decompressed <= 1<<20 {
		t.Errorf("decompressed %d bytes, which does not exceed the limit", res.Decompressed)
	}
	if res.Decompressed > (1<<20)+1 {
		t.Errorf("decompressed %d bytes, which is more than one past the limit",
			res.Decompressed)
	}
	if !strings.Contains(res.String(), "REACHED") {
		t.Errorf("report:\n%s", res)
	}

	// The same limit placed before the decompressor would not have caught it, which is the
	// mistake worth demonstrating rather than asserting.
	zr, err := gzip.NewReader(io.LimitReader(bytes.NewReader(bomb), 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, zr)
	if err != nil {
		t.Fatalf("copying: %v", err)
	}
	if n <= 1<<20 {
		t.Errorf("a limit before the decompressor bounded the output at %d bytes, so this "+
			"case does not show the problem", n)
	}
}

// TestReachingTheLimitIsAnErrorRatherThanASilentTruncation, because io.LimitReader ends the stream
// at the limit and that looks exactly like the document ending.
func TestReachingTheLimitIsAnErrorRatherThanASilentTruncation(t *testing.T) {
	doc := page(100)
	compressed := gz(t, doc)

	// A limit under the document's size.
	var short strings.Builder
	res, err := Rewrite(bytes.NewReader(compressed), &short, 100, annotate())
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
	if !res.LimitHit {
		t.Error("LimitHit is false")
	}
	if res.Clean {
		t.Error("a truncated document was reported as clean")
	}

	// A limit exactly the document's size is not reached, because the limit is a maximum
	// rather than a boundary the document must stay under.
	var exact strings.Builder
	res, err = Rewrite(bytes.NewReader(compressed), &exact, int64(len(doc)), annotate())
	if err != nil {
		t.Errorf("a document exactly at the limit: %v", err)
	}
	if res.LimitHit {
		t.Error("a document exactly at the limit was reported as over it")
	}
	if !res.Clean {
		t.Error("a complete document was not reported as clean")
	}

	// And a plain io.LimitReader really is silent, which is what this is working around.
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	var silent strings.Builder
	w, err := lolhtml.NewWriter(&silent, annotate())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(w, io.LimitReader(zr, 100)); err != nil {
		t.Fatalf("copying: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if silent.Len() == 0 || silent.Len() >= len(doc) {
		t.Errorf("the truncation produced %d bytes of %d", silent.Len(), len(doc))
	}
	// No error anywhere, and the output is half a page.
}

// TestATruncatedStreamCanStillDeliverEveryByte, which makes the trailer error about integrity
// rather than completeness - and a caller who ignores it because the output looks whole has
// shipped an unverified document.
func TestATruncatedStreamCanStillDeliverEveryByte(t *testing.T) {
	doc := page(100)
	full := gz(t, doc)

	sawWhole := false
	for _, frac := range []int{10, 30, 50, 70, 90, 99} {
		cut := len(full) * frac / 100
		var out strings.Builder
		res, err := Rewrite(bytes.NewReader(full[:cut]), &out, 0, annotate())
		if err == nil {
			t.Errorf("%d%%: a truncated stream produced no error", frac)
			continue
		}
		if res.Clean {
			t.Errorf("%d%%: reported clean", frac)
		}
		// Whatever was written is a prefix of what a complete stream produces.
		whole, err2 := lolhtml.RewriteString(doc, annotate())
		if err2 != nil {
			t.Fatal(err2)
		}
		if !strings.HasPrefix(whole, out.String()) {
			t.Errorf("%d%%: the output is not a prefix of the whole", frac)
		}
		if out.Len() == len(whole) {
			sawWhole = true
		}
	}
	if !sawWhole {
		t.Error("no truncation point delivered the whole document, so the integrity " +
			"point this documents does not reproduce")
	}
}

// TestACorruptedByteIsReportedAfterTheBytesHaveGone, which is the shape of the problem: the
// checksum is at the end, so the rewrite has already happened when it fails.
func TestACorruptedByteIsReportedAfterTheBytesHaveGone(t *testing.T) {
	doc := page(100)
	full := gz(t, doc)

	// Flip a bit in the deflate data, well past the header.
	corrupt := make([]byte, len(full))
	copy(corrupt, full)
	corrupt[len(corrupt)/2] ^= 0x40

	var out strings.Builder
	res, err := Rewrite(bytes.NewReader(corrupt), &out, 0, annotate())
	if err == nil {
		t.Fatalf("a corrupted stream produced no error; %d bytes written", out.Len())
	}
	if res.Clean {
		t.Error("a corrupted stream was reported as clean")
	}
	// The point: bytes reached the destination before the failure was known.
	if out.Len() == 0 {
		t.Log("nothing was written before the failure, which is the lucky case")
	}
}

// TestACleanStreamRewritesExactly, which is the case that has to keep working.
func TestACleanStreamRewritesExactly(t *testing.T) {
	for _, doc := range []string{
		page(1),
		page(200),
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<p>x</p><script>var a = 1 < 2;</script>`,
		``,
		`just text`,
		`<p attr="unfinished`,
	} {
		var out strings.Builder
		res, err := Rewrite(bytes.NewReader(gz(t, doc)), &out, 1<<20, annotate())
		if err != nil {
			t.Errorf("%.40q: %v", doc, err)
			continue
		}
		if !res.Clean {
			t.Errorf("%.40q: not reported clean", doc)
		}
		want, err := lolhtml.RewriteString(doc, annotate())
		if err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("%.40q: the output differs from rewriting the plain document",
				doc)
		}
		if res.Decompressed != int64(len(doc)) {
			t.Errorf("%.40q: decompressed %d bytes for a %d-byte document",
				doc, res.Decompressed, len(doc))
		}
	}
}

// TestSomethingThatIsNotGzipIsRejectedAtTheHeader, before a rewriter is involved at all.
func TestSomethingThatIsNotGzipIsRejectedAtTheHeader(t *testing.T) {
	for _, body := range []string{`<p>plain html</p>`, ``, "\x1f\x8b", "\x1f\x8b\x08"} {
		var out strings.Builder
		res, err := Rewrite(strings.NewReader(body), &out, 0, annotate())
		if err == nil {
			t.Errorf("%q was accepted as a gzip stream", body)
		}
		if out.Len() != 0 {
			t.Errorf("%q: %d bytes were written", body, out.Len())
		}
		if res.Clean {
			t.Errorf("%q: reported clean", body)
		}
	}
}

// TestASizeIsReadWithItsSuffix, since a limit is the kind of flag people write as 8m.
func TestASizeIsReadWithItsSuffix(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"1024", 1024},
		{"1k", 1 << 10},
		{"1K", 1 << 10},
		{"8m", 8 << 20},
		{"8M", 8 << 20},
		{"2g", 2 << 30},
	} {
		got, err := parseSize(tt.in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseSize(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
	// Sscanf would accept the middle three of these, reading "1x" as 1 and "1.5m" as 1: a
	// limit that quietly means something other than what was typed is worse than a rejected
	// flag.
	for _, bad := range []string{"x", "1x", "-1", "1.5m", "m", "1 2", "0x10", "1_000", " 1"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) was accepted", bad)
		}
	}
}

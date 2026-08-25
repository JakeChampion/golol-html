package lolhtml_test

// What MaxMemory has to cover. Not the document: the largest single token a handler
// is given, and only when that token straddles two writes. A document delivered in
// one Write needs almost nothing however long it is, the same document in 64-byte
// writes needs the length of the biggest tag a handler matches, and a tag nothing
// matches costs nothing either way.
//
// It means a rewrite can raise the memory limit a pipeline needed before it was
// added, without the document having changed - which is the position anything
// touching srcset attributes is in, since those are the longest tags on a page.

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// floorFor is the smallest MaxMemory at which doc completes, fed in chunks of the
// given size (zero for one Write), with the given options. Found by doubling and
// then bisecting, so the answer is exact.
func floorFor(t *testing.T, doc string, chunk int, opts func() []lolhtml.Option) int {
	t.Helper()

	run := func(limit int) bool {
		all := append([]lolhtml.Option{
			lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: limit}),
		}, opts()...)
		w, err := lolhtml.NewWriter(io.Discard, all...)
		if err != nil {
			return false
		}
		step := chunk
		if step <= 0 {
			step = len(doc)
		}
		for i := 0; i < len(doc); i += step {
			if _, err := w.Write([]byte(doc[i:min(i+step, len(doc))])); err != nil {
				w.Close()
				return false
			}
		}
		return w.Close() == nil
	}

	hi := 8
	for hi < 1<<24 && !run(hi) {
		hi *= 2
	}
	lo := hi / 2
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if run(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

func noHandlers() []lolhtml.Option { return nil }

func matchesImg() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement("img", func(*lolhtml.Element) error { return nil })}
}

func longTag(n int) string { return `<img alt="` + strings.Repeat("x", n) + `">` }

// TestOneWriteNeedsAlmostNothing, however long the document and whatever matches.
func TestOneWriteNeedsAlmostNothing(t *testing.T) {
	for _, doc := range []string{longTag(2000), strings.Repeat(`<img alt="ab">`, 200)} {
		for name, opts := range map[string]func() []lolhtml.Option{
			"no handlers": noHandlers, "a handler that matches": matchesImg,
		} {
			if got := floorFor(t, doc, 0, opts); got > 64 {
				t.Errorf("%d-byte document, %s, one Write: floor %d, want under 64",
					len(doc), name, got)
			}
		}
	}
}

// TestAMatchedTokenCostsItsWholeLength, exactly, at every chunk size small enough
// to split it.
func TestAMatchedTokenCostsItsWholeLength(t *testing.T) {
	for _, n := range []int{500, 1000, 2000, 4000} {
		doc := longTag(n)
		for _, chunk := range []int{8, 64, 256} {
			if got := floorFor(t, doc, chunk, matchesImg); got != len(doc) {
				t.Errorf("a %d-byte tag in %d-byte writes: floor %d, want %d",
					len(doc), chunk, got, len(doc))
			}
		}
	}
}

// TestAnUnmatchedTokenCostsTheWriteSize, so the same document under the same writes
// is cheap until a handler asks for it.
func TestAnUnmatchedTokenCostsTheWriteSize(t *testing.T) {
	doc := longTag(2000)
	for _, chunk := range []int{1, 8, 64, 256} {
		got := floorFor(t, doc, chunk, noHandlers)
		if got > chunk+16 {
			t.Errorf("a %d-byte tag nothing matches in %d-byte writes: floor %d, want no more than %d",
				len(doc), chunk, got, chunk+16)
		}
	}
	// A handler for something else in the document is no different.
	other := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("video", func(*lolhtml.Element) error { return nil })}
	}
	if got := floorFor(t, doc, 64, other); got > 80 {
		t.Errorf("a handler for another selector: floor %d, want no more than 80", got)
	}
}

// TestManyShortTagsCostTheWriteSizeRatherThanTheDocument: it is the biggest token,
// not the total.
func TestManyShortTagsCostTheWriteSizeRatherThanTheDocument(t *testing.T) {
	const tag = `<img alt="ab">`
	doc := strings.Repeat(tag, 200)
	for _, chunk := range []int{1, 8, 64, 256} {
		got := floorFor(t, doc, chunk, matchesImg)
		if want := max(chunk+16, len(tag)); got > want {
			t.Errorf("%d bytes of short tags in %d-byte writes: floor %d, want no more than %d",
				len(doc), chunk, got, want)
		}
		if got >= len(doc) {
			t.Errorf("%d bytes of short tags in %d-byte writes: floor %d, want less than the document",
				len(doc), chunk, got)
		}
	}
}

// TestTextIsNotMaterialisedAndACommentIs, which is the difference between a handler
// that costs nothing and one that costs a token: text arrives in chunks and a comment
// arrives whole.
func TestTextIsNotMaterialisedAndACommentIs(t *testing.T) {
	pad := strings.Repeat("x", 2000)

	text := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil })}
	}
	if got := floorFor(t, `<p>`+pad+`</p>`, 64, text); got > 80 {
		t.Errorf("a text handler on a 2000-byte text node: floor %d, want no more than 80", got)
	}

	comment := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return nil })}
	}
	doc := `<!--` + pad + `-->`
	if got := floorFor(t, doc, 64, comment); got != len(doc) {
		t.Errorf("a comment handler on a %d-byte comment: floor %d, want %d", len(doc), got, len(doc))
	}
	if got := floorFor(t, doc, 64, noHandlers); got > 80 {
		t.Errorf("the same comment with no handler: floor %d, want no more than 80", got)
	}
}

// TestWhatTheRewriteWritesDoesNotCount, because the limit is the parsing buffer:
// growing an attribute by 64 times does not raise the floor by a byte.
func TestWhatTheRewriteWritesDoesNotCount(t *testing.T) {
	doc := longTag(2000)
	base := floorFor(t, doc, 64, matchesImg)
	for _, grow := range []int{2, 8, 64} {
		opts := func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("img", func(e *lolhtml.Element) error {
				v, _ := e.Attribute("alt")
				return e.SetAttribute("alt", strings.Repeat(v, grow))
			})}
		}
		if got := floorFor(t, doc, 64, opts); got != base {
			t.Errorf("growing the value %d times: floor %d, want %d", grow, got, base)
		}
	}
}

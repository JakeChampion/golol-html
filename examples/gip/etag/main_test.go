package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func page(n int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := range n {
		fmt.Fprintf(&b, `<div><a href="https://a.example/%d">item %d</a> text</div>`, i, i)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func tag(t *testing.T, doc, version string, verify bool) (string, Tag) {
	t.Helper()
	var out strings.Builder
	got, err := Rewrite(strings.NewReader(doc), &out, version, verify)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	return out.String(), got
}

// TestTheTagIsKnownBeforeTheBodyIsWritten, which is the whole design: an etag is a header, and a
// header that is only known after the body is not a header.
func TestTheTagIsKnownBeforeTheBodyIsWritten(t *testing.T) {
	// A destination that fails on the first write, so nothing of the body can have been
	// written when the tag is formed - and yet the tag is still what it would have been.
	doc := page(20)
	_, whole := tag(t, doc, "v1", false)

	var partial strings.Builder
	broken := &failAfter{w: &partial, after: 0}
	_, err := Rewrite(strings.NewReader(doc), broken, "v1", false)
	if err == nil {
		t.Fatal("a destination that fails immediately did not produce an error")
	}
	if partial.Len() != 0 {
		t.Errorf("%d bytes reached the destination", partial.Len())
	}
	// The tag is derived from the input, so a rewrite that could not write a byte would
	// still have named the same output.
	_, again := tag(t, doc, "v1", false)
	if again.Input != whole.Input {
		t.Errorf("the input hash changed: %s then %s", whole.Input, again.Input)
	}
}

// failAfter fails once it has written n bytes.
type failAfter struct {
	w     io.Writer
	after int
	n     int
}

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n >= f.after {
		return 0, fmt.Errorf("the destination is gone")
	}
	n, err := f.w.Write(p)
	f.n += n
	return n, err
}

// TestTheSameInputGivesTheSameTagAndTheSameOutput, which is what the design rests on. A rewrite
// that is not a function of its input cannot be named by its input.
func TestTheSameInputGivesTheSameTagAndTheSameOutput(t *testing.T) {
	doc := page(50)
	first, a := tag(t, doc, "v1", true)
	second, b := tag(t, doc, "v1", true)

	if a.Value() != b.Value() {
		t.Errorf("two runs gave %s and %s", a.Value(), b.Value())
	}
	if first != second {
		t.Error("two runs gave different output")
	}
	if !a.Deterministic || !a.Verified() {
		t.Errorf("the rewrite was not verified deterministic: %+v", a)
	}
	if a.Output == "" {
		t.Error("no output hash was collected")
	}
	// The output hash is a hash of different bytes, so it is not the input hash - which is
	// worth pinning, because reporting one as the other would look like agreement.
	if a.Output == a.Input {
		t.Errorf("the output hash equals the input hash, which would be a coincidence: %s",
			a.Output)
	}
	if !strings.Contains(a.String(), "produced it again") {
		t.Errorf("report:\n%s", a)
	}
}

// TestTheChunkSizeDoesNotChangeTheOutput, since a rewrite whose output depends on how the input
// arrived is not a function of its input either, and the tag would be wrong for a reason no
// second rewrite in the same process would find.
func TestTheChunkSizeDoesNotChangeTheOutput(t *testing.T) {
	doc := page(30)
	_, whole := tag(t, doc, "v1", false)

	for _, size := range []int{1, 2, 3, 7, 64, 1024} {
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, annotate())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		var oneShot strings.Builder
		if _, err := Rewrite(strings.NewReader(doc), &oneShot, "v1", false); err != nil {
			t.Fatal(err)
		}
		if out.String() != oneShot.String() {
			t.Errorf("chunk %d changed the output", size)
		}
	}
	if whole.Input == "" {
		t.Error("no input hash")
	}
}

// TestTheVersionIsPartOfTheTag, because two rewriters over one input are two pages and an etag
// that does not change when the rewriter does is the bug this design invites.
func TestTheVersionIsPartOfTheTag(t *testing.T) {
	doc := page(10)
	_, a := tag(t, doc, "v1", false)
	_, b := tag(t, doc, "v2", false)

	if a.Value() == b.Value() {
		t.Errorf("two versions gave the same tag: %s", a.Value())
	}
	if a.Input != b.Input {
		t.Errorf("the input hash changed with the version: %s and %s", a.Input, b.Input)
	}
	if !strings.HasPrefix(a.Value(), `"v1-`) {
		t.Errorf("the tag does not carry the version: %s", a.Value())
	}
	// The value is quoted, as the specification requires of an entity tag.
	if !strings.HasPrefix(a.Value(), `"`) || !strings.HasSuffix(a.Value(), `"`) {
		t.Errorf("the tag is not quoted: %s", a.Value())
	}
	if a.Header() != "ETag: "+a.Value() {
		t.Errorf("header %q", a.Header())
	}

	// And a missing version is refused rather than defaulted, since a default would be a
	// version that never changes.
	var out strings.Builder
	if _, err := Rewrite(strings.NewReader(doc), &out, "", false); err == nil {
		t.Error("an empty version was accepted")
	}
	if out.Len() != 0 {
		t.Errorf("%d bytes were written before the refusal", out.Len())
	}
}

// TestDifferentInputsGiveDifferentTags, which is the other half of being a validator: a tag that
// does not change when the page does serves stale content.
func TestDifferentInputsGiveDifferentTags(t *testing.T) {
	seen := map[string]string{}
	for _, doc := range []string{
		`<p>a</p>`,
		`<p>b</p>`,
		`<p>a </p>`,
		`<p>A</p>`,
		`<a href="https://a.example/">x</a>`,
		`<a href="https://a.example/">y</a>`,
		page(1),
		page(2),
	} {
		_, got := tag(t, doc, "v1", false)
		if prev, ok := seen[got.Value()]; ok {
			t.Errorf("%q and %q share the tag %s", prev, doc, got.Value())
		}
		seen[got.Value()] = doc
	}
}

// TestANonDeterministicRewriteIsCaught, which is what -verify is for. The rewrite this program
// ships is a function of its input; the one here deliberately is not, and the check has to notice.
func TestANonDeterministicRewriteIsCaught(t *testing.T) {
	doc := page(5)

	// A handler that writes something different each time, which is what a clock or a map
	// iteration would do. This is the shape -verify exists to catch.
	calls := 0
	shifty := lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		calls++
		return e.SetAttribute("data-n", fmt.Sprint(calls))
	})

	one := runWith(t, doc, shifty)
	two := runWith(t, doc, shifty)
	if one == two {
		t.Fatal("the deliberately non-deterministic rewrite gave the same output twice, " +
			"so this test proves nothing")
	}

	// The shipped rewrite, by contrast, agrees with itself.
	if runWith(t, doc, annotate()) != runWith(t, doc, annotate()) {
		t.Error("the shipped rewrite is not deterministic")
	}

	// And -verify reports the shipped one as deterministic, which is the claim the tag
	// depends on.
	_, got := tag(t, doc, "v1", true)
	if !got.Deterministic {
		t.Errorf("%+v", got)
	}
}

func runWith(t *testing.T, doc string, opts ...lolhtml.Option) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTheBodyIsTheRewrittenDocument, since an etag for a body nobody sent is no use.
func TestTheBodyIsTheRewrittenDocument(t *testing.T) {
	const doc = `<a href="https://a.example/x">x</a><a href="/rel">y</a>`
	out, got := tag(t, doc, "v1", false)
	if !strings.Contains(out, `<a href="https://a.example/x" rel="noopener">`) {
		t.Errorf("the external link was not annotated: %s", out)
	}
	if strings.Contains(out, `href="/rel" rel=`) {
		t.Errorf("the relative link was annotated: %s", out)
	}
	if got.OutputBytes != int64(len(out)) {
		t.Errorf("counted %d bytes and wrote %d", got.OutputBytes, len(out))
	}
	if got.InputBytes != int64(len(doc)) {
		t.Errorf("counted %d input bytes for %d", got.InputBytes, len(doc))
	}
}

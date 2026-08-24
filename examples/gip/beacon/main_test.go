package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<html><head><title>T</title></head><body><p>x</p></body></html>`,
	`<html><body><p>Hello &amp; welcome</p></body></html>`,
	`<html><body></body></html>`,
	`<html><body><p>x</p>`,
	`<html><body><script>var a = 1`,
	`<html><body><!-- unterminated`,
	`<html><body><img data-beacon=""></body></html>`,
	`<html><body><p>a</p></body><body><p>b</p></body></html>`,
	`<body/><p>x</p>`,
	`<p>a fragment</p>`,
	`<html><body><textarea><p>x</p></textarea></body></html>`,
	`<html><body><table><tr><td>x</table></body></html>`,
	``,
}

func withPath(b *beacon) { b.path = "/a b&c" }
func atEnd(b *beacon)    { b.atEnd = true }

func chunked(in string, n int, opts ...func(*beacon)) (string, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	if err := b.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, b.options()...)
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

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		for _, opt := range []func(*beacon){withPath, atEnd} {
			whole, _, err := injectString(doc, opt)
			if err != nil {
				t.Fatalf("%q: %v", doc, err)
			}
			for _, n := range []int{1, 2, 3, 29} {
				got, err := chunked(doc, n, opt)
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
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := injectString(doc, withPath)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, b, err := injectString(once, withPath)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if b.injected != 0 {
			t.Errorf("the second pass of %q injected %d", doc, b.injected)
		}
	}
}

// TestNothingElseChanged is the claim in the program's name, checked over the
// whole corpus by stripping the beacon back out and comparing byte for byte.
func TestNothingElseChanged(t *testing.T) {
	for _, doc := range corpus {
		if strings.Contains(doc, "data-beacon") {
			// verify strips every marked element, including one the input
			// already had, so it can only compare an input with none. Stated on
			// verify itself.
			continue
		}
		out, b, err := injectString(doc, withPath)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if err := verify(b.marker, doc, out); err != nil {
			t.Errorf("input %q\n%v", doc, err)
		}
	}
}

// TestAppendingAtTheEndOfATruncatedDocumentIsNotMarkup is the finding this
// program exists around. DocumentEnd.Append adds to the end of the output, not
// to the end of the tree, so a document cut off inside a script or a comment
// swallows the beacon - with no error from anything.
func TestAppendingAtTheEndOfATruncatedDocumentIsNotMarkup(t *testing.T) {
	for _, in := range []string{
		`<html><body><script>var a = 1`,
		`<html><body><style>p{`,
		`<html><body><textarea>x`,
		`<html><body><title>x`,
		`<html><body><!-- unterminated`,
	} {
		out, b, err := injectString(in, atEnd)
		if err != nil {
			t.Fatal(err)
		}
		if b.injected != 1 {
			t.Fatalf("%q: injected=%d, want 1", in, b.injected)
		}

		// The parser is the judge of whether an element is an element.
		var found int
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnElement("img[data-beacon]", func(*lolhtml.Element) error {
				found++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if found != 0 {
			t.Errorf("%q: the appended beacon parsed as an element after all; the "+
				"behaviour this test pins has changed: %s", in, out)
		}

		// And this is what makes it findable rather than silent.
		if err := verify(b.marker, in, out); err == nil {
			t.Errorf("%q: verify did not notice that the beacon is not an element", in)
		}
	}
}

// TestWithoutABodyEndTagNothingIsInjected: the default refuses rather than
// emitting something that may not be markup, and says why.
func TestWithoutABodyEndTagNothingIsInjected(t *testing.T) {
	for _, in := range []string{
		`<html><body><p>x</p>`,
		`<html><body><script>var a = 1`,
		`<p>a fragment</p>`,
		`<body/><p>x</p>`,
	} {
		out, b, err := injectString(in)
		if err != nil {
			t.Fatal(err)
		}
		if b.injected != 0 {
			t.Errorf("%q: injected=%d with no </body>", in, b.injected)
		}
		if out != in {
			t.Errorf("%q: the document changed: %s", in, out)
		}
		if total(b.skipped) != 1 {
			t.Errorf("%q: skipped=%v, want one reason", in, b.skipped)
		}
	}
}

// TestTheBodyEndTagIsThePreferredPlace, because it is the one position that is
// inside the tree rather than at the end of a byte stream.
func TestTheBodyEndTagIsThePreferredPlace(t *testing.T) {
	out, b, err := injectString(`<html><body><p>x</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.injected != 1 {
		t.Fatalf("injected=%d, want 1", b.injected)
	}
	i, j := strings.Index(out, "data-beacon"), strings.Index(out, "</body>")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the beacon is not inside the body: %s", out)
	}
}

// TestTwoBodiesGetOneBeacon: a rewriter has no tree, so both end tags reach a
// handler.
func TestTwoBodiesGetOneBeacon(t *testing.T) {
	out, b, err := injectString(`<html><body><p>a</p></body><body><p>b</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.injected != 1 {
		t.Errorf("injected=%d, want 1", b.injected)
	}
	if n := strings.Count(out, "data-beacon"); n != 1 {
		t.Errorf("%d beacons: %s", n, out)
	}
}

// TestThePathIsEncodedTwiceOverForTwoDifferentReasons: percent-encoding keeps an
// "&" from starting a second query parameter, and HTML escaping keeps it from
// being read as a character reference in the attribute. Both are needed and they
// are not the same job.
func TestThePathIsEncodedTwiceOverForTwoDifferentReasons(t *testing.T) {
	out, _, err := injectString(`<html><body>x</body></html>`, func(b *beacon) {
		b.path = `/a b&c="d"`
	})
	if err != nil {
		t.Fatal(err)
	}

	var src string
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("[data-beacon]", func(e *lolhtml.Element) error {
			src, _ = e.Attribute("src")
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	// One query parameter, whose value round-trips to the original path.
	if want := `/b?p=%2Fa+b%26c%3D%22d%22`; src != want {
		t.Errorf("src is %q, want %q", src, want)
	}
}

// TestAnEndpointWithAQueryGetsAnAmpersand rather than a second question mark.
func TestAnEndpointWithAQueryGetsAnAmpersand(t *testing.T) {
	out, _, err := injectString(`<html><body>x</body></html>`, func(b *beacon) {
		b.endpoint = "/b?site=1"
		b.path = "/x"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `src="/b?site=1&amp;p=%2Fx"`) {
		t.Errorf("the parameter was not appended correctly: %s", out)
	}
}

// TestAConfigurationThatCannotWorkIsRefused.
func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*beacon)
	}{
		{"empty endpoint", func(b *beacon) { b.endpoint = "" }},
		{"empty marker", func(b *beacon) { b.marker = "" }},
		{"marker with a quote", func(b *beacon) { b.marker = `x" onload="` }},
		{"marker starting with a digit", func(b *beacon) { b.marker = "1x" }},
		{"marker with a space", func(b *beacon) { b.marker = "a b" }},
		{"upper-case marker", func(b *beacon) { b.marker = "dataBeacon" }},
	} {
		if _, _, err := injectString(`<html><body>x</body></html>`, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestVerifyReportsWhereItDiffers, because "the documents differ" is not
// something anyone can act on.
func TestVerifyReportsWhereItDiffers(t *testing.T) {
	err := verify("data-beacon", "<p>abcdef</p>", "<p>abcXef</p>")
	if err == nil {
		t.Fatal("no error for documents that differ")
	}
	if !strings.Contains(err.Error(), "byte 6") {
		t.Errorf("the error does not say where: %v", err)
	}
	if !strings.Contains(err.Error(), "<HERE>") {
		t.Errorf("the error does not show the position: %v", err)
	}
}

func TestValidAttrName(t *testing.T) {
	for _, good := range []string{"data-beacon", "x", "a-b-c", "d1", "data-x-1"} {
		if !validAttrName(good) {
			t.Errorf("validAttrName(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "A", "1a", "-a", "a b", `a"b`, "a=b", "a_b", "a.b", "a>b"} {
		if validAttrName(bad) {
			t.Errorf("validAttrName(%q) = true", bad)
		}
	}
}

// TestAppendingIntoAnUnterminatedTagMergesTheBeaconIntoIt, which is worse than
// the beacon being swallowed: the attributes become the truncated element's own.
// A document cut off inside a start tag turns
//
//	<p title="unterminated
//
// plus an appended <img ... data-beacon="" ...> into a single <p> carrying
// data-beacon, so a marker search finds the marker on the wrong element.
func TestAppendingIntoAnUnterminatedTagMergesTheBeaconIntoIt(t *testing.T) {
	const in = `<html><body><p title="unterminated`
	out, b, err := injectString(in, atEnd)
	if err != nil {
		t.Fatal(err)
	}
	if b.injected != 1 {
		t.Fatalf("injected=%d, want 1", b.injected)
	}

	var tags []string
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("[data-beacon]", func(e *lolhtml.Element) error {
			tags = append(tags, e.TagName())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "p" {
		t.Errorf("the marker landed on %v, want it merged into the p: %s", tags, out)
	}
	// And no img was produced, because the img never closed its own tag.
	var imgs int
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("img", func(*lolhtml.Element) error {
			imgs++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if imgs != 0 {
		t.Errorf("%d img elements, want none: %s", imgs, out)
	}
}

// TestIdempotenceIsLostWhenTheInsertionIsNotMarkup: with -at-end on a truncated
// document, the beacon is text inside a comment, so a second pass cannot see it
// and appends another. This is not a defect in the recognition, it is what
// inserting something that is not markup costs, and it is the reason the default
// refuses.
func TestIdempotenceIsLostWhenTheInsertionIsNotMarkup(t *testing.T) {
	const in = `<html><body><!-- unterminated`
	once, _, err := injectString(in, atEnd)
	if err != nil {
		t.Fatal(err)
	}
	twice, b, err := injectString(once, atEnd)
	if err != nil {
		t.Fatal(err)
	}
	if b.injected != 1 {
		t.Errorf("the second pass injected %d, want 1 - the first beacon should be "+
			"invisible to it", b.injected)
	}
	if strings.Count(twice, "data-beacon") != 2 {
		t.Errorf("expected two beacons after two passes: %s", twice)
	}
	// The default does not have this problem, because it inserts nothing here.
	if _, b, err := injectString(in); err != nil {
		t.Fatal(err)
	} else if b.injected != 0 {
		t.Errorf("the default injected %d into a truncated document", b.injected)
	}
}

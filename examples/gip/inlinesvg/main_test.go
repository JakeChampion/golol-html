package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// files is the resolver used throughout: a fixed set, so a test says what the
// resolver returned rather than what a directory happened to hold.
var files = map[string]string{
	"/i/save.svg":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect id="r" fill="url(#g)"/><use href="#r"/></svg>`,
	"/i/plain.svg": `<svg><circle r="2"/></svg>`,
	"/i/script.svg": `<svg><script>alert(1)</script><style>.a{}</style>` +
		`<circle onload="x()" onclick="y()" r="2"/></svg>`,
	"/i/escape.svg": `<svg><rect/><p>oops</p><circle/></svg>`,
	"/i/font.svg":   `<svg><font color="red">a</font><circle/></svg>`,
	"/i/tame.svg":   `<svg><font class="x">a</font><circle/></svg>`,
	"/i/remote.svg": `<svg><use href="https://other/i.svg#a"/><rect id="r"/></svg>`,
	"/i/big.svg":    `<svg>` + strings.Repeat(`<rect/>`, 1000) + `</svg>`,
}

func resolver() Resolver {
	return func(src string) ([]byte, error) {
		f, ok := files[strip(src)]
		if !ok {
			return nil, errors.New("not found")
		}
		return []byte(f), nil
	}
}

func std() Options { return Options{Resolve: resolver(), Max: 4096, Prefix: "i"} }

func inline(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Inline(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Inline(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheFileTakesTheImagesPlace, with the accessible name on the file's own svg tag.
func TestTheFileTakesTheImagesPlace(t *testing.T) {
	got, res := inline(t, `<img src="/i/plain.svg" alt="Circle">`, std())
	// An img with no src never reaches this program, since the selector asks for one.
	if out, r := inline(t, `<img alt="no src">`, std()); out != `<img alt="no src">` || r.Inlined+r.Skipped != 0 {
		t.Errorf("got %q, %v", out, r)
	}
	if want := `<svg role="img" aria-label="Circle"><circle r="2"/></svg>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Inlined != 1 || !res.OK() {
		t.Errorf("%v", res)
	}
	// No name means decoration, and decoration should not be announced.
	got, _ = inline(t, `<img src="/i/plain.svg" alt="">`, std())
	if !strings.HasPrefix(got, `<svg aria-hidden="true">`) {
		t.Errorf("got %q", got)
	}
	got, _ = inline(t, `<img src="/i/plain.svg">`, std())
	if !strings.HasPrefix(got, `<svg aria-hidden="true">`) {
		t.Errorf("got %q", got)
	}
	// The name is an attribute value and stays one: only the quote is escaped.
	got, _ = inline(t, `<img src="/i/plain.svg" alt='a"b &amp; <c>'>`, std())
	if !strings.Contains(got, `aria-label="a&quot;b &amp; <c>"`) {
		t.Errorf("got %q", got)
	}
}

// TestAnImageThatIsNotInlinedIsLeftAlone, and counted under the reason.
func TestAnImageThatIsNotInlinedIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		doc    string
		reason func(Result) int
	}{
		{`<img src="photo.png" alt="p">`, func(r Result) int { return r.Skipped }},
		{`<img src="/i/missing.svg">`, func(r Result) int { return r.Unresolved }},
		{`<img src="/i/big.svg">`, func(r Result) int { return r.TooBig }},
		{`<img src="/i/escape.svg">`, func(r Result) int { return r.Escaping }},
	} {
		got, res := inline(t, tc.doc, std())
		if got != tc.doc {
			t.Errorf("%q was rewritten to %q", tc.doc, got)
		}
		if tc.reason(res) != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestAFileThatWouldEndTheSvgIsRefused, and the tag name is in the report, because
// "it did not work" is not something a caller can act on.
func TestAFileThatWouldEndTheSvgIsRefused(t *testing.T) {
	_, res := inline(t, `<img src="/i/escape.svg">`, std())
	if res.Escaping != 1 || len(res.EscapingTags) != 1 || res.EscapingTags[0] != "p" {
		t.Errorf("%v", res)
	}
	if res.OK() {
		t.Error("OK() is true with an escaping file")
	}
	// A font is conditional: it ends the svg only with a color, face or size.
	_, res = inline(t, `<img src="/i/font.svg">`, std())
	if res.Escaping != 1 || res.EscapingTags[0] != "font" {
		t.Errorf("a coloured font was not refused: %v", res)
	}
	got, res := inline(t, `<img src="/i/tame.svg">`, std())
	if res.Inlined != 1 {
		t.Errorf("a font with only a class was refused: %v", res)
	}
	if !strings.Contains(got, "circle") {
		t.Errorf("got %q", got)
	}
	// Every name in the list is refused, over one file each.
	opts := std()
	for tag := range BreaksOutOfSVG {
		src := "/i/gen-" + tag + ".svg"
		files[src] = `<svg><rect/><` + tag + `>x</` + tag + `><circle/></svg>`
		_, res := inline(t, `<img src="`+src+`">`, opts)
		if res.Escaping != 1 {
			t.Errorf("<%s> was not refused: %v", tag, res)
		}
		delete(files, src)
	}
}

// TestScriptAndHandlersAreDropped, because inlining is a privilege change: the same
// bytes behind an <img> could not run.
func TestScriptAndHandlersAreDropped(t *testing.T) {
	got, res := inline(t, `<img src="/i/script.svg">`, std())
	for _, gone := range []string{"script", "style", "onload", "onclick", "alert"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived: %q", gone, got)
		}
	}
	if res.Scripts != 2 || res.Handlers != 2 {
		t.Errorf("%v", res)
	}
	// What the image was for is still there.
	if !strings.Contains(got, `<circle r="2"`) {
		t.Errorf("got %q", got)
	}
}

// TestIdsArePrefixedPerInline, so that two copies of one file do not collide.
func TestIdsArePrefixedPerInline(t *testing.T) {
	got, res := inline(t, `<img src="/i/save.svg"><img src="/i/save.svg">`, std())
	for _, want := range []string{`id="i0-r"`, `href="#i0-r"`, `url(#i0-g)`, `id="i1-r"`, `href="#i1-r"`, `url(#i1-g)`} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
	if strings.Contains(got, `id="r"`) || strings.Contains(got, `url(#g)`) {
		t.Errorf("an id was left unprefixed: %q", got)
	}
	// Three per file: the id, the url(#…) in the fill, and the use's href.
	if res.Renamed != 6 {
		t.Errorf("Renamed = %d, want 6", res.Renamed)
	}
	// The ids read back as attributes rather than as text that looks right.
	var ids []string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("[id]", func(e *lolhtml.Element) error {
		v, _ := e.Attribute("id")
		ids = append(ids, v)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, " ") != "i0-r i1-r" {
		t.Errorf("ids are %v", ids)
	}
}

// TestAReferenceOutOfTheFileIsDropped: an href to another document is a request the
// <img> would not have made.
func TestAReferenceOutOfTheFileIsDropped(t *testing.T) {
	got, res := inline(t, `<img src="/i/remote.svg">`, std())
	if strings.Contains(got, "https://other") {
		t.Errorf("the remote reference survived: %q", got)
	}
	if res.Opaque != 1 {
		t.Errorf("%v", res)
	}
	// The local one in the same file is renamed as usual.
	if !strings.Contains(got, `id="i0-r"`) {
		t.Errorf("got %q", got)
	}
}

// TestTheSizeLimitIsBytesOfFile.
func TestTheSizeLimitIsBytesOfFile(t *testing.T) {
	opts := std()
	opts.Max = len(files["/i/plain.svg"])
	if _, res := inline(t, `<img src="/i/plain.svg">`, opts); res.Inlined != 1 {
		t.Errorf("a file exactly at the limit was refused: %v", res)
	}
	opts.Max--
	if _, res := inline(t, `<img src="/i/plain.svg">`, opts); res.TooBig != 1 {
		t.Errorf("a file one byte over was inlined: %v", res)
	}
	// Zero means no limit.
	opts.Max = 0
	if _, res := inline(t, `<img src="/i/big.svg">`, opts); res.Inlined != 1 {
		t.Errorf("%v", res)
	}
}

// TestInliningTwiceChangesNothing: the second pass has no img to work on.
func TestInliningTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<img src="/i/save.svg" alt="S">`,
		`<p>a</p><img src="/i/script.svg">`,
		`<img src="/i/escape.svg">`,
		`<img src="photo.png">`,
	} {
		once, _ := inline(t, doc, std())
		twice, res := inline(t, once, std())
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Inlined != 0 && !strings.Contains(doc, "escape") {
			t.Errorf("%q: the second pass inlined %d", doc, res.Inlined)
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<p>a</p><img src="/i/save.svg" alt="S"><img src="photo.png"><img src="/i/script.svg" alt="">`
	want, wantRes := inline(t, doc, std())
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		in := &inliner{opts: std()}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, in.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for k := 0; k < len(doc); k += size {
			if _, err := w.Write([]byte(doc[k:min(k+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if in.res.Inlined != wantRes.Inlined || in.res.Renamed != wantRes.Renamed {
			t.Errorf("chunks of %d: %v, want %v", size, in.res, wantRes)
		}
	}
}

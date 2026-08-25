package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var std = Options{Src: "/captions/{name}.vtt", Lang: "en", Label: "Captions"}

func add(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Add(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Add(%q): %v", doc, err)
	}
	return out.String(), res
}

func track(src string) string {
	return `<track kind="captions" srclang="en" label="Captions" src="` + src + `">`
}

// TestThePlaceholderGoesAtTheEndOfTheVideo, which is where a track element belongs:
// after the source children.
func TestThePlaceholderGoesAtTheEndOfTheVideo(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<video src="/v/talk.mp4"></video>`,
			`<video src="/v/talk.mp4">` + track("/captions/talk.vtt") + `</video>`},
		{`<video src="a.mp4"><source src="b.mp4"></video>`,
			`<video src="a.mp4"><source src="b.mp4">` + track("/captions/a.vtt") + `</video>`},
		// No src of its own: the first source child names it.
		{`<video><source src="/v/two.webm"></video>`,
			`<video><source src="/v/two.webm">` + track("/captions/two.vtt") + `</video>`},
		// An id will do when there is no URL anywhere.
		{`<video id="promo"></video>`, `<video id="promo">` + track("/captions/promo.vtt") + `</video>`},
		// Fallback content stays where it is; the track goes after it, since that is
		// where the video ends.
		{`<video src="a.mp4"><p>no video</p></video>`,
			`<video src="a.mp4"><p>no video</p>` + track("/captions/a.vtt") + `</video>`},
	} {
		got, res := add(t, tc.doc, std)
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if res.Added != 1 || !res.OK() {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestTheNameComesFromTheURLAndNothingElse.
func TestTheNameComesFromTheURLAndNothingElse(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"/v/talk.mp4", "talk"},
		{"talk.mp4", "talk"},
		{"/v/talk.en.mp4", "talk.en"},
		{"https://cdn/v/talk.mp4?t=1", "talk"},
		{"/v/talk.mp4#at=30", "talk"},
		{"/v/talk", "talk"},
		{"/v/dir/", "dir"},
		{"", ""},
		{"/", ""},
		{"?a=1", ""},
	} {
		if got := nameFrom(tc.src); got != tc.want {
			t.Errorf("nameFrom(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestAVideoWithNoNameIsReportedRatherThanGuessed: a track src that resolves to the
// page itself is worse than no track at all.
func TestAVideoWithNoNameIsReportedRatherThanGuessed(t *testing.T) {
	const doc = `<video></video>`
	got, res := add(t, doc, std)
	if got != doc {
		t.Errorf("got %q, want it untouched", got)
	}
	if res.NoName != 1 || res.Added != 0 || res.OK() {
		t.Errorf("%v", res)
	}
	// A pattern that needs no name is used as it stands, for a caller who wants one
	// shared placeholder file.
	got, res = add(t, doc, Options{Src: "/captions/todo.vtt", Lang: "en", Label: "Captions"})
	if got != `<video>`+track("/captions/todo.vtt")+`</video>` {
		t.Errorf("got %q", got)
	}
	if res.Added != 1 || !res.OK() {
		t.Errorf("%v", res)
	}
}

// TestACaptionedVideoIsLeftAlone, and the question is asked with the child
// combinator because that is the only one an implied end tag cannot fool.
func TestACaptionedVideoIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		hadTrack bool
	}{
		{`<video src="a.mp4"><track kind="captions" src="c.vtt"></video>`, true},
		{`<video src="a.mp4"><track kind="subtitles" src="c.vtt"></video>`, true},
		{`<video src="a.mp4"><track kind="CAPTIONS" src="c.vtt"></video>`, true},
		// The attribute's own default is subtitles, so a bare track counts.
		{`<video src="a.mp4"><track src="c.vtt"></video>`, true},
		// These are not captions.
		{`<video src="a.mp4"><track kind="descriptions" src="c.vtt"></video>`, false},
		{`<video src="a.mp4"><track kind="chapters" src="c.vtt"></video>`, false},
		// A track that is not the video's child is not the video's track. In the
		// tree the second list item's track is nowhere near the video, and
		// "video track" would have matched it.
		{`<ul><li><video src="a.mp4"></video><li><track kind="captions" src="c.vtt"></ul>`, false},
	} {
		got, res := add(t, tc.doc, std)
		if tc.hadTrack {
			if res.HadTrack != 1 || res.Added != 0 || got != tc.doc {
				t.Errorf("%q: %v, output %q", tc.doc, res, got)
			}
			continue
		}
		if res.Added != 1 {
			t.Errorf("%q: %v, output %q", tc.doc, res, got)
		}
	}
}

// TestAForeignEndTagIsUsedWhenItIsTheVideosEnd and declined when it is not. This is
// the end-tag rule in one test: <div><video></div> ends the video at </div>, and
// <li><video><li> ended it at a start tag that fires no handler.
func TestAForeignEndTagIsUsedWhenItIsTheVideosEnd(t *testing.T) {
	got, res := add(t, `<div><video src="a.mp4"></div>`, std)
	if want := `<div><video src="a.mp4">` + track("/captions/a.vtt") + `</div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Added != 1 || res.Displaced != 0 {
		t.Errorf("%v", res)
	}

	const displaced = `<ul><li><video src="a.mp4"><li>next</ul>`
	got, res = add(t, displaced, std)
	if got != displaced {
		t.Errorf("got %q, want it untouched", got)
	}
	if res.Displaced != 1 || res.Added != 0 || res.OK() {
		t.Errorf("%v", res)
	}
	// Every tag that closes by starting, over the shapes a video can sit in. Each
	// needs a real end tag somewhere after it, because that is what makes the
	// position wrong rather than absent: with nothing closing the video at all the
	// handler never runs and the video is unclosed instead.
	for _, doc := range []string{
		`<div><p><video src="a.mp4"><p>next</div>`,
		`<table><tr><td><video src="a.mp4"><td>next</table>`,
		`<dl><dt><video src="a.mp4"><dd>next</dl>`,
		`<table><tbody><tr><td><video src="a.mp4"><tr><td>next</table>`,
	} {
		got, res := add(t, doc, std)
		if strings.Contains(got, "track") {
			t.Errorf("%q got a track: %q", doc, got)
		}
		if res.Displaced != 1 {
			t.Errorf("%q: %v", doc, res)
		}
	}
}

// TestAVideoNothingClosesGetsNothing, because no end-tag handler runs at all: the
// count is the only thing left to report.
func TestAVideoNothingClosesGetsNothing(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		unclosed int
	}{
		{`<video src="a.mp4">`, 1},
		{`<video src="a.mp4"><video src="b.mp4">`, 2},
		{`<video src="a.mp4"><source src="b.mp4">`, 1},
	} {
		got, res := add(t, tc.doc, std)
		if strings.Contains(got, "track") {
			t.Errorf("%q got a track: %q", tc.doc, got)
		}
		if res.Unclosed != tc.unclosed {
			t.Errorf("%q: Unclosed = %d, want %d", tc.doc, res.Unclosed, tc.unclosed)
		}
		if res.OK() {
			t.Errorf("%q: OK() is true with an unclosed video", tc.doc)
		}
	}
	// A closed video before an unclosed one still gets its track: the unclosed one
	// is the only thing missed.
	got, res := add(t, `<video src="a.mp4"></video><video src="b.mp4">`, std)
	if want := `<video src="a.mp4">` + track("/captions/a.vtt") + `</video><video src="b.mp4">`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Added != 1 || res.Unclosed != 1 {
		t.Errorf("%v", res)
	}
}

// TestAddingTwiceChangesNothing, which is what a placeholder has to be: the second
// pass finds the track it wrote.
func TestAddingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<video src="/v/talk.mp4"></video>`,
		`<video><source src="b.webm"></video>`,
		`<div><video src="a.mp4"></div>`,
		`<video src="a.mp4"><track kind="descriptions" src="d.vtt"></video>`,
		`<ul><li><video src="a.mp4"><li>next</ul>`,
	} {
		once, _ := add(t, doc, std)
		twice, res := add(t, once, std)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Added != 0 {
			t.Errorf("%q: the second pass added %d", doc, res.Added)
		}
	}
}

// TestTheTrackIsEscaped, since the pattern and the labels come from a caller and
// the name comes from the document.
func TestTheTrackIsEscaped(t *testing.T) {
	got, _ := add(t, `<video src='a"b.mp4'></video>`, Options{
		Src: "/c/{name}.vtt", Lang: `en"x`, Label: `Cap "tions" & co`,
	})
	// Nothing the caller or the document supplied can end an attribute or the tag.
	if strings.Contains(got, `<track kind="captions" srclang="en"`) {
		t.Errorf("the srclang value ended its attribute: %q", got)
	}
	// It reads back as the source spells it, references and all, because that is
	// what an attribute value is here: see the package documentation on source
	// being undecoded. So the readback is the escaped form, which is the evidence
	// that the escaping happened.
	var lang, label, src string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("track", func(e *lolhtml.Element) error {
		lang, _ = e.Attribute("srclang")
		label, _ = e.Attribute("label")
		src, _ = e.Attribute("src")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if lang != `en&quot;x` || label != `Cap &quot;tions&quot; &amp; co` || src != `/c/a&quot;b.vtt` {
		t.Errorf("srclang=%q label=%q src=%q", lang, label, src)
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<video src="/v/a.mp4"></video><video src="b.mp4"><track kind="captions" src="c.vtt"></video><ul><li><video src="d.mp4"><li>x</ul>`
	want, wantRes := add(t, doc, std)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		c := &captioner{opts: std}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, c.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
		if c.res != wantRes {
			t.Errorf("chunks of %d: %v, want %v", size, c.res, wantRes)
		}
	}
}

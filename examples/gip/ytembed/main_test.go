package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const id = "dQw4w9WgXcQ"

var corpus = []string{
	`<iframe src="https://www.youtube.com/embed/` + id + `"></iframe>`,
	`<iframe src="https://youtube.com/embed/` + id + `"></iframe>`,
	`<iframe src="https://www.youtube-nocookie.com/embed/` + id + `?rel=0"></iframe>`,
	`<iframe src="https://youtu.be/` + id + `"></iframe>`,
	`<iframe src="https://www.youtube.com/watch?v=` + id + `"></iframe>`,
	`<iframe src="https://www.youtube.com/v/` + id + `"></iframe>`,
	`<iframe src="//www.youtube.com/embed/` + id + `"></iframe>`,
	`<iframe src="https://www.youtube.com/playlist?list=x"></iframe>`,
	`<iframe src="https://www.youtube.com/embed/short"></iframe>`,
	`<iframe src="https://other.example/e"></iframe>`,
	`<iframe></iframe>`,
	`<iframe src="https://www.youtube.com/embed/` + id + `" style="border:0" class="v"></iframe>`,
	`<iframe src="https://www.youtube.com/embed/` + id + `">fallback</iframe>`,
	`<p>no iframes</p>`,
	``,
}

func chunked(in string, n int, r *replacer) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, r.options()...)
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
		whole, _, err := replaceString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &replacer{quality: "hq"})
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
		once, _, err := replaceString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, r, err := replaceString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if r.replaced != 0 {
			t.Errorf("second pass of %q replaced %d", doc, r.replaced)
		}
	}
}

// TestVideoIDShapes is where this program's work is. An id that came out wrong
// gives a placeholder for the wrong video, which is worse than no placeholder.
func TestVideoIDShapes(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want string
	}{
		{"https://www.youtube.com/embed/" + id, id},
		{"https://youtube.com/embed/" + id, id},
		{"https://m.youtube.com/embed/" + id, id},
		{"https://www.youtube-nocookie.com/embed/" + id, id},
		{"https://youtube-nocookie.com/embed/" + id, id},
		{"https://www.youtube.com/embed/" + id + "?rel=0&start=30", id},
		{"https://www.youtube.com/embed/" + id + "#t=10", id},
		{"https://youtu.be/" + id, id},
		{"https://youtu.be/" + id + "?t=30", id},
		{"https://www.youtube.com/watch?v=" + id, id},
		{"https://www.youtube.com/watch?list=x&v=" + id, id},
		{"https://www.youtube.com/v/" + id, id},
		{"//www.youtube.com/embed/" + id, id},
		{"HTTPS://WWW.YOUTUBE.COM/embed/" + id, id},

		// Not an embed, or not an id.
		{"https://www.youtube.com/playlist?list=x", ""},
		{"https://www.youtube.com/embed/short", ""},
		{"https://www.youtube.com/embed/" + id + "extra", ""},
		{"https://www.youtube.com/embed/", ""},
		{"https://www.youtube.com/", ""},
		{"https://www.youtube.com/watch", ""},
		{"https://www.youtube.com/embed/bad.chars!!", ""},
		{"https://other.example/embed/" + id, ""},
		{"", ""},
		{"not a url at all", ""},
	} {
		got, ok := videoID(tt.src)
		if tt.want == "" {
			if ok {
				t.Errorf("videoID(%q) = %q, want no id", tt.src, got)
			}
			continue
		}
		if !ok || got != tt.want {
			t.Errorf("videoID(%q) = %q/%v, want %q", tt.src, got, ok, tt.want)
		}
	}
}

// TestAnUnrecognisedYouTubeURLIsReported, and a non-YouTube one is not: the first
// is something this program was meant to handle and could not, the second is none
// of its business.
func TestAnUnrecognisedYouTubeURLIsReported(t *testing.T) {
	_, r, err := replaceString(`<iframe src="https://www.youtube.com/playlist?list=x"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if total(r.skipped) != 1 {
		t.Errorf("skipped=%v, want one entry", r.skipped)
	}

	_, r, err = replaceString(`<iframe src="https://other.example/e"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if total(r.skipped) != 0 {
		t.Errorf("a non-YouTube iframe was reported as skipped: %v", r.skipped)
	}
}

// TestNoMarkupIsBuiltByHand: the placeholder is made by changing the iframe, so
// every value goes through SetAttribute and is escaped. A hostile title cannot
// become an attribute.
func TestNoMarkupIsBuiltByHand(t *testing.T) {
	in := `<iframe title='" onload=alert(1) x="' src="https://www.youtube.com/embed/` + id + `"></iframe>`
	got, r, err := replaceString(in)
	if err != nil {
		t.Fatal(err)
	}
	if r.replaced != 1 {
		t.Fatalf("replaced=%d, want 1", r.replaced)
	}
	// The title is re-emitted in its original single-quoted form, so the double
	// quotes stay literal and no attribute was created from them.
	if !strings.Contains(got, `title='" onload=alert(1) x="'`) {
		t.Errorf("the title was not re-emitted verbatim: %s", got)
	}
	if strings.Contains(got, `x=""`) {
		t.Errorf("the payload became attributes: %s", got)
	}
}

// TestTheEmbedNoLongerLoads is the point: the src has to go, or the player is
// still fetched.
func TestTheEmbedNoLongerLoads(t *testing.T) {
	got, _, err := replaceString(`<iframe src="https://www.youtube.com/embed/` + id + `"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, ` src="`) {
		t.Errorf("the src survived: %s", got)
	}
	if !strings.Contains(got, `data-yt-src="https://www.youtube.com/embed/`+id+`"`) {
		t.Errorf("the original src was not kept for a script to restore: %s", got)
	}
	if !strings.Contains(got, "<div ") || strings.Contains(got, "<iframe") {
		t.Errorf("the element was not changed: %s", got)
	}
}

func TestThumbnailQualities(t *testing.T) {
	for _, tt := range []struct{ quality, want string }{
		{"default", "https://i.ytimg.com/vi/" + id + "/default.jpg"},
		{"mq", "https://i.ytimg.com/vi/" + id + "/mqdefault.jpg"},
		{"hq", "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"},
		{"sd", "https://i.ytimg.com/vi/" + id + "/sddefault.jpg"},
		{"maxres", "https://i.ytimg.com/vi/" + id + "/maxresdefault.jpg"},
	} {
		if got := thumbURL(id, tt.quality); got != tt.want {
			t.Errorf("thumbURL(%q) = %q, want %q", tt.quality, got, tt.want)
		}
	}
}

func TestExistingStyleAndClassAreKept(t *testing.T) {
	got, _, err := replaceString(
		`<iframe src="https://www.youtube.com/embed/` + id + `" style="border:0" class="v"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "border:0;background-image:") {
		t.Errorf("the existing style was not preserved: %s", got)
	}
	if !strings.Contains(got, `class="v yt-placeholder"`) {
		t.Errorf("the existing class was not preserved: %s", got)
	}
}

// TestValidIDRejectsAnythingElse: an id is eleven characters of the URL-safe
// base64 alphabet, and accepting more would put unescaped text into a URL.
func TestValidIDRejects(t *testing.T) {
	for _, bad := range []string{
		"", "short", "waytoolongforanid", "elevenchar!", "eleven char",
		"eleven/char", "eleven?char", `eleven"char`, "eleven<char",
	} {
		if got, ok := validID(bad); ok {
			t.Errorf("validID(%q) = %q, true", bad, got)
		}
	}
	for _, good := range []string{id, "abcdefghijk", "A-B_c1234_5", "___________"} {
		if _, ok := validID(good); !ok {
			t.Errorf("validID(%q) = false", good)
		}
	}
	// A trailing path or query is trimmed rather than rejected.
	if got, ok := validID(id + "/more"); !ok || got != id {
		t.Errorf("validID(%q) = %q/%v", id+"/more", got, ok)
	}
}

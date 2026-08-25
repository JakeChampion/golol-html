package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func stop(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Stop(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Stop(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheAttributeGoesWhereItWouldMakeSound, and stays where it would not.
func TestTheAttributeGoesWhereItWouldMakeSound(t *testing.T) {
	for _, tc := range []struct {
		doc, want string
		muted     int
	}{
		{`<video autoplay src="a"></video>`, `<video src="a"></video>`, 0},
		{`<audio autoplay src="a"></audio>`, `<audio src="a"></audio>`, 0},
		{`<video AUTOPLAY src="a"></video>`, `<video src="a"></video>`, 0},
		{`<video autoplay="autoplay" src="a"></video>`, `<video src="a"></video>`, 0},
		// A boolean attribute is about presence, so this autoplays too and goes.
		{`<video autoplay="false" src="a"></video>`, `<video src="a"></video>`, 0},
		// Muted: a background element rather than a nuisance, left alone and counted.
		{`<video autoplay muted src="a"></video>`, `<video autoplay muted src="a"></video>`, 1},
		{`<video autoplay muted="" src="a"></video>`, `<video autoplay muted="" src="a"></video>`, 1},
		// Nothing to do.
		{`<video src="a"></video>`, `<video src="a"></video>`, 0},
		{`<video muted src="a"></video>`, `<video muted src="a"></video>`, 0},
	} {
		got, res := stop(t, tc.doc, Options{})
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.doc, got, tc.want)
		}
		if res.Muted != tc.muted {
			t.Errorf("%q: Muted = %d, want %d", tc.doc, res.Muted, tc.muted)
		}
	}
	// -all takes the muted one too, for a caller who would rather have the still
	// image.
	got, res := stop(t, `<video autoplay muted src="a"></video>`, Options{All: true})
	if got != `<video muted src="a"></video>` {
		t.Errorf("got %q", got)
	}
	if res.Attributes != 1 || res.Muted != 0 {
		t.Errorf("%v", res)
	}
}

// TestTheQueryParameterGoesAndTheRestOfTheURLDoesNot, which is the part that has
// to be exact: a rewrite that normalises a URL it was only pruning has changed it.
func TestTheQueryParameterGoesAndTheRestOfTheURLDoesNot(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`https://p/e/1?autoplay=1`, `https://p/e/1`},
		{`https://p/e/1?autoplay=1&t=3`, `https://p/e/1?t=3`},
		{`https://p/e/1?t=3&autoplay=1`, `https://p/e/1?t=3`},
		{`https://p/e/1?a=1&autoplay=1&b=2`, `https://p/e/1?a=1&b=2`},
		// The separator the document used is the separator that comes back.
		{`https://p/e/1?autoplay=1&amp;t=3`, `https://p/e/1?t=3`},
		{`https://p/e/1?t=3&amp;autoplay=1&amp;r=0`, `https://p/e/1?t=3&amp;r=0`},
		{`https://p/e/1?t=3&autoplay=1&amp;r=0`, `https://p/e/1?t=3&amp;r=0`},
		// The fragment is not part of the query.
		{`/e?autoplay=1#at=30`, `/e#at=30`},
		{`/e?a=1&autoplay=1#at=30`, `/e?a=1#at=30`},
		// The other spellings players use.
		{`/e?auto_play=1`, `/e`},
		{`/e?autostart=true`, `/e`},
		{`/e?AUTOPLAY=1`, `/e`},
		// A value of zero is a page that already decided, and the parameter goes
		// either way: what it says is not the question.
		{`/e?autoplay=0`, `/e`},
		// Nothing to do.
		{`/e?t=3`, `/e?t=3`},
		{`/e`, `/e`},
		{``, ``},
		// A parameter that merely contains the word is not the parameter.
		{`/e?myautoplay=1`, `/e?myautoplay=1`},
		{`/e?x=autoplay`, `/e?x=autoplay`},
	} {
		doc := `<iframe src="` + tc.in + `"></iframe>`
		got, _ := stop(t, doc, Options{})
		want := `<iframe src="` + tc.want + `"></iframe>`
		if tc.in == "" {
			want = doc
		}
		if got != want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, want)
		}
	}
}

// TestTheURLIsReadBackAsOneAttribute, because this program writes it and the
// document's own spelling is what goes in.
func TestTheURLIsReadBackAsOneAttribute(t *testing.T) {
	got, _ := stop(t, `<iframe src="/e?autoplay=1&amp;a=1&amp;b=2"></iframe>`, Options{})
	var value string
	var count int
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("iframe", func(e *lolhtml.Element) error {
		for _, a := range e.AttributeList() {
			if a.Name == "src" {
				value = a.Value
				count++
			}
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%d src attributes in %q", count, got)
	}
	if value != "/e?a=1&amp;b=2" {
		t.Errorf("src reads back as %q", value)
	}
}

// TestTheAllowTokenGoesAndTheOthersStay.
func TestTheAllowTokenGoesAndTheOthersStay(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`autoplay`, ``},
		{`autoplay; fullscreen`, `fullscreen`},
		{`fullscreen; autoplay`, `fullscreen`},
		{`fullscreen; autoplay; picture-in-picture`, `fullscreen; picture-in-picture`},
		{`AUTOPLAY; fullscreen`, `fullscreen`},
		{`autoplay 'self' https://x`, ``},
		{`fullscreen 'self'; autoplay 'src'`, `fullscreen 'self'`},
		{`fullscreen`, `fullscreen`},
	} {
		doc := `<iframe src="/e" allow="` + tc.in + `"></iframe>`
		got, _ := stop(t, doc, Options{})
		if tc.want == "" {
			// Nothing left in the list, so the attribute goes: an empty allow is not
			// the same as an absent one to every reader.
			if strings.Contains(got, "allow") {
				t.Errorf("%q -> %q, want no allow attribute", tc.in, got)
			}
			continue
		}
		if !strings.Contains(got, `allow="`+tc.want+`"`) {
			t.Errorf("%q -> %q, want allow=%q", tc.in, got, tc.want)
		}
	}
}

// TestAScriptThatPlaysIsReportedAndNotRewritten, because a program that edited
// JavaScript would be a different program and a worse idea.
func TestAScriptThatPlaysIsReportedAndNotRewritten(t *testing.T) {
	for _, doc := range []string{
		`<script>document.querySelector("video").play()</script>`,
		`<script>v . play ( )</script>`,
		`<script>new Player({autoplay: true})</script>`,
		`<script>x={autoplay:1}</script>`,
	} {
		got, res := stop(t, doc, Options{})
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Scripts != 1 {
			t.Errorf("%q: Scripts = %d, want 1", doc, res.Scripts)
		}
		if res.OK() {
			t.Errorf("%q: OK() is true with a playing script", doc)
		}
	}
	// A script that says nothing about playing is not reported.
	for _, doc := range []string{
		`<script>console.log("hello")</script>`,
		`<script>display()</script>`,
		`<p>call video.play() to start</p>`, // prose, not a script
	} {
		_, res := stop(t, doc, Options{})
		if res.Scripts != 0 {
			t.Errorf("%q: Scripts = %d, want 0", doc, res.Scripts)
		}
	}
}

// TestTheScriptSearchSurvivesChunkBoundaries, which a search per chunk would not:
// missing a match makes this program report less than it should.
func TestTheScriptSearchSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<script>window.addEventListener("load",()=>{document.video.play()})</script>`
	for _, size := range []int{1, 2, 3, 7, 64, len(doc)} {
		s := &stopper{}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, s.options()...)
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
		if s.res.Scripts != 1 {
			t.Errorf("chunks of %d: Scripts = %d, want 1", size, s.res.Scripts)
		}
		if out.String() != doc {
			t.Errorf("chunks of %d: the script was changed: %q", size, out.String())
		}
	}
}

// TestStoppingTwiceChangesNothing.
func TestStoppingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<video autoplay src="a"></video>`,
		`<video autoplay muted src="a"></video>`,
		`<iframe src="/e?autoplay=1&amp;t=3" allow="autoplay; fullscreen"></iframe>`,
		`<script>v.play()</script>`,
	} {
		for _, opts := range []Options{{}, {All: true}} {
			once, _ := stop(t, doc, opts)
			twice, res := stop(t, once, opts)
			if twice != once {
				t.Errorf("%q (all=%v)\n once %q\ntwice %q", doc, opts.All, once, twice)
			}
			if res.Attributes+res.Params+res.Allows != 0 {
				t.Errorf("%q: the second pass removed something: %v", doc, res)
			}
		}
	}
}

// TestOnlyMediaAndEmbedsAreTouched: an autoplay attribute on something else is not
// this program's business, and neither is a query parameter on a link.
func TestOnlyMediaAndEmbedsAreTouched(t *testing.T) {
	for _, doc := range []string{
		`<div autoplay>x</div>`,
		`<a href="/e?autoplay=1">x</a>`,
		`<img src="/e?autoplay=1">`,
		`<form action="/e?autoplay=1"></form>`,
	} {
		if got, _ := stop(t, doc, Options{}); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
	// An embed element is an embed.
	got, _ := stop(t, `<embed src="/e?autoplay=1">`, Options{})
	if strings.Contains(got, "autoplay") {
		t.Errorf("got %q", got)
	}
}

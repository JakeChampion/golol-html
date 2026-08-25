package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func modernise(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Modernise(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Modernise(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestEveryRenameIsTheOneTheTableSays.
func TestEveryRenameIsTheOneTheTableSays(t *testing.T) {
	for tag, want := range Renames {
		doc := "<" + tag + ">x</" + tag + ">"
		if tag == "image" {
			doc = `<image src="a.png">`
		}
		got, res := modernise(t, doc, Options{Prefix: "m-"})
		if !strings.HasPrefix(got, "<"+want.to) {
			t.Errorf("<%s> became %q, want a <%s>", tag, got, want.to)
		}
		if res.Renamed != 1 {
			t.Errorf("<%s>: %v", tag, res)
		}
		if want.class != "" && !strings.Contains(got, `class="m-`+want.class+`"`) {
			t.Errorf("<%s> became %q, want the class m-%s", tag, got, want.class)
		}
		if want.warn != "" && len(res.Warnings) == 0 {
			t.Errorf("<%s>: no warning, want %q", tag, want.warn)
		}
	}
}

// TestWhatItWillNotRenameIsReportedAndLeft.
func TestWhatItWillNotRenameIsReportedAndLeft(t *testing.T) {
	for tag, why := range Left {
		doc := "<" + tag + ">"
		if tag != "frame" && tag != "basefont" && tag != "keygen" && tag != "isindex" && tag != "spacer" {
			doc = "<" + tag + "></" + tag + ">"
		}
		if tag == "plaintext" {
			doc = "<plaintext>x"
		}
		got, res := modernise(t, doc, Options{})
		if got != doc {
			t.Errorf("<%s> was rewritten to %q", tag, got)
		}
		if res.Left != 1 || res.Renamed != 0 {
			t.Errorf("<%s>: %v", tag, res)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], why) {
			t.Errorf("<%s>: warnings are %v, want %q", tag, res.Warnings, why)
		}
	}
}

// TestFontsAttributesBecomeClasses, one per attribute, with the value in the name
// because red and blue are different styles.
func TestFontsAttributesBecomeClasses(t *testing.T) {
	got, res := modernise(t, `<font color="red" face="Arial Black" size="4">x</font>`, Options{})
	for _, want := range []string{"m-color-red", "m-face-arial-black", "m-size-4"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
	for _, gone := range []string{"color=", "face=", "size="} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived: %q", gone, got)
		}
	}
	if res.Classes != 3 {
		t.Errorf("%v", res)
	}
	// A hex colour keeps its digits and gets its hash back in the stylesheet.
	got, res = modernise(t, `<font color="#ff0000">x</font>`, Options{})
	if !strings.Contains(got, "m-color-ff0000") {
		t.Errorf("got %q", got)
	}
	if css := res.Stylesheet("m-"); !strings.Contains(css, "color: #ff0000") {
		t.Errorf("the stylesheet says %q", css)
	}
	// A relative size is not an absolute one, and the class says which it was.
	got, _ = modernise(t, `<font size="+2">x</font>`, Options{})
	if !strings.Contains(got, "m-size-plus2") {
		t.Errorf("got %q", got)
	}
}

// TestTheClassIsAddedToWhateverTheElementHad, since a page's own classes are the ones
// its stylesheet knows about.
func TestTheClassIsAddedToWhateverTheElementHad(t *testing.T) {
	got, _ := modernise(t, `<center class="hero wide">x</center>`, Options{})
	if want := `<div class="hero wide m-center">x</div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// The same class twice is one class.
	got, res := modernise(t, `<center class="m-center">x</center>`, Options{})
	if want := `<div class="m-center">x</div>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Classes != 0 {
		t.Errorf("%v", res)
	}
}

// TestAttributeClassesAreOptional, because the attribute set is larger and its CSS is
// likelier to collide with a page's own.
func TestAttributeClassesAreOptional(t *testing.T) {
	const doc = `<td align="left" bgcolor="#fff">x</td>`
	got, res := modernise(t, doc, Options{})
	if got != doc {
		t.Errorf("without -attributes the document changed: %q", got)
	}
	if res.Classes != 0 {
		t.Errorf("%v", res)
	}
	got, res = modernise(t, doc, Options{Attributes: true})
	if want := `<td class="m-align-left m-bgcolor-fff">x</td>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Classes != 2 {
		t.Errorf("%v", res)
	}
}

// TestAnSVGImageIsLeftAlone: it is its own element, not a spelling of img.
func TestAnSVGImageIsLeftAlone(t *testing.T) {
	const doc = `<svg><image xlink:href="a.png"/></svg>`
	got, res := modernise(t, doc, Options{})
	if got != doc {
		t.Errorf("got %q", got)
	}
	if res.Renamed != 0 {
		t.Errorf("%v", res)
	}
	// And the HTML one is renamed, since the parser was going to anyway.
	got, res = modernise(t, `<image src="a.png">`, Options{})
	if got != `<img src="a.png">` {
		t.Errorf("got %q", got)
	}
	if res.Renamed != 1 {
		t.Errorf("%v", res)
	}
}

// TestTheStylesheetSaysWhatTheClassesAreFor.
func TestTheStylesheetSaysWhatTheClassesAreFor(t *testing.T) {
	_, res := modernise(t, `<center>x</center><nobr>y</nobr><font color="red">z</font>`, Options{})
	css := res.Stylesheet("m-")
	for _, want := range []string{
		".m-center { text-align: center }",
		".m-nowrap { white-space: nowrap }",
		".m-color-red { color: red }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("%q is missing from:\n%s", want, css)
		}
	}
	// Uses are counted, so a caller knows which rules matter.
	_, res = modernise(t, strings.Repeat(`<center>x</center>`, 3), Options{})
	if !strings.Contains(res.Stylesheet("m-"), "3 uses") {
		t.Errorf("%s", res.Stylesheet("m-"))
	}
	// A font size maps to the keyword scale rather than to a bare number, which
	// would not be CSS at all.
	_, res = modernise(t, `<font size="7">x</font>`, Options{})
	if !strings.Contains(res.Stylesheet("m-"), "font-size: xxx-large") {
		t.Errorf("%s", res.Stylesheet("m-"))
	}
}

// TestModernisingTwiceChangesNothing, which is what makes it safe in a pipeline.
func TestModernisingTwiceChangesNothing(t *testing.T) {
	for _, opts := range []Options{{}, {Attributes: true}} {
		for _, doc := range []string{
			`<center><font color="red">x</font></center>`,
			`<acronym title="t">WWW</acronym>`,
			`<dir><li>a</li></dir>`,
			`<image src="a.png">`,
			`<td align="left">y</td>`,
			`<applet code="x"></applet>`,
		} {
			once, _ := modernise(t, doc, opts)
			twice, res := modernise(t, once, opts)
			if twice != once {
				t.Errorf("%q (attributes=%v)\n once %q\ntwice %q", doc, opts.Attributes, once, twice)
			}
			if res.Renamed != 0 || res.Classes != 0 {
				t.Errorf("%q: the second pass changed %v", doc, res)
			}
		}
	}
}

// TestTheContentIsWhereItWas, which is the finding applied: every rename in the table
// is one whose new content model accepts what the old element held.
func TestTheContentIsWhereItWas(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<center><p>x</p><table><tr><td>y</td></tr></table></center>`,
			`<div class="m-center"><p>x</p><table><tr><td>y</td></tr></table></div>`},
		{`<dir><li>a</li><li>b</li></dir>`, `<ul><li>a</li><li>b</li></ul>`},
		{`<big>x<b>y</b>z</big>`, `<span class="m-big">x<b>y</b>z</span>`},
	} {
		got, _ := modernise(t, tc.doc, Options{})
		if got != tc.want {
			t.Errorf("\n got %q\nwant %q", got, tc.want)
		}
	}
}

// TestTheDecisionSurvivesChunkBoundaries.
func TestTheDecisionSurvivesChunkBoundaries(t *testing.T) {
	const doc = `<center><font color="red" size="4">x</font></center><acronym title="t">WWW</acronym><image src="a.png"><applet code="c"></applet>`
	want, wantRes := modernise(t, doc, Options{Attributes: true})
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		m := &moderniser{opts: Options{Prefix: "m-", Attributes: true}}
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, m.options()...)
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
		if m.res.Renamed != wantRes.Renamed || m.res.Classes != wantRes.Classes {
			t.Errorf("chunks of %d: %v, want %v", size, m.res, wantRes)
		}
	}
}

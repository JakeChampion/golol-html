package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func check(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Check([]byte(doc))
	if err != nil {
		t.Fatalf("Check(%q): %v", doc, err)
	}
	return res
}

func kinds(res Result) string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, string(f.Kind))
	}
	return strings.Join(out, ",")
}

// TestAMissingAltAndAnEmptyAltAreOpposites. This is the rule a naive linter gets
// backwards, and getting it backwards makes the linter worse than nothing: it
// tells a page that did the right thing to stop doing it.
func TestAMissingAltAndAnEmptyAltAreOpposites(t *testing.T) {
	for _, tc := range []struct {
		doc        string
		want       string
		decorative int
	}{
		{`<img src="/a.png">`, "missing", 0},
		{`<img src="/a.png" alt="">`, "", 1},
		{`<img src="/a.png" alt>`, "", 1},
		{`<img src="/a.png" alt="A cat">`, "", 0},
		{`<img src="/a.png" alt=" ">`, "whitespace", 0},
		{"<img src=\"/a.png\" alt=\" \t \">", "whitespace", 0},
		// A presentation role says the same thing as an empty alt.
		{`<img src="/a.png" role="presentation">`, "", 1},
		{`<img src="/a.png" role="none">`, "", 1},
		{`<img src="/a.png" role="NONE">`, "", 1},
		// A role that is not one of those does not excuse a missing alt.
		{`<img src="/a.png" role="button">`, "missing", 0},
	} {
		res := check(t, tc.doc)
		if got := kinds(res); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
		if res.Decorative != tc.decorative {
			t.Errorf("%q: Decorative = %d, want %d", tc.doc, res.Decorative, tc.decorative)
		}
	}
}

// TestTheOtherPlacesANameCanComeFrom, because calling an alt missing when the
// image is named another way is a false report, and a linter that cries wolf is
// switched off.
func TestTheOtherPlacesANameCanComeFrom(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<img src="/a.png" aria-label="A cat">`, ""},
		{`<img src="/a.png" aria-label="   ">`, "missing"},
		{`<h2 id="cap">A cat</h2><img src="/a.png" aria-labelledby="cap">`, ""},
		// The target after the image, which is the case that needs the whole
		// document before it can be decided.
		{`<img src="/a.png" aria-labelledby="cap"><h2 id="cap">A cat</h2>`, ""},
		{`<img src="/a.png" aria-labelledby="cap">`, "labelledby,missing"},
		{`<img src="/a.png" aria-labelledby="one two"><h2 id="one">a</h2>`, "labelledby,missing"},
		{`<img src="/a.png" aria-labelledby="one two"><h2 id="one">a</h2><h3 id="two">b</h3>`, ""},
		// A title is not a name: support for it is too poor to rely on, and the
		// message says so rather than pretending the image is unnamed for no
		// reason.
		{`<img src="/a.png" title="A cat">`, "missing"},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
	// The title case says what it found.
	res := check(t, `<img src="/a.png" title="A cat">`)
	if !strings.Contains(res.Findings[0].Message, "title") {
		t.Errorf("the message does not mention the title: %q", res.Findings[0].Message)
	}
}

// TestAltThatRepeatsTheFileName.
func TestAltThatRepeatsTheFileName(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<img src="/img/logo.png" alt="logo.png">`, "filename"},
		{`<img src="/img/logo.png" alt="logo">`, "filename"},
		{`<img src="/img/logo.png" alt="LOGO.PNG">`, "filename"},
		{`<img src="/img/logo.png" alt="The company logo">`, ""},
		{`<img src="/img/logo.png" alt="logotype">`, ""},
		{`<img alt="anything">`, ""},
		{`<img src="/" alt="/">`, ""},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestAltThatSaysItIsAnImage, which a screen reader has already said.
func TestAltThatSaysItIsAnImage(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<img src="/a.png" alt="Image of a cat">`, "prefix"},
		{`<img src="/a.png" alt="photo of a cat">`, "prefix"},
		{`<img src="/a.png" alt="A picture of a cat">`, "prefix"},
		{`<img src="/a.png" alt="Screenshot of the settings page">`, "prefix"},
		{`<img src="/a.png" alt="A cat">`, ""},
		// Not a prefix: the words appear later in the sentence.
		{`<img src="/a.png" alt="A cat in a photo of a garden">`, ""},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestAltThatRepeatsTextArrivingAfterIt is the ordering point: a rewrite would
// need a second pass for this, and a report does not need one at all.
func TestAltThatRepeatsTextArrivingAfterIt(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<a href="/d"><img src="/i.png" alt="Read the docs"> Read the docs</a>`, "link-text"},
		{`<a href="/d"><img src="/i.png" alt="read   the docs">Read the docs</a>`, "link-text"},
		{`<a href="/d"><img src="/i.png" alt="Documentation"> Read the docs</a>`, ""},
		// An image in a link with no other text is the link's name, which is
		// correct and common.
		{`<a href="/d"><img src="/i.png" alt="Read the docs"></a>`, ""},
		{`<figure><img src="/f.png" alt="A chart"><figcaption>A chart</figcaption></figure>`, "caption"},
		{`<figure><img src="/f.png" alt="Revenue by quarter"><figcaption>A chart</figcaption></figure>`, ""},
		// A figure's own text is not its caption.
		{`<figure><img src="/f.png" alt="A chart">A chart</figure>`, ""},
		// Nesting: the innermost link is the one that matters.
		{`<a href="/o">outer<a href="/i"><img src="/i.png" alt="inner">inner</a></a>`, "link-text"},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheOtherElementsThatNeedAName.
func TestTheOtherElementsThatNeedAName(t *testing.T) {
	for _, tc := range []struct {
		doc    string
		want   string
		images int
	}{
		{`<input type="image" src="/go.png">`, "missing", 1},
		{`<input type="image" src="/go.png" alt="Search">`, "", 1},
		{`<map><area href="/a" shape="rect" coords="0,0,1,1"></map>`, "missing", 1},
		{`<map><area href="/a" alt="Region"></map>`, "", 1},
		// An input that is not an image button needs no alt.
		{`<input type="text">`, "", 0},
		{`<input>`, "", 0},
	} {
		res := check(t, tc.doc)
		if got := kinds(res); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
		if res.Images != tc.images {
			t.Errorf("%q: Images = %d, want %d", tc.doc, res.Images, tc.images)
		}
	}
}

// TestTheFindingsAreInDocumentOrderWithLocations, since a report is read by a
// person and by a tool that jumps to a line.
func TestTheFindingsAreInDocumentOrderWithLocations(t *testing.T) {
	doc := "<body>\n  <img src=\"/a.png\">\n  <img src=\"/b.png\" alt=\"b.png\">\n</body>"
	res := check(t, doc)
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %v", res.Findings)
	}
	if res.Findings[0].Line != 2 || res.Findings[0].Column != 3 {
		t.Errorf("first finding at %d:%d, want 2:3", res.Findings[0].Line, res.Findings[0].Column)
	}
	if res.Findings[1].Line != 3 || res.Findings[1].Column != 3 {
		t.Errorf("second finding at %d:%d, want 3:3", res.Findings[1].Line, res.Findings[1].Column)
	}
	if res.Findings[0].At >= res.Findings[1].At {
		t.Error("the findings are not in document order")
	}
	// A finding whose evidence arrived later is still reported at the image.
	doc = "<a href=\"/d\">\n  <img src=\"/i.png\" alt=\"Read the docs\">\n  Read the docs\n</a>"
	res = check(t, doc)
	if len(res.Findings) != 1 || res.Findings[0].Line != 2 {
		t.Errorf("findings = %v, want one on line 2", res.Findings)
	}
}

// TestNothingToSayIsSaidWithNothing, so the tool is quiet on a good page.
func TestNothingToSayIsSaidWithNothing(t *testing.T) {
	doc := `<body>
	  <img src="/hero.png" alt="A cat asleep on a keyboard">
	  <img src="/divider.png" alt="">
	  <a href="/docs"><img src="/book.png" alt="Documentation"> Read the docs</a>
	  <figure><img src="/chart.png" alt="Revenue rose in every quarter"><figcaption>Revenue by quarter</figcaption></figure>
	  <h2 id="cap">A diagram</h2><img src="/d.png" aria-labelledby="cap">
	  <input type="image" src="/go.png" alt="Search">
	</body>`
	res := check(t, doc)
	if !res.OK() {
		t.Errorf("findings on a good page: %v", res.Findings)
	}
	if res.Images != 6 || res.Decorative != 1 {
		t.Errorf("%v", res)
	}
}

// TestTheReportIsStableAcrossWritePatterns.
func TestTheReportIsStableAcrossWritePatterns(t *testing.T) {
	doc := "<body>\n<img src=\"/a.png\">\n<a href=\"/d\"><img src=\"/i.png\" alt=\"Read the docs\">Read the docs</a>\n" +
		"<figure><img src=\"/f.png\" alt=\"A chart\"><figcaption>A chart</figcaption></figure>\n" +
		"<img src=\"/y.png\" aria-labelledby=\"nope\">\n</body>"
	want := check(t, doc)
	if len(want.Findings) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		c := &checker{ids: map[string]bool{}, linkText: map[int]*strings.Builder{},
			captions: map[int]*strings.Builder{}}
		w, err := lolhtml.NewWriter(io.Discard, c.options()...)
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
		got := c.report([]byte(doc))
		if len(got.Findings) != len(want.Findings) {
			t.Errorf("chunks of %d: %d findings, want %d", size, len(got.Findings), len(want.Findings))
			continue
		}
		for i := range got.Findings {
			if got.Findings[i] != want.Findings[i] {
				t.Errorf("chunks of %d: finding %d is %+v, want %+v", size, i,
					got.Findings[i], want.Findings[i])
			}
		}
	}
}

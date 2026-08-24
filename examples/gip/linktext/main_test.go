package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<a href="/x">click here</a>`,
	`<a href="/x">Click Here</a>`,
	`<a href="/x">read more &raquo;</a>`,
	`<a href="/x">Proper description</a>`,
	`<a href="/x"></a>`,
	`<a href="/annual-report-2024.pdf">click here</a>`,
	`<a href="/x" aria-label="Open the pricing page">read more</a>`,
	`<a href="/x" title="The pricing page">more</a>`,
	`<a href="/12345">more</a>`,
	`<a href="/x"><img src="/i" alt="Product photo"></a>`,
	`<a href="/x"><img src="/i" alt="more"></a>`,
	`<a href="/x">click <b>here</b></a>`,
	`<a>no href</a>`,
	`<a href="">empty</a>`,
	`<a href="/x">Caf&eacute;</a>`,
	`<ul><li><a href="/a">here</a></li><li><a href="/b">Real</a></li></ul>`,
	`<p>no links</p>`,
	``,
}

func chunked(in string, n int, c *checker, rewrite bool) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, c.options(rewrite)...)
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
		whole, wc, err := checkString(doc, true)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			c := &checker{mark: true}
			got, err := chunked(doc, n, c, false)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
			if c.report() != wc.report() {
				t.Errorf("chunk size %d changed the report for %q", n, doc)
			}
		}
	}
}

// TestTheMarkerIsOneWellFormedComment. Built out of three After calls it comes
// out backwards, as "-->note<!--", which is valid-looking output containing
// broken markup. One call is the fix, and this is what says so.
func TestTheMarkerIsOneWellFormedComment(t *testing.T) {
	got, _, err := checkString(`<a href="/x">click here</a>`, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<!-- linktext: ") {
		t.Errorf("no marker was inserted: %s", got)
	}
	if strings.Index(got, "<!--") > strings.Index(got, "-->") {
		t.Errorf("the comment is inside out: %s", got)
	}
	if strings.Count(got, "<!--") != 1 || strings.Count(got, "-->") != 1 {
		t.Errorf("expected exactly one comment: %s", got)
	}
	// The comment must come after the link, not inside it.
	if strings.Index(got, "</a>") > strings.Index(got, "<!--") {
		t.Errorf("the marker is inside the link: %s", got)
	}
}

// TestTheMarkerCannotEndItsOwnComment: the note carries text from the document,
// so a link whose text contains "-->" would otherwise close the comment early
// and turn the rest into markup.
func TestTheMarkerCannotEndItsOwnComment(t *testing.T) {
	got, _, err := checkString(`<a href="/x--&gt;&lt;script&gt;alert(1)&lt;/script&gt;">here</a>`, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "-->") != 1 {
		t.Errorf("the comment was ended more than once: %s", got)
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("markup escaped the comment: %s", got)
	}
}

func TestGenericTextIsRecognised(t *testing.T) {
	for _, tt := range []struct {
		text string
		want bool
	}{
		{"click here", true},
		{"Click Here", true},
		{"CLICK HERE", true},
		{"here", true},
		{"read more", true},
		{"read more »", true},
		{"more...", true},
		{"", true},
		{"Annual report", false},
		{"Download the annual report", false},
		{"Contact us", false},
	} {
		if got := isGeneric(tt.text); got != tt.want {
			t.Errorf("isGeneric(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// TestReplacementSourcePreference: an author-written label beats an image's alt,
// which beats a guess from the URL. Naming the source in the report is what lets
// a reviewer judge the guess.
func TestReplacementSourcePreference(t *testing.T) {
	for _, tt := range []struct {
		in     string
		want   string
		source string
	}{
		{`<a href="/x" aria-label="From aria">click here</a>`, "From aria", "aria-label or title"},
		{`<a href="/x" title="From title">click here</a>`, "From title", "aria-label or title"},
		// The alt is only a replacement source when the visible text is
		// generic; an image with a descriptive alt and no text is a fine link.
		{`<a href="/x">click here<img src="/i" alt="From alt"></a>`, "From alt", "the image alt text"},
		{`<a href="/annual-report-2024.pdf">click here</a>`, "Annual report 2024", "the target URL"},
		// aria-label wins over the URL.
		{`<a href="/annual-report.pdf" aria-label="From aria">here</a>`, "From aria", "aria-label or title"},
		// A generic aria-label is no better than the text it would replace.
		{`<a href="/annual-report.pdf" aria-label="click here">here</a>`, "Annual report", "the target URL"},
	} {
		_, c, err := checkString(tt.in, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if len(c.bad) != 1 {
			t.Fatalf("%s: bad=%d, want 1", tt.in, len(c.bad))
		}
		if c.bad[0].replacement != tt.want || c.bad[0].source != tt.source {
			t.Errorf("%s: got %q from %q, want %q from %q",
				tt.in, c.bad[0].replacement, c.bad[0].source, tt.want, tt.source)
		}
	}
}

// TestADescriptiveAltIsAFineAccessibleName: an image is often the whole of a
// link, and its alt is what a screen reader reads. Flagging those would be a
// false finding, which is worse than none.
func TestADescriptiveAltIsAFineAccessibleName(t *testing.T) {
	_, c, err := checkString(`<a href="/x"><img src="/i" alt="Product photo"></a>`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.bad) != 0 {
		t.Errorf("bad=%d, want 0: %v", len(c.bad), c.bad)
	}

	// A generic alt is no better than generic text.
	_, c, err = checkString(`<a href="/x"><img src="/i" alt="more"></a>`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.bad) != 1 {
		t.Errorf("bad=%d, want 1: a generic alt is still generic", len(c.bad))
	}
}

// TestNoGuessIsBetterThanABadGuess: a URL segment that is an id says nothing, so
// the link is reported with no suggestion rather than given a meaningless one.
func TestNoGuessIsBetterThanABadGuess(t *testing.T) {
	for _, href := range []string{"/12345", "/x", "/a/b/1", "/", "/p/99"} {
		if got := fromHref(href); got != "" {
			t.Errorf("fromHref(%q) = %q, want no guess", href, got)
		}
	}
	for _, tt := range []struct{ href, want string }{
		{"/annual-report-2024.pdf", "Annual report 2024"},
		{"/about_us", "About us"},
		{"/pricing", "Pricing"},
		{"/docs/getting-started.html", "Getting started"},
	} {
		if got := fromHref(tt.href); got != tt.want {
			t.Errorf("fromHref(%q) = %q, want %q", tt.href, got, tt.want)
		}
	}
}

// TestFixRewritesTheTextInTwoPasses. The decision needs the text, which is not
// known until the end tag, and the content can only be replaced at the start
// tag - so the passes are not optional.
func TestFixRewritesTheTextInTwoPasses(t *testing.T) {
	in := `<a href="/annual-report-2024.pdf">click here</a>` +
		`<a href="/x" aria-label="Pricing">read more</a>` +
		`<a href="/y">Proper text</a>` +
		`<a href="/12345">more</a>`

	got, c, err := fixString(in)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, ">Annual report 2024<") {
		t.Errorf("the URL-derived replacement is missing: %s", got)
	}
	if !strings.Contains(got, ">Pricing<") {
		t.Errorf("the aria-label replacement is missing: %s", got)
	}
	if !strings.Contains(got, ">Proper text<") {
		t.Errorf("a good link was changed: %s", got)
	}
	if !strings.Contains(got, ">more<") {
		t.Errorf("a link with no better description should be left alone: %s", got)
	}
	if len(c.bad) != 3 {
		t.Errorf("bad=%d, want 3", len(c.bad))
	}
}

// TestFixReplacesNestedMarkupToo: the content is replaced, not the text, so
// markup inside a generic link goes with it. That is the intent - the whole
// point is that the link's content did not describe it.
func TestFixReplacesNestedMarkupToo(t *testing.T) {
	got, _, err := fixString(`<a href="/annual-report.pdf">click <b>here</b></a>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("markup inside the replaced link survived: %s", got)
	}
	if !strings.Contains(got, ">Annual report<") {
		t.Errorf("the replacement is missing: %s", got)
	}
}

// TestFixIsIdempotent: a fixed link is no longer generic, so a second run leaves
// it alone.
func TestFixIsIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := fixString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, c, err := fixString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		// The links with no available replacement are still reported, which is
		// correct: they are still bad, just unfixable.
		for _, o := range c.bad {
			if o.replacement != "" {
				t.Errorf("%q: a second pass still has a replacement for %q", doc, o.text)
			}
		}
	}
}

// TestReportOnlyModeWritesNoDocument.
func TestReportOnlyModeWritesNoDocument(t *testing.T) {
	c := &checker{}
	if err := c.pass(strings.NewReader(`<a href="/x">here</a>`), io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if len(c.bad) != 1 {
		t.Errorf("bad=%d, want 1", len(c.bad))
	}
}

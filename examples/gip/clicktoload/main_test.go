package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<iframe src="https://v.example/e"></iframe>`,
	`<iframe src="https://v.example/e" width="560" height="315"></iframe>`,
	`<iframe src="/local"></iframe>`,
	`<iframe src=""></iframe>`,
	`<iframe></iframe>`,
	`<iframe src="https://v.example/e" onload="alert(1)"></iframe>`,
	`<iframe src="https://v.example/e" data-junk="x"></iframe>`,
	`<iframe src="https://v.example/e" class="existing"></iframe>`,
	`<iframe src="https://v.example/e" title="A &quot;talk&quot;"></iframe>`,
	`<iframe title='" onload=alert(1) x="' src="https://v.example/e"></iframe>`,
	`<iframe srcdoc="<p>inline</p>"></iframe>`,
	`<iframe src="https://v.example/e">fallback text</iframe>`,
	`<div><iframe src="https://v.example/e"></iframe></div>`,
	`<p>no iframes</p>`,
	``,
}

func chunked(in string, n int, c *converter) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, c.options()...)
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

func newConverter() *converter {
	return &converter{label: "Click to load", keep: map[string]bool{}}
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := convertString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, newConverter())
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
		once, _, err := convertString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, c, err := convertString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if c.converted != 0 {
			t.Errorf("second pass of %q converted %d", doc, c.converted)
		}
	}
}

// TestNoMarkupIsBuiltByHand is the point of the design. A hostile attribute value
// must reach the placeholder as a value, never as markup - and the way to
// guarantee that is never to assemble a tag from strings.
func TestNoMarkupIsBuiltByHand(t *testing.T) {
	// A title that would close its own attribute and open an event handler if it
	// were written into hand-built markup.
	in := `<iframe title='" onload=alert(1) x="' src="https://v.example/e"></iframe>`

	got, c, err := convertString(in)
	if err != nil {
		t.Fatal(err)
	}
	if c.converted != 1 {
		t.Fatalf("converted=%d, want 1", c.converted)
	}

	// The title is re-emitted in its original single-quoted form, where the
	// double quotes are literal, so no attribute was created from it.
	if !strings.Contains(got, `title='" onload=alert(1) x="'`) {
		t.Errorf("the title was not re-emitted verbatim: %s", got)
	}
	// And nothing named onload exists as an attribute: the only "onload=" in the
	// output is inside a quoted value.
	if strings.Contains(got, `" onload=alert(1) x=""`) {
		t.Errorf("the payload became attributes: %s", got)
	}

	// And the src is carried as a value, through SetAttribute, which escapes it.
	if !strings.Contains(got, `data-ctl-src="https://v.example/e"`) {
		t.Errorf("the src was not carried as a value: %s", got)
	}
}

// TestEventHandlersDoNotSurviveTheRename. A tag rename keeps every attribute, so
// an onload written for an iframe would fire on the placeholder.
func TestEventHandlersDoNotSurviveTheRename(t *testing.T) {
	got, c, err := convertString(
		`<iframe src="https://v.example/e" onload="alert(1)" ONERROR="x()"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(got), "onload") ||
		strings.Contains(strings.ToLower(got), "onerror") {
		t.Errorf("a handler survived: %s", got)
	}
	if c.handlers != 2 {
		t.Errorf("handlers=%d, want 2", c.handlers)
	}

	for _, tt := range []struct {
		name    string
		handler bool
	}{
		{"onload", true}, {"onerror", true}, {"onfuturething", true},
		{"on", false}, {"on-load", false}, {"data-on", false}, {"title", false},
	} {
		if got := isEventHandler(tt.name); got != tt.handler {
			t.Errorf("isEventHandler(%q) = %v, want %v", tt.name, got, tt.handler)
		}
	}
}

// TestOnlyUsefulAttributesAreCarried: the placeholder needs what sizes it and
// what a script restoring the iframe will read, and nothing else.
func TestOnlyUsefulAttributesAreCarried(t *testing.T) {
	got, _, err := convertString(
		`<iframe src="https://v.example/e" width="560" height="315" title="T" ` +
			`allow="autoplay" data-junk="x" frameborder="0"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`width="560"`, `height="315"`, `title="T"`, `allow="autoplay"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s was dropped: %s", want, got)
		}
	}
	for _, unwanted := range []string{"data-junk", "frameborder"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%s was carried over: %s", unwanted, got)
		}
	}
	// " src=" with the leading space, so data-ctl-src does not match it.
	if strings.Contains(got, ` src="`) {
		t.Errorf("the src survived, so the embed still loads: %s", got)
	}
}

func TestSameOriginIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<iframe src="/local"></iframe>`,
		`<iframe src="page.html"></iframe>`,
		`<iframe src="https://keep.example/x"></iframe>`,
	} {
		got, c, err := convertString(in, func(c *converter) { c.keep["keep.example"] = true })
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was converted:\n got: %s", in, got)
		}
		if c.converted != 0 {
			t.Errorf("%s: converted=%d, want 0", in, c.converted)
		}
	}

	// With -all, they are converted.
	got, c, err := convertString(`<iframe src="/local"></iframe>`,
		func(c *converter) { c.all = true })
	if err != nil {
		t.Fatal(err)
	}
	if c.converted != 1 || !strings.Contains(got, "click-to-load") {
		t.Errorf("-all did not convert a relative src: %s", got)
	}
}

// TestSrclessIframesAreLeftAlone: an iframe with no src loads nothing, so there
// is nothing to defer, and converting it would remove content for no gain.
func TestSrclessIframesAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<iframe></iframe>`,
		`<iframe src=""></iframe>`,
		`<iframe src="   "></iframe>`,
	} {
		got, c, err := convertString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was converted: %s", in, got)
		}
		if c.converted != 0 {
			t.Errorf("%s: converted=%d, want 0", in, c.converted)
		}
	}
}

func TestExistingClassIsKept(t *testing.T) {
	got, _, err := convertString(`<iframe src="https://v.example/e" class="a b"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `class="a b click-to-load"`) {
		t.Errorf("classes were not merged in order: %s", got)
	}
}

// TestTheLabelIsInsertedAsText, so a label containing markup cannot become
// markup.
func TestTheLabelIsInsertedAsText(t *testing.T) {
	got, _, err := convertString(`<iframe src="https://v.example/e"></iframe>`,
		func(c *converter) { c.label = `<script>alert(1)</script>` })
	if err != nil {
		t.Fatal(err)
	}
	// Escaped into element content, where it is text rather than a script.
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the label was not escaped into the content: %s", got)
	}
	// And it is the content, not a second element: no start tag was created.
	if strings.Contains(got, "<script>") {
		t.Errorf("the label became markup: %s", got)
	}
}

// TestFallbackContentIsReplaced: whatever was inside the iframe was fallback for
// browsers that could not render it, and the placeholder's own label replaces it.
func TestFallbackContentIsReplaced(t *testing.T) {
	got, _, err := convertString(`<iframe src="https://v.example/e">old fallback</iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "old fallback") {
		t.Errorf("the fallback survived alongside the label: %s", got)
	}
	if !strings.Contains(got, ">Click to load<") {
		t.Errorf("the label is missing: %s", got)
	}
}

func TestHostOf(t *testing.T) {
	for _, tt := range []struct{ src, want string }{
		{"https://v.example/e", "v.example"},
		{"https://V.EXAMPLE/e", "v.example"},
		{"//v.example/e", "v.example"},
		{"/local", ""},
		{"page.html", ""},
		{"", ""},
		{"https://v.example:8080/e", "v.example"},
		{"https://v.example/e?a=1&amp;b=2", "v.example"},
	} {
		if got := hostOf(tt.src); got != tt.want {
			t.Errorf("hostOf(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

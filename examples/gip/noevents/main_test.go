package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<img src=x onerror=alert(1)>`,
	`<div ONCLICK="x()">t</div>`,
	`<div onfuturething="y()">t</div>`,
	`<div on-click="data">t</div>`,
	`<div data-on="k">t</div>`,
	`<div on="k">t</div>`,
	`<a href="javascript:x()">j</a>`,
	`<a href=" JavaScript:x()">j</a>`,
	"<a href=\"java\tscript:x()\">j</a>",
	`<a href="java&#9;script:x()">j</a>`,
	`<a href="&#106;avascript:x()">j</a>`,
	`<a href="/safe">ok</a>`,
	`<a href="https://example.com/x">ok</a>`,
	`<form action="javascript:x()"><input formaction="javascript:y()"></form>`,
	`<body onload="x()" onunload="y()">t</body>`,
	`<div onclick="a" onclick="b">duplicate</div>`,
	`<p>nothing to do</p>`,
	``,
}

func chunked(in string, n int, s *sanitiser) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, s.options()...)
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
		whole, _, err := sanitiseString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &sanitiser{stripURLs: true})
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
		once, _, err := sanitiseString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, s, err := sanitiseString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if s.total() != 0 {
			t.Errorf("second pass of %q removed %d thing(s)", doc, s.total())
		}
	}
}

// TestEncodedSchemesAreCaught is the bug this program had. A browser decodes an
// attribute value before it looks at the scheme, so a check on the raw string
// lets the encoded forms through - and each of these executes.
func TestEncodedSchemesAreCaught(t *testing.T) {
	for _, href := range []string{
		`javascript:x()`,
		`JavaScript:x()`,
		` javascript:x()`,
		"java\tscript:x()",
		`java&#9;script:x()`,
		`java&Tab;script:x()`,
		`&#106;avascript:x()`,
		`&#x6a;avascript:x()`,
		`&#106;&#97;vascript:x()`,
	} {
		in := `<a href="` + href + `">t</a>`
		got, s, err := sanitiseString(in)
		if err != nil {
			t.Fatalf("%s: %v", href, err)
		}
		if s.urls != 1 {
			t.Errorf("%q was not recognised as a script URL: %s", href, got)
		}
		if strings.Contains(got, "href=") {
			t.Errorf("%q survived: %s", href, got)
		}
	}
}

// TestSafeURLsAreLeftExactlyAsTheyWere, including the encoded ampersand, because
// rewriting a value that was fine is how a filter breaks a page.
func TestSafeURLsAreLeftExactlyAsTheyWere(t *testing.T) {
	for _, in := range []string{
		`<a href="/safe">t</a>`,
		`<a href="https://example.com/x?a=1&amp;b=2">t</a>`,
		`<a href="#frag">t</a>`,
		`<a href="mailto:a@b.c">t</a>`,
		`<a href="notjavascript:x">t</a>`,
		`<a href="/x?q=javascript:x()">t</a>`,
		`<img src="data:image/png;base64,AA">`,
	} {
		got, s, err := sanitiseString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was changed:\n got: %s", in, got)
		}
		if s.total() != 0 {
			t.Errorf("%s: removed %d, want 0", in, s.total())
		}
	}
}

// TestHandlersAreMatchedByShapeNotByList: a browser dispatches any "on*"
// attribute, including ones added to the platform after any list was written.
func TestHandlersAreMatchedByShapeNotByList(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler bool
	}{
		{"onclick", true},
		{"onerror", true},
		{"onload", true},
		{"onfuturething", true},
		{"onx", true},
		{"ONCLICK", true},
		{"on", false},
		{"on-click", false},
		{"onclick2", false},
		{"data-on", false},
		{"one", true},
		{"only", true},
		{"href", false},
	} {
		if got := isEventHandler(strings.ToLower(tt.name)); got != tt.handler {
			t.Errorf("isEventHandler(%q) = %v, want %v", tt.name, got, tt.handler)
		}
	}

	// And end to end, with the case the document used.
	got, s, err := sanitiseString(`<div ONCLICK="a" onFutureThing="b" on-click="c">t</div>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(got), "onclick=") ||
		strings.Contains(strings.ToLower(got), "onfuturething=") {
		t.Errorf("a handler survived: %s", got)
	}
	if !strings.Contains(got, `on-click="c"`) {
		t.Errorf("a non-handler attribute was removed: %s", got)
	}
	if s.handlers["onclick"] != 1 || s.handlers["onfuturething"] != 1 {
		t.Errorf("handlers = %v", s.handlers)
	}
}

// TestEveryCopyOfADuplicateHandlerGoes. A browser reads the first, so leaving
// any copy behind leaves the handler in force.
func TestEveryCopyOfADuplicateHandlerGoes(t *testing.T) {
	got, _, err := sanitiseString(`<div onclick="a" onclick="b" onclick="c">t</div>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "onclick") {
		t.Errorf("a copy survived: %s", got)
	}
	if !strings.Contains(got, ">t<") {
		t.Errorf("the content was lost: %s", got)
	}
}

// TestStructureIsUntouched: this is an attribute-only rewrite, so nothing moves.
// That is what makes it safe to run over content nobody controls.
func TestStructureIsUntouched(t *testing.T) {
	for _, doc := range corpus {
		got, _, err := sanitiseString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		// Tag counts are a cheap structural proxy, and enough here: the rewrite
		// inserts nothing and removes no element, so every tag must survive.
		for _, tag := range []string{"<div", "<a", "<img", "<form", "<input", "<p", "<body"} {
			if strings.Count(got, tag) != strings.Count(doc, tag) {
				t.Errorf("%q: the count of %q changed\n got: %s", doc, tag, got)
			}
		}
		if strings.Count(got, "</") != strings.Count(doc, "</") {
			t.Errorf("%q: the end tag count changed\n got: %s", doc, got)
		}
	}
}

func TestKeepJavaScriptURLs(t *testing.T) {
	in := `<a href="javascript:x()" onclick="y()">t</a>`
	got, s, err := sanitiseString(in, func(s *sanitiser) { s.stripURLs = false })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `href="javascript:x()"`) {
		t.Errorf("the URL should have been kept: %s", got)
	}
	if strings.Contains(got, "onclick") {
		t.Errorf("the handler should still go: %s", got)
	}
	if s.urls != 0 {
		t.Errorf("urls=%d, want 0", s.urls)
	}
}

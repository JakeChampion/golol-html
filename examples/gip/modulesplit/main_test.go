package main

import (
	stdhtml "html"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = `<html><head>` +
	`<script src="/a.js?x=1&amp;y=2" data-id="k"></script>` +
	`<script type="module" src="/b.mjs"></script>` +
	`<script src="/c.js" nomodule></script>` +
	`<script src="/noext"></script>` +
	`</head><body>x</body></html>`

var corpus = []string{
	page,
	`<html><head><script src="/a.js"></script></head><body>x</body></html>`,
	`<html><head><script src="/a.js" type="text/javascript"></script></head><body>x</body></html>`,
	`<html><head><script src="/a.js" type="text/template"></script></head><body>x</body></html>`,
	`<html><head><script src=""></script></head><body>x</body></html>`,
	`<html><head><script>inline()</script></head><body>x</body></html>`,
	`<html><head><script src="/a.mjs"></script></head><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// pairs returns each script as "src|type|nomodule".
func pairs(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			src, _ := e.Attribute("src")
			typ, _ := e.Attribute("type")
			_, nomodule := e.Attribute("nomodule")
			out = append(out, src+"|"+typ+"|"+map[bool]string{true: "nomodule", false: "-"}[nomodule])
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(in string, n int, opts ...func(*splitter)) (string, *splitter, error) {
	s := defaults()
	for _, o := range opts {
		o(s)
	}
	if err := s.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, s.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), s, nil
}

func TestChunkInvariance(t *testing.T) {
	for _, in := range corpus {
		whole, _, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, n := range []int{1, 2, 3, 31} {
			got, _, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, in, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range corpus {
		once, _, err := splitString(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		twice, s, err := splitString(once)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", in, once, twice)
		}
		if s.split != 0 {
			t.Errorf("the second pass of %q split %d", in, s.split)
		}
	}
}

// TestThePairIsExact. A browser that ran both halves would run the page's code
// twice, so exactly one is a module and exactly one is nomodule.
func TestThePairIsExact(t *testing.T) {
	out, s, err := splitString(
		`<html><head><script src="/a.js" data-id="k"></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if s.split != 1 {
		t.Fatalf("split=%d, want 1", s.split)
	}
	got := pairs(t, out)
	want := []string{
		"/a.mjs|module|-",
		"/a.js||nomodule",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	// The module comes first in source order.
	if strings.Index(out, "module") > strings.Index(out, "nomodule") {
		t.Errorf("the nomodule half is first: %s", out)
	}
	// Other attributes are copied to both.
	if n := strings.Count(out, `data-id="k"`); n != 2 {
		t.Errorf("data-id appears %d times, want 2: %s", n, out)
	}
}

// TestARawSourceValueSurvivesTheCopy. Everything the library reports is raw
// attribute source, so escaping it directly would double-escape and writing it
// back raw can let an ampersand form a reference. Both halves have to read back
// as the url the page wrote.
func TestARawSourceValueSurvivesTheCopy(t *testing.T) {
	for _, src := range []string{
		`/a.js?x=1&amp;y=2`,
		`/a.js?a=1&amp;lt=2`,
		`/a.js?a=1&amp;amp;b`,
		`/a.js?a=1&y=2`,
		`/a.js?q=a+b`,
	} {
		doc := `<html><head><script src="` + src + `"></script></head><body>x</body></html>`
		out, _, err := splitString(doc)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}

		want := stdhtml.UnescapeString(src)
		for _, p := range pairs(t, out) {
			fields := strings.SplitN(p, "|", 3)
			got := stdhtml.UnescapeString(fields[0])
			// The module half has its extension swapped; compare the query.
			gotQuery := got[strings.Index(got, "?"):]
			wantQuery := want[strings.Index(want, "?"):]
			if gotQuery != wantQuery {
				t.Errorf("%s: a half reads back as %q, want the query %q",
					src, got, wantQuery)
			}
		}
	}
}

// TestTheModuleURLIsDerivedNotGuessed.
func TestModuleURL(t *testing.T) {
	s := defaults()
	for _, tt := range []struct {
		src, want string
		ok        bool
	}{
		{"/a.js", "/a.mjs", true},
		{"/a/b.js", "/a/b.mjs", true},
		{"/a.min.js", "/a.min.mjs", true},
		{"/a.js?v=1", "/a.mjs?v=1", true},
		{"/a.js#f", "/a.mjs#f", true},
		{"https://cdn.example/a.js", "https://cdn.example/a.mjs", true},

		{"/noext", "", false},
		{"/dir/", "", false},
		{"/a.mjs", "", false}, // already the module build
		{"", "", false},
	} {
		got, ok := s.moduleURL(tt.src)
		if ok != tt.ok || got != tt.want {
			t.Errorf("moduleURL(%q) = %q/%v, want %q/%v", tt.src, got, ok, tt.want, tt.ok)
		}
	}
}

// TestWhatIsLeftAlone.
func TestWhatIsLeftAlone(t *testing.T) {
	for _, tt := range []struct {
		name, markup string
		want         bool
	}{
		{"a classic script", `<script src="/a.js"></script>`, true},
		{"an explicit type", `<script src="/a.js" type="text/javascript"></script>`, true},

		{"a module", `<script type="module" src="/a.js"></script>`, false},
		{"a nomodule half", `<script src="/a.js" nomodule></script>`, false},
		{"a template", `<script src="/a.js" type="text/template"></script>`, false},
		{"inline", `<script>x()</script>`, false},
		{"an empty src", `<script src=""></script>`, false},
		{"no extension", `<script src="/noext"></script>`, false},
	} {
		doc := `<html><head>` + tt.markup + `</head><body>x</body></html>`
		_, s, err := splitString(doc)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := s.split == 1; got != tt.want {
			t.Errorf("%s: split=%v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestTheFallbackGetsDefer, because nomodule does not imply it and a fallback
// that blocks parsing defeats the point.
func TestTheFallbackGetsDefer(t *testing.T) {
	out, _, err := splitString(
		`<html><head><script src="/a.js"></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	var deferred int
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("script[nomodule]", func(e *lolhtml.Element) error {
			if _, ok := e.Attribute("defer"); ok {
				deferred++
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if deferred != 1 {
		t.Errorf("the fallback has no defer: %s", out)
	}

	// And it can be turned off.
	out, _, err = splitString(
		`<html><head><script src="/a.js"></script></head><body>x</body></html>`,
		func(s *splitter) { s.addDefer = false })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "defer") {
		t.Errorf("-defer=false still added it: %s", out)
	}
}

// TestTheCopyKeepsTheSourceSpelling. The copy is built by hand, so it chooses
// between Attribute.Name, which is lower-cased, and NamePreserveCase, which is
// what the page wrote. It uses the latter: the original passes through with its
// own spelling, and a copy that differed would make the pair look like two
// different elements in a diff.
func TestTheCopyKeepsTheSourceSpelling(t *testing.T) {
	out, _, err := splitString(
		`<html><head><script src="/a.js" dataFoo="1"></script></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, `dataFoo="1"`); n != 2 {
		t.Errorf(`dataFoo="1" appears %d times, want 2 - once per half: %s`, n, out)
	}
	if strings.Contains(out, `datafoo=`) {
		t.Errorf("the copy lower-cased the name: %s", out)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*splitter)
	}{
		{"an empty suffix", func(s *splitter) { s.suffix = "" }},
		{"a suffix with no dot", func(s *splitter) { s.suffix = "mjs" }},
	} {
		if _, _, err := splitString(page, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

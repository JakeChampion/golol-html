package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func always(m *marker) { m.always = true }

var corpus = []string{
	`<!DOCTYPE html><html><head><title>t</title></head><body>x</body></html>`,
	`<html><head><meta name="robots" content="nofollow"></head><body>x</body></html>`,
	`<html><head><meta name="ROBOTS" content="NOFOLLOW, all"></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<html><head></head><body>x</body></html>`,
	`<html><body><meta name="robots" content="all"></body></html>`,
	`<html><head><meta name="description" content="d"></head><body>x</body></html>`,
	`<p>fragment</p>`,
	`<!DOCTYPE html><p>no head</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*marker)) (string, error) {
	m := defaults()
	for _, o := range opts {
		o(m)
	}
	if err := m.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, m.options()...)
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
		whole, _, err := markString(doc, always)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 37} {
			got, err := chunked(doc, n, always)
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
		once, _, err := markString(doc, always)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, m, err := markString(once, always)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if m.marked != 0 {
			t.Errorf("the second pass of %q inserted %d", doc, m.marked)
		}
	}
}

// TestExactlyOneRobotsMetaInTheHead is the invariant that matters: two of them is
// not twice the instruction, because a crawler may take either.
func TestExactlyOneRobotsMetaInTheHead(t *testing.T) {
	for _, doc := range corpus {
		out, _, err := markString(doc, always)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}

		// Count what a parser would see, not what the string contains.
		var robots []string
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnElement("meta[name]", func(e *lolhtml.Element) error {
				if strings.EqualFold(strings.TrimSpace(attr(e, "name")), "robots") {
					robots = append(robots, attr(e, "content"))
				}
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if len(robots) > 1 {
			// The one exception is a robots meta the input had in its body,
			// which this program leaves alone by design.
			if !strings.Contains(doc, "<body><meta") {
				t.Errorf("%q -> %q has %d robots metas: %v", doc, out, len(robots), robots)
			}
		}
	}
}

// TestAnExistingRobotsMetaIsRewrittenNotJoined, and the union keeps what was
// there: a page already asking for nofollow keeps asking for it.
func TestAnExistingRobotsMetaIsRewrittenNotJoined(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`<html><head><meta name="robots" content="nofollow"></head><body>x</body></html>`,
			"nofollow, noindex"},
		{`<html><head><meta name="ROBOTS" content="NOFOLLOW"></head><body>x</body></html>`,
			"nofollow, noindex"},
		{`<html><head><meta name="robots" content="noindex"></head><body>x</body></html>`,
			"noindex"},
		{`<html><head><meta name="robots" content=""></head><body>x</body></html>`,
			"noindex"},
		{`<html><head><meta name="robots" content="all, noarchive"></head><body>x</body></html>`,
			"all, noarchive, noindex"},
		{`<html><head><meta name=" robots " content="a"></head><body>x</body></html>`,
			"a, noindex"},
	} {
		got, m, err := markString(tt.in, always)
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if m.rewrote != 1 || m.marked != 0 {
			t.Errorf("%q: rewrote=%d inserted=%d, want 1 and 0", tt.in, m.rewrote, m.marked)
		}
		if !strings.Contains(got, `content="`+tt.want+`"`) {
			t.Errorf("%q -> %q, want content %q", tt.in, got, tt.want)
		}
	}
}

// TestWithNoHeadElementTheMetaGoesBeforeBody, which is where the implied head
// ends. differential/head_test.go checks with an independent parser that the
// result really is in the head.
func TestWithNoHeadElementTheMetaGoesBeforeBody(t *testing.T) {
	got, m, err := markString(`<html><body>x</body></html>`, always)
	if err != nil {
		t.Fatal(err)
	}
	if m.marked != 1 {
		t.Fatalf("inserted=%d, want 1", m.marked)
	}
	if got != `<html><meta name="robots" content="noindex"><body>x</body></html>` {
		t.Errorf("unexpected placement: %s", got)
	}
}

// TestARobotsMetaInTheBodyIsLeftAlone: it is not the one a crawler reads for the
// document, and the head has this program's.
func TestARobotsMetaInTheBodyIsLeftAlone(t *testing.T) {
	got, m, err := markString(`<html><body><meta name="robots" content="all"></body></html>`, always)
	if err != nil {
		t.Fatal(err)
	}
	if m.marked != 1 || m.rewrote != 0 || total(m.skipped) != 1 {
		t.Errorf("inserted=%d rewrote=%d skipped=%v", m.marked, m.rewrote, m.skipped)
	}
	if !strings.Contains(got, `<body><meta name="robots" content="all">`) {
		t.Errorf("the body meta was changed: %s", got)
	}
}

// TestADocumentWithNoHeadAndNoBodyIsReported rather than guessed at. Inserting
// before the first element would usually work, and "usually" is the problem: text
// before that element has already opened the body, and a robots meta later in the
// fragment cannot be known about yet. Both are pinned in differential/head_test.go.
func TestADocumentWithNoHeadAndNoBodyIsReported(t *testing.T) {
	for _, in := range []string{`<p>fragment</p>`, `<!DOCTYPE html><p>x</p>`, `text only`, ``} {
		got, m, err := markString(in, always)
		if err != nil {
			t.Fatal(err)
		}
		if m.marked != 0 || m.rewrote != 0 {
			t.Errorf("%q: inserted=%d rewrote=%d", in, m.marked, m.rewrote)
		}
		if got != in {
			t.Errorf("%q changed: %q", in, got)
		}
		if total(m.skipped) != 1 {
			t.Errorf("%q: skipped=%v, want one reason", in, m.skipped)
		}
	}
}

// TestOnlyMatchingPathsAreMarked: the point of the tool is to mark some pages.
func TestOnlyMatchingPathsAreMarked(t *testing.T) {
	const doc = `<html><head></head><body>x</body></html>`
	for _, tt := range []struct {
		patterns []string
		url      string
		want     bool
	}{
		{[]string{"/search*"}, "/search", true},
		{[]string{"/search*"}, "/search?q=x", true},
		{[]string{"/search*"}, "/searching", true},
		{[]string{"/search/*"}, "/search/x", true},
		{[]string{"/search/*"}, "/search", false},
		{[]string{"/print/*"}, "/print/1", true},
		{[]string{"/print/*"}, "/a/print/1", false},
		{[]string{"/*"}, "/anything", true},
		{[]string{"/*"}, "/a/b", false}, // Match does not cross a separator
		{[]string{"/search*"}, "https://x.example/search?q=1", true},
		{[]string{"/search*"}, "https://x.example/other", false},
		{[]string{"/search*"}, "/other", false},
		{[]string{"/search*", "/print/*"}, "/print/2", true},
	} {
		_, m, err := markString(doc, func(m *marker) {
			m.patterns = tt.patterns
			m.url = tt.url
		})
		if err != nil {
			t.Fatalf("%v %q: %v", tt.patterns, tt.url, err)
		}
		if got := m.marked == 1; got != tt.want {
			t.Errorf("patterns %v url %q: marked=%v, want %v", tt.patterns, tt.url, got, tt.want)
		}
	}
}

func TestUnion(t *testing.T) {
	for _, tt := range []struct{ have, want, out string }{
		{"", "noindex", "noindex"},
		{"nofollow", "noindex", "nofollow, noindex"},
		{"noindex", "noindex", "noindex"},
		{"NOINDEX", "noindex", "noindex"},
		{" a , b ", "noindex", "a, b, noindex"},
		{"a,a,b", "noindex", "a, b, noindex"},
		{"&amp;", "noindex", "&, noindex"},
		{",,,", "noindex", "noindex"},
		{"noindex, nofollow", "noindex, nofollow", "noindex, nofollow"},
	} {
		if got := union(tt.have, tt.want); got != tt.out {
			t.Errorf("union(%q, %q) = %q, want %q", tt.have, tt.want, got, tt.out)
		}
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*marker)
	}{
		{"no patterns and not always", func(m *marker) { m.url = "/x" }},
		{"patterns but no url", func(m *marker) { m.patterns = []string{"/x*"} }},
		{"a malformed pattern", func(m *marker) {
			m.patterns = []string{"[a-"}
			m.url = "/x"
		}},
	} {
		if _, _, err := markString(`<html><head></head><body>x</body></html>`, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

// TestTheContentIsEscaped: the meta is assembled as markup, and -no-follow
// changes what goes in it.
func TestTheContent(t *testing.T) {
	got, _, err := markString(`<html><head></head><body>x</body></html>`, always,
		func(m *marker) { m.noFollow = true })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `<meta name="robots" content="noindex, nofollow">`) {
		t.Errorf("unexpected meta: %s", got)
	}
}

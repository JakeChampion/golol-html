package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const trail = `<html><body><nav class="breadcrumb">` +
	`<a href="/">Home</a> / <a href="/docs/">Docs</a> / <span>Current &amp; page</span>` +
	`</nav><p>x</p></body></html>`

func based(b *builder) { b.base = "https://example.com/" }

var corpus = []string{
	trail,
	`<html><body><nav class="breadcrumb"><a href="/a">A</a></nav></body></html>`,
	`<html><body><nav class="breadcrumb"></nav></body></html>`,
	`<html><body><nav aria-label="breadcrumb"><a href="/a">A</a></nav></body></html>`,
	`<html><body><div class="breadcrumbs"><a href="/a">A</a></div></body></html>`,
	`<html><body><nav class="breadcrumb"><a href="/a">A</a></nav><nav class="breadcrumb"><a href="/b">B</a></nav></body></html>`,
	`<html><body><nav class="breadcrumb"><a href="/a">   </a><a href="/b">B</a></nav></body></html>`,
	`<html><body><p>no breadcrumb</p></body></html>`,
	``,
}

// scripts returns the ld+json blocks a parser finds, with their text.
func scripts(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	in := false
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`script[type="application/ld+json"]`, func(e *lolhtml.Element) error {
			cur.Reset()
			in = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				in = false
				out = append(out, cur.String())
				return nil
			})
		}),
		lolhtml.OnText(`script[type="application/ld+json"]`, func(tc *lolhtml.TextChunk) error {
			if in {
				cur.WriteString(tc.Text())
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func chunked(in string, n int, opts ...func(*builder)) (string, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	if err := b.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, b.options()...)
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
		whole, _, err := buildString(doc, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 53} {
			got, err := chunked(doc, n, based)
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

// TestIdempotent: a second pass must not add a second BreadcrumbList.
//
// The mechanism is worth stating because the obvious one does not work. The
// script is emitted after the nav's end tag, and a previous run's script is
// further on again, so at the moment of deciding there is nothing to look at.
// What stops it is a marker attribute on the nav, set at its start tag.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := buildString(doc, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, _, err := buildString(once, based)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if n := len(scripts(t, twice)); n > 1 {
			t.Errorf("%q produced %d BreadcrumbLists after two passes:\n%s", doc, n, twice)
		}
	}
}

// TestTheListIsBuiltFromTheNav.
func TestTheListIsBuiltFromTheNav(t *testing.T) {
	out, b, err := buildString(trail, based)
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 1 || len(b.items) != 3 {
		t.Fatalf("emitted=%d items=%d, want 1 and 3", b.emitted, len(b.items))
	}
	got := scripts(t, out)
	if len(got) != 1 {
		t.Fatalf("%d scripts, want 1: %s", len(got), out)
	}
	for _, want := range []string{
		`"@type":"BreadcrumbList"`,
		`"position":1,"name":"Home","item":"https:\/\/example.com\/"`,
		`"position":2,"name":"Docs","item":"https:\/\/example.com\/docs\/"`,
		`"position":3,"name":"Current & page"`,
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the JSON does not contain %s:\n%s", want, got[0])
		}
	}
	// The last crumb has no href, so it has no item member.
	if strings.Count(got[0], `"item":`) != 2 {
		t.Errorf("expected two item members: %s", got[0])
	}
}

// TestTheScriptGoesAfterTheNav, which is the position that needs no second pass:
// by the nav's end tag every crumb has been seen.
func TestTheScriptGoesAfterTheNav(t *testing.T) {
	out, _, err := buildString(trail, based)
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(out, "</nav>"), strings.Index(out, "ld+json")
	if i < 0 || j < 0 || j < i {
		t.Errorf("the script is not after the nav: %s", out)
	}
}

// TestSlashesAreEscapedForTheElementNotForJSON. JSON does not require "\/"; the
// element the JSON lands in does, and the library refuses the unescaped form.
func TestSlashesAreEscapedForTheElementNotForJSON(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`/`, `"\/"`},
		{`a/b`, `"a\/b"`},
		{`</script>`, `"<\/script>"`},
		{`"quoted"`, `"\"quoted\""`},
		{`back\slash`, `"back\\slash"`},
		{"tab\there", `"tab\there"`},
		{"nl\nhere", `"nl\nhere"`},
		{"\x00\x1f", `"\u0000\u001f"`},
		{`plain`, `"plain"`},
		{"caf\u00e9 \u65e5", "\"caf\u00e9 \u65e5\""},
		{"bad\xffutf8", `"bad` + "\ufffd" + `utf8"`},
	} {
		if got := jsonString(tt.in); got != tt.want {
			t.Errorf("jsonString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestAnUnescapedSlashWouldBeRefused is the safety net, exercised through the
// placeholder path - the only one the library can check, because inserting a
// whole <script> element legitimately contains its own closing tag.
func TestAnUnescapedSlashWouldBeRefused(t *testing.T) {
	const doc = `<html><body><nav class="breadcrumb"><a href="/a">x</a></nav>` +
		`<script type="application/ld+json" data-breadcrumb></script></body></html>`

	// What the program does produce is accepted.
	if _, _, err := buildString(doc, func(b *builder) { b.placeholder = true }); err != nil {
		t.Fatalf("the escaped form was refused: %v", err)
	}

	// And the unescaped form, which an earlier version of jsonString would have
	// produced, is not.
	_, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(placeholderSelector, func(e *lolhtml.Element) error {
			return e.SetInnerContent(`{"item":"</script><img src=1>"}`, lolhtml.HTML)
		}))
	if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
		t.Errorf("the unescaped form was accepted: %v", err)
	}
}

// TestThePlaceholderIsAnExplicitChoice. The nav's end tag comes before a
// placeholder that follows it, so a program that inserted there and then found
// one would have emitted two.
func TestThePlaceholderIsAnExplicitChoice(t *testing.T) {
	const doc = `<html><body><nav class="breadcrumb"><a href="/a">x</a></nav>` +
		`<script type="application/ld+json" data-breadcrumb></script></body></html>`

	// Without the flag, the placeholder is left empty and a script is inserted.
	out, b, err := buildString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 1 {
		t.Errorf("emitted=%d, want 1", b.emitted)
	}
	if n := len(scripts(t, out)); n != 2 {
		t.Errorf("%d scripts, want 2 - one filled, the placeholder still empty: %s", n, out)
	}

	// With it, the placeholder is filled and nothing is inserted.
	out, b, err = buildString(doc, func(b *builder) { b.placeholder = true })
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 1 {
		t.Errorf("emitted=%d, want 1", b.emitted)
	}
	if n := len(scripts(t, out)); n != 1 {
		t.Errorf("%d scripts, want 1: %s", n, out)
	}
}

// TestAPlaceholderBeforeTheNavIsReported, because the position has been passed by
// the time there is anything to put in it.
func TestAPlaceholderBeforeTheNavIsReported(t *testing.T) {
	const doc = `<html><head><script type="application/ld+json" data-breadcrumb></script></head>` +
		`<body><nav class="breadcrumb"><a href="/a">x</a></nav></body></html>`
	out, b, err := buildString(doc, func(b *builder) { b.placeholder = true })
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 0 || total(b.skipped) != 1 {
		t.Errorf("emitted=%d skipped=%v", b.emitted, b.skipped)
	}
	if len(scripts(t, out)) != 1 || scripts(t, out)[0] != "" {
		t.Errorf("the placeholder was filled anyway: %s", out)
	}
}

// TestDescendantsExtendsEveryAlternative is the bug this program shipped first:
// -selector is a list, and appending " a" to the string extends only its last
// alternative, which silently made the nav itself match the crumb handler.
func TestDescendants(t *testing.T) {
	for _, tt := range []struct{ list, want string }{
		{"nav.b", "nav.b a, nav.b span"},
		{"nav.b, .c", "nav.b a, nav.b span, .c a, .c span"},
		{" nav.b , .c ", "nav.b a, nav.b span, .c a, .c span"},
		{"nav.b,", "nav.b a, nav.b span"},
	} {
		if got := descendants(tt.list, "a", "span"); got != tt.want {
			t.Errorf("descendants(%q) = %q, want %q", tt.list, got, tt.want)
		}
	}

	// And the effect: with a two-alternative list, the second one's crumbs are
	// still found.
	out, b, err := buildString(
		`<html><body><div class="breadcrumbs"><a href="/a">A</a><a href="/b">B</a></div></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.items) != 2 {
		t.Errorf("items=%d, want 2: %s", len(b.items), out)
	}
}

// TestOnlyOneNavIsUsed: a footer duplicate is more likely than a second trail.
func TestOnlyOneNavIsUsed(t *testing.T) {
	out, b, err := buildString(
		`<html><body><nav class="breadcrumb"><a href="/a">A</a></nav>` +
			`<nav class="breadcrumb"><a href="/b">B</a></nav></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 1 || total(b.skipped) != 1 {
		t.Errorf("emitted=%d skipped=%v", b.emitted, b.skipped)
	}
	if n := len(scripts(t, out)); n != 1 {
		t.Errorf("%d scripts, want 1: %s", n, out)
	}
}

// TestAnEmptyCrumbIsSkipped, and an empty nav emits nothing at all.
func TestEmptyThings(t *testing.T) {
	out, b, err := buildString(
		`<html><body><nav class="breadcrumb"><a href="/a">   </a><a href="/b">B</a></nav></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.items) != 1 {
		t.Errorf("items=%d, want 1: %s", len(b.items), out)
	}

	_, b, err = buildString(`<html><body><nav class="breadcrumb"></nav></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 0 || total(b.skipped) != 1 {
		t.Errorf("emitted=%d skipped=%v", b.emitted, b.skipped)
	}
}

// TestWhitespaceInNamesIsCollapsed, because breadcrumb markup is indented.
func TestSquash(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"  Home  ", "Home"},
		{"A\n\t  B", "A B"},
		{"", ""},
		{"   ", ""},
		{"one", "one"},
	} {
		if got := squash(tt.in); got != tt.want {
			t.Errorf("squash(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestARelativeCrumbIsDroppedWhenItCannotBeResolved rather than emitted relative.
func TestResolve(t *testing.T) {
	b := defaults()
	b.base = "https://example.com/docs/"
	for _, tt := range []struct{ in, want string }{
		{"", ""},
		{"/a", "https://example.com/a"},
		{"b", "https://example.com/docs/b"},
		{"https://other.example/c", "https://other.example/c"},
		{"//other.example/d", "https://other.example/d"},
	} {
		if got := b.resolve(tt.in); got != tt.want {
			t.Errorf("resolve(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// With no base, hrefs are left as they are.
	plain := defaults()
	if got := plain.resolve("/a"); got != "/a" {
		t.Errorf("resolve with no base = %q", got)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*builder)
	}{
		{"empty selector", func(b *builder) { b.selector = "" }},
		{"zero max", func(b *builder) { b.maxItems = 0 }},
		{"relative base", func(b *builder) { b.base = "/docs/" }},
		{"unparseable base", func(b *builder) { b.base = "http://[::1" }},
	} {
		if _, _, err := buildString(trail, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

func TestMaxItemsIsHonoured(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<html><body><nav class="breadcrumb">`)
	for i := 0; i < 30; i++ {
		sb.WriteString(`<a href="/a">A</a>`)
	}
	sb.WriteString(`</nav></body></html>`)

	_, b, err := buildString(sb.String(), func(b *builder) { b.maxItems = 5 })
	if err != nil {
		t.Fatal(err)
	}
	if len(b.items) != 5 {
		t.Errorf("items=%d, want 5", len(b.items))
	}
	if total(b.skipped) == 0 {
		t.Error("the truncation was not reported")
	}
}

// TestTheMarkerGoesOnTheNav, and is what makes a second run a no-op. It is
// asserted separately because it is a visible change to the document, made for
// the benefit of the next run rather than of the page.
func TestTheMarkerGoesOnTheNav(t *testing.T) {
	out, _, err := buildString(trail, based)
	if err != nil {
		t.Fatal(err)
	}
	var marked int
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnElement("["+markAttr+"]", func(e *lolhtml.Element) error {
			marked++
			if !strings.EqualFold(e.TagName(), "nav") {
				t.Errorf("the marker is on a %s, not the nav", e.TagName())
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if marked != 1 {
		t.Errorf("%d marked elements, want 1: %s", marked, out)
	}

	// And a second run reports the nav as already done rather than doing it.
	_, b, err := buildString(out, based)
	if err != nil {
		t.Fatal(err)
	}
	if b.emitted != 0 || total(b.skipped) != 1 {
		t.Errorf("second run: emitted=%d skipped=%v", b.emitted, b.skipped)
	}
}

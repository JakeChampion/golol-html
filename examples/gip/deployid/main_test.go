package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func echo(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Echo(strings.NewReader(doc), &out, "d7f3a91", "deploy-id")
	if err != nil {
		t.Fatalf("Echo(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestWhetherTheHeadIsStillOpenIsDecidedByWhatCameBefore, which is the whole program: a meta in
// the body is ignored, and whether the insertion point is still in the head depends on the
// document rather than on the rewrite.
func TestWhetherTheHeadIsStillOpenIsDecidedByWhatCameBefore(t *testing.T) {
	for _, tt := range []struct {
		name  string
		doc   string
		where Placement
	}{
		{"a whole page", `<!doctype html><html><head><title>t</title></head><body><p>x</p></body></html>`, InHead},
		{"no head spelled", `<!doctype html><p>x</p>`, InHead},
		{"html but no head", `<!doctype html><html><body><p>x</p></body></html>`, InHead},
		{"body spelled first", `<!doctype html><body><p>x</p></body>`, InHead},
		{"title first", `<!doctype html><title>t</title><p>x</p>`, InHead},
		{"a comment first", `<!doctype html><!-- c --><p>x</p>`, InHead},
		{"whitespace first", "<!doctype html>\n  \t<p>x</p>", InHead},
		{"no doctype", `<p>x</p>`, InHead},

		{"text first", `<!doctype html>text<p>x</p>`, InBody},
		{"one letter first", `<!doctype html>a<p>x</p>`, InBody},
		{"a nbsp first", "<!doctype html>\u00a0<p>x</p>", InBody},
		{"an entity first", `<!doctype html>&amp;<p>x</p>`, InBody},

		{"only text", `just text`, NoElement},
		{"empty", ``, NoElement},
		{"only a comment", `<!-- c -->`, NoElement},
		{"only a doctype", `<!doctype html>`, NoElement},
	} {
		out, res := echo(t, tt.doc)
		if res.Where != tt.where {
			t.Errorf("%s: %v, want %v (output %s)", tt.name, res.Where, tt.where, out)
		}
		// The tag is always written, whatever a parser will make of it.
		if !strings.Contains(out, `content="d7f3a91"`) {
			t.Errorf("%s: no meta in %s", tt.name, out)
		}
		if n := strings.Count(out, "<meta"); n != 1 {
			t.Errorf("%s: %d metas in %s", tt.name, n, out)
		}
		// And the report says which case it is, rather than implying success.
		report := res.String()
		if tt.where == InHead && !strings.Contains(report, "head reached       yes") {
			t.Errorf("%s: report:\n%s", tt.name, report)
		}
		if tt.where != InHead && !strings.Contains(report, "ignores it") {
			t.Errorf("%s: report does not say it is ignored:\n%s", tt.name, report)
		}
	}
}

// TestANonBreakingSpaceIsNotWhitespace, which is the trap: it looks like indentation and it closes
// the head. The five characters a parser skips are tab, line feed, form feed, carriage return and
// space, and nothing else.
func TestANonBreakingSpaceIsNotWhitespace(t *testing.T) {
	for _, r := range []rune{'\t', '\n', '\f', '\r', ' '} {
		if !isHTMLSpace(r) {
			t.Errorf("%q is not whitespace", r)
		}
		if !blank(string(r) + string(r)) {
			t.Errorf("%q%[1]q is not blank", r)
		}
	}
	for _, r := range []rune{'\u00a0', '\u2007', '\u202f', '\u3000', '\v', 'a', '0'} {
		if isHTMLSpace(r) {
			t.Errorf("%q is whitespace", r)
		}
		if blank(string(r)) {
			t.Errorf("%q is blank", r)
		}
	}
	if !blank("") {
		t.Error("the empty string is not blank")
	}
	if !blank(" \t\r\n\f ") {
		t.Error("a run of the five is not blank")
	}

	// End to end, because that is where it costs something.
	_, space := echo(t, "<!doctype html> <p>x</p>")
	if space.Where != InHead {
		t.Errorf("a space before the first element gave %v", space.Where)
	}
	_, nbsp := echo(t, "<!doctype html>\u00a0<p>x</p>")
	if nbsp.Where != InBody {
		t.Errorf("a non-breaking space before the first element gave %v", nbsp.Where)
	}
	if !strings.Contains(nbsp.String(), "came before it") {
		t.Errorf("the report does not name the text:\n%s", nbsp)
	}
}

// TestABareMetaIsEnough: the parser builds the head around it, so wrapping it in a head of our own
// is unnecessary - and this asserts the output has no head we did not find in the source.
func TestABareMetaIsEnough(t *testing.T) {
	out, res := echo(t, `<!doctype html><p>x</p>`)
	if res.Where != InHead {
		t.Fatalf("%v", res.Where)
	}
	if strings.Contains(out, "<head") {
		t.Errorf("the rewrite spelled a head: %s", out)
	}
	if want := `<!doctype html><meta name="deploy-id" content="d7f3a91"><p>x</p>`; out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// The tree that produces is measured in differential/deployid_test.go, where
	// x/net/html lives.
}

// TestTheDeployIdIsEscapedRatherThanTrusted, since it comes from the environment and an attribute
// value is source.
func TestTheDeployIdIsEscapedRatherThanTrusted(t *testing.T) {
	for _, tt := range []struct{ id, want string }{
		{`d7f3a91`, `content="d7f3a91"`},
		{`a"b`, `content="a&quot;b"`},
		{`a&b`, `content="a&amp;b"`},
		{`<script>`, `content="&lt;script&gt;"`},
		{`a<b>c`, `content="a&lt;b&gt;c"`},
	} {
		var out strings.Builder
		res, err := Echo(strings.NewReader(`<p>x</p>`), &out, tt.id, "deploy-id")
		if err != nil {
			t.Fatalf("%q: %v", tt.id, err)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Errorf("%q gave %s, want %s", tt.id, out.String(), tt.want)
		}
		_ = res

		// The property that matters: the document gained one meta and no other element,
		// whatever the id said.
		var names []string
		if _, err := lolhtml.RewriteString(out.String(),
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				names = append(names, e.TagName())
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(names, " "); got != "meta p" {
			t.Errorf("%q produced elements %q", tt.id, got)
		}
	}

	// And an empty id or name is refused before anything is written.
	for _, tt := range []struct{ id, name string }{{"", "deploy-id"}, {"d1", ""}} {
		var out strings.Builder
		if _, err := Echo(strings.NewReader(`<p>x</p>`), &out, tt.id, tt.name); err == nil {
			t.Errorf("id %q name %q was accepted", tt.id, tt.name)
		}
		if out.Len() != 0 {
			t.Errorf("id %q name %q wrote %d bytes", tt.id, tt.name, out.Len())
		}
	}
}

// TestTheMetaIsTheOnlyChange, since a deploy stamp that reformatted the page would be worse than
// none.
func TestTheMetaIsTheOnlyChange(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<div><ul><li>a<li>b</ul><img src="/x"></div>`,
		`<p>x</p><script>var a = 1 < 2;</script>`,
		`<!doctype html>text<p>x</p>`,
	} {
		out, res := echo(t, doc)
		without := strings.Replace(out, res.Meta(), "", 1)
		if without != doc {
			t.Errorf("more than the meta changed:\n  in:  %s\n  out: %s", doc, without)
		}
	}
}

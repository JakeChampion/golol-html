package lolhtml_test

// What DocumentEnd.Append actually appends to.
//
// The name says "document end" and the obvious reading is "the end of the
// document tree". It is the end of the output, which is where the input stopped -
// and an input can stop anywhere. This file pins the difference, because it is
// silent: nothing errors, and the appended content is simply not markup.

import (
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// truncated documents, one per way an input can stop mid-construct.
var truncated = []struct {
	name string
	in   string
}{
	{"inside a script", `<html><body><script>var a = 1`},
	{"inside a style", `<html><body><style>p{`},
	{"inside a textarea", `<html><body><textarea>x`},
	{"inside a title", `<html><body><title>x`},
	{"inside a comment", `<html><body><!-- unterminated`},
	{"inside a doctype", `<!DOCTYPE`},
}

// TestAppendIntoATruncatedDocumentIsNotMarkup is the behaviour itself. The
// insertion succeeds, the rewrite succeeds, and the result contains no such
// element.
func TestAppendIntoATruncatedDocumentIsNotMarkup(t *testing.T) {
	for _, tt := range truncated {
		t.Run(tt.name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(tt.in,
				lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
					return d.Append(`<img data-x="1">`, lolhtml.HTML)
				}))
			if err != nil {
				t.Fatalf("the rewrite failed, which would at least be a signal: %v", err)
			}
			if !strings.HasSuffix(out, `<img data-x="1">`) {
				t.Fatalf("the content was not appended at all: %q", out)
			}

			// Ask the parser. It is the judge of whether an element is an
			// element, and it says no.
			var found int
			if _, err := lolhtml.RewriteString(out,
				lolhtml.OnElement("img[data-x]", func(*lolhtml.Element) error {
					found++
					return nil
				})); err != nil {
				t.Fatal(err)
			}
			if found != 0 {
				t.Errorf("the appended img parsed as an element: %q", out)
			}
		})
	}
}

// TestAppendIsNotAffectedByStrictMode: strict mode exists to refuse input the
// rewriter cannot handle safely, and this is not one of the things it refuses.
// Worth pinning so nobody reaches for it as the answer.
func TestAppendIsNotAffectedByStrictMode(t *testing.T) {
	for _, tt := range truncated {
		var outs [2]string
		for i, strict := range []bool{false, true} {
			out, err := lolhtml.RewriteString(tt.in,
				lolhtml.WithStrict(strict),
				lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
					return d.Append(`<img data-x="1">`, lolhtml.HTML)
				}))
			if err != nil {
				t.Fatalf("%s strict=%v: %v", tt.name, strict, err)
			}
			outs[i] = out
		}
		if outs[0] != outs[1] {
			t.Errorf("%s: strict mode changed the result:\n off: %q\n  on: %q",
				tt.name, outs[0], outs[1])
		}
	}
}

// TestAppendIntoATruncatedStartTagIsWorseThanBeingSwallowed. The other cases
// lose the insertion; this one turns it into something else. An unterminated
// attribute value absorbs the appended bytes as far as the next quote, and
// whatever follows is re-parsed as attributes of the truncated element - so what
// comes out depends on where the quotes in the payload happen to fall.
//
// The results below are measured, not derived. They are pinned because they are
// the concrete answer to "what happens", and no reading of them is reassuring.
func TestAppendIntoATruncatedStartTagIsWorseThanBeingSwallowed(t *testing.T) {
	const in = `<html><body><p title="unterminated`

	for _, tt := range []struct {
		payload string
		// elements and, for each, the attribute names a parser reports.
		want map[string][]string
	}{
		// The payload's own quotes end the title, and its remainder becomes
		// attributes of the p - including an attribute whose name is a fragment
		// of the payload.
		{`<img data-x="1">`, map[string][]string{
			"html": {}, "body": {}, "p": {"title", `1"`},
		}},
		{`<img data-x="1" alt="">`, map[string][]string{
			"html": {}, "body": {}, "p": {"title", `1"`, "alt"},
		}},
		// With no quote in the payload there is nothing to close the title, so
		// the p's start tag never ends and the p does not exist either.
		{`<img data-x>`, map[string][]string{
			"html": {}, "body": {},
		}},
	} {
		out, err := lolhtml.RewriteString(in,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append(tt.payload, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.payload, err)
		}

		got := map[string][]string{}
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				names := []string{}
				for name := range e.Attributes() {
					names = append(names, name)
				}
				got[e.TagName()] = names
				return nil
			})); err != nil {
			t.Fatal(err)
		}

		if len(got) != len(tt.want) {
			t.Errorf("%s: elements %v, want %v (output %q)", tt.payload, keys(got), keys(tt.want), out)
			continue
		}
		for tag, wantAttrs := range tt.want {
			gotAttrs, ok := got[tag]
			if !ok {
				t.Errorf("%s: no %s element (output %q)", tt.payload, tag, out)
				continue
			}
			if strings.Join(gotAttrs, ",") != strings.Join(wantAttrs, ",") {
				t.Errorf("%s: %s carries %v, want %v (output %q)",
					tt.payload, tag, gotAttrs, wantAttrs, out)
			}
		}
		// In every shape, the element that was appended is not there.
		if _, ok := got["img"]; ok {
			t.Errorf("%s: an img element survived, which contradicts the rest of "+
				"this file: %q", tt.payload, out)
		}
	}
}

func keys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestAppendIntoAWholeDocumentIsMarkup, so the tests above are about truncation
// and not about Append.
func TestAppendIntoAWholeDocumentIsMarkup(t *testing.T) {
	for _, in := range []string{
		`<html><body><p>x</p></body></html>`,
		`<html><body><script>var a = 1</script></body></html>`,
		`<html><body><!-- a comment --></body></html>`,
		`<p>a fragment</p>`,
		``,
	} {
		out, err := lolhtml.RewriteString(in,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append(`<img data-x="1">`, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatal(err)
		}
		var found int
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnElement("img[data-x]", func(*lolhtml.Element) error {
				found++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Errorf("%q: %d appended elements, want 1: %q", in, found, out)
		}
	}
}

// TestABodyWithoutAnEndTagNeverCallsOnEndTag is the other half of the trap. The
// natural place for injected content is before </body>, and </body> is optional
// in HTML - so the handler that would do it is simply never called, with no
// error and nothing to distinguish that from a document with no body at all.
func TestABodyWithoutAnEndTagNeverCallsOnEndTag(t *testing.T) {
	for _, tt := range []struct {
		in         string
		wantCalled int
	}{
		{`<html><body><p>x</p></body></html>`, 1},
		{`<html><body><p>x</p></body>`, 1},
		{`<html><body><p>x</p>`, 0},
		{`<html><body><script>x`, 0},
		{`<body/><p>x</p>`, 0},
		{`<p>no body at all</p>`, 0},
	} {
		called := 0
		if _, err := lolhtml.RewriteString(tt.in,
			lolhtml.OnElement("body", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					called++
					return nil
				})
			})); err != nil {
			t.Fatal(err)
		}
		if called != tt.wantCalled {
			t.Errorf("%q: the body end-tag handler ran %d times, want %d",
				tt.in, called, tt.wantCalled)
		}
	}
}

// TestEndTagContentSurvivesRemovingTheEndTag: unlike Element.Remove, whose
// interaction with insertions is order-dependent, an end tag's Before and After
// content is emitted whether the end tag is removed before or after the
// insertion is requested.
func TestEndTagContentSurvivesRemovingTheEndTag(t *testing.T) {
	for _, tt := range []struct {
		name string
		fn   func(*lolhtml.EndTag) error
		want string
	}{
		{"before then remove", func(e *lolhtml.EndTag) error {
			if err := e.Before("[B]", lolhtml.Text); err != nil {
				return err
			}
			e.Remove()
			return nil
		}, "<div>c[B]"},
		{"remove then before", func(e *lolhtml.EndTag) error {
			e.Remove()
			return e.Before("[B]", lolhtml.Text)
		}, "<div>c[B]"},
		{"after then remove", func(e *lolhtml.EndTag) error {
			if err := e.After("[A]", lolhtml.Text); err != nil {
				return err
			}
			e.Remove()
			return nil
		}, "<div>c[A]"},
		{"remove then after", func(e *lolhtml.EndTag) error {
			e.Remove()
			return e.After("[A]", lolhtml.Text)
		}, "<div>c[A]"},
	} {
		got, err := lolhtml.RewriteString(`<div>c</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.OnEndTag(tt.fn)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

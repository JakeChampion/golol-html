package lolhtml_test

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// spacedTag is a start tag written the way a template writes one: an attribute per line. The
// formatting is what these tests are about, so it has to be worth losing.
const spacedTag = "<a\n  href=\"/x\"\n  class=\"c\">t</a>"

// TestTouchingTheAttributesReserialisesTheStartTag, and nothing else does.
//
// A rewrite is not "the input plus the edit". Setting an attribute, removing one that is
// there, or renaming the element re-serialises the whole start tag, and the re-serialisation
// regenerates the separators between attributes: newlines and runs of spaces become single
// spaces. Everything else - reading, inserting, an end-tag handler, user data, removing an
// attribute that is not there - leaves the tag's bytes exactly as they arrived.
//
// It is measurable on a real page. cloudflare.com.html has 233 matched anchors, 183 of whose
// start tags span several lines; setting one fresh attribute on each takes the document from
// 119,237 bytes to 114,542 - about four per cent smaller, having been asked to make it bigger.
// A byte comparison, a checksum or an ETag over the output therefore sees changes the caller
// did not ask for.
func TestTouchingTheAttributesReserialisesTheStartTag(t *testing.T) {
	tests := []struct {
		name        string
		reserialise bool
		fn          func(*lolhtml.Element) error
	}{
		{"doing nothing", false, func(*lolhtml.Element) error { return nil }},
		{"reading an attribute", false, func(e *lolhtml.Element) error {
			_, _ = e.Attribute("href")
			return nil
		}},
		{"listing the attributes", false, func(e *lolhtml.Element) error {
			_ = e.AttributeList()
			return nil
		}},
		{"an end-tag handler", false, func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		}},
		{"user data", false, func(e *lolhtml.Element) error { return e.SetUserData("x") }},
		{"Before", false, func(e *lolhtml.Element) error { return e.Before("<!--b-->", lolhtml.HTML) }},
		{"After", false, func(e *lolhtml.Element) error { return e.After("<!--a-->", lolhtml.HTML) }},
		{"Prepend", false, func(e *lolhtml.Element) error { return e.Prepend("<!--p-->", lolhtml.HTML) }},
		{"Append", false, func(e *lolhtml.Element) error { return e.Append("<!--x-->", lolhtml.HTML) }},
		{"SetInnerContent", false, func(e *lolhtml.Element) error {
			return e.SetInnerContent("x", lolhtml.Text)
		}},
		{"removing an attribute that is not there", false, func(e *lolhtml.Element) error {
			return e.RemoveAttribute("nope")
		}},

		{"SetAttribute", true, func(e *lolhtml.Element) error {
			return e.SetAttribute("data-x", "1")
		}},
		{"SetAttribute to the value it already has", true, func(e *lolhtml.Element) error {
			return e.SetAttribute("href", "/x")
		}},
		{"RemoveAttribute", true, func(e *lolhtml.Element) error {
			return e.RemoveAttribute("class")
		}},
		{"SetTagName", true, func(e *lolhtml.Element) error { return e.SetTagName("span") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := lolhtml.RewriteString(spacedTag, lolhtml.OnElement("a", tt.fn))
			if err != nil {
				t.Fatal(err)
			}
			kept := strings.Contains(out, "<a\n  href=\"/x\"\n  class=\"c\"") ||
				strings.Contains(out, "<span\n  href")
			if tt.reserialise && kept {
				t.Errorf("the start tag kept its formatting: %q", out)
			}
			if !tt.reserialise && !kept {
				t.Errorf("the start tag was reformatted: %q", out)
			}
		})
	}
}

// TestWhatTheReserialisationKeeps. Only the separators are regenerated: each attribute's own
// source text comes back exactly as it arrived, which is more than a reader would assume of
// something described as re-serialised.
func TestWhatTheReserialisationKeeps(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // the mutated output, in full
	}{
		{"single quotes stay single", `<a href='/x'>t</a>`, `<a href='/x' data-x="1">t</a>`},
		{"an unquoted value stays unquoted", `<a href=/x>t</a>`, `<a href=/x data-x="1">t</a>`},
		{"space around the equals survives", `<a href = "/x">t</a>`, `<a href = "/x" data-x="1">t</a>`},
		{"the case of a name survives", `<a HrEf="/x">t</a>`, `<a HrEf="/x" data-x="1">t</a>`},
		{"a duplicate attribute survives twice", `<a href="/x" href="/y">t</a>`,
			`<a href="/x" href="/y" data-x="1">t</a>`},
		{"a bare boolean stays bare", `<a href="/x" hidden>t</a>`,
			`<a href="/x" hidden data-x="1">t</a>`},
		{"an entity in a value is not decoded", `<a href="/x?a=1&amp;b=2">t</a>`,
			`<a href="/x?a=1&amp;b=2" data-x="1">t</a>`},
		{"a newline inside a value survives", "<a href=\"/x\ny\">t</a>",
			"<a href=\"/x\ny\" data-x=\"1\">t</a>"},

		{"runs of spaces between attributes collapse", `<a   href="/x"    class="c">t</a>`,
			`<a href="/x" class="c" data-x="1">t</a>`},
		{"a tab between attributes collapses", "<a\thref=\"/x\">t</a>",
			`<a href="/x" data-x="1">t</a>`},
		{"trailing space before the bracket goes", `<a href="/x" >t</a>`,
			`<a href="/x" data-x="1">t</a>`},
		{"missing space between attributes is added", `<a href="/x"class="c">t</a>`,
			`<a href="/x" class="c" data-x="1">t</a>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The passthrough is the control: none of these documents is changed by
			// being read, so any difference below is the mutation's doing.
			pass, err := lolhtml.RewriteString(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if pass != tt.in {
				t.Fatalf("a passthrough changed it: %q", pass)
			}

			got, err := lolhtml.RewriteString(tt.in, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-x", "1")
			}))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestTheTagNameCaseSurvivesAMutation, which matters because a rewrite that sets an attribute
// on an XHTML-ish document does not quietly lowercase its markup.
func TestTheTagNameCaseSurvivesAMutation(t *testing.T) {
	got, err := lolhtml.RewriteString(`<A HREF="/x">t</A>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-x", "1")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<A HREF="/x" data-x="1">t</A>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestASelfClosingSlashIsRewrittenWithASpace, which is the one shape whose *own* text changes
// rather than only its separators.
func TestASelfClosingSlashIsRewrittenWithASpace(t *testing.T) {
	got, err := lolhtml.RewriteString(`<svg><circle r="1"/></svg>`,
		lolhtml.OnElement("circle", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-x", "1")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<svg><circle r="1" data-x="1" /></svg>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestAMutationCanShrinkADocument, which is the consequence a caller notices: the output is
// not the input plus the edit, and a byte-for-byte comparison sees the difference.
func TestAMutationCanShrinkADocument(t *testing.T) {
	// A page written the way pages are written: attributes on their own lines.
	doc := strings.Repeat("<a\n    href=\"/x\"\n    class=\"link\">l</a>\n", 50)

	pass, err := lolhtml.RewriteString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if pass != doc {
		t.Fatalf("a passthrough changed the document")
	}

	mutated, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	}))
	if err != nil {
		t.Fatal(err)
	}

	added := 50 * len(` rel="noopener"`)
	if len(mutated) >= len(doc)+added {
		t.Errorf("the output is %d bytes, the input %d and the additions %d: nothing was "+
			"reformatted away", len(mutated), len(doc), added)
	}
	if !strings.Contains(mutated, `<a href="/x" class="link" rel="noopener">`) {
		t.Errorf("the tag came back as something else: %q", first(mutated, 60))
	}
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

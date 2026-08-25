package lolhtml_test

// Renaming an element can change what its content means.
//
// Whether content is markup is decided by the element it is in, so a rename
// across the raw-text boundary reinterprets everything inside. Nothing is
// inserted, so ErrRawTextBreakout has nothing to look at, and the call happens at
// the start tag, before any content has been reported - so the library cannot see
// it coming either.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// rename applies SetTagName to every element matching sel.
func rename(t *testing.T, doc, sel, to string) string {
	t.Helper()
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
		return e.SetTagName(to)
	}))
	if err != nil {
		t.Fatalf("renaming %s to %s in %q: %v", sel, to, doc, err)
	}
	return out
}

// countElements re-parses markup and counts the elements in it, which is the
// only way to ask whether something became markup.
func countElements(t *testing.T, doc, sel string) int {
	t.Helper()
	n := 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(*lolhtml.Element) error {
		n++
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRenamingRawTextTurnsItsTextIntoMarkup is the direction that matters: an
// image inside a script is nine characters of JavaScript, and after the rename it
// is an image.
func TestRenamingRawTextTurnsItsTextIntoMarkup(t *testing.T) {
	tests := []struct{ from, doc string }{
		{"script", `<script>var x = "<img src=x onerror=alert(1)>"</script>`},
		{"style", `<style>p{content:"<img src=x>"}</style>`},
		{"textarea", `<textarea><img src=x onerror=alert(1)></textarea>`},
		{"title", `<title><img src=x></title>`},
	}
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			// Before: the img is text, so nothing matches it.
			if n := countElements(t, tt.doc, "img"); n != 0 {
				t.Fatalf("the document already has %d img elements", n)
			}
			out := rename(t, tt.doc, tt.from, "div")
			if n := countElements(t, out, "img"); n != 1 {
				t.Errorf("after renaming <%s> to <div>, %q has %d img elements, want 1",
					tt.from, out, n)
			}
		})
	}
}

// The other direction: markup becomes text, which is quieter and still a change
// of meaning.
func TestRenamingToRawTextTurnsMarkupIntoText(t *testing.T) {
	const doc = `<div><img src=x><p>text</p></div>`
	if n := countElements(t, doc, "img"); n != 1 {
		t.Fatalf("the document has %d img elements, want 1", n)
	}
	for _, to := range []string{"script", "style", "textarea", "title"} {
		out := rename(t, doc, "div", to)
		if n := countElements(t, out, "img"); n != 0 {
			t.Errorf("after renaming <div> to <%s>, %q still has %d img elements",
				to, out, n)
		}
		if !strings.Contains(out, "<img src=x>") {
			t.Errorf("renaming to <%s> lost the text: %q", to, out)
		}
	}
}

// A rename that does not cross the boundary changes nothing about the content,
// which is what makes the boundary the thing to watch rather than renames in
// general.
func TestRenamingWithinAKindLeavesContentAlone(t *testing.T) {
	tests := []struct{ doc, sel, to string }{
		{`<div><img src=x></div>`, "div", "section"},
		{`<script>var x = "<img>"</script>`, "script", "style"},
		{`<textarea><img></textarea>`, "textarea", "title"},
	}
	for _, tt := range tests {
		before := countElements(t, tt.doc, "img")
		out := rename(t, tt.doc, tt.sel, tt.to)
		if after := countElements(t, out, "img"); after != before {
			t.Errorf("renaming %s to %s changed the element count from %d to %d: %q",
				tt.sel, tt.to, before, after, out)
		}
	}
}

// The end tag is renamed with the start tag, which is what keeps the output
// well-formed.
func TestTheEndTagIsRenamedToo(t *testing.T) {
	if got, want := rename(t, `<div>a</div>`, "div", "section"), `<section>a</section>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A void element has no end tag to rename, and renaming it to a non-void
	// name does not invent one.
	if got, want := rename(t, `<br>`, "br", "span"), `<span>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// What SetTagName refuses, so the boundary between "rejected" and "accepted and
// surprising" is on the record.
func TestSetTagNameValidation(t *testing.T) {
	for _, name := range []string{"div", "DIV", "my-element", `esi\:include`, "sünde"} {
		if _, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetTagName(strings.ReplaceAll(name, `\`, ""))
			})); err != nil {
			t.Errorf("SetTagName(%q) was refused: %v", name, err)
		}
	}
	for _, name := range []string{"", "a b", "1x", "<div>"} {
		_, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetTagName(name)
			}))
		if err == nil {
			t.Errorf("SetTagName(%q) was accepted", name)
		}
	}
}

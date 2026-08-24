package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<p style="color:red">a</p>`,
	`<p style="color:red">a</p><p style="color: red;">b</p>`,
	`<p style="">empty</p>`,
	`<p style=" ">blank</p>`,
	`<p style class="k">valueless</p>`,
	`<p style="x">no colon</p>`,
	`<p style="content:&quot;<x>&quot;">has markup</p>`,
	`<p style="color:red" class="existing">merged</p>`,
	`<p style="color:red" class="s-6h7xpzo5">already has it</p>`,
	`<div style="margin:0"><span style="margin:0">nested</span></div>`,
	`<p style="COLOR:RED">upper property</p>`,
	`<p style="color:red;;;">extra semicolons</p>`,
	`<p style="color:red&#59;padding:0">encoded semicolon</p>`,
	`<!DOCTYPE html><html><body><p style="color:red">doc</p></body></html>`,
	`<p>no styles</p>`,
	``,
}

func chunked(in string, n int, h *hoister) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, h.options()...)
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
		whole, _, err := hoistString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &hoister{prefix: "s"})
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
		once, _, err := hoistString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, h, err := hoistString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if len(h.order) != 0 {
			t.Errorf("second pass of %q hoisted %d rule(s)", doc, len(h.order))
		}
	}
}

// TestEquivalentDeclarationsShareOneRule is the saving. A style repeated across a
// page is the common case, and writing it once is the whole point.
func TestEquivalentDeclarationsShareOneRule(t *testing.T) {
	in := `<p style="color:red">a</p>` +
		`<p style="color: red;">b</p>` +
		`<p style=" COLOR : red ">c</p>` +
		`<p style="color:red;;">d</p>`

	got, h, err := hoistString(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.order) != 1 {
		t.Fatalf("rules=%d, want 1: %v", len(h.order), h.order)
	}
	if n := strings.Count(got, "<style>"); n != 1 {
		t.Errorf("expected one stylesheet, got %d", n)
	}
	// Every element carries the same class.
	class := h.byDecls[h.order[0]].class
	if n := strings.Count(got, class); n != 5 { // four elements plus the rule
		t.Errorf("the class appears %d times, want 5: %s", n, got)
	}
}

// TestValuesKeepTheirCase: a property name is case-insensitive in CSS and a value
// is not, so lower-casing a whole declaration would break a font family or a
// content string.
func TestValuesKeepTheirCase(t *testing.T) {
	_, h, err := hoistString(`<p style="FONT-FAMILY: Helvetica Neue">a</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.order) != 1 {
		t.Fatalf("rules=%d, want 1", len(h.order))
	}
	if got := h.order[0]; got != "font-family:Helvetica Neue" {
		t.Errorf("normalised to %q, want the property lowered and the value kept", got)
	}
}

// TestExistingClassesAreKept: dropping a class would break whatever selects on
// it, and adding one that is already there would be noise.
func TestExistingClassesAreKept(t *testing.T) {
	got, _, err := hoistString(`<p style="color:red" class="one two">a</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "one two s-") {
		t.Errorf("existing classes were not preserved in order: %s", got)
	}
	if strings.Contains(got, "style=") {
		t.Errorf("the style attribute survived: %s", got)
	}

	// Adding the same class twice is a no-op.
	for _, tt := range []struct{ existing, add, want string }{
		{"", "s-x", "s-x"},
		{"a", "s-x", "a s-x"},
		{"a s-x", "s-x", "a s-x"},
		{"  a   b  ", "s-x", "a b s-x"},
	} {
		if got := addClass(tt.existing, tt.add); got != tt.want {
			t.Errorf("addClass(%q, %q) = %q, want %q", tt.existing, tt.add, got, tt.want)
		}
	}
}

// TestEmptyStylesAreRemovedWithoutARule: an empty style attribute is worth
// removing and not worth a class. [style] matches a present-but-empty attribute,
// which is what makes this reachable.
func TestEmptyStylesAreRemovedWithoutARule(t *testing.T) {
	for _, in := range []string{
		`<p style="">a</p>`,
		`<p style=" ">a</p>`,
		`<p style>a</p>`,
		`<p style=";;">a</p>`,
	} {
		got, h, err := hoistString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if strings.Contains(got, "style") {
			t.Errorf("%s: the attribute survived: %s", in, got)
		}
		if len(h.order) != 0 {
			t.Errorf("%s: made %d rule(s) for an empty style", in, len(h.order))
		}
		if strings.Contains(got, "<style>") {
			t.Errorf("%s: emitted an empty stylesheet: %s", in, got)
		}
	}
}

// TestUnhoistableStylesAreLeftAlone. A value that cannot go into a stylesheet
// safely stays where it is: leaving it inline is correct, and silently dropping
// it would change how the page looks.
func TestUnhoistableStylesAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<p style="x">no colon</p>`,
		`<p style="content:&quot;<x>&quot;">angle bracket</p>`,
		`<p style="a:b}c{d:e">braces</p>`,
		`<p style="content:&quot;q&quot;">quote</p>`,
	} {
		got, h, err := hoistString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !strings.Contains(got, "style=") {
			t.Errorf("%s: the style was removed rather than left: %s", in, got)
		}
		if len(h.skipped) != 1 {
			t.Errorf("%s: skipped=%v, want one entry", in, h.skipped)
		}
	}
}

// TestTheStylesheetCannotEndItsOwnElement is why plausibleDeclarations refuses
// angle brackets. The rules are written into a <style> as HTML, and a "</style>"
// carried in a declaration would end that element early and turn whatever
// followed into markup.
//
// The assertion is about the stylesheet, not about the document: a "<script>" in
// an attribute value is inert and stays inert, so its presence in the output
// proves nothing either way. What matters is that no emitted <style> ever
// contains a "<".
func TestTheStylesheetCannotEndItsOwnElement(t *testing.T) {
	hostile := []string{
		`<p style="content:'</style><script>alert(1)</script>'">a</p>`,
		`<p style="content:'&lt;/style&gt;'">a</p>`,
		`<p style="a:b}.x{color:red">a</p>`,
	}

	for _, in := range hostile {
		got, h, err := hoistString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}

		// Either nothing was hoisted, or what was hoisted is inert.
		if i := strings.Index(got, "<style>"); i >= 0 {
			sheet := got[i:]
			if j := strings.Index(sheet, "</style>"); j >= 0 {
				sheet = sheet[len("<style>"):j]
			}
			if strings.ContainsAny(sheet, "<>{}") && !isJustRules(sheet) {
				t.Errorf("%s: the stylesheet carries markup: %q", in, sheet)
			}
		}
		// And the declaration is still on the element, where it cannot execute.
		if !strings.Contains(got, "style=") {
			t.Errorf("%s: the style was removed rather than left inline: %s", in, got)
		}
		if len(h.skipped) != 1 {
			t.Errorf("%s: skipped=%v, want one entry", in, h.skipped)
		}
	}
}

// isJustRules reports whether a stylesheet body is only ".class{decls}" groups,
// so the braces it necessarily contains are not counted against it.
func isJustRules(sheet string) bool {
	if strings.ContainsAny(sheet, "<>") {
		return false
	}
	depth := 0
	for _, r := range sheet {
		switch r {
		case '{':
			depth++
			if depth > 1 {
				return false
			}
		case '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// TestClassNamesAreStable across runs, because a changing class name defeats
// caching of the page and of the stylesheet.
func TestClassNamesAreStable(t *testing.T) {
	first, h1, err := hoistString(`<p style="color:red">a</p>`)
	if err != nil {
		t.Fatal(err)
	}
	second, h2, err := hoistString(`<div style="color:red">b</div>`)
	if err != nil {
		t.Fatal(err)
	}
	c1 := h1.byDecls[h1.order[0]].class
	c2 := h2.byDecls[h2.order[0]].class
	if c1 != c2 {
		t.Errorf("the same declarations produced %q and %q", c1, c2)
	}
	_ = first
	_ = second

	// And the name is a usable class: letters, digits, hyphen, nothing else.
	for _, r := range c1 {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			t.Errorf("class %q contains %q, which needs escaping in a selector", c1, r)
		}
	}
}

func TestPrefixValidation(t *testing.T) {
	for _, good := range []string{"s", "style", "_x", "aB9", "a-b"} {
		if !validClassPrefix(good) {
			t.Errorf("validClassPrefix(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "1x", "-x", "a b", "a.b", "a}b", "a<b", `a"b`} {
		if validClassPrefix(bad) {
			t.Errorf("validClassPrefix(%q) = true", bad)
		}
	}
}

// TestStylesheetGoesAtTheEnd: the set of rules is not known until the last
// element has been seen, so the document end is the only place a single pass can
// put it.
func TestStylesheetGoesAtTheEnd(t *testing.T) {
	got, _, err := hoistString(`<head></head><body><p style="color:red">a</p></body>`)
	if err != nil {
		t.Fatal(err)
	}
	styleAt := strings.Index(got, "<style>")
	if styleAt < 0 {
		t.Fatalf("no stylesheet: %s", got)
	}
	if styleAt < strings.Index(got, "</body>") {
		t.Errorf("the stylesheet is not at the end: %s", got)
	}
}

package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var tabs = Rule{
	Match: "div.tabs",
	Name:  "my-tabs",
	Attrs: map[string]string{"data-active": "active"},
	Parts: map[string]string{"div.tab-title": "title"},
}

func upgrade(t *testing.T, doc string, rules ...Rule) Result {
	t.Helper()
	if len(rules) == 0 {
		rules = []Rule{tabs}
	}
	res, err := Upgrade(doc, rules)
	if err != nil {
		t.Fatalf("Upgrade(%q): %v", doc, err)
	}
	return res
}

// TestATargetThatCannotHoldContentIsRefused, which is the mistake this program exists not to
// make. A rename writes the new name over both tags and whoever parses the output applies the
// new name's content model, so a void target does one of four things to the widget and none of
// them is keeping it.
func TestATargetThatCannotHoldContentIsRefused(t *testing.T) {
	for _, name := range []string{
		"br", "img", "hr", "input", "wbr", "area", "col", "meta", "base", "link",
		"embed", "source", "track",
	} {
		err := Rule{Match: "div.tabs", Name: name}.Validate()
		if err == nil {
			t.Errorf("%s was accepted as a target", name)
			continue
		}
		if !strings.Contains(err.Error(), "cannot hold content") {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Raw text is the other direction of the same hazard: the widget's markup becomes text.
	for _, name := range []string{"script", "style", "textarea", "title", "xmp", "plaintext"} {
		err := Rule{Match: "div.tabs", Name: name}.Validate()
		if err == nil {
			t.Errorf("%s was accepted as a target", name)
			continue
		}
		if !strings.Contains(err.Error(), "text rather than markup") {
			t.Errorf("%s: %v", name, err)
		}
	}

	// And a built-in container is refused too, not because it is unsafe here but because a
	// name without a hyphen is not what this program is for and its content model is not
	// guaranteed to be a container's.
	for _, name := range []string{"section", "div", "span"} {
		if err := (Rule{Match: "div.tabs", Name: name}).Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// A custom element name is accepted, and it is safe because a hyphenated name is always
	// an ordinary container.
	for _, name := range []string{"my-tabs", "x-a", "a-b-c"} {
		if err := (Rule{Match: "div.tabs", Name: name}).Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := (Rule{Match: "div.tabs", Name: "My-Tabs"}).Validate(); err == nil {
		t.Error("a name starting with a capital was accepted")
	}

	// Upgrade refuses before it rewrites anything.
	if _, err := Upgrade(`<div class="tabs">x</div>`,
		[]Rule{{Match: "div.tabs", Name: "br"}}); err == nil {
		t.Error("Upgrade accepted a void target")
	}
}

// TestWhatARenameIntoAVoidNameWouldDo, so the refusal above is known to be worth having. The
// output markup looks reasonable in every case; what a parser builds does not.
func TestWhatARenameIntoAVoidNameWouldDo(t *testing.T) {
	const doc = `<div class="tabs">x</div>`
	for _, tt := range []struct{ to, want string }{
		{"br", `<br class="tabs">x</br>`},
		{"img", `<img class="tabs">x</img>`},
		{"col", `<col class="tabs">x</col>`},
		{"meta", `<meta class="tabs">x</meta>`},
	} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("div.tabs", func(e *lolhtml.Element) error {
				return e.SetTagName(tt.to)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.to, err)
		}
		if out != tt.want {
			t.Errorf("%s: %q, want %q", tt.to, out, tt.want)
		}
		// The stray end tag is the whole problem, and it is right there in the output
		// with nothing to mark it as a mistake.
		if !strings.Contains(out, "</"+tt.to+">") {
			t.Errorf("%s: no stray end tag in %q", tt.to, out)
		}
	}
	// The tree each of those produces is measured in differential/rename_test.go, which is
	// where x/net/html lives.
}

// TestAWidgetWhoseEndTagWasOmittedIsSkipped. A rename writes over the token that closed the
// element, and where the source left this element's end tag out that token belongs to something
// enclosing - so renaming the items of a list writes over the list's end tag.
func TestAWidgetWhoseEndTagWasOmittedIsSkipped(t *testing.T) {
	panels := Rule{Match: "li.panel", Name: "my-panel"}

	const omitted = `<ul><li class="panel">a<li class="panel">b</ul>`
	res := upgrade(t, omitted, panels)
	if res.Doc != omitted {
		t.Errorf("the document changed:\n%s\n%s", omitted, res.Doc)
	}
	if got := res.Counts["my-panel"].Skipped; got != 2 {
		t.Errorf("%d skipped, want 2", got)
	}
	if got := res.Counts["my-panel"].Upgraded; got != 0 {
		t.Errorf("%d upgraded", got)
	}
	if !strings.Contains(res.String(), "omitted their end tag") {
		t.Errorf("the report does not say why:\n%s", res)
	}

	// With the end tags spelled out, the same widgets upgrade.
	const spelled = `<ul><li class="panel">a</li><li class="panel">b</li></ul>`
	res = upgrade(t, spelled, panels)
	if got := res.Counts["my-panel"].Upgraded; got != 2 {
		t.Errorf("%d upgraded, want 2", got)
	}
	if res.Counts["my-panel"].Skipped != 0 {
		t.Errorf("%d skipped", res.Counts["my-panel"].Skipped)
	}
	if !strings.Contains(res.Doc, `<my-panel class="panel">a</my-panel>`) {
		t.Errorf("%s", res.Doc)
	}
	if !strings.Contains(res.Doc, "</ul>") {
		t.Errorf("the list lost its end tag:\n%s", res.Doc)
	}

	// A document with some of each: only the ones that closed themselves are upgraded.
	const mixed = `<ul><li class="panel">a</li><li class="panel">b<li class="panel">c</li></ul>`
	res = upgrade(t, mixed, panels)
	c := res.Counts["my-panel"]
	if c.Upgraded != 2 || c.Skipped != 1 {
		t.Errorf("%d upgraded and %d skipped, want 2 and 1: %s", c.Upgraded, c.Skipped, res.Doc)
	}
	if !strings.Contains(res.Doc, "</ul>") {
		t.Errorf("the list lost its end tag:\n%s", res.Doc)
	}
}

// TestWhatRenamingAnOmittedEndTagWouldDo, which is what the skip is protecting. This is the
// measurement, so the two-pass design is known to be worth its pass.
func TestWhatRenamingAnOmittedEndTagWouldDo(t *testing.T) {
	out, err := lolhtml.RewriteString(`<ul><li>a<li>b<li>c</ul>`,
		lolhtml.OnElement("li", func(e *lolhtml.Element) error {
			return e.SetTagName("my-item")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<ul><my-item>a<my-item>b<my-item>c</my-item>`; out != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
	if strings.Contains(out, "</ul>") {
		t.Errorf("the list end tag survived: %q", out)
	}

	// The outermost rename wins, which distinct names show.
	n := 0
	out, err = lolhtml.RewriteString(`<ul><li>a<li>b<li>c</ul>`,
		lolhtml.OnElement("li", func(e *lolhtml.Element) error {
			n++
			return e.SetTagName("item-" + string(rune('0'+n)))
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "</item-1>") {
		t.Errorf("%q does not end with the outermost item's name", out)
	}
}

// TestTheStateMovesAndTheStylingStays, since a class attribute is a list and the state tokens
// are only some of it.
func TestTheStateMovesAndTheStylingStays(t *testing.T) {
	rule := Rule{
		Match:       "div.tabs",
		Name:        "my-tabs",
		Attrs:       map[string]string{"data-active": "active", "data-orient": "orientation"},
		DropClasses: []string{"tabs", "js-tabs"},
	}
	res := upgrade(t, `<div class="tabs widget js-tabs dark" data-active="2" `+
		`data-orient="v" id="t1">x</div>`, rule)
	doc := res.Doc
	for _, want := range []string{
		`<my-tabs`, `class="widget dark"`, `active="2"`, `orientation="v"`, `id="t1"`,
		`</my-tabs>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the output does not contain %s:\n%s", want, doc)
		}
	}
	for _, unwanted := range []string{"data-active", "data-orient", "js-tabs", `class="tabs`} {
		if strings.Contains(doc, unwanted) {
			t.Errorf("the output still contains %s:\n%s", unwanted, doc)
		}
	}

	// Every token dropped means the attribute goes rather than becoming empty.
	res = upgrade(t, `<div class="tabs" data-active="1">x</div>`, rule)
	if strings.Contains(res.Doc, "class=") {
		t.Errorf("an empty class attribute was left behind:\n%s", res.Doc)
	}

	// An attribute the widget does not have is not invented.
	res = upgrade(t, `<div class="tabs">x</div>`, rule)
	if strings.Contains(res.Doc, "active") {
		t.Errorf("a missing attribute was invented:\n%s", res.Doc)
	}
}

// TestOnlyADirectChildBecomesASlot, since a part is a child of the widget and the same markup
// deeper down belongs to something else.
func TestOnlyADirectChildBecomesASlot(t *testing.T) {
	res := upgrade(t, `<div class="tabs">`+
		`<div class="tab-title">own</div>`+
		`<div class="inner"><div class="tab-title">nested</div></div>`+
		`</div>`)
	if n := strings.Count(res.Doc, `slot="title"`); n != 1 {
		t.Errorf("%d slots, want 1:\n%s", n, res.Doc)
	}
	if !strings.Contains(res.Doc, `<div class="tab-title" slot="title">own</div>`) {
		t.Errorf("the direct child did not get the slot:\n%s", res.Doc)
	}
	if !strings.Contains(res.Doc, `<div class="tab-title">nested</div>`) {
		t.Errorf("the nested one got a slot:\n%s", res.Doc)
	}
}

// TestUpgradingTwiceChangesNothingTheSecondTime, because the selector no longer matches once the
// element has been renamed - which is idempotence for free rather than by a guard.
func TestUpgradingTwiceChangesNothingTheSecondTime(t *testing.T) {
	first := upgrade(t, `<div class="tabs" data-active="2">`+
		`<div class="tab-title">t</div></div>`)
	second := upgrade(t, first.Doc)
	if second.Doc != first.Doc {
		t.Errorf("the second pass changed the document:\n%s\n%s", first.Doc, second.Doc)
	}
	if got := second.Total(func(c *Count) int { return c.Upgraded }); got != 0 {
		t.Errorf("the second pass upgraded %d", got)
	}
}

// TestARuleIsReadFromItsFlagForm, since the flag is the interface.
func TestARuleIsReadFromItsFlagForm(t *testing.T) {
	r, err := ParseRule("div.tabs=my-tabs,data-active:active,part=div.tab-title:title,drop=tabs")
	if err != nil {
		t.Fatal(err)
	}
	if r.Match != "div.tabs" || r.Name != "my-tabs" {
		t.Errorf("%+v", r)
	}
	if r.Attrs["data-active"] != "active" {
		t.Errorf("attrs %v", r.Attrs)
	}
	if r.Parts["div.tab-title"] != "title" {
		t.Errorf("parts %v", r.Parts)
	}
	if len(r.DropClasses) != 1 || r.DropClasses[0] != "tabs" {
		t.Errorf("drop %v", r.DropClasses)
	}

	for _, bad := range []string{"", "div.tabs", "div.tabs=my-tabs,noseparator",
		"div.tabs=my-tabs,part=noslot"} {
		if _, err := ParseRule(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := Upgrade(`<p>x</p>`, nil); err == nil {
		t.Error("no rules was accepted")
	}
}

// TestADocumentWithNoWidgetsIsUnchanged, which is most of a page.
func TestADocumentWithNoWidgetsIsUnchanged(t *testing.T) {
	doc := `<main><p>text &amp; more</p><div class="other">x</div></main>`
	res := upgrade(t, doc)
	if res.Doc != doc {
		t.Errorf("the document changed:\n%s\n%s", doc, res.Doc)
	}
	if len(res.Counts) != 0 {
		t.Errorf("%d rules counted", len(res.Counts))
	}
}

package main

import (
	"fmt"
	"html"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func rewrite(t *testing.T, doc string) Result {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(strings.NewReader(doc), &out)
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", doc, err)
	}
	res.Doc = out.String()
	return res
}

// TestADirectiveIsNamedAsTheAuthorSpelledIt, which is the whole reason this reads
// NamePreserveCase. The tokenizer lower-cases attribute names because HTML matches them
// case-insensitively; a template compiler does not, so *ngif is not a directive and a report
// that says so names something nobody can search for.
func TestADirectiveIsNamedAsTheAuthorSpelledIt(t *testing.T) {
	const doc = `<div *ngIf="ok" [ngClass]="c" [(ngModel)]="v" v-bind:someProp="p" @myEvent="r">x</div>`

	// What the two readers give, measured, because the program's correctness rests on the
	// difference.
	want := map[string]string{
		"*ngif":           "*ngIf",
		"[ngclass]":       "[ngClass]",
		"[(ngmodel)]":     "[(ngModel)]",
		"v-bind:someprop": "v-bind:someProp",
		"@myevent":        "@myEvent",
	}
	got := map[string]string{}
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		for _, a := range e.AttributeList() {
			got[a.Name] = a.NamePreserveCase
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %v", got)
	}
	for lower, spelled := range want {
		if got[lower] != spelled {
			t.Errorf("Name %q has NamePreserveCase %q, want %q", lower, got[lower], spelled)
		}
	}

	// And the report uses the spelling, so a reader can find the line it is about.
	report := rewrite(t, doc).String()
	for _, spelled := range want {
		if !strings.Contains(report, spelled) {
			t.Errorf("the report does not name %s:\n%s", spelled, report)
		}
	}
	for lower := range want {
		if strings.Contains(report, lower) {
			t.Errorf("the report used the lower-cased %s:\n%s", lower, report)
		}
	}
}

// TestACamelCaseDirectiveCannotBeAdded, which is why this program only ever writes plain
// attribute names. SetAttribute lower-cases a name it is adding and keeps the document's
// spelling for one already there, so a rewrite cannot put a directive back.
func TestACamelCaseDirectiveCannotBeAdded(t *testing.T) {
	for _, name := range []string{"*ngIf", "[ngClass]", "v-bind:someProp", "@myEvent"} {
		out, err := lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.SetAttribute(name, "v")
			}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out, name) {
			t.Errorf("%s survived being added: %s", name, out)
		}
		if !strings.Contains(out, strings.ToLower(name)) {
			t.Errorf("%s: %s", name, out)
		}
	}

	// Updating one that is already there keeps the spelling, which is the only way a
	// camelCase name survives a write.
	out, err := lolhtml.RewriteString(`<div *ngIf="ok">x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetAttribute("*ngif", "changed")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div *ngIf="changed">x</div>`; out != want {
		t.Errorf("got %q, want %q", out, want)
	}

	// So does building the tag, which is the escape hatch the library documents for SVG.
	out, err = lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.Replace(`<div *ngIf="ok">x</div>`, lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "*ngIf") {
		t.Errorf("Replace lost the spelling: %s", out)
	}

	// Every plain name this program writes is lower-case already, which is what makes it
	// safe to write them at all.
	for _, subject := range []string{"someProp", "ATTR", "MaxLength", "attr.Role"} {
		if to := target(subject); to != strings.ToLower(strings.TrimPrefix(
			strings.ToLower(subject), "attr.")) {
			t.Errorf("target(%q) = %q", subject, to)
		}
	}
}

// TestOnlyALiteralBecomesAPlainAttribute. An expression needs a runtime, and a program that
// guessed would produce a page that looks right and says something else.
func TestOnlyALiteralBecomesAPlainAttribute(t *testing.T) {
	for _, tt := range []struct {
		expr  string
		value string
		ok    bool
	}{
		{`'Home'`, "Home", true},
		{"`Home`", "Home", true},
		{`10`, "10", true},
		{`-2.5`, "-2.5", true},
		{`true`, "true", true},
		{`false`, "false", true},
		{`  'padded'  `, "padded", true},

		{`url`, "", false},
		{`a + b`, "", false},
		{`{a:1}`, "", false},
		{`[1,2]`, "", false},
		{`fn()`, "", false},
		{`'a' + 'b'`, "", false},
		{`'it\'s'`, "", false},
		{``, "", false},
		{`1.2.3`, "", false},
		{`0x10`, "", false},
	} {
		value, ok := literal(tt.expr)
		if ok != tt.ok || value != tt.value {
			t.Errorf("literal(%q) = %q, %v; want %q, %v",
				tt.expr, value, ok, tt.value, tt.ok)
		}
	}

	// End to end: the literal is rewritten and the expression is reported.
	res := rewrite(t, `<a :title="'Home'" :href="url">x</a>`)
	if !strings.Contains(res.Doc, `title="Home"`) {
		t.Errorf("%s", res.Doc)
	}
	if !strings.Contains(res.Doc, `:href="url"`) {
		t.Errorf("the expression was touched: %s", res.Doc)
	}
	if len(res.Rewritten()) != 1 || len(res.LeftAlone()) != 1 {
		t.Errorf("%d rewritten, %d left", len(res.Rewritten()), len(res.LeftAlone()))
	}
	if why := res.LeftAlone()[0].Why; !strings.Contains(why, "expression") {
		t.Errorf("why = %q", why)
	}
}

// TestAValueIsSourceAndStaysSource, which is the rule the whole library runs on: what a binding's
// quotes hold is attribute-value source, so it goes back as source. Decoding it and re-encoding
// would be a round trip that changes the document; escaping it would double-encode.
func TestAValueIsSourceAndStaysSource(t *testing.T) {
	for _, tt := range []struct{ expr, want string }{
		{`'a &amp; b'`, `a &amp; b`},
		{`'&lt;p&gt;'`, `&lt;p&gt;`},
		{`'caf&eacute;'`, `caf&eacute;`},
		{`'plain'`, `plain`},
		{`'&quot;q&quot;'`, `&quot;q&quot;`},
	} {
		doc := fmt.Sprintf(`<a :title="%s">x</a>`, tt.expr)
		res := rewrite(t, doc)
		if want := fmt.Sprintf(`title="%s"`, tt.want); !strings.Contains(res.Doc, want) {
			t.Errorf("%s gave %s, want %s", doc, res.Doc, want)
		}

		// The property that matters: what a browser reads is what the template meant.
		before := html.UnescapeString(strings.Trim(tt.expr, "'"))
		after := html.UnescapeString(tt.want)
		if before != after {
			t.Errorf("%s: the decoded value changed from %q to %q", doc, before, after)
		}
	}
}

// TestEveryFrameworkNameNeedsEscapingInASelector, which is why this program reads the attribute
// list instead. An unescaped one is a rejected selector rather than one that matches nothing, so
// the mistake is loud - which is worth knowing before building a selector from a string.
func TestEveryFrameworkNameNeedsEscapingInASelector(t *testing.T) {
	const doc = `<a :href="u" @click="c" #ref *ngIf="i" (click)="k" [ngClass]="n" ` +
		`[(ngModel)]="m" v-bind:title="t">x</a>`

	for _, tt := range []struct{ escaped, raw string }{
		{`[\:href]`, `[:href]`},
		{`[\@click]`, `[@click]`},
		{`[\#ref]`, `[#ref]`},
		{`[\*ngIf]`, `[*ngIf]`},
		{`[\(click\)]`, `[(click)]`},
		{`[\[ngClass\]]`, `[[ngClass]]`},
		{`[\[\(ngModel\)\]]`, `[[(ngModel)]]`},
		{`[v-bind\:title]`, `[v-bind:title]`},
	} {
		n := 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(tt.escaped, func(*lolhtml.Element) error {
				n++
				return nil
			})); err != nil {
			t.Errorf("%s was rejected: %v", tt.escaped, err)
			continue
		}
		if n != 1 {
			t.Errorf("%s matched %d elements, want 1", tt.escaped, n)
		}

		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(tt.raw, func(*lolhtml.Element) error { return nil })); err == nil {
			t.Errorf("%s was accepted unescaped", tt.raw)
		}
	}
}

// TestEveryKindIsRecognisedAndGivenAReason, since the reason is the output a reader acts on.
func TestEveryKindIsRecognisedAndGivenAReason(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind Kind
		to   string
	}{
		{":href", Property, "href"},
		{"v-bind:href", Property, "href"},
		{"x-bind:href", Property, "href"},
		{"[href]", Property, "href"},
		{"[attr.role]", Property, "role"},
		{":maxlength.number", Property, "maxlength"},

		{"@click", Event, ""},
		{"v-on:click", Event, ""},
		{"x-on:click.prevent", Event, ""},
		{"(click)", Event, ""},

		{"*ngIf", Structural, ""},
		{"*ngFor", Structural, ""},
		{"v-if", Structural, ""},
		{"v-else-if", Structural, ""},
		{"v-for", Structural, ""},
		{"v-show", Structural, ""},

		{"[(ngModel)]", TwoWay, ""},
		{"v-model", TwoWay, ""},

		{"v-html", Markup, ""},
		{"v-text", Markup, ""},
		{"[innerHTML]", Markup, ""},

		{"href", NotABinding, ""},
		{"class", NotABinding, ""},
		{"data-x", NotABinding, ""},
		{"aria-label", NotABinding, ""},
	} {
		kind, to := classify(tt.name)
		if kind != tt.kind {
			t.Errorf("classify(%q) kind = %v, want %v", tt.name, kind, tt.kind)
		}
		if to != tt.to {
			t.Errorf("classify(%q) target = %q, want %q", tt.name, to, tt.to)
		}
		if kind != NotABinding && kind != Property && kind.why() == "" {
			t.Errorf("%q has no reason", tt.name)
		}
	}

	// A subject with no plain form is a property binding with no target, and it gets its
	// own reason rather than the expression one.
	res := rewrite(t, `<span [class.active]="on" [style.color]="c">s</span>`)
	if len(res.Rewritten()) != 0 {
		t.Errorf("%v", res.Rewritten())
	}
	for _, b := range res.LeftAlone() {
		if !strings.Contains(b.Why, "no plain attribute form") {
			t.Errorf("%s: %q", b.Name, b.Why)
		}
	}
}

// TestTheResultDoesNotDependOnTheReadSize, since the attribute list is read at the start tag and
// a document arrives in whatever pieces the network chose.
func TestTheResultDoesNotDependOnTheReadSize(t *testing.T) {
	doc := `<a :title="'Home'" :href="url" @click="go">home</a>` +
		`<div v-bind:id="'main'" *ngIf="ok" [attr.role]="'nav'">x</div>`
	whole := rewrite(t, doc)

	for _, size := range []int{1, 2, 3, 11, 512} {
		src := &chunked{s: doc, n: size}
		var out strings.Builder
		res, err := Rewrite(src, &out)
		if err != nil {
			t.Fatalf("chunk %d: %v", size, err)
		}
		if size < len(doc) && src.reads < 2 {
			t.Errorf("chunk %d: the reader was asked once, so the size did nothing",
				size)
		}
		if out.String() != whole.Doc {
			t.Errorf("chunk %d: %q", size, out.String())
		}
		res.Doc = out.String()
		if res.String() != whole.String() {
			t.Errorf("chunk %d: the report differs\n%s\n%s", size, res, whole)
		}
	}
}

type chunked struct {
	s     string
	n     int
	reads int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	c.reads++
	return n, nil
}

// TestRewritingTwiceChangesNothingTheSecondTime, because a plain attribute is not a binding.
func TestRewritingTwiceChangesNothingTheSecondTime(t *testing.T) {
	first := rewrite(t, `<a :title="'Home'" :href="url" @click="go">x</a>`)
	second := rewrite(t, first.Doc)
	if second.Doc != first.Doc {
		t.Errorf("the second pass changed the document:\n%s\n%s", first.Doc, second.Doc)
	}
	if len(second.Rewritten()) != 0 {
		t.Errorf("the second pass rewrote %v", second.Rewritten())
	}
	// The bindings it could not rewrite are still there and still reported, which is what
	// makes running it twice useful rather than merely harmless.
	if len(second.LeftAlone()) != len(first.LeftAlone()) {
		t.Errorf("%d left alone, then %d", len(first.LeftAlone()), len(second.LeftAlone()))
	}
}

// TestADocumentWithNoBindingsIsUnchanged, which is every page that is not a template.
func TestADocumentWithNoBindingsIsUnchanged(t *testing.T) {
	doc := `<main><a href="/x" class="c" data-id="1">text &amp; more</a><img src="/i"></main>`
	res := rewrite(t, doc)
	if res.Doc != doc {
		t.Errorf("the document changed:\n%s\n%s", doc, res.Doc)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("%d bindings", len(res.Bindings))
	}
	if !strings.Contains(res.String(), "0 bindings") {
		t.Errorf("%s", res)
	}
}

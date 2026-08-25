package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func add(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Add(&out, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Add(%q): %v", doc, err)
	}
	return out.String(), res
}

// honeypots returns the names of the decoy fields in a document, read back rather
// than pattern-matched.
func honeypots(t *testing.T, doc string) []string {
	t.Helper()
	var names []string
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("input", func(e *lolhtml.Element) error {
		if _, hidden := e.Attribute("hidden"); !hidden {
			return nil
		}
		if aria, _ := e.Attribute("aria-hidden"); aria != "true" {
			return nil
		}
		name, _ := e.Attribute("name")
		names = append(names, name)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return names
}

// TestEveryFormGetsOne, and the field is what it says it is.
func TestEveryFormGetsOne(t *testing.T) {
	got, res := add(t, `<form action="/a">x</form><form action="/b">y</form>`)
	if names := honeypots(t, got); len(names) != 2 {
		t.Errorf("got %q, want two decoy fields", got)
	}
	if len(res.Entries) != 2 || res.Forms != 2 {
		t.Errorf("%v", res)
	}
	// A GET form gets one too: this is about spam rather than about tokens.
	got, _ = add(t, `<form method="get" action="/search">x</form>`)
	if len(honeypots(t, got)) != 1 {
		t.Errorf("got %q", got)
	}
	// A page with no forms is untouched.
	if got, res := add(t, `<p>x</p>`); got != `<p>x</p>` || res.Forms != 0 {
		t.Errorf("got %q (%v)", got, res)
	}
}

// TestTheFieldIsHiddenWithoutCSS. The obvious way to hide a honeypot is an inline
// style, and a Content-Security-Policy without 'unsafe-inline' drops it - showing
// the field to the users of exactly the sites most likely to have a policy.
func TestTheFieldIsHiddenWithoutCSS(t *testing.T) {
	got, _ := add(t, `<form action="/a">x</form>`)
	if strings.Contains(got, "style=") {
		t.Errorf("got %q, want no inline style", got)
	}
	attrs := map[string]string{}
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("input", func(e *lolhtml.Element) error {
		for _, a := range e.AttributeList() {
			attrs[a.Name] = a.Value
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hidden", "tabindex", "autocomplete", "aria-hidden", "name", "type"} {
		if _, ok := attrs[want]; !ok {
			t.Errorf("the field has no %s: %v", want, attrs)
		}
	}
	if attrs["tabindex"] != "-1" || attrs["autocomplete"] != "off" || attrs["aria-hidden"] != "true" {
		t.Errorf("attributes = %v", attrs)
	}
}

// TestTheNameAvoidsTheFormsOwnFields, which is the per-form decision: a collision
// means the bot fills in both and the server cannot tell them apart.
func TestTheNameAvoidsTheFormsOwnFields(t *testing.T) {
	// The form uses the first candidate for its own purposes, so the honeypot takes
	// the second.
	got, res := add(t, `<form action="/a"><input name="`+Candidates[0]+`"></form>`)
	names := honeypots(t, got)
	if len(names) != 1 || names[0] != Candidates[1] {
		t.Errorf("got %q, want the second candidate", got)
	}
	if len(res.Entries) != 1 || res.Entries[0].Field != Candidates[1] {
		t.Errorf("the manifest says %v", res.Entries)
	}

	// Two forms, one with a collision and one without: each is decided on its own
	// fields.
	got, _ = add(t, `<form action="/a"><input name="`+Candidates[0]+`"></form><form action="/b">x</form>`)
	names = honeypots(t, got)
	if len(names) != 2 || names[0] != Candidates[1] || names[1] != Candidates[0] {
		t.Errorf("got %v, want the second candidate then the first", names)
	}

	// A select or a textarea counts as a name in use as much as an input does.
	for _, field := range []string{
		`<textarea name="` + Candidates[0] + `"></textarea>`,
		`<select name="` + Candidates[0] + `"></select>`,
		`<button name="` + Candidates[0] + `">x</button>`,
	} {
		got, _ := add(t, `<form action="/a">`+field+`</form>`)
		if names := honeypots(t, got); len(names) != 1 || names[0] != Candidates[1] {
			t.Errorf("%s: got %v", field, names)
		}
	}

	// Every candidate taken: refused and reported rather than colliding.
	var b strings.Builder
	b.WriteString(`<form action="/a">`)
	for _, c := range Candidates {
		b.WriteString(`<input name="` + c + `">`)
	}
	b.WriteString(`</form>`)
	got, res = add(t, b.String())
	if len(honeypots(t, got)) != 0 {
		t.Errorf("got %q, want no field", got)
	}
	if res.Refused[NoName] != 1 || res.OK() {
		t.Errorf("%v: want the refusal reported", res)
	}
}

// TestOurOwnFieldIsRecognised, which is what makes a second pass a no-op - and it
// is recognised by more than its name, so a form with its own "url_2" still gets a
// honeypot.
func TestOurOwnFieldIsRecognised(t *testing.T) {
	once, _ := add(t, `<form action="/a">x</form>`)
	twice, res := add(t, once)
	if twice != once {
		t.Errorf("\n once %q\ntwice %q", once, twice)
	}
	if res.Refused[AlreadyIn] != 1 || len(res.Entries) != 0 {
		t.Errorf("%v", res)
	}
	// A plain field with a candidate's name is not ours.
	_, res = add(t, `<form action="/a"><input name="`+Candidates[0]+`"></form>`)
	if res.Refused[AlreadyIn] != 0 || len(res.Entries) != 1 {
		t.Errorf("%v: a form's own field was mistaken for a honeypot", res)
	}
	// Hidden but not marked, or marked but not hidden, is not ours either.
	for _, field := range []string{
		`<input name="` + Candidates[0] + `" hidden>`,
		`<input name="` + Candidates[0] + `" aria-hidden="true">`,
	} {
		_, res := add(t, `<form action="/a">`+field+`</form>`)
		if res.Refused[AlreadyIn] != 0 {
			t.Errorf("%s was mistaken for a honeypot", field)
		}
	}
}

// TestAFormWhereTheFieldWouldNotBeInItIsRefused, for the reason
// examples/gip/csrf refuses the same shapes: the markup would look right and the
// field would not be submitted.
func TestAFormWhereTheFieldWouldNotBeInItIsRefused(t *testing.T) {
	for _, tc := range []struct {
		doc     string
		refused bool
	}{
		{`<table><form action="/a"><tr><td>x</td></tr></form></table>`, true},
		{`<table><tbody><form action="/a"><tr><td>x</td></tr></form></tbody></table>`, true},
		{`<select><form action="/a"><option>x</option></form></select>`, true},
		{`<table><tr><td><form action="/a">x</form></td></tr></table>`, false},
		{`<div><form action="/a">x</form></div>`, false},
	} {
		got, res := add(t, tc.doc)
		added := len(honeypots(t, got)) == 1
		if added == tc.refused {
			t.Errorf("%q -> %q: added = %v, want %v", tc.doc, got, added, !tc.refused)
		}
		if tc.refused && res.Refused[Fostered] != 1 {
			t.Errorf("%q: %v", tc.doc, res)
		}
	}
}

// TestTheManifestMatchesTheDocument, which is the point of it: the server cannot
// enforce a honeypot it cannot name.
func TestTheManifestMatchesTheDocument(t *testing.T) {
	doc := "<body>\n  <form action=\"/one\">a</form>\n  <form>b</form>\n</body>"
	got, res := add(t, doc)
	names := honeypots(t, got)
	if len(names) != len(res.Entries) {
		t.Fatalf("%d fields in the document and %d in the manifest", len(names), len(res.Entries))
	}
	for i, e := range res.Entries {
		if e.Field != names[i] {
			t.Errorf("manifest %d says %q and the document has %q", i, e.Field, names[i])
		}
	}
	if res.Entries[0].Action != "/one" || res.Entries[0].Line != 2 || res.Entries[0].Column != 3 {
		t.Errorf("the first entry is %+v, want /one at 2:3", res.Entries[0])
	}
	// A form with no action is reported as this page, since that is where it posts.
	if res.Entries[1].Action != "" {
		t.Errorf("the second entry is %+v", res.Entries[1])
	}
	if !strings.Contains(res.Entries[1].String(), "this page") {
		t.Errorf("the line reads %q", res.Entries[1].String())
	}
}

// TestTheFieldGoesFirst, so a document truncated mid-form still carries it.
func TestTheFieldGoesFirst(t *testing.T) {
	got, _ := add(t, `<form action="/a"><label>Name<input name="n"></label>`)
	if !strings.HasPrefix(got, `<form action="/a"><input type="text" name="`) {
		t.Errorf("the field is not first: %q", got)
	}
}

// TestTheFirstPassIsChunkInvariant.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := `<body><form action="/a"><input name="` + Candidates[0] + `"></form>` +
		`<form action="/b">x</form><table><form action="/c"><tr><td>y</td></tr></form></table></body>`
	want, wantRes, err := Scan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{res: Result{Refused: map[Reason]int{}}}
		w, err := lolhtml.NewWriter(io.Discard, s.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		got := s.decide([]byte(doc))
		if len(got) != len(want) {
			t.Errorf("chunks of %d: %v, want %v", size, got, want)
			continue
		}
		for at, name := range want {
			if got[at] != name {
				t.Errorf("chunks of %d: form at %d got %q, want %q", size, at, got[at], name)
			}
		}
		if len(s.res.Entries) != len(wantRes.Entries) {
			t.Errorf("chunks of %d: %d entries, want %d", size, len(s.res.Entries),
				len(wantRes.Entries))
		}
	}
}

// TestNestedFormsAreEachTheirOwn, which is markup no specification allows and a
// document can still write.
func TestNestedFormsAreEachTheirOwn(t *testing.T) {
	got, res := add(t, `<form action="/outer"><form action="/inner">x</form></form>`)
	if res.Forms != 2 {
		t.Errorf("Forms = %d, want 2", res.Forms)
	}
	if n := len(honeypots(t, got)); n != 2 {
		t.Errorf("got %q, want two fields", got)
	}
}

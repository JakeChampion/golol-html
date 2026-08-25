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

// token is the autocomplete the one field in doc was given, or "".
func token(t *testing.T, doc string) string {
	t.Helper()
	got, _ := add(t, doc)
	var value string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("input,select,textarea",
		func(e *lolhtml.Element) error {
			if v, ok := e.Attribute("autocomplete"); ok && value == "" {
				value = v
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestTheTypeIsTheStrongestEvidence, being a promise the document already made.
func TestTheTypeIsTheStrongestEvidence(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<input type="email">`, "email"},
		{`<input type="tel">`, "tel"},
		{`<input type="url">`, "url"},
		{`<input type="EMAIL">`, "email"},
		{`<input type=" email ">`, "email"},
		// A type with no meaning for autofill, and a name that says nothing.
		{`<input type="text">`, ""},
		{`<input>`, ""},
	} {
		if got := token(t, tc.doc); got != tc.want {
			t.Errorf("%q: token %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheNameIsWeakerEvidenceAndIsRead, in the spellings a form actually uses.
func TestTheNameIsWeakerEvidenceAndIsRead(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<input name="email">`, "email"},
		{`<input name="user_email">`, "email"},
		{`<input name="userEmail">`, "email"},
		{`<input name="user[email]">`, "email"},
		{`<input id="email">`, "email"},
		{`<input name="fname">`, "given-name"},
		{`<input name="surname">`, "family-name"},
		{`<input name="postcode">`, "postal-code"},
		{`<input name="zip_code">`, "postal-code"},
		{`<input name="city">`, "address-level2"},
		{`<input name="country">`, "country-name"},
		{`<input name="company">`, "organization"},
		{`<input name="otp">`, "one-time-code"},
		{`<select name="country"></select>`, "country-name"},
		{`<textarea name="street"></textarea>`, "street-address"},
		// The longest match wins, so a card number is not a number.
		{`<input name="cardnumber">`, "cc-number"},
		{`<input name="cc-number">`, "cc-number"},
		{`<input name="cvv">`, "cc-csc"},
	} {
		if got := token(t, tc.doc); got != tc.want {
			t.Errorf("%q: token %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestAnAmbiguousNameIsLeftAlone, because a wrong token fills a card number into a
// phone box.
func TestAnAmbiguousNameIsLeftAlone(t *testing.T) {
	for _, doc := range []string{
		`<input name="name">`,
		`<input name="code">`,
		`<input name="promo_code">`,
		`<input name="number">`,
		`<input name="q">`,
		`<input name="search">`,
		`<input name="field1">`,
		`<input name="custom_widget">`,
	} {
		got, _ := add(t, doc)
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
	// The ones that look like evidence are counted, so a form full of them is
	// visible rather than silent.
	_, res := add(t, `<input name="name"><input name="code">`)
	if res.Ambiguous != 2 {
		t.Errorf("Ambiguous = %d, want 2", res.Ambiguous)
	}
}

// TestThePasswordTokenIsDecidedPerForm, which is the reason for the first pass:
// the same field in two forms gets two different tokens.
func TestThePasswordTokenIsDecidedPerForm(t *testing.T) {
	for _, tc := range []struct{ what, doc, want string }{
		{"a sign-in form", `<form action="/login"><input type="password" name="password"></form>`,
			"current-password"},
		{"a registration form by action", `<form action="/register"><input type="password" name="p"></form>`,
			"new-password"},
		{"a reset form", `<form action="/password-reset"><input type="password" name="p"></form>`,
			"new-password"},
		{"a form named for signing up", `<form id="signup"><input type="password" name="p"></form>`,
			"new-password"},
		// Two password fields is a form setting a password, whatever it is called.
		{"two passwords", `<form action="/x"><input type="password" name="p"><input type="password" name="p2"></form>`,
			"new-password"},
		// No form at all: a single field widget, and nothing says it is new.
		{"no form", `<input type="password" name="password">`, "current-password"},
	} {
		if got := token(t, tc.doc); got != tc.want {
			t.Errorf("%s: token %q, want %q", tc.what, got, tc.want)
		}
	}

	// The headline: two forms on one page, decided differently. A per-element rule
	// cannot do this.
	doc := `<form action="/login"><input type="password" name="password"></form>` +
		`<form action="/register"><input type="password" name="password"><input type="password" name="confirm"></form>`
	got, res := add(t, doc)
	if n := strings.Count(got, `autocomplete="current-password"`); n != 1 {
		t.Errorf("%d current-password, want 1: %q", n, got)
	}
	if n := strings.Count(got, `autocomplete="new-password"`); n != 2 {
		t.Errorf("%d new-password, want 2: %q", n, got)
	}
	if res.NewPassword != 1 || res.CurrentPassword != 1 {
		t.Errorf("%v: want one form of each", res)
	}
}

// TestAFieldNamedForTheOldPasswordIsTheOldPassword, even in a form that is
// setting a new one - which is what a change-password form looks like.
func TestAFieldNamedForTheOldPasswordIsTheOldPassword(t *testing.T) {
	doc := `<form action="/change-password">` +
		`<input type="password" name="current_password">` +
		`<input type="password" name="new_password">` +
		`<input type="password" name="confirm_password">` +
		`</form>`
	got, _ := add(t, doc)
	if !strings.Contains(got, `name="current_password" autocomplete="current-password"`) {
		t.Errorf("the old password field is wrong: %q", got)
	}
	if n := strings.Count(got, `autocomplete="new-password"`); n != 2 {
		t.Errorf("%d new-password, want 2: %q", n, got)
	}
}

// TestWhatTheDocumentSaidIsNotReplaced, including autocomplete="off": a page that
// turned it off may be wrong about that, and it is not this program's decision.
func TestWhatTheDocumentSaidIsNotReplaced(t *testing.T) {
	for _, doc := range []string{
		`<input type="email" autocomplete="off">`,
		`<input type="email" autocomplete="username">`,
		`<input type="password" autocomplete="new-password">`,
		`<input name="postcode" autocomplete="">`,
	} {
		got, res := add(t, doc)
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Already != 1 {
			t.Errorf("%q: Already = %d, want 1", doc, res.Already)
		}
	}
}

// TestTheTypesThatAutofillNothing.
func TestTheTypesThatAutofillNothing(t *testing.T) {
	for _, kind := range []string{"hidden", "submit", "reset", "button", "image",
		"checkbox", "radio", "file", "range", "color"} {
		doc := `<input type="` + kind + `" name="email">`
		got, res := add(t, doc)
		if got != doc {
			t.Errorf("type=%q was rewritten to %q", kind, got)
		}
		if res.Skipped != 1 {
			t.Errorf("type=%q: Skipped = %d, want 1", kind, res.Skipped)
		}
	}
}

// TestAddingTwiceChangesNothing, which falls out of never replacing what is there.
func TestAddingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<form action="/login"><input name="username"><input type="password" name="p"></form>`,
		`<form action="/register"><input type="email" name="email"><input type="password" name="p"><input type="password" name="p2"></form>`,
		`<input name="promo_code">`,
	} {
		once, _ := add(t, doc)
		twice, res := add(t, once)
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if len(res.Added) != 0 {
			t.Errorf("%q: the second pass added %v", doc, res.Added)
		}
	}
}

// TestTheFirstPassIsChunkInvariant.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := `<body><form action="/login"><input name="username"><input type="password" name="p"></form>` +
		`<form action="/register"><input type="email" name="email"><input type="password" name="p1">` +
		`<input type="password" name="p2"><input name="postcode"><input name="code"></form></body>`
	want, _, err := Scan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{res: Result{Added: map[string]int{}}, forms: map[int]*form{}}
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
		got := s.decide()
		if len(got) != len(want) {
			t.Errorf("chunks of %d: %v, want %v", size, got, want)
			continue
		}
		for at, tok := range want {
			if got[at] != tok {
				t.Errorf("chunks of %d: offset %d is %q, want %q", size, at, got[at], tok)
			}
		}
	}
}

// TestAFieldOutsideAnyFormIsItsOwnGroup, because a form-less field is what a
// single-field widget looks like.
func TestAFieldOutsideAnyFormIsItsOwnGroup(t *testing.T) {
	// Two password fields outside a form are not a registration form.
	doc := `<input type="password" name="a"><input type="password" name="b">`
	got, _ := add(t, doc)
	if n := strings.Count(got, `autocomplete="current-password"`); n != 2 {
		t.Errorf("got %q, want both treated as sign-in fields", got)
	}
	// And a field inside a form is not affected by one outside it.
	doc = `<input type="password" name="a"><form action="/register"><input type="password" name="b"></form>`
	got, _ = add(t, doc)
	if !strings.Contains(got, `name="a" autocomplete="current-password"`) ||
		!strings.Contains(got, `name="b" autocomplete="new-password"`) {
		t.Errorf("got %q", got)
	}
}

// TestNestedFormsAttributeToTheInnermost, which is markup no specification allows
// and a document can still write.
func TestNestedFormsAttributeToTheInnermost(t *testing.T) {
	doc := `<form action="/login"><form action="/register"><input type="password" name="p"></form></form>`
	got, _ := add(t, doc)
	if !strings.Contains(got, `autocomplete="new-password"`) {
		t.Errorf("got %q, want the innermost form to decide", got)
	}
}

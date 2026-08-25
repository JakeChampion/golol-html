package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func upgrade(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Upgrade(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Upgrade(%q): %v", doc, err)
	}
	return out.String(), res
}

// TestTheNameSaysWhatTheFieldHolds, in the spellings a form uses.
func TestTheNameSaysWhatTheFieldHolds(t *testing.T) {
	for _, tc := range []struct{ doc, wantType, wantMode string }{
		{`<input name="email">`, "email", "email"},
		{`<input name="user_email">`, "email", "email"},
		{`<input name="userEmail">`, "email", "email"},
		{`<input name="email2">`, "email", "email"},
		{`<input id="email">`, "email", "email"},
		{`<input name="phone">`, "tel", "tel"},
		{`<input name="mobile">`, "tel", "tel"},
		{`<input name="telephone">`, "tel", "tel"},
		{`<input type="text" name="email">`, "email", "email"},
		{`<input type="" name="email">`, "email", "email"},
		// A name that says nothing gets nothing: no type, no inputmode.
		{`<input name="nickname">`, "", ""},
		{`<input>`, "", ""},
		{`<input name="">`, "", ""},
	} {
		got, _ := upgrade(t, tc.doc, Options{})
		if tc.wantType == "" {
			if got != tc.doc {
				t.Errorf("%q was rewritten to %q", tc.doc, got)
			}
			continue
		}
		if !strings.Contains(got, `type="`+tc.wantType+`"`) {
			t.Errorf("%q\n got %q\nwant type=%q", tc.doc, got, tc.wantType)
		}
		if !strings.Contains(got, `inputmode="`+tc.wantMode+`"`) {
			t.Errorf("%q\n got %q\nwant inputmode=%q", tc.doc, got, tc.wantMode)
		}
	}
}

// TestWhatTheDocumentAlreadyTypedIsLeftAlone, including search: a type is a
// deliberate choice with its own behaviour.
func TestWhatTheDocumentAlreadyTypedIsLeftAlone(t *testing.T) {
	for _, kind := range []string{"search", "password", "hidden", "email", "tel", "url", "number"} {
		doc := `<input type="` + kind + `" name="email">`
		got, res := upgrade(t, doc, Options{})
		if got != doc {
			t.Errorf("type=%q was rewritten to %q", kind, got)
		}
		if res.Already != 1 {
			t.Errorf("type=%q: Already = %d, want 1", kind, res.Already)
		}
	}
	// An inputmode the document wrote is kept, and the type is still upgraded.
	got, _ := upgrade(t, `<input name="email" inputmode="numeric">`, Options{})
	if !strings.Contains(got, `inputmode="numeric"`) || !strings.Contains(got, `type="email"`) {
		t.Errorf("got %q", got)
	}
}

// TestAPatternIsARefusal: the field already has validation and the type brings its
// own, and whether they agree is not knowable from here.
func TestAPatternIsARefusal(t *testing.T) {
	got, res := upgrade(t, `<input name="email" pattern="[^@]+@example\.com">`, Options{})
	if strings.Contains(got, `type="email"`) {
		t.Errorf("got %q, want the type left alone", got)
	}
	if !strings.Contains(got, `inputmode="email"`) {
		t.Errorf("got %q, want the keyboard anyway", got)
	}
	if res.Refused[HasPattern] != 1 {
		t.Errorf("%v: want the refusal counted", res)
	}
}

// TestAValueTheNewTypeWouldRejectIsARefusal, because a form that cannot be
// submitted until the user fixes a field they never filled in is worse than a
// missing keyboard.
func TestAValueTheNewTypeWouldRejectIsARefusal(t *testing.T) {
	for _, tc := range []struct {
		doc     string
		refused bool
	}{
		{`<input name="email" value="tbc">`, true},
		{`<input name="email" value="a@b.com">`, false},
		{`<input name="email" value="">`, false},
		{`<input name="email" value="   ">`, false},
		{`<input name="email" value="a@b c">`, true},
		{`<input name="email" value="@b.com">`, true},
		{`<input name="email" value="a@">`, true},
		// A tel accepts anything, which is why it exists rather than number.
		{`<input name="phone" value="not a number">`, false},
		{`<input name="phone" value="(020) 7946 0018">`, false},
	} {
		got, res := upgrade(t, tc.doc, Options{})
		typed := strings.Contains(got, `type="`)
		if typed == tc.refused {
			t.Errorf("%q -> %q: typed = %v, want %v", tc.doc, got, typed, !tc.refused)
		}
		if tc.refused && res.Refused[BadValue] != 1 {
			t.Errorf("%q: %v, want the refusal counted", tc.doc, res)
		}
	}
	// And a url value that is not absolute.
	got, res := upgrade(t, `<input name="website" value="example.com">`, Options{URL: true})
	if strings.Contains(got, `type="url"`) {
		t.Errorf("got %q", got)
	}
	if res.Refused[BadValue] != 1 {
		t.Errorf("%v", res)
	}
	got, _ = upgrade(t, `<input name="website" value="https://example.com">`, Options{URL: true})
	if !strings.Contains(got, `type="url"`) {
		t.Errorf("got %q", got)
	}
}

// TestAURLUpgradeIsOptIn, because its validation is the one people meet daily and
// lose to.
func TestAURLUpgradeIsOptIn(t *testing.T) {
	got, res := upgrade(t, `<input name="website">`, Options{})
	if strings.Contains(got, `type="url"`) {
		t.Errorf("got %q, want no type by default", got)
	}
	if !strings.Contains(got, `inputmode="url"`) {
		t.Errorf("got %q, want the keyboard anyway", got)
	}
	if res.Refused[URLNotAsked] != 1 {
		t.Errorf("%v", res)
	}
	got, _ = upgrade(t, `<input name="website">`, Options{URL: true})
	if !strings.Contains(got, `type="url"`) {
		t.Errorf("got %q with -url", got)
	}
}

// TestAPageThatStylesOnTheTypeStopsEveryTypeChange. The evidence is in the
// document, so the program looks - and it can be anywhere, which is why the
// document is read twice.
func TestAPageThatStylesOnTheTypeStopsEveryTypeChange(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"a stylesheet before the field",
			`<style>input[type=text]{border:1px}</style><input name="email">`},
		{"a stylesheet after the field",
			`<input name="email"><style>input[type=text]{border:1px}</style>`},
		{"quoted in the stylesheet",
			`<input name="email"><style>input[type="text"]{}</style>`},
		{"single-quoted",
			`<input name="email"><style>input[type='text']{}</style>`},
		{"spaced out",
			`<input name="email"><style>input[ type = text ]{}</style>`},
		{"a script at the end of the body",
			`<input name="email"><script>document.querySelectorAll('input[type=text]')</script>`},
	} {
		got, res := upgrade(t, tc.doc, Options{})
		if strings.Contains(got, `type="email"`) {
			t.Errorf("%s: the type was changed anyway: %q", tc.what, got)
		}
		if !strings.Contains(got, `inputmode="email"`) {
			t.Errorf("%s: the keyboard should still be added: %q", tc.what, got)
		}
		if !res.PageUsesType {
			t.Errorf("%s: PageUsesType = false", tc.what)
		}
		if res.Refused[PageUsesType] != 1 {
			t.Errorf("%s: %v", tc.what, res)
		}
	}
	// A page that mentions the type in ordinary prose is not styling on it.
	doc := `<p>Set input[type=text] in your CSS</p><input name="email">`
	got, res := upgrade(t, doc, Options{})
	if !strings.Contains(got, `type="email"`) {
		t.Errorf("prose stopped the upgrade: %q", got)
	}
	if res.PageUsesType {
		t.Error("prose was read as a stylesheet")
	}
}

// TestUpgradingTwiceChangesNothing, which falls out of leaving a typed field alone.
func TestUpgradingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<input name="email"><input name="phone"><input name="nickname">`,
		`<input name="website">`,
		`<input name="email" pattern=".+">`,
		`<style>input[type=text]{}</style><input name="email">`,
	} {
		once, _ := upgrade(t, doc, Options{})
		twice, res := upgrade(t, once, Options{})
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if len(res.Types) != 0 {
			t.Errorf("%q: the second pass typed %v", doc, res.Types)
		}
	}
}

// TestTheFirstPassIsChunkInvariant, and the stylesheet evidence with it: a match
// split across two chunks would be missed, which is why this measures several
// sizes.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	docs := []string{
		`<body><input name="email"><input name="phone"><input name="website"></body>`,
		`<body><input name="email"><style>input[type=text]{border:1px solid}</style></body>`,
	}
	for _, doc := range docs {
		want, wantRes, err := Scan([]byte(doc), Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{1, 2, 3, 7, 64} {
			s := &scanner{res: Result{
				Types: map[string]int{}, InputMode: map[string]int{}, Refused: map[Reason]int{},
			}}
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
				t.Errorf("chunks of %d on %q: %v, want %v", size, doc, got, want)
				continue
			}
			for at, c := range want {
				if got[at] != c {
					t.Errorf("chunks of %d: offset %d is %+v, want %+v", size, at, got[at], c)
				}
			}
			if s.res.PageUsesType != wantRes.PageUsesType {
				t.Errorf("chunks of %d on %q: PageUsesType = %v, want %v",
					size, doc, s.res.PageUsesType, wantRes.PageUsesType)
			}
		}
	}
}

// TestOnlyInputsAreTouched: a select or a textarea has no type to upgrade.
func TestOnlyInputsAreTouched(t *testing.T) {
	for _, doc := range []string{
		`<select name="email"></select>`,
		`<textarea name="email"></textarea>`,
		`<button name="email">x</button>`,
	} {
		if got, _ := upgrade(t, doc, Options{}); got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
	}
}

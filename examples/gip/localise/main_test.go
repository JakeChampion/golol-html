package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func localise(t *testing.T, doc, header string, markers map[string]string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Localise(&out, strings.NewReader(doc), Negotiate(header), markers)
	if err != nil {
		t.Fatalf("Localise(%q): %v", doc, err)
	}
	return out.String(), res
}

// formatted is the text of the one marked element, which is what these tests are
// about.
func formatted(t *testing.T, kind, value, currency, header string) string {
	t.Helper()
	doc := `<span data-format="` + kind + `" data-value="` + value + `"`
	if currency != "" {
		doc += ` data-currency="` + currency + `"`
	}
	doc += `>fallback</span>`
	out, _ := localise(t, doc, header, nil)
	start := strings.Index(out, ">") + 1
	end := strings.LastIndex(out, "<")
	return out[start:end]
}

// TestTheNumbersAreFormattedPerLocale. The separators are the whole of it, and
// fr-FR's group separator is a non-breaking space rather than a space.
func TestTheNumbersAreFormattedPerLocale(t *testing.T) {
	for _, tc := range []struct {
		locale, value, want string
	}{
		{"en-US", "1234567.891", "1,234,567.891"},
		{"en-GB", "1234567.891", "1,234,567.891"},
		{"fr-FR", "1234567.891", "1" + nbsp + "234" + nbsp + "567,891"},
		{"de-DE", "1234567.891", "1.234.567,891"},
		{"ja-JP", "1234567.891", "1,234,567.891"},
		{"en-US", "0", "0"},
		{"en-US", "-1234.5", "-1,234.5"},
		{"fr-FR", "-1234.5", "-1" + nbsp + "234,5"},
		{"en-US", "999", "999"},
		{"en-US", "1000", "1,000"},
		{"de-DE", "0.5", "0,5"},
	} {
		if got := formatted(t, "number", tc.value, "", tc.locale); got != tc.want {
			t.Errorf("%s number %q\n got %q\nwant %q", tc.locale, tc.value, got, tc.want)
		}
	}
}

// TestTheCurrenciesAreFormattedPerLocaleAndPerCurrency. Two axes: where the symbol
// goes is the locale's business, and how many decimals is the currency's - JPY has
// none, which is the case that catches a formatter written against dollars.
func TestTheCurrenciesAreFormattedPerLocaleAndPerCurrency(t *testing.T) {
	for _, tc := range []struct {
		locale, value, currency, want string
	}{
		{"en-US", "1234.5", "USD", "$1,234.50"},
		{"en-GB", "1234.5", "GBP", "£1,234.50"},
		{"fr-FR", "1234.5", "EUR", "1" + nbsp + "234,50" + nbsp + "€"},
		{"de-DE", "1234.5", "EUR", "1.234,50" + nbsp + "€"},
		{"ja-JP", "1234.5", "JPY", "¥1,235"},
		{"en-US", "1234.5", "JPY", "¥1,235"},
		{"fr-FR", "1234.5", "JPY", "1" + nbsp + "235" + nbsp + "¥"},
		{"en-US", "0.005", "USD", "$0.01"},
		{"en-US", "-3", "USD", "-$3.00"},
	} {
		if got := formatted(t, "currency", tc.value, tc.currency, tc.locale); got != tc.want {
			t.Errorf("%s %s %q\n got %q\nwant %q", tc.locale, tc.currency, tc.value, got, tc.want)
		}
	}
}

// TestTheDatesFollowThePattern, including a pattern that is not separators at all.
func TestTheDatesFollowThePattern(t *testing.T) {
	for _, tc := range []struct{ locale, value, want string }{
		{"en-US", "2026-08-25", "08/25/2026"},
		{"en-GB", "2026-08-25", "25/08/2026"},
		{"fr-FR", "2026-08-25", "25/08/2026"},
		{"de-DE", "2026-08-25", "25.08.2026"},
		{"ja-JP", "2026-08-25", "2026年08月25日"},
		{"en-US", "2026-01-01T13:45:00Z", "01/01/2026"},
	} {
		if got := formatted(t, "date", tc.value, "", tc.locale); got != tc.want {
			t.Errorf("%s date %q\n got %q\nwant %q", tc.locale, tc.value, got, tc.want)
		}
	}
}

// TestThePercentagesUseTheLocaleSpacing, which is a real difference rather than a
// decorative one: fr-FR puts a non-breaking space before the sign.
func TestThePercentagesUseTheLocaleSpacing(t *testing.T) {
	for _, tc := range []struct{ locale, value, want string }{
		{"en-US", "0.0725", "7.25%"},
		{"fr-FR", "0.0725", "7,25" + nbsp + "%"},
		{"de-DE", "0.0725", "7,25" + nbsp + "%"},
		{"en-US", "1", "100.00%"},
		{"en-US", "0", "0.00%"},
	} {
		if got := formatted(t, "percent", tc.value, "", tc.locale); got != tc.want {
			t.Errorf("%s percent %q\n got %q\nwant %q", tc.locale, tc.value, got, tc.want)
		}
	}
}

// TestAValueThatDoesNotParseIsLeftAlone, visibly and counted: the element's
// existing content is the fallback the document wrote.
func TestAValueThatDoesNotParseIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		kind, value, currency string
		unparsed, unknownCur  int
	}{
		{"number", "not a number", "", 1, 0},
		{"number", "", "", 1, 0},
		{"number", "1,234", "", 1, 0},
		{"number", "Inf", "", 1, 0},
		{"number", "NaN", "", 1, 0},
		{"date", "25/08/2026", "", 1, 0},
		{"date", "2026-8-5", "", 1, 0},
		{"date", "2026-13-01", "", 1, 0},
		{"date", "2026-08-32", "", 1, 0},
		{"currency", "1234", "XYZ", 0, 1},
		{"currency", "1234", "", 0, 1},
	} {
		doc := `<span data-format="` + tc.kind + `" data-value="` + tc.value + `"`
		if tc.currency != "" {
			doc += ` data-currency="` + tc.currency + `"`
		}
		doc += `>fallback</span>`
		got, res := localise(t, doc, "en-US", nil)
		if got != doc {
			t.Errorf("%s %q\n got %q\nwant it unchanged", tc.kind, tc.value, got)
		}
		if res.Unparsed != tc.unparsed || res.UnknownCurrency != tc.unknownCur {
			t.Errorf("%s %q: %v", tc.kind, tc.value, res)
		}
	}
	// An unknown kind is its own count.
	_, res := localise(t, `<span data-format="colour" data-value="1">x</span>`, "en-US", nil)
	if res.UnknownKind != 1 {
		t.Errorf("UnknownKind = %d, want 1", res.UnknownKind)
	}
}

// TestTheValueIsWrittenAsText. A data-value is a string from a CMS, so it gets the
// same treatment as anything else from outside.
func TestTheValueIsWrittenAsText(t *testing.T) {
	// The number path cannot carry markup, so the test is the date pattern, which
	// echoes nothing, and the unparsed path, which writes nothing at all. What can
	// carry it is an element-name marker with an unparseable value: the content is
	// left as the document wrote it, so nothing new is introduced.
	doc := `<span data-format="number" data-value="1&lt;script&gt;">x</span>`
	got, res := localise(t, doc, "en-US", nil)
	if got != doc {
		t.Errorf("got %q, want it unchanged", got)
	}
	if res.Unparsed != 1 {
		t.Errorf("%v", res)
	}
	// And a value that does parse is written through SetInnerContent as Text, so a
	// document whose fallback content was markup keeps no trace of it.
	doc = `<span data-format="number" data-value="1234"><b>1234</b></span>`
	got, _ = localise(t, doc, "en-US", nil)
	if want := `<span data-format="number" data-value="1234">1,234</span>`; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestTheAcceptLanguageNegotiation, q-values and all.
func TestTheAcceptLanguageNegotiation(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"en-US", "en-US"},
		{"fr-FR", "fr-FR"},
		{"FR-fr", "fr-FR"},
		{"fr-CH", "fr-FR"},                     // language match
		{"fr-CH, en;q=0.9", "fr-FR"},           // and it wins on q
		{"en;q=0.9, fr-FR;q=1.0", "fr-FR"},     // order by q, not by position
		{"en-GB;q=0.5, de-DE;q=0.8", "de-DE"},  //
		{"en-GB;q=0.8, de-DE;q=0.8", "en-GB"},  // a tie keeps the header's order
		{"zz, ja-JP", "ja-JP"},                 // unknown tags are skipped
		{"en-US;q=0", "en-US"},                 // q=0 means no, so the default
		{"", "en-US"},                          // no header, the default
		{"   ", "en-US"},                       //
		{"*", "en-US"},                         // a wildcard is not a locale here
		{"en-US;q=bogus", "en-US"},             // an unparseable q is 1
		{"de-DE;q=0.9;charset=utf-8", "de-DE"}, // extra parameters ignored
		{",,,fr-FR,,,", "fr-FR"},               // empty entries skipped
		{"ja, fr", "ja-JP"},                    // language-only tags
	} {
		if got := Negotiate(tc.header).Tag; got != tc.want {
			t.Errorf("Negotiate(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

// TestAnElementNameMarkerIsMatchedInEitherCase, which no single selector can do:
// HTML folds the ASCII letters of a name and leaves the rest, so <PRÉSTAMO> is the
// element "prÉstamo" and <préstamo> is "préstamo".
func TestAnElementNameMarkerIsMatchedInEitherCase(t *testing.T) {
	markers := map[string]string{"préstamo": "currency"}
	for _, doc := range []string{
		`<PRÉSTAMO data-value="99.5" data-currency="EUR">x</PRÉSTAMO>`,
		`<préstamo data-value="99.5" data-currency="EUR">x</préstamo>`,
		`<PrÉstamo data-value="99.5" data-currency="EUR">x</PrÉstamo>`,
	} {
		got, res := localise(t, doc, "en-US", markers)
		if !strings.Contains(got, "€99.50") {
			t.Errorf("%q\n got %q\nwant it formatted", doc, got)
		}
		if res.Currencies != 1 {
			t.Errorf("%q: %v", doc, res)
		}
	}
	// The spelling with the accent in the other case is a different name, and the
	// program does not claim otherwise: "PRÉSTAMO" folds to "prÉstamo", and a
	// marker configured as "préstamo" is matched by EqualFold, which folds the
	// accent too. A selector would not.
	both := 0
	for _, sel := range []string{"préstamo", "prÉstamo"} {
		n := 0
		if _, err := lolhtml.RewriteString(
			`<PRÉSTAMO>a</PRÉSTAMO><préstamo>b</préstamo>`,
			lolhtml.OnElement(sel, func(*lolhtml.Element) error {
				n++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("selector %q matched %d of the two spellings, want 1", sel, n)
		}
		both += n
	}
	if both != 2 {
		t.Errorf("the two selectors matched %d elements between them, want 2", both)
	}
	// And the program's own matching gets both from one pass.
	got, res := localise(t,
		`<PRÉSTAMO data-value="1" data-currency="EUR">a</PRÉSTAMO>`+
			`<préstamo data-value="2" data-currency="EUR">b</préstamo>`,
		"en-US", markers)
	if res.Currencies != 2 {
		t.Errorf("%v: got %q", res, got)
	}
}

// TestFormattingTwiceChangesNothing: the value is in the attribute, so the second
// pass formats the same value again.
func TestFormattingTwiceChangesNothing(t *testing.T) {
	const doc = `<p><span data-format="number" data-value="1234.5">x</span>` +
		`<span data-format="currency" data-value="9" data-currency="JPY">y</span>` +
		`<span data-format="date" data-value="2026-08-25">z</span>` +
		`<span data-format="number" data-value="bad">keep</span></p>`
	for _, locale := range []string{"en-US", "fr-FR", "ja-JP"} {
		once, _ := localise(t, doc, locale, nil)
		twice, _ := localise(t, once, locale, nil)
		if twice != once {
			t.Errorf("%s\n once %q\ntwice %q", locale, once, twice)
		}
	}
}

// TestChunkInvariance.
func TestChunkInvariance(t *testing.T) {
	const doc = `<html><body><p><span data-format="number" data-value="1234567.891">a</span>` +
		`<span data-format="currency" data-value="1234.5" data-currency="EUR">b</span>` +
		`<PRÉSTAMO data-value="7" data-currency="JPY">c</PRÉSTAMO>` +
		`<span data-format="date" data-value="2026-08-25">d</span></p></body></html>`
	markers := map[string]string{"préstamo": "currency"}
	want, _ := localise(t, doc, "fr-FR", markers)
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		l := &localiser{locale: Negotiate("fr-FR"), markers: markers, res: Result{Locale: "fr-FR"}}
		w, err := lolhtml.NewWriter(&out, l.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func check(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Check([]byte(doc))
	if err != nil {
		t.Fatalf("Check(%q): %v", doc, err)
	}
	return res
}

func kinds(res Result) string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, string(f.Kind))
	}
	return strings.Join(out, ",")
}

// TestALabelCanNameItsControlFromEitherDirection, which is the join: the id may be
// resolved forwards or backwards, and a report can do both because it decides at
// the end.
func TestALabelCanNameItsControlFromEitherDirection(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"label first", `<label for="n">Name</label><input id="n">`},
		{"control first", `<input id="n"><label for="n">Name</label>`},
		{"far apart", `<input id="n"><p>a</p><div><section><label for="n">Name</label></section></div>`},
		{"implicit, control inside", `<label>Name <input></label>`},
		{"implicit, nested deeper", `<label>Name <span><input></span></label>`},
		{"aria-label", `<input aria-label="Name">`},
		{"aria-labelledby, target after", `<input aria-labelledby="h"><h2 id="h">Name</h2>`},
		{"aria-labelledby, target before", `<h2 id="h">Name</h2><input aria-labelledby="h">`},
		// Several labels for one control is allowed.
		{"two labels", `<label for="n">A</label><label for="n">B</label><input id="n">`},
	} {
		res := check(t, tc.doc)
		if !res.OK() {
			t.Errorf("%s: %q gave %v", tc.what, tc.doc, res.Findings)
		}
		if res.Labelled != 1 {
			t.Errorf("%s: Labelled = %d, want 1", tc.what, res.Labelled)
		}
	}
}

// TestAControlWithNothingNamingIt.
func TestAControlWithNothingNamingIt(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<input>`, "no-label"},
		{`<input type="text">`, "no-label"},
		{`<input type="checkbox">`, "no-label"},
		{`<input type="date">`, "no-label"},
		{`<select><option>a</option></select>`, "no-label"},
		{`<textarea></textarea>`, "no-label"},
		{`<button></button>`, "no-label"},
		{`<meter value="1"></meter>`, "no-label"},
		{`<progress value="1"></progress>`, "no-label"},
		{`<output></output>`, "no-label"},
		// An id that nothing points at is not a label.
		{`<input id="n">`, "no-label"},
		// A label whose for points elsewhere does not name this one.
		{`<label for="other">A</label><input id="n"><span id="other"></span>`,
			"for-not-labelable,no-label"},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestAPlaceholderIsNotALabel, which is the mistake this program exists to find,
// and the message says the placeholder is there rather than pretending it is not.
func TestAPlaceholderIsNotALabel(t *testing.T) {
	res := check(t, `<input type="email" placeholder="Email">`)
	if got := kinds(res); got != "placeholder-only" {
		t.Fatalf("findings %q", got)
	}
	if !strings.Contains(res.Findings[0].Message, "placeholder") {
		t.Errorf("the message does not mention it: %q", res.Findings[0].Message)
	}
	// A placeholder alongside a real label is fine.
	res = check(t, `<label for="e">Email</label><input id="e" placeholder="you@example.com">`)
	if !res.OK() {
		t.Errorf("findings on a labelled field with a placeholder: %v", res.Findings)
	}
	// A title is the same shape of near-miss.
	res = check(t, `<input title="Email">`)
	if got := kinds(res); got != "title-only" {
		t.Errorf("findings %q, want title-only", got)
	}
}

// TestWhatNeedsNoLabel, decided by the type. A linter that reported a hidden input
// would be a linter nobody runs.
func TestWhatNeedsNoLabel(t *testing.T) {
	for _, tc := range []struct {
		doc     string
		want    string
		skipped int
	}{
		{`<input type="hidden" name="csrf">`, "", 1},
		{`<input type="submit">`, "", 1},
		{`<input type="reset">`, "", 1},
		{`<input type="submit" value="Send">`, "", 1},
		{`<input type="button" value="Go">`, "", 1},
		// A button with nothing written on it is a different finding.
		{`<input type="button">`, "no-label", 1},
		{`<input type="image" src="/go.png" alt="Search">`, "", 1},
		{`<input type="image" src="/go.png">`, "image-no-alt", 1},
		// The type is matched case-insensitively and trimmed, because a document
		// writes it however it likes.
		{`<input type="HIDDEN">`, "", 1},
		{`<input type=" hidden ">`, "", 1},
	} {
		res := check(t, tc.doc)
		if got := kinds(res); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
		if res.Skipped != tc.skipped {
			t.Errorf("%q: Skipped = %d, want %d", tc.doc, res.Skipped, tc.skipped)
		}
		if res.Controls != 0 {
			t.Errorf("%q: Controls = %d, want 0", tc.doc, res.Controls)
		}
	}
}

// TestALabelThatPointsAtNothing, in the three ways it can.
func TestALabelThatPointsAtNothing(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<label for="gone">A</label>`, "for-missing"},
		{`<label for="d">A</label><div id="d"></div>`, "for-not-labelable"},
		{`<label>A</label>`, "dangling-label"},
		// A label with text and a control inside it is not dangling.
		{`<label>A <input></label>`, ""},
		// A label with no for, inside which the control is nested deeply.
		{`<label>A <span><b><input></b></span></label>`, ""},
		// An empty for is not a for.
		{`<label for="">A</label>`, "dangling-label"},
	} {
		if got := kinds(check(t, tc.doc)); got != tc.want {
			t.Errorf("%q: findings %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestADuplicateIdMakesTheAssociationAmbiguous, which is the finding a single
// element could never produce: it is a fact about the whole document.
func TestADuplicateIdMakesTheAssociationAmbiguous(t *testing.T) {
	res := check(t, `<label for="n">A</label><input id="n"><input id="n">`)
	if !strings.Contains(kinds(res), "ambiguous-id") {
		t.Errorf("findings %q, want the ambiguity reported", kinds(res))
	}
	// And both controls are unlabelled as far as this program is concerned, because
	// which one a browser labels is not something the markup says.
	if res.Labelled != 0 {
		t.Errorf("Labelled = %d, want 0", res.Labelled)
	}
	// A duplicate id nothing points at is not this program's business.
	res = check(t, `<span id="n"></span><span id="n"></span>`)
	if !res.OK() {
		t.Errorf("findings %v, want none", res.Findings)
	}
}

// TestTheFindingsAreInDocumentOrderWithLocations.
func TestTheFindingsAreInDocumentOrderWithLocations(t *testing.T) {
	doc := "<form>\n  <input type=\"email\" placeholder=\"Email\">\n  <label for=\"gone\">A</label>\n</form>"
	res := check(t, doc)
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %v", res.Findings)
	}
	if res.Findings[0].Line != 2 || res.Findings[0].Column != 3 {
		t.Errorf("first at %d:%d, want 2:3", res.Findings[0].Line, res.Findings[0].Column)
	}
	if res.Findings[1].Line != 3 || res.Findings[1].Column != 3 {
		t.Errorf("second at %d:%d, want 3:3", res.Findings[1].Line, res.Findings[1].Column)
	}
	if res.Findings[0].At >= res.Findings[1].At {
		t.Error("the findings are not in document order")
	}
}

// TestAGoodFormIsQuiet.
func TestAGoodFormIsQuiet(t *testing.T) {
	doc := `<form>
	  <label for="name">Name</label><input id="name" placeholder="Ada Lovelace">
	  <label>Email <input type="email"></label>
	  <fieldset><legend>Colour</legend>
	    <label for="r">Red</label><input type="radio" id="r" name="c">
	    <label for="b">Blue</label><input type="radio" id="b" name="c">
	  </fieldset>
	  <input type="hidden" name="csrf" value="x">
	  <textarea aria-label="Notes"></textarea>
	  <h2 id="q">Quantity</h2><input type="number" aria-labelledby="q">
	  <input type="image" src="/go.png" alt="Search">
	  <input type="submit" value="Send">
	</form>`
	res := check(t, doc)
	if !res.OK() {
		t.Errorf("findings on a good form: %v", res.Findings)
	}
	if res.Controls != 6 || res.Labelled != 6 {
		t.Errorf("%v: want six controls, all labelled", res)
	}
}

// TestTheReportIsStableAcrossWritePatterns.
func TestTheReportIsStableAcrossWritePatterns(t *testing.T) {
	doc := "<form>\n<input type=\"email\" placeholder=\"Email\">\n<label for=\"gone\">A</label>\n" +
		"<label>Dangling</label>\n<label for=\"n\">N</label><input id=\"n\"><input id=\"n\">\n</form>"
	want := check(t, doc)
	if len(want.Findings) == 0 {
		t.Fatal("nothing to compare")
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		c := &checker{ids: map[string][]string{}}
		w, err := lolhtml.NewWriter(io.Discard, c.options()...)
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
		got := c.report([]byte(doc))
		if len(got.Findings) != len(want.Findings) {
			t.Errorf("chunks of %d: %d findings, want %d", size, len(got.Findings), len(want.Findings))
			continue
		}
		for i := range got.Findings {
			if got.Findings[i] != want.Findings[i] {
				t.Errorf("chunks of %d: finding %d is %+v, want %+v", size, i,
					got.Findings[i], want.Findings[i])
			}
		}
	}
}

// TestNestedLabelsBothContainTheControl. Nested labels are markup no specification
// allows and documents write anyway. HTML associates a label with its first
// labelable descendant, so both of these label the same control and neither is
// dangling - reporting the outer one would be this program inventing a rule.
func TestNestedLabelsBothContainTheControl(t *testing.T) {
	res := check(t, `<label>outer<label>inner <input></label></label>`)
	if res.Labelled != 1 {
		t.Errorf("Labelled = %d, want 1", res.Labelled)
	}
	if !res.OK() {
		t.Errorf("findings %v, want none", res.Findings)
	}
	// A label wrapping another label with no control anywhere is still dangling,
	// twice: neither has one.
	res = check(t, `<label>outer<label>inner</label></label>`)
	if got := kinds(res); got != "dangling-label,dangling-label" {
		t.Errorf("findings %q, want both labels reported", got)
	}
}

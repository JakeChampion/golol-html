package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func pick(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(strings.NewReader(doc), &out)
	if err != nil {
		t.Fatalf("Rewrite(%.60q): %v", doc, err)
	}
	return out.String(), res
}

// TestOnlyADoctypeAParserHonoursCounts, which is the finding: OnDoctype fires for every doctype
// token in the source, and a parser honours only the first one with nothing but whitespace and
// comments before it. Three of these fire the handler and are quirks mode.
func TestOnlyADoctypeAParserHonoursCounts(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		mode Mode
	}{
		{"a doctype first", `<!doctype html><table></table>`, Standards},
		{"whitespace then a doctype", "\n \t<!doctype html><table></table>", Standards},
		{"a comment then a doctype", `<!-- c --><!doctype html><table></table>`, Standards},
		{"two comments then a doctype", `<!-- a --><!-- b --><!doctype html><table></table>`, Standards},
		{"upper case", `<!DOCTYPE HTML><table></table>`, Standards},

		// The handler fires for all of these and a parser ignores the doctype.
		{"text then a doctype", `text<!doctype html><table></table>`, Quirks},
		{"an element then a doctype", `<div>x</div><!doctype html><table></table>`, Quirks},
		{"a nbsp then a doctype", "\u00a0<!doctype html><table></table>", Quirks},
		{"a legacy doctype", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN"><table></table>`, Quirks},
		{"no doctype", `<table></table>`, Quirks},
	} {
		out, res := pick(t, tt.doc)
		if res.Mode != tt.mode {
			t.Errorf("%s: %v, want %v", tt.name, res.Mode, tt.mode)
		}
		// The chosen set is the one that ran, which the output shows.
		want := `data-mode="quirks"`
		if tt.mode == Standards {
			want = `data-mode="standards"`
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: the output does not contain %s:\n%s", tt.name, want, out)
		}
		if res.Matches != 1 {
			t.Errorf("%s: %d matches, want 1", tt.name, res.Matches)
		}
	}
}

// TestTheHandlerFiresForADoctypeThatDoesNotCount, so the distinction is not theoretical: the three
// quirks rows above are documents where OnDoctype reports "html".
func TestTheHandlerFiresForADoctypeThatDoesNotCount(t *testing.T) {
	for _, doc := range []string{
		`text<!doctype html><p>x</p>`,
		`<div>x</div><!doctype html>`,
		"\u00a0<!doctype html><p>x</p>",
	} {
		// What the raw handler reports.
		reported := ""
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				n, _ := d.Name()
				reported = n
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if reported != "html" {
			t.Errorf("%q: the handler reported %q, so this case does not show the "+
				"difference", doc, reported)
		}
		// And what this program decides.
		_, res := pick(t, doc)
		if res.Mode != Quirks {
			t.Errorf("%q: decided %v", doc, res.Mode)
		}
		if res.Counted {
			t.Errorf("%q: counted a doctype a parser ignores", doc)
		}
		if res.Doctype != "html" {
			t.Errorf("%q: reported doctype %q", doc, res.Doctype)
		}
		if res.Disqualifier == "" {
			t.Errorf("%q: nothing was named as coming first", doc)
		}
		if !strings.Contains(res.String(), "which a parser ignores") {
			t.Errorf("%q: report:\n%s", doc, res)
		}
	}
}

// TestOnlyTheFiveWhitespaceCharactersKeepADoctypeEligible, the same set that decides whether a meta
// reaches the head. A non-breaking space looks like indentation and is text.
func TestOnlyTheFiveWhitespaceCharactersKeepADoctypeEligible(t *testing.T) {
	for _, r := range []rune{'\t', '\n', '\f', '\r', ' '} {
		if !isHTMLSpace(r) {
			t.Errorf("%q is not whitespace", r)
		}
		doc := string(r) + `<!doctype html><table></table>`
		if _, res := pick(t, doc); res.Mode != Standards {
			t.Errorf("%q before a doctype gave %v", r, res.Mode)
		}
	}
	for _, r := range []rune{'\u00a0', '\u2007', '\u202f', '\u3000', '\v', 'x'} {
		if isHTMLSpace(r) {
			t.Errorf("%q is whitespace", r)
		}
		doc := string(r) + `<!doctype html><table></table>`
		if _, res := pick(t, doc); res.Mode != Quirks {
			t.Errorf("%q before a doctype gave %v", r, res.Mode)
		}
	}
	if !blank(" \t\r\n\f ") || blank("\u00a0") || blank("x") {
		t.Error("blank() disagrees with isHTMLSpace()")
	}
}

// TestTheFirstDoctypeDecides, since a second one is a parse error and dropped.
func TestTheFirstDoctypeDecides(t *testing.T) {
	_, res := pick(t, `<!doctype html><table></table><!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN">`)
	if res.Mode != Standards {
		t.Errorf("a legacy doctype later in the document changed the mode to %v", res.Mode)
	}
	_, res = pick(t, `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN"><!doctype html><table></table>`)
	if res.Mode != Quirks {
		t.Errorf("a standards doctype after a legacy one changed the mode to %v", res.Mode)
	}
}

// TestTheDocumentIsOtherwiseUnchanged, since a mode decision that reformatted the page would be
// worse than none.
func TestTheDocumentIsOtherwiseUnchanged(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>t &amp; u</title></head><body><p>a &lt; b</p></body></html>`,
		`<div><ul><li>a<li>b</ul><img src="/x"></div>`,
		`<p>x</p><script>var a = 1 < 2;</script>`,
		``,
		`just text`,
	} {
		out, res := pick(t, doc)
		if res.Matches != 0 {
			t.Errorf("%.40q: %d matches in a document with no table", doc, res.Matches)
		}
		if out != doc {
			t.Errorf("the document changed:\n  in:  %s\n  out: %s", doc, out)
		}
	}
}

// TestBothSetsAreRegisteredRatherThanChosenAfterAPeek, which is the design: one pass, and a peek
// could not see a doctype far enough in anyway.
func TestBothSetsAreRegisteredRatherThanChosenAfterAPeek(t *testing.T) {
	// A doctype past any reasonable peek.
	doc := strings.Repeat(`<!-- c -->`, 200) + `<!doctype html><table></table>`
	if len(doc) < 2000 {
		t.Fatalf("the test document is only %d bytes", len(doc))
	}
	out, res := pick(t, doc)
	if res.Mode != Standards {
		t.Errorf("a doctype %d bytes in was missed: %v", strings.Index(doc, "<!doctype"), res.Mode)
	}
	if !strings.Contains(out, `data-mode="standards"`) {
		t.Errorf("the standards set did not run")
	}

	// A 512-byte peek would not have found it, which is why there is no peek.
	found := false
	if _, err := lolhtml.RewriteString(doc[:512],
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
			found = true
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("a 512-byte peek found the doctype, so this document does not show why a " +
			"peek is a bound on correctness")
	}
}

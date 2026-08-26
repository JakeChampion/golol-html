package differential

// Which doctype a parser honours, against which one a rewriter reports. OnDoctype fires for every
// doctype token in the source; a parser honours the first one with nothing but whitespace and
// comments before it and drops the rest as parse errors.
//
// The mode is read from the outside by B174's behaviour: a table start tag closes an open paragraph
// in standards mode and not in quirks.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// parserMode reports which mode a document beginning with prefix is parsed in.
func parserMode(t *testing.T, prefix string) string {
	t.Helper()
	got := tree(t, prefix+`<p>a<table><tr><td>x</table>`)
	if strings.Contains(got, "..p ...#a ..table") || strings.Contains(got, "..p ...#a ...# ") {
		return "standards"
	}
	return "quirks"
}

// reportedDoctype returns the doctype name a rewriter reports, or the empty string.
func reportedDoctype(t *testing.T, doc string) string {
	t.Helper()
	name := ""
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
		if name == "" {
			n, _ := d.Name()
			name = n
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestTheReportedDoctypeIsNotAlwaysTheParsersMode(t *testing.T) {
	for _, tt := range []struct {
		name, prefix, reports, mode string
	}{
		{"a doctype first", `<!doctype html>`, "html", "standards"},
		{"whitespace then a doctype", "\n  \t<!doctype html>", "html", "standards"},
		{"a comment then a doctype", `<!-- c --><!doctype html>`, "html", "standards"},
		{"two comments then a doctype", `<!-- a --><!-- b --><!doctype html>`, "html", "standards"},

		// The handler fires and the parser is in quirks mode.
		{"text then a doctype", `text<!doctype html>`, "html", "quirks"},
		{"an element then a doctype", `<div>x</div><!doctype html>`, "html", "quirks"},
		{"a nbsp then a doctype", " <!doctype html>", "html", "quirks"},

		{"a legacy doctype", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN">`, "html", "quirks"},
		{"no doctype", ``, "", "quirks"},
	} {
		if got := reportedDoctype(t, tt.prefix+`<p>a</p>`); got != tt.reports {
			t.Errorf("%s: the rewriter reports %q, want %q", tt.name, got, tt.reports)
		}
		if got := parserMode(t, tt.prefix); got != tt.mode {
			t.Errorf("%s: the parser is in %s mode, want %s", tt.name, got, tt.mode)
		}
	}

	// The point in one assertion: there are documents where the handler says html and the
	// mode is quirks.
	disagreements := 0
	for _, prefix := range []string{`text<!doctype html>`, `<div>x</div><!doctype html>`,
		" <!doctype html>"} {
		if reportedDoctype(t, prefix+`<p>a</p>`) == "html" && parserMode(t, prefix) == "quirks" {
			disagreements++
		}
	}
	if disagreements != 3 {
		t.Errorf("%d of 3 documents disagree, so the finding does not reproduce", disagreements)
	}
}

package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const page = `<html><head>` +
	`<meta property="og:title" content="Widget &amp; Co">` +
	`<meta property="og:image" content="/a.png">` +
	`<meta property="og:image" content="/b.png">` +
	`<meta name="twitter:title" content="T">` +
	`<meta name="description" content="ignored">` +
	`<meta property="og:type">` +
	`<title>x</title></head><body>y</body></html>`

var corpus = []string{
	page,
	`<meta property="og:title" content="a">`,
	`<meta name="og:title" content="a">`,
	`<meta property="twitter:card" content="summary">`,
	`<meta property="og:title" content="">`,
	`<meta property="og:title" property="og:description" content="a">`,
	`<meta property="OG:TITLE" content="a">`,
	`<meta property="og:title" content="a"><meta property="og:title" content="b">`,
	`<meta name="viewport" content="width=device-width">`,
	`<p>no metadata</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*reporter)) (string, *reporter, error) {
	r := defaults()
	for _, o := range opts {
		o(r)
	}
	if err := r.validate(); err != nil {
		return "", nil, err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, r.options()...)
	if err != nil {
		return "", nil, err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return out.String(), r, nil
}

func TestTheDocumentIsUnchanged(t *testing.T) {
	for _, in := range corpus {
		for _, n := range []int{len(in) + 1, 1, 2, 3, 13} {
			out, _, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if out != in {
				t.Errorf("chunk %d changed the document:\n in: %q\nout: %q", n, in, out)
			}
		}
	}
}

func TestTheReportIsChunkInvariant(t *testing.T) {
	for _, in := range corpus {
		_, whole, err := chunked(in, len(in)+1)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		want := whole.report()
		for _, n := range []int{1, 2, 3, 13} {
			_, got, err := chunked(in, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, in, err)
			}
			if got.report() != want {
				t.Errorf("chunk %d changed the report for %q:\nwant:\n%s got:\n%s",
					n, in, want, got.report())
			}
		}
	}
}

// TestARepeatedPropertyKeepsEveryValueInOrder. Open Graph is explicit that a
// property can appear more than once and that the order matters - og:image is the
// common case - so this is the opposite choice from a repeated attribute on one
// element, where only the first copy counts.
func TestARepeatedPropertyKeepsEveryValueInOrder(t *testing.T) {
	_, r, err := chunked(page, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := r.values("og:image")
	if len(got) != 2 || got[0] != "/a.png" || got[1] != "/b.png" {
		t.Errorf("og:image = %v, want [/a.png /b.png] in that order", got)
	}
}

// TestARepeatedAttributeOnOneMetaUsesTheFirst, which is the other rule, and the
// reason this program reads through Attribute rather than through the iterator.
func TestARepeatedAttributeOnOneMetaUsesTheFirst(t *testing.T) {
	_, r, err := chunked(`<meta property="og:title" property="og:description" content="a">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.tags) != 1 {
		t.Fatalf("tags=%v", r.tags)
	}
	if r.tags[0].key != "og:title" {
		t.Errorf("key is %q, want og:title - the first copy is the one a parser keeps",
			r.tags[0].key)
	}
}

// TestBothAttributesAreAccepted: an Open Graph property is named by property and
// a Twitter one by name, and pages routinely use the wrong one. Reporting a tag
// as missing when it is on the page would be worse than being liberal here.
func TestBothAttributesAreAccepted(t *testing.T) {
	for _, markup := range []string{
		`<meta property="og:title" content="a">`,
		`<meta name="og:title" content="a">`,
	} {
		_, r, err := chunked(markup, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.values("og:title"); len(got) != 1 || got[0] != "a" {
			t.Errorf("%s -> og:title = %v", markup, got)
		}
	}

	// property wins when both are present, since it is the Open Graph one.
	_, r, err := chunked(`<meta property="og:title" name="twitter:title" content="a">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.tags) != 1 || r.tags[0].key != "og:title" {
		t.Errorf("tags=%v, want just og:title", r.tags)
	}
}

// TestOnlyTheKnownVocabulariesAreReported: a viewport meta is not social
// metadata, and reporting it would bury what is.
func TestOnlyTheKnownVocabulariesAreReported(t *testing.T) {
	_, r, err := chunked(`<meta name="viewport" content="w"><meta name="description" content="d">`+
		`<meta name="twitter:card" content="summary"><meta property="article:author" content="a">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, tg := range r.tags {
		keys = append(keys, tg.key)
	}
	if strings.Join(keys, ",") != "twitter:card,article:author" {
		t.Errorf("reported %v", keys)
	}
}

// TestKeysAndValuesAreDecoded, since a report is read by a person.
func TestKeysAndValuesAreDecoded(t *testing.T) {
	_, r, err := chunked(`<meta property="og:title" content="a &amp; b &lt;c&gt;">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.values("og:title"); len(got) != 1 || got[0] != "a & b <c>" {
		t.Errorf("og:title = %v", got)
	}
}

// TestAKeyIsMatchedWithoutRegardToCase, because pages write OG:TITLE.
func TestAKeyIsMatchedWithoutRegardToCase(t *testing.T) {
	_, r, err := chunked(`<meta property="OG:TITLE" content="a">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.values("og:title"); len(got) != 1 {
		t.Errorf("og:title = %v, want one value", got)
	}
}

// TestAnEmptyOrAbsentContentIsReportedNotCounted. A property present with no
// value is worse than absent, because it looks satisfied.
func TestAnEmptyOrAbsentContentIsReportedNotCounted(t *testing.T) {
	for _, markup := range []string{
		`<meta property="og:title" content="">`,
		`<meta property="og:title" content="   ">`,
		`<meta property="og:title">`,
	} {
		_, r, err := chunked(markup, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.tags) != 0 {
			t.Errorf("%s -> tags=%v, want none", markup, r.tags)
		}
		if total(r.skipped) != 1 {
			t.Errorf("%s -> skipped=%v, want one note", markup, r.skipped)
		}
		if !containsString(r.missing(), "og:title") {
			t.Errorf("%s -> og:title is not reported missing: %v", markup, r.missing())
		}
	}
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestMissingIsInTheOrderAsked, so two runs can be diffed and a report reads in
// the order a person set out.
func TestMissingIsInTheOrderAsked(t *testing.T) {
	_, r, err := chunked(`<meta property="og:image" content="/a">`, 1,
		func(r *reporter) { r.want = []string{"og:url", "og:title", "og:image"} })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.missing(), ","); got != "og:url,og:title" {
		t.Errorf("missing = %q", got)
	}
}

// TestTheTwitterCardAdviceOnlyAppearsWhenThereIsMetadata: a page with none does
// not need advice about Twitter, it needs Open Graph.
func TestTheTwitterCardAdvice(t *testing.T) {
	_, r, err := chunked(`<p>nothing</p>`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.report(), "twitter:card") {
		t.Errorf("advice given for a page with no metadata:\n%s", r.report())
	}

	_, r, err = chunked(`<meta property="og:title" content="a">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.report(), "twitter:card is absent") {
		t.Errorf("no advice for a page with Open Graph and no card:\n%s", r.report())
	}

	_, r, err = chunked(`<meta property="og:title" content="a"><meta name="twitter:card" content="summary">`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.report(), "twitter:card is absent") {
		t.Errorf("advice given when a card is present:\n%s", r.report())
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	if _, _, err := reportString(page, func(r *reporter) {
		r.want = []string{"title"}
	}); err == nil {
		t.Error("a want without a vocabulary prefix was accepted")
	}
}

func TestTheReportIsStable(t *testing.T) {
	_, r, err := chunked(page, len(page)+1)
	if err != nil {
		t.Fatal(err)
	}
	if first, second := r.report(), r.report(); first != second {
		t.Errorf("the report changed between calls:\n%s\n%s", first, second)
	}
}

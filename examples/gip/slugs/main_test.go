package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var corpus = []string{
	`<h2>Plain</h2>`,
	`<h2>Same</h2><h2>Same</h2><h2>Same</h2>`,
	`<h2 id="mine">Has one</h2>`,
	`<h2 id="">Empty id</h2>`,
	`<h2 id="   ">Blank id</h2>`,
	`<h2>Caf&eacute; culture</h2>`,
	`<h2>Über alles</h2>`,
	`<h2>日本語</h2>`,
	`<h2></h2>`,
	`<h2>   </h2>`,
	`<h1>One</h1><h2>Two</h2><h3>Three</h3><h4>Four</h4><h5>Five</h5><h6>Six</h6>`,
	`<h2><em>Marked</em> up</h2>`,
	`<h2>Punctuation! (and) &amp; more?</h2>`,
	`<h2>` + strings.Repeat("long ", 40) + `</h2>`,
	`<!DOCTYPE html><html><body><h2>In a document</h2></body></html>`,
	`<p>no headings</p>`,
	``,
}

func chunked(in string, n int, s *slugger) (string, error) {
	// The first pass is over the whole buffer either way; only the second is
	// chunked, since that is the one whose output is compared.
	if err := s.pass([]byte(in), noopWriter{}, false); err != nil {
		return "", err
	}
	s.planned = s.seen
	s.nth, s.assigned, s.kept = 0, 0, 0
	s.used = nil

	var out bytes.Buffer
	w, err := newWriter(&out, s)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := slugString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &slugger{})
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := slugString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, s, err := slugString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if s.assigned != 0 {
			t.Errorf("second pass of %q assigned %d id(s)", doc, s.assigned)
		}
	}
}

// TestSlugify covers the cases a slug has to survive: text that arrives as raw
// source, letters outside ASCII, and nothing usable at all.
func TestSlugify(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Plain heading", "plain-heading"},
		{"Caf&eacute; culture", "cafe-culture"},
		{"Caf&#233; culture", "cafe-culture"},
		{"Café culture", "cafe-culture"},
		{"Über alles", "uber-alles"},
		{"Straße", "strasse"},
		{"Œuvre", "oeuvre"},
		{"Punctuation! (and) &amp; more?", "punctuation-and-more"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"multiple   spaces", "multiple-spaces"},
		{"Mixed-CASE_and.dots", "mixed-case-and-dots"},
		{"日本語", ""},
		{"", ""},
		{"---", ""},
		{"2024 review", "2024-review"},
	} {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestCollisionsAreDisambiguated: two headings must never claim one anchor, or
// a link to the second silently goes to the first.
func TestCollisionsAreDisambiguated(t *testing.T) {
	got, _, err := slugString(`<h2>Same</h2><h2>Same</h2><h2>Same</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="same"`, `id="same-2"`, `id="same-3"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing: %s", want, got)
		}
	}
}

// TestEmptyHeadingsStillGetAnAnchor: a heading with no usable text still needs
// an id, or a fragment link to it cannot exist at all.
func TestEmptyHeadingsStillGetAnAnchor(t *testing.T) {
	got, _, err := slugString(`<h2></h2><h2>日本語</h2>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `id="section"`) || !strings.Contains(got, `id="section-2"`) {
		t.Errorf("expected fallback anchors: %s", got)
	}
}

// TestDocumentsOwnIDsAreKept unless asked otherwise. An author who wrote an id
// has links pointing at it.
func TestDocumentsOwnIDsAreKept(t *testing.T) {
	in := `<h2 id="mine">Has one</h2>`
	got, s, err := slugString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("changed a heading that had an id:\n got: %s\nwant: %s", got, in)
	}
	if s.kept != 1 || s.assigned != 0 {
		t.Errorf("kept=%d assigned=%d, want 1 and 0", s.kept, s.assigned)
	}

	got, _, err = slugString(in, func(s *slugger) { s.overwrite = true })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `id="mine"`) {
		t.Errorf("-overwrite did not replace the id: %s", got)
	}
}

// TestAnchorsSurviveARewording is the reason this program persists a mapping. An
// anchor that changes when someone fixes a typo breaks every inbound link, so
// the id follows the heading's position rather than its text.
func TestAnchorsSurviveARewording(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.json")

	first := `<h2>Café culture</h2><h3>Über alles</h3>`
	s := &slugger{}
	if err := s.loadMap(path); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.run([]byte(first), &out); err != nil {
		t.Fatal(err)
	}
	if err := s.saveMap(path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `id="cafe-culture"`) {
		t.Fatalf("first run: %s", out.String())
	}

	// Reword both headings entirely.
	second := `<h2>Coffee culture</h2><h3>Everything else</h3>`
	s2 := &slugger{}
	if err := s2.loadMap(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := s2.run([]byte(second), &out); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, `id="cafe-culture"`) {
		t.Errorf("the anchor did not survive the rewording: %s", got)
	}
	if !strings.Contains(got, `id="uber-alles"`) {
		t.Errorf("the second anchor did not survive: %s", got)
	}
	if s2.reused != 2 {
		t.Errorf("reused=%d, want 2", s2.reused)
	}
	if strings.Contains(got, "coffee-culture") {
		t.Errorf("a new anchor was minted for a reworded heading: %s", got)
	}
}

// TestLostAnchorsAreReported: an anchor that no longer appears is a link that no
// longer works, and no rewriter can fix that. Saying so is the only useful thing
// to do about it.
func TestLostAnchorsAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.json")

	s := &slugger{}
	if err := s.loadMap(path); err != nil {
		t.Fatal(err)
	}
	if err := s.run([]byte(`<h2>Kept</h2><h3>Removed</h3>`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveMap(path); err != nil {
		t.Fatal(err)
	}

	s2 := &slugger{}
	if err := s2.loadMap(path); err != nil {
		t.Fatal(err)
	}
	if err := s2.run([]byte(`<h2>Kept</h2>`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	report := s2.report()
	if !strings.Contains(report, "no longer present") || !strings.Contains(report, "removed") {
		t.Errorf("the lost anchor was not reported:\n%s", report)
	}
}

func TestMapRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.json")

	s := &slugger{}
	if err := s.loadMap(path); err != nil {
		t.Fatalf("a missing map should start empty: %v", err)
	}
	if err := s.run([]byte(`<h2>One</h2><h3>Two</h3>`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveMap(path); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("the map is not valid JSON: %v", err)
	}
	if m["h2#1"] != "one" || m["h3#2"] != "two" {
		t.Errorf("map = %v, want h2#1=one and h3#2=two", m)
	}

	// A corrupt map is an error rather than a silent fresh start: quietly
	// forgetting every anchor is the failure this program exists to prevent.
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&slugger{}).loadMap(path); err == nil {
		t.Error("a corrupt map was accepted")
	}
}

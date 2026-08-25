package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestPassthroughIsByteIdentical is the whole program as a test, so the corpus
// runs on every platform the examples run on.
func TestPassthroughIsByteIdentical(t *testing.T) {
	cases := Corpus()
	if len(cases) < 50 {
		t.Fatalf("the corpus has %d documents; it is meant to be a corpus", len(cases))
	}
	failures, checks := Check(cases)
	for _, f := range failures {
		t.Error(f)
	}
	if checks < 1000 {
		t.Errorf("only %d comparisons were made; the modes or write patterns "+
			"have been narrowed", checks)
	}
}

// A check that cannot fail proves nothing. This one runs the corpus through a
// mode that does change the document and requires that it is caught.
func TestTheCheckCanFail(t *testing.T) {
	mutate := func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-seen", "1")
		})}
	}
	caught := 0
	for _, c := range Corpus() {
		opts := mutate()
		if c.NeedsLenientMode {
			opts = append(opts, lolhtml.WithStrict(false))
		}
		got, err := rewrite(c.Doc, 0, opts)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got != c.Doc {
			caught++
		}
	}
	// Not every document has an element in it, so this is a floor rather than
	// the count.
	if caught < 40 {
		t.Errorf("a handler that adds an attribute to every element changed only "+
			"%d of the corpus; the comparison is not doing its job", caught)
	}
}

// The exceptions have to keep being exceptions. One that nobody can reproduce is
// stale, and carrying it forward hides whatever it was protecting.
func TestTheExceptionsAreStillExceptions(t *testing.T) {
	if stale := CheckExceptions(Corpus()); len(stale) > 0 {
		t.Errorf("listed as changed by a text handler but unchanged: %v", stale)
	}
	// And there is at least one, so the list is not empty by accident.
	n := 0
	for _, c := range Corpus() {
		if c.TextHandlerChanges {
			n++
		}
	}
	if n == 0 {
		t.Error("no document is marked as an encoding exception; that claim is untested")
	}
}

// The ambiguous documents are the other exception, and strict mode has to be the
// reason they are excluded rather than something else having gone quiet.
func TestTheAmbiguousDocumentsReallyNeedLenientMode(t *testing.T) {
	n := 0
	for _, c := range Corpus() {
		if !c.NeedsLenientMode {
			continue
		}
		n++
		if _, err := rewrite(c.Doc, 0, nil); err == nil {
			t.Errorf("%s is marked as needing lenient mode and strict mode accepted it", c.Name)
		}
		if _, err := rewrite(c.Doc, 0, []lolhtml.Option{lolhtml.WithStrict(false)}); err != nil {
			t.Errorf("%s failed even with strict mode off: %v", c.Name, err)
		}
	}
	if n == 0 {
		t.Error("no document is marked as needing lenient mode")
	}
}

// Every case needs a name, and the names have to be distinct, because a failure
// report that says "case" twice is a failure report nobody can act on.
func TestTheCorpusIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Corpus() {
		if c.Name == "" {
			t.Errorf("a case has no name: %q", c.Doc)
		}
		if seen[c.Name] {
			t.Errorf("two cases are called %q", c.Name)
		}
		seen[c.Name] = true
	}
}

// The concatenated document is what makes the corpus more than a list of
// fragments, so it has to actually be large.
func TestTheConcatenatedDocumentIsLarge(t *testing.T) {
	for _, c := range Corpus() {
		if c.Name == "everything concatenated" {
			if len(c.Doc) < 4096 {
				t.Errorf("the concatenated document is %d bytes; it is meant to be "+
					"past the size where chunking starts to matter", len(c.Doc))
			}
			if !strings.Contains(c.Doc, "<section") {
				t.Error("the concatenated document does not contain the sections")
			}
			return
		}
	}
	t.Error("there is no concatenated document")
}

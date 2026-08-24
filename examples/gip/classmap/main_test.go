package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var mapping = map[string]string{
	"btn":         "a1b2",
	"card":        "c3d4",
	"btn-primary": "e5f6",
	"café":        "g7h8",
	"same":        "same",
}

var corpus = []string{
	`<div class="card">a</div>`,
	`<div class="btn btn-primary">a</div>`,
	`<div class="btn extra">a</div>`,
	`<div class="extra">a</div>`,
	`<div class="">a</div>`,
	`<div class="  btn  ">a</div>`,
	`<div class>a</div>`,
	`<div class="caf&eacute;">a</div>`,
	`<div class="same">a</div>`,
	`<div class="btn btn">a</div>`,
	`<div>no class</div>`,
	`<div class="card"><span class="btn">nested</span></div>`,
	`<!DOCTYPE html><html><body><p class="btn">doc</p></body></html>`,
	``,
}

func chunked(in string, n int, r *renamer) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, r.options()...)
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

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := renameString(doc, mapping)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &renamer{mapping: mapping})
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

// TestRenameCannotChain is why one pass is enough. Selectors match the document
// as it arrived, so a class renamed to something the mapping also has a rule for
// is renamed once, not twice.
func TestRenameCannotChain(t *testing.T) {
	chainy := map[string]string{"a": "b", "b": "c"}

	got, _, err := renameString(`<p class="a">x</p>`, chainy)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p class="b">x</p>`; got != want {
		t.Errorf("\n got: %s\nwant: %s (renamed once, not twice)", got, want)
	}

	// And a second run does rename it again, which is the honest consequence: a
	// mapping with a chain in it is not idempotent, and that is the mapping's
	// problem rather than this program's.
	twice, _, err := renameString(got, chainy)
	if err != nil {
		t.Fatal(err)
	}
	if twice != `<p class="c">x</p>` {
		t.Errorf("a second run over a chaining mapping: got %s", twice)
	}
}

// TestIdempotentForANonChainingMapping, which is the normal case: generated
// names do not collide with original ones.
func TestIdempotentForANonChainingMapping(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := renameString(doc, mapping)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, _, err := renameString(once, mapping)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
	}
}

// TestUnmappedClassesAreKeptAndReported. Dropping a class the stylesheet no
// longer defines would hide the bug; keeping it and saying so does not.
func TestUnmappedClassesAreKeptAndReported(t *testing.T) {
	got, r, err := renameString(`<div class="btn extra other">a</div>`, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div class="a1b2 extra other">a</div>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if r.unmapped["extra"] != 1 || r.unmapped["other"] != 1 {
		t.Errorf("unmapped = %v, want both", r.unmapped)
	}
	if !strings.Contains(r.report(), "no mapping for") {
		t.Errorf("the report does not list them:\n%s", r.report())
	}
}

// TestTokenOrderIsPreserved: the order is what an author sees in the DOM, and
// reordering makes a diff unreadable for no gain.
func TestTokenOrderIsPreserved(t *testing.T) {
	got, _, err := renameString(`<div class="extra btn other card">a</div>`, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div class="extra a1b2 other c3d4">a</div>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

// TestClassNamesAreDecodedForLookup: a class written with a character reference
// is the same class, so the lookup has to be on the decoded name.
func TestClassNamesAreDecodedForLookup(t *testing.T) {
	got, r, err := renameString(`<div class="caf&eacute;">a</div>`, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<div class="g7h8">a</div>`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if r.renamed["café"] != 1 {
		t.Errorf("renamed = %v, want the decoded name", r.renamed)
	}
}

// TestAnUnchangedListIsNotRewritten, so a document with nothing to rename comes
// out byte for byte as it went in - including its original quoting and spacing.
func TestAnUnchangedListIsNotRewritten(t *testing.T) {
	for _, in := range []string{
		`<div class="extra">a</div>`,
		`<div class='single'>a</div>`,
		`<div class="  spaced  out  ">a</div>`,
		`<div class="same">a</div>`,
		`<div class="">a</div>`,
		`<div class>a</div>`,
	} {
		got, _, err := renameString(in, mapping)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was rewritten with nothing to change:\n got: %s", in, got)
		}
	}
}

func TestExtraAttributes(t *testing.T) {
	in := `<div class="btn" data-class="card">a</div>`

	got, _, err := renameString(in, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `data-class="c3d4"`) {
		t.Errorf("data-class was rewritten without being asked: %s", got)
	}

	got, _, err = renameString(in, mapping, "data-class")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `data-class="c3d4"`) {
		t.Errorf("data-class was not rewritten: %s", got)
	}
	if !strings.Contains(got, `class="a1b2"`) {
		t.Errorf("class was not rewritten: %s", got)
	}
}

// TestMappingValidation: a generated name that needs escaping would be written
// into a class attribute unescaped, so it is refused when the map is read rather
// than mangled later.
func TestMappingValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "m.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if _, err := loadMap(write(`{"a":"b"}`)); err != nil {
		t.Errorf("a good map was rejected: %v", err)
	}
	for _, bad := range []string{
		`{"a":""}`,
		`{"":"b"}`,
		`{"a":"b c"}`,
		`{"a":"b\"c"}`,
		`{"a":"b<c"}`,
		`{"a":"b&c"}`,
		`not json`,
	} {
		if _, err := loadMap(write(bad)); err == nil {
			t.Errorf("accepted a bad map: %s", bad)
		}
	}
	if _, err := loadMap(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing map file was accepted")
	}
}

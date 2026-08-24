package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<!-- prose -->`,
	`<p>a</p><!-- prose --><p>b</p>`,
	`<?php echo $x; ?>`,
	`<?xml version="1.0"?>`,
	`<!bogus>`,
	`<!x>`,
	`<! spaced>`,
	`<!--[if IE]><p>ie</p><![endif]-->`,
	`<!--[if !IE]><!--><p>m</p><!--<![endif]-->`,
	`<!-- build:keep -->`,
	`<!---->`,
	`<!-->`,
	`<script><!--not a comment--></script>`,
	`<style><!--nor this--></style>`,
	`<textarea><!--nor this--></textarea>`,
	`<!DOCTYPE html><!-- after the doctype -->`,
	`<div><!--nested--><p><!--deeper--></p></div>`,
	`<p>no comments</p>`,
	``,
}

func chunked(in string, n int, s *stripper) (string, error) {
	s.src = []byte(in)
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, s.options()...)
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
		whole, _, err := stripString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			got, err := chunked(doc, n, &stripper{keepConditional: true, keepCode: true})
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
		once, _, err := stripString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, s, err := stripString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if s.removed != 0 {
			t.Errorf("second pass of %q removed %d comment(s)", doc, s.removed)
		}
	}
}

// TestCodeSurvives is the whole reason this program is not three lines. Each of
// these is a well-formed comment to the parser, and each of them is code.
func TestCodeSurvives(t *testing.T) {
	for _, in := range []string{
		`<?php echo $x; ?>`,
		`<?xml version="1.0"?>`,
		`<?= $shorthand ?>`,
		`<!bogus>`,
		`<!x>`,
		`<! spaced>`,
	} {
		got, s, err := stripString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s was removed:\n got: %q", in, got)
		}
		if s.removed != 0 {
			t.Errorf("%s: removed=%d, want 0", in, s.removed)
		}
	}

	// And with -keep-code=false they go, which is what the flag is for.
	got, s, err := stripString(`<?php echo $x; ?>`, func(s *stripper) { s.keepCode = false })
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || s.removed != 1 {
		t.Errorf("with keepCode off: got %q removed=%d, want empty and 1", got, s.removed)
	}
}

// TestABogusCommentIsToldApartByItsSource. "<!x>" and "<!--x-->" have the same
// text, so the source range is the only discriminator - and getting it wrong
// either deletes code or keeps prose.
func TestABogusCommentIsToldApartByItsSource(t *testing.T) {
	got, s, err := stripString(`<!--x--><!x>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `<!x>` {
		t.Errorf("\n got: %q\nwant: %q (the real comment goes, the bogus one stays)", got, `<!x>`)
	}
	if s.removed != 1 {
		t.Errorf("removed=%d, want 1", s.removed)
	}
	if s.kept["bogus comment"] != 1 {
		t.Errorf("kept = %v, want one bogus comment", s.kept)
	}
}

// TestConditionalCommentsKeepBothHalves. The downlevel-revealed form is two
// comments and only the first contains "[if"; dropping the second leaves a
// conditional that never closes.
func TestConditionalCommentsKeepBothHalves(t *testing.T) {
	for _, in := range []string{
		`<!--[if IE]><p>ie</p><![endif]-->`,
		`<!--[if !IE]><!--><p>m</p><!--<![endif]-->`,
		`<!--[if lt IE 9]><script src="/s"></script><![endif]-->`,
	} {
		got, _, err := stripString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("a conditional comment was damaged:\n got: %q\nwant: %q", got, in)
		}
	}

	// A filter that matched only "[if" would keep the opening half and drop the
	// closing one, which is the mistake this program exists to avoid. Prove the
	// closing half is recognised on its own.
	if !isConditional("<![endif]") {
		t.Error("the closing half of a downlevel-revealed conditional is not recognised")
	}
	if !isConditional("[if !IE]><!") {
		t.Error("the opening half is not recognised")
	}
}

// TestCommentsInRawTextAreNotComments: text inside a script, style or textarea
// is text, so there is nothing there to remove and nothing to protect.
func TestCommentsInRawTextAreNotComments(t *testing.T) {
	for _, in := range []string{
		`<script><!--x--></script>`,
		`<style><!--x--></style>`,
		`<textarea><!--x--></textarea>`,
	} {
		got, s, err := stripString(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != in {
			t.Errorf("%s changed: %q", in, got)
		}
		if s.removed != 0 {
			t.Errorf("%s: removed=%d, want 0", in, s.removed)
		}
	}
}

func TestKeepPatterns(t *testing.T) {
	in := `<!-- build:keep --><!-- drop me -->`
	got, s, err := stripString(in, func(s *stripper) { s.keep = []string{"build:"} })
	if err != nil {
		t.Fatal(err)
	}
	if want := `<!-- build:keep -->`; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
	if s.kept["matches -keep build:"] != 1 {
		t.Errorf("kept = %v", s.kept)
	}
}

// TestProseIsActuallyRemoved, so the program does the thing it is for.
func TestProseIsActuallyRemoved(t *testing.T) {
	got, s, err := stripString(`<p>a</p><!-- a note --><p>b</p><!-- another -->`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>a</p><p>b</p>`; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
	if s.removed != 2 {
		t.Errorf("removed=%d, want 2", s.removed)
	}
}

// TestUnparseableSourceRangeKeepsTheComment: if the range cannot be checked, the
// safe answer is to keep it. Deleting something because a bounds check failed
// would be the worst of both.
func TestUnparseableSourceRangeKeepsTheComment(t *testing.T) {
	s := &stripper{keepConditional: true, keepCode: true, src: nil}
	if !s.realComment(lolhtml.SourceLocation{Start: 0, End: 10}) {
		t.Error("a range outside the buffer should be treated as prose, not deleted as code")
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

func dup(t *testing.T, doc, selector, suffix string, chunk int) (string, Stats) {
	t.Helper()
	var out bytes.Buffer
	st, err := Duplicate(strings.NewReader(doc), &out, selector, suffix, chunk)
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out.String(), st
}

// TestTheCopyIsTheSourceBytesExceptWhereAnIdWasSet is the property that makes this worth doing
// from offsets rather than from what the handlers report: the copy is the section's own bytes, so
// everything a reconstruction would lose survives - a stray end tag inside it, which reaches no
// handler at all, a comment, an entity, the whitespace.
//
// The exception is the start tag the second pass touched. Setting an attribute re-serialises the
// whole start tag (B171): each attribute keeps its own source text, and the separators between
// them are regenerated, so `id=a class = 'x'  data-k` comes back as `id="a2" class = 'x' data-k`
// - a value that was bare is quoted and the double space is one. Everything the second pass did
// not touch is untouched, which the last case here is for.
func TestTheCopyIsTheSourceBytesExceptWhereAnIdWasSet(t *testing.T) {
	for _, tt := range []struct{ name, doc, want string }{
		{
			"the start tag with the id is re-serialised, the rest is not",
			`<div id=a class = 'x'  data-k>t</div>`,
			`<div id=a class = 'x'  data-k>t</div><div id="a2" class = 'x' data-k>t</div>`,
		},
		{
			"a stray end tag inside survives",
			`<div id=a>t</span>u</div>`,
			`<div id=a>t</span>u</div><div id="a2">t</span>u</div>`,
		},
		{
			"a comment inside survives",
			`<div id=a><!-- keep --></div>`,
			`<div id=a><!-- keep --></div><div id="a2"><!-- keep --></div>`,
		},
		{
			"an entity survives undecoded",
			`<div id=a>caf&eacute;</div>`,
			`<div id=a>caf&eacute;</div><div id="a2">caf&eacute;</div>`,
		},
		{
			"an inner start tag with no id keeps its own bytes",
			`<div id=a><b class = 'y'  hidden>t</b></div>`,
			`<div id=a><b class = 'y'  hidden>t</b></div>` +
				`<div id="a2"><b class = 'y'  hidden>t</b></div>`,
		},
	} {
		got, st := dup(t, tt.doc, "div", "2", 0)
		if got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
		if st.Sections != 1 {
			t.Errorf("%s: %d sections, want 1", tt.name, st.Sections)
		}
	}
}

// TestTheIdsInTheCopyAreRenamedAndSoAreReferencesToThem. A second element with the same id is not
// a copy, it is a bug, and a reference that still points at the original is the same bug one step
// removed.
func TestTheIdsInTheCopyAreRenamedAndSoAreReferencesToThem(t *testing.T) {
	const doc = `<section id="s"><label for="i">n</label><input id="i">` +
		`<a href="#i">jump</a><a href="/page#i">away</a>` +
		`<div aria-labelledby="s i" aria-controls="i"></div></section>`
	got, _ := dup(t, doc, "section", "-copy", 0)

	copyPart := got[len(doc):]
	for _, want := range []string{
		`id="s-copy"`, `for="i-copy"`, `id="i-copy"`,
		`href="#i-copy"`, `aria-labelledby="s-copy i-copy"`, `aria-controls="i-copy"`,
	} {
		if !strings.Contains(copyPart, want) {
			t.Errorf("the copy does not contain %s:\n%s", want, copyPart)
		}
	}
	// A reference into another document is left alone: the copy does not exist there.
	if !strings.Contains(copyPart, `href="/page#i"`) {
		t.Errorf("a cross-document fragment was renamed:\n%s", copyPart)
	}
	// And the original is untouched.
	if got[:len(doc)] != doc {
		t.Errorf("the original changed:\n%s", got[:len(doc)])
	}
}

// TestTheAnswerDoesNotDependOnTheReadSize. The offsets are absolute and do not move with the
// write pattern, so a copy driven by them cannot either - including when a section straddles
// every possible read boundary.
func TestTheAnswerDoesNotDependOnTheReadSize(t *testing.T) {
	const doc = `<p>before</p><div id=a><b>t</b>x</div><p>after</p>`
	want, _ := dup(t, doc, "div", "2", 0)
	for size := 1; size <= len(doc)+1; size++ {
		got, st := dup(t, doc, "div", "2", size)
		if got != want {
			t.Errorf("read size %d:\n got %s\nwant %s", size, got, want)
		}
		if st.Sections != 1 {
			t.Errorf("read size %d: %d sections, want 1", size, st.Sections)
		}
	}
}

// TestRetentionIsTheSectionAndNotTheDocument is the claim in the package comment, measured. The
// read size is small and fixed so that the write in flight is not what dominates the peak - with
// a 32 KB read a small document arrives in one write and the figure says nothing.
func TestRetentionIsTheSectionAndNotTheDocument(t *testing.T) {
	const read = 512
	section := `<div id=a>` + strings.Repeat("s", 1000) + `</div>`
	var peaks []int
	for _, filler := range []int{2000, 20000, 400000} {
		doc := strings.Repeat("f", filler) + section + strings.Repeat("g", filler)
		_, st := dup(t, doc, "div", "2", read)
		peaks = append(peaks, st.PeakRetained)
		t.Logf("document %7d bytes, section %d, read %d: peak retained %d",
			len(doc), len(section), read, st.PeakRetained)
	}
	// The section has to be held in full to be copied, so the peak cannot be below it.
	for i, p := range peaks {
		if p < len(section) {
			t.Errorf("peak %d retained %d bytes, which is less than the %d-byte section it "+
				"copied, so the figure is not measuring what it says", i, p, len(section))
		}
		if limit := len(section) + 4*read; p > limit {
			t.Errorf("peak %d retained %d bytes, want no more than %d", i, p, limit)
		}
	}
	// The document grows 160-fold across these three and the figures do not track it. They
	// are not identical either - where the read boundaries fall around the section moves them
	// by a read or so - so the claim is the bound, not a constant.
	spread := peaks[0]
	for _, p := range peaks {
		if p-spread > 4*read || spread-p > 4*read {
			t.Errorf("retention varied by more than %d bytes across a 160-fold change in "+
				"document size: %v", 4*read, peaks)
			break
		}
	}
}

// TestAnOmittedEndTagIsNotCopied is the guard from Element.SourceLocation, tested by the shape it
// exists for. Without the name check, both list items would measure to the end of the list and
// the copies would be nested nonsense.
func TestAnOmittedEndTagIsNotCopied(t *testing.T) {
	got, st := dup(t, `<ul><li id=a>a<li id=b>b</ul>`, "li", "2", 0)
	if st.Sections != 0 {
		t.Errorf("%d sections copied, want none: neither item has its own end tag", st.Sections)
	}
	// Only the first item ever opens: while one section is open a later match looks nested
	// and is skipped, and the first one's open state is cleared by the </ul> that closed it.
	if st.Unfinished != 1 {
		t.Errorf("%d unfinished, want 1", st.Unfinished)
	}
	if got != `<ul><li id=a>a<li id=b>b</ul>` {
		t.Errorf("the document changed: %s", got)
	}
}

// TestAnUnfinishedSectionIsNotCopied. A document that stops inside the section leaves nothing to
// measure, so nothing is emitted and the run still succeeds.
func TestAnUnfinishedSectionIsNotCopied(t *testing.T) {
	got, st := dup(t, `<p>a</p><div id=a>unfinished`, "div", "2", 0)
	if st.Sections != 0 || st.Unfinished != 1 {
		t.Errorf("%d copied, %d unfinished, want 0 and 1", st.Sections, st.Unfinished)
	}
	if got != `<p>a</p><div id=a>unfinished` {
		t.Errorf("got %s", got)
	}
}

// TestNestedMatchesCopyOnlyTheOuterOne. Copying both would put the inner section into the output
// three times, which is not what "duplicate this section" means.
func TestNestedMatchesCopyOnlyTheOuterOne(t *testing.T) {
	got, st := dup(t, `<div id=o><div id=i>t</div></div>`, "div", "2", 0)
	if st.Sections != 1 {
		t.Fatalf("%d sections, want 1: %s", st.Sections, got)
	}
	if want := `<div id=o><div id=i>t</div></div><div id="o2"><div id="i2">t</div></div>`; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestSeveralSectionsAreEachCopiedOnce, and the buffer is dropped between them - so the second
// copy is the second section and not both of them.
func TestSeveralSectionsAreEachCopiedOnce(t *testing.T) {
	got, st := dup(t, `<div id=a>1</div>gap<div id=b>2</div>`, "div", "2", 3)
	if st.Sections != 2 {
		t.Fatalf("%d sections, want 2: %s", st.Sections, got)
	}
	want := `<div id=a>1</div><div id="a2">1</div>gap<div id=b>2</div><div id="b2">2</div>`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestAVoidElementIsNotASection. There is no end tag to take an extent from, so there is nothing
// to copy - and asking for an end-tag handler on one is an error rather than a no-op.
func TestAVoidElementIsNotASection(t *testing.T) {
	got, st := dup(t, `<p>a</p><img id=a src=x><br>`, "img, br", "2", 0)
	if st.Sections != 0 || st.Unfinished != 0 {
		t.Errorf("%d copied, %d unfinished, want none", st.Sections, st.Unfinished)
	}
	if got != `<p>a</p><img id=a src=x><br>` {
		t.Errorf("got %s", got)
	}
}

// TestTheCopyIsWhatTheRewriterWouldEmit, for the copy that has no ids at all: then the second
// pass changes nothing and the copy has to be byte-identical to the section.
func TestTheCopyIsWhatTheRewriterWouldEmit(t *testing.T) {
	for _, section := range []string{
		`<div>plain</div>`,
		`<div class='q' data-x>a<b>c</b></div>`,
		`<div><!--c--><p>t</p></div>`,
	} {
		got, st := dup(t, section, "div", "2", 0)
		if st.Sections != 1 {
			t.Fatalf("%q: %d sections", section, st.Sections)
		}
		if want := section + section; got != want {
			t.Errorf("%q: with no ids the copy should be identical:\n got %s\nwant %s",
				section, got, want)
		}
	}
}

// TestRetentionIsReportedHonestly. If PeakRetained were never set the retention test above would
// pass vacuously, so this checks the number is real.
func TestRetentionIsReportedHonestly(t *testing.T) {
	_, st := dup(t, `<p>`+strings.Repeat("x", 5000)+`</p>`, "div", "2", 512)
	if st.PeakRetained == 0 {
		t.Error("nothing was ever retained, so the peak figure means nothing")
	}
	if st.PeakRetained > 1024 {
		t.Errorf("with no section open, peak retention was %d bytes; it should be about one "+
			"write", st.PeakRetained)
	}
}

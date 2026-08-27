package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// breakout is the failure being reduced: inserting "</script>" into a raw-text element's own
// content would end that element, and the library refuses it.
func breakout(doc []byte) any {
	_, err := lolhtml.Rewrite(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		return e.SetInnerContent("</script>", lolhtml.HTML)
	}))
	if err == nil {
		return nil
	}
	return err.Error()
}

const haystack = `<!doctype html><html><body><div class="a"><p>hello</p><span>x</span></div>` +
	`<script>var a=1</script><!--c--><table><tr><td>cell</td></tr></table></body></html>`

// TestItFindsTheMinimalDocument. The whole failure is one element, and 157 bytes should come down
// to that element's start tag.
func TestItFindsTheMinimalDocument(t *testing.T) {
	got, st := Shrink([]byte(haystack), SameFailure([]byte(haystack), breakout))
	t.Logf("%d bytes to %d (%.1f%%) in %d rounds, %d oracle calls",
		st.Before, st.After, st.Ratio()*100, st.Rounds, st.OracleCalls)

	if string(got) != "<script>" {
		t.Errorf("reduced to %q, want the one element that fails", got)
	}
	if st.After >= st.Before {
		t.Errorf("reduced %d bytes to %d", st.Before, st.After)
	}
	if breakout(got) == nil {
		t.Error("the reduced document does not fail at all")
	}
}

// TestTheReducedDocumentFailsTheSameWay, which is the property that makes a reduction worth
// reading. Not just "fails" - the same signature.
func TestTheReducedDocumentFailsTheSameWay(t *testing.T) {
	want := breakout([]byte(haystack))
	got, _ := Shrink([]byte(haystack), SameFailure([]byte(haystack), breakout))
	if g := breakout(got); g != want {
		t.Errorf("the reduction changed the failure:\n from %v\n to   %v", want, g)
	}
}

// TestALooseOracleReducesToADifferentBug is the classic mistake, measured. An oracle that asks
// only "does it fail" will happily walk to whichever failure is nearest, and the reduction then
// describes a bug nobody was looking at.
func TestALooseOracleReducesToADifferentBug(t *testing.T) {
	// Two distinct failures in one document: a script that cannot take the insertion, and a
	// textarea whose insertion is refused with a different message.
	const twoBugs = `<div><script>a</script></div><div><style>b</style></div>`
	signature := func(doc []byte) any {
		_, err := lolhtml.Rewrite(doc, lolhtml.OnElement("script, style", func(e *lolhtml.Element) error {
			return e.SetInnerContent("</"+e.TagName()+">", lolhtml.HTML)
		}))
		if err == nil {
			return nil
		}
		return err.Error()
	}
	first := signature([]byte(twoBugs))
	if first == nil {
		t.Fatal("the document does not fail, so there is nothing to reduce")
	}

	// The strict oracle keeps the failure it started with.
	strict, _ := Shrink([]byte(twoBugs), SameFailure([]byte(twoBugs), signature))
	if got := signature(strict); got != first {
		t.Errorf("the strict oracle drifted: %v became %v", first, got)
	}
	t.Logf("strict: %q -> %v", strict, signature(strict))

	// The loose one only asks whether it fails.
	loose, _ := Shrink([]byte(twoBugs), func(doc []byte) bool { return signature(doc) != nil })
	t.Logf("loose:  %q -> %v", loose, signature(loose))

	// Both are minimal; the point is that "still fails" does not mean "still this failure",
	// so a reducer has to be given the signature. If the loose reduction happens to land on
	// the same failure here, say so rather than pretending the test proved something.
	if signature(loose) == first {
		t.Logf("the loose oracle happened to keep the same failure on this document; the " +
			"reason to pass a signature is that nothing makes it do so")
	} else {
		t.Logf("the loose oracle reduced to a different failure, which is the mistake this " +
			"documents")
	}
	if len(loose) == 0 {
		t.Error("the loose reduction is empty, which cannot fail anything")
	}
}

// TestStructureCutsPayForThemselvesOnBalance, which is a weaker claim than the one this program
// was built on, and the measurement is why. Structural cuts do not win every document: they win
// six of the nine below and lose three, so what is asserted is the total and a bound on the worst
// case, with the table logged.
//
// They also only win because the candidates are ordered by size rather than by provenance. Trying
// every structural cut before any byte cut - the obvious design, and the first one here - cost 595
// oracle calls on the deep wrapper against 34 for halving, because the cuts structure proposes are
// mostly the ones that remove the failure.
func TestStructureCutsPayForThemselvesOnBalance(t *testing.T) {
	docs := []struct{ name, doc string }{
		{"small mixed", haystack},
		{"deep wrapper", strings.Repeat("<div>", 30) + "<script>a</script>" + strings.Repeat("</div>", 30)},
		{"many siblings", strings.Repeat("<p>text here</p>", 40) + "<script>a</script>"},
		{"failure first", "<script>a</script>" + strings.Repeat("<p>text here</p>", 40)},
		{"big table", "<table>" + strings.Repeat("<tr><td>cell</td></tr>", 30) + "</table><script>a</script>"},
		{"long attributes", `<div data-x="` + strings.Repeat("y", 300) + `"><script>a</script></div>`},
		{"comments", strings.Repeat("<!-- a comment -->", 30) + "<script>a</script>"},
		{"nested scripts", "<div><script>a</script></div><div><script>b</script></div>"},
		{"malformed prefix", strings.Repeat("</div>", 30) + "<script>a</script>"},
	}

	totalStruct, totalBytes, structWins := 0, 0, 0
	for _, d := range docs {
		doc := []byte(d.doc)
		if breakout(doc) == nil {
			t.Errorf("%s does not fail, so it measures nothing", d.name)
			continue
		}
		oracle := SameFailure(doc, breakout)
		a, sa := Shrink(doc, oracle)
		b, sb := ShrinkBytesOnly(doc, oracle)

		if string(a) != string(b) {
			t.Errorf("%s: the two modes disagree on the answer: %q against %q", d.name, a, b)
		}
		if breakout(a) == nil || breakout(b) == nil {
			t.Errorf("%s: a reduction does not fail", d.name)
		}
		totalStruct += sa.OracleCalls
		totalBytes += sb.OracleCalls
		if sa.OracleCalls < sb.OracleCalls {
			structWins++
		}
		t.Logf("%-17s %2d bytes; %3d calls with structure, %3d without", d.name, sa.After,
			sa.OracleCalls, sb.OracleCalls)

		// The bound is what stops a regression to the provenance-ordered version, which was
		// 17 times worse on one of these.
		if sa.OracleCalls > sb.OracleCalls*3 {
			t.Errorf("%s: %d calls with structure against %d without, which is the shape of "+
				"the bug where structural cuts are all tried first", d.name,
				sa.OracleCalls, sb.OracleCalls)
		}
	}

	t.Logf("total: %d calls with structure, %d without; structure won %d of %d",
		totalStruct, totalBytes, structWins, len(docs))
	if totalStruct >= totalBytes {
		t.Errorf("structural cuts cost %d oracle calls in total against %d without them, so "+
			"they are not paying for themselves", totalStruct, totalBytes)
	}
}

// TestADocumentThatDoesNotFailIsReturnedUnchanged. Reducing something that passes would produce a
// smaller document that fails for an unrelated reason, which is worse than doing nothing.
func TestADocumentThatDoesNotFailIsReturnedUnchanged(t *testing.T) {
	const fine = `<p>nothing here fails</p>`
	got, st := Shrink([]byte(fine), SameFailure([]byte(fine), breakout))
	if string(got) != fine {
		t.Errorf("reduced a passing document to %q", got)
	}
	if st.After != st.Before {
		t.Errorf("%d bytes became %d", st.Before, st.After)
	}
	if st.OracleCalls != 1 {
		t.Errorf("%d oracle calls, want one - the first answer settles it", st.OracleCalls)
	}
}

// TestItIsDeterministic, because a reducer whose answer moves is a reducer nobody can quote in a
// bug report.
func TestItIsDeterministic(t *testing.T) {
	oracle := SameFailure([]byte(haystack), breakout)
	first, s1 := Shrink([]byte(haystack), oracle)
	for i := 0; i < 3; i++ {
		got, st := Shrink([]byte(haystack), oracle)
		if string(got) != string(first) {
			t.Fatalf("run %d gave %q, first gave %q", i, got, first)
		}
		if st.OracleCalls != s1.OracleCalls {
			t.Errorf("run %d took %d oracle calls, first took %d", i, st.OracleCalls, s1.OracleCalls)
		}
	}
}

// TestItSurvivesMalformedInput. A fuzz finding is usually malformed, and the structural cuts come
// from a rewrite of it - so an unterminated tag, a stray end tag and an omitted end tag all have
// to leave the reducer working rather than looping or panicking.
func TestItSurvivesMalformedInput(t *testing.T) {
	for _, doc := range []string{
		`<script>a`,
		`</div><script>a</script>`,
		`<ul><li><script>a</script><li>b</ul>`,
		`<div class="unterminated<script>a</script>`,
		`<script>a</script>` + strings.Repeat("<", 50),
		`<!--<script>a</script>`,
	} {
		if breakout([]byte(doc)) == nil {
			t.Logf("%q does not fail; skipping", doc)
			continue
		}
		got, st := Shrink([]byte(doc), SameFailure([]byte(doc), breakout))
		if breakout(got) == nil {
			t.Errorf("%q reduced to %q, which does not fail", doc, got)
		}
		if st.After > st.Before {
			t.Errorf("%q grew to %d bytes", doc, st.After)
		}
		t.Logf("%-42q -> %q (%d calls)", doc, got, st.OracleCalls)
	}
}

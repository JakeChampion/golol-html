package main

import (
	"strings"
	"testing"
)

func doc(paragraphs int) []byte {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><body>")
	for i := 0; i < paragraphs; i++ {
		b.WriteString("<p>paragraph with some text</p><!--c-->")
	}
	b.WriteString("</body></html>")
	return []byte(b.String())
}

// TestTheOutputIsTheSameEitherWay, which is the guarantee the program exists to check.
func TestTheOutputIsTheSameEitherWay(t *testing.T) {
	d := doc(40)
	for _, chunk := range []int{1, 2, 3, 7, 13, 64, 1024} {
		res := Compare(d, chunk, false)
		if !res.Identical {
			t.Errorf("chunk %d: %s", chunk, res.Diff)
		}
		if res.Memory.Err != nil || res.Streamed.Err != nil {
			t.Errorf("chunk %d: %v %v", chunk, res.Memory.Err, res.Streamed.Err)
		}
	}
	// And the rewrite really did something, or comparing outputs proves nothing.
	res := Compare(d, 64, false)
	if !strings.Contains(string(res.Memory.Output), `data-seen="1"`) {
		t.Errorf("the rewrite made no change: %q", firstBytes(res.Memory.Output))
	}
}

func firstBytes(b []byte) string {
	if len(b) > 60 {
		return string(b[:60])
	}
	return string(b)
}

// TestElementCommentAndDoctypeCountsAreTheSame, and text-node counts too: those are the
// invocation counts a rewrite can rely on.
func TestElementCommentAndDoctypeCountsAreTheSame(t *testing.T) {
	d := doc(40)
	for _, chunk := range []int{1, 3, 64, 1024} {
		res := Compare(d, chunk, false)
		m, s := res.Memory.Counts, res.Streamed.Counts
		if m.Elements != s.Elements {
			t.Errorf("chunk %d: %d elements in memory, %d streamed", chunk, m.Elements, s.Elements)
		}
		if m.Comments != s.Comments {
			t.Errorf("chunk %d: %d comments in memory, %d streamed", chunk, m.Comments, s.Comments)
		}
		if m.Doctypes != s.Doctypes {
			t.Errorf("chunk %d: %d doctypes in memory, %d streamed", chunk, m.Doctypes, s.Doctypes)
		}
		if m.Nodes != s.Nodes {
			t.Errorf("chunk %d: %d text nodes in memory, %d streamed", chunk, m.Nodes, s.Nodes)
		}
	}
}

// TestTextChunkCountsDiffer, which is the one that does not carry over - and the reason
// anything accumulating text has to accumulate to the boundary chunk.
func TestTextChunkCountsDiffer(t *testing.T) {
	d := doc(40)
	res := Compare(d, 8, false)
	if res.Streamed.Counts.Texts <= res.Memory.Counts.Texts {
		t.Errorf("%d text chunks in memory and %d in 8-byte writes; the streamed pass "+
			"should see more", res.Memory.Counts.Texts, res.Streamed.Counts.Texts)
	}
	// Smaller writes, more chunks: the boundaries follow the writes.
	small := Compare(d, 4, false).Streamed.Counts.Texts
	large := Compare(d, 1024, false).Streamed.Counts.Texts
	if small <= large {
		t.Errorf("4-byte writes gave %d chunks and 1024-byte writes %d", small, large)
	}
	// And the node count is stable across all of it.
	if a, b := Compare(d, 4, false).Streamed.Counts.Nodes, Compare(d, 1024, false).Streamed.Counts.Nodes; a != b {
		t.Errorf("text nodes: %d at 4-byte writes, %d at 1024", a, b)
	}
}

// TestTheMemoryFloorIsWhereTheTwoShapesPartCompany.
func TestTheMemoryFloorIsWhereTheTwoShapesPartCompany(t *testing.T) {
	d := doc(200)
	res := Compare(d, 1024, true)
	if res.Memory.Floor == 0 || res.Streamed.Floor == 0 {
		t.Fatalf("no floor found: %d and %d", res.Memory.Floor, res.Streamed.Floor)
	}
	if res.Streamed.Floor <= res.Memory.Floor {
		t.Errorf("the floor is %d in memory and %d at 1024-byte writes; the streamed "+
			"shape was expected to need more", res.Memory.Floor, res.Streamed.Floor)
	}
	// Which is the practical point: a limit chosen with the in-memory shape is too
	// small for the streamed one.
	if p := run("check", d, 1024, res.Memory.Floor); p.Err == nil {
		t.Errorf("the in-memory floor %d also completed at 1024-byte writes, so this "+
			"document does not show the difference", res.Memory.Floor)
	}
}

// TestOKIsAboutTheGuaranteesAndNotTheDifferences: the text-chunk counts differ by design,
// and that is not a failure.
func TestOKIsAboutTheGuaranteesAndNotTheDifferences(t *testing.T) {
	res := Compare(doc(20), 8, false)
	if res.Memory.Counts.Texts == res.Streamed.Counts.Texts {
		t.Skip("this document does not split its text nodes at this write size")
	}
	if !res.OK() {
		t.Errorf("a differing text-chunk count made the comparison fail: %s", res)
	}
}

// TestTheReportSaysWhichLinesAreGuarantees.
func TestTheReportSaysWhichLinesAreGuarantees(t *testing.T) {
	s := Compare(doc(20), 8, true).String()
	for _, want := range []string{"output", "element handlers", "text handlers", "text nodes",
		"comment handlers", "doctype handlers", "memory floor", "both ways"} {
		if !strings.Contains(s, want) {
			t.Errorf("the report is missing %q:\n%s", want, s)
		}
	}
}

// TestAnEmptyDocumentIsNotAnError.
func TestAnEmptyDocumentIsNotAnError(t *testing.T) {
	res := Compare(nil, 64, false)
	if !res.Identical || !res.OK() {
		t.Errorf("%s", res)
	}
}

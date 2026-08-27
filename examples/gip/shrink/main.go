// Command shrink reduces a failing document to its essence: the smallest input it can find that
// still fails the same way. It is delta debugging, with the rewriter itself proposing the cuts.
//
// A fuzz target hands back a document of a few hundred bytes and the interesting part is twenty of
// them. Cutting by hand is slow and cutting at random is worse, because most cuts to HTML produce
// a document that fails for a different reason - truncate a start tag and everything after it
// becomes attribute value, and the reduced document "still fails" while saying nothing about the
// bug. So the two things this needs are a way to propose cuts that keep the document a document,
// and a predicate strict enough to notice when the failure changed.
//
// The cuts come from the rewrite. An element's extent - its start tag's Start to its own end tag's
// End, guarded by name - is a byte range that can be removed whole, and so are its start tag, a
// comment, a doctype and a text node. Those are proposed first, largest first; byte ranges are the
// fallback for what structure cannot reach.
//
// An attribute is not among them, and cannot be: an Attribute is a name and a value with no source
// location, and Element.SourceLocation covers the whole start tag. So "remove this one attribute"
// is not a cut this can propose from the API, and attributes are reduced by the byte fallback
// working inside the start tag - which gets there, in more oracle calls than a range would have
// taken.
//
// The predicate is the caller's, and the interface is deliberately narrow: given a candidate, does
// it fail the same way? A predicate that only asks "does it fail" reduces to whichever failure is
// nearest. Measured on a document holding two of them, `<div><script>a</script></div>` beside
// `<div><style>b</style></div>`: the strict oracle reduces to `<script>` and the loose one to
// `<style>`, a different error with different advice in it.
//
// How much the structural cuts are worth is smaller than it sounds, and only true with the
// candidates ordered by size rather than by provenance. Oracle calls to reach the same eight-byte
// answer, with structural cuts and without:
//
//	document           with   without
//	big table            28        42
//	long attributes      25        33
//	nested scripts       29        44
//	failure first        33        41
//	small mixed          33        36
//	many siblings        28        34
//	comments             33        29
//	malformed prefix     44        38
//	deep wrapper         55        34
//	total               308       331
//
// Six of nine, and a modest total. The first version of this program tried every structural cut
// before any byte cut, which is the obvious design and is much worse: 595 calls on the deep wrapper
// against 34 for plain halving, because the cuts structure proposes are mostly the ones that remove
// the failure. Sorting every candidate by size puts a big removable element ahead of a blind half,
// and a blind half ahead of the element that holds the bug.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Oracle reports whether a candidate document still exhibits the failure being reduced.
type Oracle func(doc []byte) bool

// Stats records what the reduction cost and achieved.
type Stats struct {
	Before      int
	After       int
	OracleCalls int
	Rounds      int
}

// Ratio is how much of the document survived.
func (s Stats) Ratio() float64 {
	if s.Before == 0 {
		return 1
	}
	return float64(s.After) / float64(s.Before)
}

// cut is a byte range that can be removed as a unit, with a note about what it is.
type cut struct {
	start, end int
	kind       string
}

func (c cut) len() int { return c.end - c.start }

// structuralCuts asks the rewriter what can be removed whole: elements with their content,
// comments, doctypes, and text nodes. Nothing here depends on the document being well formed - an
// element whose end tag never arrives simply produces no extent, and is left to the byte fallback.
func structuralCuts(doc []byte) []cut {
	var cuts []cut
	add := func(kind string, start, end int) {
		if start >= 0 && end <= len(doc) && end > start {
			cuts = append(cuts, cut{start, end, kind})
		}
	}

	var textStart = -1
	var textEnd int
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			l := d.SourceLocation()
			add("doctype", l.Start, l.End)
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			l := c.SourceLocation()
			add("comment", l.Start, l.End)
			return nil
		}),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			l := tc.SourceLocation()
			if textStart < 0 {
				textStart = l.Start
			}
			if l.End > textEnd {
				textEnd = l.End
			}
			if tc.IsLastInTextNode() {
				add("text", textStart, textEnd)
				textStart, textEnd = -1, 0
			}
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			start := e.SourceLocation().Start
			name := e.TagName()
			// The start tag alone is always a candidate: removing it turns the element's
			// content into its parent's, which is often the smaller reproduction.
			add("start tag", start, e.SourceLocation().End)
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != name {
					// An omitted end tag hands over an enclosing element's tag, and
					// this arithmetic would then measure to the end of that one.
					return nil
				}
				add("element", start, t.SourceLocation().End)
				return nil
			})
		}),
	)
	if err != nil {
		return nil
	}
	if _, err := w.Write(doc); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}

	// Largest first: one big cut beats many small ones, and a cut that lands takes every
	// smaller cut inside it with it.
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].len() > cuts[j].len() })
	return cuts
}

// byteCuts is the fallback: halves, then quarters, and so on, in the manner of ddmin. It reaches
// what structure cannot - an unterminated tag, a stray end tag, the bytes of an attribute value.
func byteCuts(doc []byte, granularity int) []cut {
	if granularity < 1 {
		granularity = 1
	}
	size := len(doc) / granularity
	if size < 1 {
		return nil
	}
	var cuts []cut
	for start := 0; start < len(doc); start += size {
		end := min(start+size, len(doc))
		cuts = append(cuts, cut{start, end, "bytes"})
	}
	return cuts
}

// Shrink reduces doc to a smaller document the oracle still rejects, and reports what it did.
//
// It is greedy and it converges: each round tries every candidate cut against the current
// document, keeps the ones that hold, and stops when a whole round changes nothing.
func Shrink(doc []byte, fails Oracle) ([]byte, Stats) {
	return shrinkWith(doc, fails, true)
}

// ShrinkBytesOnly is the same reduction without the structural cuts, which is what a reducer that
// knows nothing about HTML does. It exists to be compared against Shrink.
func ShrinkBytesOnly(doc []byte, fails Oracle) ([]byte, Stats) {
	return shrinkWith(doc, fails, false)
}

func shrinkWith(doc []byte, fails Oracle, useStructure bool) ([]byte, Stats) {
	st := Stats{Before: len(doc)}
	calls := 0
	ask := func(candidate []byte) bool {
		calls++
		return fails(candidate)
	}

	if !ask(doc) {
		// Nothing to reduce: the document does not fail, so any smaller one that does
		// would be a different bug.
		st.After = len(doc)
		st.OracleCalls = calls
		return doc, st
	}

	current := append([]byte(nil), doc...)
	for {
		st.Rounds++
		progress := false

		// One list, largest cut first, whatever proposed it. Trying every structural cut
		// before any byte cut is worse and measurably so: the cuts structure proposes are
		// mostly the ones that remove the failure, so a document wrapped in thirty divs
		// spends 595 oracle calls rejecting them where halving takes 34. Sorting by size
		// puts a big removable element ahead of a blind half, and a blind half ahead of the
		// element that holds the bug.
		var candidates []cut
		if useStructure {
			candidates = append(candidates, structuralCuts(current)...)
		}
		for granularity := 2; granularity <= 32; granularity *= 2 {
			candidates = append(candidates, byteCuts(current, granularity)...)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].len() > candidates[j].len()
		})

		for _, c := range candidates {
			if c.end > len(current) {
				continue
			}
			candidate := remove(current, c)
			if len(candidate) < len(current) && ask(candidate) {
				current = candidate
				progress = true
				break
			}
		}
		if !progress {
			break
		}
	}

	st.After = len(current)
	st.OracleCalls = calls
	return current, st
}

func remove(doc []byte, c cut) []byte {
	out := make([]byte, 0, len(doc)-c.len())
	out = append(out, doc[:c.start]...)
	return append(out, doc[c.end:]...)
}

// SameFailure builds an oracle that holds only when a candidate fails the same way as the original
// - which is the difference between reducing a bug and reducing to the nearest other bug.
//
// signature is whatever a caller can compare: an error string, a handler's observation, a hash. A
// nil signature means "did not fail".
func SameFailure(original []byte, signature func([]byte) any) Oracle {
	want := fmt.Sprint(signature(original))
	return func(candidate []byte) bool {
		got := signature(candidate)
		return got != nil && fmt.Sprint(got) == want
	}
}

func main() {
	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shrink:", err)
		os.Exit(1)
	}

	// The demonstration oracle: the rewrite returns an error. A real caller passes their own.
	signature := func(candidate []byte) any {
		_, err := lolhtml.Rewrite(candidate, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			return e.SetInnerContent("</script>", lolhtml.HTML)
		}))
		if err == nil {
			return nil
		}
		return err.Error()
	}

	reduced, st := Shrink(doc, SameFailure(doc, signature))
	if _, err := os.Stdout.Write(reduced); err != nil {
		fmt.Fprintln(os.Stderr, "shrink:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\nshrink: %d bytes to %d (%.1f%%) in %d rounds, %d oracle calls\n",
		st.Before, st.After, st.Ratio()*100, st.Rounds, st.OracleCalls)
}

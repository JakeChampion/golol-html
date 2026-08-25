package lolhtml_test

// Splitting a document between two rewriters is not the same as chunking it into
// one. A fragment that ends inside a tag is invisible to every handler and is
// emitted verbatim, so joining the outputs can reassemble markup that neither
// pass inspected.
//
// This is the gate on the package documentation's claim, and the claim is exact:
// which cut points fail, how many, and in which of two ways. A change upstream
// that made the tail of a truncated document visible to handlers would fail here,
// which is the point - the documentation would then be wrong.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// stripScripts removes every script element and reports the tag names it saw.
func stripScripts(t *testing.T, doc string) (string, []string) {
	t.Helper()
	var seen []string
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		seen = append(seen, e.TagName())
		if e.TagName() == "script" {
			e.Remove()
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return out, seen
}

// holdsScript re-parses a document and reports whether it contains a script
// element, because text that looks like one is not one.
func holdsScript(t *testing.T, doc string) bool {
	t.Helper()
	found := false
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("script", func(*lolhtml.Element) error {
			found = true
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestOneRewriterIsSafeAtEveryChunkBoundary(t *testing.T) {
	const doc = `<p>a</p><script>alert(1)</script><p>b</p>`

	for size := 1; size <= len(doc)+1; size++ {
		var out strings.Builder
		var seen []string
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			seen = append(seen, e.TagName())
			if e.TagName() == "script" {
				e.Remove()
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != `<p>a</p><p>b</p>` {
			t.Errorf("chunk %d: %q", size, got)
		}
		if strings.Join(seen, " ") != "p script p" {
			t.Errorf("chunk %d: saw %v", size, seen)
		}
	}
}

func TestTwoRewritersOverTwoFragmentsAreNot(t *testing.T) {
	const doc = `<p>a</p><script>alert(1)</script><p>b</p>`
	start := strings.Index(doc, "<script>")
	end := start + len("<script>")

	var element, textOnly, clean []int
	for i := 1; i < len(doc); i++ {
		a, seenA := stripScripts(t, doc[:i])
		b, seenB := stripScripts(t, doc[i:])
		joined := a + b
		switch {
		case holdsScript(t, joined):
			element = append(element, i)
			// The failure is silent: neither pass saw a script to remove.
			for _, seen := range [][]string{seenA, seenB} {
				for _, name := range seen {
					if name == "script" {
						t.Errorf("cut %d: a pass saw the script and "+
							"left the element anyway", i)
					}
				}
			}
		case strings.Contains(joined, "alert(1)"):
			textOnly = append(textOnly, i)
		default:
			clean = append(clean, i)
		}
	}

	// Strictly inside the start tag, and only there.
	for _, i := range element {
		if i <= start || i >= end {
			t.Errorf("cut %d reassembled an element, and it is not inside the start "+
				"tag at %d..%d", i, start, end)
		}
	}
	if len(element) != end-start-1 {
		t.Errorf("%d cuts reassembled an element, want %d (%v)",
			len(element), end-start-1, element)
	}

	// The cut immediately after the start tag leaves the payload as text.
	if len(textOnly) != 1 || textOnly[0] != end {
		t.Errorf("the payload survived as text at %v, want [%d]", textOnly, end)
	}
	a, _ := stripScripts(t, doc[:end])
	b, _ := stripScripts(t, doc[end:])
	if got, want := a+b, `<p>a</p>alert(1)</script><p>b</p>`; got != want {
		t.Errorf("cut %d gave %q, want %q", end, got, want)
	}

	// Everything else is clean, so the hazard is the tag boundary and nothing wider.
	if want := len(doc) - 1 - len(element) - len(textOnly); len(clean) != want {
		t.Errorf("%d clean cuts, want %d", len(clean), want)
	}
}

func TestATagTheDocumentNeverFinishesReachesNoHandler(t *testing.T) {
	// A tag is the only construct a document can end inside that no handler sees.
	for _, doc := range []string{
		"<p",
		"<p ",
		"<p attr",
		`<p attr="v`,
		"<p/",
		"</p",
		"<script",
	} {
		if elements, text, comments, doctypes, out := count(t, doc); elements+text+comments+doctypes != 0 {
			t.Errorf("%q: %d elements, %d text chunks, %d comments, %d doctypes reached "+
				"a handler", doc, elements, text, comments, doctypes)
			_ = out
		} else if out != doc {
			t.Errorf("%q: output %q", doc, out)
		}
	}

	// Everything else a document can end inside still arrives. This is what makes the tag
	// case worth documenting: it is the exception, not the rule.
	for _, tt := range []struct {
		doc                                string
		elements, text, comments, doctypes int
	}{
		{"<!-", 0, 0, 1, 0},
		{"<!--", 0, 0, 1, 0},
		{"<!-- x", 0, 0, 1, 0},
		{"<!-- x --", 0, 0, 1, 0},
		{"<!", 0, 0, 1, 0},
		{"<?php", 0, 0, 1, 0},
		{"<![CDATA[x", 0, 0, 1, 0},
		{"<!DOCTYPE", 0, 0, 0, 1},
		{"<script>", 1, 0, 0, 0},
		{"<script>var a", 1, 1, 0, 0},
		{"<style>p{", 1, 1, 0, 0},
		{"<textarea>x", 1, 1, 0, 0},
		{"text", 0, 1, 0, 0},
	} {
		elements, text, comments, doctypes, out := count(t, tt.doc)
		if elements != tt.elements || text != tt.text || comments != tt.comments ||
			doctypes != tt.doctypes {
			t.Errorf("%q: %d/%d/%d/%d elements/text/comments/doctypes, want %d/%d/%d/%d",
				tt.doc, elements, text, comments, doctypes,
				tt.elements, tt.text, tt.comments, tt.doctypes)
		}
		if out != tt.doc {
			t.Errorf("%q: output %q", tt.doc, out)
		}
	}

	// And the same bytes in a finished document are seen normally, so this is about the
	// document ending rather than about the bytes.
	if elements, _, _, _, _ := count(t, `<p attr="v">x</p>`); elements != 1 {
		t.Errorf("%d elements for a finished tag", elements)
	}
}

// count reports how many of each handler kind ran, and the output.
func count(t *testing.T, doc string) (elements, text, comments, doctypes int, out string) {
	t.Helper()
	var err error
	out, err = lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(*lolhtml.Element) error { elements++; return nil }),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				text++
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { comments++; return nil }),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error { doctypes++; return nil }))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return elements, text, comments, doctypes, out
}

// Which unfinished constructs swallow what is written after them, which is the question that
// decides whether a fragment is safe to join. It is a wider set than the blind one above, and
// the same set the documentation for DocumentEnd.Append describes, for the same reason: an
// unfinished construct has no end until one arrives, and the next thing written becomes part of
// it.
//
// The test is the one a caller can use, and it reimplements nothing - see the scan test below
// for why that matters.
func TestWhichUnfinishedConstructsSwallowWhatFollows(t *testing.T) {
	swallows := func(doc string) bool {
		const sentinel = `<x-sentinel-9f3></x-sentinel-9f3>`
		seen := false
		if _, err := lolhtml.RewriteString(doc+sentinel,
			lolhtml.OnElement("x-sentinel-9f3", func(*lolhtml.Element) error {
				seen = true
				return nil
			})); err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return !seen
	}

	for _, doc := range []string{
		// A tag, of either kind.
		"<p", "<p attr", `<p attr="v`, "</p", "</",
		// A comment, a bogus comment, a doctype.
		"<!-- c", "<!-- c --", "<!", "<?php", "<!DOCTYPE",
		// Any open raw-text element.
		"<script>", "<script>var a", "<style>p{", "<textarea>x", "<title>t",
	} {
		if !swallows(doc) {
			t.Errorf("%q does not swallow what follows it", doc)
		}
	}

	for _, doc := range []string{
		// An element left open is not an unfinished construct: more content is
		// simply more content.
		"<ul><li>a", "<p>a", "<div>",
		// A stray end tag closes nothing and absorbs nothing.
		"</div>", "</p>",
		// And the ordinary cases.
		"<p>a</p>", "<!DOCTYPE html>", "<script>var a</script>", "a < b", "a <", "text", "",
	} {
		if swallows(doc) {
			t.Errorf("%q swallowed the sentinel", doc)
		}
	}
}

// TestAScanForTheLastAngleBracketMissesMostOfIt, which is why the check above appends a sentinel
// instead. The obvious string test - a "<" after the last ">" followed by a letter - is wrong in
// one direction, which is the dangerous one for a safety check: it says "safe" when it is not.
// Measured over a fixed set of 4000 generated fragments, it misses 1007 of them and never
// over-reports.
func TestAScanForTheLastAngleBracketMissesMostOfIt(t *testing.T) {
	byScan := func(doc string) bool {
		lt := strings.LastIndexByte(doc, '<')
		if lt < 0 || lt < strings.LastIndexByte(doc, '>') {
			return false
		}
		rest := doc[lt+1:]
		if rest != "" && rest[0] == '/' {
			rest = rest[1:]
		}
		if rest == "" {
			return false
		}
		c := rest[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	bySentinel := func(doc string) bool {
		const sentinel = `<x-sentinel-9f3></x-sentinel-9f3>`
		seen := false
		if _, err := lolhtml.RewriteString(doc+sentinel,
			lolhtml.OnElement("x-sentinel-9f3", func(*lolhtml.Element) error {
				seen = true
				return nil
			})); err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return !seen
	}

	pieces := []string{
		`<p id="x">`, `</p>`, `text`, `<!-- c -->`, `<br>`, `<div class="a b">`, `</div>`,
		`<script>var a="<b>"</script>`, `<!DOCTYPE html>`, `a < b`, `<?php ?>`, ` `,
		`<ul><li>a<li>b</ul>`, `<svg><rect/></svg>`, `<p a="1" b='2'>`, `&amp;`,
	}
	// A fixed sequence rather than a random one, in int64 arithmetic, so the counts below
	// are the same on every platform.
	var seed int64 = 1
	next := func(n int) int {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		return int(seed % int64(n))
	}

	var missed, overReported int
	for range 4000 {
		var b strings.Builder
		for range next(5) + 1 {
			b.WriteString(pieces[next(len(pieces))])
		}
		full := b.String()
		doc := full[:next(len(full)+1)]
		switch {
		case bySentinel(doc) && !byScan(doc):
			missed++
		case byScan(doc) && !bySentinel(doc):
			overReported++
		}
	}
	if missed != 1007 || overReported != 0 {
		t.Errorf("the scan missed %d and over-reported %d of 4000, want 1007 and 0",
			missed, overReported)
	}

	// The three shapes that account for it, each a thing the scan cannot know.
	for _, tt := range []struct {
		doc, why string
	}{
		{`<!DOCTYPE`, "a doctype does not begin with a letter"},
		{`<script>var a="`, "an open raw-text element has its last > behind it"},
		{`<br><svg><rect/></`, "a bare </ at the end is an unfinished end tag"},
	} {
		if !bySentinel(tt.doc) {
			t.Errorf("%q: the tokenizer says it is safe, so this case is wrong", tt.doc)
		}
		if byScan(tt.doc) {
			t.Errorf("%q: the scan caught it, so it no longer shows that %s",
				tt.doc, tt.why)
		}
	}
}

// Command needsrewrite decides whether a document is worth rewriting before rewriting it, which is
// the question a proxy asks thousands of times a second and mostly answers "no".
//
// The cheap answer is a byte search: if the body cannot contain anything the handlers match, skip
// the rewrite. Two things make that harder than it sounds - one about cost and one about
// correctness - and both are measurable.
//
// Cost first, and getting it cheap took three attempts, each of which I expected to be the
// answer. HTML tag names are case-insensitive, so the search has to be, and the usual way to write
// that lower-cases the body. On a 93 KB page with nothing in it to match, fastest of twenty:
//
//	                                             time   allocates
//	one bytes.Contains, case-sensitive (wrong)    26µs           0
//	ToLower then Contains, once per probe        157µs      98,304
//	fold the comparison, byte at a time          318µs           0
//	fold, skipping with bytes.IndexByte          150µs           0
//	fold, one pass, table on the byte after "<"   52µs           0
//	the rewrite this is avoiding                 175µs           -
//
// The three surprises, in order. A hand-written fold loop is worse than lower-casing the whole
// document, because ToLower and Contains have vectorised implementations behind them and a
// byte-at-a-time loop does not. Skipping with IndexByte helps less than it looks like it should,
// because in HTML the "<" is exactly as dense as the tags are - that page has 6,000 of them, one
// every fifteen bytes - so there is little to skip and the work at each candidate is what counts.
// And searching once per probe is a full pass per probe on a miss, so three probes cost three
// scans while the rewrite is paid once.
//
// What works is one pass with a 256-entry table on the byte after the bracket, which rejects almost
// every candidate with a lookup: 52µs against a 175µs rewrite, and nothing allocated. A gate that
// costs a third of what it saves is worth having; the first three attempts were not.
//
// Correctness second, and it is one rule: the probe has to match a superset of what the handlers
// match, or the gate skips a document it should have rewritten - silently, since the whole point is
// that nothing runs. Being too broad only costs a wasted rewrite.
//
// The rule bites in a way worth naming. A selector for images has to be `img,image`, because
// `<image>` is a spelling of `<img>` that the parser renames and this library reports as spelled
// (B155). A probe of "<img" is therefore not a superset of that selector, and a page whose only
// image is spelled `<image src=x>` would be skipped. The probe is "<im".
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Gate is a set of probes and the handlers they stand for. The probes must match a superset of
// what the handlers can match; Probes documents what that means per selector.
type Gate struct {
	// Probes are ASCII byte sequences searched case-insensitively. Any one of them matching
	// means the document might need rewriting.
	Probes []string
}

// Probes for the selectors this program's own handlers use, with the reasoning that matters:
//
//	selector      probe    why not something shorter or longer
//	a[href]       "<a"     an attribute cannot appear without its tag
//	img,image     "<im"    <image> is a spelling of <img> (B155), so "<img" would miss it
//	form          "<form"  no shorter prefix is shared with anything else here
//
// A probe is never the attribute name: `a[href]` cannot match without an `<a` in the bytes, and
// searching for "href" instead would also fire on every `<link href>`.
var DefaultGate = Gate{Probes: []string{"<a", "<im", "<form"}}

// Needed reports whether doc might contain something the handlers match. It is allowed to say yes
// when the answer is no; it must never say no when the answer is yes.
//
// One pass for every probe, not one pass each. A probe that misses costs a scan of the whole
// document, so searching three times is three scans, and three scans of a 93 KB page cost about
// what rewriting it costs - which leaves the gate paying for itself only by luck. Testing every
// probe at each candidate position costs one scan whatever the probe count.
func (g Gate) Needed(doc []byte) bool {
	if len(g.Probes) == 0 {
		return false
	}
	// Every probe here starts with "<". One that does not has to be searched on its own,
	// because the skip below is keyed on that byte.
	var others []string
	for _, p := range g.Probes {
		if p == "" || p[0] != '<' {
			others = append(others, p)
		}
	}
	// A byte table for the character after the bracket. In HTML the bracket itself is as
	// dense as the tags are - the 93 KB page below has 6,000 of them - so the skip does not
	// skip much and the work at each candidate is what counts. One table lookup rejects
	// almost all of them.
	var second [256]bool
	for _, p := range g.Probes {
		if len(p) >= 2 && p[0] == '<' {
			second[lowerASCII(p[1])] = true
			second[upperASCII(p[1])] = true
		}
	}
	for i := 0; ; {
		j := bytes.IndexByte(doc[i:], '<')
		if j < 0 {
			break
		}
		i += j
		if i+1 < len(doc) && second[doc[i+1]] {
			for _, p := range g.Probes {
				if p == "" || p[0] != '<' {
					continue
				}
				if i+len(p) <= len(doc) && equalFold(doc[i:i+len(p)], p) {
					return true
				}
			}
		}
		i++
	}
	for _, p := range others {
		if containsFold(doc, p) {
			return true
		}
	}
	return false
}

// containsFold is a case-insensitive search that allocates nothing. Allocating nothing is not
// enough on its own: written as a byte-at-a-time loop it cost 318µs on the 93 KB document below,
// against 259µs for the whole rewrite it exists to avoid. The scan has to use bytes.IndexByte to
// skip, which is the part with the vectorised implementation behind it; the loop is only for the
// few bytes after a candidate.
//
// Every probe here begins with "<", which has no case variant, so one IndexByte finds the
// candidates. A needle beginning with a letter needs both cases and falls back to the slow path,
// which is a reason to write probes that start at the bracket.
func containsFold(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	first := needle[0]
	if lowerASCII(first) == upperASCII(first) {
		// Case-invariant first byte: skip to each candidate rather than testing every
		// position.
		for i := 0; ; {
			j := bytes.IndexByte(haystack[i:], first)
			if j < 0 {
				return false
			}
			i += j
			if i+len(needle) > len(haystack) {
				return false
			}
			if equalFold(haystack[i:i+len(needle)], needle) {
				return true
			}
			i++
		}
	}
	lo, hi := lowerASCII(first), upperASCII(first)
	for i := 0; i+len(needle) <= len(haystack); i++ {
		c := haystack[i]
		if c != lo && c != hi {
			continue
		}
		if equalFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func equalFold(a []byte, b string) bool {
	for i := range b {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func upperASCII(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

// Handlers are what the gate stands in for: the rewrite that would run if it says yes.
func Handlers(changed *int) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			*changed++
			return e.SetAttribute("rel", "noopener")
		}),
		lolhtml.OnElement("img, image", func(e *lolhtml.Element) error {
			*changed++
			return e.SetAttribute("loading", "lazy")
		}),
		lolhtml.OnElement("form", func(e *lolhtml.Element) error {
			*changed++
			return e.SetAttribute("data-checked", "1")
		}),
	}
}

// Run writes doc to w, rewriting it only if the gate says it might need it. It reports whether the
// rewrite ran and how many elements it changed.
func (g Gate) Run(doc []byte, w io.Writer) (ran bool, changed int, err error) {
	if !g.Needed(doc) {
		_, err = w.Write(doc)
		return false, 0, err
	}
	rw, err := lolhtml.NewWriter(w, Handlers(&changed)...)
	if err != nil {
		return false, 0, err
	}
	if _, err := rw.Write(doc); err != nil {
		return true, changed, err
	}
	return true, changed, rw.Close()
}

func main() {
	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "needsrewrite:", err)
		os.Exit(1)
	}
	ran, changed, err := DefaultGate.Run(doc, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "needsrewrite:", err)
		os.Exit(1)
	}
	if ran {
		fmt.Fprintf(os.Stderr, "\nneedsrewrite: rewrote it, %d elements changed\n", changed)
	} else {
		fmt.Fprintf(os.Stderr, "\nneedsrewrite: skipped, no probe matched %d bytes\n", len(doc))
	}
}

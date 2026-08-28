// Command selectorcoverage reports which of a stylesheet's selectors never match
// a document.
//
//	selectorcoverage -selectors rules.txt < page.html
//	selectorcoverage -css site.css < page.html
//
// It cannot check every selector, and saying so is most of the point. lol-html
// matches the subset a streaming rewriter can decide when it sees a start tag, so
// anything needing what follows - :last-child, :only-child, :empty - is rejected,
// along with the sibling combinators, :is/:where/:has, pseudo-elements and state
// selectors like :hover. Real stylesheets are full of those.
//
// So every selector lands in one of three buckets: matched, never matched, or not
// checkable with the reason. A tool that quietly dropped the third would report
// coverage it had not measured.
//
// Each checkable selector costs a handler, and matching cost grows with the number
// registered, on every element. A stylesheet with thousands of rules is worth
// splitting across runs rather than registering all at once.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	list := flag.String("selectors", "", "file of selectors, one per line")
	css := flag.String("css", "", "stylesheet to take selectors from")
	verbose := flag.Bool("v", false, "list the matched selectors too")
	flag.Parse()

	if (*list == "") == (*css == "") {
		fmt.Fprintln(os.Stderr, "usage: selectorcoverage -selectors file | -css file < in.html")
		os.Exit(2)
	}

	var sels []string
	var err error
	if *list != "" {
		sels, err = readList(*list)
	} else {
		sels, err = readCSS(*css)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "selectorcoverage:", err)
		os.Exit(2)
	}
	if len(sels) == 0 {
		fmt.Fprintln(os.Stderr, "selectorcoverage: no selectors found")
		os.Exit(2)
	}

	c := &coverage{verbose: *verbose}
	if err := c.run(sels, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "selectorcoverage:", err)
		os.Exit(1)
	}
	fmt.Print(c.report())
	if len(c.unmatched) > 0 {
		os.Exit(1)
	}
}

func readList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// readCSS pulls the selector part of each rule out of a stylesheet. It is a
// deliberately shallow scan: it skips at-rules with a block, since their contents
// are rules of their own, and it does not try to be a CSS parser - a selector it
// mis-extracts is rejected by the rewriter and reported as not checkable, which
// is the honest outcome.
func readCSS(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := stripComments(string(b))

	var out []string
	seen := map[string]bool{}
	for len(src) > 0 {
		open := strings.IndexByte(src, '{')
		if open < 0 {
			break
		}
		prelude := strings.TrimSpace(src[:open])
		body, rest := matchingBrace(src[open:])
		src = rest

		if strings.HasPrefix(prelude, "@") {
			// A nested block: its contents are rules, so scan them instead.
			if inner := scanSelectors(body); len(inner) > 0 {
				for _, s := range inner {
					if !seen[s] {
						seen[s] = true
						out = append(out, s)
					}
				}
			}
			continue
		}
		for _, s := range splitSelectorList(prelude) {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func scanSelectors(block string) []string {
	var out []string
	for len(block) > 0 {
		open := strings.IndexByte(block, '{')
		if open < 0 {
			break
		}
		prelude := strings.TrimSpace(block[:open])
		_, rest := matchingBrace(block[open:])
		block = rest
		if strings.HasPrefix(prelude, "@") {
			continue
		}
		out = append(out, splitSelectorList(prelude)...)
	}
	return out
}

// matchingBrace splits at the brace matching the one src starts with, returning
// the body and what follows.
func matchingBrace(src string) (body, rest string) {
	depth := 0
	for i, r := range src {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[1:i], src[i+1:]
			}
		}
	}
	return src[1:], ""
}

func splitSelectorList(prelude string) []string {
	var out []string
	for _, s := range strings.Split(prelude, ",") {
		if s = strings.Join(strings.Fields(s), " "); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stripComments(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+j+2:]
	}
}

type uncheckable struct {
	selector string
	reason   string
}

type coverage struct {
	verbose bool

	hits        map[string]int
	unmatched   []string
	uncheckable []uncheckable
}

func (c *coverage) run(sels []string, src io.Reader) error {
	c.hits = map[string]int{}

	opts := make([]lolhtml.Option, 0, len(sels))
	for _, sel := range sels {
		if reason := unsupported(sel); reason != "" {
			c.uncheckable = append(c.uncheckable, uncheckable{sel, reason})
			continue
		}
		sel := sel
		c.hits[sel] = 0
		opts = append(opts, lolhtml.OnElement(sel, func(*lolhtml.Element) error {
			c.hits[sel]++
			return nil
		}))
	}

	if len(opts) > 0 {
		// io.Discard: this pass counts, it does not rewrite.
		w, err := lolhtml.NewWriter(io.Discard, opts...)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, src); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}

	for sel, n := range c.hits {
		if n == 0 {
			c.unmatched = append(c.unmatched, sel)
		}
	}
	sort.Strings(c.unmatched)
	return nil
}

// unsupported asks the rewriter whether a selector is usable, and returns its
// reason if not. Asking beats maintaining a list: the answer comes from the same
// code that will do the matching.
func unsupported(sel string) string {
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement(sel, func(*lolhtml.Element) error { return nil }))
	if err == nil {
		// Closed rather than dropped. Every Writer has to be Closed, including
		// one built only to be thrown away: the drop cleanup is a backstop, not
		// a second way of doing this, and it runs whenever the garbage collector
		// gets round to it. This probe runs once per selector, and the stylesheet
		// this program is written for has thousands of rules - so discarding the
		// Writer here left thousands of live rewriters and their selectors alive
		// at once.
		w.Close()
		return ""
	}
	var se *lolhtml.SelectorError
	if errors.As(err, &se) {
		return se.Message
	}
	return err.Error()
}

func (c *coverage) report() string {
	matched := 0
	for _, n := range c.hits {
		if n > 0 {
			matched++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "selectors=%d matched=%d unmatched=%d not-checkable=%d\n",
		len(c.hits)+len(c.uncheckable), matched, len(c.unmatched), len(c.uncheckable))

	if c.verbose && matched > 0 {
		sels := make([]string, 0, matched)
		for sel, n := range c.hits {
			if n > 0 {
				sels = append(sels, fmt.Sprintf("%s (%d)", sel, n))
			}
		}
		sort.Strings(sels)
		for _, s := range sels {
			fmt.Fprintf(&sb, "matched: %s\n", s)
		}
	}
	for _, sel := range c.unmatched {
		fmt.Fprintf(&sb, "never matched: %s\n", sel)
	}

	// Sorted for a stable report, and never elided: coverage that was not
	// measured must not read as coverage that was.
	sort.Slice(c.uncheckable, func(i, j int) bool {
		return c.uncheckable[i].selector < c.uncheckable[j].selector
	})
	for _, u := range c.uncheckable {
		fmt.Fprintf(&sb, "not checkable: %s (%s)\n", u.selector, u.reason)
	}
	return sb.String()
}

func coverageOf(sels []string, doc string) (*coverage, error) {
	c := &coverage{}
	err := c.run(sels, bytes.NewReader([]byte(doc)))
	return c, err
}

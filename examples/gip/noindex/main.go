// Command noindex adds a robots meta to pages whose path matches a pattern, so a
// staging host, a print view or a search-results page is not indexed.
//
//	noindex -path '/search*' -path '/print/*' -url /search?q=x page.html
//	<head><meta name="robots" content="noindex, nofollow">...
//
// An existing robots meta is rewritten rather than joined. Two of them is not
// twice the instruction: a crawler is entitled to take either, so the only safe
// thing is to leave exactly one, carrying the union of what was asked for.
//
// Where the meta goes is the part with teeth. It has to be in the head to be
// honoured, and <head> is optional in HTML, so there is often no head element to
// insert into. Two positions cover it, and both are decidable in a single pass:
//
//	</head>        when the document has a head element
//	before <body>  when it does not, since that is where the implied head ends
//
// Both come after every position a robots meta could occupy in the head, which
// is what makes "rewrite the existing one or insert a new one, never both"
// possible without buffering the document. A first version decided at the first
// element instead and produced two robots metas on a page that already had one,
// because <html> arrives before the meta inside it.
//
// A document with neither a head nor a body gets nothing, and says so. The
// tempting fallback - append at the end of the output - is wrong twice over: a
// meta there is not in the head, and on a response cut off mid-construct it may
// not be markup at all.
//
// One thing not to reach for here: OnDoctype. It fires for every DOCTYPE token
// in the input, including the ones a parser discards - after an element, after
// text, or a second one - so "a doctype was seen" is not "this page has a
// doctype". It says nothing about where the head is.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type marker struct {
	patterns []string // shell-style path patterns
	url      string   // the page's own url or path, matched against the patterns
	noFollow bool     // also ask crawlers not to follow links
	always   bool     // mark regardless of the patterns

	marked  int
	rewrote int
	skipped map[string]int
}

func (m *marker) note(reason string) {
	if m.skipped == nil {
		m.skipped = map[string]int{}
	}
	m.skipped[reason]++
}

func defaults() *marker { return &marker{} }

func (m *marker) validate() error {
	for _, p := range m.patterns {
		if _, err := path.Match(p, "/"); err != nil {
			return fmt.Errorf("-path %q is not a valid pattern: %w", p, err)
		}
	}
	if !m.always && len(m.patterns) == 0 {
		return fmt.Errorf("nothing to do: give -path at least once, or -always")
	}
	if !m.always && m.url == "" {
		return fmt.Errorf("-url is needed to match against -path")
	}
	return nil
}

// wanted reports whether this page should be marked. The path is compared, not
// the query: "/search*" is about the page, and a pattern that had to know about
// query strings would be a different kind of tool.
func (m *marker) wanted() bool {
	if m.always {
		return true
	}
	p := m.url
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(p, "://"); i >= 0 {
		if j := strings.Index(p[i+3:], "/"); j >= 0 {
			p = p[i+3+j:]
		} else {
			p = "/"
		}
	}
	for _, pat := range m.patterns {
		if ok, err := path.Match(pat, p); err == nil && ok {
			return true
		}
	}
	return false
}

// content is the directive list this program wants in place.
func (m *marker) content() string {
	if m.noFollow {
		return "noindex, nofollow"
	}
	return "noindex"
}

func (m *marker) options() []lolhtml.Option {
	// The head region is everything before the head ends: the explicit
	// </head>, or the start of <body> when the head was implied. Both are
	// decidable in one pass, and both come after every position where a robots
	// meta could be in the head - which is what makes "rewrite the existing one
	// or insert a new one, never both" possible without a second pass.
	headRegion := true
	sawHead := false
	placed := false

	return []lolhtml.Option{
		// An existing robots meta in the head is the one to change. Two robots
		// metas is not twice the instruction: a crawler may take either, so the
		// only safe result is exactly one carrying the union.
		lolhtml.OnElement(`meta[name]`, func(e *lolhtml.Element) error {
			name := strings.ToLower(strings.TrimSpace(
				stdhtml.UnescapeString(attr(e, "name"))))
			if name != "robots" || !m.wanted() {
				return nil
			}
			if !headRegion {
				// A robots meta in the body is not the one a crawler reads for
				// the document, and the head already has ours.
				m.note("a robots meta in the body was left alone")
				return nil
			}
			placed = true
			m.rewrote++
			return e.SetAttribute("content", union(attr(e, "content"), m.content()))
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			sawHead = true
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				defer func() { headRegion = false }()
				if placed || !m.wanted() {
					return nil
				}
				placed = true
				m.marked++
				return end.Before(m.markup(), lolhtml.HTML)
			})
		}),

		// No head element in the source, which is legal - <head> is optional.
		// The start of <body> is the end of the implied head, so inserting
		// before it puts the meta in the head a parser builds.
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			defer func() { headRegion = false }()
			if sawHead || placed || !m.wanted() {
				return nil
			}
			placed = true
			m.marked++
			return e.Before(m.markup(), lolhtml.HTML)
		}),

		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if m.wanted() && !placed {
				// Deliberately not appended at the end of the output: a meta
				// there is not in the head, and on a truncated response it may
				// not be markup at all. See DocumentEnd.Append.
				m.note("no head and no body to put the meta in")
			}
			return nil
		}),
	}
}

// markup is the meta. Assembled as a string because there is no element to hold,
// so the value is escaped for an attribute first.
func (m *marker) markup() string {
	return `<meta name="robots" content="` + lolhtml.EscapeAttribute(m.content()) + `">`
}

// union merges directive lists, keeping what was already asked for. Order is
// stable so the output is diffable, and duplicates are dropped because a crawler
// reading "noindex, noindex" learns nothing extra.
func union(have, want string) string {
	seen := map[string]bool{}
	var out []string
	add := func(list string) {
		for _, tok := range strings.Split(stdhtml.UnescapeString(list), ",") {
			tok = strings.ToLower(strings.TrimSpace(tok))
			if tok == "" || seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	add(have)
	add(want)
	return strings.Join(out, ", ")
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (m *marker) run(r io.Reader, w io.Writer) error {
	if err := m.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, m.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func markString(in string, opts ...func(*marker)) (string, *marker, error) {
	m := defaults()
	for _, o := range opts {
		o(m)
	}
	var out bytes.Buffer
	err := m.run(strings.NewReader(in), &out)
	return out.String(), m, err
}

func total(mm map[string]int) int {
	n := 0
	for _, v := range mm {
		n += v
	}
	return n
}

func (m *marker) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "inserted=%d rewritten=%d", m.marked, m.rewrote)
	reasons := make([]string, 0, len(m.skipped))
	for r := range m.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, m.skipped[r])
	}
	return sb.String()
}

type patternList struct{ v *[]string }

func (p patternList) String() string {
	if p.v == nil {
		return ""
	}
	return strings.Join(*p.v, ",")
}

func (p patternList) Set(v string) error {
	*p.v = append(*p.v, v)
	return nil
}

func main() {
	m := defaults()
	flag.Var(patternList{&m.patterns}, "path",
		"shell-style path pattern to mark, repeatable")
	flag.StringVar(&m.url, "url", "", "the page's url or path, matched against -path")
	flag.BoolVar(&m.noFollow, "no-follow", false, `also add "nofollow"`)
	flag.BoolVar(&m.always, "always", false, "mark every page, ignoring -path")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "noindex:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: noindex -path PAT -url PATH [file.html]")
		os.Exit(2)
	}

	if err := m.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "noindex:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, m.report())
}

// Command landmarks adds ARIA landmark roles to a document that has none.
//
//	<div id="masthead">  ->  <div id="masthead" role="banner">
//	<div class="sidebar">    <div class="sidebar" role="complementary">
//
// A screen reader's landmark list is how a keyboard user skips to the content, and
// a page built out of unmarked divs has none. HTML's own elements are landmarks
// already - main, nav, aside, and header and footer when they are not nested in a
// sectioning element - so a modern page needs nothing from this program and a
// legacy one needs everything.
//
// Which is the first thing that makes this different from the other two-pass
// programs here. "The document has none" is a fact about the whole document, and
// the roles go on start tags, so nothing can be decided until the end - and the
// decision is not per element, it is one decision about the page: touch it, or
// leave it alone entirely. Adding a second banner to a document that already has
// one is worse than adding nothing, because two banners is a landmark list that
// lies. So pass one answers a question about the page and pass two acts on it, and
// most of the interesting code is in the answer.
//
// The second difference is that three of the roles are unique. A document has at
// most one banner, one main and one contentinfo; navigation, complementary and
// search may repeat. So for those three the program is choosing among candidates
// rather than deciding about each one - a global choice, scored, with document
// order breaking a tie: the first plausible banner, the first plausible main, the
// last plausible contentinfo. A per-element rule cannot express that, which is why
// the earlier two-pass programs did not have to.
//
// The evidence is names, because that is what a legacy page offers: an id or a
// class saying "header", "masthead", "nav", "content", "sidebar", "footer". That is
// weak evidence and the program treats it as such - a candidate needs a name that
// means one thing, and anything ambiguous is left alone and counted. It cannot see
// CSS or layout, so a page whose markup says nothing gets nothing from this.
//
// Two things it deliberately does not do.
//
// It does not rename elements. A div with role="main" is second best to a <main>,
// and turning one into the other changes the parse of everything inside it -
// display, margins, what a stylesheet matches. A role is additive; a tag name is
// not.
//
// It does not nest landmarks. A banner inside main, or a navigation inside a
// banner it also added, is a list that reads worse than the page it came from, so
// a candidate inside another chosen candidate is dropped and counted.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Role is an ARIA landmark role.
type Role string

const (
	Banner        Role = "banner"
	Navigation    Role = "navigation"
	Main          Role = "main"
	Complementary Role = "complementary"
	ContentInfo   Role = "contentinfo"
	Search        Role = "search"
)

// Unique are the roles a document may have at most one of.
var Unique = map[Role]bool{Banner: true, Main: true, ContentInfo: true}

// Landmarks are every landmark role, including the ones this program does not add:
// finding one is what makes it leave the document alone.
var Landmarks = map[string]bool{
	"banner": true, "navigation": true, "main": true, "complementary": true,
	"contentinfo": true, "search": true, "form": true, "region": true,
}

// Elements are HTML's own landmarks. header and footer count only outside a
// sectioning element, which is why they are not in here.
var Elements = map[string]Role{
	"main": Main, "nav": Navigation, "aside": Complementary,
}

// Sectioning are the elements that take header and footer out of landmark
// position.
var Sectioning = map[string]bool{
	"article": true, "aside": true, "nav": true, "section": true, "main": true,
}

// Names maps a word in an id or a class to the role it suggests. One word, one
// role: a name that could mean two things is no evidence at all.
var Names = map[string]Role{
	"masthead": Banner, "banner": Banner, "site-header": Banner, "pageheader": Banner,
	"nav": Navigation, "navigation": Navigation, "navbar": Navigation, "menu": Navigation,
	"breadcrumb": Navigation, "breadcrumbs": Navigation,
	"main": Main, "content": Main, "maincontent": Main, "primary": Main,
	"sidebar": Complementary, "aside": Complementary, "secondary": Complementary,
	"related": Complementary,
	"footer":  ContentInfo, "site-footer": ContentInfo, "pagefooter": ContentInfo,
	"colophon": ContentInfo,
	"search":   Search, "searchbox": Search, "search-form": Search,
}

// Ambiguous are names that look like evidence and are not: a "header" may be a
// page banner or the head of a table, a "content" may be the page's main content
// or the inside of a widget.
var Ambiguous = map[string]bool{
	"header": true, "head": true, "top": true, "bottom": true, "wrapper": true,
	"container": true, "inner": true, "outer": true, "box": true, "panel": true,
}

// A Result says what happened.
type Result struct {
	// Added roles, by role.
	Added map[Role]int
	// Existing landmarks found, which is what makes the program stop.
	Existing int
	// Candidates seen, Ambiguous names skipped, and Nested candidates dropped
	// because a chosen landmark already contains them.
	Candidates, Ambiguous, Nested int
	// Why the document was left alone, if it was.
	Reason string
}

// OK reports whether anything was added.
func (r Result) OK() bool { return len(r.Added) > 0 }

func (r Result) String() string {
	if r.Reason != "" {
		return "landmarks: nothing added: " + r.Reason
	}
	parts := make([]string, 0, len(r.Added))
	total := 0
	for role, n := range r.Added {
		parts = append(parts, fmt.Sprintf("%d %s", n, role))
		total += n
	}
	sort.Strings(parts)
	return fmt.Sprintf("landmarks: %d roles added (%s); %d candidates, %d ambiguous names, "+
		"%d nested candidates dropped", total, strings.Join(parts, ", "),
		r.Candidates, r.Ambiguous, r.Nested)
}

// a candidate element and the evidence for it.
type candidate struct {
	at    int
	end   int // the offset of the token that closed it, for the nesting check
	role  Role
	score int
}

// Add reads src to the end, decides, and writes the annotated document. The two
// passes have to see the same bytes, so the input is buffered.
func Add(dst io.Writer, src io.Reader) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	roles, res, err := Scan(doc)
	if err != nil {
		return res, err
	}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		role, ok := roles[e.SourceLocation().Start]
		if !ok {
			return nil
		}
		return e.SetAttribute("role", string(role))
	}))
	if err != nil {
		return res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return res, err
	}
	return res, w.Close()
}

// Scan is the first pass: it answers the question about the page, and then chooses.
func Scan(doc []byte) (map[int]Role, Result, error) {
	s := &scanner{res: Result{Added: map[Role]int{}}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

type scanner struct {
	res Result
	// sectioning is how many sectioning elements this position is inside, which is
	// what decides whether a header or a footer is a landmark.
	sectioning int
	candidates []*candidate
	// open is the candidates whose end tag has not arrived, so their extent can be
	// recorded for the nesting check.
	open []*candidate
}

func (s *scanner) options() []lolhtml.Option {
	// One handler on every element: the rule really is about every element, which
	// is what a wide selector is for.
	return []lolhtml.Option{lolhtml.OnElement("*", s.element)}
}

func (s *scanner) element(e *lolhtml.Element) error {
	name := e.TagName()
	at := e.SourceLocation().Start

	// An explicit landmark role, anywhere, means the document has been marked up
	// already and this program has nothing to add.
	if role, ok := e.Attribute("role"); ok {
		for _, word := range strings.Fields(strings.ToLower(role)) {
			if Landmarks[word] {
				s.res.Existing++
			}
		}
	}
	// HTML's own landmarks count too.
	if _, ok := Elements[name]; ok {
		s.res.Existing++
	}
	if (name == "header" || name == "footer") && s.sectioning == 0 {
		s.res.Existing++
	}

	if Sectioning[name] && e.CanHaveContent() && !e.IsSelfClosing() {
		s.sectioning++
		if err := e.OnEndTag(func(*lolhtml.EndTag) error {
			s.sectioning--
			return nil
		}); err != nil {
			return err
		}
	}

	if role, score, ok := s.candidate(e); ok {
		c := &candidate{at: at, end: at, role: role, score: score}
		s.candidates = append(s.candidates, c)
		s.res.Candidates++
		if e.CanHaveContent() && !e.IsSelfClosing() {
			s.open = append(s.open, c)
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				for i := len(s.open) - 1; i >= 0; i-- {
					if s.open[i] == c {
						// The extent is taken only from this element's
						// own end tag. </p> and </li> are omissible and
						// <p> is a candidate here: a following <div>
						// closes it implicitly, so this handler runs
						// against the ancestor's end tag and a position
						// taken from it is the ancestor's extent, not
						// this element's. The nesting check below would
						// then read every later sibling as nested inside
						// the p and drop its role. Left at the start tag,
						// the extent contains nothing - which is the right
						// answer for an element whose content this program
						// cannot delimit.
						if t.Name() == name {
							c.end = t.SourceLocation().End
						}
						s.open = append(s.open[:i], s.open[i+1:]...)
						return nil
					}
				}
				return nil
			})
		}
	}
	return nil
}

// candidate reads the evidence: a name that means one thing, on an element that
// could hold a landmark.
func (s *scanner) candidate(e *lolhtml.Element) (Role, int, bool) {
	switch e.TagName() {
	case "div", "section", "form", "ul", "p":
	default:
		return "", 0, false
	}
	if _, has := e.Attribute("role"); has {
		return "", 0, false // the document already said something about this one
	}

	// A form holding a search field is a search landmark whatever it is called,
	// which is the one piece of evidence here that is not a name.
	if e.TagName() == "form" {
		if v, ok := e.Attribute("role"); !ok || v == "" {
			if id, _ := e.Attribute("id"); words(id)["search"] {
				return Search, 3, true
			}
			if cls, _ := e.Attribute("class"); words(cls)["search"] {
				return Search, 3, true
			}
		}
	}

	best, score := Role(""), 0
	ambiguous := false
	for _, source := range []string{attr(e, "id"), attr(e, "class")} {
		for word := range words(source) {
			if Ambiguous[word] {
				ambiguous = true
				continue
			}
			if role, ok := Names[word]; ok {
				// An id is better evidence than a class, and a longer name is
				// better than a shorter one: "site-header" over "header".
				weight := 2
				if source == attr(e, "class") {
					weight = 1
				}
				if weight > score {
					best, score = role, weight
				}
			}
		}
	}
	if best == "" {
		if ambiguous {
			s.res.Ambiguous++
		}
		return "", 0, false
	}
	return best, score, true
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// words splits an id or a class into the words a name is made of, so that
// "site-header", "site_header" and "siteHeader" all offer "header" - and the whole
// string too, because "site-header" is better evidence than "header".
func words(s string) map[string]bool {
	out := map[string]bool{}
	if s == "" {
		return out
	}
	lower := strings.ToLower(s)
	for _, part := range strings.Fields(lower) {
		out[part] = true
		for _, w := range strings.FieldsFunc(part, func(r rune) bool {
			return r == '-' || r == '_' || r == '.'
		}) {
			out[w] = true
		}
	}
	// A camelCase name, split at the case changes.
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	camel := strings.Fields(strings.ToLower(b.String()))
	for _, w := range camel {
		out[w] = true
	}
	// The camelCase name as a hyphenated one too, so "siteHeader" offers
	// "site-header" - which is evidence where "header" on its own is not.
	if len(camel) > 1 {
		out[strings.Join(camel, "-")] = true
	}
	return out
}

// decide is the choice: one banner, one main, one contentinfo, and no landmark
// inside another.
func (s *scanner) decide() map[int]Role {
	if s.res.Existing > 0 {
		s.res.Reason = fmt.Sprintf("the document already has %d landmark(s), and a second "+
			"banner or main is worse than none", s.res.Existing)
		return nil
	}
	if len(s.candidates) == 0 {
		s.res.Reason = "no element's name says what it is"
		return nil
	}

	chosen := map[int]Role{}
	// The unique roles are a choice among candidates rather than a decision about
	// each one: best score first, then document order - except contentinfo, where
	// the last plausible one is the page's footer.
	for role := range Unique {
		var best *candidate
		for _, c := range s.candidates {
			if c.role != role {
				continue
			}
			switch {
			case best == nil, c.score > best.score:
				best = c
			case c.score == best.score && role == ContentInfo:
				best = c // the last one wins
			}
		}
		if best != nil {
			chosen[best.at] = role
		}
	}
	for _, c := range s.candidates {
		if !Unique[c.role] {
			chosen[c.at] = c.role
		}
	}

	// No landmark inside another. Walk in document order so the outer one is
	// already chosen when its children are considered.
	order := make([]*candidate, 0, len(s.candidates))
	for _, c := range s.candidates {
		if _, ok := chosen[c.at]; ok {
			order = append(order, c)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].at < order[j].at })
	for i, c := range order {
		for _, outer := range order[:i] {
			if _, still := chosen[outer.at]; !still {
				continue
			}
			if c.at > outer.at && c.at < outer.end {
				delete(chosen, c.at)
				s.res.Nested++
				break
			}
		}
	}

	for _, role := range chosen {
		s.res.Added[role]++
	}
	return chosen
}

func main() {
	res, err := Add(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "landmarks:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

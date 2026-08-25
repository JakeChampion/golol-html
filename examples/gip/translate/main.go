// Command translate adds translate="no" to the elements whose text is not prose,
// so a machine translator leaves them alone.
//
//	<p>Run <code>git push</code> to publish.</p>
//	<p>Run <code translate="no">git push</code> to publish.</p>
//
// The rewrite is one attribute on one kind of element, which makes it the program
// to be careful about cost with rather than about ordering. Nothing here needs
// evidence from later in the document: the rule is "this element is a code
// element", and the tag name is on the start tag. Compare examples/gip/lang, whose
// rule is "this element's text is in another script" and which therefore cannot be
// a single pass at all. Same shape of output, two different programs, and the
// difference is where the evidence lives.
//
// So the interesting question is which selector to write, and it has a measured
// answer. Three ways to say this rule, on a 2000-element page where a tenth of the
// elements match:
//
//	three narrow selectors     439 allocations
//	one selector list          424
//	one "*" with a switch    4,228
//
// A selector that does not match costs matching; a handler that runs costs a unit
// wrapper and whatever it reads. A "*" handler therefore pays per element of the
// document instead of per element the rule is about, and it loses by an order of
// magnitude - which is the opposite of what the library's cost documentation used
// to advise. This program uses a selector list, which is the cheapest of the three
// and also the one that reads like the rule.
//
// The rest is what "leave it alone" has to mean.
//
// An element that already says translate - either way - is left as it is. A page
// that says translate="yes" on a code sample means it, and a rewrite that flips
// that has decided it knows better than the page.
//
// translate is inherited, so an attribute inside an element that already carries
// one says nothing. A code inside a marked code is skipped and counted, which
// keeps the diff to the elements that needed it. That needs only a stack of what
// the enclosing elements said, because they are all known by the time the
// descendant arrives - the same reason this program is one pass. A stack rather
// than a counter, because translate="yes" inside translate="no" turns translation
// back on, and an element in there is worth marking again.
//
// class="notranslate" is the other mechanism, from before the attribute existed,
// and a page using it means the same thing. Those elements get the attribute too,
// matched with [class~="notranslate" i]: the ~= is "one of the space-separated
// words", so it does not match "notranslated", and the i flag folds the case
// because a class selector does not - ".notranslate" misses class="noTranslate",
// measured.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Elements are the ones marked by default: HTML's own "this is not prose"
// elements. A selector list rather than one handler each, which is the cheapest
// way to write it and the way that reads like the rule.
const Elements = "code,kbd,samp,var"

// NotTranslated matches the class convention that predates the attribute. The ~=
// is a word match rather than a substring one, and the i flag is there because a
// class selector is case-sensitive.
const NotTranslated = `[class~="notranslate" i]`

// A Result says what happened.
type Result struct {
	// Marked elements, by tag name.
	Marked map[string]int
	// Already had a translate attribute, whatever it said.
	Already int
	// Nested inside an element that already carries one, so the attribute would
	// have said nothing.
	Nested int
	// ByClass is how many of the marked ones were found by the class convention
	// rather than by their tag name.
	ByClass int
}

func (r Result) String() string {
	total := 0
	tags := make([]string, 0, len(r.Marked))
	for tag, n := range r.Marked {
		total += n
		tags = append(tags, fmt.Sprintf("%d %s", n, tag))
	}
	sort.Strings(tags)
	s := fmt.Sprintf("translate: %d elements marked", total)
	if total > 0 {
		s += " (" + strings.Join(tags, ", ") + ")"
	}
	return s + fmt.Sprintf("; %d already said so, %d nested, %d by class",
		r.Already, r.Nested, r.ByClass)
}

// Mark copies src to dst with the attribute added. elements is a selector list;
// empty means the default.
func Mark(dst io.Writer, src io.Reader, elements string) (Result, error) {
	m := &marker{res: Result{Marked: map[string]int{}}}
	if elements == "" {
		elements = Elements
	}
	w, err := lolhtml.NewWriter(dst, m.options(elements)...)
	if err != nil {
		return m.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return m.res, err
	}
	if err := w.Close(); err != nil {
		return m.res, err
	}
	return m.res, nil
}

type marker struct {
	res Result
	// off is a stack of the translate regions this position is inside, innermost
	// last: true for an element that says no and false for one that says yes. A
	// stack rather than a depth, because translate="yes" inside translate="no"
	// turns translation back on and an element in there is worth marking again.
	off []bool
}

// inside reports whether translation is already off here, which is the innermost
// thing anything said.
func (m *marker) inside() bool { return len(m.off) > 0 && m.off[len(m.off)-1] }

func (m *marker) options(elements string) []lolhtml.Option {
	return []lolhtml.Option{
		// Narrow selectors rather than one broad one: measured, that is an order of
		// magnitude cheaper. The class selector is a separate rule rather than
		// another name for the same one, so it is a separate handler.
		lolhtml.OnElement(elements, m.byTag),
		lolhtml.OnElement(NotTranslated, m.byClass),
		// The inheritance counter has to see the elements the document marked, and
		// those can be any element at all. It cannot see the ones this program
		// marks: matching is decided against the document as it arrived, so an
		// attribute a handler writes never changes which handlers fire. That is
		// documented, and it is what makes these three handlers independent.
		lolhtml.OnElement("[translate]", m.carrier),
	}
}

// byTag marks one of the named elements, and byClass one that uses the class
// convention. They differ only in the count.
func (m *marker) byTag(e *lolhtml.Element) error   { return m.mark(e, false) }
func (m *marker) byClass(e *lolhtml.Element) error { return m.mark(e, true) }

func (m *marker) mark(e *lolhtml.Element, byClass bool) error {
	if _, has := e.Attribute("translate"); has {
		// The document said something about this element. Whatever it said, it
		// meant it; carrier counts it.
		return nil
	}
	if m.inside() {
		// Inside something that already says it, and the attribute is inherited,
		// so this one would say nothing.
		m.res.Nested++
		return nil
	}
	if err := e.SetAttribute("translate", "no"); err != nil {
		return err
	}
	m.res.Marked[e.TagName()]++
	if byClass {
		m.res.ByClass++
	}
	return m.enter(e, true)
}

// carrier counts an element the document marked and keeps the depth right.
//
// An explicit translate="yes" turns inheritance back on, so it is not a region to
// stay out of - it is the opposite, and an element inside it is worth marking.
func (m *marker) carrier(e *lolhtml.Element) error {
	value, _ := e.Attribute("translate")
	m.res.Already++
	return m.enter(e, !strings.EqualFold(strings.TrimSpace(value), "yes"))
}

// enter pushes a region for the element's content, and pops it at whatever closed
// the element - which for an element the document left unclosed is an enclosing
// element's end tag, and that is the right answer: the inheritance ends where the
// element did.
func (m *marker) enter(e *lolhtml.Element, off bool) error {
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	at := len(m.off)
	m.off = append(m.off, off)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		if at < len(m.off) {
			m.off = m.off[:at]
		}
		return nil
	})
}

func main() {
	elements := ""
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: translate [selector-list] < page")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		elements = os.Args[1]
	}
	res, err := Mark(os.Stdout, os.Stdin, elements)
	if err != nil {
		fmt.Fprintln(os.Stderr, "translate:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

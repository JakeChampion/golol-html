// Command dir adds dir="rtl" to the elements whose text reads right to left.
//
//	<p>مرحبا بالعالم</p>  ->  <p dir="rtl">مرحبا بالعالم</p>
//
// The mechanism is the one in examples/gip/lang: the attribute belongs on the
// start tag, the evidence is the element's text, so the document is read twice and
// the passes are joined by [lolhtml.SourceLocation]. What is different is the rule
// inside, and the difference is the point of this program.
//
// Direction is not decided by a majority. The Unicode Bidi Algorithm decides a
// paragraph's direction from its first strong character - the first letter that is
// itself left-to-right or right-to-left - and everything before it is skipped:
// digits, punctuation, quotation marks, spaces. So
//
//	<p>مرحبا this paragraph is mostly English words after that first word</p>
//
// is a right-to-left paragraph, and a program that counted characters would call
// it English and be wrong about how a browser lays it out. The rule cuts the other
// way too, which is the part worth being honest about: a mostly-Arabic paragraph
// that happens to start with a Latin word is left to right, and this program
// leaves it alone rather than overruling the algorithm it is implementing.
//
// Where the evidence is weak, say less. An element with no strong character at all
// - a number, a date, a row of punctuation - gets nothing: its direction is
// inherited and inheritance is the right answer. And -auto writes dir="auto"
// instead of dir="rtl", which is the same rule applied by the browser at render
// time rather than by this program at build time. For text that can change after
// the page is built - a template slot, a comment field - auto is the better
// answer, because the rule will be applied to whatever the text turns out to be.
//
// What this program does not do: an inline run of right-to-left text inside a
// left-to-right paragraph is not an element-level question, and dir on a span is
// not the fix for it. The fix is <bdi>, which isolates the run so the surrounding
// text's direction is not disturbed by it, and deciding where a run begins and
// ends is a different program.
//
// The elements it stays out of: what [lolhtml.IsRawText] names, code, kbd, samp,
// var and pre, anything inside a <bdo> - whose whole purpose is to override the
// algorithm, so a program implementing the algorithm has no business there - and
// any element that already says dir, in either direction. dir is inherited, so an
// element inside one that already says rtl gets nothing.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// RTL are the scripts written right to left. A letter in one of these is a strong
// right-to-left character; any other letter is a strong left-to-right one.
var RTL = []*unicode.RangeTable{
	unicode.Arabic, unicode.Hebrew, unicode.Syriac, unicode.Thaana,
	unicode.Nko, unicode.Samaritan, unicode.Mandaic, unicode.Adlam,
}

// Skip are the elements whose text is not prose, on top of what IsRawText names.
// bdo is here for a different reason: it exists to override the algorithm.
var Skip = map[string]bool{
	"code": true, "kbd": true, "samp": true, "var": true, "pre": true, "bdo": true,
}

// Options are the flags.
type Options struct {
	// Auto writes dir="auto" instead of dir="rtl", which asks the browser to apply
	// the same rule to whatever the text turns out to be.
	Auto bool
}

// A Result says what happened.
type Result struct {
	// Marked elements.
	Marked int
	// LeftToRight elements whose first strong character was left to right, so
	// nothing was written - including the mostly-right-to-left ones that begin
	// with a Latin word, which is the rule being honest.
	LeftToRight int
	// NoEvidence elements with no strong character at all.
	NoEvidence int
	// Already had a dir attribute, in either direction.
	Already int
	// Nested inside an element already marked rtl, where the attribute would say
	// nothing.
	Nested int
	// Regions whose text is not prose.
	Regions int
}

func (r Result) String() string {
	return fmt.Sprintf("dir: %d elements marked; %d left to right, %d with no strong "+
		"character, %d already said so, %d nested, %d regions skipped",
		r.Marked, r.LeftToRight, r.NoEvidence, r.Already, r.Nested, r.Regions)
}

// Annotate reads src to the end, decides, and writes the annotated document. The
// two passes have to see the same bytes, so the input is buffered.
func Annotate(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	marks, res, err := Scan(doc, opts)
	if err != nil {
		return res, err
	}
	value := "rtl"
	if opts.Auto {
		value = "auto"
	}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		if !marks[e.SourceLocation().Start] {
			return nil
		}
		return e.SetAttribute("dir", value)
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

// Scan is the first pass: which elements need the attribute, by the offset of
// their start tag.
func Scan(doc []byte, opts Options) (map[int]bool, Result, error) {
	s := &scanner{opts: opts}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

// direction is what the first strong character said.
type direction int

const (
	unknown direction = iota // no strong character yet
	ltr
	rtl
)

// a frame is an open element and what its own text has said so far.
type frame struct {
	at      int
	name    string
	parent  int
	skip    bool
	already bool
	// inherited is the direction this element gets from its ancestors' own dir
	// attributes, which is what makes an attribute here redundant or not.
	inherited direction
	// says is what this element's own dir attribute states, which its descendants
	// inherit. Distinct from dir, which is what its own text said.
	says direction
	dir  direction
}

type scanner struct {
	opts   Options
	res    Result
	stack  []frame
	dirs   map[int]direction
	parent map[int]int
	order  []int
	// inherited is what each element got from the document's own dir attributes
	// above it. An element already inside rtl needs nothing, whether that rtl came
	// from the document or from this pass.
	inherited map[int]direction
	// already is the elements the document had already said something about.
	already map[int]bool
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", s.element),
		lolhtml.OnDocumentText(s.text),
	}
}

func (s *scanner) element(e *lolhtml.Element) error {
	if s.dirs == nil {
		s.dirs, s.parent = map[int]direction{}, map[int]int{}
		s.inherited, s.already = map[int]direction{}, map[int]bool{}
	}
	name := e.TagName()
	at := e.SourceLocation().Start

	f := frame{at: at, name: name, parent: -1}
	if len(s.stack) > 0 {
		top := s.stack[len(s.stack)-1]
		f.parent, f.skip = top.at, top.skip
		// What an ancestor says, this element has unless it says otherwise.
		f.inherited = top.inherited
		if top.says != unknown {
			f.inherited = top.says
		}
	}
	s.inherited[at] = f.inherited
	if Skip[name] || lolhtml.IsRawText(name) {
		if !f.skip {
			s.res.Regions++
		}
		f.skip = true
	}
	if value, has := e.Attribute("dir"); has {
		f.already = true
		s.already[at] = true
		s.res.Already++
		// What this element says, its descendants inherit. Only rtl and ltr say
		// anything about direction: auto and an empty value ask for the algorithm,
		// which is not a direction to inherit.
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "rtl":
			f.says = rtl
		case "ltr":
			f.says = ltr
		}
	}
	s.parent[at] = f.parent
	s.order = append(s.order, at)

	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.stack = append(s.stack, f)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		for i := len(s.stack) - 1; i >= 0; i-- {
			if s.stack[i].at == at {
				s.dirs[at] = s.stack[i].dir
				s.stack = s.stack[:i]
				return nil
			}
		}
		return nil
	})
}

// text reads the innermost open element's own text, and stops looking once that
// element has a direction: the first strong character is the only one that counts.
func (s *scanner) text(t *lolhtml.TextChunk) error {
	if len(s.stack) == 0 {
		return nil
	}
	i := len(s.stack) - 1
	if s.stack[i].skip || s.stack[i].already || s.stack[i].dir != unknown {
		return nil
	}
	for _, r := range t.Text() {
		if d := strong(r); d != unknown {
			s.stack[i].dir = d
			return nil
		}
	}
	return nil
}

// strong classifies one character. A letter in a right-to-left script is strongly
// right to left, any other letter is strongly left to right, and everything else -
// digits, punctuation, spaces, symbols - is neither and is skipped.
//
// The bidi control characters are skipped too. A document using them has already
// answered this question, and answering it again over the top would be a program
// arguing with its input.
func strong(r rune) direction {
	if !unicode.IsLetter(r) {
		return unknown
	}
	for _, table := range RTL {
		if unicode.Is(table, r) {
			return rtl
		}
	}
	return ltr
}

// decide turns the directions into marks.
func (s *scanner) decide() map[int]bool {
	marks := map[int]bool{}
	for _, at := range s.order {
		if s.already[at] {
			// The document said something about this element, and whatever it said
			// it meant. Its own text is not evidence against it.
			continue
		}
		switch s.dirs[at] {
		case rtl:
			if s.inherited[at] == rtl {
				// Already inside something the document marked rtl, and dir is
				// inherited, so this would say nothing.
				s.res.Nested++
				continue
			}
			marks[at] = true
		case ltr:
			s.res.LeftToRight++
		default:
			s.res.NoEvidence++
		}
	}
	// dir is inherited, so an element inside a marked one says nothing new.
	for _, at := range s.order {
		if !marks[at] {
			continue
		}
		for p := s.parent[at]; p >= 0; p = s.parent[p] {
			if marks[p] {
				delete(marks, at)
				s.res.Nested++
				break
			}
		}
	}
	s.res.Marked = len(marks)
	return marks
}

func main() {
	opts := Options{}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-auto":
			opts.Auto = true
		default:
			fmt.Fprintln(os.Stderr, "usage: dir [-auto] < page")
			os.Exit(2)
		}
	}
	res, err := Annotate(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dir:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

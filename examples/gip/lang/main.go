// Command lang adds a lang attribute to the elements whose own text is in a
// script other than the document's.
//
//	<p>Здравствуйте, мир</p>  ->  <p lang="und-Cyrl">Здравствуйте, мир</p>
//
// This is the one shape a single pass cannot do, and it is worth being precise
// about why. The attribute belongs on the start tag; the evidence for it is the
// element's text, which arrives afterwards. An insertion can only go where the
// rewriter has not been, so by the time the program knows what the element says,
// the place to say it about has gone past. Buffering the element would work and
// would mean holding a document's worth of markup for the case where the element
// is <body>.
//
// So this reads the document twice, which is what the library's documentation
// recommends wherever a decision needs what comes later, and uses
// [lolhtml.SourceLocation] as the identity between the passes: the first pass
// records "the element whose start tag began at byte 417 is Cyrillic", and the
// second sets the attribute on the element whose start tag begins at byte 417.
//
// That works because a source range is a range of the bytes fed to the rewriter -
// absolute, unaffected by how the document was written in, and unaffected by the
// declared encoding. It requires the two passes to be fed the same bytes, which is
// why the program buffers the input rather than re-reading it from anywhere: two
// reads of the same URL are not the same document, and an offset from the first is
// a lie about the second. TestTheOffsetsAreOnlyMeaningfulForTheSameBytes measures
// what happens when that rule is broken, which is nothing at all - no marks, no
// error - and is exactly why it is a rule.
//
// The rest is decisions.
//
// Whose text. An element's own text, not its descendants': a page whose body holds
// one Russian paragraph should have the attribute on the paragraph. So the counts
// go to the innermost open element, and a parent that would get the same script as
// a child it contains is left alone.
//
// How much text. A script has to hold a majority of the letters and there have to
// be enough letters to mean anything: one Russian word in an English paragraph is
// a quotation, not a change of language. Both numbers are constants here, and both
// are the sort of thing a real deployment would tune with a corpus.
//
// What to say. A script is not a language: Cyrillic is Russian, Ukrainian,
// Bulgarian and more. BCP 47 has a spelling for exactly this - "und-Cyrl", an
// undetermined language in a known script - and that is the default. A caller who
// knows better can map a script to a language, which is what the command line
// arguments are for.
//
// Two writing systems are more than one script, and counting scripts alone would
// call them mixed and say nothing: Japanese is Han with kana, and Korean is Hangul
// with Han. Their counts are combined under the BCP 47 subtags for the
// combinations, Jpan and Kore, with the kana as the evidence - Han on its own is
// Chinese.
//
// Where not to look. The elements [lolhtml.IsRawText] names, plus code, kbd, samp,
// var and pre: a Cyrillic identifier in a code sample is not prose. And an element
// that already has a lang is left alone, attribute and all, which is what makes
// running this twice a no-op.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

// MinLetters is how many letters an element needs before its script is worth
// naming. Below this, an element is a fragment of the sentence around it.
const MinLetters = 8

// Majority is the share of an element's letters one script has to hold.
const Majority = 0.7

// A Script is one writing system this program can name.
type Script struct {
	// Subtag is the BCP 47 script subtag, used as "und-<Subtag>".
	Subtag string
	Table  *unicode.RangeTable
}

// Scripts are the ones this program distinguishes. Latin is here so that it can
// be the document's script and so that a Latin element inside a Cyrillic page is
// marked too - the rule is "different from the document", not "not Latin".
var Scripts = []Script{
	{"Latn", unicode.Latin},
	{"Cyrl", unicode.Cyrillic},
	{"Grek", unicode.Greek},
	{"Arab", unicode.Arabic},
	{"Hebr", unicode.Hebrew},
	{"Hani", unicode.Han},
	{"Hira", unicode.Hiragana},
	{"Kana", unicode.Katakana},
	{"Hang", unicode.Hangul},
	{"Deva", unicode.Devanagari},
	{"Thai", unicode.Thai},
}

// Skip are the elements whose text is not prose, on top of what IsRawText names.
var Skip = map[string]bool{"code": true, "kbd": true, "samp": true, "var": true, "pre": true}

// Options are the flags.
type Options struct {
	// Language maps a script subtag to a language tag, for a caller who knows
	// which language the script is being used for.
	Language map[string]string
	// DocumentScript is the script the document is taken to be in when its root
	// element does not say. Empty means "work it out from the first element that
	// has enough text", which is the usual case.
	DocumentScript string
}

// A Result says what happened.
type Result struct {
	// Document is the script the document was taken to be in.
	Document string
	// Marked elements, by the value written.
	Marked map[string]int
	// Skipped elements that already had a lang, and Regions whose text is not
	// prose.
	Skipped, Regions int
	// Mixed elements: enough letters, no majority script.
	Mixed int
	// Nested marks pruned because a parent already said the same thing.
	Pruned int
}

func (r Result) String() string {
	marks := make([]string, 0, len(r.Marked))
	total := 0
	for value, n := range r.Marked {
		marks = append(marks, fmt.Sprintf("%d %s", n, value))
		total += n
	}
	sort.Strings(marks)
	s := fmt.Sprintf("lang: document is %s; %d elements marked", r.Document, total)
	if total > 0 {
		s += " (" + strings.Join(marks, ", ") + ")"
	}
	return s + fmt.Sprintf("; %d already had one, %d regions skipped, %d mixed, %d nested marks pruned",
		r.Skipped, r.Regions, r.Mixed, r.Pruned)
}

// Annotate reads src to the end, decides, and writes the annotated document.
//
// Reading it all is the price of two passes: the offsets the first pass records
// are offsets into these bytes, and only these bytes.
func Annotate(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	marks, res, err := Scan(doc, opts)
	if err != nil {
		return res, err
	}
	return res, Apply(dst, doc, marks, &res)
}

// Scan is the first pass: it decides which elements need a lang, keyed by the
// offset of their start tag.
func Scan(doc []byte, opts Options) (map[int]string, Result, error) {
	s := &scanner{opts: opts, res: Result{Marked: map[string]int{}}, counts: map[int]map[string]int{}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

// Apply is the second pass: it writes the attribute on the elements the first pass
// named, found by the same offsets.
func Apply(dst io.Writer, doc []byte, marks map[int]string, res *Result) error {
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		value, ok := marks[e.SourceLocation().Start]
		if !ok {
			return nil
		}
		if err := e.SetAttribute("lang", value); err != nil {
			return err
		}
		res.Marked[value]++
		return nil
	}))
	if err != nil {
		return err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return err
	}
	return w.Close()
}

// an open element on the scanner's stack.
type frame struct {
	at     int    // the offset of its start tag, which is its identity
	name   string // its tag name, for the end-tag guard
	parent int    // its parent's offset, or -1
	skip   bool   // its text is not prose
	lang   bool   // it already says what language it is in
}

type scanner struct {
	opts  Options
	res   Result
	stack []frame
	// counts is letters per script, per element offset.
	counts map[int]map[string]int
	// parent is each element's parent offset, for pruning a nested mark.
	parents map[int]int
	// order is the offsets in document order, so the report and the pruning are
	// deterministic.
	order []int
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", s.element),
		lolhtml.OnDocumentText(s.text),
	}
}

func (s *scanner) element(e *lolhtml.Element) error {
	name := e.TagName()
	at := e.SourceLocation().Start

	f := frame{at: at, name: name, parent: -1}
	if len(s.stack) > 0 {
		top := s.stack[len(s.stack)-1]
		f.parent = top.at
		f.skip = top.skip
	}
	if Skip[name] || lolhtml.IsRawText(name) {
		if !f.skip {
			s.res.Regions++
		}
		f.skip = true
	}
	if _, has := e.Attribute("lang"); has {
		f.lang = true
		s.res.Skipped++
	}
	if s.parents == nil {
		s.parents = map[int]int{}
	}
	s.parents[at] = f.parent
	s.order = append(s.order, at)

	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.stack = append(s.stack, f)
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		// Pop to this element: an end tag that is not its own closed it and
		// everything still open inside it.
		for i := len(s.stack) - 1; i >= 0; i-- {
			if s.stack[i].at == at {
				s.stack = s.stack[:i]
				break
			}
		}
		return nil
	})
}

// text attributes letters to the innermost open element, which is the one whose
// own text this is.
func (s *scanner) text(t *lolhtml.TextChunk) error {
	if len(s.stack) == 0 {
		return nil
	}
	top := s.stack[len(s.stack)-1]
	if top.skip || top.lang {
		return nil
	}
	counts := s.counts[top.at]
	if counts == nil {
		counts = map[string]int{}
		s.counts[top.at] = counts
	}
	for _, r := range t.Text() {
		if !unicode.IsLetter(r) {
			continue
		}
		for _, sc := range Scripts {
			if unicode.Is(sc.Table, r) {
				counts[sc.Subtag]++
				break
			}
		}
	}
	return nil
}

// decide turns the counts into marks: which offsets get which value.
func (s *scanner) decide() map[int]string {
	// The document's script is the caller's if they said, and otherwise the one
	// with the most letters across the page: a document is mostly what it mostly
	// says.
	document := s.opts.DocumentScript
	if document == "" {
		totals := map[string]int{}
		for _, counts := range s.counts {
			for subtag, n := range counts {
				totals[subtag] += n
			}
		}
		combine(totals)
		best, bestN := "", 0
		for _, subtag := range subtags() {
			if totals[subtag] > bestN {
				best, bestN = subtag, totals[subtag]
			}
		}
		document = best
	}
	if document == "" {
		document = "Latn"
	}
	s.res.Document = document

	marks := map[int]string{}
	for _, at := range s.order {
		counts := s.counts[at]
		if len(counts) == 0 {
			continue
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total < MinLetters {
			continue
		}
		// After the total, because a letter counted twice would inflate it.
		combine(counts)
		best, bestN := "", 0
		for _, subtag := range subtags() {
			if counts[subtag] > bestN {
				best, bestN = subtag, counts[subtag]
			}
		}
		if float64(bestN)/float64(total) < Majority {
			s.res.Mixed++
			continue
		}
		if best == document {
			continue
		}
		marks[at] = s.value(best)
	}

	// A child that says the same thing as an ancestor says nothing new.
	for _, at := range s.order {
		value, ok := marks[at]
		if !ok {
			continue
		}
		for p := s.parents[at]; p >= 0; p = s.parents[p] {
			if marks[p] == value {
				delete(marks, at)
				s.res.Pruned++
				break
			}
		}
	}
	return marks
}

// value is what goes in the attribute: a language if the caller mapped one, and
// otherwise the BCP 47 spelling for "undetermined language, this script".
func (s *scanner) value(subtag string) string {
	if lang, ok := s.opts.Language[subtag]; ok && lang != "" {
		return lang
	}
	return "und-" + subtag
}

// subtags is the script order, so that a tie between two scripts is broken the
// same way every time. The combinations come first: where they apply they are the
// better answer, and where they do not they are zero.
func subtags() []string {
	out := []string{"Jpan", "Kore"}
	for _, s := range Scripts {
		out = append(out, s.Subtag)
	}
	return out
}

// combine adds the counts for the writing systems that are more than one script.
// Japanese is Han, Hiragana and Katakana together and no single one of them holds
// a majority of an ordinary sentence, so a program that only counted scripts would
// call Japanese "mixed" and say nothing. BCP 47 has Jpan for the combination, and
// Kore for Hangul with Han.
//
// The kana are the evidence: Han on its own is Chinese, and Han with kana is
// Japanese.
func combine(counts map[string]int) {
	if kana := counts["Hira"] + counts["Kana"]; kana > 0 {
		counts["Jpan"] = kana + counts["Hani"]
	}
	// Kore is Hangul with Han; Hangul on its own is Hang, and saying Kore for it
	// would be claiming a mixture the text does not have. The kana need no such
	// care: they are used for nothing but Japanese, so Jpan is right with or
	// without Han.
	if counts["Hang"] > 0 && counts["Hani"] > 0 {
		counts["Kore"] = counts["Hang"] + counts["Hani"]
	}
}

func main() {
	opts := Options{Language: map[string]string{}}
	for _, arg := range os.Args[1:] {
		subtag, lang, ok := strings.Cut(arg, "=")
		if !ok || subtag == "" || lang == "" {
			fmt.Fprintln(os.Stderr, "usage: lang [Subtag=language ...] < page")
			os.Exit(2)
		}
		opts.Language[subtag] = lang
	}
	res, err := Annotate(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lang:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

// Command labels reports form controls with no label, and labels that point at
// nothing.
//
//	form.html:8:5: <input type="email"> has no label; its placeholder is not one
//	form.html:14:3: <label for="phone"> names no element in this document
//	form.html:22:3: <label> has no for attribute and no control inside it
//
// The association between a label and a control is a join rather than a lookup,
// and that is what makes this program different from the two report programs
// before it. A label can name its control by id, and the control can be anywhere -
// before the label or after it. A control can be named by being inside a label. An
// aria-labelledby can point at anything at all. So both sides have to be collected
// and matched at the end, which a report can do freely: nothing is being written,
// so there is no position that has already gone past.
//
// It also makes one finding possible that a single element could never produce. If
// two elements share an id, a label naming that id is ambiguous - a browser picks
// the first, and the second control looks labelled in the markup and is not. That
// is a question about the whole document, and it is reported.
//
// What needs a label is decided by the type, and the table is the substance of the
// program rather than a detail:
//
//	hidden                        nothing; it is not shown
//	submit, reset                 its value, and both have a default, so nothing
//	button                        its value, or nothing to click on
//	image                         its alt, which is examples/gip/alt's question
//	everything else               a label
//
// A select, a textarea and a button element need one too. A meter, a progress and
// an output are labelable and are usually described by the text around them, so
// they are reported when nothing names them at all - the same rule, and worth
// saying because a linter that reported every progress bar would be switched off.
//
// Four things can name a control, and a fifth is the one everybody reaches for and
// is not a label: a placeholder. It disappears when the field is filled in, it is
// not read as a name by every screen reader, and it fails at the first zoom level
// that clips it. So a control whose only name is a placeholder is reported, and the
// message says the placeholder is there rather than claiming the control is
// nameless. The same goes for a title, for the same reason as in the alt program.
//
// What this cannot see: whether the label says something useful. "Field 1" is a
// label, and only a person can tell you it is a bad one.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what is wrong.
type Kind string

const (
	NoLabel         Kind = "no-label"
	PlaceholderOnly Kind = "placeholder-only"
	TitleOnly       Kind = "title-only"
	ForMissing      Kind = "for-missing"
	ForNotLabelable Kind = "for-not-labelable"
	DanglingLabel   Kind = "dangling-label"
	AmbiguousID     Kind = "ambiguous-id"
	ImageNoAlt      Kind = "image-no-alt"
)

// Labelable are the elements a label may be associated with. A label pointing at
// anything else is pointing at nothing, whatever the markup looks like.
var Labelable = map[string]bool{
	"input": true, "select": true, "textarea": true, "button": true,
	"meter": true, "progress": true, "output": true,
}

// NamedByValue are the input types whose value is their name, so a label is not
// what they are missing.
var NamedByValue = map[string]bool{"submit": true, "reset": true, "button": true}

// A Finding is one thing worth reporting.
type Finding struct {
	Kind         Kind
	At           int
	Line, Column int
	Message      string
}

func (f Finding) String() string {
	return fmt.Sprintf("%d:%d: %s", f.Line, f.Column, f.Message)
}

// A Result is the report.
type Result struct {
	Findings []Finding
	// Controls that needed a label, and Labelled ones that had one.
	Controls, Labelled int
	// Skipped controls that need no label: a hidden input, a submit button.
	Skipped int
}

// OK reports whether there was nothing to say.
func (r Result) OK() bool { return len(r.Findings) == 0 }

func (r Result) String() string {
	return fmt.Sprintf("labels: %d controls, %d labelled, %d needing none; %d findings",
		r.Controls, r.Labelled, r.Skipped, len(r.Findings))
}

// a control and everything that might name it.
type control struct {
	at   int
	tag  string
	kind string // the input type, lower-cased, or the tag name
	id   string
	// inLabel is the offset of the label it sits inside, or -1.
	inLabel     int
	label       string
	labelledBy  string
	title       string
	placeholder string
	value       string
	alt         string
}

// a label and what it claims.
type label struct {
	at   int
	for_ string
	// contains is how many labelable controls were inside it.
	contains int
}

// Check reads doc and reports on its controls.
func Check(doc []byte) (Result, error) {
	c := &checker{ids: map[string][]string{}}
	w, err := lolhtml.NewWriter(io.Discard, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.report(doc), nil
}

type checker struct {
	res      Result
	controls []*control
	labels   []*label
	// ids maps an id to the tag names that use it, so a duplicate is visible and a
	// label pointing at something unlabelable is too.
	ids map[string][]string
	// open is the labels this position is inside, innermost last.
	open []*label
}

func (c *checker) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("label", c.label),
		lolhtml.OnElement("input,select,textarea,button,meter,progress,output", c.control),
		lolhtml.OnElement("[id]", c.id),
	}
}

func (c *checker) id(e *lolhtml.Element) error {
	if v, ok := e.Attribute("id"); ok && v != "" {
		c.ids[v] = append(c.ids[v], e.TagName())
	}
	return nil
}

func (c *checker) label(e *lolhtml.Element) error {
	l := &label{at: e.SourceLocation().Start}
	l.for_, _ = e.Attribute("for")
	c.labels = append(c.labels, l)
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	c.open = append(c.open, l)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		for i := len(c.open) - 1; i >= 0; i-- {
			if c.open[i] == l {
				c.open = append(c.open[:i], c.open[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

func (c *checker) control(e *lolhtml.Element) error {
	ctl := &control{at: e.SourceLocation().Start, tag: e.TagName(), inLabel: -1}
	ctl.kind = ctl.tag
	if ctl.tag == "input" {
		if t, ok := e.Attribute("type"); ok {
			ctl.kind = strings.ToLower(strings.TrimSpace(t))
		} else {
			ctl.kind = "text" // the default, and the one a bare <input> gets
		}
	}
	ctl.id, _ = e.Attribute("id")
	ctl.label, _ = e.Attribute("aria-label")
	ctl.labelledBy, _ = e.Attribute("aria-labelledby")
	ctl.title, _ = e.Attribute("title")
	ctl.placeholder, _ = e.Attribute("placeholder")
	ctl.value, _ = e.Attribute("value")
	ctl.alt, _ = e.Attribute("alt")
	// Every open label contains this control, not only the innermost: HTML
	// associates a label with its first labelable descendant, so in nested labels -
	// which no specification allows and documents write anyway - both label the
	// same control and neither is dangling.
	for _, l := range c.open {
		l.contains++
	}
	if len(c.open) > 0 {
		ctl.inLabel = c.open[len(c.open)-1].at
	}
	c.controls = append(c.controls, ctl)
	return nil
}

// report does the join, which is why it happens at the end.
func (c *checker) report(doc []byte) Result {
	lines := newlines(doc)
	add := func(kind Kind, at int, format string, args ...any) {
		line, col := position(lines, doc, at)
		c.res.Findings = append(c.res.Findings, Finding{
			Kind: kind, At: at, Line: line, Column: col,
			Message: fmt.Sprintf(format, args...),
		})
	}

	// Which ids a label points at, and how many labels point at each - several
	// labels for one control is allowed, so that is not a finding.
	pointedAt := map[string]bool{}
	for _, l := range c.labels {
		if l.for_ != "" {
			pointedAt[l.for_] = true
		}
	}

	for _, l := range c.labels {
		switch {
		case l.for_ != "":
			tags, ok := c.ids[l.for_]
			switch {
			case !ok:
				add(ForMissing, l.at, "<label for=%q> names no element in this document", l.for_)
			case len(tags) > 1:
				add(AmbiguousID, l.at, "<label for=%q> is ambiguous: %d elements share that id, "+
					"so a browser labels the first and the rest look labelled and are not",
					l.for_, len(tags))
			case !Labelable[tags[0]]:
				add(ForNotLabelable, l.at, "<label for=%q> names a <%s>, which a label "+
					"cannot be associated with", l.for_, tags[0])
			}
		case l.contains == 0:
			add(DanglingLabel, l.at, "<label> has no for attribute and no control inside it")
		}
	}

	for _, ctl := range c.controls {
		if ctl.kind == "hidden" {
			c.res.Skipped++
			continue
		}
		if ctl.tag == "input" && NamedByValue[ctl.kind] {
			// A submit and a reset have default labels; a button with no value has
			// nothing to say, and that is a different finding from a missing label.
			c.res.Skipped++
			if ctl.kind == "button" && strings.TrimSpace(ctl.value) == "" {
				add(NoLabel, ctl.at, `<input type="button"> has no value, so there is `+
					`nothing written on it`)
			}
			continue
		}
		if ctl.tag == "input" && ctl.kind == "image" {
			c.res.Skipped++
			if strings.TrimSpace(ctl.alt) == "" {
				add(ImageNoAlt, ctl.at, `<input type="image"> is named by its alt, and has none`)
			}
			continue
		}

		c.res.Controls++
		named := strings.TrimSpace(ctl.label) != ""
		if ctl.inLabel >= 0 {
			named = true
		}
		if ctl.id != "" && pointedAt[ctl.id] && len(c.ids[ctl.id]) == 1 {
			named = true
		}
		if ctl.labelledBy != "" {
			missing := []string{}
			for _, id := range strings.Fields(ctl.labelledBy) {
				if len(c.ids[id]) == 0 {
					missing = append(missing, id)
				}
			}
			if len(missing) == 0 {
				named = true
			}
		}
		if named {
			c.res.Labelled++
			continue
		}

		switch {
		case strings.TrimSpace(ctl.placeholder) != "":
			add(PlaceholderOnly, ctl.at, "<%s%s> has no label; its placeholder is not one, "+
				"because it disappears as soon as the field is filled in",
				ctl.tag, typeNote(ctl))
		case strings.TrimSpace(ctl.title) != "":
			add(TitleOnly, ctl.at, "<%s%s> has no label; its title is not a substitute, "+
				"because support for one is too poor to rely on", ctl.tag, typeNote(ctl))
		default:
			add(NoLabel, ctl.at, "<%s%s> has no label", ctl.tag, typeNote(ctl))
		}
	}

	sort.SliceStable(c.res.Findings, func(i, j int) bool {
		return c.res.Findings[i].At < c.res.Findings[j].At
	})
	return c.res
}

func typeNote(ctl *control) string {
	if ctl.tag != "input" {
		return ""
	}
	return fmt.Sprintf(" type=%q", ctl.kind)
}

func newlines(doc []byte) []int {
	var at []int
	for i, b := range doc {
		if b == '\n' {
			at = append(at, i)
		}
	}
	return at
}

func position(lines []int, doc []byte, at int) (line, column int) {
	line = 1
	start := 0
	for _, nl := range lines {
		if nl >= at {
			break
		}
		line++
		start = nl + 1
	}
	if at > len(doc) {
		at = len(doc)
	}
	return line, utf8.RuneCount(doc[start:at]) + 1
}

func main() {
	name := "-"
	var doc []byte
	var err error
	switch len(os.Args) {
	case 1:
		doc, err = io.ReadAll(os.Stdin)
	case 2:
		name = os.Args[1]
		doc, err = os.ReadFile(name)
	default:
		fmt.Fprintln(os.Stderr, "usage: labels [file] < page")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "labels:", err)
		os.Exit(1)
	}
	res, err := Check(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "labels:", err)
		os.Exit(1)
	}
	for _, f := range res.Findings {
		fmt.Printf("%s:%s\n", name, f)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

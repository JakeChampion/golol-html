// Command honeypot adds a decoy field to every form, and reports where each one
// went.
//
//	<form action="/comment">  ->  <form …><input type="text" name="url_2" hidden
//	                                tabindex="-1" autocomplete="off" aria-hidden="true">
//
//	stdout  the rewritten document
//	stderr  /comment 12:3 url_2
//
// A honeypot is a field a person never fills in and a naive bot does, so a
// submission carrying a value for it can be dropped. Which means the rewrite is
// worthless on its own: the server has to know what to look for, and only the
// rewrite knows what it chose. So this program's output is two things - the
// document, and a manifest of the field it put in each form - and the manifest is
// not an extra, it is half the feature.
//
// The name is chosen per form. A honeypot named for a field the form already has
// collides: the bot fills in both, the server sees a value in the real one, and the
// decoy is either ignored or - worse - the real field's value is dropped. So the
// name comes from a list of plausible-looking candidates, and a candidate a form
// already uses is skipped. That is a decision about a form's contents, which arrive
// after its start tag, so the document is read twice - the same reason as
// examples/gip/csrf and a different question.
//
// Being invisible is done without CSS, deliberately. The obvious way to hide a
// honeypot is style="display:none", and a Content-Security-Policy without
// 'unsafe-inline' drops that attribute's effect - so on exactly the sites most
// likely to have a policy, the field appears in the page and real users fill it in.
// The hidden attribute needs no stylesheet and cannot be blocked, and tabindex,
// autocomplete and aria-hidden keep it out of the way of a keyboard, a password
// manager and a screen reader. A bot reading markup sees it either way.
//
// Two things it will not do.
//
// It will not put a field where the field would not be part of the form. A form
// written between a <table> and its first row takes an inserted field beside the
// table rather than inside it, so those shapes are refused and reported - see
// examples/gip/csrf, which refuses them for the same reason.
//
// It will not register an end-tag handler on every element. A registration costs a
// live handle until the rewrite ends rather than until the end tag arrives, so
// doing it per element on a large page costs about 300 bytes each and nothing in
// MemorySettings bounds it. This program needs the boundary of a form and nothing
// else, so it registers one per form: see [lolhtml.Element.OnEndTag].
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

// Candidates are the names to try, in order. Each looks like a field a form might
// have, because a name a bot skips is a honeypot that catches nothing.
var Candidates = []string{
	"url_2", "website_url", "email_confirm", "contact_url", "homepage_url",
	"company_url", "referrer_url", "alt_email",
}

// FosteredParents are the elements a form cannot hold content inside of: a form
// written as their child is a shape where an insertion is fostered out of it.
var FosteredParents = map[string]bool{
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"select": true, "optgroup": true, "datalist": true,
}

// Reason is why a form got no field.
type Reason string

const (
	Fostered  Reason = "would-not-be-in-the-form"
	NoName    Reason = "no-unused-name-left"
	AlreadyIn Reason = "already-has-one"
)

// An Entry is one line of the manifest: what to look for, and where it is.
type Entry struct {
	Action       string
	Field        string
	Line, Column int
}

func (e Entry) String() string {
	action := e.Action
	if action == "" {
		action = "(this page)"
	}
	return fmt.Sprintf("%s %d:%d %s", action, e.Line, e.Column, e.Field)
}

// A Result is the manifest and the counts.
type Result struct {
	Entries []Entry
	Forms   int
	Refused map[Reason]int
}

// OK reports whether every form got a field.
func (r Result) OK() bool { return len(r.Entries) == r.Forms }

func (r Result) String() string {
	parts := make([]string, 0, len(r.Refused))
	for reason, n := range r.Refused {
		parts = append(parts, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(parts)
	s := fmt.Sprintf("honeypot: %d forms, %d fields added", r.Forms, len(r.Entries))
	if len(parts) > 0 {
		s += "; " + strings.Join(parts, ", ")
	}
	return s
}

// what is known about one form.
type form struct {
	at       int
	action   string
	names    map[string]bool
	fostered bool
	// mine says the form already carries a field this program put there, which is
	// not the same as carrying a field whose name happens to be on the list: a form
	// with its own "url_2" gets a honeypot under the next candidate.
	mine bool
}

// Add reads src to the end, decides, writes the document and returns the manifest.
func Add(dst io.Writer, src io.Reader) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	fields, res, err := Scan(doc)
	if err != nil {
		return res, err
	}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("form", func(e *lolhtml.Element) error {
		name, ok := fields[e.SourceLocation().Start]
		if !ok {
			return nil
		}
		return e.Prepend(field(name), lolhtml.HTML)
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

// field is the markup. No inline style: a Content-Security-Policy without
// 'unsafe-inline' would drop it and show the field to real users, and the hidden
// attribute needs no stylesheet at all.
func field(name string) string {
	return fmt.Sprintf(`<input type="text" name="%s" hidden tabindex="-1" `+
		`autocomplete="off" aria-hidden="true">`, lolhtml.EscapeAttribute(name))
}

// Scan is the first pass: it chooses a name per form, which needs the form's own
// fields.
func Scan(doc []byte) (map[int]string, Result, error) {
	s := &scanner{res: Result{Refused: map[Reason]int{}}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(doc), s.res, nil
}

type scanner struct {
	res   Result
	forms []*form
	// open is the forms this position is inside, innermost last, and parents the
	// element names, which is what says whether a form is in a fostering position.
	open    []*form
	parents []string
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		// A form's boundary and the fields inside it, and nothing else: an end-tag
		// registration costs a handle for the whole rewrite, so this program takes
		// one per form rather than one per element.
		lolhtml.OnElement("form", s.form),
		lolhtml.OnElement("input,select,textarea,button", s.field),
		// The containers that foster an insertion out, and the ones that appear
		// inside them and do not - a cell, a caption, a template. Tracking both is
		// what makes "the form's immediate parent" answerable without registering
		// an end-tag handler on every element in the document.
		lolhtml.OnElement("table,thead,tbody,tfoot,tr,select,optgroup,datalist,"+
			"td,th,caption,template", s.container),
	}
}

func (s *scanner) container(e *lolhtml.Element) error {
	name := e.TagName()
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.parents = append(s.parents, name)
	at := len(s.parents) - 1
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		if at < len(s.parents) {
			s.parents = s.parents[:at]
		}
		return nil
	})
}

func (s *scanner) form(e *lolhtml.Element) error {
	f := &form{at: e.SourceLocation().Start, names: map[string]bool{}}
	f.action, _ = e.Attribute("action")
	if len(s.parents) > 0 && FosteredParents[s.parents[len(s.parents)-1]] {
		f.fostered = true
	}
	s.forms = append(s.forms, f)
	s.res.Forms++
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.open = append(s.open, f)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		for i := len(s.open) - 1; i >= 0; i-- {
			if s.open[i] == f {
				s.open = append(s.open[:i], s.open[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

// field records a name the form already uses, which is what a candidate must not
// collide with - and recognises this program's own field, which is what makes a
// second pass a no-op.
func (s *scanner) field(e *lolhtml.Element) error {
	if len(s.open) == 0 {
		return nil
	}
	f := s.open[len(s.open)-1]
	name, ok := e.Attribute("name")
	if !ok || name == "" {
		return nil
	}
	f.names[name] = true

	// One of ours: a candidate name, hidden, and marked as decoration. A form's own
	// field called "url_2" has none of the rest, and gets a honeypot under a
	// different name rather than being skipped.
	if _, hidden := e.Attribute("hidden"); !hidden {
		return nil
	}
	if aria, _ := e.Attribute("aria-hidden"); !strings.EqualFold(strings.TrimSpace(aria), "true") {
		return nil
	}
	for _, candidate := range Candidates {
		if name == candidate {
			f.mine = true
		}
	}
	return nil
}

func (s *scanner) decide(doc []byte) map[int]string {
	lines := newlines(doc)
	out := map[int]string{}
	for _, f := range s.forms {
		switch {
		case f.fostered:
			s.res.Refused[Fostered]++
			continue
		case f.mine:
			s.res.Refused[AlreadyIn]++
			continue
		}
		name := ""
		for _, candidate := range Candidates {
			if !f.names[candidate] {
				name = candidate
				break
			}
		}
		if name == "" {
			// Every candidate is a field this form already has, which is either a
			// remarkable form or a document this program has already rewritten with
			// a different list.
			s.res.Refused[NoName]++
			continue
		}
		out[f.at] = name
		line, col := position(lines, doc, f.at)
		s.res.Entries = append(s.res.Entries, Entry{
			Action: f.action, Field: name, Line: line, Column: col,
		})
	}
	return out
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
	res, err := Add(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "honeypot:", err)
		os.Exit(1)
	}
	for _, e := range res.Entries {
		fmt.Fprintln(os.Stderr, e)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

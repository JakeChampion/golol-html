// Command csrf inserts a hidden token field into every form that posts.
//
//	<form method="post" action="/buy">  ->  <form …><input type="hidden"
//	                                          name="csrf_token" value="…">
//
// A token that is missing from one form is a form that fails, and a token that
// reaches the wrong origin is the thing the token was protecting against. So this
// program is the one in this set that refuses rather than guesses, and every
// refusal is counted: a silent gap in a security control is worse than a loud one.
//
// Three of the four decisions cannot be made at the form's start tag.
//
// Whether the form posts. A method attribute says, and a submit button can
// override it: <form action="/x"><button formmethod="post"> posts, and the button
// arrives after the form's start tag - after the only place a first child can be
// inserted. So the document is read twice. The alternative, inserting into every
// form including the ones that only ever GET, puts a token in a URL, which is how
// tokens end up in logs and Referer headers.
//
// Whether the form posts to us. A token is a secret shared between this origin and
// its own pages, and a form posting to another origin hands it over: the browser
// will send it, and the site receiving it now has a valid token for a user's
// session. So a cross-origin action is a refusal, and so is a cross-origin
// formaction on any submitter - which is, again, evidence that arrives after the
// start tag.
//
// Whether the field would end up in the form at all. This is the one that has to be
// measured rather than reasoned about: an insertion goes where the markup says, and
// tree construction can move it. A form written between a <table> and its first row
// is such a shape - the field is prepended inside the form and a parser puts it
// beside the table:
//
//	<table><form method=post><tr>…    the field is a child of the table
//	<table><tbody><form…><tr>…        a child of the tbody
//	<select><form…>                   a child of the body
//	<table><tr><td><form…>            inside the form, as written
//
// Measured against golang.org/x/net/html in differential/table_test.go. A field
// that is not in the form is not submitted with it, so those shapes are refused and
// reported: the page needs its markup fixed, and no rewrite can do it from here.
//
// And whether the form has a token already, which is the easy one.
//
// The field goes first, before anything else in the form, so that it is present
// even in a document that is truncated before the form closes - which is what a
// failed upstream response looks like.
package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Reason is why a form was left without a token.
type Reason string

const (
	NotPosting  Reason = "does-not-post"
	CrossOrigin Reason = "cross-origin"
	Fostered    Reason = "would-not-be-in-the-form"
	Already     Reason = "already-has-one"
)

// FosteredParents are the elements a form cannot hold content inside of: a form
// written as their child is a shape where an insertion is fostered out.
var FosteredParents = map[string]bool{
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"select": true, "optgroup": true, "datalist": true,
}

// Options are what the caller has to supply.
type Options struct {
	// Field is the hidden input's name, and Token its value.
	Field, Token string
	// Origin is this document's own origin, as scheme://host[:port]. A form whose
	// action is absolute and elsewhere is refused. Empty means every absolute
	// action is treated as elsewhere, which is the safe reading of "I did not say".
	Origin string
}

// A Result says what happened.
type Result struct {
	// Forms seen and Tokens inserted.
	Forms, Tokens int
	// Refused, by reason.
	Refused map[Reason]int
}

// OK reports whether every posting form got a token.
func (r Result) OK() bool { return r.Refused[CrossOrigin] == 0 && r.Refused[Fostered] == 0 }

func (r Result) String() string {
	parts := make([]string, 0, len(r.Refused))
	for reason, n := range r.Refused {
		parts = append(parts, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(parts)
	s := fmt.Sprintf("csrf: %d forms, %d tokens inserted", r.Forms, r.Tokens)
	if len(parts) > 0 {
		s += "; " + strings.Join(parts, ", ")
	}
	if r.Refused[Fostered] > 0 {
		s += "\ncsrf: WARNING: a form is written where a hidden field cannot be part of " +
			"it - the markup has to be fixed before it can be protected"
	}
	return s
}

// what is known about one form.
type form struct {
	at int
	// method is the form's own, lower-cased, and posts is the answer after the
	// submitters have had their say.
	method string
	posts  bool
	// actions is every URL the form can post to: its own, and every formaction.
	actions []string
	// fostered says the form is written where an insertion would not be part of it.
	fostered bool
	// hasField says the token is already there.
	hasField bool
}

// Insert reads src to the end, decides, and writes the result.
func Insert(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	wanted, res, err := Scan(doc, opts)
	if err != nil {
		return res, err
	}
	field := fmt.Sprintf(`<input type="hidden" name="%s" value="%s">`,
		lolhtml.EscapeAttribute(opts.Field), lolhtml.EscapeAttribute(opts.Token))
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("form", func(e *lolhtml.Element) error {
		if !wanted[e.SourceLocation().Start] {
			return nil
		}
		// First, so that it is there even if the document is truncated before the
		// form closes.
		return e.Prepend(field, lolhtml.HTML)
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

// Scan is the first pass: it decides which forms get a token, which needs the
// whole form rather than its start tag.
func Scan(doc []byte, opts Options) (map[int]bool, Result, error) {
	s := &scanner{opts: opts, res: Result{Refused: map[Reason]int{}}, forms: map[int]*form{}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

type scanner struct {
	opts  Options
	res   Result
	forms map[int]*form
	order []int
	// open is the forms this position is inside, innermost last.
	open []*form
	// parents is the element names this position is inside, innermost last, which
	// is what says whether a form is in a fostering position.
	parents []string
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", s.element),
	}
}

func (s *scanner) element(e *lolhtml.Element) error {
	name := e.TagName()
	if name == "form" {
		s.form(e)
	} else {
		s.inForm(e)
	}
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.parents = append(s.parents, name)
	at := len(s.parents) - 1
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		if at < len(s.parents) {
			s.parents = s.parents[:at]
		}
		if name == "form" && len(s.open) > 0 {
			s.open = s.open[:len(s.open)-1]
		}
		return nil
	})
}

func (s *scanner) form(e *lolhtml.Element) error {
	f := &form{at: e.SourceLocation().Start}
	if m, ok := e.Attribute("method"); ok {
		f.method = strings.ToLower(strings.TrimSpace(m))
	}
	f.posts = f.method == "post"
	if action, ok := e.Attribute("action"); ok {
		f.actions = append(f.actions, action)
	}
	// The form's position decides whether a first child would be part of it.
	if len(s.parents) > 0 && FosteredParents[s.parents[len(s.parents)-1]] {
		f.fostered = true
	}
	s.forms[f.at] = f
	s.order = append(s.order, f.at)
	s.res.Forms++
	if e.CanHaveContent() && !e.IsSelfClosing() {
		s.open = append(s.open, f)
	}
	return nil
}

// inForm reads the elements that can change what the enclosing form does, all of
// which arrive after its start tag.
func (s *scanner) inForm(e *lolhtml.Element) error {
	if len(s.open) == 0 {
		return nil
	}
	f := s.open[len(s.open)-1]

	switch e.TagName() {
	case "input", "button":
		if m, ok := e.Attribute("formmethod"); ok {
			if strings.EqualFold(strings.TrimSpace(m), "post") {
				// A submitter can post from a form that says it does not.
				f.posts = true
			}
		}
		if a, ok := e.Attribute("formaction"); ok {
			f.actions = append(f.actions, a)
		}
		if name, ok := e.Attribute("name"); ok && name == s.opts.Field {
			f.hasField = true
		}
	}
	return nil
}

func (s *scanner) decide() map[int]bool {
	out := map[int]bool{}
	for _, at := range s.order {
		f := s.forms[at]
		switch {
		case !f.posts:
			s.res.Refused[NotPosting]++
		case f.hasField:
			s.res.Refused[Already]++
		case !s.sameOrigin(f):
			s.res.Refused[CrossOrigin]++
		case f.fostered:
			s.res.Refused[Fostered]++
		default:
			out[at] = true
			s.res.Tokens++
		}
	}
	return out
}

// sameOrigin reports whether every URL this form can post to is ours. Every one:
// a form with one same-origin action and one cross-origin formaction can send the
// token elsewhere, and which submitter the user presses is not for this program to
// predict.
func (s *scanner) sameOrigin(f *form) bool {
	for _, action := range f.actions {
		if !s.ours(action) {
			return false
		}
	}
	return true
}

func (s *scanner) ours(action string) bool {
	action = strings.TrimSpace(action)
	if action == "" {
		return true // the form posts to the document's own URL
	}
	u, err := url.Parse(action)
	if err != nil {
		return false // unparseable is not ours
	}
	if u.Scheme == "" && u.Host == "" {
		return true // a relative URL
	}
	if s.opts.Origin == "" {
		return false // nothing was said, so nothing absolute is ours
	}
	mine, err := url.Parse(s.opts.Origin)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		// A scheme-relative URL: "//example.com/x" keeps the scheme and changes
		// the host.
		return strings.EqualFold(u.Host, mine.Host)
	}
	return strings.EqualFold(u.Scheme, mine.Scheme) && strings.EqualFold(u.Host, mine.Host)
}

func main() {
	opts := Options{Field: "csrf_token"}
	for _, arg := range os.Args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			usage()
		}
		switch key {
		case "-token":
			opts.Token = value
		case "-field":
			opts.Field = value
		case "-origin":
			opts.Origin = value
		default:
			usage()
		}
	}
	if opts.Token == "" || opts.Field == "" {
		usage()
	}
	res, err := Insert(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "csrf:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: csrf -token=… [-field=csrf_token] [-origin=https://example.com] < page")
	os.Exit(2)
}

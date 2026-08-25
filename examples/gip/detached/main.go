// Command detached shows what every rewritable unit answers after its handler has
// returned.
//
//	$ detached
//	unit         method                 answer
//	Element      SetAttribute           ErrDetached
//	Element      TagName                "" and no error
//	Element      Attribute              "" false - the same as an absent attribute
//	Element      HasAttribute           ErrDetached - the only getter that can say
//	Element      Detached               true
//	Sink         WriteString            ErrDetached
//	Sink         Err                    ErrDetached
//	...
//
// lol-html guarantees a unit only for the duration of the handler it is passed to, so
// the wrapper is detached on return and the answers afterwards are these. The rule is
// not "every method errors": a mutator errors, a getter answers with a zero value and
// says nothing, because a getter has nowhere to put an error. So a retained element
// reports an empty document rather than a problem, and the program that retained it
// gets plausible answers to every question it asks.
//
// Two exceptions are worth seeing in the table. [lolhtml.Element.HasAttribute] reports
// the detachment, because its signature has room for an error - which makes it the only
// way to tell "no such attribute" from "no such element". And a [lolhtml.Sink] reports
// it from every method including [lolhtml.Sink.Err], so a retained sink cannot be
// mistaken for a working one.
//
// The program takes no input: it captures one of each unit from a fixed document and
// then asks. Its point is to be run and read, and to fail if any of those answers
// changes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what sort of method was called.
type Kind int

const (
	// Mutator changes the document and can report the detachment.
	Mutator Kind = iota
	// Getter reads and has nowhere to put an error.
	Getter
	// Answer is Detached itself, which is the question rather than a victim of it.
	Answer
)

func (k Kind) String() string {
	switch k {
	case Mutator:
		return "mutator"
	case Getter:
		return "getter"
	}
	return "answer"
}

// Call is one method and what it answered.
type Call struct {
	Unit   string
	Method string
	Kind   Kind
	Err    error
	Value  string
}

// Reports whether the answer says the unit is detached.
func (c Call) Reports() bool { return errors.Is(c.Err, lolhtml.ErrDetached) }

func (c Call) String() string {
	switch {
	case c.Reports():
		return "ErrDetached"
	case c.Err != nil:
		return "error: " + c.Err.Error()
	case c.Value != "":
		return c.Value
	}
	return "nothing"
}

// Units are the six rewritable units plus the sink, all captured from one document and
// all detached by the time they are used.
type Units struct {
	Element *lolhtml.Element
	Text    *lolhtml.TextChunk
	Comment *lolhtml.Comment
	Doctype *lolhtml.Doctype
	EndTag  *lolhtml.EndTag
	DocEnd  *lolhtml.DocumentEnd
	Sink    *lolhtml.Sink
	Rewrote string
}

// Capture runs one rewrite and keeps every unit it was handed - which is exactly what a
// program should not do, and the point of the demonstration.
func Capture() (Units, error) {
	var u Units
	out, err := lolhtml.RewriteString(`<!DOCTYPE html><a href="/x" class="c">t<!--c--></a>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			u.Element = e
			if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
				u.EndTag = t
				return nil
			}); err != nil {
				return err
			}
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				u.Sink = s
				return s.WriteString("", lolhtml.HTML)
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if len(c.Bytes()) > 0 {
				u.Text = c
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			u.Comment = c
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			u.Doctype = d
			return nil
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			u.DocEnd = d
			return nil
		}),
	)
	if err != nil {
		return u, err
	}
	u.Rewrote = out
	switch {
	case u.Element == nil, u.Text == nil, u.Comment == nil, u.Doctype == nil,
		u.EndTag == nil, u.DocEnd == nil, u.Sink == nil:
		return u, errors.New("not every handler ran")
	}
	return u, nil
}

// Ask calls every method on every captured unit and records the answer.
func (u Units) Ask() []Call {
	var calls []Call
	add := func(unit, method string, kind Kind, err error, value string) {
		calls = append(calls, Call{Unit: unit, Method: method, Kind: kind, Err: err, Value: value})
	}

	e := u.Element
	add("Element", "SetAttribute", Mutator, e.SetAttribute("a", "1"), "")
	add("Element", "RemoveAttribute", Mutator, e.RemoveAttribute("class"), "")
	add("Element", "SetTagName", Mutator, e.SetTagName("b"), "")
	add("Element", "Before", Mutator, e.Before("x", lolhtml.Text), "")
	add("Element", "Append", Mutator, e.Append("x", lolhtml.Text), "")
	add("Element", "Replace", Mutator, e.Replace("x", lolhtml.Text), "")
	add("Element", "SetInnerContent", Mutator, e.SetInnerContent("x", lolhtml.Text), "")
	add("Element", "OnEndTag", Mutator, e.OnEndTag(func(*lolhtml.EndTag) error { return nil }), "")
	add("Element", "SetUserData", Mutator, e.SetUserData(1), "")
	add("Element", "StreamAppend", Mutator, e.StreamAppend(func(*lolhtml.Sink) error { return nil }), "")
	has, herr := e.HasAttribute("class")
	add("Element", "HasAttribute", Getter, herr, fmt.Sprintf("%v", has))
	add("Element", "TagName", Getter, nil, fmt.Sprintf("%q", e.TagName()))
	add("Element", "NamespaceURI", Getter, nil, fmt.Sprintf("%q", e.NamespaceURI()))
	v, ok := e.Attribute("class")
	add("Element", "Attribute", Getter, nil, fmt.Sprintf("%q %v - the same as an absent attribute", v, ok))
	add("Element", "AttributeList", Getter, nil, fmt.Sprintf("%d attributes", len(e.AttributeList())))
	add("Element", "CanHaveContent", Getter, nil, fmt.Sprintf("%v", e.CanHaveContent()))
	add("Element", "IsSelfClosing", Getter, nil, fmt.Sprintf("%v", e.IsSelfClosing()))
	add("Element", "IsRemoved", Getter, nil, fmt.Sprintf("%v", e.IsRemoved()))
	loc := e.SourceLocation()
	add("Element", "SourceLocation", Getter, nil, fmt.Sprintf("{%d %d}", loc.Start, loc.End))
	add("Element", "UserData", Getter, nil, fmt.Sprintf("%v", e.UserData()))
	add("Element", "Detached", Answer, nil, fmt.Sprintf("%v", e.Detached()))

	t := u.Text
	add("TextChunk", "Replace", Mutator, t.Replace("x", lolhtml.Text), "")
	add("TextChunk", "Before", Mutator, t.Before("x", lolhtml.Text), "")
	add("TextChunk", "After", Mutator, t.After("x", lolhtml.Text), "")
	add("TextChunk", "SetUserData", Mutator, t.SetUserData(1), "")
	add("TextChunk", "Text", Getter, nil, fmt.Sprintf("%q", t.Text()))
	add("TextChunk", "IsLastInTextNode", Getter, nil, fmt.Sprintf("%v", t.IsLastInTextNode()))
	add("TextChunk", "Detached", Answer, nil, fmt.Sprintf("%v", t.Detached()))

	c := u.Comment
	add("Comment", "SetText", Mutator, c.SetText("x"), "")
	add("Comment", "Before", Mutator, c.Before("x", lolhtml.Text), "")
	add("Comment", "Replace", Mutator, c.Replace("x", lolhtml.Text), "")
	add("Comment", "Text", Getter, nil, fmt.Sprintf("%q", c.Text()))
	add("Comment", "Detached", Answer, nil, fmt.Sprintf("%v", c.Detached()))

	d := u.Doctype
	name, nameOK := d.Name()
	add("Doctype", "Name", Getter, nil, fmt.Sprintf("%q %v", name, nameOK))
	add("Doctype", "Detached", Answer, nil, fmt.Sprintf("%v", d.Detached()))

	x := u.EndTag
	add("EndTag", "SetName", Mutator, x.SetName("b"), "")
	add("EndTag", "Before", Mutator, x.Before("x", lolhtml.Text), "")
	add("EndTag", "After", Mutator, x.After("x", lolhtml.Text), "")
	add("EndTag", "Name", Getter, nil, fmt.Sprintf("%q", x.Name()))
	add("EndTag", "Detached", Answer, nil, fmt.Sprintf("%v", x.Detached()))

	de := u.DocEnd
	add("DocumentEnd", "Append", Mutator, de.Append("x", lolhtml.Text), "")
	add("DocumentEnd", "Detached", Answer, nil, fmt.Sprintf("%v", de.Detached()))

	s := u.Sink
	add("Sink", "WriteString", Mutator, s.WriteString("x", lolhtml.HTML), "")
	add("Sink", "WriteChunk", Mutator, s.WriteChunk([]byte("x"), lolhtml.Text), "")
	_, werr := s.AsWriter(lolhtml.HTML).Write([]byte("x"))
	add("Sink", "AsWriter().Write", Mutator, werr, "")
	add("Sink", "Err", Getter, s.Err(), "")

	return calls
}

// Report is the table.
type Report struct {
	Calls   []Call
	Rewrote string
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s %-18s %-8s %s\n", "unit", "method", "kind", "answer")
	for _, c := range r.Calls {
		fmt.Fprintf(&b, "%-12s %-18s %-8s %s\n", c.Unit, c.Method, c.Kind, c)
	}
	return b.String()
}

// Silent returns the getters that answered without saying anything, which is the half
// worth knowing about.
func (r Report) Silent() []Call {
	var out []Call
	for _, c := range r.Calls {
		if c.Kind == Getter && !c.Reports() {
			out = append(out, c)
		}
	}
	return out
}

// Loud returns the calls that reported the detachment.
func (r Report) Loud() []Call {
	var out []Call
	for _, c := range r.Calls {
		if c.Reports() {
			out = append(out, c)
		}
	}
	return out
}

// Run captures the units and asks them everything.
func Run() (Report, error) {
	u, err := Capture()
	if err != nil {
		return Report{}, err
	}
	return Report{Calls: u.Ask(), Rewrote: u.Rewrote}, nil
}

func main() {
	only := flag.String("only", "", `print one group: "loud" or "silent"`)
	flag.Parse()

	r, err := Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "detached:", err)
		os.Exit(2)
	}
	switch *only {
	case "loud":
		for _, c := range r.Loud() {
			fmt.Printf("%-12s %s\n", c.Unit, c.Method)
		}
	case "silent":
		for _, c := range r.Silent() {
			fmt.Printf("%-12s %-18s %s\n", c.Unit, c.Method, c)
		}
	default:
		fmt.Print(r)
		fmt.Printf("\n%d calls reported the detachment, %d getters answered silently\n",
			len(r.Loud()), len(r.Silent()))
	}
}

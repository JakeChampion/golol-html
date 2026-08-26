// Command widgets turns legacy widget markup into web component markup: a container becomes a
// custom element, the state it kept in classes and data attributes becomes properties, and the
// parts it kept in nested divs become slots.
//
//	$ widgets -rule 'div.tabs=my-tabs,data-active:active,part=div.tab-title:title' page.html
//	3 widgets upgraded, 1 skipped
//	  my-tabs        2 upgraded
//	  my-accordion   1 upgraded, 1 skipped: its end tag was omitted
//
// # Renaming is only safe into a container
//
// A rename writes the new name over the start tag and over the end tag, and whoever parses the
// output applies the new name's content model to what is inside. The library documents what that
// does to a table or a select. The void direction is worse, because there are four answers and
// none of them is "the element, with its content". Measured against x/net/html on
// <div class="w">x</div>:
//
//	renamed to   output                          the tree a parser builds
//	br           <br class="w">x</br>            two br elements, x between them
//	img hr       <img class="w">x</img>           one element, x its sibling
//	input wbr    (the same shape)                 one element, x its sibling
//	area
//	col          <col class="w">x</col>          no element at all, only x
//	meta         <meta class="w">x</meta>        the element in <head>, x in <body>
//
// br is the only end tag HTML treats as a start tag, so the stray </br> becomes a second
// element: the rename duplicated the widget. A col outside a table is dropped, so the rename
// deleted it. A meta belongs in the head, so the rename moved it there and left its content
// behind. Nothing errors in any of the four.
//
// A custom element name always has a hyphen, and a hyphenated name is always an ordinary
// container, so this program's targets are safe by construction. It refuses a target that is not
// one anyway - a void name, a raw-text name, or a name with no hyphen - because the mistake is
// available and silent.
//
// # An omitted end tag is a widget this cannot upgrade
//
// A rename writes over the token that closed the element, and where the source omitted the
// element's own end tag that token belongs to something else: renaming the items of
// <ul><li>a<li>b<li>c</ul> yields <ul><my-item>a<my-item>b<my-item>c</my-item>, where the </ul>
// has become an </my-item> and the list never closes. The outermost rename wins, so with distinct
// names the </ul> becomes </item-1>.
//
// Whether an element has its own end tag is knowable - the end-tag handler reports the tag that
// closed it, and a name that differs means the source left this one out - but it is knowable too
// late, because the rename has to happen at the start tag. So this is two passes: the first
// records which candidates closed themselves, the second renames only those. The rest are counted
// and reported, which is the honest answer rather than a corrupted list.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// voidNames are the elements that cannot hold content, so a rename into one changes what the
// document means. The list is HTML's, and every entry is measured in the tests.
var voidNames = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true,
}

// rawTextNames hold text rather than markup, so a rename into one turns the widget's markup into
// text - the other direction of the same hazard.
var rawTextNames = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true, "iframe": true,
	"noembed": true, "noframes": true, "noscript": true, "plaintext": true, "xmp": true,
}

// Rule is one upgrade: which markup to match, what to call it, and what to carry across.
type Rule struct {
	// Match is the selector for the legacy container, and Name the custom element it
	// becomes.
	Match string
	Name  string
	// Attrs maps a legacy attribute name to the property name to give it.
	Attrs map[string]string
	// Parts maps a selector for a child to the slot name it should take.
	Parts map[string]string
	// DropClasses is the class tokens to remove, which are the ones that were state
	// rather than styling.
	DropClasses []string
}

// Validate refuses a target this cannot rename into. A custom element name is always a
// container, which is what makes the rename safe; anything else has a content model of its own.
func (r Rule) Validate() error {
	switch {
	case r.Match == "":
		return errors.New("a rule needs a selector to match")
	case r.Name == "":
		return errors.New("a rule needs a name to rename to")
	case voidNames[r.Name]:
		return fmt.Errorf("%s cannot hold content, so renaming a container to it changes "+
			"what the document means: see the package comment", r.Name)
	case rawTextNames[r.Name]:
		return fmt.Errorf("%s holds text rather than markup, so renaming a container to it "+
			"turns the widget's markup into text", r.Name)
	case !strings.Contains(r.Name, "-"):
		return fmt.Errorf("%s is not a custom element name: it needs a hyphen, and without "+
			"one it is a built-in whose content model may not be a container's", r.Name)
	case r.Name[0] < 'a' || r.Name[0] > 'z':
		return fmt.Errorf("%s does not start with a lower-case ASCII letter, so it is not a "+
			"custom element name", r.Name)
	}
	return nil
}

// Count is what happened for one rule.
type Count struct {
	Name string
	// Upgraded is how many widgets were renamed, and Skipped how many were left alone
	// because the source omitted their end tag.
	Upgraded int
	Skipped  int
}

// Result is what a run did.
type Result struct {
	Doc    string
	Counts map[string]*Count
}

func (r Result) Total(f func(*Count) int) int {
	n := 0
	for _, c := range r.Counts {
		n += f(c)
	}
	return n
}

func (r Result) Names() []string {
	out := make([]string, 0, len(r.Counts))
	for name := range r.Counts {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// closes is the first pass: which candidate elements the source closed itself. The key is the
// element's start-tag offset, which is stable and unique within one document.
func closes(doc string, rules []Rule) (map[int]bool, error) {
	own := map[int]bool{}
	var opts []lolhtml.Option
	for _, rule := range rules {
		opts = append(opts, lolhtml.OnElement(rule.Match, func(e *lolhtml.Element) error {
			at := e.SourceLocation().Start
			tag := e.TagName()
			if !e.CanHaveContent() {
				// Nothing to close, so nothing can be upgraded either.
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				// An end tag that names this element is this element's. A name
				// that differs means the source left this one's out, and the
				// token belongs to something enclosing.
				if strings.EqualFold(t.Name(), tag) {
					own[at] = true
				}
				return nil
			})
		}))
	}
	if _, err := lolhtml.RewriteString(doc, opts...); err != nil {
		return nil, err
	}
	return own, nil
}

// Upgrade rewrites doc, renaming every match whose end tag the source spelled.
func Upgrade(doc string, rules []Rule) (Result, error) {
	res := Result{Counts: map[string]*Count{}}
	if len(rules) == 0 {
		return res, errors.New("widgets: no rules")
	}
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return res, fmt.Errorf("widgets: %w", err)
		}
	}

	own, err := closes(doc, rules)
	if err != nil {
		return res, err
	}

	count := func(name string) *Count {
		c, ok := res.Counts[name]
		if !ok {
			c = &Count{Name: name}
			res.Counts[name] = c
		}
		return c
	}

	var opts []lolhtml.Option
	for _, rule := range rules {
		rule := rule
		opts = append(opts, lolhtml.OnElement(rule.Match, func(e *lolhtml.Element) error {
			c := count(rule.Name)
			if !own[e.SourceLocation().Start] {
				c.Skipped++
				return nil
			}
			c.Upgraded++
			return apply(e, rule)
		}))
		for sel, slot := range rule.Parts {
			slot := slot
			opts = append(opts, lolhtml.OnElement(rule.Match+" > "+sel,
				func(e *lolhtml.Element) error {
					return e.SetAttribute("slot", slot)
				}))
		}
	}

	out, err := lolhtml.RewriteString(doc, opts...)
	if err != nil {
		return res, err
	}
	res.Doc = out
	return res, nil
}

// apply carries the state across and renames the element. The rename goes last, because the
// attribute reads are about the legacy markup and the name is what stops it being legacy.
func apply(e *lolhtml.Element, rule Rule) error {
	for from, to := range rule.Attrs {
		value, ok := e.Attribute(from)
		if !ok {
			continue
		}
		if err := e.RemoveAttribute(from); err != nil {
			return err
		}
		if err := e.SetAttribute(to, value); err != nil {
			return err
		}
	}

	if len(rule.DropClasses) > 0 {
		if class, ok := e.Attribute("class"); ok {
			kept := drop(class, rule.DropClasses)
			if kept == "" {
				if err := e.RemoveAttribute("class"); err != nil {
					return err
				}
			} else if err := e.SetAttribute("class", kept); err != nil {
				return err
			}
		}
	}

	return e.SetTagName(rule.Name)
}

// drop removes the named tokens from a class attribute, keeping the rest in order. The value is
// written back as tokens rather than edited as a string, because a class attribute is a list.
func drop(class string, remove []string) string {
	gone := map[string]bool{}
	for _, r := range remove {
		gone[r] = true
	}
	var kept []string
	for _, token := range strings.Fields(class) {
		if !gone[token] {
			kept = append(kept, token)
		}
	}
	return strings.Join(kept, " ")
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d widgets upgraded, %d skipped\n",
		r.Total(func(c *Count) int { return c.Upgraded }),
		r.Total(func(c *Count) int { return c.Skipped }))
	for _, name := range r.Names() {
		c := r.Counts[name]
		line := fmt.Sprintf("%d upgraded", c.Upgraded)
		if c.Skipped > 0 {
			line += fmt.Sprintf(", %d skipped: the source omitted their end tag, and "+
				"renaming one writes over an enclosing element's", c.Skipped)
		}
		fmt.Fprintf(&b, "  %-16s %s\n", name, line)
	}
	return b.String()
}

// ParseRule reads a rule from the flag form:
//
//	div.tabs=my-tabs,data-active:active,part=div.tab-title:title,drop=tabs
func ParseRule(spec string) (Rule, error) {
	fields := strings.Split(spec, ",")
	match, name, ok := strings.Cut(fields[0], "=")
	if !ok {
		return Rule{}, fmt.Errorf("%q: want selector=name", fields[0])
	}
	r := Rule{Match: match, Name: name, Attrs: map[string]string{}, Parts: map[string]string{}}
	for _, field := range fields[1:] {
		switch {
		case strings.HasPrefix(field, "part="):
			sel, slot, ok := strings.Cut(strings.TrimPrefix(field, "part="), ":")
			if !ok {
				return Rule{}, fmt.Errorf("%q: want part=selector:slot", field)
			}
			r.Parts[sel] = slot
		case strings.HasPrefix(field, "drop="):
			r.DropClasses = append(r.DropClasses,
				strings.Fields(strings.ReplaceAll(
					strings.TrimPrefix(field, "drop="), ":", " "))...)
		default:
			from, to, ok := strings.Cut(field, ":")
			if !ok {
				return Rule{}, fmt.Errorf("%q: want attribute:property", field)
			}
			r.Attrs[from] = to
		}
	}
	return r, nil
}

type ruleList []Rule

func (l *ruleList) String() string { return "" }

func (l *ruleList) Set(v string) error {
	r, err := ParseRule(v)
	if err != nil {
		return err
	}
	*l = append(*l, r)
	return nil
}

func main() {
	var rules ruleList
	flag.Var(&rules, "rule", "selector=name[,attr:prop][,part=sel:slot][,drop=class] - repeatable")
	report := flag.Bool("report", false, "print the counts instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "widgets:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}
	// The whole document is read, because deciding which widgets closed themselves takes a
	// pass of its own and the evidence is later than the first place it is needed.
	doc, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "widgets:", err)
		os.Exit(1)
	}

	res, err := Upgrade(string(doc), rules)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(res)
		return
	}
	fmt.Print(res.Doc)
}

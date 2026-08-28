// Command bindings turns framework attribute syntax into plain HTML attributes where it can, and
// says why it cannot everywhere else.
//
//	$ bindings page.html
//	9 bindings: 4 rewritten, 5 left alone
//	  rewritten
//	    :title="'Home'"           -> title="Home"
//	    v-bind:id="'main'"        -> id="main"
//	    [attr.role]="'nav'"       -> role="nav"
//	    :hidden="true"            -> hidden="true"
//	  left alone
//	    :href="url"               the value is an expression, and nothing here evaluates one
//	    @click="go"               an event handler has no plain form
//	    *ngIf="ok"                a structural directive decides whether the element exists
//	    [(ngModel)]="v"           two-way binding has no plain form
//	    v-html="body"             writing markup from a binding is not a plain attribute
//
// Only a literal can become a plain attribute: a quoted string, a number, true or false. An
// expression needs a runtime, and a program that guessed would produce a page that looks right
// and says something else.
//
// # A template is not HTML, and its compiler is case-sensitive
//
// The tokenizer lower-cases attribute names, because HTML matches them case-insensitively. A
// framework template is HTML-shaped text read by something that does not:
//
//	source              Attribute.Name      Attribute.NamePreserveCase
//	*ngIf="ok"          *ngif               *ngIf
//	[ngClass]="c"       [ngclass]           [ngClass]
//	[(ngModel)]="v"     [(ngmodel)]         [(ngModel)]
//	v-bind:someProp     v-bind:someprop     v-bind:someProp
//	@myEvent            @myevent            @myEvent
//
// `*ngIf` is a directive and `*ngif` is not, so a report built from Name names something the
// author cannot find, and an attribute *added* as `*ngIf` arrives as `*ngif` and stops working -
// [lolhtml.Element.SetAttribute] lower-cases a name it is adding and keeps the document's
// spelling for one already there. This program reads NamePreserveCase for everything it prints
// and never adds a name with a capital in it: the plain attributes it writes are lower-case by
// definition, which is the only reason it is safe to write them at all.
//
// The same rule is documented for SVG, where viewbox is not viewBox. A template compiler is the
// second consumer of the same kind, and there will be others: the question is not whether the
// document is HTML but whether whoever reads it next cares about case.
//
// # Why the selectors are not used
//
// Every one of these names needs escaping to appear in a selector, because the characters that
// make them recognisable are the characters CSS uses for something else:
//
//	[\:href]  [\@click]  [\*ngIf]  [\(click\)]  [\[ngClass\]]  [\[\(ngModel\)\]]
//
// Those all work, and are tested. What this program does instead is match every element and read
// its attribute list, because a page can carry a dozen prefixes and one selector per prefix is a
// dozen registrations to keep in step with a list that is only going to grow. The escaping is
// worth knowing about anyway: an unescaped `[:href]` is a rejected selector rather than one that
// matches nothing, so it fails loudly - see the package documentation on escaping.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind says what a binding is, which decides whether it has a plain form.
type Kind int

const (
	// Property is a one-way binding of an attribute or property: :x, v-bind:x, [x],
	// [attr.x], x-bind:x.
	Property Kind = iota
	// Event is a handler: @x, v-on:x, (x).
	Event
	// Structural decides whether the element or its children exist: *ngIf, v-if, v-for.
	Structural
	// TwoWay is [(x)] or v-model.
	TwoWay
	// Markup writes content rather than an attribute: v-html, [innerHTML].
	Markup
	// NotABinding is an ordinary attribute.
	NotABinding
)

func (k Kind) why() string {
	switch k {
	case Event:
		return "an event handler has no plain form"
	case Structural:
		return "a structural directive decides whether the element exists"
	case TwoWay:
		return "two-way binding has no plain form"
	case Markup:
		return "writing markup from a binding is not a plain attribute"
	}
	return "the value is an expression, and nothing here evaluates one"
}

// classify reads a binding's source spelling and says what kind it is and, for a property
// binding, what plain attribute it corresponds to.
//
// The spelling has to be the source one: a report built from the lower-cased name names
// something the author cannot search for.
func classify(name string) (Kind, string) {
	lower := strings.ToLower(name)

	switch {
	case strings.HasPrefix(lower, "v-html"), strings.HasPrefix(lower, "[innerhtml]"),
		strings.HasPrefix(lower, "v-text"):
		return Markup, ""
	case strings.HasPrefix(lower, "v-model"):
		return TwoWay, ""
	case strings.HasPrefix(lower, "[(") && strings.HasSuffix(lower, ")]"):
		return TwoWay, ""
	case strings.HasPrefix(lower, "*"), strings.HasPrefix(lower, "v-if"),
		strings.HasPrefix(lower, "v-else"), strings.HasPrefix(lower, "v-for"),
		strings.HasPrefix(lower, "v-show"):
		return Structural, ""
	case strings.HasPrefix(name, "@"):
		return Event, ""
	case strings.HasPrefix(lower, "v-on:"), strings.HasPrefix(lower, "x-on:"):
		return Event, ""
	case strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")"):
		return Event, ""
	case strings.HasPrefix(name, ":"):
		return Property, target(name[1:])
	case strings.HasPrefix(lower, "v-bind:"):
		return Property, target(name[len("v-bind:"):])
	case strings.HasPrefix(lower, "x-bind:"):
		return Property, target(name[len("x-bind:"):])
	case strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]"):
		return Property, target(name[1 : len(name)-1])
	}
	return NotABinding, ""
}

// target turns a binding's subject into the plain attribute name it corresponds to, or "" when
// there is none. It lower-cases, because a plain attribute name is lower-case in HTML and adding
// one with a capital is not possible anyway.
func target(subject string) string {
	// A modifier after a dot is a framework instruction rather than part of the name, except
	// for the attr. and class. and style. prefixes, which say what the subject is.
	switch {
	case strings.HasPrefix(strings.ToLower(subject), "attr."):
		subject = subject[len("attr."):]
	case strings.HasPrefix(strings.ToLower(subject), "class."),
		strings.HasPrefix(strings.ToLower(subject), "style."):
		// A single class or style property is a computed value, not an attribute.
		return ""
	}
	if i := strings.IndexByte(subject, '.'); i >= 0 {
		subject = subject[:i]
	}
	subject = strings.ToLower(subject)
	if subject == "" || !plainName(subject) {
		return ""
	}
	return subject
}

// plainName reports whether s is a name a plain HTML attribute can have: letters, digits,
// hyphens and underscores. Anything else is a framework construct that has no plain form.
func plainName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// literal returns the plain attribute value a binding's expression is worth, and whether it is
// one at all. The value it is given is raw attribute-value source and the value it returns is
// too, so a character reference passes through unchanged rather than being decoded and re-encoded.
func literal(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "true", "false":
		return expr, true
	case "":
		return "", false
	}

	// A quoted string: an apostrophe or a backtick, which are the two quotes a
	// template expression can use inside a double-quoted attribute without escaping
	// them. A double-quoted expression is not recognised here and is reported as an
	// expression instead - a miss in the harmless direction, and the place to start
	// for anyone widening what this accepts. A backslash means an escape this does
	// not understand, so it is not treated as a literal at all.
	if len(expr) >= 2 && (expr[0] == '\'' || expr[0] == '`') && expr[len(expr)-1] == expr[0] {
		inner := expr[1 : len(expr)-1]
		if strings.ContainsAny(inner, "\\'`") {
			return "", false
		}
		return inner, true
	}

	// A number.
	digits, dots := 0, 0
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
		case c == '-' && i == 0:
		default:
			return "", false
		}
	}
	if digits > 0 && dots <= 1 {
		return expr, true
	}
	return "", false
}

// Binding is one attribute the program looked at.
type Binding struct {
	// Name is the source spelling, which is what a reader has to search for.
	Name  string
	Value string
	Kind  Kind
	// To and With are the plain attribute it became, empty when it was left alone.
	To   string
	With string
	// Why is the reason it was left alone.
	Why string
}

// Result is what a run did.
type Result struct {
	Doc      string
	Bindings []Binding
}

func (r Result) Rewritten() []Binding {
	var out []Binding
	for _, b := range r.Bindings {
		if b.To != "" {
			out = append(out, b)
		}
	}
	return out
}

func (r Result) LeftAlone() []Binding {
	var out []Binding
	for _, b := range r.Bindings {
		if b.To == "" {
			out = append(out, b)
		}
	}
	return out
}

// Rewrite turns every literal property binding in doc into a plain attribute.
func Rewrite(src io.Reader, dst io.Writer) (Result, error) {
	var res Result

	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		// The list is read once, before anything is changed, because SetAttribute and
		// RemoveAttribute re-serialise the start tag and this decides on the source.
		var todo []Binding
		for _, a := range e.AttributeList() {
			// The source spelling, not the lower-cased name: *ngif is not a
			// directive anyone wrote.
			name := a.NamePreserveCase
			kind, to := classify(name)
			if kind == NotABinding {
				continue
			}
			b := Binding{Name: name, Value: a.Value, Kind: kind}
			if kind != Property || to == "" {
				b.Why = kind.why()
				if kind == Property {
					b.Why = "the binding's subject has no plain attribute form"
				}
				todo = append(todo, b)
				continue
			}
			value, ok := literal(a.Value)
			if !ok {
				b.Why = kind.why()
				todo = append(todo, b)
				continue
			}
			b.To, b.With = to, value
			todo = append(todo, b)
		}

		for _, b := range todo {
			res.Bindings = append(res.Bindings, b)
			if b.To == "" {
				continue
			}
			if err := e.RemoveAttribute(b.Name); err != nil {
				return err
			}
			// b.With is raw attribute-value source, as read, so it is written
			// back without escaping: EscapeAttribute here would double-encode a
			// character reference the template already spelled.
			if err := e.SetAttribute(b.To, b.With); err != nil {
				return err
			}
		}
		return nil
	}))
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}
	return res, nil
}

func (r Result) String() string {
	var b strings.Builder
	rewritten, left := r.Rewritten(), r.LeftAlone()
	fmt.Fprintf(&b, "%d bindings: %d rewritten, %d left alone\n",
		len(r.Bindings), len(rewritten), len(left))
	if len(rewritten) > 0 {
		b.WriteString("  rewritten\n")
		for _, x := range rewritten {
			fmt.Fprintf(&b, "    %-26s -> %s=%q\n",
				fmt.Sprintf("%s=%q", x.Name, x.Value), x.To, x.With)
		}
	}
	if len(left) > 0 {
		b.WriteString("  left alone\n")
		// Grouped by reason, so the list is a set of decisions rather than a log.
		byWhy := map[string][]string{}
		for _, x := range left {
			byWhy[x.Why] = append(byWhy[x.Why], fmt.Sprintf("%s=%q", x.Name, x.Value))
		}
		whys := make([]string, 0, len(byWhy))
		for why := range byWhy {
			whys = append(whys, why)
		}
		sort.Strings(whys)
		for _, why := range whys {
			for _, spelling := range byWhy[why] {
				fmt.Fprintf(&b, "    %-26s %s\n", spelling, why)
			}
		}
	}
	return b.String()
}

func main() {
	report := flag.Bool("report", false, "print what was found instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "bindings:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	dst := io.Writer(os.Stdout)
	var held strings.Builder
	if *report {
		dst = &held
	}
	res, err := Rewrite(src, dst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bindings:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(res)
	}
}

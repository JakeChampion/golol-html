// Command inventory lists the custom elements a page uses and says which of them nothing
// defines.
//
//	$ inventory page.html
//	6 custom elements used, 2 defined, 4 undefined
//	  element                used   defined
//	  my-badge                  1   page.html
//	  my-card                   3   page.html
//	  my-div (is=)              1   -
//	  site-header               1   -
//	  ui-button                 4   -
//	  x-tooltip                 1   -
//	2 names that look like components and can never be one
//	  my_card            1   an underscore is not a hyphen
//	  mycard             1   spelled <myCard>, and HTML lower-cases a tag name
//	2 hyphenated names that are not custom elements
//	  annotation-xml     1   reserved by the specification
//	  font-face          1   reserved by the specification
//
// It exits non-zero when anything is undefined, since that is a component the page asks for and
// nothing provides.
//
// A definition comes from `customElements.define("name"` in a <script> in the document, or from
// -defined for the ones a bundle registers. Everything else it finds is reported as undefined,
// which is the point: an undefined custom element renders as an unstyled inline box and nothing
// in the console says which one was missing.
//
// # What counts as a custom element
//
// The specification's rule is narrower than "has a hyphen", and an inventory that uses the wide
// version is wrong in both directions. A valid name starts with an ASCII lower-case letter,
// contains a hyphen, and is not one of eight names the specification reserves for SVG and
// MathML - annotation-xml, color-profile, font-face, font-face-src, font-face-uri,
// font-face-format, font-face-name and missing-glyph. Those eight have hyphens and are not
// custom elements.
//
// The other direction is the one that costs time. HTML lower-cases a tag name, so a component
// written the way a framework spells it does not survive the parse:
//
//	source            TagName      a custom element?
//	<my-card>         my-card      yes
//	<MY-CARD>         my-card      yes, the same one
//	<myCard>          mycard       no - no hyphen once lower-cased
//	<my_card>         my_card      no - an underscore is not a hyphen
//
// A name with no hyphen and no capitals - <fancybox> - is not reported at all, because it is
// indistinguishable from a typo of a built-in and reporting every unknown element would bury
// the ones that matter. The two rows above are reported, because a spelling with capitals or an
// underscore is evidence that someone meant a component.
//
// Measured with [lolhtml.Element.TagName], which reports what the tokenizer produced.
// [lolhtml.Element.TagNamePreserveCase] reports the source spelling, and the inventory keeps
// both: the lower-case name is what a definition has to match, and the spelling is what tells
// the reader why their component never upgraded.
//
// # One rewriter per document, not one per fragment
//
// The package documentation promises that chunk boundaries never affect handler behaviour, and
// they do not - for one rewriter. Rewriting two fragments with two rewriters and joining the
// outputs is a different thing, and it is not safe, because a fragment that ends inside a tag
// is invisible to every handler and is emitted verbatim.
//
// Measured on `<p>a</p><script>alert(1)</script><p>b</p>`, removing every script, over all forty
// places the document can be cut in two:
//
//	one rewriter, whole document          saw p script p   <p>a</p><p>b</p>
//	one rewriter, two writes, any cut     saw p script p   <p>a</p><p>b</p>
//	two rewriters, cuts 9 to 15           saw p and p      <p>a</p><script>alert(1)</script><p>b</p>
//	two rewriters, cut 16                 saw p script, p  <p>a</p>alert(1)</script><p>b</p>
//	two rewriters, every other cut        the script is removed
//
// Seven cuts land strictly inside the eight bytes of `<script>`, and each of them puts a live
// script element back into the output that neither pass ever saw: the first fragment ends
// mid-tag, so no element handler runs for it and the bytes pass through, and the second begins
// mid-name, so its remainder is text and passes through too. The join reassembles them.
//
// Cut 16 is the boundary immediately after the complete start tag, and it fails differently:
// the first pass does see the script and removes it, but the content was in the other fragment,
// so the payload survives as text next to a stray end tag. Not executable, and not the document
// either.
//
// Every one of those is silent - no error from either pass. So an inventory of a page assembled
// from separately-rewritten fragments undercounts, and a sanitiser built the same way has a
// hole. Feed the fragments to one rewriter as successive writes instead, which the measurement
// above shows is correct at every boundary. This program reads its input with [io.Copy] into a
// single writer for that reason.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// reserved is the specification's list of hyphenated names that are not custom element names.
var reserved = map[string]bool{
	"annotation-xml":   true,
	"color-profile":    true,
	"font-face":        true,
	"font-face-src":    true,
	"font-face-uri":    true,
	"font-face-format": true,
	"font-face-name":   true,
	"missing-glyph":    true,
}

// Use is one element name the document used.
type Use struct {
	Name string
	// Spellings are the source spellings seen for it, so a report can say <myCard> rather
	// than mycard.
	Spellings map[string]int
	Count     int
	// DefinedAt is where a definition for it was found, empty if none was.
	DefinedAt string
	// Is is true when the name came from an is= attribute rather than from a tag.
	Is bool
}

// Kind says how a name relates to the custom element rules.
type Kind int

const (
	// Custom is a valid custom element name.
	Custom Kind = iota
	// Reserved is hyphenated but on the specification's list.
	Reserved
	// Impossible looks like a component and can never be one: no hyphen after
	// lower-casing, or a character a name cannot have.
	Impossible
	// Builtin is an ordinary HTML element, which the inventory ignores.
	Builtin
)

// classify applies the specification's rule to a tokenizer-produced name.
func classify(name, spelling string) Kind {
	if name == "" {
		return Builtin
	}
	if reserved[name] {
		return Reserved
	}
	if strings.Contains(name, "-") {
		if c := name[0]; c < 'a' || c > 'z' {
			return Impossible
		}
		return Custom
	}
	// No hyphen. It only counts as a name someone meant as a component if it looks
	// deliberate: an underscore, or capitals in the source that a hyphen would have made
	// work. Everything else is an ordinary unknown element.
	if strings.Contains(name, "_") {
		return Impossible
	}
	if spelling != name && strings.ContainsAny(spelling, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return Impossible
	}
	return Builtin
}

// Report is the inventory.
type Report struct {
	Uses map[string]*Use
	// Kinds is the classification of each name in Uses.
	Kinds map[string]Kind
	// Definitions maps a name to where it was defined.
	Definitions map[string]string
}

// byKind returns the names of one kind, in order.
func (r Report) byKind(k Kind) []string {
	var out []string
	for name, kind := range r.Kinds {
		if kind == k {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Custom lists the valid custom element names used.
func (r Report) Custom() []string { return r.byKind(Custom) }

// Undefined lists the custom elements nothing defines.
func (r Report) Undefined() []string {
	var out []string
	for _, name := range r.Custom() {
		if r.Uses[name].DefinedAt == "" {
			out = append(out, name)
		}
	}
	return out
}

// defineRe finds customElements.define("name" in script text. The name is the first argument and
// the quote may be single, double or a backtick.
var defineRe = regexp.MustCompile(`customElements\s*\.\s*define\s*\(\s*['"` + "`" + `]([^'"` + "`" + `]+)`)

// Take builds the inventory of src. name is what to call the document in the report, and defined
// is the names a bundle registers that no script in the document mentions.
func Take(src io.Reader, name string, defined []string) (Report, error) {
	r := Report{
		Uses:        map[string]*Use{},
		Kinds:       map[string]Kind{},
		Definitions: map[string]string{},
	}
	for _, d := range defined {
		r.Definitions[strings.ToLower(d)] = "-defined"
	}

	use := func(tag, spelling string, isAttr bool) {
		u, ok := r.Uses[tag]
		if !ok {
			u = &Use{Name: tag, Spellings: map[string]int{}, Is: isAttr}
			r.Uses[tag] = u
			r.Kinds[tag] = classify(tag, spelling)
		}
		u.Count++
		u.Spellings[spelling]++
	}

	// script text arrives in chunks, and a define call can straddle two of them, so the
	// text of each script is accumulated and searched once at the end of the node.
	var script strings.Builder

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			use(e.TagName(), e.TagNamePreserveCase(), false)

			// A customized built-in is a use of a custom element too, and the name
			// is in the attribute rather than the tag. It only counts on a built-in:
			// on a custom element the specification says to ignore it.
			if is, ok := e.Attribute("is"); ok && is != "" {
				if classify(e.TagName(), e.TagNamePreserveCase()) == Builtin {
					use(strings.ToLower(is), is, true)
				}
			}
			return nil
		}),
		lolhtml.OnText("script", func(t *lolhtml.TextChunk) error {
			script.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				return nil
			}
			for _, m := range defineRe.FindAllStringSubmatch(script.String(), -1) {
				n := strings.ToLower(strings.TrimSpace(m[1]))
				if _, already := r.Definitions[n]; !already {
					r.Definitions[n] = name
				}
			}
			script.Reset()
			return nil
		}))
	if err != nil {
		return r, err
	}
	// One rewriter for the whole input, however it arrives: see the package comment on
	// fragments.
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return r, err
	}
	if err := w.Close(); err != nil {
		return r, err
	}

	for n, u := range r.Uses {
		if at, ok := r.Definitions[n]; ok {
			u.DefinedAt = at
		}
	}
	return r, nil
}

func (r Report) String() string {
	var b strings.Builder
	custom := r.Custom()
	undefined := r.Undefined()
	fmt.Fprintf(&b, "%d custom elements used, %d defined, %d undefined\n",
		len(custom), len(custom)-len(undefined), len(undefined))
	if len(custom) > 0 {
		fmt.Fprintf(&b, "  %-20s %6s   %s\n", "element", "used", "defined")
		for _, n := range custom {
			u := r.Uses[n]
			at := u.DefinedAt
			if at == "" {
				at = "-"
			}
			label := n
			if u.Is {
				label = n + " (is=)"
			}
			fmt.Fprintf(&b, "  %-20s %6d   %s\n", label, u.Count, at)
		}
	}

	if names := r.byKind(Impossible); len(names) > 0 {
		fmt.Fprintf(&b, "%d name%s that look like components and can never be one\n",
			len(names), plural(len(names)))
		for _, n := range names {
			fmt.Fprintf(&b, "  %-16s %3d   %s\n", n, r.Uses[n].Count, why(n, r.Uses[n]))
		}
	}
	if names := r.byKind(Reserved); len(names) > 0 {
		fmt.Fprintf(&b, "%d hyphenated name%s that are not custom elements\n",
			len(names), plural(len(names)))
		for _, n := range names {
			fmt.Fprintf(&b, "  %-16s %3d   reserved by the specification\n",
				n, r.Uses[n].Count)
		}
	}
	return b.String()
}

// why says what stopped a name from being a custom element, which is the line a reader acts on.
func why(name string, u *Use) string {
	if strings.Contains(name, "_") {
		return "an underscore is not a hyphen"
	}
	for spelling := range u.Spellings {
		if spelling != name {
			return fmt.Sprintf("spelled <%s>, and HTML lower-cases a tag name", spelling)
		}
	}
	return "a custom element name starts with a lower-case ASCII letter"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type nameList []string

func (l *nameList) String() string { return strings.Join(*l, ",") }

func (l *nameList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l = append(*l, part)
		}
	}
	return nil
}

func main() {
	var defined nameList
	flag.Var(&defined, "defined", "comma-separated names a bundle registers, repeatable")
	flag.Parse()

	src, name := io.Reader(os.Stdin), "stdin"
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "inventory:", err)
			os.Exit(1)
		}
		defer f.Close()
		src, name = f, flag.Arg(0)
	}

	r, err := Take(src, name, defined)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inventory:", err)
		os.Exit(1)
	}
	fmt.Print(r)

	// An undefined custom element is the thing worth a non-zero exit: it is a component
	// the page asks for and nothing provides.
	if len(r.Undefined()) > 0 {
		os.Exit(1)
	}
}

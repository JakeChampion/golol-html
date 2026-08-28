// Command islands annotates the interactive regions of a page for partial hydration: which
// ones there are, which are inside which, and what each one needs to hydrate.
//
//	$ islands page.html
//	5 islands, 2 of them nested, deepest 3
//	  id   island          hydrate     inside   props
//	  i1   Header          visible     -        -
//	  i2   Cart            visible     -        count=3
//	  i3   CartBadge       parent      i2       label=Items & more
//	  i4   Search          idle        -        -
//	  i5   SearchSuggest   parent      i4       -
//
// The annotation is three attributes per island - a unique id, a hydration strategy, and the id
// of the enclosing island where there is one - plus a manifest a bundler can read.
//
// # A selector cannot say "not inside another island"
//
// Which is the one thing this program most needs to ask. In CSS it is
// `[data-island]:not([data-island] [data-island])`, and that selector is rejected:
//
//	lolhtml: invalid selector "[data-island]:not([data-island] [data-island])":
//	Unsupported pseudo-class or pseudo-element in selector.
//
// Any combinator inside :not() is rejected, and only inside :not(). Measured, on the same
// document, the whole boundary:
//
//	:not(div)  :not(.a)  :not(#i)  :not([a])  :not([a=v])  :not(*)   accepted
//	:not(div.a)  :not(div, span)                                     accepted, see below
//	:not(:first-child)  :not(:nth-child(2))  :not(:not(div))         accepted
//	:not(div p)  :not(div > p)  :not(div + p)  :not(div ~ p)         rejected
//
// The rejection is not the rule the library documents. That rule is that a selector works if the
// rewriter can decide it at the start tag, and "is this element inside a [data-island]" is
// decidable at the start tag - the plain descendant selector `[data-island] [data-island]`
// works, and this program uses it as a cross-check. What is missing is the negation of it, and
// the error blames the pseudo-class rather than the combinator inside it.
//
// So the nesting comes from a stack instead: push at the start tag, pop at the end tag, and the
// island's parent is whatever is on top when it arrives. That has a trap of its own.
//
// # A void element marked as an island would abort the whole rewrite
//
// [lolhtml.Element.OnEndTag] returns an error on an element that has no end tag, and that error
// fails the rewrite rather than the handler. A page with `<img data-island="Hero">` in it - which
// is a reasonable thing for a template to emit and a mistake this program should survive - would
// produce no output at all. So every push checks [lolhtml.Element.CanHaveContent] first, and an
// island that cannot contain anything is annotated and counted but never pushed.
//
// # An island with no end tag of its own swallows what follows it
//
// A rewriter has no tree and does not apply the parser's implied end tags, so in
// `<ul><li data-island="A">x<li data-island="B">y</ul>` the handlers run in the order
// start A, start B, end B, end A: A is still open when B arrives, and both a stack and the
// descendant selector make B a child of A. The HTML tree has them as siblings. There is no
// answer available to a streaming rewriter, so the honest thing is to say when the question
// arose: an end tag that names some other element closed one whose own was omitted, and this
// program marks every island that ends that way. Paragraphs, table rows and definition lists
// behave the same.
//
// The check is on [lolhtml.EndTag.Name] against the element's own tag name, and the tag name has
// to be read at the start tag, because inside the end-tag handler the element is detached and
// reports nothing.
//
// # What the annotation cannot know
//
// An island whose end tag never arrives never pops, because an end-tag handler for an element
// nothing closes never runs. In a truncated document that is invisible: the stack is left deep
// and there is nothing after it to mis-nest. The report says how deep the stack was when the
// document ended, which is the only honest signal available.
package main

import (
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Island is one annotated region.
type Island struct {
	ID     string
	Name   string
	Parent string
	Depth  int
	// Hydrate is the strategy: the page's own if it named one, "parent" for a nested island,
	// or the default otherwise.
	Hydrate string
	// Props are the data-prop-* attributes, decoded. The document keeps its own spelling -
	// only the manifest sees the decoded value.
	Props map[string]string
	// Void is true for an island on an element that cannot contain anything, which is a
	// template mistake worth reporting rather than a reason to fail.
	Void bool
	// EndOmitted is true when the island was closed by some other element's end tag,
	// because its own was left out. Anything this manifest calls a child of it may be a
	// following sibling instead - see "An island with no end tag of its own swallows what
	// follows it".
	EndOmitted bool
}

// Result is what a run found.
type Result struct {
	Doc     string
	Islands []Island
	// DepthAtEnd is how many islands were still open when the document ended. Anything but
	// zero means end tags were missing.
	DepthAtEnd int
}

// Nested counts the islands that are inside another one.
func (r Result) Nested() int {
	n := 0
	for _, is := range r.Islands {
		if is.Parent != "" {
			n++
		}
	}
	return n
}

// Deepest is the depth of the most deeply nested island, counting from one.
func (r Result) Deepest() int {
	d := 0
	for _, is := range r.Islands {
		if is.Depth+1 > d {
			d = is.Depth + 1
		}
	}
	return d
}

// Annotate rewrites src, adding the three attributes to every element carrying attr, and returns
// the manifest alongside the document. def is the hydration strategy for an island that does not
// name one and is not nested.
func Annotate(src io.Reader, attr, def string) (Result, error) {
	if attr == "" {
		return Result{}, errors.New("islands: the marker attribute cannot be empty")
	}
	if def == "" {
		return Result{}, errors.New("islands: the default strategy cannot be empty")
	}

	var res Result
	var stack []string // ids of the open islands, outermost first
	next := 0

	var out strings.Builder
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnElement("["+attr+"]", func(e *lolhtml.Element) error {
			next++
			id := "i" + strconv.Itoa(next)

			name, _ := e.Attribute(attr)
			is := Island{
				ID:    id,
				Name:  html.UnescapeString(name),
				Depth: len(stack),
				Props: props(e),
				Void:  !e.CanHaveContent(),
			}
			if len(stack) > 0 {
				is.Parent = stack[len(stack)-1]
			}

			// A page that already said how this island hydrates keeps its answer -
			// and keeps its own spelling of it.
			declared := false
			switch existing, ok := e.Attribute("data-hydrate"); {
			case ok && existing != "":
				declared = true
				is.Hydrate = html.UnescapeString(existing)
			case is.Parent != "":
				is.Hydrate = "parent"
			default:
				is.Hydrate = def
			}

			if err := e.SetAttribute("data-island-id", id); err != nil {
				return err
			}
			// Only when the page did not write one. Attribute returns raw source
			// text with character references still encoded and SetAttribute takes
			// raw source text, so reading a value, decoding it and writing it
			// straight back decodes it once too often: data-hydrate="a&amp;amp;b"
			// came out as "a&amp;b", and what a browser reads changed from
			// "a&amp;b" to "a&b". The manifest holds the decoded form, the
			// document keeps the encoded one - the same split props relies on.
			if !declared {
				if err := e.SetAttribute("data-hydrate", is.Hydrate); err != nil {
					return err
				}
			}
			if is.Parent != "" {
				if err := e.SetAttribute("data-island-parent", is.Parent); err != nil {
					return err
				}
			}
			res.Islands = append(res.Islands, is)

			// An element with no end tag cannot contain anything, and asking it for one
			// fails the whole rewrite rather than this handler - so ask CanHaveContent
			// first and leave a void island off the stack.
			if is.Void {
				return nil
			}
			stack = append(stack, id)

			// The tag name has to be read here: inside the end-tag handler the
			// element is already detached and TagName reports nothing.
			tag := e.TagName()
			at := len(res.Islands) - 1
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				// An end tag that names something else closed this element
				// because its own was omitted, which is the signal that its
				// apparent children may be siblings.
				if !strings.EqualFold(end.Name(), tag) {
					res.Islands[at].EndOmitted = true
				}
				return nil
			})
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

	res.Doc = out.String()
	res.DepthAtEnd = len(stack)
	return res, nil
}

// props reads the data-prop-* attributes into the manifest, decoded. The attribute itself is
// left exactly as the page wrote it: the decoded form is what a bundler wants and the raw form
// is what the browser needs.
func props(e *lolhtml.Element) map[string]string {
	var out map[string]string
	for _, a := range e.AttributeList() {
		name, ok := strings.CutPrefix(strings.ToLower(a.Name), "data-prop-")
		if !ok || name == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = html.UnescapeString(a.Value)
	}
	return out
}

// NestedBySelector answers "which islands are inside another one" a second way, with the plain
// descendant selector that the negation of it cannot use. It exists to check the stack: two
// methods that disagree mean the stack is wrong, and a test says so.
func NestedBySelector(src, attr string) (int, error) {
	n := 0
	sel := "[" + attr + "] [" + attr + "]"
	if _, err := lolhtml.RewriteString(src,
		lolhtml.OnElement(sel, func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
		return 0, err
	}
	return n, nil
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d islands, %d of them nested, deepest %d\n",
		len(r.Islands), r.Nested(), r.Deepest())
	if len(r.Islands) == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "  %-4s %-15s %-11s %-8s %s\n", "id", "island", "hydrate", "inside", "props")
	for _, is := range r.Islands {
		fmt.Fprintf(&b, "  %-4s %-15s %-11s %-8s %s\n",
			is.ID, dash(is.Name), is.Hydrate, dash(is.Parent), dash(propList(is.Props)))
	}
	if voids := countVoid(r.Islands); voids > 0 {
		fmt.Fprintf(&b, "  %d island%s on an element that cannot contain anything: annotated, "+
			"but nothing can be nested inside it\n", voids, plural(voids))
	}
	if omitted := countOmitted(r.Islands); omitted > 0 {
		fmt.Fprintf(&b, "  %d island%s closed by another element's end tag, so what this "+
			"says is inside them may be a following sibling\n",
			omitted, plural(omitted))
	}
	if r.DepthAtEnd > 0 {
		fmt.Fprintf(&b, "  %d island%s never closed, so the document ended inside them\n",
			r.DepthAtEnd, plural(r.DepthAtEnd))
	}
	return b.String()
}

func countVoid(islands []Island) int {
	n := 0
	for _, is := range islands {
		if is.Void {
			n++
		}
	}
	return n
}

func countOmitted(islands []Island) int {
	n := 0
	for _, is := range islands {
		if is.EndOmitted {
			n++
		}
	}
	return n
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func propList(props map[string]string) string {
	if len(props) == 0 {
		return ""
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+props[k])
	}
	return strings.Join(parts, " ")
}

func main() {
	attr := flag.String("attr", "data-island", "the attribute that marks an island")
	def := flag.String("strategy", "visible", "hydration strategy for a top-level island that does not name one")
	doc := flag.Bool("doc", false, "print the annotated document instead of the manifest")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "islands:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	res, err := Annotate(src, *attr, *def)
	if err != nil {
		fmt.Fprintln(os.Stderr, "islands:", err)
		os.Exit(1)
	}
	if *doc {
		fmt.Print(res.Doc)
		return
	}
	fmt.Print(res)
}

// Command esi expands Edge Side Include markers two ways - with [lolhtml.WithESITags] and without
// it - and reports where the two disagree.
//
//	$ esi -frag /header='<h1>Site</h1>' page.html
//	with the option        3 includes expanded, output 214 bytes
//	without it             3 includes expanded, output 262 bytes, 3 markers left in
//	  the two outputs differ: the markers, and nothing else
//	  every element the source had is still there, in the same place
//
// examples/gip/include is the one to copy: it uses the option, fetches in the handler rather than
// in the sink, and implements the spec's error handling. This one exists to answer a narrower
// question - what the option is actually buying - because "use the option" is easier to follow
// when the alternative has been measured.
//
// # What the option buys
//
// Without it, an esi: element is an ordinary container. It is conventionally written unclosed, so
// its content runs to the next end tag, which belongs to whatever encloses it. Every operation
// positioned at the element's end therefore acts on a range that is not the element. Measured on
//
//	<div><p>before</p><esi:include src="/frag"/><p>after</p></div>
//
// expanding the include into <b>F</b>:
//
//	operation                     without the option                        with it
//	Replace                       loses <p>after</p> and </div>             correct
//	Before then Remove            loses <p>after</p> and </div>             correct
//	SetInnerContent               keeps the marker, loses <p>after</p>      correct
//	RemoveAndKeepContent          loses </div>                              correct
//	Before then RemoveAndKeep     loses </div>                              correct
//	Before alone                  correct, and the marker stays             the marker stays
//
// So there is exactly one lossless way to do it without the option, and it leaves the marker in
// the output. Everything that also removes the marker takes the enclosing element's end tag with
// it.
//
// # Why the marker being left in is not nothing, and not much
//
// It is not nothing, because the marker is an open element: in the source tree `<p>after</p>` is
// already a child of the esi:include rather than of the div. Leaving it there keeps that, so a
// selector like `div > p` matches one paragraph instead of two - in the source and in the output
// alike.
//
// It is not much, because the source tree is that shape before any rewrite. Measured against
// x/net/html, `Before` alone produces the source tree with the fragment added and nothing moved.
// The operations that remove the marker produce a *different* tree, and on a document that ends
// soon after the include the difference does not show:
//
//	document                                       Before+RemoveAndKeep          WithESITags
//	<div>…include…<p>after</p></div>                div > p, b, p                 the same
//	<div>…include…</div><section><p>tail</p></…>    section moved inside the div  section stays outside
//
// A rewrite tested on the first shape and run on the second re-parents everything after the
// include, silently. That is the cost of avoiding the option, and it is not worth paying.
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

// Fragments resolves an ESI src to the markup it stands for.
type Fragments map[string]string

// Outcome is what one expansion did.
type Outcome struct {
	// Name is "with the option" or "without it".
	Name string
	Doc  string
	// Expanded is how many includes were expanded, Markers how many were left in the
	// output, and Missing how many had no fragment.
	Expanded int
	Markers  int
	Missing  int
}

// Elements counts the elements a parser finds in the output, keyed by name, which is how a
// re-parse says whether anything moved or vanished.
func (o Outcome) Elements() (map[string]int, error) {
	counts := map[string]int{}
	if _, err := lolhtml.RewriteString(o.Doc, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		counts[e.TagName()]++
		return nil
	})); err != nil {
		return nil, err
	}
	return counts, nil
}

// WithOption expands every include using WithESITags, which makes an esi: element void, so
// Replace is positioned at the marker rather than at the enclosing element's end.
func WithOption(doc string, frags Fragments) (Outcome, error) {
	out := Outcome{Name: "with the option"}
	rewritten, err := lolhtml.RewriteString(doc,
		lolhtml.WithESITags(),
		lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
			src, _ := e.Attribute("src")
			body, ok := frags[src]
			if !ok {
				out.Missing++
				out.Markers++
				return nil
			}
			out.Expanded++
			return e.Replace(body, lolhtml.HTML)
		}))
	if err != nil {
		return out, err
	}
	out.Doc = rewritten
	return out, nil
}

// WithoutOption expands every include with the one operation that loses nothing when an esi:
// element is an ordinary unclosed container: an insertion positioned at the start tag. The marker
// stays, because everything that removes it is positioned at an end tag that is not the marker's.
func WithoutOption(doc string, frags Fragments) (Outcome, error) {
	out := Outcome{Name: "without it"}
	rewritten, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
			src, _ := e.Attribute("src")
			body, ok := frags[src]
			out.Markers++
			if !ok {
				out.Missing++
				return nil
			}
			out.Expanded++
			return e.Before(body, lolhtml.HTML)
		}))
	if err != nil {
		return out, err
	}
	out.Doc = rewritten
	return out, nil
}

// Comparison is what the two outcomes say about each other.
type Comparison struct {
	With, Without Outcome
	// SameElements is true when the two outputs hold the same elements in the same numbers,
	// once the markers the second one left in are discounted.
	SameElements bool
	// Extra names the element counts that differ, which should be the marker alone.
	Extra map[string]int
}

// Compare runs both and reports the difference.
func Compare(doc string, frags Fragments) (Comparison, error) {
	var c Comparison
	var err error
	if c.With, err = WithOption(doc, frags); err != nil {
		return c, err
	}
	if c.Without, err = WithoutOption(doc, frags); err != nil {
		return c, err
	}

	withCounts, err := c.With.Elements()
	if err != nil {
		return c, err
	}
	withoutCounts, err := c.Without.Elements()
	if err != nil {
		return c, err
	}

	c.Extra = map[string]int{}
	for name, n := range withoutCounts {
		if diff := n - withCounts[name]; diff != 0 {
			c.Extra[name] = diff
		}
	}
	for name, n := range withCounts {
		if _, seen := withoutCounts[name]; !seen {
			c.Extra[name] = -n
		}
	}
	// The marker is the difference that is expected; anything else is not.
	c.SameElements = true
	for name := range c.Extra {
		if name != "esi:include" {
			c.SameElements = false
		}
	}
	return c, nil
}

func (c Comparison) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %d includes expanded, output %d bytes\n",
		c.With.Name, c.With.Expanded, len(c.With.Doc))
	fmt.Fprintf(&b, "%-22s %d includes expanded, output %d bytes, %d marker%s left in\n",
		c.Without.Name, c.Without.Expanded, len(c.Without.Doc),
		c.Without.Markers, plural(c.Without.Markers))
	if c.With.Missing > 0 {
		fmt.Fprintf(&b, "  %d include%s had no fragment and were left alone\n",
			c.With.Missing, plural(c.With.Missing))
	}
	if c.SameElements {
		b.WriteString("  the two outputs differ: the markers, and nothing else\n")
	} else {
		names := make([]string, 0, len(c.Extra))
		for name := range c.Extra {
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString("  the two outputs differ by more than the markers:\n")
		for _, name := range names {
			fmt.Fprintf(&b, "    %-16s %+d without the option\n", name, c.Extra[name])
		}
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type fragList map[string]string

func (f fragList) String() string { return "" }

func (f fragList) Set(s string) error {
	src, body, ok := strings.Cut(s, "=")
	if !ok || src == "" {
		return fmt.Errorf("%q: want src=markup", s)
	}
	f[src] = body
	return nil
}

func main() {
	frags := fragList{}
	flag.Var(frags, "frag", "src=markup, repeatable")
	which := flag.String("use", "compare", `"option", "manual" or "compare"`)
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "esi:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}
	// The whole document, because comparing two expansions means holding both.
	doc, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "esi:", err)
		os.Exit(1)
	}

	switch *which {
	case "option":
		out, err := WithOption(string(doc), Fragments(frags))
		if err != nil {
			fmt.Fprintln(os.Stderr, "esi:", err)
			os.Exit(1)
		}
		fmt.Print(out.Doc)
	case "manual":
		out, err := WithoutOption(string(doc), Fragments(frags))
		if err != nil {
			fmt.Fprintln(os.Stderr, "esi:", err)
			os.Exit(1)
		}
		fmt.Print(out.Doc)
	default:
		c, err := Compare(string(doc), Fragments(frags))
		if err != nil {
			fmt.Fprintln(os.Stderr, "esi:", err)
			os.Exit(1)
		}
		fmt.Print(c)
		if !c.SameElements {
			os.Exit(1)
		}
	}
}

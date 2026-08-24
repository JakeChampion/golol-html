// Command descript removes script elements and reports what that saved.
//
//	descript < page.html > out.html
//	descript -inline-only -report < page.html
//
// Reporting the saving is most of the work, because an element's own
// SourceLocation is its start tag and nothing else. The extent of an element is
// the start of its start tag to the end of its end tag, which means holding the
// start location until the end tag arrives - and an element whose end tag never
// comes has no measurable extent at all, which is reported rather than guessed.
//
// A void element has no end tag, so OnEndTag fails on one rather than doing
// nothing. Scripts are never void, so this program does not need the guard; a
// broader selector would.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	inlineOnly := flag.Bool("inline-only", false, "remove only scripts with no src")
	keepJSON := flag.Bool("keep-json", true, `keep scripts whose type is not JavaScript, such as application/json`)
	report := flag.Bool("report", false, "list each removal on stderr")
	flag.Parse()

	r := &remover{inlineOnly: *inlineOnly, keepJSON: *keepJSON, verbose: *report}
	if err := r.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "descript:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, r.report())
}

// executableTypes are the type values that make a script element executable. An
// empty type, or no type at all, means JavaScript.
func executable(typ string) bool {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch t {
	case "", "module",
		"text/javascript", "application/javascript", "text/ecmascript",
		"application/ecmascript", "text/jscript", "text/livescript",
		"application/x-javascript":
		return true
	}
	return false
}

type removal struct {
	inline bool
	typ    string
	src    string
	bytes  int
}

type remover struct {
	inlineOnly bool
	keepJSON   bool
	verbose    bool

	removed  []removal
	kept     map[string]int
	unclosed int
	// open holds the start location of the script being read, because the extent
	// is not known until the end tag.
	open *pendingScript
}

type pendingScript struct {
	start  int
	inline bool
	typ    string
	src    string
}

func (r *remover) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (r *remover) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			typ, _ := e.Attribute("type")
			src, hasSrc := e.Attribute("src")

			switch {
			case r.keepJSON && !executable(typ):
				r.note("not executable: " + strings.ToLower(strings.TrimSpace(typ)))
				return nil
			case r.inlineOnly && hasSrc:
				r.note("external, and -inline-only was given")
				return nil
			}

			p := &pendingScript{
				start:  e.SourceLocation().Start,
				inline: !hasSrc,
				typ:    typ,
				src:    src,
			}
			r.open = p
			e.Remove()

			// The saving is start-of-start-tag to end-of-end-tag, and the second
			// half only exists here.
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				open := r.open
				r.open = nil
				if open == nil {
					return nil
				}
				r.removed = append(r.removed, removal{
					inline: open.inline,
					typ:    open.typ,
					src:    open.src,
					bytes:  t.SourceLocation().End - open.start,
				})
				return nil
			})
		}),

		// A script whose end tag never arrives is removed all the same - the
		// rewriter takes everything to the end of the input - but its extent
		// cannot be measured, so it is counted separately rather than folded
		// into a number that would then be wrong.
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			if r.open != nil {
				r.unclosed++
				r.open = nil
			}
			return nil
		}),
	}
}

func (r *remover) note(reason string) {
	if r.kept == nil {
		r.kept = map[string]int{}
	}
	r.kept[reason]++
}

func (r *remover) saved() int {
	n := 0
	for _, rm := range r.removed {
		n += rm.bytes
	}
	return n
}

func (r *remover) report() string {
	inline, external := 0, 0
	for _, rm := range r.removed {
		if rm.inline {
			inline++
		} else {
			external++
		}
	}

	reasons := make([]string, 0, len(r.kept))
	total := 0
	for reason, n := range r.kept {
		reasons = append(reasons, fmt.Sprintf("%s=%d", reason, n))
		total += n
	}
	sort.Strings(reasons)

	var sb strings.Builder
	fmt.Fprintf(&sb, "scripts removed=%d (inline=%d external=%d) bytes=%d kept=%d",
		len(r.removed), inline, external, r.saved(), total)
	if len(reasons) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(reasons, "; "))
	}
	sb.WriteString("\n")

	if r.unclosed > 0 {
		fmt.Fprintf(&sb, "note: %d script(s) had no end tag, so their size is not counted\n",
			r.unclosed)
	}
	if r.verbose {
		for _, rm := range r.removed {
			what := "inline"
			if !rm.inline {
				what = rm.src
			}
			fmt.Fprintf(&sb, "removed %d bytes: %s\n", rm.bytes, what)
		}
	}
	return sb.String()
}

func removeString(in string, opts ...func(*remover)) (string, *remover, error) {
	r := &remover{keepJSON: true}
	for _, o := range opts {
		o(r)
	}
	var out bytes.Buffer
	err := r.run(strings.NewReader(in), &out)
	return out.String(), r, err
}

// Command noevents removes the ways markup can execute script: inline event
// handler attributes, and javascript: URLs.
//
//	noevents < untrusted.html > safe.html
//	noevents -report < untrusted.html
//
// It works by name rather than by list. A browser dispatches any attribute
// beginning with "on", including ones that did not exist when a list was written,
// so an allow-list of known handlers is a filter with a hole in it. Every "on*"
// attribute goes.
//
// This is an attribute-only rewrite: nothing is inserted and no element is
// removed, so the document's structure is untouched. That is a property worth
// stating because it is what makes this safe to run over content you do not
// control - the output is the input with attributes taken away, and nothing else.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	report := flag.Bool("report", false, "list each removal on stderr")
	keepURLs := flag.Bool("keep-javascript-urls", false, "leave javascript: URLs alone")
	flag.Parse()

	s := &sanitiser{verbose: *report, stripURLs: !*keepURLs}
	if err := s.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "noevents:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.report())
	if s.total() > 0 {
		os.Exit(1)
	}
}

// urlAttrs are the attributes whose value a browser will navigate or fetch, and
// so the ones where a javascript: scheme executes.
var urlAttrs = []string{"href", "src", "action", "formaction", "data", "poster", "cite"}

type sanitiser struct {
	verbose   bool
	stripURLs bool

	handlers map[string]int
	urls     int
	samples  []string
}

func (s *sanitiser) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, s.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (s *sanitiser) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			// Collect first, then remove. Removing while iterating happens to
			// work, but collecting makes the intent obvious and costs one small
			// slice per element that has something to remove.
			var doomed []string
			for name := range e.Attributes() {
				if isEventHandler(name) {
					doomed = append(doomed, name)
				}
			}
			for _, name := range doomed {
				s.noteHandler(name, e.TagName())
				if err := e.RemoveAttribute(name); err != nil {
					return err
				}
			}

			if !s.stripURLs {
				return nil
			}
			for _, name := range urlAttrs {
				v, ok := e.Attribute(name)
				if !ok || !isScriptURL(v) {
					continue
				}
				s.noteURL(name, e.TagName(), v)
				// Removed rather than replaced with "#": a javascript: href is
				// usually a button written as a link, and leaving a dead link
				// behind is less confusing than leaving one that scrolls to the
				// top of the page.
				if err := e.RemoveAttribute(name); err != nil {
					return err
				}
			}
			return nil
		}),
	}
}

// isEventHandler matches by shape, not by list. Attribute names arrive
// lowercased, so a simple prefix test is enough - and it catches the handlers
// that were added to the platform after any list was written.
//
// "on" alone is not a handler, and neither is anything with a hyphen after the
// prefix: "on-click" is a data attribute by convention and not dispatched.
func isEventHandler(name string) bool {
	if !strings.HasPrefix(name, "on") || len(name) < 3 {
		return false
	}
	rest := name[2:]
	for _, r := range rest {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// isScriptURL recognises a javascript: scheme the way a browser does, which
// takes two steps in this order.
//
// First decode. The value arrives as raw source, with character references still
// encoded, and a browser decodes them before it looks at the scheme. So
// "java&#9;script:x()" is "java<tab>script:x()" to the browser and executes,
// while a check on the raw string sees a scheme called "java&#9;script" and lets
// it through. An earlier version of this program did exactly that.
//
// Then strip. A scheme may carry whitespace and control characters, which are
// removed before the scheme is compared, so " JavaScript:" and "java\tscript:"
// both execute.
//
// The general rule: decide on the decoded form, rewrite the raw one.
func isScriptURL(v string) bool {
	decoded := stdhtml.UnescapeString(v)

	var b strings.Builder
	for _, r := range decoded {
		if r <= ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.HasPrefix(strings.ToLower(b.String()), "javascript:")
}

func (s *sanitiser) noteHandler(name, tag string) {
	if s.handlers == nil {
		s.handlers = map[string]int{}
	}
	s.handlers[name]++
	s.sample(fmt.Sprintf("<%s %s=...>", tag, name))
}

func (s *sanitiser) noteURL(name, tag, value string) {
	s.urls++
	s.sample(fmt.Sprintf("<%s %s=%q>", tag, name, truncate(stdhtml.UnescapeString(value))))
}

func (s *sanitiser) sample(x string) {
	if s.verbose && len(s.samples) < 40 {
		s.samples = append(s.samples, x)
	}
}

func (s *sanitiser) total() int {
	n := s.urls
	for _, c := range s.handlers {
		n += c
	}
	return n
}

func (s *sanitiser) report() string {
	names := make([]string, 0, len(s.handlers))
	for n, c := range s.handlers {
		names = append(names, fmt.Sprintf("%s=%d", n, c))
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "removed handlers=%d javascript-urls=%d",
		s.total()-s.urls, s.urls)
	if len(names) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(names, " "))
	}
	sb.WriteString("\n")
	for _, x := range s.samples {
		fmt.Fprintf(&sb, "removed: %s\n", x)
	}
	return sb.String()
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 40 {
		return s
	}
	return s[:40] + "..."
}

func sanitiseString(in string, opts ...func(*sanitiser)) (string, *sanitiser, error) {
	s := &sanitiser{stripURLs: true}
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run(strings.NewReader(in), &out)
	return out.String(), s, err
}

// Command modulesplit turns a classic script into a module-and-fallback pair.
//
//	modulesplit -module-suffix .mjs page.html
//	<script type="module" src="/a.mjs"></script>
//	<script src="/a.js" nomodule defer></script>
//
// A browser with module support runs the first and ignores the second; one
// without does the opposite. The pair has to be exact about which is which,
// because a browser that ran both would run the page's code twice.
//
// The original element is turned into the module and a copy is inserted after it
// as the fallback, rather than the other way round, so that the module comes
// first in source order - which is what a reader expects and what keeps the
// nomodule copy from being the one an old browser starts fetching first.
//
// Copying an element means re-emitting its attributes into markup this program
// builds, and that is the part worth reading. Everything the library reports is
// raw attribute source, so:
//
//	the attribute value as reported   /a.js?x=1&amp;y=2
//	EscapeAttribute of it             /a.js?x=1&amp;amp;y=2   wrong: a different url
//	decoded, then EscapeAttribute     /a.js?x=1&amp;y=2       right
//
// The middle line is the obvious call and it silently changes the URL. So the
// copy decodes each value and escapes the result, which is what the
// documentation on EscapeAttribute prescribes for a value read from the
// document.
//
// The same rule applies going the other way, and this program had it wrong first.
// SetAttribute takes attribute source, so a decoded value has to be escaped
// before it is written back: a src of "?a=1&amp;lt=2" decodes to "?a=1&lt=2", and
// writing that raw reads back as "?a=1<=2", because "&lt" is a character
// reference with or without its semicolon.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type splitter struct {
	suffix   string // extension the module build uses, replacing the original's
	addDefer bool   // put defer on the fallback, which nomodule does not imply

	split   int
	skipped map[string]int
}

func (s *splitter) note(reason string) {
	if s.skipped == nil {
		s.skipped = map[string]int{}
	}
	s.skipped[reason]++
}

func defaults() *splitter { return &splitter{suffix: ".mjs", addDefer: true} }

func (s *splitter) validate() error {
	if s.suffix == "" {
		return fmt.Errorf("-module-suffix cannot be empty: the pair needs two " +
			"different urls or both scripts fetch the same file")
	}
	if !strings.HasPrefix(s.suffix, ".") {
		return fmt.Errorf("-module-suffix %q should start with a dot", s.suffix)
	}
	return nil
}

func (s *splitter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("script[src]", func(e *lolhtml.Element) error {
			// Already one half of a pair.
			if _, ok := e.Attribute("nomodule"); ok {
				return nil
			}
			typ := strings.ToLower(strings.TrimSpace(stdhtml.UnescapeString(attr(e, "type"))))
			if typ == "module" {
				s.note("a module was left alone; it is already the modern half")
				return nil
			}
			switch typ {
			case "", "text/javascript", "application/javascript":
			default:
				s.note("the type " + typ + " is not executable javascript")
				return nil
			}

			src := stdhtml.UnescapeString(strings.TrimSpace(attr(e, "src")))
			if src == "" {
				s.note("a script with an empty src was left alone")
				return nil
			}
			moduleSrc, ok := s.moduleURL(src)
			if !ok {
				s.note("no module url could be derived from " + src)
				return nil
			}

			// The fallback is a copy of the original, built before the original
			// is changed - once type and src are rewritten the values are gone.
			fallback := s.fallbackMarkup(e)

			s.split++
			if err := e.SetAttribute("type", "module"); err != nil {
				return err
			}
			// Escaped on the way back in. SetAttribute takes attribute source,
			// and moduleSrc is a decoded value: writing it raw would let an
			// ampersand sequence in it form a reference. A src of
			// "?a=1&amp;lt=2" decodes to "?a=1&lt=2", and writing that back
			// unescaped reads as "?a=1<=2" - a different url, silently.
			if err := e.SetAttribute("src", lolhtml.EscapeAttribute(moduleSrc)); err != nil {
				return err
			}
			return e.After(fallback, lolhtml.HTML)
		}),
	}
}

// moduleURL swaps the extension for the module build's. A src with no extension
// is refused rather than guessed at: appending a suffix to a path that ends in a
// slash or a query would produce a URL nobody serves.
func (s *splitter) moduleURL(src string) (string, bool) {
	cut := len(src)
	if i := strings.IndexAny(src, "?#"); i >= 0 {
		cut = i
	}
	base, rest := src[:cut], src[cut:]
	ext := path.Ext(base)
	if ext == "" || strings.HasSuffix(base, "/") {
		return "", false
	}
	if ext == s.suffix {
		return "", false
	}
	return strings.TrimSuffix(base, ext) + s.suffix + rest, true
}

// fallbackMarkup copies the element as a nomodule script.
//
// Each value is decoded and then escaped: what the library reports is raw
// attribute source, so escaping it directly would turn "&amp;" into "&amp;amp;"
// and change the URL. See the note at the top of the file.
func (s *splitter) fallbackMarkup(e *lolhtml.Element) string {
	var sb strings.Builder
	sb.WriteString("<script")
	for _, a := range e.AttributeList() {
		if a.Name == "nomodule" || a.Name == "defer" {
			continue
		}
		sb.WriteString(" " + a.NamePreserveCase + `="` +
			lolhtml.EscapeAttribute(stdhtml.UnescapeString(a.Value)) + `"`)
	}
	sb.WriteString(" nomodule")
	if s.addDefer {
		// nomodule does not imply defer, and a fallback that blocks parsing
		// defeats the point of splitting.
		sb.WriteString(` defer=""`)
	}
	sb.WriteString("></script>")
	return sb.String()
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (s *splitter) run(r io.Reader, w io.Writer) error {
	if err := s.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, s.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func splitString(in string, opts ...func(*splitter)) (string, *splitter, error) {
	s := defaults()
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run(strings.NewReader(in), &out)
	return out.String(), s, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (s *splitter) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "split=%d\n", s.split)
	reasons := make([]string, 0, len(s.skipped))
	for r := range s.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, s.skipped[r])
	}
	return sb.String()
}

func main() {
	s := defaults()
	flag.StringVar(&s.suffix, "module-suffix", s.suffix,
		"extension the module build uses, replacing the original's")
	flag.BoolVar(&s.addDefer, "defer", s.addDefer,
		"put defer on the fallback, which nomodule does not imply")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "modulesplit:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: modulesplit [-module-suffix .mjs] [file.html]")
		os.Exit(2)
	}

	if err := s.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "modulesplit:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.report())
}

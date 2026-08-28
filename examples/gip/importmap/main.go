// Command importmap injects an import map before the first module script.
//
// An import map is only read if it appears before the first module script that
// resolves through it, and only the first one in the document counts. That makes
// this a position problem rather than a content problem: the map itself is a few
// bytes of JSON, and all the difficulty is in deciding where "before the first
// module script" is in a stream that has no tree.
//
// Three things decide it.
//
// A module script is a <script> whose type attribute, with leading and trailing
// ASCII whitespace stripped, is an ASCII case-insensitive match for "module".
// The stripping is why the obvious selector is wrong: script[type=module] folds
// case, because type is on the HTML specification's list of attributes whose
// values selectors match case-insensitively, but it does not strip, so it misses
// <script type=" module ">, which browsers do run. So this program selects every
// script and decides in Go.
//
// A <link rel=modulepreload> resolves through the map too, so it is an anchor as
// well. rel is a space-separated token list, so the check is per token.
//
// Template content is inert. A module script inside a <template> is never run,
// so it is not an anchor, and an import map injected inside one would never be
// read. The library has no tree to ask, so the program keeps a depth counter and
// decrements it from an end-tag handler.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Map is the import map to inject. The field names are the ones the HTML
// specification uses, so a caller can marshal a real import map file into it.
type Map struct {
	Imports map[string]string            `json:"imports,omitempty"`
	Scopes  map[string]map[string]string `json:"scopes,omitempty"`
}

// A Report says what the program did and why. Every path sets exactly one of
// Injected or Skipped, so a caller can log one line without a nil check.
type Report struct {
	Injected bool
	// Anchor describes what the map was injected before, for a log line:
	// `script type="module"` or `link rel="modulepreload"`.
	Anchor string
	// Skipped is the reason nothing was injected, empty if something was.
	Skipped string
	// RemovedStale counts import maps found after the injection point. Those
	// were already inert - an import map after the first module script is
	// ignored by browsers - so removing them changes no behaviour.
	RemovedStale int
}

// Inject writes r to w with m inserted before the first module script or
// modulepreload link, and reports what it did.
func Inject(w io.Writer, r io.Reader, m Map) (Report, error) {
	payload, err := payload(m)
	if err != nil {
		return Report{}, err
	}

	var rep Report
	// templateDepth counts <template> elements the rewriter is inside. Anything
	// at depth > 0 is inert: not an anchor, and not a place to inject.
	templateDepth := 0
	// existing is set by an import map seen before any anchor. That one is the
	// document's own map and it is in a valid position, so this program leaves
	// the document alone rather than adding a second one that would be ignored.
	existing := false

	scripts := lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		typ := scriptType(e)
		switch {
		case typ == "importmap":
			if rep.Injected {
				// Already inert: it comes after the first anchor, which is
				// where this program injected. Drop it so the document has
				// one import map rather than one and a decoy.
				rep.RemovedStale++
				e.Remove()
				return nil
			}
			if templateDepth == 0 {
				existing = true
			}
			return nil
		case typ == "module" && templateDepth == 0:
			return anchor(e, &rep, &existing, payload, `script type="module"`)
		}
		return nil
	})

	links := lolhtml.OnElement("link", func(e *lolhtml.Element) error {
		if templateDepth > 0 || !hasToken(e, "rel", "modulepreload") {
			return nil
		}
		return anchor(e, &rep, &existing, payload, `link rel="modulepreload"`)
	})

	templates := lolhtml.OnElement("template", func(e *lolhtml.Element) error {
		// A self-closing template in foreign content has no end tag, so there
		// is nothing to be inside and nothing to decrement.
		if !e.CanHaveContent() {
			return nil
		}
		templateDepth++
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			templateDepth--
			return nil
		})
	})

	rw, err := lolhtml.NewWriter(w, scripts, links, templates)
	if err != nil {
		return Report{}, err
	}
	if _, err := io.Copy(rw, r); err != nil {
		rw.Close()
		return Report{}, err
	}
	if err := rw.Close(); err != nil {
		return Report{}, err
	}
	if !rep.Injected && rep.Skipped == "" {
		if existing {
			rep.Skipped = "document already has an import map"
		} else {
			rep.Skipped = "no module script or modulepreload link to anchor to"
		}
	}
	return rep, nil
}

// anchor injects before e, once.
func anchor(e *lolhtml.Element, rep *Report, existing *bool, payload, what string) error {
	if rep.Injected {
		return nil
	}
	if *existing {
		return nil
	}
	if err := e.Before(payload, lolhtml.HTML); err != nil {
		return err
	}
	rep.Injected = true
	rep.Anchor = what
	return nil
}

// payload builds the <script type="importmap"> element to inject.
//
// The JSON goes inside a script, which is raw text: the parser looks for the
// literal bytes "</script" and nothing else terminates it, so a specifier or a
// URL containing them would end the element early and spill the rest of the map
// into the page as markup. JSON's own escaping is the fix, because "\u003c" is
// the same string to a JSON reader and is not a "<" to an HTML parser.
//
// Nothing in the library checks that afterwards, and the reason is worth knowing.
// A breakout is refused only for an insertion into an element's own content -
// Prepend, Append, SetInnerContent and EndTag.Before. This element is written
// with [lolhtml.Element.Before], which puts it outside the anchor element, where
// a closing tag is ordinary markup. It has to be: the payload itself ends in
// "</script>", so a check that applied here would refuse every injection this
// program makes. What is checked is the position, not the type. So the escaping
// below is the only thing standing between a specifier from a build file and a
// page that renders the map as text - and a caller who wants the check as well
// can run [lolhtml.CheckRawText] over the JSON body, which is the part that lands
// inside the script.
func payload(m Map) (string, error) {
	if len(m.Imports) == 0 && len(m.Scopes) == 0 {
		return "", errors.New("importmap: nothing to inject")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	// encoding/json escapes <, > and & by default, but that default is a
	// setting; do it here so the guarantee does not depend on it.
	body := strings.NewReplacer(
		"<", `\u003c`,
		">", `\u003e`,
		"&", `\u0026`,
	).Replace(string(b))
	return `<script type="importmap">` + body + `</script>`, nil
}

// scriptType returns the script's type attribute, stripped and lower-cased, the
// way the HTML specification compares it. A missing type is a classic script,
// which reports as the empty string.
func scriptType(e *lolhtml.Element) string {
	v, ok := e.Attribute("type")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.Trim(v, " \t\n\f\r"))
}

// hasToken reports whether attr contains token, comparing ASCII
// case-insensitively over whitespace-separated tokens the way rel is defined.
func hasToken(e *lolhtml.Element, attr, token string) bool {
	v, ok := e.Attribute(attr)
	if !ok {
		return false
	}
	for _, f := range strings.Fields(v) {
		if strings.EqualFold(f, token) {
			return true
		}
	}
	return false
}

func main() {
	m := Map{Imports: map[string]string{}}
	for _, arg := range os.Args[1:] {
		name, url, ok := strings.Cut(arg, "=")
		if !ok || name == "" || url == "" {
			fmt.Fprintf(os.Stderr, "usage: importmap specifier=url ...\n")
			os.Exit(2)
		}
		m.Imports[name] = url
	}
	if len(m.Imports) == 0 {
		fmt.Fprintf(os.Stderr, "usage: importmap specifier=url ...\n")
		os.Exit(2)
	}
	rep, err := Inject(os.Stdout, os.Stdin, m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "importmap:", err)
		os.Exit(1)
	}
	if rep.Injected {
		names := make([]string, 0, len(m.Imports))
		for n := range m.Imports {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "importmap: %d specifiers before %s\n", len(names), rep.Anchor)
	} else {
		fmt.Fprintf(os.Stderr, "importmap: skipped: %s\n", rep.Skipped)
	}
	if rep.RemovedStale > 0 {
		fmt.Fprintf(os.Stderr, "importmap: removed %d stale import map(s)\n", rep.RemovedStale)
	}
}

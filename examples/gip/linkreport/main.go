// Command linkreport collects every anchor in a document with its target and
// text, and reports the ones a reader or a screen reader would struggle with.
//
//	linkreport < page.html
//	linkreport -json < page.html | jq .
//
// It writes no document: the report is the output. Rewritten bytes go to
// io.Discard, which is the honest way to run a read-only pass rather than
// pretending to be a filter.
//
// Link text is accumulated across chunks and finished at the end tag, and it is
// compared decoded: the text arrives as raw source, so "Caf&eacute;" has to
// become "Café" before it can be measured against anything a person typed.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	asJSON := flag.Bool("json", false, "emit the report as JSON")
	base := flag.String("base", "", "base URL, so internal and external can be told apart")
	flag.Parse()

	r := &reporter{base: *base}
	if err := r.run(os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "linkreport:", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r.summary()); err != nil {
			fmt.Fprintln(os.Stderr, "linkreport:", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(r.render())
	}

	if len(r.findings()) > 0 {
		os.Exit(1)
	}
}

// Link is one anchor, as the report sees it.
type Link struct {
	Href   string `json:"href"`
	Text   string `json:"text"`
	Rel    string `json:"rel,omitempty"`
	Target string `json:"target,omitempty"`
	Kind   string `json:"kind"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

type reporter struct {
	base string

	links []Link
	open  bool
	text  strings.Builder
}

func (r *reporter) run(src io.Reader) error {
	// io.Discard: this pass reads, it does not filter.
	w, err := lolhtml.NewWriter(io.Discard, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (r *reporter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			rel, _ := e.Attribute("rel")
			target, _ := e.Attribute("target")
			loc := e.SourceLocation()

			l := Link{
				// Decoded, because these are compared and reported to people.
				Href:   stdhtml.UnescapeString(strings.TrimSpace(href)),
				Rel:    rel,
				Target: target,
				Start:  loc.Start,
				End:    loc.End,
			}
			l.Kind = classify(l.Href, r.base)
			r.links = append(r.links, l)

			r.open = true
			r.text.Reset()
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				r.open = false
				last := &r.links[len(r.links)-1]
				last.Text = strings.Join(strings.Fields(
					stdhtml.UnescapeString(r.text.String())), " ")
				last.End = loc.End
				return nil
			})
		}),

		lolhtml.OnText("a", func(t *lolhtml.TextChunk) error {
			if r.open {
				r.text.WriteString(t.Text())
			}
			return nil
		}),

		// An image is often the whole of a link's content, in which case its alt
		// text is the link text as far as a screen reader is concerned.
		lolhtml.OnElement("a img[alt]", func(e *lolhtml.Element) error {
			if !r.open {
				return nil
			}
			if alt, ok := e.Attribute("alt"); ok {
				r.text.WriteString(" " + alt + " ")
			}
			return nil
		}),
	}
}

func classify(href, base string) string {
	switch {
	case href == "":
		return "empty"
	case strings.HasPrefix(href, "#"):
		return "fragment"
	}

	u, err := url.Parse(href)
	if err != nil {
		return "unparseable"
	}
	switch strings.ToLower(u.Scheme) {
	case "mailto":
		return "mailto"
	case "tel":
		return "tel"
	case "javascript":
		return "javascript"
	case "data":
		return "data"
	}
	if !u.IsAbs() && u.Host == "" {
		return "internal"
	}
	if base != "" {
		if b, err := url.Parse(base); err == nil && strings.EqualFold(b.Host, u.Host) {
			return "internal"
		}
	}
	return "external"
}

// generic is link text that tells a reader nothing about where the link goes.
// A screen reader user listing a page's links hears only this.
var generic = map[string]bool{
	"click here": true, "here": true, "read more": true, "more": true,
	"link": true, "this": true, "this link": true, "learn more": true,
	"details": true, "info": true, "continue": true, "go": true,
	"download": true, "view": true, "see more": true, "full story": true,
}

// Finding is one problem with one or more links.
type Finding struct {
	Kind  string   `json:"kind"`
	Note  string   `json:"note"`
	Links []Link   `json:"links,omitempty"`
	Texts []string `json:"texts,omitempty"`
}

func (r *reporter) findings() []Finding {
	var out []Finding

	add := func(kind, note string, links []Link) {
		if len(links) > 0 {
			out = append(out, Finding{Kind: kind, Note: note, Links: links})
		}
	}

	var empty, genericText, urlAsText, unparseable, jsLinks []Link
	for _, l := range r.links {
		lower := strings.ToLower(l.Text)
		switch {
		case l.Text == "":
			empty = append(empty, l)
		case generic[lower]:
			genericText = append(genericText, l)
		case looksLikeURL(l.Text):
			urlAsText = append(urlAsText, l)
		}
		if l.Kind == "unparseable" {
			unparseable = append(unparseable, l)
		}
		if l.Kind == "javascript" {
			jsLinks = append(jsLinks, l)
		}
	}

	add("empty-text", "a link with no text is unreachable by name", empty)
	add("generic-text", "the text does not say where the link goes", genericText)
	add("url-as-text", "a URL read aloud is not a description", urlAsText)
	add("unparseable-target", "the target is not a URL", unparseable)
	add("javascript-target", "a javascript: target does not work without scripting", jsLinks)

	// The same text pointing at different places, which is the one a reader
	// cannot resolve at all: two links called "Documentation" going elsewhere.
	byText := map[string]map[string][]Link{}
	for _, l := range r.links {
		if l.Text == "" {
			continue
		}
		key := strings.ToLower(l.Text)
		if byText[key] == nil {
			byText[key] = map[string][]Link{}
		}
		byText[key][l.Href] = append(byText[key][l.Href], l)
	}
	var ambiguous []Finding
	for text, targets := range byText {
		if len(targets) < 2 {
			continue
		}
		f := Finding{
			Kind: "same-text-different-targets",
			Note: fmt.Sprintf("%q points at %d different targets", text, len(targets)),
		}
		for _, ls := range targets {
			f.Links = append(f.Links, ls...)
		}
		sort.Slice(f.Links, func(i, j int) bool { return f.Links[i].Start < f.Links[j].Start })
		ambiguous = append(ambiguous, f)
	}
	sort.Slice(ambiguous, func(i, j int) bool { return ambiguous[i].Note < ambiguous[j].Note })
	out = append(out, ambiguous...)

	return out
}

// looksLikeURL catches text that is the target repeated rather than described.
func looksLikeURL(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") ||
		strings.HasPrefix(l, "www.")
}

// Summary is the JSON shape.
type Summary struct {
	Total    int            `json:"total"`
	ByKind   map[string]int `json:"by_kind"`
	Links    []Link         `json:"links"`
	Findings []Finding      `json:"findings"`
}

func (r *reporter) summary() Summary {
	byKind := map[string]int{}
	for _, l := range r.links {
		byKind[l.Kind]++
	}
	return Summary{
		Total:    len(r.links),
		ByKind:   byKind,
		Links:    r.links,
		Findings: r.findings(),
	}
}

func (r *reporter) render() string {
	s := r.summary()

	kinds := make([]string, 0, len(s.ByKind))
	for k, n := range s.ByKind {
		kinds = append(kinds, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(kinds)

	var sb strings.Builder
	fmt.Fprintf(&sb, "links=%d [%s]\n", s.Total, strings.Join(kinds, " "))
	for _, f := range s.Findings {
		fmt.Fprintf(&sb, "\n%s: %s\n", f.Kind, f.Note)
		for _, l := range f.Links {
			fmt.Fprintf(&sb, "  %d-%d %q -> %q\n", l.Start, l.End, l.Text, l.Href)
		}
	}
	return sb.String()
}

func reportString(in string, base string) (*reporter, error) {
	r := &reporter{base: base}
	err := r.run(bytes.NewReader([]byte(in)))
	return r, err
}

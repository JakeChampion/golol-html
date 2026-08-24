// Command socialmeta reports the Open Graph and Twitter card metadata a page
// carries, and what is missing for a link to it to render well.
//
//	socialmeta page.html
//	og:title      = Widget & Co
//	og:type       = website
//	og:image      = https://example.com/w.png
//	missing: og:url, og:description
//	twitter:card is absent, so a Twitter card falls back to Open Graph
//
// It changes nothing: the document goes to standard output byte for byte and the
// report to standard error.
//
// Two details of the reading are worth naming, because both are places an
// extractor can quietly be wrong.
//
// An Open Graph property is named by the property attribute and a Twitter one by
// name, and pages routinely use the wrong one of the two. Both are accepted for
// both vocabularies, because the alternative is reporting a tag as missing when
// it is on the page in the form everything else in the world accepts.
//
// A repeated meta is not an error either. Open Graph is explicit that a property
// can appear several times - og:image is the common one - and the order matters,
// so they are collected in order rather than overwritten. That is the opposite
// choice from a repeated *attribute* on one element, where the first copy is the
// only one anything acts on; see "An attribute can appear twice" in the package
// documentation.
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

	lolhtml "github.com/JakeChampion/golol-html"
)

// A tag is one metadata property, in document order.
type tag struct {
	key   string
	value string
}

type reporter struct {
	// want is the set of properties to insist on, in report order.
	want []string
	// full also reports the properties that are present, not only what is not.
	full bool

	tags    []tag
	skipped map[string]int
}

func (r *reporter) note(reason string) {
	if r.skipped == nil {
		r.skipped = map[string]int{}
	}
	r.skipped[reason]++
}

// The properties a link preview needs. og:type and og:url are not strictly
// required by anything, but a page without them renders as a bare title.
func defaults() *reporter {
	return &reporter{
		want: []string{"og:title", "og:type", "og:url", "og:image", "og:description"},
		full: true,
	}
}

func (r *reporter) validate() error {
	for _, w := range r.want {
		if !strings.Contains(w, ":") {
			return fmt.Errorf("-want %q is not a property name: they look like "+
				"og:title or twitter:card", w)
		}
	}
	return nil
}

// interesting reports whether a key belongs to one of the vocabularies this
// program is about. Anything else on the page is none of its business.
func interesting(key string) bool {
	for _, prefix := range []string{"og:", "twitter:", "article:", "book:", "profile:", "fb:"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (r *reporter) options() []lolhtml.Option {
	return []lolhtml.Option{
		// Both attributes, because pages use both for both vocabularies. Read
		// through Attribute rather than the iterator: a meta can carry property
		// twice, and the first copy is the one a parser keeps.
		lolhtml.OnElement("meta[property], meta[name]", func(e *lolhtml.Element) error {
			key := strings.ToLower(decoded(attr(e, "property")))
			if key == "" {
				key = strings.ToLower(decoded(attr(e, "name")))
			}
			if key == "" || !interesting(key) {
				return nil
			}
			value, ok := e.Attribute("content")
			if !ok {
				r.note("a meta with no content attribute")
				return nil
			}
			value = decoded(value)
			if value == "" {
				r.note("a meta with an empty content attribute")
				return nil
			}
			r.tags = append(r.tags, tag{key: key, value: value})
			return nil
		}),
	}
}

// values returns every value for a key, in document order.
func (r *reporter) values(key string) []string {
	var out []string
	for _, t := range r.tags {
		if t.key == key {
			out = append(out, t.value)
		}
	}
	return out
}

// missing lists the wanted properties with no value, in the order asked for.
func (r *reporter) missing() []string {
	var out []string
	for _, key := range r.want {
		if len(r.values(key)) == 0 {
			out = append(out, key)
		}
	}
	return out
}

func decoded(s string) string { return stdhtml.UnescapeString(strings.TrimSpace(s)) }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (r *reporter) run(in io.Reader, w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func reportString(in string, opts ...func(*reporter)) (string, *reporter, error) {
	r := defaults()
	for _, o := range opts {
		o(r)
	}
	var out bytes.Buffer
	err := r.run(strings.NewReader(in), &out)
	return out.String(), r, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (r *reporter) report() string {
	var sb strings.Builder

	if r.full {
		// Keys in first-appearance order, values in document order under each.
		seen := map[string]bool{}
		width := 0
		for _, t := range r.tags {
			if len(t.key) > width {
				width = len(t.key)
			}
		}
		for _, t := range r.tags {
			if seen[t.key] {
				continue
			}
			seen[t.key] = true
			for _, v := range r.values(t.key) {
				fmt.Fprintf(&sb, "%-*s = %s\n", width, t.key, v)
			}
		}
	}

	if m := r.missing(); len(m) > 0 {
		fmt.Fprintf(&sb, "missing: %s\n", strings.Join(m, ", "))
	}

	// The one piece of advice worth giving, because it is the commonest cause of
	// a bad preview and it is not a missing tag.
	if len(r.values("twitter:card")) == 0 && len(r.tags) > 0 {
		sb.WriteString("twitter:card is absent, so a Twitter card falls back to Open Graph\n")
	}

	reasons := make([]string, 0, len(r.skipped))
	for reason := range r.skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", reason, r.skipped[reason])
	}

	fmt.Fprintf(&sb, "tags=%d missing=%d\n", len(r.tags), len(r.missing()))
	return sb.String()
}

type keyList struct{ v *[]string }

func (k keyList) String() string {
	if k.v == nil {
		return ""
	}
	return strings.Join(*k.v, ",")
}

func (k keyList) Set(v string) error {
	*k.v = nil
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(strings.ToLower(f)); f != "" {
			*k.v = append(*k.v, f)
		}
	}
	return nil
}

func main() {
	r := defaults()
	flag.Var(keyList{&r.want}, "want",
		"comma-separated properties to insist on, replacing the default set")
	flag.BoolVar(&r.full, "full", r.full, "list the properties that are present too")
	flag.Parse()

	var in io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "socialmeta:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: socialmeta [-want list] [file.html]")
		os.Exit(2)
	}

	if err := r.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "socialmeta:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, r.report())
}

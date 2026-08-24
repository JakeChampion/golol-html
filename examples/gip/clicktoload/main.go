// Command clicktoload turns every third-party iframe into a placeholder that
// loads on click, so a page does not hand an embed to the reader before they ask
// for it.
//
//	clicktoload -same-origin example.com < page.html > out.html
//
// The iframe is changed rather than replaced. That is deliberate: replacing it
// means writing the placeholder's markup by hand, and a hand-built attribute is
// the one path in this library that escapes nothing - a title of
// `" onload=alert(1) x="` walks straight out of it. SetTagName, SetAttribute and
// RemoveAttribute do the same job with every value escaped on the way out, and in
// less code.
//
// Event handler attributes are removed rather than carried over. A tag rename
// keeps every attribute, so an onload written for an iframe would otherwise fire
// on the placeholder.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	sameOrigin := flag.String("same-origin", "", "comma-separated hosts to leave alone")
	label := flag.String("label", "Click to load", "placeholder label")
	all := flag.Bool("all", false, "convert same-origin iframes too")
	flag.Parse()

	c := &converter{label: *label, all: *all, keep: map[string]bool{}}
	for _, h := range strings.Split(*sameOrigin, ",") {
		if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
			c.keep[h] = true
		}
	}

	if err := c.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "clicktoload:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, c.report())
}

// carriedOver are the attributes worth keeping on the placeholder, because a
// script restoring the iframe needs them and CSS may size it.
var carriedOver = map[string]bool{
	"width": true, "height": true, "style": true, "class": true, "id": true,
	"title": true, "allow": true, "allowfullscreen": true, "loading": true,
	"referrerpolicy": true, "sandbox": true, "name": true,
}

type converter struct {
	label string
	all   bool
	keep  map[string]bool

	converted int
	byHost    map[string]int
	left      map[string]int
	handlers  int
}

func (c *converter) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, c.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (c *converter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("iframe", func(e *lolhtml.Element) error {
			src, hasSrc := e.Attribute("src")
			host := hostOf(src)

			switch {
			case !hasSrc || strings.TrimSpace(src) == "":
				// An iframe with no src loads nothing, so there is nothing to
				// defer.
				c.note(&c.left, "no src")
				return nil
			case !c.all && (host == "" || c.keep[host]):
				c.note(&c.left, "same origin or no host")
				return nil
			}

			// Collect what is being dropped before anything changes, since the
			// decision depends on the names and RemoveAttribute changes them.
			var drop []string
			for name := range e.Attributes() {
				if isEventHandler(name) {
					drop = append(drop, name)
					continue
				}
				if name == "src" || name == "srcdoc" || carriedOver[name] {
					continue
				}
				drop = append(drop, name)
			}
			for _, name := range drop {
				if isEventHandler(name) {
					c.handlers++
				}
				if err := e.RemoveAttribute(name); err != nil {
					return err
				}
			}

			// Every value here goes through SetAttribute, which escapes it. No
			// markup is assembled by hand, so there is nothing to get wrong.
			if err := e.SetAttribute("data-ctl-src", src); err != nil {
				return err
			}
			if err := e.RemoveAttribute("src"); err != nil {
				return err
			}
			if err := e.RemoveAttribute("srcdoc"); err != nil {
				return err
			}
			if err := e.SetAttribute("class",
				addClass(attr(e, "class"), "click-to-load")); err != nil {
				return err
			}
			if err := e.SetAttribute("role", "button"); err != nil {
				return err
			}
			if err := e.SetAttribute("tabindex", "0"); err != nil {
				return err
			}

			c.converted++
			c.note(&c.byHost, host)

			// An iframe is not void, so its end tag is renamed with it. The
			// label goes inside as text, which escapes it.
			if err := e.SetInnerContent(c.label, lolhtml.Text); err != nil {
				return err
			}
			return e.SetTagName("div")
		}),
	}
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// isEventHandler matches by shape: a browser dispatches any "on*" attribute, and
// a tag rename would otherwise carry one onto the placeholder.
func isEventHandler(name string) bool {
	if !strings.HasPrefix(name, "on") || len(name) < 3 {
		return false
	}
	for _, r := range name[2:] {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// hostOf returns the lower-cased host of a URL, or "" for a relative one - which
// is same-origin by definition.
func hostOf(src string) string {
	u, err := url.Parse(strings.TrimSpace(stdhtml.UnescapeString(src)))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func addClass(existing, add string) string {
	for _, c := range strings.Fields(existing) {
		if c == add {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return add
	}
	return strings.Join(append(strings.Fields(existing), add), " ")
}

func (c *converter) note(m *map[string]int, key string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}

func (c *converter) report() string {
	hosts := make([]string, 0, len(c.byHost))
	for h, n := range c.byHost {
		hosts = append(hosts, fmt.Sprintf("%s=%d", h, n))
	}
	sort.Strings(hosts)

	reasons := make([]string, 0, len(c.left))
	for r, n := range c.left {
		reasons = append(reasons, fmt.Sprintf("%s=%d", r, n))
	}
	sort.Strings(reasons)

	var sb strings.Builder
	fmt.Fprintf(&sb, "converted=%d handlers-removed=%d", c.converted, c.handlers)
	if len(hosts) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(hosts, " "))
	}
	sb.WriteString("\n")
	if len(reasons) > 0 {
		fmt.Fprintf(&sb, "left as iframes: %s\n", strings.Join(reasons, " "))
	}
	return sb.String()
}

func convertString(in string, opts ...func(*converter)) (string, *converter, error) {
	c := &converter{label: "Click to load", keep: map[string]bool{}}
	for _, o := range opts {
		o(c)
	}
	var out bytes.Buffer
	err := c.run(strings.NewReader(in), &out)
	return out.String(), c, err
}

// Command consentgate stops third-party scripts from running until consent is
// given, by rewriting their type attribute rather than removing them.
//
// A <script> whose type a browser does not recognise is not executed and not
// fetched, but it is still in the document, with its src and its attributes
// intact. So consent can be granted later by a few lines of first-party script
// that put the type back - no re-render, no second request for the page, and
// nothing lost if consent never comes.
//
//	<script src="https://third.party/a.js"></script>
//	<script type="text/plain" data-consent-src="https://third.party/a.js"
//	        data-consent-type="">...</script>
//
// Which scripts are gated is a decision this program will not make for you.
// There is no built-in list of trackers: a host allowed by one site is a tracker
// on the next, so the hosts to gate are given on the command line, and with none
// given nothing is gated.
//
// An inline script has no host to match on, so it is named by selector instead:
//
//	consentgate -host third.party -inline-selector 'script#ga-init' page.html
//
// A selector rather than a search of the script's text, and the reason is the
// streaming model rather than taste. A selector is decided on the start tag,
// which is where the type attribute is; the text arrives afterwards, by which
// point the start tag has already been written out. Gating an inline script by
// what it contains would need a second pass over the document.
//
// What it will not do is gate a script it cannot restore. An inline script with
// a type that a browser treats as data already - importmap, application/json,
// speculationrules - is left alone: rewriting its type changes what the page
// means rather than when it runs.
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

	lolhtml "github.com/JakeChampion/golol-html"
)

// gate is the configuration and the tally.
type gate struct {
	hosts     []string // hosts to gate, matched as a suffix on the host name
	selectors []string // selectors naming inline scripts to gate
	gatedType string   // the type to park scripts under
	prefix    string   // attribute prefix, so the restoring script knows what to look for

	gated   int
	skipped map[string]int
}

func (g *gate) note(reason string) {
	if g.skipped == nil {
		g.skipped = map[string]int{}
	}
	g.skipped[reason]++
}

// typesThatAreData are types a browser does not execute but does read. Parking a
// script under a different type would change what the document means, so these
// are never gated. text/template is not here: it is inert either way.
var typesThatAreData = map[string]bool{
	"importmap":                    true,
	"application/json":             true,
	"application/ld+json":          true,
	"speculationrules":             true,
	"application/manifest+json":    true,
	"application/importmap+json":   true,
	"application/speculationrules": true,
}

func (g *gate) options() []lolhtml.Option {
	opts := []lolhtml.Option{
		lolhtml.OnElement("script[src]", func(e *lolhtml.Element) error {
			if g.alreadyGated(e) {
				g.note("already gated")
				return nil
			}
			if typ, data := g.typeIsData(e); data {
				g.note("the type " + typ + " is data rather than code")
				return nil
			}
			src, _ := e.Attribute("src")
			if !g.gatedHost(stdhtml.UnescapeString(strings.TrimSpace(src))) {
				return nil
			}
			return g.park(e, src)
		}),
	}

	// One handler per selector. Registered after the src handler, so a script
	// that both a selector and a host match is parked once: handlers see each
	// other's edits, so the second finds the element already gated.
	for _, sel := range g.selectors {
		opts = append(opts, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
			if !strings.EqualFold(e.TagName(), "script") {
				g.note("a selector matched something that is not a script")
				return nil
			}
			if g.alreadyGated(e) {
				return nil
			}
			if typ, data := g.typeIsData(e); data {
				g.note("the type " + typ + " is data rather than code")
				return nil
			}
			src, _ := e.Attribute("src")
			return g.park(e, src)
		}))
	}
	return opts
}

// alreadyGated recognises this program's own output, so a second pass adds
// nothing and two handlers cannot park the same script twice.
func (g *gate) alreadyGated(e *lolhtml.Element) bool {
	if _, ok := e.Attribute(g.prefix + "-type"); ok {
		return true
	}
	_, ok := e.Attribute(g.prefix + "-src")
	return ok
}

// typeIsData reports a type a browser reads rather than runs. Changing one of
// those changes what the document means, not when it runs, so it is left alone.
func (g *gate) typeIsData(e *lolhtml.Element) (string, bool) {
	typ := strings.ToLower(strings.TrimSpace(stdhtml.UnescapeString(attr(e, "type"))))
	return typ, typesThatAreData[typ]
}

// park is the rewrite. The src and the original type are moved to attributes
// with the configured prefix, and the type becomes one no browser executes.
//
// Every value is written with SetAttribute, so the values are escaped by the
// library and this program assembles no markup at all. Moving src rather than
// leaving it is the point: a browser fetches src even for a type it will not
// run, so leaving it would gate the execution and not the request.
func (g *gate) park(e *lolhtml.Element, src string) error {
	typ, _ := e.Attribute("type")
	if src != "" {
		if err := e.SetAttribute(g.prefix+"-src", src); err != nil {
			return err
		}
		if err := e.RemoveAttribute("src"); err != nil {
			return err
		}
	}
	// Recorded even when empty, because empty is meaningful: it is what the
	// restoring script must put back, and "absent" and "empty" restore
	// differently.
	if err := e.SetAttribute(g.prefix+"-type", typ); err != nil {
		return err
	}
	if err := e.SetAttribute("type", g.gatedType); err != nil {
		return err
	}
	g.gated++
	return nil
}

// gatedHost reports whether a script URL is one of the hosts to gate. A relative
// URL is first-party by definition and never gated.
func gatedHostOf(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	h := strings.ToLower(u.Hostname())
	return strings.TrimSuffix(h, "."), true
}

func (g *gate) gatedHost(rawURL string) bool {
	host, ok := gatedHostOf(rawURL)
	if !ok {
		return false
	}
	for _, want := range g.hosts {
		want = strings.ToLower(strings.TrimPrefix(want, "."))
		if host == want || strings.HasSuffix(host, "."+want) {
			return true
		}
	}
	return false
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (g *gate) run(r io.Reader, w io.Writer) error {
	if err := g.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, g.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// validate refuses a configuration that cannot work, rather than producing a
// document that looks gated and is not.
func (g *gate) validate() error {
	if g.gatedType == "" {
		return fmt.Errorf("-gated-type cannot be empty: an empty type is treated as " +
			"JavaScript, so the script would still run")
	}
	if strings.EqualFold(g.gatedType, "text/javascript") ||
		strings.EqualFold(g.gatedType, "module") ||
		strings.EqualFold(g.gatedType, "application/javascript") {
		return fmt.Errorf("-gated-type %q is executable, so nothing would be gated",
			g.gatedType)
	}
	if !validAttrPrefix(g.prefix) {
		return fmt.Errorf("-prefix %q is not usable as the start of an attribute name: "+
			"it has to be letters, digits and hyphens", g.prefix)
	}
	return nil
}

func validAttrPrefix(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

func gateString(in string, opts ...func(*gate)) (string, *gate, error) {
	g := defaults()
	for _, o := range opts {
		o(g)
	}
	var out bytes.Buffer
	err := g.run(strings.NewReader(in), &out)
	return out.String(), g, err
}

func defaults() *gate {
	return &gate{gatedType: "text/plain", prefix: "data-consent"}
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// report is written to stderr so the output stays a document.
func (g *gate) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "gated=%d", g.gated)
	reasons := make([]string, 0, len(g.skipped))
	for r := range g.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, " [%s]=%d", r, g.skipped[r])
	}
	return sb.String()
}

type stringList struct{ v *[]string }

func (s stringList) String() string {
	if s.v == nil {
		return ""
	}
	return strings.Join(*s.v, ",")
}

func (s stringList) Set(v string) error {
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			*s.v = append(*s.v, f)
		}
	}
	return nil
}

func main() {
	g := defaults()
	flag.Var(stringList{&g.hosts}, "host",
		"host to gate, repeatable or comma-separated; nothing is gated without one")
	flag.Var(stringList{&g.selectors}, "inline-selector",
		"selector naming inline scripts to gate, repeatable")
	flag.StringVar(&g.gatedType, "gated-type", g.gatedType,
		"the type gated scripts are parked under; must not be executable")
	flag.StringVar(&g.prefix, "prefix", g.prefix,
		"attribute prefix for the saved src and type")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "consentgate:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: consentgate [-host h] [-inline-with s] [file.html]")
		os.Exit(2)
	}

	if err := g.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "consentgate:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, g.report())
}

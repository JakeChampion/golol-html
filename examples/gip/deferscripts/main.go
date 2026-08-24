// Command deferscripts adds defer to the scripts that can take it.
//
//	deferscripts page.html
//	<script src="/a.js" defer></script>
//
// A classic script with a src blocks parsing until it has downloaded and run.
// defer moves it to after parsing while keeping the execution order the page
// wrote, which is the safe half of the async/defer pair: async reorders, defer
// does not.
//
// What it will not touch, and why each one would break something:
//
// An inline script - no src - runs where it stands, and defer is ignored on it.
// Adding the attribute would be noise that reads as though it did something.
//
// A module. type=module is deferred already, and marking it does nothing.
//
// A script with async. The page asked for unordered execution; turning that into
// ordered execution is a decision about the page's own behaviour, not a fix.
//
// A script inside the body, unless -body is given. Deferring one changes when it
// runs relative to the markup around it, and a script placed late in the body was
// often placed there deliberately - the pattern predates defer.
//
// One cosmetic thing it cannot help. There is no way to write a bare attribute
// through this API, so the output says defer="" where the page's own scripts say
// defer. Any parser treats those as the same attribute, since presence is what a
// boolean attribute means, but a diff will show two styles.
//
// A script with document.write in reach. This cannot be detected from a src
// attribute, which is the honest limit of the program: deferring a script that
// calls document.write after parsing has finished blanks the page. So the report
// says how many were changed, and the caller is the one who knows whether their
// scripts do that.
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

type deferrer struct {
	includeBody bool     // also defer scripts inside the body
	skipHosts   []string // hosts whose scripts are left alone

	deferred int
	skipped  map[string]int
}

func (d *deferrer) note(reason string) {
	if d.skipped == nil {
		d.skipped = map[string]int{}
	}
	d.skipped[reason]++
}

func defaults() *deferrer { return &deferrer{} }

func (d *deferrer) validate() error {
	for _, h := range d.skipHosts {
		if h == "" {
			return fmt.Errorf("-skip-host cannot be empty")
		}
	}
	return nil
}

func (d *deferrer) options() []lolhtml.Option {
	inBody := false

	return []lolhtml.Option{
		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			inBody = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				inBody = false
				return nil
			})
		}),

		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			src, hasSrc := e.Attribute("src")
			if !hasSrc || strings.TrimSpace(decoded(src)) == "" {
				d.note("an inline script cannot be deferred")
				return nil
			}
			if _, ok := e.Attribute("defer"); ok {
				return nil
			}
			if _, ok := e.Attribute("async"); ok {
				d.note("a script with async was left alone; the page asked for " +
					"unordered execution")
				return nil
			}

			typ := strings.ToLower(strings.TrimSpace(decoded(attr(e, "type"))))
			switch typ {
			case "module":
				d.note("a module is deferred already")
				return nil
			case "", "text/javascript", "application/javascript",
				"text/ecmascript", "application/ecmascript":
				// A classic script.
			default:
				// Anything else is data rather than code: importmap, ld+json,
				// a template. Deferring it is meaningless and marking it is
				// noise.
				d.note("the type " + typ + " is not executable")
				return nil
			}

			if inBody && !d.includeBody {
				d.note("a script in the body was left alone; use -body to include " +
					"them")
				return nil
			}
			if host := hostOf(decoded(src)); host != "" && d.hostIsSkipped(host) {
				d.note("a script from a skipped host was left alone")
				return nil
			}

			d.deferred++
			return e.SetAttribute("defer", "")
		}),
	}
}

// hostIsSkipped reports whether a host is on the skip list, matched as a suffix
// so a subdomain of a skipped host is skipped too.
func (d *deferrer) hostIsSkipped(host string) bool {
	host = strings.ToLower(host)
	for _, want := range d.skipHosts {
		want = strings.ToLower(strings.TrimPrefix(want, "."))
		if host == want || strings.HasSuffix(host, "."+want) {
			return true
		}
	}
	return false
}

// hostOf reads the host out of a URL without a full parse: a relative src has no
// host and is first-party by definition.
func hostOf(raw string) string {
	s := raw
	if i := strings.Index(s, "//"); i >= 0 {
		s = s[i+2:]
	} else {
		return ""
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

func decoded(s string) string { return stdhtml.UnescapeString(s) }

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func (d *deferrer) run(r io.Reader, w io.Writer) error {
	if err := d.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, d.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func deferString(in string, opts ...func(*deferrer)) (string, *deferrer, error) {
	d := defaults()
	for _, o := range opts {
		o(d)
	}
	var out bytes.Buffer
	err := d.run(strings.NewReader(in), &out)
	return out.String(), d, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (d *deferrer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "deferred=%d\n", d.deferred)
	reasons := make([]string, 0, len(d.skipped))
	for r := range d.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, d.skipped[r])
	}
	if d.deferred > 0 {
		sb.WriteString("check that none of these calls document.write: deferring " +
			"one that does blanks the page, and a src attribute cannot say\n")
	}
	return sb.String()
}

type hostList struct{ v *[]string }

func (h hostList) String() string {
	if h.v == nil {
		return ""
	}
	return strings.Join(*h.v, ",")
}

func (h hostList) Set(v string) error {
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			*h.v = append(*h.v, f)
		}
	}
	return nil
}

func main() {
	d := defaults()
	flag.BoolVar(&d.includeBody, "body", false,
		"also defer scripts inside the body, which changes when they run relative "+
			"to the markup around them")
	flag.Var(hostList{&d.skipHosts}, "skip-host",
		"host whose scripts are left alone, repeatable or comma-separated")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "deferscripts:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: deferscripts [-body] [file.html]")
		os.Exit(2)
	}

	if err := d.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "deferscripts:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, d.report())
}

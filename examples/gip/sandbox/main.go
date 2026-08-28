// Command sandbox hardens third-party iframes: it adds a sandbox attribute and a
// referrer policy, and reports the sandboxes that do not sandbox anything.
//
//	sandbox -same-origin example.com < page.html > out.html
//
// The one piece of real judgement here is that allow-scripts and
// allow-same-origin together defeat the sandbox. A frame granted both can reach
// into its own document and remove the sandbox attribute, so the combination is
// no safer than no sandbox at all. This program never writes it, and reports an
// existing one rather than silently trusting it.
//
// An author-written sandbox is otherwise left alone. Tightening it would break
// embeds that were working, and this program cannot know which tokens the embed
// needs.
//
// The other judgement is what "same origin" means, and it is narrower than "the
// URL names no host". A relative src loads from this document and needs nothing; a
// data:, blob: or javascript: src names no host either and is not first-party at
// all - it is content the page carries, which is what the sandbox attribute exists
// for. A src the URL parser refuses is hardened too, because a guard that fails
// open on the inputs it cannot read is not a guard. See sameOrigin.
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

func main() {
	sameOrigin := flag.String("same-origin", "", "comma-separated hosts to leave alone")
	tokens := flag.String("sandbox", "allow-scripts allow-popups allow-forms",
		"sandbox tokens to apply to iframes that have none")
	policy := flag.String("referrerpolicy", "no-referrer", "referrer policy to apply")
	all := flag.Bool("all", false, "harden same-origin iframes too")
	flag.Parse()

	h := &hardener{tokens: strings.Fields(*tokens), policy: *policy, all: *all, keep: map[string]bool{}}
	for _, s := range strings.Split(*sameOrigin, ",") {
		if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
			h.keep[s] = true
		}
	}

	if defeats(h.tokens) {
		fmt.Fprintln(os.Stderr, "sandbox: refusing to write allow-scripts and "+
			"allow-same-origin together; a frame with both can remove its own sandbox")
		os.Exit(2)
	}
	if h.policy != "" && !validPolicy(h.policy) {
		fmt.Fprintf(os.Stderr, "sandbox: %q is not a referrer policy\n", h.policy)
		os.Exit(2)
	}

	if err := h.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, h.report())
	if len(h.defeated) > 0 {
		os.Exit(1)
	}
}

// defeats reports the combination that makes a sandbox pointless.
func defeats(tokens []string) bool {
	scripts, sameOrigin := false, false
	for _, t := range tokens {
		switch strings.ToLower(t) {
		case "allow-scripts":
			scripts = true
		case "allow-same-origin":
			sameOrigin = true
		}
	}
	return scripts && sameOrigin
}

var policies = map[string]bool{
	"no-referrer": true, "no-referrer-when-downgrade": true, "origin": true,
	"origin-when-cross-origin": true, "same-origin": true,
	"strict-origin": true, "strict-origin-when-cross-origin": true,
	"unsafe-url": true, "": true,
}

func validPolicy(p string) bool { return policies[strings.ToLower(strings.TrimSpace(p))] }

type hardener struct {
	tokens []string
	policy string
	all    bool
	keep   map[string]bool

	sandboxed int
	keptOwn   int
	policySet int
	defeated  []string
	leftAlone map[string]int
}

func (h *hardener) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, h.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (h *hardener) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("iframe", func(e *lolhtml.Element) error {
			src, hasSrc := e.Attribute("src")
			host := hostOf(src)

			// An iframe with a srcdoc runs content from this document, so it is
			// first-party whatever its src says.
			_, hasSrcdoc := e.Attribute("srcdoc")

			if !h.all && (!hasSrc || hasSrcdoc || sameOrigin(src) || h.keep[host]) {
				h.note("same origin, srcdoc or no src")
				return nil
			}

			if existing, ok := e.Attribute("sandbox"); ok {
				tokens := strings.Fields(stdhtml.UnescapeString(existing))
				if defeats(tokens) {
					// Reported rather than corrected: removing a token the embed
					// needs breaks it, and which one it needs is not knowable
					// from here.
					h.defeated = append(h.defeated,
						fmt.Sprintf("%s sandbox=%q", displayHost(host), existing))
				}
				h.keptOwn++
			} else {
				h.sandboxed++
				if err := e.SetAttribute("sandbox", strings.Join(h.tokens, " ")); err != nil {
					return err
				}
			}

			if h.policy == "" {
				return nil
			}
			if _, ok := e.Attribute("referrerpolicy"); ok {
				return nil
			}
			h.policySet++
			return e.SetAttribute("referrerpolicy", h.policy)
		}),
	}
}

func hostOf(src string) string {
	u, err := url.Parse(strings.TrimSpace(stdhtml.UnescapeString(src)))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// sameOrigin reports whether a src loads from this document's own origin, which is
// the one case with nothing to harden.
//
// It is deliberately not "hostOf returned nothing". hostOf answers "" for a URL
// with no authority component and for a URL url.Parse rejects, and reading that as
// same-origin exempts the four srcs that most need a sandbox:
//
//	src="data:text/html,<script>…</script>"   a document this page carries
//	src="blob:https://x.example/…"            likewise
//	src="javascript:alert(1)"                 script in the embedding document
//	src="http://[::1"                         unparseable, so unvouched for
//
// A relative src - "/embed", "e.html", "?q=1", "#frag" - is the only one of them
// that really is this origin, and it is the only one this returns true for. A
// parse failure returns false: failing open is how a guard that reads well ends up
// doing nothing.
func sameOrigin(src string) bool {
	u, err := url.Parse(strings.TrimSpace(stdhtml.UnescapeString(src)))
	if err != nil {
		return false
	}
	// No scheme and no authority is a relative reference, resolved against this
	// document. A scheme-relative "//host/x" has an authority, so it is judged by
	// its host like any absolute URL.
	return u.Scheme == "" && u.Host == ""
}

func displayHost(h string) string {
	if h == "" {
		return "(no host)"
	}
	return h
}

func (h *hardener) note(reason string) {
	if h.leftAlone == nil {
		h.leftAlone = map[string]int{}
	}
	h.leftAlone[reason]++
}

func (h *hardener) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "sandboxed=%d kept-own-sandbox=%d referrerpolicy-set=%d defeated=%d\n",
		h.sandboxed, h.keptOwn, h.policySet, len(h.defeated))

	reasons := make([]string, 0, len(h.leftAlone))
	for r, n := range h.leftAlone {
		reasons = append(reasons, fmt.Sprintf("%s=%d", r, n))
	}
	sort.Strings(reasons)
	if len(reasons) > 0 {
		fmt.Fprintf(&sb, "left alone: %s\n", strings.Join(reasons, " "))
	}

	sort.Strings(h.defeated)
	for _, d := range h.defeated {
		fmt.Fprintf(&sb, "sandbox defeated by allow-scripts with allow-same-origin: %s\n", d)
	}
	return sb.String()
}

func hardenString(in string, opts ...func(*hardener)) (string, *hardener, error) {
	h := &hardener{
		tokens: []string{"allow-scripts", "allow-popups", "allow-forms"},
		policy: "no-referrer",
		keep:   map[string]bool{},
	}
	for _, o := range opts {
		o(h)
	}
	var out bytes.Buffer
	err := h.run(strings.NewReader(in), &out)
	return out.String(), h, err
}

// Command noopener hardens links that open a new browsing context.
//
//	noopener < page.html > out.html
//	noopener -no-referrer=false -limit 65536 < page.html
//
// A link with target="_blank" hands the new page a window.opener reference to
// the one it came from, which lets it navigate the original elsewhere. Adding
// rel="noopener" severs that. rel="noreferrer" implies it and also withholds the
// Referer header, which is why both are added by default.
//
// Modern browsers imply noopener for target="_blank" already. The attribute
// still matters for older ones, and it documents the intent, which is why this
// is worth doing to stored content rather than relying on the browser.
//
// The rel attribute is a token list, so existing tokens are kept: a link with
// rel="nofollow" comes out as rel="nofollow noopener noreferrer", not with its
// nofollow replaced.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	noReferrer := flag.Bool("no-referrer", true, `also add rel="noreferrer"`)
	limit := flag.Int("limit", 0, "memory limit in bytes for the rewriter, 0 for none")
	graceful := flag.Bool("graceful", true, "on exceeding the limit, pass the rest through unrewritten")
	flag.Parse()

	h := &hardener{noReferrer: *noReferrer, limit: *limit, graceful: *graceful}
	err := h.run(os.Stdin, os.Stdout)
	fmt.Fprint(os.Stderr, h.report())
	if err != nil {
		fmt.Fprintln(os.Stderr, "noopener:", err)
		os.Exit(1)
	}
}

// newWindowTargets are the target values that create a new browsing context.
// _self, _parent and _top reuse an existing one, so they are not at risk.
func isNewWindow(target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	switch t {
	case "_self", "_parent", "_top", "":
		return false
	case "_blank":
		return true
	default:
		// A named target reuses a context if one already has that name, and
		// creates one otherwise. It can carry an opener either way, so it is
		// treated as a new window.
		return true
	}
}

type hardener struct {
	noReferrer bool
	limit      int
	graceful   bool

	hardened  int
	alreadyOK int
	byTarget  map[string]int
	// bailedOut records that the rewriter hit its memory limit, which is the
	// difference between a document that was hardened and one that merely
	// reached the client.
	bailedOut bool
}

func (h *hardener) run(src io.Reader, dst io.Writer) error {
	opts := h.options()
	if h.limit > 0 {
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory:       h.limit,
			GracefulBailOut: h.graceful,
		}))
	}

	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return h.classify(err)
	}
	return h.classify(w.Close())
}

// classify separates a memory bail-out from any other failure, because they
// need different responses: a bail-out with GracefulBailOut set means the
// document reached the client intact but unhardened past some boundary, which is
// a security-relevant outcome rather than a transport error.
func (h *hardener) classify(err error) error {
	if err == nil {
		return nil
	}
	var ne *lolhtml.NativeError
	if errors.As(err, &ne) && ne.MemoryLimitExceeded() {
		h.bailedOut = true
		if h.graceful {
			// The response is whole; it is simply not fully rewritten.
			return nil
		}
	}
	return err
}

func (h *hardener) count(target string) {
	if h.byTarget == nil {
		h.byTarget = map[string]int{}
	}
	h.byTarget[strings.ToLower(target)]++
}

func (h *hardener) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a[target], area[target], form[target]", func(e *lolhtml.Element) error {
			target, ok := e.Attribute("target")
			if !ok || !isNewWindow(target) {
				return nil
			}

			want := []string{"noopener"}
			if h.noReferrer {
				want = append(want, "noreferrer")
			}

			// A form has no rel attribute, so the only thing to do is report it.
			if e.TagName() == "form" {
				h.count(target)
				h.hardened++
				return nil
			}

			have, _ := e.Attribute("rel")
			got, added := mergeTokens(have, want)
			h.count(target)
			if added == 0 {
				h.alreadyOK++
				return nil
			}
			h.hardened++
			return e.SetAttribute("rel", got)
		}),

		// A link that opens a new window from JavaScript cannot be hardened by
		// an attribute, so it is reported instead of silently missed.
		lolhtml.OnElement("[onclick]", func(e *lolhtml.Element) error {
			if v, ok := e.Attribute("onclick"); ok && strings.Contains(strings.ToLower(v), "window.open") {
				h.count("window.open")
			}
			return nil
		}),
	}
}

// mergeTokens adds the wanted tokens to a space-separated list, keeping what was
// there, preserving order, and matching case insensitively because rel tokens
// are case insensitive. It reports how many it had to add.
//
// Rewriting the whole attribute would be simpler and wrong: rel carries tokens
// this program knows nothing about, and dropping nofollow or sponsored changes
// what a search engine does with the link.
func mergeTokens(have string, want []string) (string, int) {
	tokens := strings.Fields(have)
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		seen[strings.ToLower(t)] = true
	}

	added := 0
	for _, w := range want {
		if seen[w] {
			continue
		}
		// noreferrer implies noopener, so a list that already has it does not
		// need the other. The reverse is not true.
		if w == "noopener" && seen["noreferrer"] {
			continue
		}
		tokens = append(tokens, w)
		seen[w] = true
		added++
	}
	return strings.Join(tokens, " "), added
}

func (h *hardener) report() string {
	targets := make([]string, 0, len(h.byTarget))
	for t, n := range h.byTarget {
		targets = append(targets, fmt.Sprintf("%s=%d", t, n))
	}
	sort.Strings(targets)

	var sb strings.Builder
	fmt.Fprintf(&sb, "hardened=%d already-safe=%d", h.hardened, h.alreadyOK)
	if len(targets) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(targets, " "))
	}
	sb.WriteString("\n")
	if h.bailedOut {
		sb.WriteString("WARNING: the memory limit was exceeded; " +
			"the tail of this document was passed through unhardened\n")
	}
	return sb.String()
}

func hardenString(in string, opts ...func(*hardener)) (string, *hardener, error) {
	h := &hardener{noReferrer: true, graceful: true}
	for _, o := range opts {
		o(h)
	}
	var out bytes.Buffer
	err := h.run(strings.NewReader(in), &out)
	return out.String(), h, err
}

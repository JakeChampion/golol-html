// Command cspnonce prepares a document for a nonce-based Content Security
// Policy: it stamps every script and style with the nonce, removes any nonce
// the document already carried, hashes inline script bodies so a hash-based
// policy can be emitted as well, and reports the constructs that no nonce can
// rescue.
//
//	cspnonce -nonce r4nd0m < page.html > out.html
//
// The policy for the rewritten document is printed to stderr, so it can be put
// in a header:
//
//	Content-Security-Policy: script-src 'nonce-r4nd0m'; style-src 'nonce-r4nd0m'
//
// Exit status is 1 if the document contains something a nonce cannot cover - an
// inline event handler, a javascript: URL, or a style attribute, which style-src
// governs and which has nowhere to carry a nonce - because those have to be
// fixed in the source rather than at the edge.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	nonce := flag.String("nonce", "", "nonce value to stamp (required)")
	meta := flag.Bool("meta", false, "also inject a CSP meta element into <head>")
	mark := flag.Bool("mark", false, "mark constructs no nonce can cover, in the document")
	flag.Parse()

	if *nonce == "" {
		fmt.Fprintln(os.Stderr, "usage: cspnonce -nonce <value> [-meta] [-mark] < in.html > out.html")
		os.Exit(2)
	}
	if err := validNonce(*nonce); err != nil {
		fmt.Fprintln(os.Stderr, "cspnonce:", err)
		os.Exit(2)
	}

	s := &stamper{nonce: *nonce, injectMeta: *meta, mark: *mark}
	if err := s.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cspnonce:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.report())
	if len(s.unnonceable) > 0 {
		os.Exit(1)
	}
}

// validNonce rejects anything that would break out of the policy string. A
// nonce is base64 in practice; anything with a quote or a semicolon in it would
// end the source list early.
func validNonce(n string) error {
	if len(n) < 8 {
		return fmt.Errorf("nonce %q is shorter than 8 characters", n)
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '-' || r == '_' || r == '=':
		default:
			return fmt.Errorf("nonce %q contains %q, which is not valid base64", n, r)
		}
	}
	return nil
}

// eventHandlerAttrs are the attributes that hold script and cannot be given a
// nonce. The list is the common ones rather than all of them: the point is to
// report that the document needs source changes, not to be exhaustive.
var eventHandlerAttrs = []string{
	"onclick", "onload", "onerror", "onmouseover", "onmouseout", "onfocus",
	"onblur", "onchange", "onsubmit", "oninput", "onkeydown", "onkeyup",
	"onanimationend", "ontoggle", "onbeforeunload", "onpageshow",
}

type stamper struct {
	nonce      string
	injectMeta bool
	// mark writes the findings into the document as well as reporting them.
	// Off by default, because a diagnostic that edits the document is not
	// idempotent: a second pass would mark the same constructs again.
	mark bool

	nonced       map[string]int // by tag name
	strippedOld  int
	unnonceable  []string
	inlineHashes []string
	headSeen     bool

	// acc accumulates the body of the inline script currently open, so it can
	// be hashed. Text arrives in chunks with no guaranteed boundaries, and the
	// element's end tag is what says the body is complete.
	acc strings.Builder
	// inInline is set while an inline script is open. Nested scripts cannot
	// happen, so one flag is enough.
	inInline bool
}

func (s *stamper) run(src io.Reader, dst io.Writer) error {
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

func (s *stamper) count(tag string) {
	if s.nonced == nil {
		s.nonced = map[string]int{}
	}
	s.nonced[tag]++
}

func (s *stamper) options() []lolhtml.Option {
	return []lolhtml.Option{
		// Every script and style gets the nonce, external ones included: under a
		// nonce-based policy with no host allowlist, a src'd script needs it too.
		lolhtml.OnElement("script, style", func(e *lolhtml.Element) error {
			// A nonce the document arrived with is not ours and must not
			// survive: an injected script carrying a guessed nonce would be
			// trusted by the policy we are about to publish.
			if had, err := e.HasAttribute("nonce"); err != nil {
				return err
			} else if had {
				s.strippedOld++
				if err := e.RemoveAttribute("nonce"); err != nil {
					return err
				}
			}
			s.count(e.TagName())
			if err := e.SetAttribute("nonce", s.nonce); err != nil {
				return err
			}

			// Only an inline script has a body to hash.
			if e.TagName() != "script" {
				return nil
			}
			if _, external := e.Attribute("src"); external {
				return nil
			}
			// This selector matches by tag name, so it also matches a
			// <script/> in foreign content, which really is self-closing: it
			// has no body to hash and no end tag to wait for. OnEndTag returns
			// an error for an element that cannot have content, and that error
			// fails the whole rewrite - here, after the prefix of the page has
			// already been written to the output. CanHaveContent is the guard
			// the library asks for before OnEndTag on any selector that can
			// match an element like this.
			if !e.CanHaveContent() {
				return nil
			}
			s.acc.Reset()
			s.inInline = true
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				s.inInline = false
				sum := sha256.Sum256([]byte(s.acc.String()))
				s.inlineHashes = append(s.inlineHashes,
					"'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
				return nil
			})
		}),

		// The body of an inline script, which arrives in as many chunks as the
		// network felt like. Accumulate; the end tag above decides when it is
		// whole.
		lolhtml.OnText("script", func(t *lolhtml.TextChunk) error {
			if s.inInline {
				s.acc.WriteString(t.Text())
			}
			return nil
		}),

		// Constructs a nonce cannot cover. Reported rather than removed:
		// silently deleting behaviour is worse than refusing to certify it.
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			found := 0
			for _, a := range eventHandlerAttrs {
				if v, ok := e.Attribute(a); ok {
					s.unnonceable = append(s.unnonceable,
						fmt.Sprintf("<%s %s=%q>", e.TagName(), a, truncate(v)))
					found++
				}
			}
			for _, a := range []string{"href", "src", "action", "formaction"} {
				if v, ok := e.Attribute(a); ok && isJavaScriptURL(v) {
					s.unnonceable = append(s.unnonceable,
						fmt.Sprintf("<%s %s=%q>", e.TagName(), a, truncate(v)))
					found++
				}
			}
			// A style attribute is unnonceable for the same reason an event
			// handler is: style-src governs it, and there is nowhere to put a
			// nonce on an attribute. Only 'unsafe-hashes' or 'unsafe-inline'
			// admits one, and the policy printed below has neither - so a
			// document full of them would render unstyled under the policy this
			// program certified.
			if v, ok := e.Attribute("style"); ok {
				s.unnonceable = append(s.unnonceable,
					fmt.Sprintf("<%s style=%q>", e.TagName(), truncate(v)))
				found++
			}
			if found == 0 || !s.mark {
				return nil
			}
			// Text, not HTML: nothing here is markup, and Text is what keeps
			// that true if the message ever grows to quote the value it found.
			//
			// After writes outside the element, not into its content, and the
			// difference matters on the elements this handler can match. An
			// insertion into a <script> or a <style> is checked against
			// ErrRawTextBreakout; one written next to it is ordinary markup and
			// is deliberately not checked. What is checked is the position.
			return e.After(fmt.Sprintf(" [csp: %d construct(s) no nonce can cover]", found),
				lolhtml.Text)
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			s.headSeen = true
			if !s.injectMeta {
				return nil
			}
			// HTML: this is an element we are constructing, so its tags have to
			// survive as markup. The nonce is validated above, so it cannot
			// close the attribute.
			return e.Prepend(`<meta http-equiv="Content-Security-Policy" content="`+
				s.policy()+`">`, lolhtml.HTML)
		}),
	}
}

func (s *stamper) policy() string {
	return "script-src 'nonce-" + s.nonce + "'; style-src 'nonce-" + s.nonce + "'"
}

func (s *stamper) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Content-Security-Policy: %s\n", s.policy())

	tags := make([]string, 0, len(s.nonced))
	for t, n := range s.nonced {
		tags = append(tags, fmt.Sprintf("%s=%d", t, n))
	}
	sort.Strings(tags)
	fmt.Fprintf(&sb, "nonced: %s stripped-existing=%d head=%v\n",
		strings.Join(tags, " "), s.strippedOld, s.headSeen)

	for _, h := range s.inlineHashes {
		fmt.Fprintf(&sb, "inline-hash: %s\n", h)
	}
	for _, u := range s.unnonceable {
		fmt.Fprintf(&sb, "no nonce can cover: %s\n", u)
	}
	return sb.String()
}

func isJavaScriptURL(v string) bool {
	// The value arrives as source text with its character references still
	// encoded, and a browser decodes them before it ever looks at the scheme:
	// href="&#106;avascript:alert(1)" navigates to javascript:alert(1). So the
	// decode comes first, or the guard is bypassed by spelling one letter as a
	// reference. Anything deciding something about an attribute value has to do
	// this; only a value being copied straight back stays raw.
	//
	// A URL scheme is case insensitive and may carry whitespace or control
	// characters before the colon, which browsers strip. "java\tscript:" is a
	// real bypass, and so is "java&#9;script:" once decoded, so the comparison
	// is made after removing them.
	var b strings.Builder
	for _, r := range stdhtml.UnescapeString(v) {
		if r <= ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.HasPrefix(strings.ToLower(b.String()), "javascript:")
}

func truncate(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:40] + "..."
}

func stampString(in, nonce string, meta bool) (string, *stamper, error) {
	s := &stamper{nonce: nonce, injectMeta: meta}
	var out bytes.Buffer
	err := s.run(strings.NewReader(in), &out)
	return out.String(), s, err
}

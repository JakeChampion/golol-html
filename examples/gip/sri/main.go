// Command sri adds subresource integrity attributes to a document's scripts and
// stylesheets, from a manifest of hashes, and reports every subresource the
// manifest does not cover.
//
//	sri -manifest hashes.txt < page.html > out.html
//
// The manifest is one "url sha384-base64" pair per line. A subresource with no
// entry is left alone and reported: guessing an integrity value would break the
// page, and omitting the attribute silently would defeat the point of running
// this at all. Exit status is 1 if anything was left uncovered.
//
// The manifest that was actually used is embedded in the output as a JSON block,
// written through a streaming sink so a large manifest never has to be
// assembled in memory first.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	manifest := flag.String("manifest", "", "file of \"url sha384-...\" lines (required)")
	embed := flag.Bool("embed", false, "embed the manifest used, as a JSON block in <head>")
	flag.Parse()

	if *manifest == "" {
		fmt.Fprintln(os.Stderr, "usage: sri -manifest <file> [-embed] < in.html > out.html")
		os.Exit(2)
	}
	f, err := os.Open(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sri:", err)
		os.Exit(2)
	}
	defer f.Close()

	m, err := parseManifest(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sri:", err)
		os.Exit(2)
	}

	a := &adder{manifest: m, embed: *embed}
	if err := a.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sri:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, a.report())
	if len(a.uncovered) > 0 || len(a.conflicts) > 0 {
		os.Exit(1)
	}
}

// parseManifest reads "url hash" pairs. A malformed line is an error rather than
// a skip: a manifest that is half read produces a document that is half
// protected, which is the worst of both.
func parseManifest(r io.Reader) (map[string]string, error) {
	m := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		url, hash, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("manifest line %d: want \"url hash\", got %q", n, line)
		}
		hash = strings.TrimSpace(hash)
		if err := validHash(hash); err != nil {
			return nil, fmt.Errorf("manifest line %d: %w", n, err)
		}
		m[url] = hash
	}
	return m, sc.Err()
}

// validHash rejects anything that could not be an integrity value, including
// anything that would escape the attribute it is about to be written into.
func validHash(h string) error {
	algo, b64, ok := strings.Cut(h, "-")
	if !ok {
		return fmt.Errorf("hash %q has no algorithm prefix", h)
	}
	switch algo {
	case "sha256", "sha384", "sha512":
	default:
		return fmt.Errorf("hash %q uses %q, which is not an SRI algorithm", h, algo)
	}
	if b64 == "" {
		return fmt.Errorf("hash %q has no digest", h)
	}
	for _, r := range b64 {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '=':
		default:
			return fmt.Errorf("hash %q contains %q, which is not base64", h, r)
		}
	}
	return nil
}

// integritySelectors are the elements that honour an integrity attribute. A
// preload or a modulepreload needs one too, or the fetch it primes is unchecked.
var integritySelectors = []struct{ selector, attr string }{
	{"script[src]", "src"},
	{`link[rel="stylesheet"][href], link[rel="preload"][href], link[rel="modulepreload"][href]`, "href"},
}

type adder struct {
	manifest map[string]string
	embed    bool

	added     int
	kept      int
	uncovered []string
	conflicts []string
	used      map[string]string
}

func (a *adder) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, a.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (a *adder) options() []lolhtml.Option {
	opts := make([]lolhtml.Option, 0, len(integritySelectors)+2)

	for _, sel := range integritySelectors {
		sel := sel
		opts = append(opts, lolhtml.OnElement(sel.selector, func(e *lolhtml.Element) error {
			url, ok := e.Attribute(sel.attr)
			if !ok || strings.TrimSpace(url) == "" {
				return nil
			}

			want, known := a.manifest[url]
			if !known {
				a.uncovered = append(a.uncovered, url)
				return nil
			}

			// An integrity attribute the document already carried is checked
			// rather than overwritten. Replacing a hash that disagrees would
			// turn a detectable mismatch into a silent one.
			if have, ok := e.Attribute("integrity"); ok {
				if have != want {
					a.conflicts = append(a.conflicts,
						fmt.Sprintf("%s: document has %s, manifest has %s", url, have, want))
					return nil
				}
				a.kept++
				return nil
			}

			if a.used == nil {
				a.used = map[string]string{}
			}
			a.used[url] = want
			a.added++

			if err := e.SetAttribute("integrity", want); err != nil {
				return err
			}
			// Integrity on a cross-origin fetch is only enforced if the request
			// is CORS, so the two attributes travel together.
			if _, ok := e.Attribute("crossorigin"); !ok {
				return e.SetAttribute("crossorigin", "anonymous")
			}
			return nil
		}))
	}

	opts = append(opts,
		// The manifest actually used, embedded as data rather than as script.
		// Written through a sink so a large manifest is never assembled in
		// memory: the closure runs at the point the content is needed.
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if !a.embed {
				return nil
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.StreamBefore(a.writeManifestBlock)
			})
		}),

		// A document that names a subresource inside a comment is a common way
		// to leave a half-disabled tag behind, and a rewriter cannot reach it.
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if strings.Contains(c.Text(), "<script") || strings.Contains(c.Text(), "<link") {
				a.uncovered = append(a.uncovered, "(in a comment) "+truncate(c.Text()))
			}
			return nil
		}),
	)

	return opts
}

// writeManifestBlock emits the manifest as a JSON script block, one entry at a
// time. A JSON block is the right shape for data that came from outside: the
// document reads it with JSON.parse rather than executing it, and the content
// type stays HTML because the delimiters have to survive as markup.
//
// The URLs and hashes are written with encoding/json, which escapes < and > as
// < and >, so nothing in the data can end the script element.
func (a *adder) writeManifestBlock(s *lolhtml.Sink) error {
	if err := s.WriteString(`<script type="application/json" id="sri-manifest">`, lolhtml.HTML); err != nil {
		return err
	}

	urls := make([]string, 0, len(a.used))
	for u := range a.used {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	if err := s.WriteString("{", lolhtml.HTML); err != nil {
		return err
	}
	for i, u := range urls {
		if i > 0 {
			if err := s.WriteString(",", lolhtml.HTML); err != nil {
				return err
			}
		}
		k, err := json.Marshal(u)
		if err != nil {
			return err
		}
		v, err := json.Marshal(a.used[u])
		if err != nil {
			return err
		}
		if err := s.WriteChunk(append(append(k, ':'), v...), lolhtml.HTML); err != nil {
			return err
		}
	}
	if err := s.WriteString("}", lolhtml.HTML); err != nil {
		return err
	}
	return s.WriteString("</script>", lolhtml.HTML)
}

func (a *adder) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "integrity added=%d kept=%d uncovered=%d conflicts=%d\n",
		a.added, a.kept, len(a.uncovered), len(a.conflicts))
	for _, u := range a.uncovered {
		fmt.Fprintf(&sb, "uncovered: %s\n", u)
	}
	for _, c := range a.conflicts {
		fmt.Fprintf(&sb, "conflict: %s\n", c)
	}
	return sb.String()
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 50 {
		return s
	}
	return s[:50] + "..."
}

func addString(in string, m map[string]string, embed bool) (string, *adder, error) {
	a := &adder{manifest: m, embed: embed}
	var out bytes.Buffer
	err := a.run(strings.NewReader(in), &out)
	return out.String(), a, err
}

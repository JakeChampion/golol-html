// Command weight reports the total byte weight of the scripts, styles and
// images a page references.
//
// It cannot fetch anything, so the sizes of external resources come from a
// manifest - the kind a build already produces - and everything not in it is
// reported as unknown rather than guessed at or silently counted as zero. The
// weight of inline scripts and styles is not guessed either: it is measured from
// the source, as the byte distance between the end of the start tag and the start
// of the end tag, which is exactly what those bytes cost on the wire.
//
// Two decisions worth stating, because they change the number.
//
// A URL referenced twice is counted once. The second reference is a cache hit,
// so counting it twice would report a weight the page never has.
//
// Every candidate in a srcset is counted as referenced, because which one a
// browser picks depends on the device. The report separates them so a caller can
// decide: Images.Known is the whole set, and Images.LargestCandidates is what
// the page weighs if every picture element takes its heaviest option.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Manifest maps a referenced URL to its size in bytes.
type Manifest map[string]int64

// A Kind is the accounting for one class of resource.
type Kind struct {
	// URLs are the distinct URLs referenced, in the order first seen.
	URLs []string
	// Known is the total size of the URLs the manifest knows.
	Known int64
	// Unknown are the URLs the manifest has no size for, in the order first
	// seen. They are not counted in Known, and they are not zero.
	Unknown []string
	// Inline is the measured byte weight of inline content of this kind: the
	// text of a <script> or a <style>, as it appeared in the source.
	Inline int64
}

// Total is the weight this kind contributes, which is the part that is known.
func (k Kind) Total() int64 { return k.Known + k.Inline }

// A Report is the whole accounting.
type Report struct {
	Scripts Kind
	Styles  Kind
	Images  Kind
	// LargestCandidates is the total of Images plus, for each srcset, only its
	// heaviest candidate rather than all of them. It is the weight of the page
	// on the device that picks the worst option.
	LargestCandidates int64
}

// Total is the weight of everything known.
func (r Report) Total() int64 { return r.Scripts.Total() + r.Styles.Total() + r.Images.Total() }

// Unknown is every URL with no size, across all three kinds.
func (r Report) Unknown() []string {
	var all []string
	all = append(all, r.Scripts.Unknown...)
	all = append(all, r.Styles.Unknown...)
	all = append(all, r.Images.Unknown...)
	return all
}

// Measure reads the document from r and reports what it references.
func Measure(r io.Reader, m Manifest) (Report, error) {
	var rep Report
	seen := map[string]bool{}

	// add records one reference. The manifest lookup happens here rather than at
	// the end so that a URL is classified once, when it is first seen.
	add := func(k *Kind, raw string) {
		u := strings.TrimSpace(raw)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		k.URLs = append(k.URLs, u)
		if size, ok := m[u]; ok {
			k.Known += size
		} else {
			k.Unknown = append(k.Unknown, u)
		}
	}

	// inline measures the source bytes between a start tag and its end tag. The
	// start position is captured when the element is reported and used when the
	// end tag arrives, which is the only order that works: the end tag knows
	// where it starts, and nothing else does.
	//
	// This needs no guard against being handed an enclosing element's end tag,
	// which is what happens to an element whose end tag the source left out,
	// because script and style are raw text: nothing but their own closing tag
	// ends them. The one way to get no end tag here is a truncated document, and
	// then the handler does not run and the weight is reported as zero.
	inline := func(e *lolhtml.Element, k *Kind) error {
		if !e.CanHaveContent() {
			return nil
		}
		from := e.SourceLocation().End
		return e.OnEndTag(func(t *lolhtml.EndTag) error {
			if n := t.SourceLocation().Start - from; n > 0 {
				k.Inline += int64(n)
			}
			return nil
		})
	}

	// largest tracks, per srcset, the biggest candidate the manifest knows, so
	// the report can say what the page weighs when every choice goes badly.
	var largestSum int64

	opts := []lolhtml.Option{
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			if src, ok := e.Attribute("src"); ok {
				add(&rep.Scripts, src)
				// A script with a src has no content that reaches the network
				// twice; its inline text, if any, is ignored by the browser.
				return nil
			}
			return inline(e, &rep.Scripts)
		}),
		lolhtml.OnElement("style", func(e *lolhtml.Element) error {
			return inline(e, &rep.Styles)
		}),
		lolhtml.OnElement("link", func(e *lolhtml.Element) error {
			if !hasToken(e, "rel", "stylesheet") {
				return nil
			}
			if href, ok := e.Attribute("href"); ok {
				add(&rep.Styles, href)
			}
			return nil
		}),
		lolhtml.OnElement("img, source", func(e *lolhtml.Element) error {
			if src, ok := e.Attribute("src"); ok {
				add(&rep.Images, src)
			}
			if set, ok := e.Attribute("srcset"); ok {
				var biggest int64
				for _, u := range ParseSrcset(set) {
					add(&rep.Images, u)
					if size, ok := m[u]; ok && size > biggest {
						biggest = size
					}
				}
				largestSum += biggest
			}
			return nil
		}),
	}

	// Nothing is rewritten, so the output goes nowhere. There is no read-only
	// mode, and asking for one would be asking for a second implementation of
	// the parser.
	rw, err := lolhtml.NewWriter(io.Discard, opts...)
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
	rep.LargestCandidates = largestSum
	return rep, nil
}

// ParseSrcset splits a srcset attribute into its URLs, following the HTML
// algorithm rather than splitting on commas.
//
// Splitting on commas is wrong because a URL may contain one - a data: URL
// almost always does, and a query string may. The algorithm is: skip leading
// whitespace and commas, take everything up to the next whitespace as the URL,
// and if that ends in commas, strip them and the candidate is done; otherwise
// skip the descriptor up to the next comma.
func ParseSrcset(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (isSpace(s[i]) || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		url := s[start:i]
		trimmed := strings.TrimRight(url, ",")
		if len(trimmed) < len(url) {
			// The URL carried the separator, so there is no descriptor.
			if trimmed != "" {
				out = append(out, trimmed)
			}
			continue
		}
		if url != "" {
			out = append(out, url)
		}
		// Skip the descriptor, which runs to the next comma.
		for i < len(s) && s[i] != ',' {
			i++
		}
	}
	return out
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return false
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
	var m Manifest
	if len(os.Args) > 1 {
		b, err := os.ReadFile(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "weight:", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			fmt.Fprintln(os.Stderr, "weight: manifest:", err)
			os.Exit(1)
		}
	}
	rep, err := Measure(os.Stdin, m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "weight:", err)
		os.Exit(1)
	}
	for _, row := range []struct {
		name string
		k    Kind
	}{{"scripts", rep.Scripts}, {"styles", rep.Styles}, {"images", rep.Images}} {
		fmt.Printf("%-8s %8d bytes  %d external, %d inline bytes, %d unknown\n",
			row.name, row.k.Total(), len(row.k.URLs), row.k.Inline, len(row.k.Unknown))
	}
	fmt.Printf("%-8s %8d bytes\n", "total", rep.Total())
	if unknown := rep.Unknown(); len(unknown) > 0 {
		sort.Strings(unknown)
		fmt.Printf("unknown size: %s\n", strings.Join(unknown, " "))
	}
}

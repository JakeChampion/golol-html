// Command jsonld extracts every JSON-LD block from a document and reports what
// is wrong with each one.
//
//	jsonld page.html
//	block 1 at bytes 47..312  @type=BreadcrumbList  ok
//	block 2 at bytes 980..1024  @type=Product  2 problems
//	  missing "@context"
//	  "offers" is not an object or an array
//
// It changes nothing. The document goes to standard output byte for byte, and
// the report to standard error, so it composes in a pipeline with programs that
// do rewrite.
//
// The offsets are the point of doing this with a rewriter rather than a parser.
// A block's SourceLocation is counted from the first byte fed in, whatever the
// chunking, so a caller holding its own copy of the input can slice out exactly
// the bytes complained about. This program does that itself to report them, and
// the property is pinned in sourceloc_test.go rather than assumed here.
//
// The JSON is parsed with encoding/json, which is the right tool for reading -
// unlike writing into a script, where its lack of "/" escaping makes it the
// wrong one.
package main

import (
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

// A block is one JSON-LD script and what was found in it.
type block struct {
	loc      lolhtml.SourceLocation
	raw      string
	problems []string
}

type extractor struct {
	strict   bool // treat a missing @type as a problem too
	maxBytes int  // refuse to buffer a block larger than this

	blocks  []block
	skipped map[string]int
}

func (x *extractor) note(reason string) {
	if x.skipped == nil {
		x.skipped = map[string]int{}
	}
	x.skipped[reason]++
}

func defaults() *extractor { return &extractor{maxBytes: 1 << 20} }

func (x *extractor) validate() error {
	if x.maxBytes < 1 {
		return fmt.Errorf("-max-bytes %d leaves no room for a block", x.maxBytes)
	}
	return nil
}

// ldSelector matches a JSON-LD script. The type attribute's value is matched
// without regard to case because type is on the HTML list of attributes whose
// values are, so "APPLICATION/LD+JSON" is found without lower-casing anything.
const ldSelector = `script[type="application/ld+json"]`

func (x *extractor) options() []lolhtml.Option {
	// A block's text arrives in as many chunks as the input was written in, so
	// it is gathered between the start and end tags. The location is taken from
	// the first chunk and extended by each one after it, which is what makes the
	// reported range the whole block rather than its last piece.
	var text strings.Builder
	var loc lolhtml.SourceLocation
	open, started, truncated := false, false, false

	return []lolhtml.Option{
		lolhtml.OnElement(ldSelector, func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				x.note("a self-closing JSON-LD script has no content")
				return nil
			}
			text.Reset()
			open, started, truncated = true, false, false
			// The start tag's own range is the fallback: a block with no text
			// produces no text chunk, and without this the report would carry
			// the previous block's offsets.
			loc = e.SourceLocation()
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				open = false
				if truncated {
					x.note("a block was larger than -max-bytes and was not checked")
					return nil
				}
				x.blocks = append(x.blocks, x.check(loc, text.String()))
				return nil
			})
		}),

		lolhtml.OnText(ldSelector, func(tc *lolhtml.TextChunk) error {
			if !open || tc.Text() == "" {
				return nil
			}
			if text.Len()+len(tc.Text()) > x.maxBytes {
				truncated = true
				return nil
			}
			at := tc.SourceLocation()
			if !started {
				loc = at
				started = true
			} else {
				loc.End = at.End
			}
			text.WriteString(tc.Text())
			return nil
		}),
	}
}

// check parses one block and lists what is wrong with it. The raw text is a
// script body, so character references are not decoded in it by a parser and must
// not be decoded here either: "&amp;" in a JSON string is those five characters.
func (x *extractor) check(loc lolhtml.SourceLocation, raw string) block {
	b := block{loc: loc, raw: raw}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		b.problems = append(b.problems, "the block is empty")
		return b
	}

	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		b.problems = append(b.problems, "not valid JSON: "+err.Error())
		return b
	}

	switch t := v.(type) {
	case map[string]any:
		b.problems = append(b.problems, x.checkNode(t, "")...)
	case []any:
		if len(t) == 0 {
			b.problems = append(b.problems, "the array is empty")
		}
		for i, item := range t {
			node, ok := item.(map[string]any)
			if !ok {
				b.problems = append(b.problems,
					fmt.Sprintf("item %d is %s, not an object", i, kindOf(item)))
				continue
			}
			b.problems = append(b.problems, x.checkNode(node, fmt.Sprintf("item %d: ", i))...)
		}
	default:
		b.problems = append(b.problems,
			"the top level is "+kindOf(v)+", not an object or an array")
	}
	return b
}

// checkNode checks the shape a JSON-LD node has to have. Not the vocabulary:
// whether a Product may have an "offers" is schema.org's business, and a program
// that pretended to know would be wrong more often than useful.
func (x *extractor) checkNode(node map[string]any, prefix string) []string {
	var problems []string

	if _, ok := node["@context"]; !ok {
		// Nested nodes inherit the context, so only the top level needs one.
		if prefix == "" {
			problems = append(problems, `missing "@context"`)
		}
	} else if s, ok := node["@context"].(string); ok && !strings.Contains(s, "schema.org") {
		problems = append(problems,
			fmt.Sprintf("%q is not a schema.org context", s))
	}

	switch t := node["@type"].(type) {
	case nil:
		if x.strict {
			problems = append(problems, `missing "@type"`)
		}
	case string:
		if strings.TrimSpace(t) == "" {
			problems = append(problems, `"@type" is empty`)
		}
	case []any:
		if len(t) == 0 {
			problems = append(problems, `"@type" is an empty array`)
		}
	default:
		problems = append(problems, `"@type" is `+kindOf(node["@type"])+", not a string or an array")
	}

	for _, key := range sortedKeys(node) {
		if strings.HasPrefix(key, "@") {
			continue
		}
		if node[key] == nil {
			problems = append(problems, fmt.Sprintf("%q is null", key))
		}
	}

	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, prefix+p)
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "an unknown value"
}

// typeOf is what the report shows for a block, and it is deliberately read from
// the raw JSON rather than from the parsed value: a block that failed to parse
// still usually has a legible @type, and saying which block is broken is more
// useful than saying that one is.
//
// Read, and not decoded. The same rule check states applies here: this is a
// script body, so a parser does not decode character references in it and
// neither may this - a @type of "A&amp;B" is seven characters to encoding/json
// and to every JSON-LD consumer, and unescaping it would print "A&B", a value
// that appears nowhere in the document and that a reader grepping for it will
// not find.
func typeOf(raw string) string {
	i := strings.Index(raw, `"@type"`)
	if i < 0 {
		return "?"
	}
	rest := raw[i+len(`"@type"`):]
	if j := strings.Index(rest, `"`); j >= 0 {
		rest = rest[j+1:]
		if k := strings.Index(rest, `"`); k >= 0 {
			return rest[:k]
		}
	}
	return "?"
}

func (x *extractor) run(r io.Reader, w io.Writer) error {
	if err := x.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, x.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractString(in string, opts ...func(*extractor)) (string, *extractor, error) {
	x := defaults()
	for _, o := range opts {
		o(x)
	}
	var out bytes.Buffer
	err := x.run(strings.NewReader(in), &out)
	return out.String(), x, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// report is the whole point of the program, so it is built as a string rather
// than printed piecemeal: it is easier to test and easier to diff between runs.
func (x *extractor) report() string {
	var sb strings.Builder
	if len(x.blocks) == 0 {
		sb.WriteString("no JSON-LD blocks\n")
	}
	problems := 0
	for i, b := range x.blocks {
		fmt.Fprintf(&sb, "block %d at bytes %s  @type=%s  ", i+1, b.loc, typeOf(b.raw))
		switch len(b.problems) {
		case 0:
			sb.WriteString("ok\n")
		case 1:
			sb.WriteString("1 problem\n")
		default:
			fmt.Fprintf(&sb, "%d problems\n", len(b.problems))
		}
		for _, p := range b.problems {
			fmt.Fprintf(&sb, "  %s\n", p)
		}
		problems += len(b.problems)
	}
	reasons := make([]string, 0, len(x.skipped))
	for r := range x.skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&sb, "note: %s (%d)\n", r, x.skipped[r])
	}
	fmt.Fprintf(&sb, "blocks=%d problems=%d\n", len(x.blocks), problems)
	return sb.String()
}

func main() {
	x := defaults()
	flag.BoolVar(&x.strict, "strict", false, `also report a missing "@type"`)
	flag.IntVar(&x.maxBytes, "max-bytes", x.maxBytes,
		"largest block to buffer for checking")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "jsonld:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: jsonld [-strict] [file.html]")
		os.Exit(2)
	}

	if err := x.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "jsonld:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, x.report())
}

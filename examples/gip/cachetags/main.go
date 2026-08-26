// Command cachetags collects cache tags from a document and turns them into a header value, which
// is a thing a streaming rewriter cannot quite do.
//
//	$ cachetags page.html
//	Cache-Tag: product-42, category-shoes, page-home
//
//	$ cachetags -report page.html
//	3 tags from 47 elements
//	  header            Cache-Tag: product-42, category-shoes, page-home
//	  length            48 bytes of a 4096 budget
//	  refused           1: a tag containing a line feed
//
// # A header is sent before the body it describes
//
// The tags are in the body and the header goes in front of it, so there is no single streaming
// pass that can do both: by the time the last tag is known the header has left. Four answers, and
// three of them are real.
//
//	buffer the body, then send      O(N) memory, one parse
//	parse twice                     O(1) memory, two parses
//	send the tags as a trailer      no memory, no second parse, few clients read one
//	put the tags in the document     not a header
//
// This program parses twice, and the reason is a measurement rather than a preference. Fastest of
// twenty runs over a 347 KB page on an M3 Pro, normalised to one pass that rewrites the body:
//
//	pass                                  time    relative
//	copy only, no handlers               406µs       0.08x
//	collect only, no output             3.351ms      0.66x
//	rewrite only (the baseline)         5.085ms      1.00x
//	two passes: collect then copy       3.773ms      0.75x
//	two passes: collect then rewrite    8.463ms      1.67x
//	one pass, buffered                  7.285ms      1.44x
//
// The first row is the surprise and it decides the design: a pass with no handlers costs eight per
// cent of a pass with them, because with nothing registered the sink hands the destination a slice
// over lol-html's own buffer instead of copying. So if the body is passing through unchanged -
// which is the common case for a cache-tag extractor - collecting and then copying costs *less*
// than a single pass that rewrites, and holds nothing.
//
// Where the body is being rewritten too, the trade is 1.67x against 1.44x: two passes are sixteen
// per cent slower than buffering and hold nothing where buffering holds the document. Measured
// live heap, after a collection, at the moment the header would be set:
//
//	page       buffered holds   two passes hold
//	68 KB              +72 KB           nothing
//	348 KB            +351 KB           nothing
//	1.4 MB           +1.76 MB           nothing
//
// A rewriter is for documents that do not fit in memory, so the memory column is the one that
// matters, and sixteen per cent is what it costs.
//
// # A tag from a document is not a header value
//
// It arrives as attribute-value or text source, so it needs decoding before it is used and
// checking before it is sent. A carriage return or a line feed in a header value is a header
// injection, and a tag is exactly the kind of value a template interpolates from a database.
// Anything with one is refused and reported rather than stripped: a tag that has been altered is
// not the tag the page asked for, and silently caching under the wrong key is worse than not
// caching.
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Tags is what a collect pass found.
type Tags struct {
	// Names are the tags, deduplicated, in the order the document first mentioned them.
	Names []string
	// Elements is how many elements carried a tag, which is not the same as len(Names).
	Elements int
	// Refused is the tags that cannot go in a header, with the reason.
	Refused []Refusal
	// Budget is the header length allowed, and Truncated the tags dropped to fit.
	Budget    int
	Truncated []string
}

// Refusal is a tag that was found and not used.
type Refusal struct {
	Tag, Why string
}

// Header is the header value, or the empty string when there is nothing to say.
func (t Tags) Header(name string) string {
	if len(t.Names) == 0 {
		return ""
	}
	return name + ": " + strings.Join(t.Names, ", ")
}

func (t Tags) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d tag%s from %d element%s\n",
		len(t.Names), plural(len(t.Names)), t.Elements, plural(t.Elements))
	if h := t.Header("Cache-Tag"); h != "" {
		fmt.Fprintf(&b, "  %-18s %s\n", "header", h)
		fmt.Fprintf(&b, "  %-18s %d bytes of a %d budget\n", "length", len(h), t.Budget)
	}
	for _, r := range t.Refused {
		fmt.Fprintf(&b, "  %-18s %q: %s\n", "refused", r.Tag, r.Why)
	}
	if len(t.Truncated) > 0 {
		fmt.Fprintf(&b, "  %-18s %d dropped to fit the budget: %s\n", "truncated",
			len(t.Truncated), strings.Join(t.Truncated, ", "))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// checkTag says why a tag cannot go in a header value, or returns the empty string.
//
// The list is short because a header value is a narrow thing: no carriage return or line feed,
// which would end the header and begin another, and no control characters or comma, which is the
// separator this uses.
func checkTag(tag string) string {
	switch {
	case tag == "":
		return "empty"
	case strings.ContainsAny(tag, "\r\n"):
		return "a tag containing a carriage return or line feed would end the header"
	case strings.Contains(tag, ","):
		return "a tag containing a comma cannot be told from two tags"
	}
	for _, r := range tag {
		if r < 0x20 || r == 0x7f {
			return fmt.Sprintf("a tag containing %q is not a header value", r)
		}
		if r > 0x7e {
			return "a tag outside ASCII is not a header value"
		}
	}
	return ""
}

// Collect reads src and gathers the tags, writing nothing. This is the pass that costs two thirds
// of a rewriting pass and holds nothing.
func Collect(src io.Reader, attr, metaName string, budget int) (Tags, error) {
	t := Tags{Budget: budget}
	seen := map[string]bool{}

	refuse := func(tag, why string) {
		t.Refused = append(t.Refused, Refusal{Tag: tag, Why: why})
	}

	add := func(tag string) {
		if why := checkTag(tag); why != "" {
			refuse(tag, why)
			return
		}
		if seen[tag] {
			return
		}
		seen[tag] = true
		t.Names = append(t.Names, tag)
	}

	// addList decodes the whole value before splitting it, and refuses the whole value if the
	// decoded form holds a line break.
	//
	// Splitting first would be a hole with no error in it: strings.Fields treats a newline as
	// a separator, so "a\nb" becomes the two clean tags "a" and "b", the newline never
	// reaches the check, and the cache is keyed on two tags the page never asked for. The
	// header is not broken and the answer is still wrong. So the check comes before the
	// split, and the whole value goes rather than being quietly divided.
	addList := func(raw string, sep func(rune) bool) {
		decoded := html.UnescapeString(raw)
		if strings.ContainsAny(decoded, "\r\n") {
			refuse(decoded, "a value containing a carriage return or line feed would "+
				"end the header, and splitting it would invent tags")
			return
		}
		for _, field := range strings.FieldsFunc(decoded, sep) {
			if f := strings.TrimSpace(field); f != "" {
				add(f)
			}
		}
	}

	bySpace := func(r rune) bool { return r == ' ' || r == '\t' }
	byComma := func(r rune) bool { return r == ',' }

	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("["+attr+"]", func(e *lolhtml.Element) error {
			v, _ := e.Attribute(attr)
			t.Elements++
			// One attribute can carry several tags, space-separated, like a
			// class attribute.
			addList(v, bySpace)
			return nil
		}),
		lolhtml.OnElement(`meta[name="`+metaName+`"]`, func(e *lolhtml.Element) error {
			v, _ := e.Attribute("content")
			t.Elements++
			addList(v, byComma)
			return nil
		}))
	if err != nil {
		return t, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return t, err
	}
	if err := w.Close(); err != nil {
		return t, err
	}

	// The budget is applied here rather than in the header, so the report can say what was
	// dropped. A header longer than the origin allows is a request that fails.
	t.trim()
	return t, nil
}

// trim drops tags from the end until the header fits the budget.
func (t *Tags) trim() {
	if t.Budget <= 0 {
		return
	}
	for len(t.Names) > 0 {
		if len(t.Header("Cache-Tag")) <= t.Budget {
			return
		}
		t.Truncated = append(t.Truncated, t.Names[len(t.Names)-1])
		t.Names = t.Names[:len(t.Names)-1]
	}
}

// Stream copies src to dst. It registers no handlers, which is the cheap pass: with nothing
// registered the sink hands the destination a slice over lol-html's own buffer rather than copying,
// and the pass costs eight per cent of one with handlers.
func Stream(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func main() {
	attr := flag.String("attr", "data-cache-tag", "the attribute that carries tags")
	metaName := flag.String("meta", "cache-tag", "the meta name that carries tags")
	header := flag.String("header", "Cache-Tag", "the header to build")
	budget := flag.Int("budget", 4096, "the longest header value to build")
	report := flag.Bool("report", false, "print what was found instead of the header")
	body := flag.Bool("body", false, "print the document as well, after the header")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "cachetags: a file is needed, because this reads it twice")
		os.Exit(2)
	}
	name := flag.Arg(0)

	// Two passes over a file rather than one over a stream, which is the trade the package
	// comment measures: O(1) memory for a second parse.
	first, err := os.Open(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cachetags:", err)
		os.Exit(1)
	}
	tags, err := Collect(first, *attr, *metaName, *budget)
	first.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cachetags:", err)
		os.Exit(1)
	}

	if *report {
		fmt.Print(tags)
	} else if h := tags.Header(*header); h != "" {
		fmt.Println(h)
	}

	if *body {
		second, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cachetags:", err)
			os.Exit(1)
		}
		defer second.Close()
		if err := Stream(second, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "cachetags:", err)
			os.Exit(1)
		}
	}

	if len(tags.Refused) > 0 {
		os.Exit(1)
	}
}

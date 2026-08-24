// Command decomment strips comments from a document without stripping the things
// that only look like comments.
//
//	decomment < page.html > out.html
//	decomment -keep-conditional=false -report < page.html
//
// An HTML parser calls several malformed constructs comments, and a handler sees
// them all. <?php echo $x; ?> is a comment. So is <?xml version="1.0"?>, and so
// is <!doctype-ish junk>. A rewrite that removes every comment removes those too,
// silently, because each of them is a perfectly well-formed comment as far as the
// parser is concerned.
//
// So this keeps, by default:
//
//   - processing instructions and template blocks, recognised by their text
//     beginning with "?", the character that opened them
//   - every other bogus comment, which cannot be recognised from the comment at
//     all: "<!x>" and "<!--x-->" both have the text "x". The source range is the
//     only discriminator, which is why this buffers the input rather than
//     streaming it - a real comment starts with "<!--" and a bogus one does not
//   - conditional comments, both halves: the downlevel-revealed form is two
//     comments with real markup between them, and only the first contains "[if",
//     so a filter keyed on that keeps the opening half and drops the closing one
//   - anything matching -keep, for the comments a build system reads
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	keepConditional := flag.Bool("keep-conditional", true, "keep conditional comments, both halves")
	keepCode := flag.Bool("keep-code", true, "keep processing instructions and other bogus comments")
	keep := flag.String("keep", "", "comma-separated substrings whose comments are kept")
	report := flag.Bool("report", false, "list what was kept and removed on stderr")
	flag.Parse()

	s := &stripper{
		keepConditional: *keepConditional,
		keepCode:        *keepCode,
		verbose:         *report,
	}
	for _, k := range strings.Split(*keep, ",") {
		if k = strings.TrimSpace(k); k != "" {
			s.keep = append(s.keep, k)
		}
	}

	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decomment:", err)
		os.Exit(1)
	}
	if err := s.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "decomment:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, s.report())
}

type stripper struct {
	keepConditional bool
	keepCode        bool
	keep            []string
	verbose         bool

	src     []byte
	removed int
	kept    map[string]int
	samples []string
}

// run buffers rather than streams, so that a comment's source range can be
// consulted: nothing about a comment says whether it was written "<!--x-->" or
// "<!x>", and the difference decides whether removing it removes markup or code.
func (s *stripper) run(in []byte, dst io.Writer) error {
	s.src = in
	w, err := lolhtml.NewWriter(dst, s.options()...)
	if err != nil {
		return err
	}
	if _, err := w.Write(in); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// realComment reports whether the construct at loc was written as an HTML
// comment rather than as one of the malformed forms the parser calls a comment.
func (s *stripper) realComment(loc lolhtml.SourceLocation) bool {
	if loc.Start < 0 || loc.End > len(s.src) || loc.Start > loc.End {
		return true // cannot tell; treat it as prose rather than guess
	}
	return bytes.HasPrefix(s.src[loc.Start:loc.End], []byte("<!--"))
}

func (s *stripper) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			text := c.Text()
			if reason := s.reasonToKeep(text, c.SourceLocation()); reason != "" {
				s.note(reason, text)
				return nil
			}
			s.removed++
			if s.verbose && len(s.samples) < 20 {
				s.samples = append(s.samples, truncate(text))
			}
			c.Remove()
			return nil
		}),
	}
}

// reasonToKeep names why a comment survives, or "" if it does not. Returning the
// reason rather than a bool is what makes the report worth reading.
func (s *stripper) reasonToKeep(text string, loc lolhtml.SourceLocation) string {
	if s.keepCode {
		// A "<?" construct keeps its "?" in the text, so it is recognisable
		// without the source.
		if strings.HasPrefix(text, "?") {
			return "processing instruction"
		}
		// Everything else needs the source: "<!x>" and "<!--x-->" have the same
		// text, and only one of them is prose.
		if !s.realComment(loc) {
			return "bogus comment"
		}
	}

	if s.keepConditional && isConditional(text) {
		return "conditional comment"
	}

	for _, k := range s.keep {
		if strings.Contains(text, k) {
			return "matches -keep " + k
		}
	}
	return ""
}

// isConditional recognises both halves. The opening half of a downlevel-revealed
// conditional contains "[if"; the closing half contains only "[endif]", and
// dropping it leaves a conditional that never closes.
func isConditional(text string) bool {
	l := strings.ToLower(text)
	return strings.Contains(l, "[if ") || strings.Contains(l, "[if]") ||
		strings.Contains(l, "[endif]")
}

func (s *stripper) note(reason, text string) {
	if s.kept == nil {
		s.kept = map[string]int{}
	}
	s.kept[reason]++
}

func (s *stripper) report() string {
	reasons := make([]string, 0, len(s.kept))
	total := 0
	for r, n := range s.kept {
		reasons = append(reasons, fmt.Sprintf("%s=%d", r, n))
		total += n
	}
	sort.Strings(reasons)

	var sb strings.Builder
	fmt.Fprintf(&sb, "comments removed=%d kept=%d", s.removed, total)
	if len(reasons) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(reasons, " "))
	}
	sb.WriteString("\n")
	for _, x := range s.samples {
		fmt.Fprintf(&sb, "removed: %s\n", x)
	}
	return sb.String()
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 60 {
		return s
	}
	return s[:60] + "..."
}

func stripString(in string, opts ...func(*stripper)) (string, *stripper, error) {
	s := &stripper{keepConditional: true, keepCode: true}
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run([]byte(in), &out)
	return out.String(), s, err
}

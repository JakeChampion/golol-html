// Command article finds a page's article body by scoring elements as it streams past them, and
// then emits that element's subtree and nothing else.
//
//	$ article < page.html
//	<div class="post-body"><p>The first paragraph…</p>…</div>
//
//	scored 214 elements; the winner is <div class="post-body"> at bytes 4218-18902
//	  text            8214 characters
//	  link text        412 characters in 9 links
//	  paragraphs        14
//	  score           7390
//	  runner-up       <div id="content"> with 3120
//
// # Why this cannot be one pass
//
// The winner is not known until the document ends, and a rewrite cannot write to a position it
// has already passed. So the first pass scores and the second emits: two passes, which the
// library's documentation prices and which is unavoidable here rather than a convenience.
//
// What makes the second pass cheap is that the winner can be named by where it was.
// [lolhtml.SourceLocation] offsets are absolute and unaffected by how the document was written
// in - one byte at a time gives the same numbers as one call - so the byte range from the first
// pass identifies the element in the second, whatever the second pass's read sizes are. Nothing
// has to be buffered but the scores.
//
// # The scoring
//
// Text that is not inside a link, minus twice the text that is, plus a bonus per paragraph. That
// is the shape of every readability heuristic and it is not the interesting part; what the
// library decides is how the counting has to be written:
//
// A text chunk does not know what element it is in, so the count is kept per open element and
// added to all of them - a paragraph's text is its own and its container's, which is what makes a
// container score above its children.
//
// A text chunk cannot tell that it is inside a link either, so the depth of open anchors is
// counted. There is no selector for "not inside an <a>".
//
// An element's score is complete only at its end tag, so [lolhtml.Element.OnEndTag] is where a
// candidate is recorded - and an element whose end tag is missing never records one, which is why
// the report says how many elements were skipped for that reason rather than pretending the
// document was tidy.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Candidate is one element's score.
type Candidate struct {
	Tag      string
	ID       string
	Class    string
	Location lolhtml.SourceLocation

	Text       int
	LinkText   int
	Links      int
	Paragraphs int
}

// Score is the heuristic: text that is not in a link, less twice the text that is, plus a bonus
// per paragraph. Negative scores are floored at zero so that a navigation block full of links
// does not sort below an empty element in a way that depends on how many links it had.
func (c Candidate) Score() int {
	score := c.Text - 2*c.LinkText + 25*c.Paragraphs
	score += bonusFor(c.Tag, c.ID, c.Class)
	if score < 0 {
		return 0
	}
	return score
}

// Describe names a candidate the way a reader would.
func (c Candidate) Describe() string {
	var b strings.Builder
	b.WriteString("<" + c.Tag)
	if c.ID != "" {
		b.WriteString(` id="` + c.ID + `"`)
	}
	if c.Class != "" {
		b.WriteString(` class="` + c.Class + `"`)
	}
	b.WriteString(">")
	return b.String()
}

// bonusFor is the part of the heuristic that reads names. It is small on purpose: a name is a
// hint and the text is the evidence.
func bonusFor(tag, id, class string) int {
	names := strings.ToLower(id + " " + class)
	bonus := 0
	switch tag {
	case "article", "main":
		bonus += 400
	case "nav", "footer", "aside", "header":
		bonus -= 400
	}
	for _, good := range []string{"article", "post", "content", "entry", "story", "body"} {
		if strings.Contains(names, good) {
			bonus += 150
			break
		}
	}
	for _, bad := range []string{"nav", "menu", "sidebar", "comment", "footer", "promo", "share"} {
		if strings.Contains(names, bad) {
			bonus -= 300
			break
		}
	}
	return bonus
}

// Scores is what the first pass learned.
type Scores struct {
	Candidates []Candidate
	// Skipped counts elements whose end tag never arrived, so whose score was never
	// complete. An implied end tag is ordinary HTML, so this is a fact about the page
	// rather than a failure - but a page where it is large is a page this program should be
	// trusted less about.
	Skipped int
	// Elements is how many elements were seen at all.
	Elements int
}

// Best returns the highest-scoring candidate and the runner-up. A page with no candidate at all -
// no element with an end tag - returns two zero values, and the caller decides what to do.
func (s Scores) Best() (best, runnerUp Candidate) {
	ranked := make([]Candidate, len(s.Candidates))
	copy(ranked, s.Candidates)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score() != ranked[j].Score() {
			return ranked[i].Score() > ranked[j].Score()
		}
		// A tie goes to the earlier element, which is the one a reader meets first.
		return ranked[i].Location.Start < ranked[j].Location.Start
	})
	if len(ranked) > 0 {
		best = ranked[0]
	}
	if len(ranked) > 1 {
		runnerUp = ranked[1]
	}
	return best, runnerUp
}

// containers are the elements worth scoring. Scoring every element would put a <b> in the running
// and cost a candidate per word.
var containers = map[string]bool{
	"article": true, "main": true, "section": true, "div": true, "td": true,
	"blockquote": true, "aside": true, "nav": true, "footer": true, "header": true,
	"body": true,
}

// ScorePass reads a document and scores its containers. Nothing is written: this is the pass that
// only looks.
func ScorePass(r io.Reader) (Scores, error) {
	var scores Scores

	// open is the stack of containers being scored, so a paragraph's text can be added to
	// every container it is inside - which is what makes a container outscore its children.
	type frame struct {
		candidate *Candidate
	}
	var open []frame
	anchors := 0
	// node accumulates the text of the node being read, because a count taken per chunk is a
	// count of how the document was written rather than of what it says.
	var node strings.Builder

	handlers := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			scores.Elements++
			tag := e.TagName()

			if tag == "a" && e.CanHaveContent() {
				anchors++
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					anchors--
					return nil
				})
			}
			if tag == "p" {
				for _, f := range open {
					f.candidate.Paragraphs++
				}
				return nil
			}
			if !containers[tag] || !e.CanHaveContent() {
				return nil
			}

			id, _ := e.Attribute("id")
			class, _ := e.Attribute("class")
			c := &Candidate{
				Tag: tag, ID: id, Class: class,
				Location: e.SourceLocation(),
			}
			open = append(open, frame{candidate: c})
			depth := len(open)
			scores.Skipped++ // provisional: cleared when the end tag arrives

			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				scores.Skipped--
				// The end tag may close more than this element - an implied end tag
				// is a token, not a promise about which element it belongs to - so
				// the stack is unwound to this depth rather than popped once.
				if len(open) >= depth {
					open = open[:depth-1]
				}
				// The candidate's range runs to the end of its end tag, which is what
				// the second pass slices.
				c.Location.End = end.SourceLocation().End
				scores.Candidates = append(scores.Candidates, *c)
				return nil
			})
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			// The node is accumulated rather than counted per chunk. A chunk boundary
			// falls wherever the writes and the tokenizer put it, so a per-chunk count
			// is a count of the caller's read sizes: measured, the same page scored 128
			// characters read one byte at a time and 155 read whole, because every
			// chunk lost its own leading and trailing space to the trim.
			node.WriteString(c.Text())
			if !c.IsLastInTextNode() {
				return nil
			}
			text := node.String()
			node.Reset()

			n := len(strings.TrimSpace(text))
			if n == 0 {
				return nil
			}
			// A chunk does not know its element, so the counting is done against the
			// stack the element handlers maintain.
			for _, f := range open {
				if anchors > 0 {
					f.candidate.LinkText += n
					continue
				}
				f.candidate.Text += n
			}
			return nil
		}),
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			for _, f := range open {
				f.candidate.Links++
			}
			return nil
		}),
	}

	w, err := lolhtml.NewWriter(io.Discard, handlers...)
	if err != nil {
		return scores, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return scores, err
	}
	if err := w.Close(); err != nil {
		return scores, err
	}
	return scores, nil
}

// ExtractPass writes the winner's subtree to w and nothing else.
//
// The winner is named by its byte range, which the first pass measured. Offsets are absolute and
// do not depend on how the document is written in, so this pass can be fed in any chunks and
// still find the same element.
func ExtractPass(r io.Reader, w io.Writer, winner Candidate) error {
	inside := 0
	found := false

	handlers := []lolhtml.Option{
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if inside > 0 {
				// Inside the winner: keep it as it is.
				return nil
			}
			if e.SourceLocation().Start == winner.Location.Start && e.TagName() == winner.Tag {
				found = true
				inside++
				if e.CanHaveContent() {
					return e.OnEndTag(func(*lolhtml.EndTag) error {
						inside--
						return nil
					})
				}
				return nil
			}
			// Outside: the element goes, but its content is kept only if it might
			// contain the winner - which it does when it starts before the winner and
			// ends after it. RemoveAndKeepContent is the removal that keeps the door
			// open; Remove would take the winner with it.
			e.RemoveAndKeepContent()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if inside == 0 {
				c.Remove()
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if inside == 0 {
				c.Remove()
			}
			return nil
		}),
		// The doctype is outside the winner by definition - it comes before every
		// element - and a fragment does not carry one. Removing it is the only thing
		// available: a Doctype has no Replace, because lol-html offers none.
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			d.Remove()
			return nil
		}),
	}

	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("article: the winning element at byte %d was not found in the second pass",
			winner.Location.Start)
	}
	return nil
}

// Extract scores a document and writes the winner's subtree. The document is read twice, which is
// why it is a string rather than a reader: the caller has it, and pretending otherwise would only
// move the buffering.
func Extract(doc string, w io.Writer) (Scores, Candidate, error) {
	scores, err := ScorePass(strings.NewReader(doc))
	if err != nil {
		return scores, Candidate{}, err
	}
	best, _ := scores.Best()
	if len(scores.Candidates) == 0 {
		return scores, best, fmt.Errorf("article: nothing to score")
	}
	if err := ExtractPass(strings.NewReader(doc), w, best); err != nil {
		return scores, best, err
	}
	return scores, best, nil
}

// Report describes what the scoring decided.
func Report(scores Scores, best, runnerUp Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scored %d elements; the winner is %s at bytes %d-%d\n",
		scores.Elements, best.Describe(), best.Location.Start, best.Location.End)
	fmt.Fprintf(&b, "  %-14s %d characters\n", "text", best.Text)
	fmt.Fprintf(&b, "  %-14s %d characters in %d links\n", "link text", best.LinkText, best.Links)
	fmt.Fprintf(&b, "  %-14s %d\n", "paragraphs", best.Paragraphs)
	fmt.Fprintf(&b, "  %-14s %d\n", "score", best.Score())
	if runnerUp.Tag != "" {
		fmt.Fprintf(&b, "  %-14s %s with %d\n", "runner-up", runnerUp.Describe(), runnerUp.Score())
	}
	if scores.Skipped > 0 {
		fmt.Fprintf(&b, "  %-14s %d containers whose end tag never arrived, so never scored\n",
			"skipped", scores.Skipped)
	}
	return b.String()
}

func main() {
	quiet := flag.Bool("quiet", false, "do not print the report on stderr")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "article:", err)
		os.Exit(2)
	}

	var out strings.Builder
	scores, best, err := Extract(string(doc), &out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "article:", err)
		os.Exit(1)
	}
	os.Stdout.WriteString(out.String())
	if !*quiet {
		_, runnerUp := scores.Best()
		fmt.Fprint(os.Stderr, "\n"+Report(scores, best, runnerUp))
	}
}

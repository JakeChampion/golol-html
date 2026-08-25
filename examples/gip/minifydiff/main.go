// Command minifydiff minifies a document conservatively and then proves it did
// no harm, by parsing the input and the output and diffing what a parser sees.
//
// A minifier is a program whose bugs are invisible. It removes bytes nobody was
// looking at, the page still renders, and the one that mattered is discovered by
// a customer three releases later. So this one is two programs: a small set of
// edits, and a checker that has to agree the edits changed nothing. If the
// checker disagrees the input is written out unchanged, because a document that is
// 4% larger is not a bug and a document that means something else is.
//
// The edits are two.
//
// Runs of whitespace outside the elements where whitespace is significant become
// a single space. Never no space: whether the remaining one is visible depends on
// whether the surrounding elements are inline, which is a CSS question this
// program cannot answer.
//
// Comments the document spelled as comments are removed. That qualification is
// the whole of it - an HTML parser reports four different pieces of syntax as a
// comment token, and two of them are not comments to whatever reads the document
// next:
//
//	<!--a-->          a comment                      remove
//	<!bogus>          a bogus comment                keep
//	<?php echo 1; ?>  a processing instruction       keep
//	<![CDATA[x]]>     a CDATA section                keep
//
// The delimiters are gone by the time a handler sees the token, and the text does
// not say which it was: a comment containing "?php x ?" reads exactly like the
// processing instruction. What does say is the arithmetic on
// [lolhtml.Comment.SourceLocation] - the source range less the text is the
// delimiters, and 7 is "<!--" and "-->" - which needs no copy of the input and so
// works in a stream. Conditional comments and licence banners are kept on top of
// that, by their text.
//
// The checker is the interesting half. It parses a document into two projections
// and compares them.
//
// The first is structure and text: start tags with their attributes sorted, end
// tags, the doctype, and the text between them with whitespace normalised outside
// the significant elements and left exactly as it is inside them. Comments are
// not in this projection at all, and text runs are joined across them - removing
// a comment merges the text on both sides of it, and that is not a difference.
//
// The second is the comments, in order, each with the length of the delimiters
// the document used. The output's list has to be a subsequence of the input's, and
// every comment missing from the output has to have had a delimiter length of 7.
// That allowance is deliberately narrower than the rule the minifier applies: the
// checker does not re-check "is this a licence banner", because keeping a banner
// is a policy and turning a PHP block into nothing is a change of meaning. A
// checker that shares a rule with the thing it is checking is checking that it is
// consistent, which is not the same as being right.
//
// The checker's allowance is deliberately wider than what the minifier does. It
// would accept whitespace between two tags being deleted outright, because a
// parser keeps no text node for it; the minifier leaves one space there anyway,
// because whether that space is visible depends on CSS. A checker that permits
// exactly what its minifier does has stopped being a second opinion.
//
// What that combination catches, measured in the tests rather than argued: a
// whitespace edit inside a <pre> or a <script>, a comment token that was not a
// comment, a dropped tag, a mangled attribute value, a text node that lost a
// character. What it does not catch is anything a parser does not see, which is
// the honest limit: two documents with the same projections can still differ in
// what a browser lays out if CSS is involved, and that is why the whitespace rule
// is the conservative one.
//
// The cost is that this is not a streaming program. The checker needs the whole
// output before it can approve any of it, so the document is buffered and read
// three times: once to minify, once to project the input, once to project the
// output. That is a build-time or a test-time tool, not something to put in front
// of a request - and it is what a proxy that wants to minify should run over its
// corpus first.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Verbatim are the elements this stays out of, on top of everything
// lolhtml.IsRawText reports.
var Verbatim = map[string]bool{"pre": true, "listing": true}

// commentDelimiters is the length a comment token's delimiters have when the
// document spelled it "<!--" and "-->". Anything else was spelled some other way.
const commentDelimiters = 7

// A Result is what the run did.
type Result struct {
	BytesIn, BytesOut int
	// Runs is the whitespace runs that came out shorter.
	Runs int
	// Comments removed, and CommentsKept for a reason worth reporting.
	Comments, CommentsKept int
	// Tokens is how much the checker compared, so a caller can see the check was
	// not vacuous.
	Tokens int
	// Rejected says the checker disagreed and the input was written instead.
	Rejected bool
	// Why the checker disagreed.
	Difference string
}

func (r Result) String() string {
	s := fmt.Sprintf("minifydiff: %d -> %d bytes (%d saved), %d runs, %d comments removed, %d kept, %d tokens verified",
		r.BytesIn, r.BytesOut, r.BytesIn-r.BytesOut, r.Runs, r.Comments, r.CommentsKept, r.Tokens)
	if r.Rejected {
		s += "\nminifydiff: REJECTED, wrote the input unchanged: " + r.Difference
	}
	return s
}

// Options turn off the care this program takes, so the checker has something to
// catch. Nothing sets them but the tests and the -unsafe flag.
type Options struct {
	// CollapseEverywhere ignores pre, textarea and the raw-text elements.
	CollapseEverywhere bool
	// StripEveryCommentToken removes processing instructions and CDATA sections
	// along with the comments.
	StripEveryCommentToken bool
}

// Run minifies src, checks the result, and writes whichever of the two it can
// stand behind.
func Run(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	in, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	doc := string(in)

	out, res, err := Minify(doc, opts)
	if err != nil {
		return res, err
	}
	res.BytesIn, res.BytesOut = len(doc), len(out)

	before, err := Project(doc)
	if err != nil {
		return res, err
	}
	after, err := Project(out)
	if err != nil {
		return res, err
	}
	res.Tokens = len(before.Tokens) + len(before.Comments)

	if diff := Diff(before, after); diff != "" {
		res.Rejected, res.Difference = true, diff
		res.BytesOut = len(doc)
		_, err = io.WriteString(dst, doc)
		return res, err
	}
	_, err = io.WriteString(dst, out)
	return res, err
}

// Minify applies the edits. It is a single streaming pass; it is the checking
// that is not.
func Minify(doc string, opts Options) (string, Result, error) {
	m := &minifier{opts: opts}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnElement("*", m.element),
		lolhtml.OnDocumentText(m.text),
		lolhtml.OnDocumentComment(m.comment),
	)
	if err != nil {
		return "", m.res, err
	}
	if _, err := io.WriteString(w, doc); err != nil {
		w.Close()
		return "", m.res, err
	}
	if err := w.Close(); err != nil {
		return "", m.res, err
	}
	return out.String(), m.res, nil
}

type minifier struct {
	opts      Options
	res       Result
	depth     int
	lastSpace bool
}

func (m *minifier) element(e *lolhtml.Element) error {
	name := e.TagName()
	if m.opts.CollapseEverywhere || (!Verbatim[name] && !lolhtml.IsRawText(name)) {
		return nil
	}
	m.depth++
	if !e.CanHaveContent() {
		return nil // a plaintext runs to the end of the input
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		m.depth--
		m.lastSpace = false
		return nil
	})
}

func (m *minifier) text(t *lolhtml.TextChunk) error {
	s := t.Text()
	if s == "" {
		return nil
	}
	if m.depth > 0 {
		m.lastSpace = false
		return nil
	}
	out, runs := m.collapse(s)
	m.res.Runs += runs
	if out == s {
		return nil
	}
	if out == "" {
		t.Remove()
		return nil
	}
	// As markup, not as text: the chunk is the document's own spelling and
	// inserting it as text would escape its ampersands a second time.
	return t.Replace(out, lolhtml.HTML)
}

func (m *minifier) comment(c *lolhtml.Comment) error {
	if !m.opts.StripEveryCommentToken && !removable(c) {
		m.res.CommentsKept++
		return nil
	}
	m.res.Comments++
	c.Remove()
	return nil
}

// removable reports whether a comment token is a comment the document wrote as
// one and did not mean to keep.
func removable(c *lolhtml.Comment) bool {
	if delimiterLength(c) != commentDelimiters {
		// A bogus comment, a processing instruction or a CDATA section. Whatever
		// reads this document next may well be looking for it.
		return false
	}
	text := c.Text()
	switch {
	case strings.HasPrefix(text, "!"):
		return false // the convention for "keep this", used for licences
	case strings.Contains(text, "[if"), strings.Contains(text, "[endif"):
		return false // a conditional comment, whose halves are markup
	}
	return true
}

// delimiterLength is the source the token occupied less its text, which is what
// the document spelled around it. Comment text is raw source, so this is exact
// rather than an estimate.
func delimiterLength(c *lolhtml.Comment) int {
	loc := c.SourceLocation()
	return (loc.End - loc.Start) - len(c.Text())
}

func (m *minifier) collapse(s string) (string, int) {
	var b strings.Builder
	runs := 0
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			b.WriteByte(s[i])
			m.lastSpace = false
			continue
		}
		j := i
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		switch {
		case m.lastSpace:
			runs++
		case s[i:j] == " ":
			b.WriteByte(' ')
			m.lastSpace = true
		default:
			b.WriteByte(' ')
			m.lastSpace = true
			runs++
		}
		i = j - 1
	}
	return b.String(), runs
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// A Comment in a projection is a token's text and the length of the delimiters
// the document wrote around it.
type Comment struct {
	Text  string
	Delim int
}

// A Projection is what a parser sees, in the two parts that are compared
// separately.
type Projection struct {
	// Tokens are the structure and the text: start tags with sorted attributes,
	// end tags, the doctype, and text runs. Comments are not here, and a text run
	// continues across one.
	Tokens []string
	// Comments are the comment tokens in order.
	Comments []Comment
}

// Project parses a document into what a parser sees.
func Project(doc string) (Projection, error) {
	p := &projector{}
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", p.element),
		lolhtml.OnDocumentText(p.text),
		lolhtml.OnDocumentComment(p.comment),
		lolhtml.OnDoctype(p.doctype),
	); err != nil {
		return p.out, err
	}
	p.flush()
	return p.out, nil
}

type projector struct {
	out   Projection
	run   strings.Builder
	depth int
	// verbatim says the run being accumulated is inside an element where
	// whitespace is significant, so it is compared exactly.
	verbatim bool
}

func (p *projector) element(e *lolhtml.Element) error {
	name := e.TagName()
	p.flush()

	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(name)
	attrs := e.AttributeList()
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name < attrs[j].Name })
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%q", a.Name, a.Value)
	}
	if e.IsSelfClosing() {
		b.WriteByte('/')
	}
	b.WriteByte('>')
	p.out.Tokens = append(p.out.Tokens, b.String())

	if Verbatim[name] || lolhtml.IsRawText(name) {
		p.depth++
		p.verbatim = true
		if !e.CanHaveContent() {
			return nil
		}
	}
	if !e.CanHaveContent() {
		return nil
	}
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		if t.Name() != name {
			// Not this element's end tag: it belongs to an enclosing element,
			// which records it itself.
			return nil
		}
		p.flush()
		if Verbatim[name] || lolhtml.IsRawText(name) {
			p.depth--
			p.verbatim = p.depth > 0
		}
		p.out.Tokens = append(p.out.Tokens, "</"+name+">")
		return nil
	})
}

func (p *projector) text(t *lolhtml.TextChunk) error {
	p.run.WriteString(t.Text())
	return nil
}

// comment records the token and does not end the text run: removing a comment
// joins the text on both sides of it, and a projection that ended the run here
// would call that a difference.
func (p *projector) comment(c *lolhtml.Comment) error {
	p.out.Comments = append(p.out.Comments, Comment{c.Text(), delimiterLength(c)})
	return nil
}

func (p *projector) doctype(d *lolhtml.Doctype) error {
	p.flush()
	name, _ := d.Name()
	public, _ := d.PublicID()
	system, _ := d.SystemID()
	p.out.Tokens = append(p.out.Tokens, fmt.Sprintf("<!doctype %q %q %q>", name, public, system))
	return nil
}

func (p *projector) flush() {
	s := p.run.String()
	p.run.Reset()
	if s == "" {
		return
	}
	if !p.verbatim {
		s = strings.Join(strings.Fields(s), " ")
		if s == "" {
			return // whitespace between tags, which is not text a parser keeps
		}
	}
	p.out.Tokens = append(p.out.Tokens, "#text "+s)
}

// Diff compares two projections and says what the first difference is, or "" if
// there is none the minifier is allowed to make.
func Diff(before, after Projection) string {
	for i := range before.Tokens {
		if i >= len(after.Tokens) {
			return fmt.Sprintf("the output ends after %d tokens; the input had %s at %d",
				len(after.Tokens), before.Tokens[i], i)
		}
		if before.Tokens[i] != after.Tokens[i] {
			return fmt.Sprintf("token %d: input has %s, output has %s",
				i, before.Tokens[i], after.Tokens[i])
		}
	}
	if len(after.Tokens) > len(before.Tokens) {
		return fmt.Sprintf("the output has %s at %d and the input ends there",
			after.Tokens[len(before.Tokens)], len(before.Tokens))
	}

	// The comments the output kept have to be the ones the input had, in order,
	// and anything missing has to have been spelled as a comment.
	j := 0
	for _, c := range before.Comments {
		if j < len(after.Comments) && after.Comments[j] == c {
			j++
			continue
		}
		if c.Delim != commentDelimiters {
			return fmt.Sprintf("a comment token with %d bytes of delimiters was removed: %q - "+
				"the document did not spell that as a comment", c.Delim, c.Text)
		}
	}
	if j != len(after.Comments) {
		return fmt.Sprintf("the output has a comment the input did not: %q", after.Comments[j].Text)
	}
	return ""
}

func main() {
	var opts Options
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-unsafe":
			opts.CollapseEverywhere, opts.StripEveryCommentToken = true, true
		default:
			fmt.Fprintln(os.Stderr, "usage: minifydiff [-unsafe] < in > out")
			os.Exit(2)
		}
	}
	res, err := Run(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "minifydiff:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
	if res.Rejected {
		os.Exit(1)
	}
}

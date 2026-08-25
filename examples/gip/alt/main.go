// Command alt reports images with no alt attribute, and images whose alt text
// says nothing a screen reader could not already say.
//
//	page.html:12:3: <img src="logo.png"> has no alt attribute
//	page.html:31:5: alt="logo.png" repeats the file name
//	page.html:48:7: alt="Read the docs" repeats the text of the link it is in
//
// Three things are worth more than the rule.
//
// A missing alt and an empty alt are opposites, and a linter that treats them the
// same is worse than no linter. alt="" says "this image is decoration, skip it",
// which is correct and is the only way to say it; no alt at all says nothing, and
// a screen reader falls back to announcing the file name or the URL. So the
// finding is about absence, and an empty alt is a document doing the right thing.
// The near-miss is alt=" ", which most screen readers treat as empty and which no
// guidance recommends: reported, with the suggestion to write alt="" and mean it.
//
// A report has no ordering constraint. Comparing an image's alt against the text
// of the link it is inside needs text that arrives after the image; so does
// comparing it against a figcaption, and so does resolving an aria-labelledby to
// the element it names. In a rewrite that would mean two passes, because an
// attribute has to be written where the rewriter has already been - which is what
// examples/gip/lang and examples/gip/dir pay for. Here nothing is written, so the
// evidence can be gathered from anywhere in the document and the findings decided
// at the end. The ordering constraint is about insertion, not about knowledge.
//
// An image can get its name from four places, and this program has to know all of
// them before it can call one missing: alt, aria-label, aria-labelledby, and
// role="presentation" or role="none", which says the image is decoration as
// clearly as alt="" does. A title is not one of them - support for it is poor
// enough that guidance says not to rely on it - so an image with only a title is
// reported as unnamed, and the message says the title is there.
//
// What it reports on: img, input type="image" and area, all three of which need a
// name for the same reason. What it cannot see: whether an image is decorative in
// fact. A page whose alt text is wrong in a way that reads correctly is a page
// only a person can find.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what is wrong with an image.
type Kind string

const (
	Missing     Kind = "missing"
	Whitespace  Kind = "whitespace"
	FileName    Kind = "filename"
	Prefix      Kind = "prefix"
	LinkText    Kind = "link-text"
	Caption     Kind = "caption"
	MissingName Kind = "labelledby"
)

// Prefixes are the openings that say what a screen reader already says: it
// announces "image" itself, so the alt text does not have to.
var Prefixes = []string{
	"image of", "picture of", "photo of", "photograph of", "graphic of",
	"icon of", "logo of", "screenshot of", "an image of", "a picture of",
	"a photo of", "image:", "photo:",
}

// A Finding is one thing worth reporting.
type Finding struct {
	Kind         Kind
	At           int
	Line, Column int
	Message      string
}

func (f Finding) String() string {
	return fmt.Sprintf("%d:%d: %s", f.Line, f.Column, f.Message)
}

// A Result is the report.
type Result struct {
	Findings []Finding
	// Images seen, and Decorative ones - an empty alt or a presentation role -
	// which are documents doing the right thing.
	Images, Decorative int
}

// OK reports whether there was nothing to say.
func (r Result) OK() bool { return len(r.Findings) == 0 }

func (r Result) String() string {
	return fmt.Sprintf("alt: %d images, %d decorative; %d findings",
		r.Images, r.Decorative, len(r.Findings))
}

// an image and everything about it that a finding might need.
type image struct {
	at  int
	tag string
	src string
	// alt is the attribute's value, and hasAlt whether it was there at all: the
	// difference between the two is the whole of the first rule.
	alt    string
	hasAlt bool
	// label, labelledBy and role are the other places a name can come from.
	label      string
	labelledBy string
	role       string
	title      string
	// linkText and caption are what the enclosing link and figure said, which is
	// evidence that arrives after the image.
	linkText string
	caption  string
	// inLink and inFigure are the offsets of those enclosing elements, used to
	// attach their text once it has all arrived.
	inLink, inFigure int
}

// Check reads doc and reports on its images. Nothing is written: the document goes
// to io.Discard, because a text handler is registered and the report is the output.
func Check(doc []byte) (Result, error) {
	c := &checker{ids: map[string]bool{}, linkText: map[int]*strings.Builder{},
		captions: map[int]*strings.Builder{}}
	w, err := lolhtml.NewWriter(io.Discard, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.report(doc), nil
}

type checker struct {
	res    Result
	images []*image
	// ids is every id in the document, so an aria-labelledby can be resolved -
	// which can only be done once the whole document has been seen.
	ids map[string]bool
	// links and figures are the open enclosing elements, innermost last.
	links, figures []int
	// linkText and captions accumulate by the enclosing element's offset.
	linkText, captions map[int]*strings.Builder
	// inCaption is how many figcaptions this position is inside, since a figure's
	// own text is not its caption.
	inCaption int
}

func (c *checker) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a[href]", c.link),
		lolhtml.OnElement("figure", c.figure),
		lolhtml.OnElement("figcaption", c.figcaption),
		lolhtml.OnElement("img,area,input[type=image]", c.image),
		lolhtml.OnElement("[id]", c.id),
		lolhtml.OnDocumentText(c.text),
	}
}

func (c *checker) id(e *lolhtml.Element) error {
	if v, ok := e.Attribute("id"); ok && v != "" {
		c.ids[v] = true
	}
	return nil
}

func (c *checker) link(e *lolhtml.Element) error {
	at := e.SourceLocation().Start
	c.links = append(c.links, at)
	c.linkText[at] = &strings.Builder{}
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		c.links = pop(c.links, at)
		return nil
	})
}

func (c *checker) figure(e *lolhtml.Element) error {
	at := e.SourceLocation().Start
	c.figures = append(c.figures, at)
	c.captions[at] = &strings.Builder{}
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		c.figures = pop(c.figures, at)
		return nil
	})
}

// figcaption marks that the text inside it belongs to the enclosing figure's
// caption rather than to the figure's ordinary content.
func (c *checker) figcaption(e *lolhtml.Element) error {
	if len(c.figures) == 0 {
		return nil
	}
	c.inCaption++
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		c.inCaption--
		return nil
	})
}

func (c *checker) image(e *lolhtml.Element) error {
	c.res.Images++
	img := &image{at: e.SourceLocation().Start, tag: e.TagName(), inLink: -1, inFigure: -1}
	img.src, _ = e.Attribute("src")
	img.alt, img.hasAlt = e.Attribute("alt")
	img.label, _ = e.Attribute("aria-label")
	img.labelledBy, _ = e.Attribute("aria-labelledby")
	img.role, _ = e.Attribute("role")
	img.title, _ = e.Attribute("title")
	if len(c.links) > 0 {
		img.inLink = c.links[len(c.links)-1]
	}
	if len(c.figures) > 0 {
		img.inFigure = c.figures[len(c.figures)-1]
	}
	c.images = append(c.images, img)
	return nil
}

// text accumulates the text of the enclosing link, and of a figcaption, which is
// the evidence that arrives after the image it is about.
func (c *checker) text(t *lolhtml.TextChunk) error {
	s := t.Text()
	if s == "" {
		return nil
	}
	// Whitespace is kept: a chunk boundary can fall on a space, and dropping
	// whitespace-only chunks would join the words either side of one. Measured -
	// with one-byte writes, "Read the docs" became "Readthedocs" and stopped
	// matching an alt that repeated it.
	if len(c.links) > 0 {
		c.linkText[c.links[len(c.links)-1]].WriteString(s)
	}
	if c.inCaption > 0 && len(c.figures) > 0 {
		c.captions[c.figures[len(c.figures)-1]].WriteString(s)
	}
	return nil
}

func pop(stack []int, at int) []int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == at {
			return append(stack[:i], stack[i+1:]...)
		}
	}
	return stack
}

// report decides everything at the end, because that is when all the evidence is
// in. A rewrite would need a second pass for this; a report does not.
func (c *checker) report(doc []byte) Result {
	lines := newlines(doc)
	add := func(kind Kind, at int, format string, args ...any) {
		line, col := position(lines, doc, at)
		c.res.Findings = append(c.res.Findings, Finding{
			Kind: kind, At: at, Line: line, Column: col,
			Message: fmt.Sprintf(format, args...),
		})
	}

	for _, img := range c.images {
		decorative := strings.EqualFold(strings.TrimSpace(img.role), "presentation") ||
			strings.EqualFold(strings.TrimSpace(img.role), "none")
		if img.hasAlt && img.alt == "" {
			decorative = true
		}
		if decorative {
			c.res.Decorative++
			continue
		}

		named := img.alt != "" || strings.TrimSpace(img.label) != ""
		if img.labelledBy != "" {
			// An aria-labelledby names other elements. Whether they exist is a
			// question about the whole document, which is why this is decided here.
			missing := []string{}
			for _, id := range strings.Fields(img.labelledBy) {
				if !c.ids[id] {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				add(MissingName, img.at, "aria-labelledby names %s, which %s in this document",
					strings.Join(quoted(missing), " and "),
					plural(len(missing), "is not", "are not"))
			} else {
				named = true
			}
		}

		if !named {
			switch {
			case !img.hasAlt && strings.TrimSpace(img.title) != "":
				add(Missing, img.at, "<%s%s> has no alt attribute; its title is not a "+
					"substitute, because support for one is too poor to rely on",
					img.tag, srcNote(img.src))
			case !img.hasAlt:
				add(Missing, img.at, "<%s%s> has no alt attribute, so a screen reader "+
					"announces the file name", img.tag, srcNote(img.src))
			}
			continue
		}

		alt := strings.TrimSpace(img.alt)
		if img.hasAlt && img.alt != "" && alt == "" {
			add(Whitespace, img.at, `alt=%q is whitespace, which reads as decoration; `+
				`write alt="" and mean it`, img.alt)
			continue
		}

		if name := path.Base(img.src); img.src != "" && name != "." && name != "/" {
			if strings.EqualFold(alt, name) || strings.EqualFold(alt, stem(name)) {
				add(FileName, img.at, "alt=%q repeats the file name", img.alt)
			}
		}
		lower := strings.ToLower(alt)
		for _, prefix := range Prefixes {
			if strings.HasPrefix(lower, prefix) {
				add(Prefix, img.at, "alt=%q starts with %q; a screen reader already "+
					"says it is an image", img.alt, prefix)
				break
			}
		}
		if img.inLink >= 0 {
			if text := strings.TrimSpace(c.linkText[img.inLink].String()); text != "" &&
				strings.EqualFold(collapse(text), collapse(alt)) {
				add(LinkText, img.at, "alt=%q repeats the text of the link it is in", img.alt)
			}
		}
		if img.inFigure >= 0 {
			if text := strings.TrimSpace(c.captions[img.inFigure].String()); text != "" &&
				strings.EqualFold(collapse(text), collapse(alt)) {
				add(Caption, img.at, "alt=%q repeats the figure's caption", img.alt)
			}
		}
	}

	sort.SliceStable(c.res.Findings, func(i, j int) bool {
		return c.res.Findings[i].At < c.res.Findings[j].At
	})
	return c.res
}

func srcNote(src string) string {
	if src == "" {
		return ""
	}
	return fmt.Sprintf(" src=%q", src)
}

func stem(name string) string {
	if ext := path.Ext(name); ext != "" {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func quoted(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func newlines(doc []byte) []int {
	var at []int
	for i, b := range doc {
		if b == '\n' {
			at = append(at, i)
		}
	}
	return at
}

func position(lines []int, doc []byte, at int) (line, column int) {
	line = 1
	start := 0
	for _, nl := range lines {
		if nl >= at {
			break
		}
		line++
		start = nl + 1
	}
	if at > len(doc) {
		at = len(doc)
	}
	return line, utf8.RuneCount(doc[start:at]) + 1
}

func main() {
	name := "-"
	var doc []byte
	var err error
	switch len(os.Args) {
	case 1:
		doc, err = io.ReadAll(os.Stdin)
	case 2:
		name = os.Args[1]
		doc, err = os.ReadFile(name)
	default:
		fmt.Fprintln(os.Stderr, "usage: alt [file] < page")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "alt:", err)
		os.Exit(1)
	}
	res, err := Check(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "alt:", err)
		os.Exit(1)
	}
	for _, f := range res.Findings {
		fmt.Printf("%s:%s\n", name, f)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}

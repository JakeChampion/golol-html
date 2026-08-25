// Command scrollwrap puts a scroll container around the elements that overflow a
// narrow screen.
//
//	<table>...</table>  ->  <div class="scroll" tabindex="0"><table>...</table></div>
//
// A wide table or a long line of code makes a whole page scroll sideways, which is
// the thing that makes a site unusable on a phone. The fix is a container with
// overflow-x on it, and the container has to be focusable, because a region that
// scrolls and cannot be reached by keyboard is a new problem in place of the old
// one. The class is the caller's to style; this program only puts it there.
//
// A wrapper is two insertions and the parser decides whether they wrap. Three
// things it decided, all measured in differential/wrap_test.go:
//
//   - A <div> closes an open paragraph. Wrapping something inside a <p> in a div
//     takes it out of the paragraph, orphans the text that followed it and leaves an
//     empty paragraph behind. A <span> does not.
//   - A <span> cannot hold an element that closes a paragraph by starting - a
//     <pre>, or a <table> in standards mode - so the span comes out empty and the
//     element is outside it. There, the div is the right wrapper: it leaves the
//     paragraph with the element, which the element was leaving anyway.
//   - Which bucket a table is in depends on the doctype. Without one the document is
//     in quirks mode, where a table does not close a paragraph, and the answer flips.
//     The doctype arrives before any element, so this program can read it and does.
//
// So inside a paragraph the wrapper is a span or a div depending on the element and
// the doctype, and outside one it is always a div. A wrapper element that is wrong
// for its position does not fail, and nothing in the output looks damaged: it just
// wraps nothing, or moves what it was wrapping.
//
// The closing half is the end-tag rule. </div> goes at the element's own end tag,
// and an element whose end tag the document left out has no position to write at -
// so those are reported rather than wrapped, since a container closed in the wrong
// place is worse than none. See [lolhtml.Element.OnEndTag].
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Wide are the elements that overflow. A caller with a different idea can say so:
// what is wide is a fact about a page's CSS, not about HTML.
const Wide = "table,pre,iframe,svg,canvas,video,object,embed"

// ClosesParagraph are the start tags that end an open <p>. Taken from the
// specification's "in body" rules, cut to the tags that can hold or be wide
// content. A table is here only outside quirks mode, which is why it is not listed.
var ClosesParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "details": true, "dialog": true, "dir": true, "div": true,
	"dl": true, "dd": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "hr": true, "li": true, "listing": true, "main": true,
	"menu": true, "nav": true, "ol": true, "p": true, "plaintext": true,
	"pre": true, "search": true, "section": true, "summary": true, "ul": true,
	"xmp": true,
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Selector chooses what counts as wide.
	Selector string
	// Class goes on the wrapper, for the caller's stylesheet to find.
	Class string
	// Label, when set, names the region for a screen reader. Without a name a
	// role="region" is worse than no role, so the role goes on only with it.
	Label string
}

// Result is what happened.
type Result struct {
	Divs      int // wrappers that are div elements
	Spans     int // wrappers that are spans, because a div would have split a paragraph
	Displaced int // elements whose end tag is not theirs: nowhere to close the wrapper
	Unclosed  int // elements nothing closes at all
	Wrapped   int // elements already inside a wrapper of this class
}

func (r Result) String() string {
	return fmt.Sprintf("scrollwrap: wrapped %d in a div, %d in a span; %d already wrapped, %d displaced, %d unclosed",
		r.Divs, r.Spans, r.Wrapped, r.Displaced, r.Unclosed)
}

// OK reports whether every wide element got a container.
func (r Result) OK() bool { return r.Displaced+r.Unclosed == 0 }

type wrapper struct {
	opts    Options
	res     Result
	quirks  bool // no doctype: a table does not close a paragraph
	inP     bool // a paragraph is open
	inWrap  int  // depth inside a container this program already put there
	pending int  // wide elements whose end tag has not arrived
}

// doctype decides the table question for the whole document, which is why it is
// read rather than assumed. No doctype at all is quirks mode, and so is a doctype
// with a public or system identifier that is not the HTML5 one - this program takes
// the conservative half of that rule: only a bare <!DOCTYPE html> means standards.
func (w *wrapper) doctype(d *lolhtml.Doctype) error {
	name, ok := d.Name()
	if !ok || !strings.EqualFold(name, "html") {
		return nil
	}
	if _, ok := d.PublicID(); ok {
		return nil
	}
	if _, ok := d.SystemID(); ok {
		return nil
	}
	w.quirks = false
	return nil
}

func (w *wrapper) closesParagraph(tag string) bool {
	if tag == "table" {
		return !w.quirks
	}
	return ClosesParagraph[tag]
}

// closing clears the paragraph flag for every start tag that ends a paragraph by
// arriving. It is registered before paragraph, so a <p> that closes a previous one
// clears the flag and then sets it again.
func (w *wrapper) closing(e *lolhtml.Element) error {
	if w.closesParagraph(e.TagName()) {
		w.inP = false
	}
	return nil
}

// paragraph keeps the flag, which is the only piece of tree this program needs and
// the one the library cannot give it. The registration is on the paragraph itself
// rather than on every element, because each one costs memory until the rewrite
// ends: see [lolhtml.Element.OnEndTag]. Its callback fires at the paragraph's own
// end tag or at an ancestor's, and both of those mean the paragraph is over - if it
// was a start tag that ended it, closing above has already cleared the flag.
func (w *wrapper) paragraph(e *lolhtml.Element) error {
	w.inP = true
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		w.inP = false
		return nil
	})
}

// existing counts the containers already in the document so that a second run
// changes nothing.
func (w *wrapper) existing(e *lolhtml.Element) error {
	if !hasClass(e, w.opts.Class) {
		return nil
	}
	w.inWrap++
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		w.inWrap--
		return nil
	})
}

func (w *wrapper) wide(e *lolhtml.Element) error {
	if w.inWrap > 0 {
		w.res.Wrapped++
		return nil
	}
	tag := e.TagName()
	span := w.inP && !w.closesParagraph(tag)
	name := "div"
	if span {
		name = "span"
	}
	if !e.CanHaveContent() {
		// A void element has no end tag to close the wrapper at, so both halves go
		// around the tag itself.
		if err := e.Before(w.open(name), lolhtml.HTML); err != nil {
			return err
		}
		if err := e.After("</"+name+">", lolhtml.HTML); err != nil {
			return err
		}
		w.count(span)
		return nil
	}
	// The opening half cannot be written until the closing half is known to have a
	// position, and that is only known at the end tag - which is after the opening
	// half's position has gone past. So the opening half is written now and the
	// element is counted as displaced if the end tag turns out not to be its own,
	// which is the one case this program cannot make right.
	if err := e.Before(w.open(name), lolhtml.HTML); err != nil {
		return err
	}
	w.pending++
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		w.pending--
		if t.Name() != tag {
			w.res.Displaced++
			// The wrapper is closed here anyway: an unclosed div would swallow the
			// rest of the document, which is worse than a container that ends late.
			return t.Before("</"+name+">", lolhtml.HTML)
		}
		w.count(span)
		return t.After("</"+name+">", lolhtml.HTML)
	})
}

func (w *wrapper) count(span bool) {
	if span {
		w.res.Spans++
		return
	}
	w.res.Divs++
}

func (w *wrapper) open(name string) string {
	var b strings.Builder
	b.WriteString("<" + name + ` class="` + lolhtml.EscapeAttribute(w.opts.Class) + `" tabindex="0"`)
	if w.opts.Label != "" {
		b.WriteString(` role="region" aria-label="` + lolhtml.EscapeAttribute(w.opts.Label) + `"`)
	}
	b.WriteString(">")
	return b.String()
}

func (w *wrapper) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnDoctype(w.doctype),
		lolhtml.OnElement("*", w.closing),
		lolhtml.OnElement("p", w.paragraph),
		lolhtml.OnElement("div,span", w.existing),
		lolhtml.OnElement(w.opts.Selector, w.wide),
	}
}

func hasClass(e *lolhtml.Element, want string) bool {
	class, ok := e.Attribute("class")
	if !ok {
		return false
	}
	for _, f := range strings.Fields(class) {
		if f == want {
			return true
		}
	}
	return false
}

// Wrap copies src to dst, putting a scroll container around every wide element.
func Wrap(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Selector == "" {
		opts.Selector = Wide
	}
	if opts.Class == "" {
		opts.Class = "scroll"
	}
	w := &wrapper{opts: opts, quirks: true}
	rw, err := lolhtml.NewWriter(dst, w.options()...)
	if err != nil {
		return w.res, err
	}
	if _, err := io.Copy(rw, src); err != nil {
		rw.Close()
		return w.res, err
	}
	if err := rw.Close(); err != nil {
		return w.res, err
	}
	// An element still pending never had an end tag, so its wrapper was opened and
	// never closed. That is the one output this program would rather not have
	// produced, and the count says so.
	w.res.Unclosed += w.pending
	return w.res, nil
}

func main() {
	opts := Options{Selector: Wide, Class: "scroll"}
	flag.StringVar(&opts.Selector, "selector", opts.Selector, "what counts as wide")
	flag.StringVar(&opts.Class, "class", opts.Class, "class for the wrapper")
	flag.StringVar(&opts.Label, "label", "", `accessible name, adding role="region" with it`)
	flag.Parse()

	res, err := Wrap(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scrollwrap:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

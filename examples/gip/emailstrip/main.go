// Command emailstrip removes everything a mail client would reject and says what it removed and
// why.
//
//	$ emailstrip < newsletter.html
//	kept 42 elements, removed 9
//	  element        script          3   not on the allow-list
//	  element        iframe          1   not on the allow-list
//	  attribute      onclick         4   an event handler
//	  attribute      class           7   no stylesheet survives, so a class does nothing
//	  url            javascript:     1   only http, https and mailto are kept
//	  comment        ordinary        5   kept 2 conditional comments
//	  inside removed 11                  not counted above: already going
//
// An allow-list is the only defensible shape for this. A block-list is a list of the things
// somebody thought of, and mail clients reject more than anybody has thought of, so what is not
// known to be safe goes.
//
// # Why the last line of the report exists
//
// Removing an element does not stop handlers running for what was inside it. A handler on
// <script> removes it; the handlers for the attributes and text inside it still run, and a
// report that counts them says it removed eleven attributes when it removed a script.
//
// [lolhtml.Element.IsRemoved] answers for an ancestor, so an element handler can tell that it is
// looking at something already on its way out - that is what the "inside removed" line counts,
// and why the numbers above it are the ones a reader can act on.
//
// Text is the exception, and the library says so: [lolhtml.TextChunk.IsRemoved] answers for the
// chunk itself and not for its ancestors, so a text handler cannot tell. Anything accumulating
// text - the visible-text summary this program prints with -text - has to be told by an element
// handler instead, which is what the removed-depth counter here is for.
//
// # What is kept
//
// The elements and attributes a mail client renders, which is a short list and an opinionated
// one; -list prints it. Conditional comments are kept and ordinary ones are not, because
// <!--[if mso]> is how a template talks to Outlook and a comment is otherwise a place where a
// tracking pixel hides. URLs are kept for http, https and mailto and dropped for everything
// else, which is where javascript: goes.
package main

import (
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// AllowedElements is what a mail client renders. Everything else goes.
var AllowedElements = map[string]bool{
	"html": true, "head": true, "body": true, "title": true, "meta": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
	"p": true, "div": true, "span": true, "a": true, "img": true, "br": true, "hr": true,
	"b": true, "strong": true, "i": true, "em": true, "u": true, "small": true, "font": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "center": true, "blockquote": true,
}

// AllowedAttributes is what may stay, per element name. The empty key is the list that applies
// to every element.
var AllowedAttributes = map[string]map[string]bool{
	"": {"style": true, "align": true, "valign": true, "width": true, "height": true,
		"bgcolor": true, "colspan": true, "rowspan": true, "border": true,
		"cellpadding": true, "cellspacing": true, "dir": true, "lang": true, "role": true},
	"a":     {"href": true, "target": true, "title": true},
	"img":   {"src": true, "alt": true, "title": true},
	"meta":  {"charset": true, "content": true, "name": true, "http-equiv": true},
	"table": {"summary": true},
	"font":  {"color": true, "face": true, "size": true},
}

// AllowedSchemes are the URL schemes a link may use. A scheme-relative or relative URL has no
// scheme and is kept.
var AllowedSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// Removal is one thing taken out, counted by kind and name.
type Removal struct {
	Kind   string // element, attribute, url, comment
	Name   string
	Reason string
	Count  int
}

// Report is what the strip did.
type Report struct {
	Kept     int
	Removals map[string]*Removal
	// InsideRemoved counts elements and attributes met inside a subtree that was already
	// going, which are deliberately not counted above.
	InsideRemoved int
	// ConditionalComments counts the comments kept because they are conditional.
	ConditionalComments int
	// Text is the visible text, with the text of removed subtrees left out - which needs an
	// element handler's help, since a text chunk cannot tell.
	Text string
}

func newReport() *Report { return &Report{Removals: map[string]*Removal{}} }

func (r *Report) remove(kind, name, reason string) {
	key := kind + "\x00" + name
	if existing, ok := r.Removals[key]; ok {
		existing.Count++
		return
	}
	r.Removals[key] = &Removal{Kind: kind, Name: name, Reason: reason, Count: 1}
}

// Sorted returns the removals in a stable order: by kind, then by count descending, then by
// name, so a report of the same document reads the same twice.
func (r *Report) Sorted() []Removal {
	out := make([]Removal, 0, len(r.Removals))
	for _, v := range r.Removals {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Removed is the total number of things taken out, which is the headline.
func (r *Report) Removed() int {
	n := 0
	for _, v := range r.Removals {
		n += v.Count
	}
	return n
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "kept %d elements, removed %d\n", r.Kept, r.Removed())
	for _, rem := range r.Sorted() {
		fmt.Fprintf(&b, "  %-14s %-14s %-4d %s\n", rem.Kind, rem.Name, rem.Count, rem.Reason)
	}
	if r.ConditionalComments > 0 {
		fmt.Fprintf(&b, "  %-14s %-14s %-4d kept: this is how a template talks to Outlook\n",
			"comment", "conditional", r.ConditionalComments)
	}
	if r.InsideRemoved > 0 {
		fmt.Fprintf(&b, "  %-14s %-14s %-4d not counted above: already going\n",
			"inside removed", "", r.InsideRemoved)
	}
	return b.String()
}

// Options are the caller's choices.
type Options struct {
	// Note is plain text appended at the document end, inserted with lolhtml.Text so that a
	// note containing "<" or "&" is text rather than markup.
	Note string
	// Summary adds a comment in the head listing what was removed, inserted with
	// lolhtml.HTML because a comment is markup.
	Summary bool
	// KeepText collects the visible text, which is what a plain-text alternative part
	// would be built from.
	KeepText bool
}

// Strip rewrites r into w, removing what a mail client would reject.
func Strip(r io.Reader, w io.Writer, opts Options) (*Report, error) {
	report := newReport()

	// removed is the depth of removed subtrees, kept for the text handler's benefit: a text
	// chunk cannot tell that an ancestor was removed, so an element handler has to say.
	removed := 0
	var text strings.Builder

	handlers := []lolhtml.Option{
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			// A doctype is kept as it is: replacing it would change the document's
			// mode, and that decides how a table wrapper lands. See B174.
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			// IsRemoved answers for an ancestor, so this is how the report avoids
			// counting the contents of something already going.
			if e.IsRemoved() {
				report.InsideRemoved++
				return nil
			}

			tag := e.TagName()
			if !AllowedElements[tag] {
				report.remove("element", tag, "not on the allow-list")
				e.Remove()
				if e.CanHaveContent() {
					removed++
					return e.OnEndTag(func(*lolhtml.EndTag) error {
						removed--
						return nil
					})
				}
				return nil
			}

			report.Kept++
			for _, attr := range e.AttributeList() {
				name := strings.ToLower(attr.Name)
				switch {
				case strings.HasPrefix(name, "on"):
					report.remove("attribute", name, "an event handler")
				case name == "class" || name == "id":
					report.remove("attribute", name,
						"no stylesheet survives, so it does nothing")
				case allowedAttribute(tag, name):
					if (name == "href" || name == "src") && !allowedURL(attr.Value) {
						report.remove("url", scheme(attr.Value)+":",
							"only http, https and mailto are kept")
						break
					}
					if name == "style" {
						if marker, reason := styleMarker(attr.Value); marker != "" {
							report.remove("style", marker, reason)
							break
						}
					}
					continue
				default:
					report.remove("attribute", name, "not on the allow-list for <"+tag+">")
				}
				if err := e.RemoveAttribute(attr.Name); err != nil {
					return err
				}
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if isConditional(c.Text()) {
				report.ConditionalComments++
				return nil
			}
			report.remove("comment", "ordinary", "a comment is a place to hide a pixel")
			c.Remove()
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			// TextChunk.IsRemoved answers for the chunk and not for its ancestors, so
			// the depth counter above is what decides this.
			if removed > 0 || !opts.KeepText {
				return nil
			}
			text.WriteString(c.Text())
			return nil
		}),
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if !opts.Summary || e.IsRemoved() {
				return nil
			}
			// A comment is markup, so it goes in with HTML. What it says is built from
			// the counts, which are complete only at the document end - so this is
			// deliberately vague: an exact list would need a second pass.
			return e.Append("<!-- emailstrip removed what a mail client rejects -->",
				lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if opts.Note == "" {
				return nil
			}
			// Text, because a note is text: one containing "<3" or an ampersand has to
			// arrive as those characters rather than as markup.
			return d.Append(opts.Note, lolhtml.Text)
		}),
	}

	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return report, err
	}
	if _, err := io.Copy(writer, r); err != nil {
		writer.Close()
		return report, err
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	report.Text = strings.TrimSpace(text.String())
	return report, nil
}

// StripString is Strip over a string, which is what the tests use.
func StripString(doc string, opts Options) (string, *Report, error) {
	var out strings.Builder
	report, err := Strip(strings.NewReader(doc), &out, opts)
	return out.String(), report, err
}

// allowedAttribute reports whether name may stay on tag.
func allowedAttribute(tag, name string) bool {
	if AllowedAttributes[""][name] {
		return true
	}
	return AllowedAttributes[tag][name]
}

// scheme returns a URL's scheme, or "" if it has none.
//
// The value is decoded first, and that is the whole of why this function exists. An attribute
// value arrives as raw source with its character references intact - the href of
// <a href="&#106;avascript:x()"> reads as "&#106;avascript:x()" - and a browser decodes before it
// acts, so a check on the raw string sees a scheme called "&#106;avascript" and lets it through.
// Every one of these executes in a browser and none of them is "javascript" to a naive check:
//
//	javascript:alert(1)
//	&#106;avascript:alert(1)
//	&#x6a;avascript:alert(1)
//	&#0000106;avascript:alert(1)
//	jav&#x09;ascript:alert(1)
//	&Tab;javascript:alert(1)
//
// The first version of this program had exactly that hole, with the library's documentation
// warning about it on the page above the one being read at the time. The rule the documentation
// gives is: decide on the decoded form, rewrite the raw one.
//
// html.UnescapeString decodes more of an attribute value than a browser does - a named reference
// without its semicolon before "=" or an alphanumeric is not a reference to a parser and is one
// to the standard library. For a filter that is the safe direction: it can only make this reject
// a URL a browser would have accepted, never the reverse. For a rewrite it would be the wrong
// direction, which is why nothing here writes the decoded form back.
//
// It is deliberately not net/url either: a value net/url refuses to parse is a value this program
// has to decide about anyway, and the decision is the same one.
func scheme(v string) string {
	trimmed := strings.TrimLeft(stdhtml.UnescapeString(v), " \t\r\n\f\v\x00")
	colon := strings.IndexByte(trimmed, ':')
	if colon < 0 {
		return ""
	}
	candidate := strings.ToLower(trimmed[:colon])
	// A parser ignores tab, newline and carriage return inside a scheme, so "jav\tascript"
	// is "javascript" to it and has to be here too.
	candidate = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r', 0:
			return -1
		}
		return r
	}, candidate)
	for _, r := range candidate {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '-', r == '.':
		default:
			// Not a scheme: a colon inside a path or a query is not one.
			return ""
		}
	}
	// A slash or a question mark before the colon means the colon is in the path.
	if i := strings.IndexAny(trimmed[:colon], "/?#"); i >= 0 {
		return ""
	}
	return candidate
}

// allowedURL reports whether a URL's scheme is one a mail client should follow.
func allowedURL(v string) bool {
	s := scheme(v)
	return s == "" || AllowedSchemes[s]
}

// UnsafeStyle are the things a style attribute must not contain. A style attribute is allowed at
// all because inlined CSS is how an email is styled - see examples/gip/email - but its contents
// are as attacker-chosen as anything else, and CSS has had two ways to run code.
var UnsafeStyle = []struct{ Marker, Reason string }{
	{"javascript:", "a javascript: URL inside CSS"},
	{"vbscript:", "a vbscript: URL inside CSS"},
	{"expression(", "an IE CSS expression, which executes"},
	{"-moz-binding", "an XBL binding, which executes"},
	{"behavior:", "an IE behavior, which executes"},
	{"@import", "an import, which fetches"},
	{"url(data:", "a data: URL, which several clients refuse and one renders"},
}

// allowedStyle reports whether a style attribute's value is free of the markers above. The value
// is decoded first, for the reason scheme() decodes: a browser decodes before it acts, and
// "&#106;avascript:" in a style attribute is "javascript:" to it.
//
// This is a marker check rather than a CSS parser, and the difference matters: a marker check
// cannot be complete, so what it buys is the vectors that are known, not a guarantee. A program
// that has to be sure removes the style attribute instead.
func allowedStyle(v string) bool {
	marker, _ := styleMarker(v)
	return marker == ""
}

// styleMarker names the marker that made a style attribute unsafe and why, or two empty strings
// if none did. The marker is returned as well as the reason so that the report groups by what was
// found rather than by the first thing that happened to be found.
func styleMarker(v string) (marker, reason string) {
	decoded := strings.ToLower(stdhtml.UnescapeString(v))
	// CSS ignores whitespace and comments inside a value, so both are removed before the
	// markers are looked for: "expression (" and "expr/**/ession(" are the same thing to a
	// browser that runs it.
	decoded = removeCSSComments(decoded)
	decoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', 0:
			return -1
		}
		return r
	}, decoded)

	for _, unsafe := range UnsafeStyle {
		if strings.Contains(decoded, strings.ReplaceAll(unsafe.Marker, " ", "")) {
			return unsafe.Marker, unsafe.Reason
		}
	}
	return "", ""
}

// removeCSSComments takes /* ... */ out of a value, since a browser does and a marker check that
// does not is bypassed by "expr/**/ession(".
func removeCSSComments(v string) string {
	for {
		start := strings.Index(v, "/*")
		if start < 0 {
			return v
		}
		end := strings.Index(v[start+2:], "*/")
		if end < 0 {
			return v[:start]
		}
		v = v[:start] + v[start+2+end+2:]
	}
}

// isConditional reports whether a comment is an Outlook conditional comment, which is markup a
// template means rather than a place to hide something.
func isConditional(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(t, "[if ") || strings.HasPrefix(t, "[endif]") ||
		strings.HasPrefix(t, "<![endif]")
}

func main() {
	note := flag.String("note", "", "plain text to append at the end of the document")
	summary := flag.Bool("summary", false, "add a comment in the head saying the document was stripped")
	showText := flag.Bool("text", false, "print the visible text on stderr as well as the report")
	list := flag.Bool("list", false, "print the allow-list and exit")
	flag.Parse()

	if *list {
		printList()
		return
	}

	report, err := Strip(os.Stdin, os.Stdout, Options{
		Note:     *note,
		Summary:  *summary,
		KeepText: *showText,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "emailstrip:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, report)
	if *showText {
		fmt.Fprintf(os.Stderr, "\nvisible text (%d bytes):\n%s\n", len(report.Text), report.Text)
	}
}

func printList() {
	elements := make([]string, 0, len(AllowedElements))
	for name := range AllowedElements {
		elements = append(elements, name)
	}
	sort.Strings(elements)
	fmt.Println("elements:", strings.Join(elements, " "))

	kinds := make([]string, 0, len(AllowedAttributes))
	for tag := range AllowedAttributes {
		kinds = append(kinds, tag)
	}
	sort.Strings(kinds)
	for _, tag := range kinds {
		names := make([]string, 0, len(AllowedAttributes[tag]))
		for name := range AllowedAttributes[tag] {
			names = append(names, name)
		}
		sort.Strings(names)
		label := "every element"
		if tag != "" {
			label = "<" + tag + ">"
		}
		fmt.Printf("attributes on %-14s %s\n", label+":", strings.Join(names, " "))
	}

	schemes := make([]string, 0, len(AllowedSchemes))
	for s := range AllowedSchemes {
		schemes = append(schemes, s)
	}
	sort.Strings(schemes)
	fmt.Println("url schemes:", strings.Join(schemes, " "))
}

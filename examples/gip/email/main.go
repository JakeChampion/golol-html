// Command email prepares an HTML page for an email client: it inlines the stylesheet, makes
// every URL absolute, and removes what a mail client would refuse to run.
//
//	$ email -base https://example.com/ -footer "sent to you & yours" < newsletter.html
//	used 6 of 8 rules on 31 elements, absolutised 12 URLs, removed 2 scripts and 3 event handlers
//	  skipped a:hover                  Unsupported pseudo-class or pseudo-element in selector.
//	  skipped .row + .row              Unsupported combinator `+` in selector.
//	  those 2 rules still work where a client honours a style block
//
// Mail clients strip <style> blocks, so the rules have to be written onto the elements. A
// rewriter is a good fit for that and a bad fit for CSS: it matches selectors, which is most of
// the job, and it knows nothing about specificity, cascade or inheritance, which is the rest.
// This program does the part that is a match and says what it did not do.
//
// # Which rules can be inlined
//
// The ones whose selector the rewriter can express. Measured against the pinned lol-html:
//
//	supported     tag, .class, #id, [attr], [attr=value], [attr^=value], descendant,
//	              > , *, :not(simple), :first-child, :nth-child(), a selector list
//	not           + and ~ combinators, :hover, :checked, :empty, :is(), :where(),
//	              :has(), :lang(), ::before, ::slotted(), explicit namespaces
//
// A rule this cannot express is reported rather than dropped in silence, because a newsletter
// whose hover styles vanished is a thing somebody needs to know. The library makes that easy:
// an unsupported selector is a [lolhtml.SelectorError] from [lolhtml.NewWriter] that names the
// selector and the reason, so the program builds one rewriter per candidate rule set and drops
// the rules that are refused.
//
// # Why the stylesheet has to be in the head
//
// A rewrite cannot write to a position it has already passed, and the rules arrive as the text
// of the <style> element. So a rule can only be applied to elements that come after the style
// block - which is why this works on real email templates, where the stylesheet is in the head,
// and would not work on a page with a <style> at the end of the body. The program reports the
// number of elements that went past before the stylesheet arrived, so a template with that
// shape says so rather than quietly losing its styles.
//
// That is also why this is two passes: one to read the stylesheet, one to apply it. The first
// pass reads nothing else, and the document is not held between them - see
// examples/gip/pipeline for why a second pass does not have to buffer, and note that this one
// does have to, because it cannot know its selectors until the first pass is done.
//
// # The cascade, as far as it goes
//
// The rules are written onto the elements in stylesheet order and *before* whatever the element
// already had in its style attribute, because that is the cascade: the last declaration for a
// property wins, so an element's own style has to come last and keep beating the sheet. Getting
// this backwards - appending, which is the obvious thing - makes an earlier rule beat a later
// one and the sheet beat the element.
//
// Specificity is not implemented. A rewriter matches selectors and knows nothing about how
// specific they were, so `#id {color:red}` and `p {color:blue}` are applied in the order they
// appear rather than in the order a browser would. For a hand-written email template, where the
// rules are few and rarely fight, that is usually right; for a page's stylesheet it is not, and
// the report's rule count is the number to look at before trusting it.
//
// Running the program on its own output changes nothing: a declaration that is already there
// exactly is not added again. That is what makes it safe to run twice, and it is a property the
// tests check over several documents rather than a claim.
//
// # Two additions, one per content type
//
// -footer appends plain text at the end of the document with [lolhtml.Text], which is the
// escaping the library does: a footer saying "you & <3 us" arrives as text and cannot become
// markup however it is written. -mso-style puts CSS in an Outlook conditional comment in the
// head with [lolhtml.HTML], because a conditional comment *is* markup and escaping it would put
// a visible "&lt;!--[if mso]&gt;" at the top of the page. The two options are the same insertion
// with opposite answers to "is this markup", which is the whole of what ContentType decides.
//
// # What is removed
//
// <script> elements, with their content: a script that a mail client will not run is dead
// weight, and one it *will* run is worse. Event-handler attributes, which are the same problem
// spelled differently - there is no selector for "an attribute whose name starts with on", so
// the handler lists the element's attributes and removes the ones that match. And URLs whose
// scheme is javascript:, which are neither a link nor a script but get treated as one by
// something eventually.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Rule is one CSS declaration block with its selector. Its position in the slice is its
// position in the stylesheet, which is what decides the order the declarations are applied in.
type Rule struct {
	Selector     string
	Declarations string
}

// scaffolding is the elements that come before a stylesheet in the head of every document, and
// are therefore not worth reporting as having missed it.
var scaffolding = map[string]bool{
	"html": true, "head": true, "title": true, "meta": true, "link": true,
	"base": true, "style": true,
}

// Stylesheet is the rules read out of a document, and what could not be read.
type Stylesheet struct {
	Rules []Rule
	// ElementsBefore is how many elements went past before the stylesheet arrived. A
	// non-zero count means those elements cannot be styled by this pass, which is a fact
	// about the template rather than a failure.
	ElementsBefore int
}

// ParseStylesheet pulls the rules out of the <style> elements of doc.
//
// The CSS parsing here is deliberately shallow: selector, brace, declarations, brace. It
// handles what an email template's stylesheet looks like and refuses to guess at anything else
// - an at-rule is skipped whole, because @media inside an inline style attribute means nothing
// and pretending otherwise would produce a page that looks right in the program's report and
// wrong in the client.
func ParseStylesheet(doc string) (Stylesheet, error) {
	var sheet Stylesheet
	var css strings.Builder
	inStyle := 0
	elements := 0

	_, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if e.TagName() == "style" {
				inStyle++
				if e.CanHaveContent() {
					return e.OnEndTag(func(*lolhtml.EndTag) error {
						inStyle--
						return nil
					})
				}
				return nil
			}
			if inStyle == 0 && css.Len() == 0 && !scaffolding[e.TagName()] {
				elements++
			}
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if inStyle > 0 {
				css.WriteString(c.Text())
			}
			return nil
		}))
	if err != nil {
		return sheet, err
	}

	sheet.Rules = parseRules(css.String())
	sheet.ElementsBefore = elements
	return sheet, nil
}

// parseRules is the shallow CSS parser described above.
//
// It counts braces rather than looking for the next one, because an at-rule contains rules: the
// first version took the "}" of the rule inside @media as the end of the at-rule and produced a
// rule whose selector was "}". Skipping an at-rule means skipping its whole block.
func parseRules(css string) []Rule {
	var rules []Rule
	rest := css
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			return rules
		}
		selector := strings.TrimSpace(rest[:open])

		if strings.HasPrefix(selector, "@") {
			// Skip the at-rule and everything nested in it.
			depth := 0
			i := open
			for ; i < len(rest); i++ {
				switch rest[i] {
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth == 0 {
					break
				}
			}
			if i >= len(rest) {
				return rules
			}
			rest = rest[i+1:]
			continue
		}

		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			return rules
		}
		body := strings.TrimSpace(rest[open+1 : open+closing])
		rest = rest[open+closing+1:]

		if selector != "" && body != "" {
			rules = append(rules, Rule{Selector: selector, Declarations: body})
		}
	}
}

// Skipped is a rule the rewriter could not express, and why.
type Skipped struct {
	Rule   Rule
	Reason string
}

// Report is what the run did.
type Report struct {
	// Usable is the number of rules whose selector the rewriter accepted, and
	// Applications is how many elements those rules were written onto - a rule that
	// matches nothing is usable and applied nothing.
	Usable       int
	Applications int
	Skipped      []Skipped

	URLs           int
	Scripts        int
	EventHandlers  int
	JavascriptURLs int

	// ElementsBefore counts elements that came before the stylesheet and could
	// therefore not be styled by it. The document's own scaffolding - html, head and
	// the head's own children - is excluded, since it is before the stylesheet in
	// every document and is not what a reader needs to be told about.
	ElementsBefore int

	// StyleStripped says whether the <style> elements were removed, and StyleBlocks
	// how many. It matters to the reader because it decides whether the skipped rules
	// are merely uninlined or gone.
	StyleStripped bool
	StyleBlocks   int

	// FooterAdded and MSOAdded say whether the two additions happened, which is worth
	// reporting because both are silent when their option is empty.
	FooterAdded bool
	MSOAdded    bool
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "used %d of %d rules on %d elements, absolutised %d URLs, removed %d scripts and %d event handlers\n",
		r.Usable, r.Usable+len(r.Skipped), r.Applications, r.URLs, r.Scripts, r.EventHandlers)
	if r.JavascriptURLs > 0 {
		fmt.Fprintf(&b, "  removed %d javascript: URLs\n", r.JavascriptURLs)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "  skipped %-24s %s\n", s.Rule.Selector, s.Reason)
	}
	if len(r.Skipped) > 0 {
		if r.StyleStripped {
			fmt.Fprintf(&b, "  those %d rules are gone: the style blocks were stripped\n",
				len(r.Skipped))
		} else {
			fmt.Fprintf(&b, "  those %d rules still work where a client honours a style block\n",
				len(r.Skipped))
		}
	}
	if r.ElementsBefore > 0 {
		fmt.Fprintf(&b, "  %d elements came before the stylesheet and could not be styled\n",
			r.ElementsBefore)
	}
	return b.String()
}

// usableRules returns the rules whose selectors the rewriter accepts, and the ones it refuses
// with the reason. It finds out by building a rewriter per rule, which is the only authority on
// what the selector engine supports.
func usableRules(rules []Rule) (usable []Rule, skipped []Skipped) {
	for _, rule := range rules {
		w, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnElement(rule.Selector, func(*lolhtml.Element) error { return nil }))
		if err != nil {
			var se *lolhtml.SelectorError
			if errors.As(err, &se) {
				skipped = append(skipped, Skipped{rule, se.Message})
				continue
			}
			skipped = append(skipped, Skipped{rule, err.Error()})
			continue
		}
		w.Close()
		usable = append(usable, rule)
	}
	return usable, skipped
}

// Options are the choices a caller makes. Base and StripStyle are the two that change what the
// rewrite means; Footer and MSOStyle are additions an email wants and a page does not.
type Options struct {
	// Base resolves relative URLs. Nil leaves them alone, which is better than
	// inventing a host.
	Base *url.URL
	// StripStyle removes the <style> elements after inlining. See Inline.
	StripStyle bool
	// Footer is plain text appended at the end of the document - a "sent to you
	// because" line. It is inserted with lolhtml.Text, so a footer containing "<3" or
	// an ampersand is text and cannot become markup however it is written.
	Footer string
	// MSOStyle is CSS to put in an Outlook conditional comment in the head, which is
	// the one place a mail client both ignores and honours markup. It is inserted with
	// lolhtml.HTML, because a conditional comment is markup: escaping it would produce a
	// visible "&lt;!--[if mso]&gt;" at the top of the page.
	MSOStyle string
}

// InlineString is Inline over a string, which is what the tests use.
func InlineString(doc string, opts Options) (string, Report, error) {
	var out strings.Builder
	report, err := Inline(strings.NewReader(doc), &out, opts)
	return out.String(), report, err
}

// Inline rewrites doc: the stylesheet's rules onto the elements they match, URLs made absolute
// against base, and scripts and event handlers removed.
//
// The <style> elements are kept unless stripStyle is set, and that is a real choice rather than
// a default. Keeping them means the rules this program could not express - :hover, sibling
// combinators - still work in the clients that support a style block, which is most of the
// desktop ones. Stripping them means every client sees the same thing, and the skipped rules
// are lost everywhere rather than in some places. The report says which rules that applies to,
// because it is the caller's decision and it needs the list.
// The document is read twice: once to collect the stylesheet, once to apply it. The first pass
// has to finish before the second can start - the rules are not known until the <style> element
// has been read - so this one buffers, which is the case the package documentation's two-pass
// section is about. The second pass streams: NewWriter and io.Copy, with Close checked, because
// that is the path a caller writing a mail-sending service would use.
func Inline(r io.Reader, w io.Writer, opts Options) (Report, error) {
	buffered, err := io.ReadAll(r)
	if err != nil {
		return Report{}, err
	}
	doc := string(buffered)

	sheet, err := ParseStylesheet(doc)
	if err != nil {
		return Report{}, err
	}
	usable, skipped := usableRules(sheet.Rules)

	report := Report{Usable: len(usable), Skipped: skipped, ElementsBefore: sheet.ElementsBefore,
		StyleStripped: opts.StripStyle}

	// Each rule prepends its declarations to the element's style attribute, and the rules
	// are registered in reverse stylesheet order. That is what makes the cascade come out
	// right without keeping any state: CSS gives the last declaration for a property, so
	// prepending in reverse order leaves the sheet in stylesheet order with the element's
	// own inline style last - and an inline style beats a stylesheet rule, which is the
	// actual rule and not one this program gets to choose.
	//
	// Appending instead, which is the obvious thing, gets both of those backwards: an
	// earlier rule would beat a later one, and the sheet would beat the element's own
	// style attribute.
	handlers := make([]lolhtml.Option, 0, len(usable)+6)
	for i := len(usable) - 1; i >= 0; i-- {
		declarations := usable[i].Declarations
		handlers = append(handlers, lolhtml.OnElement(usable[i].Selector, func(e *lolhtml.Element) error {
			report.Applications++
			return mergeStyle(e, declarations)
		}))
	}

	// The removals go last, so a rule that matched a script still counted before the script
	// went - the report is about the stylesheet, not about what survived.
	if opts.StripStyle {
		handlers = append(handlers, lolhtml.OnElement("style", func(e *lolhtml.Element) error {
			report.StyleBlocks++
			e.Remove()
			return nil
		}))
	}
	handlers = append(handlers,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			report.Scripts++
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			// There is no selector for "an attribute whose name begins with on", so
			// the names have to be listed and checked.
			for _, attr := range e.AttributeList() {
				if len(attr.Name) > 2 && strings.HasPrefix(strings.ToLower(attr.Name), "on") {
					if err := e.RemoveAttribute(attr.Name); err != nil {
						return err
					}
					report.EventHandlers++
				}
			}
			return nil
		}),
		// The two additions, one per content type. A conditional comment is markup and
		// a footer line is not, and the difference is the whole of what ContentType
		// decides.
		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if opts.MSOStyle == "" {
				return nil
			}
			report.MSOAdded = true
			return e.Append("<!--[if mso]><style>"+opts.MSOStyle+"</style><![endif]-->",
				lolhtml.HTML)
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if opts.Footer == "" {
				return nil
			}
			report.FooterAdded = true
			return d.Append(opts.Footer, lolhtml.Text)
		}),
		lolhtml.OnElement("a[href], img[src], link[href]", func(e *lolhtml.Element) error {
			for _, name := range []string{"href", "src"} {
				v, ok := e.Attribute(name)
				if !ok {
					continue
				}
				if isJavaScriptURL(v) {
					if err := e.RemoveAttribute(name); err != nil {
						return err
					}
					report.JavascriptURLs++
					continue
				}
				abs, changed := absolutise(v, opts.Base)
				if !changed {
					continue
				}
				if err := e.SetAttribute(name, abs); err != nil {
					return err
				}
				report.URLs++
			}
			return nil
		}),
	)

	// The streaming path: the rewriter writes to w as it goes, and Close is what flushes
	// the tail and reports a failure at the end.
	writer, err := lolhtml.NewWriter(w, handlers...)
	if err != nil {
		return report, err
	}
	if _, err := io.Copy(writer, strings.NewReader(doc)); err != nil {
		writer.Close()
		return report, err
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	return report, nil
}

// mergeStyle prepends declarations to an element's style attribute, skipping any that are
// already there exactly.
//
// Prepending is the cascade: what comes later wins, so the element's own style attribute stays
// last and keeps beating the stylesheet. Skipping exact duplicates is what makes running the
// program on its own output a no-op - without it, a second pass over an inlined document turns
// style="color:red;" into style="color:red; color:red;", which renders the same and is still
// wrong.
func mergeStyle(e *lolhtml.Element, declarations string) error {
	existing := strings.TrimSpace(mustAttr(e, "style"))
	add := make([]string, 0, 4)
	for _, decl := range splitDeclarations(declarations) {
		if hasDeclaration(existing, decl) {
			continue
		}
		add = append(add, decl)
	}
	if len(add) == 0 {
		return nil
	}

	prefix := strings.Join(add, "; ") + ";"
	if existing == "" {
		return e.SetAttribute("style", prefix)
	}
	return e.SetAttribute("style", prefix+" "+existing)
}

// mustAttr reads an attribute, treating absent as empty - which is what every caller here
// wants.
func mustAttr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// splitDeclarations breaks a declaration block into normalised declarations, dropping the
// empties a trailing semicolon leaves.
func splitDeclarations(block string) []string {
	var out []string
	for _, part := range strings.Split(block, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// hasDeclaration reports whether a style attribute already contains this exact declaration,
// comparing the property and the value with the whitespace and case that do not matter
// normalised away.
func hasDeclaration(style, decl string) bool {
	want := normaliseDeclaration(decl)
	for _, have := range splitDeclarations(style) {
		if normaliseDeclaration(have) == want {
			return true
		}
	}
	return false
}

func normaliseDeclaration(decl string) string {
	name, value, ok := strings.Cut(decl, ":")
	if !ok {
		return strings.ToLower(strings.TrimSpace(decl))
	}
	return strings.ToLower(strings.TrimSpace(name)) + ":" + strings.TrimSpace(value)
}

// isJavaScriptURL reports whether a URL's scheme is javascript:, allowing for the whitespace and
// case a parser tolerates.
func isJavaScriptURL(v string) bool {
	trimmed := strings.ToLower(strings.TrimLeft(v, " \t\r\n\f"))
	trimmed = strings.ReplaceAll(trimmed, "\t", "")
	trimmed = strings.ReplaceAll(trimmed, "\n", "")
	return strings.HasPrefix(trimmed, "javascript:")
}

// absolutise resolves v against base, and says whether it changed.
func absolutise(v string, base *url.URL) (string, bool) {
	if base == nil || v == "" || strings.HasPrefix(v, "#") {
		return v, false
	}
	ref, err := url.Parse(v)
	if err != nil {
		return v, false
	}
	if ref.IsAbs() {
		return v, false
	}
	abs := base.ResolveReference(ref).String()
	return abs, abs != v
}

func main() {
	baseFlag := flag.String("base", "", "the URL to resolve relative URLs against")
	strip := flag.Bool("strip-style", false, "remove the <style> elements, losing the rules that could not be inlined")
	footer := flag.String("footer", "", "plain text to append at the end of the document")
	mso := flag.String("mso-style", "", "CSS to put in an Outlook conditional comment in the head")
	flag.Parse()

	var base *url.URL
	if *baseFlag != "" {
		parsed, err := url.Parse(*baseFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "email:", err)
			os.Exit(2)
		}
		if !parsed.IsAbs() {
			fmt.Fprintln(os.Stderr, "email: -base has to be absolute")
			os.Exit(2)
		}
		base = parsed
	}

	report, err := Inline(os.Stdin, os.Stdout, Options{
		Base:       base,
		StripStyle: *strip,
		Footer:     *footer,
		MSOStyle:   *mso,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "email:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, report)
}

// sortedSelectors is used by the tests to compare rule sets without depending on map order.
func sortedSelectors(rules []Rule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Selector)
	}
	sort.Strings(out)
	return out
}

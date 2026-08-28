// Command placeholders resolves handlebars-style {{ name }} placeholders, choosing the escape by
// where the placeholder sits, and refuses the positions where no escape is enough.
//
//	$ placeholders -v title='Tools & Toys' -v url=/tools page.html
//	7 placeholders: 4 resolved, 3 refused
//	  resolved
//	    text            {{ title }}    Tools &amp; Toys
//	    attribute       {{ url }}      /tools
//	    title           {{ title }}    Tools &amp; Toys
//	    comment         {{ url }}      /tools
//	  refused
//	    script          {{ title }}    a script body needs escaping for JavaScript, which is
//	                                   not an HTML transformation
//	    attribute name  {{ attr }}     an attribute name cannot be rewritten, and one added
//	                                   is lower-cased
//	    url attribute   {{ evil }}     the value's scheme is javascript:
//
// # A position per escape
//
// "Escaping properly" is a different job in every position, and the library has a primitive for
// two of them, refuses one, and cannot help with the other four:
//
//	position               what it needs                          what this does
//	text content           HTML text escaping                     ContentType Text
//	attribute value        attribute-value escaping               EscapeAttribute
//	title, textarea        HTML text escaping - references are    ContentType Text
//	                       decoded there, so this is escaping
//	comment                no "-->" anywhere in the value         Comment.SetText, which refuses
//	script, style          escaping for JavaScript or CSS         refused
//	on* attribute          escaping for JavaScript, which the     refused
//	                       browser reaches after decoding
//	srcdoc attribute       escaping for a whole HTML document,    refused
//	                       likewise after decoding
//	iframe, noembed,       nothing works: no references are        refused
//	noframes, noscript,    decoded and there is no inner
//	xmp, plaintext         language to escape for
//
// The script row is the one worth spelling out, because ContentType Text looks like it works.
// Measured: inserting `</script><img src=x>` as Text into a script produces
// `&lt;/script&gt;&lt;img src=x&gt;`, which is safe - no element appears - and wrong, because
// character references are not decoded inside a script, so the JavaScript now contains the six
// characters `&lt;` where a `<` was meant. Safe and corrupt is not resolved. A value that has to
// reach a script belongs in a data attribute or a `<script type="application/json">` block.
//
// A title and a textarea are the opposite case: references *are* decoded there, so Text is a real
// escape and the value arrives intact.
//
// The on* and srcdoc rows are the script row in an attribute's clothes, and they are the two an
// "escape everything" sanitiser gets wrong, because [lolhtml.EscapeAttribute] makes the output
// look right. `<button onclick="greet('{{ name }}')">` with a name of `');alert(1);//` emits
// `onclick="greet('&#39;);alert(1);//')"`, and the browser decodes `&#39;` to a quote before the
// JavaScript is parsed, so the injected statement runs. The attribute never ended, the escape did
// its job, and the value executed anyway: escaping for HTML is not escaping for the language that
// reads the attribute after HTML has finished with it. A value that has to reach a handler belongs
// in a data attribute the handler reads.
//
// The comment row is a report rather than a guard: [lolhtml.Comment.SetText] refuses a closing
// sequence on its own, and refusing it here first is only so the run says which value did it
// instead of failing. The attribute row is the reverse - [lolhtml.EscapeAttribute] is not
// stopping an injection, because SetAttribute takes raw attribute-value source and rewrites the
// double quote on the way out, so an unescaped value cannot end the attribute either. What it
// stops is corruption: a value containing "&amp;" read back as "&". Both were checked by removing
// them and watching the tests fail.
//
// # A URL is not a string
//
// An attribute that holds a URL needs its scheme checked, and checked on the decoded value:
// `&#106;avascript:alert(1)` is `javascript:alert(1)` to a browser and is not `javascript:` to a
// string comparison. This decodes before deciding and writes the raw form back, which is the rule
// the whole library runs on - decide on the decoded form, write back the raw one.
//
// The check is on the composed attribute rather than on each value, because a scheme can be
// spelled across a boundary: `href="{{ a }}{{ b }}"` with a=`java` and b=`script:alert(1)` is
// javascript:alert(1) and neither half of it is. So the substitution happens first and the whole
// result is what the scheme test sees, which also refuses a template that spells half the scheme
// itself - `href="java{{ b }}"` - because a resolver cannot tell which half was the attacker's.
//
// # Where a placeholder is not a placeholder
//
// Two positions are gone before this program sees them, and both are worth reporting because a
// rewrite that says nothing has quietly processed a different document from the one it was given.
//
// A placeholder with spaces in an attribute-name position is not one attribute. HTML splits an
// attribute list at whitespace, so
//
//	<div {{ attr }}="1">
//
// arrives as three attributes - `{{`, `attr`, and `}}="1"` - with the value attached to the last
// of them. The name is not recoverable. Written without spaces, `{{attr}}="1"` is one attribute
// whose name is the placeholder, which is recognisable and still not rewritable: there is no
// rename, and an attribute added in its place is lower-cased.
//
// A placeholder in a tag-name position is not an element at all:
//
//	<{{ tag }}>x</{{ tag }}>
//
// "<" followed by "{" is not a tag open, so the opening half is text, and "</" followed by "{" is
// a bogus comment, so the closing half is a comment. No element handler runs, nothing inside
// nests, and the bytes pass through unchanged. Measured: zero elements, one comment.
//
// # A placeholder that straddles anything
//
// Text arrives in chunks, and `{{ na` and `me }}` can be two of them, so the text of each node is
// accumulated and resolved once at [lolhtml.TextChunk.IsLastInTextNode]. A comment or an element
// in the middle ends the node, which is the right answer rather than a problem: `{{ na<!-- c
// -->me }}` is not a placeholder, and the accumulator sees two nodes and finds none.
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// placeholder matches {{ name }} with optional surrounding space. The name is deliberately narrow
// - letters, digits, dots, hyphens and underscores - because anything else is an expression and
// this resolves values rather than evaluating them.
var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// rawText elements hold text rather than markup. The first two decode character references, so an
// HTML escape is a real escape in them; the rest do not.
var (
	escapableRawText = map[string]bool{"title": true, "textarea": true}
	rawText          = map[string]bool{
		"script": true, "style": true, "title": true, "textarea": true,
		"iframe": true, "noembed": true, "noframes": true, "noscript": true,
		"xmp": true, "plaintext": true,
	}
)

// urlAttrs are the attributes whose value a browser fetches or navigates to, so a scheme it does
// not like is an execution rather than a string.
var urlAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true, "data": true,
	"poster": true, "cite": true, "background": true, "srcset": true, "ping": true,
	"longdesc": true, "usemap": true, "profile": true, "manifest": true, "codebase": true,
}

// dangerousSchemes are refused in a URL attribute. The list is the schemes that execute rather
// than fetch.
var dangerousSchemes = []string{"javascript:", "data:", "vbscript:", "livescript:", "mocha:"}

// Position is where a placeholder was found, which decides the escape.
type Position int

const (
	InText Position = iota
	InAttribute
	InURLAttribute
	InEventAttribute
	InSrcdocAttribute
	InEscapableRawText
	InRawText
	InComment
	InAttributeName
	InSplitAttributeName
	InTagName
)

func (p Position) String() string {
	switch p {
	case InText:
		return "text"
	case InAttribute:
		return "attribute"
	case InURLAttribute:
		return "url attribute"
	case InEventAttribute:
		return "event attribute"
	case InSrcdocAttribute:
		return "srcdoc attribute"
	case InEscapableRawText:
		return "title or textarea"
	case InRawText:
		return "script or style"
	case InComment:
		return "comment"
	case InAttributeName:
		return "attribute name"
	case InSplitAttributeName:
		return "split attribute name"
	case InTagName:
		return "tag name"
	}
	return "?"
}

// Found is one placeholder the program looked at.
type Found struct {
	Name     string
	Position Position
	// Resolved is the value written, and Why the reason it was not.
	Resolved string
	Why      string
}

// Result is what a run did.
type Result struct {
	Found []Found
}

func (r Result) Resolved() (out []Found) {
	for _, f := range r.Found {
		if f.Why == "" {
			out = append(out, f)
		}
	}
	return out
}

func (r Result) Refused() (out []Found) {
	for _, f := range r.Found {
		if f.Why != "" {
			out = append(out, f)
		}
	}
	return out
}

// Resolve rewrites src into dst, replacing every placeholder it can.
func Resolve(src io.Reader, dst io.Writer, values map[string]string) (Result, error) {
	res := &Result{}

	// One accumulator per text node, because a placeholder can straddle a chunk boundary.
	var text strings.Builder

	// Where the current text node is. A selector-associated text handler runs before the
	// document-level one for the same chunk, whatever order they were registered in, so the
	// per-tag handlers below can tell the accumulator where it is. Measured, and the only
	// way a document-level handler can know: a text chunk does not name its element.
	inTag := ""

	opts := []lolhtml.Option{
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			tag := inTag
			text.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				// The chunk's bytes are held for the accumulator, so they must
				// not also be written out.
				t.Remove()
				return nil
			}
			whole := text.String()
			text.Reset()
			inTag = ""

			tagNamePlaceholder(res, whole)

			if !placeholder.MatchString(whole) {
				// Nothing to resolve, but the earlier chunks were removed, so
				// the node has to be put back.
				return t.Replace(whole, lolhtml.HTML)
			}

			switch {
			case tag == "":
				return t.Replace(resolveText(res, whole, values), lolhtml.HTML)
			case escapableRawText[tag]:
				// References are decoded in a title and a textarea, so an HTML
				// escape is a real escape and Text is right.
				return t.Replace(resolveEscapableRawText(res, whole, values, tag),
					lolhtml.HTML)
			default:
				// Everything else is raw text with no escape that works. The
				// node is put back untouched and each placeholder recorded.
				refuseRawText(res, whole, tag)
				return t.Replace(whole, lolhtml.HTML)
			}
		}),

		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			body := c.Text()
			if !placeholder.MatchString(body) {
				return nil
			}
			return c.SetText(resolveComment(res, body, values))
		}),

		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			// A placeholder with spaces in an attribute-name position is not one
			// attribute: HTML splits it at the spaces, so {{ attr }}="1" arrives as
			// three attributes and the value belongs to "}}". There is nothing to
			// resolve and nothing to warn about except that it happened.
			if opens, closes := attrCount(e, "{{"), attrCount(e, "}}"); opens > 0 || closes > 0 {
				for range max(opens, closes) {
					res.Found = append(res.Found, Found{
						Name:     "?",
						Position: InSplitAttributeName,
						Why: "a placeholder with spaces in an attribute-name " +
							"position is split into separate attributes " +
							"by the parser, so its name is not recoverable",
					})
				}
			}
			for _, a := range e.AttributeList() {
				if placeholder.MatchString(a.NamePreserveCase) {
					for _, m := range placeholder.FindAllStringSubmatch(
						a.NamePreserveCase, -1) {
						res.Found = append(res.Found, Found{
							Name:     m[1],
							Position: InAttributeName,
							Why: "an attribute name cannot be rewritten, " +
								"and one added is lower-cased",
						})
					}
					continue
				}
				if !placeholder.MatchString(a.Value) {
					continue
				}
				value, ok := resolveAttribute(res, strings.ToLower(a.Name), a.Value, values)
				if !ok {
					continue
				}
				if err := e.SetAttribute(a.Name, value); err != nil {
					return err
				}
			}
			return nil
		}),
	}

	// One handler per raw-text element, whose only job is to say where the next chunk is.
	for tag := range rawText {
		tag := tag
		opts = append(opts, lolhtml.OnText(tag, func(*lolhtml.TextChunk) error {
			inTag = tag
			return nil
		}))
	}

	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return *res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return *res, err
	}
	if err := w.Close(); err != nil {
		return *res, err
	}
	return *res, nil
}

// attrCount counts the attributes whose name is exactly name, which is how the split form of an
// attribute-name placeholder shows up.
func attrCount(e *lolhtml.Element, name string) int {
	n := 0
	for _, a := range e.AttributeList() {
		if a.NamePreserveCase == name {
			n++
		}
	}
	return n
}

// tagNamePlaceholder finds "<{{" in text, which is what a placeholder in a tag-name position
// becomes: "<" followed by "{" is not a tag open, so the whole thing is text and no element
// handler ever sees the element it was meant to be. Nothing can be done about it, and a rewrite
// that does not say so has quietly processed a different document from the one it was given.
func tagNamePlaceholder(res *Result, whole string) {
	for range strings.Count(whole, "<{{") + strings.Count(whole, "</{{") {
		res.Found = append(res.Found, Found{Name: "?", Position: InTagName,
			Why: "a placeholder in a tag-name position is text to a parser, so the " +
				"element it names is not an element, nothing inside it nests, " +
				"and its closing half is a bogus comment"})
	}
}

// resolveText replaces the placeholders in one text node and returns attribute-value-free HTML
// source: the resolved values are escaped as text, and the surrounding source is passed through.
func resolveText(res *Result, whole string, values map[string]string) string {
	return placeholder.ReplaceAllStringFunc(whole, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		value, ok := values[name]
		if !ok {
			res.Found = append(res.Found, Found{Name: name, Position: InText,
				Why: "no value was supplied"})
			return match
		}
		escaped := lolhtml.EscapeText(value)
		res.Found = append(res.Found, Found{Name: name, Position: InText, Resolved: escaped})
		return escaped
	})
}

// resolveEscapableRawText resolves in a title or a textarea, where character references are
// decoded, so escaping as text is escaping rather than corruption.
func resolveEscapableRawText(res *Result, whole string, values map[string]string, tag string) string {
	return placeholder.ReplaceAllStringFunc(whole, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		value, ok := values[name]
		if !ok {
			res.Found = append(res.Found, Found{Name: name, Position: InEscapableRawText,
				Why: "no value was supplied"})
			return match
		}
		escaped := lolhtml.EscapeText(value)
		res.Found = append(res.Found, Found{Name: name, Position: InEscapableRawText,
			Resolved: escaped})
		return escaped
	})
}

// refuseRawText records every placeholder in a raw-text element that has no escape. Escaping for
// the inner language is not an HTML transformation, and for the elements with no inner language
// there is nothing to escape into at all.
func refuseRawText(res *Result, whole, tag string) {
	why := "a " + tag + " body needs escaping for its own language, which is not an HTML " +
		"transformation"
	switch tag {
	case "iframe", "noembed", "noframes", "noscript", "xmp", "plaintext":
		why = "a " + tag + " decodes no references and has no inner language, so there is " +
			"no way to write the value safely"
	}
	for _, m := range placeholder.FindAllStringSubmatch(whole, -1) {
		res.Found = append(res.Found, Found{Name: m[1], Position: InRawText, Why: why})
	}
}

// resolveComment does the same for a comment's text, where the only hazard is a closing sequence
// and Comment.SetText refuses one - so this refuses first and says which value did it.
func resolveComment(res *Result, body string, values map[string]string) string {
	return placeholder.ReplaceAllStringFunc(body, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		value, ok := values[name]
		if !ok {
			res.Found = append(res.Found, Found{Name: name, Position: InComment,
				Why: "no value was supplied"})
			return match
		}
		if strings.Contains(value, "-->") || strings.Contains(value, "--!>") ||
			strings.HasSuffix(value, "--") {
			res.Found = append(res.Found, Found{Name: name, Position: InComment,
				Why: "the value would end the comment"})
			return match
		}
		res.Found = append(res.Found, Found{Name: name, Position: InComment, Resolved: value})
		return value
	})
}

// resolveAttribute returns the new attribute value, or false to leave the attribute alone.
func resolveAttribute(res *Result, name, source string, values map[string]string) (string, bool) {
	position := InAttribute
	switch {
	case strings.HasPrefix(name, "on"):
		// Every event-handler content attribute is named on*, and the whole name
		// space is refused rather than a list of the known handlers: an on* name
		// this program has not heard of is either a handler that postdates the list
		// or an attribute nobody needed to resolve, and refusing both is the safe
		// way round.
		position = InEventAttribute
	case name == "srcdoc":
		position = InSrcdocAttribute
	case urlAttrs[name]:
		position = InURLAttribute
	}

	// Two attribute positions hold another language, and EscapeAttribute is not an escape
	// for either of them: the browser decodes the character references in an attribute value
	// before the inner language sees it, so `&#39;` reaches the JavaScript parser as a quote
	// and `&lt;script&gt;` reaches the srcdoc document as a script element. What the escaping
	// buys is that the value cannot end the attribute, which is not the same thing as being
	// inert. This is the script row of the table again, in attribute form, so it is refused
	// for the same reason.
	if position == InEventAttribute || position == InSrcdocAttribute {
		why := "an " + name + " attribute is JavaScript, and a browser decodes the " +
			"references in it before the script runs, so no HTML escape reaches it"
		if position == InSrcdocAttribute {
			why = "a srcdoc attribute is an HTML document, which the browser decodes " +
				"before parsing, so an HTML escape only survives the attribute"
		}
		for _, m := range placeholder.FindAllStringSubmatch(source, -1) {
			res.Found = append(res.Found, Found{Name: m[1], Position: position, Why: why})
		}
		return source, false
	}

	// A URL is one string however many placeholders spell it, so the scheme is checked on the
	// composed value rather than on each value in isolation. `href="{{ a }}{{ b }}"` with
	// a="java" and b="script:alert(1)" passes every per-value prefix test and navigates to
	// javascript:, and so does the half-static `href="java{{ b }}"`. Composing first also
	// means a template that already spells a dangerous scheme is refused rather than
	// completed, which is the answer this program owes a reader: a placeholder resolver
	// cannot tell which half of a URL the attacker supplied. The values go in unescaped, so
	// that a value of `&#106;avascript:` is still refused rather than being made harmless by
	// the escape it is about to receive - see dangerous, which decodes what it is given.
	if position == InURLAttribute {
		composed := placeholder.ReplaceAllStringFunc(source, func(match string) string {
			key := placeholder.FindStringSubmatch(match)[1]
			if value, ok := values[key]; ok {
				return value
			}
			return match
		})
		if scheme, bad := dangerous(composed); bad {
			for _, m := range placeholder.FindAllStringSubmatch(source, -1) {
				res.Found = append(res.Found, Found{Name: m[1], Position: position,
					Why: "the value's scheme is " + scheme})
			}
			return source, false
		}
	}

	changed := false
	out := placeholder.ReplaceAllStringFunc(source, func(match string) string {
		key := placeholder.FindStringSubmatch(match)[1]
		value, ok := values[key]
		if !ok {
			res.Found = append(res.Found, Found{Name: key, Position: position,
				Why: "no value was supplied"})
			return match
		}
		changed = true
		escaped := lolhtml.EscapeAttribute(value)
		res.Found = append(res.Found, Found{Name: key, Position: position, Resolved: escaped})
		return escaped
	})
	return out, changed
}

// dangerous decides on the decoded value, because &#106;avascript: is javascript: to a browser
// and is not to a string comparison.
func dangerous(value string) (string, bool) {
	decoded := strings.ToLower(strings.TrimLeft(html.UnescapeString(value), " \t\n\r\f\v\x00"))
	decoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v', 0:
			return -1
		}
		return r
	}, decoded)
	for _, scheme := range dangerousSchemes {
		if strings.HasPrefix(decoded, scheme) {
			return scheme, true
		}
	}
	return "", false
}

func (r Result) String() string {
	var b strings.Builder
	resolved, refused := r.Resolved(), r.Refused()
	fmt.Fprintf(&b, "%d placeholders: %d resolved, %d refused\n",
		len(r.Found), len(resolved), len(refused))
	if len(resolved) > 0 {
		b.WriteString("  resolved\n")
		for _, f := range resolved {
			fmt.Fprintf(&b, "    %-18s {{ %s }} -> %s\n", f.Position, f.Name, f.Resolved)
		}
	}
	if len(refused) > 0 {
		b.WriteString("  refused\n")
		byWhy := map[string][]string{}
		for _, f := range refused {
			byWhy[f.Why] = append(byWhy[f.Why],
				fmt.Sprintf("%-18s {{ %s }}", f.Position, f.Name))
		}
		whys := make([]string, 0, len(byWhy))
		for why := range byWhy {
			whys = append(whys, why)
		}
		sort.Strings(whys)
		for _, why := range whys {
			for _, what := range byWhy[why] {
				fmt.Fprintf(&b, "    %s %s\n", what, why)
			}
		}
	}
	return b.String()
}

type valueList map[string]string

func (v valueList) String() string { return "" }

func (v valueList) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return fmt.Errorf("%q: want name=value", s)
	}
	v[name] = value
	return nil
}

func main() {
	values := valueList{}
	flag.Var(values, "v", "name=value, repeatable")
	report := flag.Bool("report", false, "print what happened instead of the document")
	flag.Parse()

	var src io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "placeholders:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	dst := io.Writer(os.Stdout)
	var held strings.Builder
	if *report {
		dst = &held
	}
	res, err := Resolve(src, dst, values)
	if err != nil {
		fmt.Fprintln(os.Stderr, "placeholders:", err)
		os.Exit(1)
	}
	if *report {
		fmt.Print(res)
	}
	if len(res.Refused()) > 0 {
		os.Exit(1)
	}
}

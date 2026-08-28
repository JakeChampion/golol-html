// Command greet injects a personalised greeting taken from a request header into
// four kinds of place, and treats the header as what it is: a string an attacker
// chooses.
//
//	<span data-greet="name">there</span>              element content
//	<div data-greet-title="name">                     an attribute
//	<title data-greet-append="name">Hello, </title>   raw text a parser decodes
//	<script type="application/json" data-greet-json>  a script, via JSON
//
// The four positions need four different rules, and the library's own
// documentation is where they come from. What this program adds is the part before
// them, which is the part a rewriter usually gets wrong: what the value has to be
// before any of the rules apply.
//
// A header can be bytes that are not UTF-8. "X-Name: caf\xe9" is Latin-1, which
// is a perfectly ordinary thing for an old client to send, and inserting it fails
// the rewrite - not the insertion, the rewrite. Every write path refuses invalid
// UTF-8 and [lolhtml.ErrInvalidUTF8] is how to know that is what happened. The
// document path does not refuse the same bytes, which is why this is easy to miss:
// a page can carry them through and not be able to write them. So the value is
// repaired with [strings.ToValidUTF8] before it goes anywhere near an insertion,
// and the repair is counted, because a page that quietly says "caf?" is a page
// somebody should know about.
//
// A header can also hold control characters, which the library accepts. A NUL
// becomes U+FFFD in whatever parses the output; an ESC in a name reaches a log
// file as a terminal escape sequence. Neither belongs in a greeting, so they are
// stripped rather than passed on.
//
// And a header can be long. Eight kilobytes of "A" is a valid header value and
// not a name, so the value is cut to a number of runes rather than bytes: cutting
// bytes would split a character, and a string that ends mid-character is the one
// thing every write path refuses.
//
// Then the four rules, each of which is a different answer to "what is markup
// here":
//
// Element content takes [lolhtml.Text], which escapes "<", ">" and "&". That is
// the whole of it, and it is why "</span><script>alert(1)</script>" in a header is
// nine words of text rather than a script.
//
// An attribute value does not take Text. [lolhtml.Element.SetAttribute] is given
// raw attribute source, the mirror of what Attribute reports, so a literal value
// has to be encoded first with [lolhtml.EscapeAttribute] - otherwise a header
// holding the five characters "&amp;" sets an attribute that reads as one "&".
// The quote that could end the attribute is handled either way; the ampersand is
// the one that changes the value.
//
// Escaping is the whole of the rule for getting the value in, and none of the
// rule for what it then means. The page picks the attribute, so a marker can name
// href, or onclick, and a header of "javascript:alert(document.domain)" is a
// perfectly well-escaped value that runs. Nothing about the position can fix
// that, so those attributes are refused and counted instead: see
// unsafeAttribute.
//
// A title is escapable raw text: a parser does not read markup in it and does
// decode references, so Text is right there too. What is not the same is where
// the marker can go. Inside a title there are no elements - measured: a <span
// data-greet> in a title is five words of text and no handler sees it - so the
// marker has to be an attribute on the title itself, and the value is appended
// rather than replacing the fixed part.
//
// The other seven raw-text elements do not decode references, and there Text
// corrupts the content rather than escaping it: "a < b" becomes "a &lt; b" in the
// source of a script. So a content marker inside one of those is refused and
// counted rather than honoured. [lolhtml.IsRawText] says which elements hold
// content that is not markup; which of them decode references is the extra bit
// this program keeps for itself.
//
// A script is the position with no correct ContentType, and the documented answer
// is not to put the value in the script at all: it goes in a JSON block, where
// encoding/json's own escaping of "<" and ">" means "</script>" cannot end the
// element, and the script reads it from there. This program writes the block; the
// page's own script is what reads it.
//
// One thing this program cannot do anything about, and says instead: a
// personalised page must not be stored in a shared cache. That is a Vary header
// and a cache policy, not a rewrite.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// MaxRunes is how much of a header value is a name. Runes, not bytes: cutting
// bytes can split a character, and every write path refuses a string that ends
// mid-character.
const MaxRunes = 64

// Fallback is what a marker gets when there is no usable value, so a page never
// says "Hello, ".
const Fallback = "there"

// A Result counts what happened to the values as well as to the page, because the
// interesting failures are in the values.
type Result struct {
	// Markers filled, by position.
	Content, Attributes, Scripts int
	// Fallbacks used because no value was supplied or nothing was left of it.
	Fallbacks int
	// Refused markers, which are content markers inside an element whose content
	// is not markup and does not decode references - a script or a style.
	Refused int
	// Unsafe markers, which named an attribute whose value is not text: an
	// event handler, a URL, or a style. See unsafeAttribute.
	Unsafe int
	// Repaired values that were not valid UTF-8, and Stripped ones that held
	// control characters. Truncated ones were longer than MaxRunes.
	Repaired, Stripped, Truncated int
}

func (r Result) String() string {
	return fmt.Sprintf("greet: %d content, %d attributes, %d scripts, %d fallbacks, "+
		"%d refused, %d unsafe; %d values repaired, %d stripped, %d truncated",
		r.Content, r.Attributes, r.Scripts, r.Fallbacks, r.Refused, r.Unsafe,
		r.Repaired, r.Stripped, r.Truncated)
}

// Greet copies src to dst with every marker filled from header.
func Greet(dst io.Writer, src io.Reader, header http.Header) (Result, error) {
	g := &greeter{header: header}
	w, err := lolhtml.NewWriter(dst, g.options()...)
	if err != nil {
		return g.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return g.res, err
	}
	if err := w.Close(); err != nil {
		return g.res, err
	}
	return g.res, nil
}

type greeter struct {
	header http.Header
	res    Result
}

func (g *greeter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("[data-greet]", g.content),
		lolhtml.OnElement("[data-greet-append]", g.append),
		lolhtml.OnElement("[data-greet-prepend]", g.prepend),
		lolhtml.OnElement("*", g.attribute),
		lolhtml.OnElement("script[data-greet-json]", g.script),
	}
}

// content fills an element with the value as text. Text escapes the three
// characters that would be markup, which is the whole of what this position
// needs.
func (g *greeter) content(e *lolhtml.Element) error {
	if g.refuse(e) {
		return nil
	}
	name, _ := e.Attribute("data-greet")
	if err := e.SetInnerContent(g.value(name), lolhtml.Text); err != nil {
		return err
	}
	g.res.Content++
	return nil
}

// append and prepend put the value beside content the page wrote, which is what a
// title needs: the fixed part of it cannot be a marker element, because inside a
// title there are no elements.
//
// Both remove their own marker afterwards. The replacing markers can stay -
// running the pass over its own output fills them with the same value - but an
// appending one that stayed would append again, so the marker is the thing that
// makes this pass repeatable.
func (g *greeter) append(e *lolhtml.Element) error {
	if g.refuse(e) {
		return nil
	}
	name, _ := e.Attribute("data-greet-append")
	if err := e.Append(g.value(name), lolhtml.Text); err != nil {
		return err
	}
	// The marker goes with it. A marker that replaces can stay - filling it twice
	// gives the same page - and one that adds cannot: a second pass over the
	// output would append the greeting again.
	if err := e.RemoveAttribute("data-greet-append"); err != nil {
		return err
	}
	g.res.Content++
	return nil
}

func (g *greeter) prepend(e *lolhtml.Element) error {
	if g.refuse(e) {
		return nil
	}
	name, _ := e.Attribute("data-greet-prepend")
	if err := e.Prepend(g.value(name), lolhtml.Text); err != nil {
		return err
	}
	if err := e.RemoveAttribute("data-greet-prepend"); err != nil {
		return err
	}
	g.res.Content++
	return nil
}

// decodesReferences are the two raw-text elements where a character reference is
// decoded, so Text means what it says. In the others it does not: the escaped
// "&lt;" arrives in the content as four characters.
var decodesReferences = map[string]bool{"textarea": true, "title": true}

// refuse reports whether this element's content cannot take a value at all.
func (g *greeter) refuse(e *lolhtml.Element) bool {
	name := e.TagName()
	if !lolhtml.IsRawText(name) || decodesReferences[name] {
		return false
	}
	g.res.Refused++
	return true
}

// attribute fills any data-greet-<attr> into <attr>.
//
// The value has to be encoded first: SetAttribute takes raw attribute source, so
// a literal "&amp;" would arrive as "&". EscapeAttribute is the encoder, and it
// is not optional even though nothing visible breaks without it.
//
// It is also not sufficient, and that is the half worth copying. The page names
// the attribute, the header names the value, and which attribute it is decides
// what the value does - so an attribute this program will not fill is refused
// before any escaping question comes up.
func (g *greeter) attribute(e *lolhtml.Element) error {
	for _, a := range e.AttributeList() {
		attr, ok := strings.CutPrefix(a.Name, "data-greet-")
		if !ok || attr == "" || reserved[attr] {
			continue
		}
		if unsafeAttribute(strings.ToLower(attr)) {
			// Left as it is, marker and all: this is a page to fix rather than
			// a value to clean up.
			g.res.Unsafe++
			continue
		}
		value := g.value(a.Value)
		if err := e.SetAttribute(attr, lolhtml.EscapeAttribute(value)); err != nil {
			return err
		}
		g.res.Attributes++
	}
	return nil
}

// script fills a JSON block, which is the documented way to get a value into a
// script: neither ContentType is right inside one, and JSON's escaping of "<"
// means the value cannot end the element.
func (g *greeter) script(e *lolhtml.Element) error {
	names, _ := e.Attribute("data-greet-json")
	out := map[string]string{}
	for _, name := range strings.Split(names, " ") {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = g.value(name)
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	// As HTML because JSON is not text to escape: encoding/json has already
	// written "<" as <, so there is nothing in here that can end the script.
	if err := e.SetInnerContent(string(body), lolhtml.HTML); err != nil {
		return err
	}
	g.res.Scripts++
	return nil
}

// unsafeAttribute reports whether filling this attribute from a header would be a
// decision about behaviour rather than about text.
//
// EscapeAttribute answers "can this value end the attribute". It does not answer
// "what does this attribute do with it", and for three groups of names the second
// question is the only one that matters:
//
//   - on* holds script. The value is a program, and quoting it correctly is what
//     makes it run rather than what stops it.
//   - href, src and the rest are fetched or navigated to, so
//     "javascript:alert(document.domain)" in one of them is script as well - and
//     it is escaping-clean, so nothing above notices.
//   - style is CSS, which fetches, and in older engines runs.
//
// Escaping is not sanitising, and there is no escape that makes an attacker's
// choice of URL or program safe. So these are counted and skipped. The list is
// the common names rather than every name a browser has: a program that fills
// attributes a page chooses is better off with a list of the ones it will fill.
func unsafeAttribute(attr string) bool {
	if strings.HasPrefix(attr, "on") {
		return true
	}
	switch attr {
	case "style", "href", "xlink:href", "src", "srcset", "srcdoc", "action",
		"formaction", "data", "poster", "background", "ping", "cite", "manifest":
		return true
	}
	return false
}

// reserved are the data-greet- suffixes that mean something other than "set this
// attribute".
var reserved = map[string]bool{"json": true, "append": true, "prepend": true}

// value is the part that matters: what a header value has to become before any
// insertion rule applies to it.
func (g *greeter) value(name string) string {
	if name == "" {
		g.res.Fallbacks++
		return Fallback
	}
	raw := g.header.Get(name)
	if raw == "" {
		g.res.Fallbacks++
		return Fallback
	}

	// Not valid UTF-8 is not a rewrite that produces a worse page: it is a
	// rewrite that fails. Repair first, and say so.
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "�")
		g.res.Repaired++
	}

	// Control characters are accepted by every write path and belong in no
	// greeting. A NUL becomes U+FFFD in whatever parses the output; an ESC
	// reaches a log as a terminal escape.
	if stripped := strings.Map(dropControl, raw); stripped != raw {
		raw = stripped
		g.res.Stripped++
	}

	raw = strings.TrimSpace(raw)

	// Cut runes, not bytes: a string that ends mid-character is refused by every
	// write path, which would turn a long header into a failed page.
	if utf8.RuneCountInString(raw) > MaxRunes {
		cut := 0
		for i := range raw {
			if cut == MaxRunes {
				raw = raw[:i]
				break
			}
			cut++
		}
		raw = strings.TrimSpace(raw)
		g.res.Truncated++
	}

	if raw == "" {
		g.res.Fallbacks++
		return Fallback
	}
	return raw
}

func dropControl(r rune) rune {
	if r == unicode.ReplacementChar {
		return r
	}
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

func main() {
	header := http.Header{}
	for _, arg := range os.Args[1:] {
		name, value, ok := strings.Cut(arg, ":")
		if !ok {
			fmt.Fprintln(os.Stderr, "usage: greet [Header:value ...] < page")
			os.Exit(2)
		}
		header.Add(name, value)
	}
	res, err := Greet(os.Stdout, os.Stdin, header)
	if err != nil {
		fmt.Fprintln(os.Stderr, "greet:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

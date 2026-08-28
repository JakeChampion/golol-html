// Command redact removes email addresses and phone numbers from a document's
// text and from its attributes.
//
// Attributes are the half the other text programs here do not touch, and they
// bring their own problems.
//
// A value has to be read and written as source. Attribute reports what the
// document holds, references intact, and SetAttribute takes the same - so a value
// can be copied through untouched, and escaping one on the way through is what
// turns the "&" of a query string into "&amp;amp;" the second time round.
//
// But source is not what a value means. A browser decodes the references before
// the value does anything, so a pattern run over the source text is run over the
// wrong string: href="mailto:bob&#64;example.com" is a working link to an
// address whose "@" appears nowhere in the bytes. Matching therefore decodes
// first, exactly as the text half does, and escapes the result on the way back.
// Only a value that matched is written back that way, so nothing merely being
// copied through is round-tripped.
//
// A duplicated attribute cannot be sanitised by writing over it. SetAttribute
// writes the first copy and leaves the rest, so "removing" an address by
// replacing the value leaves the address in the bytes. This program removes and
// re-sets, which costs the attribute its position and is the only way to be sure.
// The value it re-sets is the first copy's, because that is the copy a browser
// uses; a later copy only decides whether the attribute has to be rebuilt.
//
// And mutating while iterating is fine, which is worth knowing because the
// obvious loop over Attributes doing SetAttribute inside it is safe: the walk is
// over the attributes as they were.
//
// Text is the same discipline as the other text programs: accumulate the node
// because a match can be split across chunks, decode once, replace as Text so
// nothing written can become markup.
package main

import (
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"regexp"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Patterns are deliberately conservative: a false negative leaves an address in
// the page, and a false positive removes something that was not one. These are
// the shapes that are unambiguous.
var (
	email = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// A phone number with enough structure to be one: an optional country code
	// and at least seven digits in groups.
	phone = regexp.MustCompile(`\+?\d[\d\-. ]{6,18}\d`)
)

// Replacements are what goes in place of a match.
const (
	emailMask = "[email removed]"
	phoneMask = "[phone removed]"
)

// skipped elements hold content that is not prose, and where a replacement would
// corrupt rather than protect.
var skipped = map[string]bool{
	"script": true, "style": true, "template": true, "noscript": true,
	"iframe": true, "xmp": true, "noembed": true, "noframes": true,
}

// A Result counts what was removed and from where.
type Result struct {
	// TextEmails and TextPhones are removals from text.
	TextEmails, TextPhones int
	// AttrEmails and AttrPhones are removals from attribute values.
	AttrEmails, AttrPhones int
	// Duplicated counts the attributes that had to be removed and re-set
	// because the name appeared more than once, which changes their position.
	Duplicated int
}

// Total is every removal.
func (r Result) Total() int {
	return r.TextEmails + r.TextPhones + r.AttrEmails + r.AttrPhones
}

func (r Result) String() string {
	return fmt.Sprintf("%d removals: %d emails and %d phone numbers in text, "+
		"%d emails and %d phone numbers in attributes; %d attributes moved because "+
		"they were duplicated",
		r.Total(), r.TextEmails, r.TextPhones, r.AttrEmails, r.AttrPhones, r.Duplicated)
}

// Redact copies src to dst with addresses and numbers removed.
func Redact(dst io.Writer, src io.Reader) (Result, error) {
	var res Result
	depth := 0
	var node strings.Builder

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			tag := e.TagName()
			if skipped[tag] && e.CanHaveContent() {
				depth++
				return e.OnEndTag(func(t *lolhtml.EndTag) error {
					if t.Name() != tag {
						return nil
					}
					depth--
					return nil
				})
			}
			if depth > 0 {
				return nil
			}
			return redactAttributes(e, &res)
		}),
		lolhtml.OnDocumentText(func(t *lolhtml.TextChunk) error {
			if depth > 0 {
				return nil
			}
			node.WriteString(t.Text())
			if !t.IsLastInTextNode() {
				t.Remove()
				return nil
			}
			source := node.String()
			node.Reset()
			// Decoded before matching, so an address written with references is
			// found, and written back as Text, which escapes it again.
			text, emails, phones := scrub(stdhtml.UnescapeString(source))
			res.TextEmails += emails
			res.TextPhones += phones
			// Written back either way: the chunks it arrived in are gone.
			return t.Replace(text, lolhtml.Text)
		}),
	)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return res, err
	}
	if err := w.Close(); err != nil {
		return res, err
	}
	return res, nil
}

// redactAttributes rewrites every attribute value that contains a match.
//
// The list is taken first rather than iterating live - not because iterating and
// mutating is unsafe, it is not, but because a duplicated name has to be removed
// and re-set, and doing that inside the walk would mean deciding what a walk over
// a list being rebuilt should mean.
func redactAttributes(e *lolhtml.Element, res *Result) error {
	list := e.AttributeList()

	// How many times each name appears, so a duplicate can be handled.
	count := make(map[string]int, len(list))
	for _, a := range list {
		count[strings.ToLower(a.Name)]++
	}

	done := make(map[string]bool, len(list))
	for i, a := range list {
		name := strings.ToLower(a.Name)
		if done[name] {
			continue
		}
		// Marked here rather than after the match, so that a name whose first
		// copy is clean is finished with. Leaving it unmarked let a later copy
		// take the decision, and the value rebuilt from it then replaced every
		// copy - which is how href="/safe" href="mailto:..." came out pointing
		// at the removed mailto.
		done[name] = true

		// The first copy is the one a browser uses: the HTML parsing
		// specification calls a repeat a parse error and drops it. So it is the
		// first copy's value that goes back, whichever copy the address was in.
		value, emails, phones := scrubAttr(a.Value)
		// A later copy is inert to a browser, but its bytes are still an address
		// sitting in the page, so it counts towards the removal even though its
		// value is discarded.
		for _, dup := range list[i+1:] {
			if strings.ToLower(dup.Name) != name {
				continue
			}
			_, e, p := scrubAttr(dup.Value)
			emails += e
			phones += p
		}
		if emails+phones == 0 {
			continue
		}
		res.AttrEmails += emails
		res.AttrPhones += phones

		if count[name] > 1 {
			// SetAttribute writes the first copy and leaves the rest, so
			// "removing" the address by writing over it would leave it in the
			// bytes. Removing takes every copy.
			res.Duplicated++
			if err := e.RemoveAttribute(name); err != nil {
				return err
			}
		}
		if err := e.SetAttribute(name, value); err != nil {
			return err
		}
	}
	return nil
}

// scrubAttr scrubs one attribute value, which is source text, and gives back
// source text.
//
// The decode is the whole point. Attribute reports the value with its character
// references left encoded, and a browser decodes them before the value means
// anything: mailto:bob&#64;example.com holds no "@" at all, so the pattern does
// not see the address and it stays in the page, fully working. Anything that
// decides something about an attribute value - a scheme check as much as a
// pattern match - has to decode first.
//
// The result is escaped again on the way back, because SetAttribute takes source
// and escapes only the double quote: a "&" left bare would change what the rest
// of the value means. A value with no match is returned exactly as it arrived
// rather than round-tripped, so an untouched attribute never pays for the
// difference between this decoder and a browser's.
func scrubAttr(source string) (string, int, int) {
	out, emails, phones := scrub(stdhtml.UnescapeString(source))
	if emails+phones == 0 {
		return source, 0, 0
	}
	return lolhtml.EscapeAttribute(out), emails, phones
}

// scrub replaces every match in s and says how many of each it replaced.
func scrub(s string) (string, int, int) {
	emails := len(email.FindAllStringIndex(s, -1))
	out := email.ReplaceAllString(s, emailMask)
	phones := len(phone.FindAllStringIndex(out, -1))
	out = phone.ReplaceAllString(out, phoneMask)
	return out, emails, phones
}

func main() {
	res, err := Redact(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "redact:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "redact:", res)
}

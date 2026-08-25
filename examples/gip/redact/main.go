// Command redact removes email addresses and phone numbers from a document's
// text and from its attributes.
//
// Attributes are the half the other text programs here do not touch, and they
// bring their own problems.
//
// A value has to be read and written as source. Attribute reports what the
// document holds, references intact, and SetAttribute takes the same - so a value
// can be read, changed and written back without escaping anything, and must not
// be decoded on the way through or the "&" in a query string comes back as
// "&amp;amp;" the second time round.
//
// A duplicated attribute cannot be sanitised by writing over it. SetAttribute
// writes the first copy and leaves the rest, so "removing" an address by
// replacing the value leaves the address in the bytes. This program removes and
// re-sets, which costs the attribute its position and is the only way to be sure.
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
	for _, a := range list {
		name := strings.ToLower(a.Name)
		if done[name] {
			continue
		}
		// Attribute values are source, and are written back as source, so
		// nothing is decoded here - decoding and re-encoding would turn "&" into
		// "&amp;amp;" on a second pass.
		value, emails, phones := scrub(a.Value)
		if emails+phones == 0 {
			continue
		}
		res.AttrEmails += emails
		res.AttrPhones += phones
		done[name] = true

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

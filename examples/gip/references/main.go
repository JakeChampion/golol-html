// Command references decodes the character references a document did not need.
//
//	<p>Caf&eacute; &amp; bar &#8212; open</p>
//	  ->  <p>Cafe&#769; &amp; bar - open</p>   (with the real characters, not these)
//
// An escaped character says the same thing as the character, so a document full of
// &eacute; and &#8212; is bigger and harder to read for no benefit - and the ones that
// do carry meaning are few: & and < in text, & and the quote in an attribute value.
// This program decodes the rest and leaves those alone.
//
// # Which references a browser decodes is not one rule but two
//
// In text, a named reference is decoded whether or not it ends in a semicolon, and the
// match is the longest one - so "&notit;" is the not sign followed by "it;". In an
// attribute value the same reference without its semicolon is not a reference at all
// when the character after it is "=" or ASCII alphanumeric, which is the rule that
// keeps "?a=1&copy=2" a URL with a parameter called copy rather than one with a
// copyright sign in it.
//
// So the standard library's html.UnescapeString is the parser's decoder for text and
// not for attribute values: measured, it turns "?a=1&notit=2" into something a browser
// never has. This program implements the attribute rule itself, which is the
// difference between normalising a URL and changing it. See
// differential/attrrefs_test.go.
//
// # Writing the result back
//
// The decoded text goes back with [lolhtml.HTML] rather than [lolhtml.Text], because
// Text escapes the three markup characters and would put back the references just
// removed - and it escapes ">", which needs no escaping in text at all. So this
// program decides for itself what stays encoded: "&" and "<" in text, "&" and the
// double quote in an attribute, and nothing else. That is the rule for moving a value
// into a context, applied to a value that is staying where it is.
package main

import (
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// RawText is the selector for the elements whose content an HTML parser does not read
// as markup. The names come from [lolhtml.IsRawText]; plaintext is here too, and
// nothing closes it, so everything after one is left alone.
const RawText = "script,style,iframe,noembed,noframes,noscript,xmp,plaintext,textarea,title"

// Options are the decisions a caller gets to make.
type Options struct {
	// Keep are the references to leave encoded whatever their context, keyed by what
	// the document wrote, lower-cased. The invisible characters are here by default:
	// the reference is the only thing that shows them in the source.
	Keep map[string]bool
	// GT leaves &gt; encoded in text. A bare ">" is valid character data, so decoding
	// it is safe - but a diff full of them is noise for some callers.
	GT bool
	// Attributes normalises attribute values as well as text.
	Attributes bool
}

// DefaultKeep are the references worth keeping as references: every one of them stands
// for a character that is invisible on the page and in the source.
var DefaultKeep = map[string]bool{
	"&nbsp;": true, "&nbsp": true, "&#160;": true, "&#xa0;": true,
	"&shy;": true, "&zwj;": true, "&zwnj;": true, "&lrm;": true, "&rlm;": true,
	"&ensp;": true, "&emsp;": true, "&thinsp;": true, "&#8203;": true,
}

// Result is what happened.
type Result struct {
	Text     int // references decoded in text
	Attrs    int // references decoded in attribute values
	Kept     int // references left encoded
	NotARef  int // "&" sequences that are not references where they sit
	Elements int // elements whose attributes were rewritten
}

func (r Result) String() string {
	return fmt.Sprintf("references: decoded %d in text and %d in %d attributes; kept %d, left %d that are not references",
		r.Text, r.Attrs, r.Elements, r.Kept, r.NotARef)
}

// Decoding are the two raw-text elements whose content does decode references. The
// other eight hold characters that are never references, so nothing in them is
// needlessly escaped and touching their text would change what they say.
var Decoding = map[string]bool{"textarea": true, "title": true}

type normaliser struct {
	opts Options
	res  Result
	text strings.Builder
	raw  int // open raw-text elements whose content is not references
}

// rawText tracks the elements whose text must be left alone. A text chunk cannot say
// which element it came from - lol-html hands it over without one - so a program that
// needs to know keeps the count itself.
func (n *normaliser) rawText(e *lolhtml.Element) error {
	if Decoding[e.TagName()] {
		return nil
	}
	n.raw++
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		n.raw--
		return nil
	})
}

// textChunk accumulates a text node and rewrites it whole, because a reference can be
// split across chunks.
func (n *normaliser) textChunk(c *lolhtml.TextChunk) error {
	if n.raw > 0 {
		return nil
	}
	n.text.WriteString(c.Text())
	if !c.IsLastInTextNode() {
		c.Remove()
		return nil
	}
	src := n.text.String()
	n.text.Reset()
	return c.Replace(n.decode(src, inText), lolhtml.HTML)
}

func (n *normaliser) element(e *lolhtml.Element) error {
	changed := false
	// AttributeList yields every copy of a repeated attribute; SetAttribute replaces the
	// first. So a later copy's decoded value would be written onto the first one, which is
	// the copy a browser keeps: <a href="/x" href="/y&copy;z"> came out as
	// <a href="/y&copy;z" ...> decoded onto the first, and the link changed where it points.
	// Only the first copy of a name is decoded, because it is the only one that means
	// anything. See "An attribute can appear twice" in the package documentation.
	seen := map[string]bool{}
	for _, a := range e.AttributeList() {
		if seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		if !strings.Contains(a.Value, "&") {
			continue
		}
		out := n.decode(a.Value, inAttribute)
		if out == a.Value {
			continue
		}
		if err := e.SetAttribute(a.NamePreserveCase, out); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		n.res.Elements++
	}
	return nil
}

// context is which of the two decoding rules applies.
type context int

const (
	inText context = iota
	inAttribute
)

// decode rewrites the references in one value. It walks the string itself rather than
// handing the whole of it to html.UnescapeString, for two reasons: the attribute rule
// is not the standard library's, and the characters that have to stay encoded have to
// be recognised rather than decoded and escaped again.
func (n *normaliser) decode(s string, ctx context) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		raw, decoded, ok := n.reference(s[i:], ctx)
		if !ok {
			// Not a reference where it sits: a bare ampersand, an unknown name, or
			// a name without its semicolon in an attribute. It stays as it is.
			n.res.NotARef++
			b.WriteByte('&')
			i++
			continue
		}
		if n.keep(raw, decoded, ctx) {
			n.res.Kept++
			b.WriteString(raw)
			i += len(raw)
			continue
		}
		b.WriteString(decoded)
		i += len(raw)
		if ctx == inText {
			n.res.Text++
		} else {
			n.res.Attrs++
		}
	}
	return b.String()
}

// reference reads one reference from the start of s, returning what the document wrote
// and what it means. It reports false when the sequence is not a reference here.
func (n *normaliser) reference(s string, ctx context) (raw, decoded string, ok bool) {
	if len(s) < 2 || s[0] != '&' {
		return "", "", false
	}
	end := 1
	numeric := false
	if s[1] == '#' {
		numeric = true
		end = 2
		if end < len(s) && (s[end] == 'x' || s[end] == 'X') {
			end++
			for end < len(s) && isHex(s[end]) {
				end++
			}
			if end == 3 {
				return "", "", false // "&#x" with no digits
			}
		} else {
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			if end == 2 {
				return "", "", false // "&#" with no digits
			}
		}
	} else {
		run := 1
		for run < len(s) && isAlnum(s[run]) {
			run++
		}
		if run == 1 {
			return "", "", false // a bare ampersand
		}
		// The table's names are matched longest-first, and the match can be a prefix
		// of the run: "&copy2" is the copyright sign followed by a "2". So the name
		// ends where the longest known one ends, and the attribute rule below looks
		// at the character after that rather than after the run.
		end = 0
		for k := run; k > 1; k-- {
			if known(s[1:k]) {
				end = k
				break
			}
		}
		if end == 0 {
			return "", "", false // no name the table has
		}
	}
	semicolon := end < len(s) && s[end] == ';'
	raw = s[:end]
	if semicolon {
		raw = s[:end+1]
	} else if ctx == inAttribute && !numeric {
		// The attribute rule: a named reference without its semicolon is not a
		// reference when what follows is "=" or ASCII alphanumeric.
		if end < len(s) && (s[end] == '=' || isAlnum(s[end])) {
			return "", "", false
		}
	}
	decoded = stdhtml.UnescapeString(raw)
	if decoded == raw {
		return "", "", false // a name the table does not have
	}
	return raw, decoded, true
}

// known reports whether the table has exactly this name. The standard library's
// decoder is the table - asking it beats carrying a copy of 2231 names - but it matches
// the longest prefix and leaves the rest, so "&copy2;" comes back as the copyright sign
// followed by "2;". An exact name is one whose decoded form is the character alone, and
// no reference in the table stands for more than two code points, which is the test.
func known(name string) bool {
	in := "&" + name + ";"
	decoded := stdhtml.UnescapeString(in)
	return decoded != in && utf8.RuneCountInString(decoded) <= 2
}

// keep reports whether a reference should stay encoded: because the caller said so,
// because the character it stands for would be markup here, or because what it stands
// for is not the character the document meant.
func (n *normaliser) keep(raw, decoded string, ctx context) bool {
	if n.opts.Keep[strings.ToLower(raw)] {
		return true
	}
	r, size := utf8.DecodeRuneInString(decoded)
	if r == utf8.RuneError {
		// An out-of-range or surrogate number, which decodes to the replacement
		// character: keep what the document had rather than write a different one.
		return true
	}
	if len(decoded) != size {
		// More than one code point, which is not the same as "no markup in it". The
		// table has &nvlt; = "<" U+20D2 and &nvgt; = ">" U+20D2, so the test has to run
		// over every character rather than over the first one. It matters because the
		// result is written back as HTML - putting the references back is the one thing
		// this program must not do - so a "<" arriving this way was a tag in the output,
		// and "&nvlt;/title&gt;" ended a title element that the source had not ended.
		for _, r := range decoded {
			if n.isMarkup(r, ctx) {
				return true
			}
		}
		return false
	}
	return n.isMarkup(r, ctx)
}

// isMarkup reports whether a decoded character has to stay encoded because it would be
// markup where it is going.
func (n *normaliser) isMarkup(r rune, ctx context) bool {
	switch r {
	case '&', '<':
		return true // markup in both contexts
	case '>':
		return ctx == inText && n.opts.GT
	case '"':
		return ctx == inAttribute // the library writes attribute values in double quotes
	}
	return false
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func (n *normaliser) options() []lolhtml.Option {
	opts := []lolhtml.Option{
		lolhtml.OnElement(RawText, n.rawText),
		lolhtml.OnDocumentText(n.textChunk),
	}
	if n.opts.Attributes {
		opts = append(opts, lolhtml.OnElement("*", n.element))
	}
	return opts
}

// Normalise copies src to dst, decoding the references it did not need.
func Normalise(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Keep == nil {
		opts.Keep = DefaultKeep
	}
	n := &normaliser{opts: opts}
	w, err := lolhtml.NewWriter(dst, n.options()...)
	if err != nil {
		return n.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return n.res, err
	}
	if err := w.Close(); err != nil {
		return n.res, err
	}
	return n.res, nil
}

func main() {
	var opts Options
	keep := flag.String("keep", "", "comma-separated references to leave encoded, in addition to the defaults")
	flag.BoolVar(&opts.GT, "keep-gt", false, "leave &gt; encoded in text")
	flag.BoolVar(&opts.Attributes, "attributes", false, "normalise attribute values too")
	flag.Parse()

	opts.Keep = map[string]bool{}
	for k := range DefaultKeep {
		opts.Keep[k] = true
	}
	for _, k := range strings.Split(*keep, ",") {
		if k = strings.TrimSpace(strings.ToLower(k)); k != "" {
			opts.Keep[k] = true
		}
	}

	res, err := Normalise(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "references:", err)
		os.Exit(2)
	}
}

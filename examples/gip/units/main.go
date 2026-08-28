// Command units converts imperial quantities in prose to metric, wrapping each
// conversion in a span whose title keeps what the page said.
//
//	<p>a 12 mile walk</p>  ->  <p>a <span class="unit" title="12 mile">19.3 km</span> walk</p>
//
// Three parts of it are decisions rather than arithmetic.
//
// Which units. Only the ones that cannot mean something else: "in" is a word, so
// "3 in 5 people" would become a length; "oz" is fluid ounces as often as it is
// weight; "gal" is two different volumes on two sides of an ocean; a bare "F"
// after a number is more likely to be a grade or a note than Fahrenheit. Each of
// those is left alone and counted, so a page that wanted them converted shows up
// as a number rather than as silence. What is converted is the list in Units,
// which is short on purpose.
//
// Where. Not inside anything whose content is not prose: the elements
// [lolhtml.IsRawText] names, and code, kbd, samp, var and pre, where "12 mi" is
// probably an identifier. And not inside a conversion this program already made,
// which is what makes running it twice a no-op.
//
// Whose text. A text handler is handed bytes, and a reader is shown something
// slightly different: a CR or a CRLF is a line feed to every parser, and a NUL in
// text is dropped - while a NUL in an attribute value is kept. So matching the
// bytes would miss "12\r\nmiles", which is a quantity on the page, and copying
// the bytes into a title would quote something nobody was shown.
//
// So a node is normalised the way a parser would normalise it before anything is
// matched, and a node that gets a conversion is written back in that form. A node
// with no conversion in it is written back byte for byte, so the normalisation
// only ever reaches text this program was already rewriting - and there it
// changes nothing a reader sees. See the package documentation on source being
// unpreprocessed, and differential/preprocess_test.go for the measurement.
//
// The rest is the discipline these programs share: a quantity spans chunks, so the
// text node is accumulated to [lolhtml.TextChunk.IsLastInTextNode] before anything
// is matched; the untouched parts of the node are written back verbatim, because
// what a text handler reports is source and escaping it again would double every
// ampersand; and the only thing written as markup is the span, whose title is
// escaped for the one character that could end it.
package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Unit is one convertible spelling.
type Unit struct {
	// Names are the spellings that mean this unit, matched case-insensitively for
	// ASCII, which is all these names are.
	Names []string
	// Factor and Offset convert: metric = value*Factor + Offset.
	Factor, Offset float64
	// To is the metric unit's symbol.
	To string
	// Decimals is how many places the result gets.
	Decimals int
}

// Units are the conversions this program will make. Short on purpose: every
// entry has to be a spelling that cannot mean something else.
var Units = []Unit{
	{[]string{"mile", "miles", "mi"}, 1.609344, 0, "km", 1},
	{[]string{"yard", "yards", "yd"}, 0.9144, 0, "m", 1},
	{[]string{"foot", "feet", "ft"}, 0.3048, 0, "m", 2},
	{[]string{"inch", "inches"}, 2.54, 0, "cm", 1},
	{[]string{"pound", "pounds", "lb", "lbs"}, 0.45359237, 0, "kg", 2},
	{[]string{"mph"}, 1.609344, 0, "km/h", 0},
	{[]string{"°f", "℉"}, 5.0 / 9.0, -32 * 5.0 / 9.0, "°C", 1},
	{[]string{"gallon", "gallons"}, 3.785411784, 0, "L", 1}, // US, and said so below
}

// Ambiguous are the spellings this program will not convert, and why. They are
// counted so that a page using them is visible.
var Ambiguous = map[string]string{
	"in":   `"in" is a word: "3 in 5 people" is not a length`,
	"oz":   `"oz" is fluid ounces as often as it is weight`,
	"gal":  `"gal" is two different volumes on two sides of an ocean`,
	"f":    `a bare "F" after a number is more often a grade than Fahrenheit`,
	"pt":   `"pt" is pints, points and typographic points`,
	"ton":  `a ton is short, long or metric depending on where you are`,
	"tons": `a ton is short, long or metric depending on where you are`,
}

// Skip are the elements whose content is not prose, on top of everything
// lolhtml.IsRawText names.
var Skip = map[string]bool{"code": true, "kbd": true, "samp": true, "var": true, "pre": true}

// A Result counts what happened.
type Result struct {
	// Converted quantities, by the unit's target symbol.
	Converted map[string]int
	// Ambiguous spellings seen after a number and left alone.
	Ambiguous map[string]int
	// Regions skipped, and Nodes of text examined.
	Regions, Nodes int
	// Normalised titles: originals that held a CR or a NUL, so the title had to be
	// normalised to say what the page said.
	Normalised int
}

func (r Result) String() string {
	total := 0
	units := make([]string, 0, len(r.Converted))
	for to, n := range r.Converted {
		total += n
		units = append(units, fmt.Sprintf("%d %s", n, to))
	}
	sort.Strings(units)
	amb := make([]string, 0, len(r.Ambiguous))
	for name, n := range r.Ambiguous {
		amb = append(amb, fmt.Sprintf("%d %s", n, name))
	}
	sort.Strings(amb)
	s := fmt.Sprintf("units: %d conversions (%s) in %d text nodes; %d regions skipped",
		total, strings.Join(units, ", "), r.Nodes, r.Regions)
	if len(amb) > 0 {
		s += "; left alone as ambiguous: " + strings.Join(amb, ", ")
	}
	if r.Normalised > 0 {
		s += fmt.Sprintf("; %d titles normalised", r.Normalised)
	}
	return s
}

// Convert copies src to dst with the quantities converted.
func Convert(dst io.Writer, src io.Reader) (Result, error) {
	c := &converter{res: Result{Converted: map[string]int{}, Ambiguous: map[string]int{}}}
	w, err := lolhtml.NewWriter(dst, c.options()...)
	if err != nil {
		return c.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	return c.res, nil
}

type converter struct {
	res Result
	// depth is how many skipped elements this position is inside.
	depth int
	// node accumulates a text node, because a quantity can straddle chunks.
	//
	// The text is kept here, not the chunks. A *TextChunk is valid only inside the
	// handler that received it, and a retained one answers every getter with a zero
	// value and silently does nothing when told to Remove - so holding the chunks of
	// a node in a slice, to remove them once the whole node is known, removes nothing
	// and duplicates every text node, with no error anywhere to say so. Each chunk is
	// removed in its own handler instead; see text below.
	node strings.Builder
}

func (c *converter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.element),
		lolhtml.OnDocumentText(c.text),
	}
}

func (c *converter) element(e *lolhtml.Element) error {
	name := e.TagName()
	skip := Skip[name] || lolhtml.IsRawText(name)
	if !skip {
		// A conversion this program made: skipping it is what makes a second pass
		// a no-op.
		if _, ok := e.Attribute("data-unit"); ok {
			skip = true
		}
	}
	if !skip {
		return nil
	}
	c.res.Regions++
	if !e.CanHaveContent() {
		// A self-closing foreign element - <svg><title/>, <svg><style/> - has no
		// content to skip and no end tag to wait for, so OnEndTag would return an
		// error rather than register anything. The counter must not go up either:
		// the decrement lives in that handler, so raising it here would leave depth
		// permanently above zero and text() returns immediately whenever it is,
		// which silently disables the rewrite for the whole rest of the document.
		return nil
	}
	c.depth++
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		c.depth--
		return nil
	})
}

// text accumulates the node and rewrites it at its last chunk. Every chunk is
// removed and the whole node written back, converted or not: writing back only
// when something changed would mean deciding, for each chunk, whether it had
// already been removed.
func (c *converter) text(t *lolhtml.TextChunk) error {
	if c.depth > 0 {
		return nil
	}
	c.node.WriteString(t.Text())
	if !t.IsLastInTextNode() {
		t.Remove()
		return nil
	}
	node := c.node.String()
	c.node.Reset()
	t.Remove()
	if node == "" {
		return nil
	}
	c.res.Nodes++

	// Matched against what a parser would show a reader rather than against the
	// bytes: a CR is a LF to a parser and a NUL is dropped, so "12\r\nmiles" and
	// "12\x00 miles" are quantities on the page even though the bytes do not say
	// so. A node this program rewrites therefore comes out normalised - which
	// changes nothing a reader sees - and a node it does not touch is written back
	// exactly as it arrived.
	text, changed := normalise(node)
	out, conversions := c.convert(text)
	if conversions == 0 {
		return t.Before(node, lolhtml.HTML)
	}
	if changed {
		c.res.Normalised++
	}
	return t.Before(out, lolhtml.HTML)
}

// convert rewrites one text node, returning markup and how many quantities it
// found.
func (c *converter) convert(node string) (string, int) {
	var b strings.Builder
	found := 0
	i := 0
	for i < len(node) {
		start, end, value, unit, ok := c.match(node, i)
		if !ok {
			b.WriteString(node[i:])
			break
		}
		found++
		// Everything up to the quantity, verbatim: it is source, and escaping it
		// again would double its ampersands.
		b.WriteString(node[i:start])
		converted := unit.convert(value)
		c.res.Converted[unit.To]++
		// The original is already what a parser would show, because the node was
		// normalised before matching; the only thing left is the one character
		// that could end the attribute this program is writing.
		fmt.Fprintf(&b, `<span class="unit" data-unit="%s" title="%s">%s</span>`,
			unit.To, attrValue(node[start:end]), converted)
		i = end
	}
	return b.String(), found
}

// match finds the next quantity at or after i: a number, optional space, a unit
// name, on word boundaries.
func (c *converter) match(node string, from int) (start, end int, value float64, unit Unit, ok bool) {
	for i := from; i < len(node); i++ {
		if !isDigit(node[i]) {
			continue
		}
		// A digit that is not the start of a number is part of one already seen -
		// including one a separator has been read past, so "12.5.3" is a version
		// number and not a quantity of 3.
		if i > 0 && isWordByte(node[i-1]) {
			continue
		}
		if i > 1 && (node[i-1] == '.' || node[i-1] == ',') && isDigit(node[i-2]) {
			continue
		}
		numEnd, value, ok := readNumber(node, i)
		if !ok {
			continue
		}
		// One optional space between the number and the unit, of any kind: prose
		// wraps, so a newline there is as ordinary as a space, and a non-breaking
		// space is what a careful page uses.
		unitStart := numEnd
		for unitStart < len(node) {
			b := node[unitStart]
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' {
				unitStart++
				continue
			}
			if strings.HasPrefix(node[unitStart:], "\u00a0") {
				unitStart += len("\u00a0")
				continue
			}
			break
		}
		name, nameEnd := readUnitName(node, unitStart)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if why, bad := Ambiguous[lower]; bad {
			_ = why
			c.res.Ambiguous[lower]++
			continue
		}
		for _, u := range Units {
			for _, n := range u.Names {
				if lower != n {
					continue
				}
				return i, nameEnd, value, u, true
			}
		}
	}
	return 0, 0, 0, Unit{}, false
}

// readNumber reads digits with optional comma groups and one decimal part.
func readNumber(s string, i int) (end int, value float64, ok bool) {
	j := i
	var digits strings.Builder
	for j < len(s) {
		switch {
		case isDigit(s[j]):
			digits.WriteByte(s[j])
			j++
		case s[j] == ',' && j+1 < len(s) && isDigit(s[j+1]) && digits.Len() > 0:
			j++ // a thousands separator
		case s[j] == '.' && j+1 < len(s) && isDigit(s[j+1]) && !strings.Contains(digits.String(), "."):
			digits.WriteByte('.')
			j++
		default:
			goto done
		}
	}
done:
	if digits.Len() == 0 {
		return 0, 0, false
	}
	n, err := strconv.ParseFloat(digits.String(), 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, 0, false
	}
	return j, n, true
}

// readUnitName reads a unit spelling: the degree sign and letters, ending on a
// word boundary so that "miles" and "milestones" are different words.
func readUnitName(s string, i int) (name string, end int) {
	j := i
	if strings.HasPrefix(s[j:], "°") {
		j += len("°")
	}
	for j < len(s) {
		r, size := utf8.DecodeRuneInString(s[j:])
		if !unicode.IsLetter(r) && r != '/' {
			break
		}
		j += size
	}
	if j == i {
		return "", i
	}
	// The character after has to be a boundary, or this is a longer word.
	if j < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[j:]); unicode.IsLetter(r) || unicode.IsDigit(r) {
			return "", i
		}
	}
	return s[i:j], j
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isWordByte(b byte) bool {
	return isDigit(b) || (b|0x20 >= 'a' && b|0x20 <= 'z') || b >= 0x80
}

// convert applies the factor and formats the result.
func (u Unit) convert(value float64) string {
	n := value*u.Factor + u.Offset
	pow := math.Pow(10, float64(u.Decimals))
	n = math.Round(n*pow) / pow
	s := strconv.FormatFloat(n, 'f', u.Decimals, 64)
	// A trailing zero after a decimal point says more precision than the input
	// had, so it goes.
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s + " " + u.To
}

// normalise turns a text node into what a parser would show for it: a CR or a
// CRLF is a LF, and a NUL is dropped. Everything this program does - matching a
// quantity, quoting the original in a title - is in the reader's terms, and the
// bytes are not.
func normalise(s string) (string, bool) {
	out := strings.ReplaceAll(s, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")
	out = strings.ReplaceAll(out, "\x00", "")
	return out, out != s
}

// attrValue prepares a source string to sit inside a double-quoted attribute.
// The value is already attribute source - references and all - so the only
// character that has to change is the one that would end the attribute.
func attrValue(s string) string {
	return strings.ReplaceAll(s, `"`, "&quot;")
}

func main() {
	res, err := Convert(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "units:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

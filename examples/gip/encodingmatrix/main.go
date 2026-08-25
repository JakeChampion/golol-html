// Command encodingmatrix runs a document through every encoding the rewriter accepts
// and compares what comes out.
//
//	$ encodingmatrix < page.html
//	structure: identical in all 36 encodings
//	readings:  14 distinct readings of the text
//	  shift_jis, euc-jp, gbk, gb18030, big5, euc-kr   京都 (and 3 more spans)
//	  windows-1252, iso-8859-1                        ‹žì (and 3 more spans)
//	  ...
//
// Two questions, and they have different answers. Which bytes are markup - where the
// tags are, what they are called, which spans are text - is the same in every encoding.
// What those bytes say is different in almost every one. The first is the property worth
// checking on a corpus; the second is the reason a document has to declare its encoding.
//
// # Why the structure is the interesting half
//
// In a browser, a legacy multi-byte encoding can hide a markup character: a lead byte
// takes the byte after it, and if that byte is a quote or a ">" then a filter reading
// the bytes and a browser reading the characters disagree about where the tag ended.
// That is a whole class of cross-site scripting, and it is the reason to check rather
// than assume.
//
// Measured, it does not happen here: over a corpus that puts every markup character
// after nine different lead bytes, all 36 accepted encodings agree with each other and
// with x-user-defined - which is single-byte and maps every high byte to a character of
// its own, so it cannot combine bytes even in principle. The structure this program
// compares is the byte spans of the elements, their names, their attribute names, and
// the byte spans of the text and comments, all from [lolhtml.Element.SourceLocation],
// which reports byte offsets whatever the encoding.
//
// # What it will not tell you
//
// Which encoding a document is actually in. This program says what each label would
// make of it, and a caller who wants to guess has to decide what looks like language -
// which is not a question a rewriter can answer. What it does say is when a label makes
// the text undecodable: a byte the decoder cannot use is reported as U+FFFD and counted
// here, and the label with the fewest of those is a reasonable first guess.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Encodings is every canonical name in the WHATWG index. The four the rewriter refuses
// are here too, so the report can say so rather than leaving them out silently.
var Encodings = strings.Fields(`
utf-8 ibm866 iso-8859-2 iso-8859-3 iso-8859-4 iso-8859-5 iso-8859-6 iso-8859-7
iso-8859-8 iso-8859-8-i iso-8859-10 iso-8859-13 iso-8859-14 iso-8859-15
iso-8859-16 koi8-r koi8-u macintosh windows-874 windows-1250 windows-1251
windows-1252 windows-1253 windows-1254 windows-1255 windows-1256 windows-1257
windows-1258 x-mac-cyrillic gbk gb18030 big5 euc-jp iso-2022-jp shift_jis
euc-kr replacement utf-16be utf-16le x-user-defined
`)

// Baseline is the encoding every other one is compared against for structure: it is
// single-byte, and every high byte has a character of its own, so it cannot combine
// bytes even in principle.
const Baseline = "x-user-defined"

// Reading is what one encoding made of the document: the structure it found, which
// should be the same everywhere, and the characters it read, which should not.
type Reading struct {
	Encoding    string
	Refused     string // why the rewriter would not use this label
	Structure   string // the byte spans and names, for comparison
	Text        []string
	Replacement int // characters the decoder could not produce
}

// Result is the whole matrix.
type Result struct {
	Readings  []Reading
	Refused   []string
	Different []string // encodings whose structure differs from the baseline
	Groups    map[string][]string
}

// OK reports whether every accepted encoding agreed about the markup.
func (r Result) OK() bool { return len(r.Different) == 0 }

func (r Result) String() string {
	var b strings.Builder
	accepted := len(r.Readings) - len(r.Refused)
	if r.OK() {
		fmt.Fprintf(&b, "structure: identical in all %d accepted encodings\n", accepted)
	} else {
		fmt.Fprintf(&b, "structure: %d of %d encodings disagree: %s\n",
			len(r.Different), accepted, strings.Join(r.Different, " "))
	}
	fmt.Fprintf(&b, "readings:  %d distinct readings of the text\n", len(r.Groups))
	keys := make([]string, 0, len(r.Groups))
	for k := range r.Groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(r.Groups[keys[i]]) != len(r.Groups[keys[j]]) {
			return len(r.Groups[keys[i]]) > len(r.Groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-46s %s\n", clip(k, 46), strings.Join(r.Groups[k], ", "))
	}
	if len(r.Refused) > 0 {
		fmt.Fprintf(&b, "refused:   %s\n", strings.Join(r.Refused, " "))
	}
	return b.String()
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// read runs the document under one encoding and records both answers.
func read(doc []byte, encoding string) (Reading, error) {
	out := Reading{Encoding: encoding}
	var structure strings.Builder
	var text []string
	opts := []lolhtml.Option{
		lolhtml.WithEncoding(encoding),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			l := e.SourceLocation()
			fmt.Fprintf(&structure, "el %s [%d:%d]", e.TagName(), l.Start, l.End)
			for _, a := range e.AttributeList() {
				fmt.Fprintf(&structure, " @%s", a.Name)
				// The name is structure and the value is characters, so the value
				// goes in the reading rather than in the comparison.
				if v := strings.TrimSpace(a.Value); v != "" {
					text = append(text, v)
				}
			}
			structure.WriteString("\n")
			return nil
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			l := c.SourceLocation()
			if l.Start == l.End {
				return nil
			}
			fmt.Fprintf(&structure, "text [%d:%d]\n", l.Start, l.End)
			if s := strings.TrimSpace(c.Text()); s != "" {
				text = append(text, s)
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			l := c.SourceLocation()
			fmt.Fprintf(&structure, "comment [%d:%d]\n", l.Start, l.End)
			return nil
		}),
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		out.Refused = err.Error()
		return out, nil // a refused label is an answer, not a failure
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return out, err
	}
	if err := w.Close(); err != nil {
		return out, err
	}
	out.Structure = structure.String()
	out.Text = text
	for _, s := range text {
		out.Replacement += strings.Count(s, "�")
	}
	return out, nil
}

// Matrix reads the document once per encoding and compares the results.
func Matrix(doc []byte) (Result, error) {
	var res Result
	res.Groups = map[string][]string{}
	var baseline string
	readings := make([]Reading, 0, len(Encodings))
	for _, enc := range Encodings {
		r, err := read(doc, enc)
		if err != nil {
			return res, fmt.Errorf("%s: %w", enc, err)
		}
		readings = append(readings, r)
		if r.Refused != "" {
			res.Refused = append(res.Refused, enc)
			continue
		}
		if enc == Baseline {
			baseline = r.Structure
		}
	}
	res.Readings = readings
	for _, r := range readings {
		if r.Refused != "" {
			continue
		}
		if baseline != "" && r.Structure != baseline {
			res.Different = append(res.Different, r.Encoding)
		}
		key := strings.Join(r.Text, " | ")
		res.Groups[key] = append(res.Groups[key], r.Encoding)
	}
	return res, nil
}

// Corpus is the document the program uses when it is given none: every markup character
// after each of nine lead bytes, which is the shape a multi-byte encoding could hide one
// in.
func Corpus() []byte {
	var b strings.Builder
	b.WriteString(`<div class="c" data-x='y'>`)
	for _, lead := range []byte{0x81, 0x8b, 0xa1, 0xc2, 0xe0, 0xf0, 0xfe, 0x80, 0x9f} {
		for _, m := range []byte{'>', '<', '"', '\'', '&', '=', '/', ' ', 0x5c} {
			b.WriteString("<p a=\"")
			b.WriteByte(lead)
			if m != '"' {
				b.WriteByte(m)
			}
			b.WriteString("\">t</p>")
		}
	}
	b.WriteString("<!--c--><span>x</span></div>")
	return []byte(b.String())
}

func main() {
	corpus := flag.Bool("corpus", false, "use the built-in adversarial corpus instead of stdin")
	guess := flag.Bool("guess", false, "rank the labels by how few undecodable bytes they leave")
	flag.Parse()

	var doc []byte
	var err error
	if *corpus {
		doc = Corpus()
	} else {
		doc, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encodingmatrix:", err)
			os.Exit(2)
		}
	}

	res, err := Matrix(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encodingmatrix:", err)
		os.Exit(2)
	}
	fmt.Print(res)
	if *guess {
		fmt.Println("\nfewest undecodable bytes first:")
		sorted := append([]Reading(nil), res.Readings...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Replacement < sorted[j].Replacement })
		for _, r := range sorted {
			if r.Refused != "" {
				continue
			}
			fmt.Printf("  %-14s %d\n", r.Encoding, r.Replacement)
		}
	}
	if !res.OK() {
		os.Exit(1)
	}
}

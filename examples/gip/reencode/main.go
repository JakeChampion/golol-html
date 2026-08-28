// Command reencode converts a document from a single-byte legacy encoding to UTF-8 and
// proves the text survived.
//
//	$ reencode -from windows-1252 < old.html > new.html
//	reencode: 1284 bytes in, 1299 out; 640 characters, fingerprints match; 1 charset declaration(s) rewritten to utf-8
//
// The document's own charset declaration is rewritten too, because converting the bytes
// alone leaves a file that says it is windows-1252 and is not: a browser believes the
// declaration and decodes UTF-8 through the legacy table, which is the mojibake this
// program exists to avoid. The proof below cannot catch that, since both of its passes
// are told the encoding rather than reading it out of the document.
//
// The conversion is a byte-for-character table and is not the interesting part. The
// proof is: the same document is read twice through the rewriter - once declared as the
// legacy encoding, once as UTF-8 - and the characters the handlers are given are
// fingerprinted both times. If the two fingerprints agree, every character of text,
// every attribute value and every comment came through unchanged. If they do not, the
// program says where they first differed and exits non-zero, leaving the caller with a
// document it does not vouch for.
//
// # Why the conversion is not done by the rewriter
//
// It cannot be. The rewriter decodes with the document's encoding and encodes back with
// the same one: there is no output-encoding option, and replacing every text chunk and
// every attribute with itself leaves the bytes exactly as they were - measured over
// windows-1252, iso-8859-2, shift_jis, euc-jp and gbk. What the rewriter is good for
// here is the other half: it is the only thing in the pipeline that knows how a browser
// will read either version, so it is the oracle rather than the converter.
//
// # What a lost byte looks like
//
// A byte the legacy decoder cannot use is handed to a handler as U+FFFD, and on the way
// out it becomes "&#65533;" - a reference, in the text, seven bytes where the document
// had one - because U+FFFD is not in a legacy repertoire and a reference is the
// fallback. In a document declared UTF-8 the same byte comes back as the character
// itself. Either way it is the signal this program looks for: a fingerprint that holds
// U+FFFD is a document that lost something before this program ever saw it, and the
// report says so rather than blaming the conversion.
//
// # References are counted as what the document wrote
//
// A handler is given "&#20140;" rather than the character it stands for, so both passes
// see the same six or eight bytes and the fingerprints agree. That is what makes the
// proof possible without a reference table: the conversion does not touch references
// and neither does the check.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Tables are the single-byte encodings this program can convert without a dependency,
// by the labels a document uses. Each is a full 256-entry map from byte to code point,
// and the ASCII half is identity in all of them, which is what keeps the markup intact
// through a byte-for-character conversion.
//
// Every label here is one the rewriter accepts, which the tests check by using it: a
// label the standard lists but this build does not know - "latin9" for iso-8859-15, for
// one - fails at NewWriter, so it has no business in this table.
//
// The labels are the standard's, not the code pages': "iso-8859-1", "latin1", "ascii"
// and "us-ascii" all mean windows-1252, because that is what the WHATWG encoding
// standard requires and what a browser does. Converting a document labelled iso-8859-1
// with a true Latin-1 table would put C1 controls where every reader sees punctuation -
// the tests compare each table against the rewriter, which is the same authority a
// browser follows, so that mistake fails rather than shipping.
var Tables = map[string]*[256]rune{
	"windows-1252": &cp1252,
	"iso-8859-1":   &cp1252,
	"latin1":       &cp1252,
	"ascii":        &cp1252,
	"us-ascii":     &cp1252,
	"iso-8859-15":  &latin9,
}

// Fingerprint is what the proof compares: the characters the rewriter reported, in
// order, reduced to a hash, with the counts that make a mismatch legible.
type Fingerprint struct {
	Hash        uint64
	Characters  int
	Runs        int // text chunks, attribute values and comments seen
	Replacement int // U+FFFD characters seen: bytes the decoder could not use
}

func (f Fingerprint) String() string {
	return fmt.Sprintf("%016x over %d characters in %d runs", f.Hash, f.Characters, f.Runs)
}

// Result is what happened.
type Result struct {
	In, Out   int // bytes
	Before    Fingerprint
	After     Fingerprint
	Unmapped  int    // bytes the table has no character for
	FirstDiff string // where the two passes first disagreed, if they did
	// Redeclared is how many of the document's own charset declarations were
	// rewritten to utf-8. Zero means the document declared nothing, and a document
	// that declares nothing is read with whatever the server or the browser decides.
	Redeclared int
}

// OK reports whether the conversion is one the program vouches for.
func (r Result) OK() bool {
	return r.FirstDiff == "" && r.Before.Hash == r.After.Hash &&
		r.Before.Characters == r.After.Characters && r.Unmapped == 0
}

func (r Result) String() string {
	s := fmt.Sprintf("reencode: %d bytes in, %d out; %d characters", r.In, r.Out, r.After.Characters)
	switch {
	case r.FirstDiff != "":
		s += "; the passes differ: " + r.FirstDiff
	case !r.OK():
		s += "; fingerprints differ"
	default:
		s += ", fingerprints match"
	}
	if r.Before.Replacement > 0 {
		s += fmt.Sprintf("; %d characters the source encoding could not decode", r.Before.Replacement)
	}
	if r.Unmapped > 0 {
		s += fmt.Sprintf("; %d bytes this table has no character for", r.Unmapped)
	}
	if r.Redeclared > 0 {
		s += fmt.Sprintf("; %d charset declaration(s) rewritten to utf-8", r.Redeclared)
	} else {
		s += "; the document declares no encoding, so it has to be served as utf-8"
	}
	return s
}

// prober reads a document and fingerprints everything a browser would treat as
// characters: text, attribute values and comment text. Element and attribute names are
// ASCII in every encoding this program handles, so they cannot change.
type prober struct {
	h          *fnv64
	characters int
	runs       int
	fffd       int
}

func newProber() *prober { return &prober{h: newFNV()} }

func (p *prober) add(kind, s string) {
	if s == "" {
		return
	}
	p.runs++
	p.characters += utf8.RuneCountInString(s)
	p.fffd += strings.Count(s, "�")
	p.h.write(kind)
	p.h.write("\x00")
	p.h.write(s)
	p.h.write("\x00")
}

func (p *prober) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			p.add("t", c.Text())
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			p.add("c", c.Text())
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				p.add("a:"+a.Name, a.Value)
			}
			return nil
		}),
	}
}

func (p *prober) fingerprint() Fingerprint {
	return Fingerprint{Hash: p.h.sum(), Characters: p.characters, Runs: p.runs, Replacement: p.fffd}
}

// fingerprint runs one document through the rewriter under the given encoding and
// returns what it saw. The destination is io.Discard: this pass is a measurement.
func fingerprint(doc []byte, encoding string) (Fingerprint, error) {
	p := newProber()
	opts := p.options()
	if encoding != "" {
		opts = append(opts, lolhtml.WithEncoding(encoding))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		return Fingerprint{}, err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return Fingerprint{}, err
	}
	if err := w.Close(); err != nil {
		return Fingerprint{}, err
	}
	return p.fingerprint(), nil
}

// runs collects the same strings the fingerprint is built from, for the report when the
// two passes disagree. It is only called on a mismatch, because holding a document's
// text is exactly what the streaming interface exists to avoid.
func runs(doc []byte, encoding string) ([]string, error) {
	var out []string
	opts := []lolhtml.Option{
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if s := c.Text(); s != "" {
				out = append(out, "t:"+s)
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			out = append(out, "c:"+c.Text())
			return nil
		}),
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			for _, a := range e.AttributeList() {
				if a.Value != "" {
					out = append(out, "a:"+a.Name+"="+a.Value)
				}
			}
			return nil
		}),
	}
	if encoding != "" {
		opts = append(opts, lolhtml.WithEncoding(encoding))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(doc); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

// Convert reads all of src, converts it to UTF-8 with the named table, writes it to dst
// and proves the text survived.
func Convert(dst io.Writer, src io.Reader, from string) (Result, error) {
	table, ok := Tables[strings.ToLower(from)]
	if !ok {
		return Result{}, fmt.Errorf("no table for %q; have %s", from, strings.Join(names(), ", "))
	}
	in, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	var res Result
	res.In = len(in)

	// The conversion itself: one character per byte.
	var out strings.Builder
	out.Grow(len(in))
	for _, b := range in {
		r := table[b]
		if r == utf8.RuneError {
			res.Unmapped++
		}
		out.WriteRune(r)
	}
	converted := out.String()
	res.Out = len(converted)

	// The proof: what a browser reads from each version.
	res.Before, err = fingerprint(in, from)
	if err != nil {
		return res, err
	}
	res.After, err = fingerprint([]byte(converted), "utf-8")
	if err != nil {
		return res, err
	}
	if res.Before.Hash != res.After.Hash {
		before, err1 := runs(in, from)
		after, err2 := runs([]byte(converted), "utf-8")
		if err1 == nil && err2 == nil {
			res.FirstDiff = firstDifference(before, after)
		}
	}
	// The last step, and the one converting the bytes does not do on its own: the
	// document says what encoding it is in, and a browser believes the document. A
	// file whose bytes are UTF-8 and whose <meta> still says windows-1252 is decoded
	// with the legacy table on the way back in, so every character converted above is
	// mojibake on screen - and the proof cannot see it, because both of its passes are
	// told the encoding rather than reading it out of the document.
	//
	// It runs after the fingerprints are taken, deliberately. The declaration is an
	// attribute value, so it is one of the runs the proof compares; rewriting it first
	// would make the two passes disagree about the one thing this program means to
	// change.
	counted := &countingWriter{w: dst}
	res.Redeclared, err = redeclare(counted, converted)
	res.Out = counted.n
	if err != nil {
		return res, err
	}
	return res, nil
}

// countingWriter is dst plus a byte count, so the report says how many bytes were
// written rather than how many were converted - the redeclaration changes the length.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// redeclare copies the converted document to dst, rewriting its charset declaration to
// utf-8, and reports how many declarations it changed.
//
// Both spellings count: <meta charset> and the http-equiv Content-Type form, which is
// what a document old enough to be in a legacy encoding is likely to use. Nothing is
// inserted when the document declares nothing - where a declaration belongs is a
// question about the document's head that this program has no answer for, and the
// report says so instead.
func redeclare(dst io.Writer, converted string) (int, error) {
	n := 0
	w, err := lolhtml.NewWriter(dst,
		lolhtml.WithEncoding("utf-8"),
		lolhtml.OnElement("meta[charset]", func(e *lolhtml.Element) error {
			n++
			return e.SetAttribute("charset", "utf-8")
		}),
		lolhtml.OnElement("meta[http-equiv][content]", func(e *lolhtml.Element) error {
			equiv, _ := e.Attribute("http-equiv")
			if !strings.EqualFold(strings.TrimSpace(equiv), "content-type") {
				return nil
			}
			content, _ := e.Attribute("content")
			got, ok := replaceCharset(content)
			if !ok {
				return nil
			}
			n++
			return e.SetAttribute("content", got)
		}),
	)
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(w, converted); err != nil {
		w.Close()
		return n, err
	}
	// Close is the final flush and the only place the last bytes and a late error
	// appear, so its error is the one that decides whether the output is whole.
	if err := w.Close(); err != nil {
		return n, err
	}
	return n, nil
}

// replaceCharset swaps the charset parameter of a Content-Type value for utf-8, keeping
// the rest of the value as the document spelled it. It reports whether there was one.
func replaceCharset(content string) (string, bool) {
	i := strings.Index(strings.ToLower(content), "charset=")
	if i < 0 {
		return content, false
	}
	rest := content[i+len("charset="):]
	end := strings.IndexAny(rest, ";, \t")
	if end < 0 {
		end = len(rest)
	}
	return content[:i] + "charset=utf-8" + rest[end:], true
}

// firstDifference names the first run the two passes disagree about, which is what a
// caller needs to look at.
func firstDifference(before, after []string) string {
	for i := range before {
		if i >= len(after) {
			return fmt.Sprintf("the converted document is missing %q", before[i])
		}
		if before[i] != after[i] {
			return fmt.Sprintf("%q became %q", before[i], after[i])
		}
	}
	if len(after) > len(before) {
		return fmt.Sprintf("the converted document gained %q", after[len(before)])
	}
	return ""
}

func names() []string {
	out := make([]string, 0, len(Tables))
	for name := range Tables {
		out = append(out, name)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// fnv64 is the hash the fingerprint is built with: a stable, order-sensitive reduction
// of everything the rewriter reported. FNV-1a rather than a cryptographic hash because
// this compares two passes over the same bytes in the same process - nobody is trying
// to forge a match.
type fnv64 struct{ h hash.Hash64 }

func newFNV() *fnv64 { return &fnv64{h: fnv.New64a()} }

func (f *fnv64) write(s string) { io.WriteString(f.h, s) }

func (f *fnv64) sum() uint64 { return f.h.Sum64() }

func main() {
	from := flag.String("from", "windows-1252", "the document's current encoding")
	list := flag.Bool("list", false, "list the encodings this program can convert")
	flag.Parse()

	if *list {
		for _, n := range names() {
			fmt.Println(n)
		}
		return
	}

	w := bufio.NewWriter(os.Stdout)
	res, err := Convert(w, os.Stdin, *from)
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reencode:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

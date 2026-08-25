// Command mojibake finds text that has been decoded with the wrong encoding, and says
// which one it probably was.
//
//	$ mojibake < page.html
//	windows-1252 read as utf-8   12 runs   "Itâ€™s a cafÃ©"   -> "It's a café"
//	utf-8 read as windows-1252    1 run    "PrÃ¼fung"                   -> "Prüfung"
//
// Mojibake is not damaged text: it is text that survived, in the wrong clothes. Bytes
// that meant "é" in UTF-8 were read as two windows-1252 characters and re-encoded as
// UTF-8, so the document now says "Ã©" - valid UTF-8, wrong characters, and reversible
// exactly. This program looks for the sequences that only occur that way, reports them
// with the reconstruction, and with -fix writes the reconstruction back.
//
// # Why the detector is a reporter by default
//
// A document whose declared encoding is wrong in the other direction - UTF-8 declared,
// windows-1252 bytes - holds bytes that are not valid UTF-8 at all, and two measured
// facts decide the shape of this program.
//
// A text handler never sees such a byte: the library hands over U+FFFD, so "not UTF-8"
// can only be reported as "the text contains U+FFFD", and cannot be told apart from a
// document that really contains that character. And registering the handler is enough
// to change the document - those bytes come back as U+FFFD in the output too, while an
// attribute, a comment or a tag name read the same way keeps its bytes. So the pass
// that diagnoses cannot be the pass that copies, and this one writes to io.Discard
// unless told to fix.
//
// That is the second thing it reports: U+FFFD in the text is the encoding declaration
// being wrong, not mojibake, and the fix is the declaration rather than the text.
//
// # What it looks for
//
// The signature of UTF-8 read as windows-1252 is a leading byte that decodes to "Ã",
// "Â", "â", "Î" or "Ð" followed by a character from the range the continuation bytes
// map to. Those pairs are almost never written on purpose - "Ã©" is not a word in any
// language - which is what makes the detection safe. Each run is reconstructed by
// encoding the characters back to windows-1252 bytes and decoding them as UTF-8; a run
// that does not survive that round trip is not reported, so the test is the fix.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kind is what was found.
type Kind int

const (
	// UTF8AsCP1252 is UTF-8 bytes read as windows-1252: the classic mojibake.
	UTF8AsCP1252 Kind = iota
	// InvalidBytes is text that is not valid UTF-8 at all, which is a wrong
	// declaration rather than mojibake.
	InvalidBytes
)

func (k Kind) String() string {
	if k == InvalidBytes {
		return "not utf-8 at all"
	}
	return "utf-8 read as windows-1252"
}

// Finding is one run of suspicious text.
type Finding struct {
	Kind  Kind
	Text  string // what the document says
	Fixed string // what it probably said
	Where string // the element the text was in, or "" for the document
	Count int    // how many times this run appears
}

// Result is what happened.
type Result struct {
	Findings []Finding
	Runs     int // suspicious runs found
	Fixed    int // runs rewritten, with -fix
	Invalid  int // text nodes holding bytes that are not UTF-8
}

// OK reports whether the document's text is what it says it is.
func (r Result) OK() bool { return len(r.Findings) == 0 }

// Encoding is the encoding the document was probably written in, or "" if this program
// found nothing to say.
func (r Result) Encoding() string {
	for _, f := range r.Findings {
		if f.Kind == UTF8AsCP1252 {
			return "utf-8, decoded as windows-1252"
		}
	}
	if r.Invalid > 0 {
		return "windows-1252 or another single-byte encoding, declared as utf-8"
	}
	return ""
}

func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mojibake: %d suspicious runs, %d text nodes that are not utf-8", r.Runs, r.Invalid)
	if e := r.Encoding(); e != "" {
		fmt.Fprintf(&b, "; probably %s", e)
	}
	b.WriteString("\n")
	for _, f := range r.Findings {
		if f.Kind == InvalidBytes {
			fmt.Fprintf(&b, "  %-26s %q\n", f.Kind, f.Text)
			continue
		}
		fmt.Fprintf(&b, "  %-26s %q -> %q (%d)\n", f.Kind, f.Text, f.Fixed, f.Count)
	}
	return b.String()
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Fix writes the reconstruction back instead of only reporting it. It applies to
	// mojibake alone: bytes that are not UTF-8 are a declaration problem, and this
	// program will not guess at them.
	Fix bool
	// Min is the shortest run to report, in characters. Two is the length of one
	// mis-decoded character and is the default; a higher number trades recall for
	// certainty on documents that legitimately mix scripts.
	Min int
}

// cp1252 is the windows-1252 table: byte to character. The bytes below 0x80 are ASCII,
// the bytes from 0xA0 up are Latin-1, and the sixteen in between are the ones that make
// mojibake recognisable. Five of those - 0x81, 0x8D, 0x8F, 0x90, 0x9D - are unassigned
// in the original code page and map to the C1 controls of the same number, which the
// WHATWG encoding standard requires and which matters because 0x9D is the third byte of
// a right double quotation mark.
var cp1252 = func() [256]rune {
	var t [256]rune
	for b := 0; b < 256; b++ {
		t[b] = rune(b)
	}
	for r, b := range specials {
		t[b] = r
	}
	return t
}()

// specials is the 0x80 to 0x9F range, where windows-1252 differs from Latin-1.
var specials = map[rune]byte{
	'\u20ac': 0x80, '\u201a': 0x82, '\u0192': 0x83, '\u201e': 0x84,
	'\u2026': 0x85, '\u2020': 0x86, '\u2021': 0x87, '\u02c6': 0x88,
	'\u2030': 0x89, '\u0160': 0x8a, '\u2039': 0x8b, '\u0152': 0x8c,
	'\u017d': 0x8e, '\u2018': 0x91, '\u2019': 0x92, '\u201c': 0x93,
	'\u201d': 0x94, '\u2022': 0x95, '\u2013': 0x96, '\u2014': 0x97,
	'\u02dc': 0x98, '\u2122': 0x99, '\u0161': 0x9a, '\u203a': 0x9b,
	'\u0153': 0x9c, '\u017e': 0x9e, '\u0178': 0x9f,
}

// cp1252Bytes is cp1252 inverted, for the bytes above 0x7F: character to byte.
var cp1252Bytes = func() map[rune]byte {
	m := map[rune]byte{}
	for b := 0x80; b < 256; b++ {
		m[cp1252[b]] = byte(b)
	}
	return m
}()

// leads are the characters a UTF-8 lead byte becomes when read as windows-1252: the
// bytes 0xC2 to 0xF4, which is every lead byte that can start a valid sequence.
var leads = func() map[rune]bool {
	m := map[rune]bool{}
	for b := 0xc2; b <= 0xf4; b++ {
		m[cp1252[b]] = true
	}
	return m
}()

// continuations are what the continuation bytes 0x80 to 0xBF become.
var continuations = func() map[rune]bool {
	m := map[rune]bool{}
	for b := 0x80; b <= 0xbf; b++ {
		m[cp1252[b]] = true
	}
	return m
}()

type finder struct {
	opts  Options
	res   Result
	text  strings.Builder
	seen  map[string]int
	order []string
	fixes map[string]string
}

func (f *finder) textChunk(c *lolhtml.TextChunk) error {
	f.text.WriteString(c.Text())
	if !c.IsLastInTextNode() {
		if f.opts.Fix {
			c.Remove()
		}
		return nil
	}
	src := f.text.String()
	f.text.Reset()

	if strings.ContainsRune(src, '\ufffd') {
		// A byte the declared encoding could not decode. The handler never sees the
		// byte itself - the library hands over U+FFFD - so this is as much as can be
		// said, and it cannot be told apart from a document that really contains
		// U+FFFD. Either way it is not mojibake and there is nothing to reconstruct:
		// the fix is the declaration.
		f.res.Invalid++
		f.note(Finding{Kind: InvalidBytes, Text: trim(src)})
	}

	out, runs := f.scan(src)
	f.res.Runs += runs
	if !f.opts.Fix {
		return nil
	}
	if runs > 0 {
		f.res.Fixed += runs
	}
	return c.Replace(out, lolhtml.HTML)
}

// scan finds the mojibake runs in one text node, records them, and returns the text
// with each run reconstructed.
func (f *finder) scan(s string) (string, int) {
	var b strings.Builder
	runs := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !leads[r] {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		// A lead character starts a candidate run: take as many mis-decoded
		// characters as follow, then try to reconstruct the whole run.
		end := i
		for end < len(s) {
			r2, size2 := utf8.DecodeRuneInString(s[end:])
			if !leads[r2] && !continuations[r2] {
				break
			}
			end += size2
		}
		candidate := s[i:end]
		fixed, ok := reconstruct(candidate)
		if !ok || utf8.RuneCountInString(candidate) < max(2, f.opts.Min) || fixed == candidate {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		runs++
		f.note(Finding{Kind: UTF8AsCP1252, Text: candidate, Fixed: fixed})
		b.WriteString(fixed)
		i = end
	}
	return b.String(), runs
}

// note records a finding, counting repeats rather than listing them.
func (f *finder) note(fi Finding) {
	if f.seen == nil {
		f.seen = map[string]int{}
		f.fixes = map[string]string{}
	}
	key := fi.Kind.String() + "\x00" + fi.Text
	if f.seen[key] == 0 {
		f.order = append(f.order, key)
		f.fixes[key] = fi.Fixed
	}
	f.seen[key]++
}

func (f *finder) report() Result {
	res := f.res
	for _, key := range f.order {
		kind, text, _ := strings.Cut(key, "\x00")
		k := UTF8AsCP1252
		if kind == InvalidBytes.String() {
			k = InvalidBytes
		}
		res.Findings = append(res.Findings, Finding{
			Kind: k, Text: text, Fixed: f.fixes[key], Count: f.seen[key],
		})
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		if res.Findings[i].Count != res.Findings[j].Count {
			return res.Findings[i].Count > res.Findings[j].Count
		}
		return res.Findings[i].Text < res.Findings[j].Text
	})
	return res
}

// reconstruct encodes the characters back to the windows-1252 bytes they came from and
// decodes those as UTF-8. It reports false when the result is not valid UTF-8, which is
// what makes the detection safe: a run that does not survive the round trip was not
// mojibake.
func reconstruct(s string) (string, bool) {
	bytes := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			bytes = append(bytes, byte(r))
			continue
		}
		b, ok := cp1252Bytes[r]
		if !ok {
			// A character windows-1252 cannot hold, so these bytes were never read
			// as windows-1252 and this is not mojibake.
			return "", false
		}
		bytes = append(bytes, b)
	}
	out := string(bytes)
	if !utf8.ValidString(out) {
		return "", false
	}
	return out, true
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 40 {
		r := []rune(s)
		return string(r[:40]) + "..."
	}
	return s
}

func (f *finder) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnDocumentText(f.textChunk)}
}

// Find reads src and reports the mojibake in it. With Options.Fix the reconstruction is
// written to dst; without it, nothing is.
func Find(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	f := &finder{opts: opts}
	out := dst
	if !opts.Fix {
		out = io.Discard
	}
	w, err := lolhtml.NewWriter(out, f.options()...)
	if err != nil {
		return f.report(), err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return f.report(), err
	}
	if err := w.Close(); err != nil {
		return f.report(), err
	}
	return f.report(), nil
}

func main() {
	var opts Options
	flag.BoolVar(&opts.Fix, "fix", false, "write the reconstruction instead of only reporting it")
	flag.IntVar(&opts.Min, "min", 2, "shortest run to report, in characters")
	flag.Parse()

	res, err := Find(os.Stdout, os.Stdin, opts)
	fmt.Fprint(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mojibake:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}

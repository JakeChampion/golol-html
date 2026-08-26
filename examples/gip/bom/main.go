// Command bom rewrites a document that may begin with a byte-order mark, and does what a browser
// would do with it - which is not what this library does on its own.
//
// Two things a leading BOM does here. It arrives as text: the first text chunk of
// "\xef\xbb\xbf<p>café</p>" is U+FEFF at 0..3, so a word counter, a search index or a translation
// memory gets a character the page never shows. And it does not affect decoding: WithEncoding is
// the caller's declaration and nothing is sniffed, which the option documents for a document's own
// <meta charset> - but a BOM is not a <meta charset>. In the WHATWG sniffing algorithm a <meta>
// ranks below a transport-level charset and a BOM ranks above it, with certainty. Measured against
// the reference implementation in golang.org/x/net/html/charset:
//
//	declared        first bytes   the algorithm's answer
//	windows-1252    UTF-8 BOM     utf-8, certain
//	shift_jis       UTF-8 BOM     utf-8, certain
//	(none)          UTF-8 BOM     utf-8, certain
//	windows-1252    UTF-16LE BOM  utf-16le, certain
//	windows-1252    UTF-16BE BOM  utf-16be, certain
//	windows-1252    no BOM        windows-1252, certain
//
// So a proxy that takes the charset from a Content-Type header and hands it to WithEncoding decodes
// the body differently from the browser it is proxying for, whenever the body has a BOM. Measured
// on "\xef\xbb\xbf<p>café</p>" declared windows-1252: the handlers are given "ï»¿" and "cafÃ©"
// where the browser reads "café". Nothing errors, and with no text handler the output is
// byte-identical either way, so it is only the handlers' view that is wrong - which is the view
// every decision is made on.
//
// What this program does is the two lines that fix it: read the BOM first, use the encoding it
// declares, and treat the mark as a mark rather than as text. The output keeps the BOM bytes,
// because removing them would change what the next consumer sniffs.
//
// UTF-16 is a different matter and this refuses it. A UTF-16 document is not something the
// rewriter can process - its markup is not ASCII-compatible, so no selector matches and, with a
// text handler registered, every byte that is not valid UTF-8 becomes U+FFFD. Measured:
// "\xff\xfe<p>x</p>" comes back with the mark replaced, which is corruption rather than a rewrite.
// Refusing is the honest answer, and the error says what was found.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Marks are the byte-order marks worth recognising, longest first so that a prefix test is
// unambiguous.
var Marks = []struct {
	Bytes    string
	Encoding string
}{
	{"\xef\xbb\xbf", "utf-8"},
	{"\xff\xfe", "utf-16le"},
	{"\xfe\xff", "utf-16be"},
}

// ErrUnsupportedEncoding is returned for a document whose mark declares an encoding the rewriter
// cannot work in.
var ErrUnsupportedEncoding = errors.New("bom: the document declares an encoding the rewriter cannot process")

// Detected is what the first bytes of a document say about it.
type Detected struct {
	Mark     string // the mark's bytes, empty if there is none
	Encoding string // the encoding to use: the mark's if there is one, else the declared label
	FromMark bool   // whether the mark decided it
}

// Detect applies the part of the sniffing algorithm that a caller of this library has to apply
// itself: a byte-order mark outranks the declared label, with certainty.
func Detect(prefix []byte, declared string) Detected {
	for _, m := range Marks {
		if strings.HasPrefix(string(prefix), m.Bytes) {
			return Detected{Mark: m.Bytes, Encoding: m.Encoding, FromMark: true}
		}
	}
	if declared == "" {
		declared = "utf-8"
	}
	return Detected{Encoding: declared}
}

// Text is the document's text as a consumer should see it: accumulated from the chunks, with a
// leading mark removed. The mark is not text, and this library reports it as text.
type Text struct {
	Body        string
	MarkDropped bool
}

// Run rewrites r into w, honouring a leading byte-order mark over declared, and returns the text
// the document says with the mark removed.
func Run(r io.Reader, w io.Writer, declared string) (Detected, Text, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return Detected{}, Text{}, err
	}
	// Three bytes is enough for every mark here, and reading them costs nothing: the caller
	// already has to buffer this much to recognise one.
	det := Detect(body, declared)
	if det.Encoding == "utf-16le" || det.Encoding == "utf-16be" {
		return det, Text{}, fmt.Errorf("%w: %s", ErrUnsupportedEncoding, det.Encoding)
	}

	var text strings.Builder
	rw, err := lolhtml.NewWriter(w,
		lolhtml.WithEncoding(det.Encoding),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			text.WriteString(tc.Text())
			return nil
		}),
	)
	if err != nil {
		return det, Text{}, err
	}
	if _, err := rw.Write(body); err != nil {
		return det, Text{}, err
	}
	if err := rw.Close(); err != nil {
		return det, Text{}, err
	}

	out := Text{Body: text.String()}
	// The mark reaches a text handler as U+FEFF whatever its bytes were, since a handler is
	// always given UTF-8.
	if det.FromMark {
		if trimmed := strings.TrimPrefix(out.Body, "\ufeff"); trimmed != out.Body {
			out.Body = trimmed
			out.MarkDropped = true
		}
	}
	return det, out, nil
}

func main() {
	declared := ""
	if len(os.Args) > 1 {
		declared = os.Args[1]
	}
	det, text, err := Run(os.Stdin, os.Stdout, declared)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bom:", err)
		os.Exit(1)
	}
	source := "the declared label"
	if det.FromMark {
		source = fmt.Sprintf("a %d-byte mark, which outranks the declared label", len(det.Mark))
	}
	fmt.Fprintf(os.Stderr, "\nbom: decoded as %s from %s; %d characters of text%s\n",
		det.Encoding, source, len([]rune(text.Body)),
		map[bool]string{true: ", mark removed from it", false: ""}[text.MarkDropped])
}

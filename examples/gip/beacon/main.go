// Command beacon injects an analytics beacon and then proves it changed nothing
// else.
//
// The beacon is an <img>, not a script: a one-pixel request that works with
// scripting off, needs no consent dialog to be honest about, and cannot block
// rendering. It carries a marker attribute so it can be found again.
//
//	<img src="/b?p=%2Fpage" alt="" width="1" height="1"
//	     referrerpolicy="no-referrer" data-beacon="" style="display:none">
//
// It goes before </body>, and if there is no </body> it does not go anywhere.
// That is deliberate, and it is the thing this program found out the hard way:
// DocumentEnd.Append adds content at the end of the output bytes, not at the end
// of the document tree. A response that was cut off mid-stream ends inside
// something, and the beacon then lands inside it:
//
//	<script>var a = 1          + append -> <script>var a = 1<img data-beacon="">
//	<!-- unterminated          + append -> <!-- unterminated<img data-beacon="">
//
// In both of those the beacon is text inside a script or a comment. No error,
// no warning, and asking a parser afterwards finds no img element at all.
// Measured: of twelve documents that end mid-construct, seven produce no element
// from the append. So with no </body> to insert before, this program reports and
// does nothing rather than emitting something that is not markup. -at-end
// overrides that for callers who know their input is complete.
//
// The proof is the other half. Any rewriter can insert something; what is hard
// to be sure of is that nothing else moved. -verify runs the output back through
// a second rewriter that removes the elements carrying the marker, and compares
// the result with the input byte for byte, reporting the offset if they differ.
//
// That is a real check rather than a restatement of the insertion: the second
// pass re-parses and re-serialises the whole document, so anything the rewriter
// normalises - an attribute's quoting, a tag's case, an entity - shows up as a
// difference even though this program never asked for it. It also catches the
// truncated-document case above, because a beacon that is not an element cannot
// be stripped back out.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

type beacon struct {
	endpoint string // where the pixel points
	marker   string // attribute name that identifies the beacon
	path     string // the page's path, sent as a query parameter
	atEnd    bool   // append at the end of the output when there is no </body>

	injected int
	skipped  map[string]int
}

func (b *beacon) note(reason string) {
	if b.skipped == nil {
		b.skipped = map[string]int{}
	}
	b.skipped[reason]++
}

func defaults() *beacon {
	return &beacon{endpoint: "/b", marker: "data-beacon"}
}

func (b *beacon) validate() error {
	if b.endpoint == "" {
		return fmt.Errorf("-endpoint cannot be empty: the beacon would request the page itself")
	}
	if !validAttrName(b.marker) {
		return fmt.Errorf("-marker %q is not usable as an attribute name and a selector: "+
			"it has to be lower-case letters, digits and hyphens, starting with a letter",
			b.marker)
	}
	return nil
}

func validAttrName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

func (b *beacon) options() []lolhtml.Option {
	var present, done, sawBodyEnd bool

	opts := []lolhtml.Option{
		// The marker is how this program recognises its own work, so a second
		// pass adds nothing.
		lolhtml.OnElement("["+b.marker+"]", func(*lolhtml.Element) error {
			present = true
			return nil
		}),

		lolhtml.OnElement("body", func(e *lolhtml.Element) error {
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(end *lolhtml.EndTag) error {
				sawBodyEnd = true
				if present || done {
					return nil
				}
				done = true
				b.injected++
				return end.Before(b.markup(), lolhtml.HTML)
			})
		}),
	}

	opts = append(opts, lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		switch {
		case present:
			b.note("a beacon is already on the page")
			return nil
		case done:
			return nil
		case sawBodyEnd:
			// Unreachable in practice - if the end tag arrived, it inserted -
			// but stated rather than assumed.
			b.note("the body end tag was seen and nothing was inserted")
			return nil
		case !b.atEnd:
			b.note("no </body> to insert before; an insertion at the end of the " +
				"output could land inside a script or a comment, so nothing was " +
				"injected. -at-end overrides this")
			return nil
		}
		done = true
		b.injected++
		return d.Append(b.markup(), lolhtml.HTML)
	}))

	return opts
}

// markup is the beacon. Assembled as a string, because the element does not
// exist yet, so every value goes through EscapeAttribute first.
//
// The values are literals: the endpoint and the path come from flags, and the
// query is built with net/url, which percent-encodes. Percent-encoding and HTML
// escaping are different jobs and both are needed - one keeps the "&" from
// starting a second parameter in the URL, the other keeps it from being read as
// a character reference in the attribute.
func (b *beacon) markup() string {
	src := b.endpoint
	if b.path != "" {
		q := url.Values{"p": []string{b.path}}
		sep := "?"
		if strings.Contains(src, "?") {
			sep = "&"
		}
		src += sep + q.Encode()
	}

	var sb strings.Builder
	sb.WriteString(`<img src="`)
	sb.WriteString(lolhtml.EscapeAttribute(src))
	sb.WriteString(`" alt="" width="1" height="1" referrerpolicy="no-referrer" `)
	sb.WriteString(lolhtml.EscapeAttribute(b.marker))
	sb.WriteString(`="" style="display:none">`)
	return sb.String()
}

func (b *beacon) run(r io.Reader, w io.Writer) error {
	if err := b.validate(); err != nil {
		return err
	}
	out, err := lolhtml.NewWriter(w, b.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// strip removes every element carrying the marker. It is the inverse of the
// insertion, and it is what makes the proof a proof: the whole document goes
// through the parser and the serialiser again, so anything else that moved shows
// up here.
func strip(marker, in string) (string, error) {
	return lolhtml.RewriteString(in,
		lolhtml.OnElement("["+marker+"]", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}))
}

// verify checks that the only difference between the input and the output is the
// beacon. A mismatch is reported with its offset and the bytes either side,
// because "the documents differ" is not something anyone can act on.
//
// It compares against an input that carries no beacon of its own: strip removes
// every marked element, including one that was already there, so an input that
// had one would look as though the rewrite had deleted it.
func verify(marker, in, out string) error {
	stripped, err := strip(marker, out)
	if err != nil {
		return fmt.Errorf("stripping the beacon back out: %w", err)
	}
	if stripped == in {
		return nil
	}
	i := commonPrefix(in, stripped)
	return fmt.Errorf("the document changed beyond the beacon, first at byte %d:\n"+
		"  input: %s\n"+
		" output: %s", i, window(in, i), window(stripped, i))
}

func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// window is 20 bytes either side of i, with the position marked, so two windows
// can be read against each other.
func window(s string, i int) string {
	lo := max(i-20, 0)
	hi := min(i+20, len(s))
	return fmt.Sprintf("%q<HERE>%q", s[lo:i], s[i:hi])
}

func injectString(in string, opts ...func(*beacon)) (string, *beacon, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	var out bytes.Buffer
	err := b.run(strings.NewReader(in), &out)
	return out.String(), b, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func main() {
	b := defaults()
	verifyFlag := flag.Bool("verify", false,
		"strip the beacon back out and prove nothing else changed; fails if it did")
	flag.StringVar(&b.endpoint, "endpoint", b.endpoint, "where the beacon points")
	flag.StringVar(&b.marker, "marker", b.marker,
		"attribute marking the beacon, used to recognise it again")
	flag.StringVar(&b.path, "path", "", "page path, sent as the p parameter")
	flag.BoolVar(&b.atEnd, "at-end", false,
		"append at the end of the output when the document has no </body>; the "+
			"insertion may land inside an unterminated script or comment")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "beacon:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: beacon [-verify] [-endpoint u] [file.html]")
		os.Exit(2)
	}

	// -verify needs the input again after the rewrite, so it is buffered. That
	// is the cost of the proof, and it is why the check is a flag rather than
	// something the program always does: a rewriter that has to hold the whole
	// document is not a streaming rewriter any more.
	if *verifyFlag {
		in, err := io.ReadAll(r)
		if err != nil {
			fmt.Fprintln(os.Stderr, "beacon:", err)
			os.Exit(1)
		}
		var out bytes.Buffer
		if err := b.run(bytes.NewReader(in), &out); err != nil {
			fmt.Fprintln(os.Stderr, "beacon:", err)
			os.Exit(1)
		}
		if err := verify(b.marker, string(in), out.String()); err != nil {
			fmt.Fprintln(os.Stderr, "beacon:", err)
			os.Exit(1)
		}
		os.Stdout.Write(out.Bytes())
		fmt.Fprintln(os.Stderr, b.report()+" verified=yes")
		return
	}

	if err := b.run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "beacon:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, b.report())
}

func (b *beacon) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "injected=%d", b.injected)
	for reason, n := range b.skipped {
		fmt.Fprintf(&sb, " [%s]=%d", reason, n)
	}
	return sb.String()
}

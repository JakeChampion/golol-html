// Command tailreport appends a generated report to the end of every document it rewrites, and it
// is here because the obvious way to do that holds the whole report in memory.
//
// DocumentEnd.Append takes a string. There is no streaming form of it - Element and EndTag each
// have six Stream methods, DocumentEnd has none - so a report built row by row has to be
// assembled in full before it can be appended. For a report of any size that is the wrong shape:
// measured on a 12 MB report of a million rows, appending it allocated 65.5 MB where streaming it
// allocated 16.0 MB, and the output was byte-identical.
//
// The streaming route is to write to your own sink after Close. The rewriter's output has already
// gone there, Close has flushed it, and what a caller writes next lands exactly where
// DocumentEnd.Append would have put it - which is "wherever the input stopped", with the same
// hazard Append documents: if the input was cut off inside a script or a comment or an attribute
// value, trailing markup lands inside that construct either way. Neither route is safer there.
//
//	rows       via Append          via the sink
//	1          1,392 bytes          1,032 bytes
//	1,000      47,808               16,568
//	100,000    6,643,200            1,600,632
//	1,000,000  65,470,544           16,000,680
//
// Two things to get right, and only two - the routes are otherwise the same.
//
// The first is escaping. Append takes a ContentType and does the escaping for you; writing to a
// sink is raw bytes. lolhtml.EscapeText is documented as exactly what the library applies for
// ContentType Text, so Text content becomes EscapeText(s) and HTML content is written as it is.
// Not EscapeAttribute: a quote is fine in text and would be written as &#34; by mistake.
//
// The second is that the report must not be written when the rewrite failed - and not for the
// reason I first wrote down. The output is not discarded. A handler error poisons the writer and
// Close reports it, but everything already emitted is in the sink: `before<a href="/">l</a>after`
// with a failing anchor handler leaves "before" there, and a document with a doctype and an open
// body leaves those. It is the documented early-stop prefix - the prefix up to the failing unit,
// with that unit not emitted - so what a report would be appended to is a truncated document, not
// an empty one. That is a better reason not to write it, and it is why Run checks Close before
// writing anything.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Counts is what the report is about: how many of each element the rewrite saw and changed.
type Counts struct {
	Seen    map[string]int
	Changed int
}

// Run rewrites r into w, adding a target attribute to every external link, then streams a report
// of what it did to w. The report never exists as one string.
func Run(r io.Reader, w io.Writer, chunk int) (Counts, error) {
	counts := Counts{Seen: map[string]int{}}

	rw, err := lolhtml.NewWriter(w, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		href, _ := e.Attribute("href")
		counts.Seen["a"]++
		if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			return nil
		}
		counts.Changed++
		return e.SetAttribute("target", "_blank")
	}))
	if err != nil {
		return counts, err
	}

	if chunk <= 0 {
		chunk = 32 * 1024
	}
	buf := make([]byte, chunk)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			if _, err := rw.Write(buf[:n]); err != nil {
				// Closed even though it is being abandoned: the Writer holds a
				// native rewriter and its handles until Close, and the runtime
				// cleanup that would otherwise free them is a leak backstop rather
				// than the supported path - one that stops working entirely the
				// moment a handler captures the Writer. The Write error is the one
				// reported; Close's is about a rewrite that already failed.
				rw.Close()
				return counts, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			// The same, and clearer here: the Writer is perfectly healthy and is
			// being dropped because the source failed.
			rw.Close()
			return counts, readErr
		}
	}

	// Close before the report: an error here means the document is the truncated prefix
	// described at the top of this file - everything up to the failing unit is already in the
	// sink - and a report appended to half a document is worse than no report.
	if err := rw.Close(); err != nil {
		return counts, err
	}
	return counts, WriteReport(w, counts)
}

// WriteReport streams the report to w. Nothing here builds the whole thing: each row is written as
// it is produced, which is what DocumentEnd.Append cannot do.
func WriteReport(w io.Writer, counts Counts) error {
	names := make([]string, 0, len(counts.Seen))
	for name := range counts.Seen {
		names = append(names, name)
	}
	sort.Strings(names)

	if _, err := io.WriteString(w, "\n<!-- tailreport -->\n<ul class=\"tailreport\">\n"); err != nil {
		return err
	}
	for _, name := range names {
		// The element name is markup-shaped and comes from the document, so it is text and
		// gets escaped the way ContentType Text would escape it.
		row := fmt.Sprintf("<li>%s: %d</li>\n", lolhtml.EscapeText(name), counts.Seen[name])
		if _, err := io.WriteString(w, row); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "<li>changed: %d</li>\n</ul>\n", counts.Changed)
	return err
}

// AppendReport is the other route, kept for comparison: the whole report as one string, handed to
// DocumentEnd.Append. It produces the same bytes and holds them all first.
func AppendReport(counts Counts) lolhtml.Option {
	return lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		var b strings.Builder
		if err := WriteReport(&b, counts); err != nil {
			return err
		}
		return d.Append(b.String(), lolhtml.HTML)
	})
}

func main() {
	counts, err := Run(os.Stdin, os.Stdout, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tailreport:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "tailreport: %d anchors, %d changed\n", counts.Seen["a"], counts.Changed)
}

// Command rewrite-url streams a page through lol-html, rewriting relative links
// to absolute ones and reporting what it changed.
//
// It shows the shape most rewriting jobs take: a streaming Writer, a handler per
// concern, and state accumulated in a closure rather than on the units.
//
//	go run ./examples/rewrite-url https://example.com
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rewrite-url <url>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(rawURL string) error {
	base, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing %q: %w", rawURL, err)
	}

	resp, err := http.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var rewritten, titles int

	// Counters live in this closure rather than on the units: a handler's
	// argument is detached as soon as it returns.
	absolutise := func(attr string) func(*lolhtml.Element) error {
		return func(e *lolhtml.Element) error {
			val, ok := e.Attribute(attr)
			if !ok || val == "" {
				return nil
			}
			ref, err := url.Parse(val)
			if err != nil || ref.IsAbs() {
				// Leave anything unparseable or already absolute alone; a
				// malformed href is the page's problem, not a reason to fail.
				return nil
			}
			rewritten++
			return e.SetAttribute(attr, base.ResolveReference(ref).String())
		}
	}

	w, err := lolhtml.NewWriter(os.Stdout,
		lolhtml.OnElement("a[href]", absolutise("href")),
		lolhtml.OnElement("img[src], script[src]", absolutise("src")),
		lolhtml.OnElement("link[href]", absolutise("href")),

		// Note the title without buffering the whole document: text arrives in
		// chunks, so accumulate until the node ends.
		lolhtml.OnText("title", func(t *lolhtml.TextChunk) error {
			if t.IsLastInTextNode() {
				titles++
			}
			return nil
		}),

		// Drop comments, which often carry build details worth not shipping.
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		}),

		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			return d.Append(fmt.Sprintf("\n<!-- rewrote %d urls -->\n", rewritten), lolhtml.HTML)
		}),
	)
	if err != nil {
		return err
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nrewrote %d urls, saw %d title%s\n",
		rewritten, titles, map[bool]string{true: "", false: "s"}[titles == 1])
	return nil
}

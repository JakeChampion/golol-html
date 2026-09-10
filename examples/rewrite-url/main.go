// Command rewrite-url streams a page through lol-html, rewriting relative links
// to absolute ones and reporting what it changed.
//
// It shows the shape most rewriting jobs take: a streaming Writer, a handler per
// concern, and state accumulated in a closure rather than on the units.
//
//	go run ./examples/rewrite-url https://example.com
package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
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

	rewritten, titles, err := rewrite(os.Stdout, base, resp.Header.Get("Content-Type"), resp.Body)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nrewrote %d urls, saw %d title%s\n",
		rewritten, titles, map[bool]string{true: "", false: "s"}[titles == 1])
	return nil
}

// rewrite streams body into dst. contentType is the response's Content-Type,
// whose charset is the encoding the bytes are in - and that matters here because
// a text handler is registered: the rewriter decodes and re-encodes text in the
// declared encoding, so a windows-1252 title fed to a UTF-8 rewriter comes back
// with U+FFFD where every non-ASCII byte was. With only element handlers nothing
// decodes and the mistake is invisible, which is how it goes unnoticed until a
// text handler is added. A charset the rewriter cannot work in (UTF-16, an
// unknown label) is an EncodingError from NewWriter, and the page is copied
// through untouched rather than guessed at. examples/gip/proxy has the full
// treatment, Content-Encoding and Content-Length included.
func rewrite(dst io.Writer, base *url.URL, contentType string, body io.Reader) (rewritten, titles int, err error) {
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

	opts := []lolhtml.Option{
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
	}
	if _, params, err := mime.ParseMediaType(contentType); err == nil && params["charset"] != "" {
		opts = append(opts, lolhtml.WithEncoding(params["charset"]))
	}

	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		var ee *lolhtml.EncodingError
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, "not rewriting:", err)
			_, err = io.Copy(dst, body)
		}
		return 0, 0, err
	}
	if _, err := io.Copy(w, body); err != nil {
		w.Close()
		return rewritten, titles, err
	}
	return rewritten, titles, w.Close()
}

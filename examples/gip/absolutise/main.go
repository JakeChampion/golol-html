// Command absolutise rewrites every relative URL in an HTML document against a
// base URL, streaming, and reports what it changed.
//
// It goes wider than examples/rewrite-url, which handles a[href] only. Every
// attribute the HTML specification calls a URL is covered, including the two
// that hold a list rather than a single URL (srcset), and the one that changes
// the answer for everything after it (<base href>).
//
//	absolutise -base https://example.com/blog/ < page.html > out.html
//
// Exit status is 0 when every URL resolved and 1 when any did not, so it can be
// used as a check over a tree of files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	base := flag.String("base", "", "base URL to resolve against (required)")
	quiet := flag.Bool("quiet", false, "do not append the report comment")
	annotate := flag.Bool("annotate", false, "mark unresolvable URLs in the document itself")
	flag.Parse()

	if *base == "" {
		fmt.Fprintln(os.Stderr, "usage: absolutise -base <url> [-quiet] [-annotate] < in.html > out.html")
		os.Exit(2)
	}

	rep, err := run(os.Stdin, os.Stdout, *base, *quiet, *annotate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "absolutise:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, rep.summary())
	if rep.Unresolved > 0 {
		os.Exit(1)
	}
}

// run streams src through the rewriter into dst. It is the whole program, so
// the tests drive exactly what the command does.
func run(src io.Reader, dst io.Writer, base string, quiet, annotate bool) (*report, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parsing -base %q: %w", base, err)
	}
	if !b.IsAbs() {
		return nil, fmt.Errorf("-base %q is not absolute", base)
	}

	rep := &report{base: b, quiet: quiet, annotate: annotate}
	w, err := lolhtml.NewWriter(dst, rep.options()...)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return rep, nil
}

// urlAttrs are the attributes the HTML specification defines as URLs, grouped by
// the elements that carry them. Two of them (srcset) hold a comma-separated list
// of candidates rather than one URL.
var urlAttrs = []struct {
	selector string
	attr     string
	list     bool
}{
	{"a[href], area[href], link[href], base[href]", "href", false},
	{"img[src], script[src], iframe[src], embed[src], source[src], track[src], audio[src], video[src], input[src], frame[src]", "src", false},
	{"img[srcset], source[srcset]", "srcset", true},
	{"form[action]", "action", false},
	{"button[formaction], input[formaction]", "formaction", false},
	{"video[poster]", "poster", false},
	{"object[data]", "data", false},
	{"blockquote[cite], q[cite], del[cite], ins[cite]", "cite", false},
	{"html[manifest]", "manifest", false},
}

type report struct {
	base *url.URL
	// quiet suppresses the trailing report comment; annotate adds a marker in
	// the document beside every URL that could not be resolved.
	//
	// Annotation is off by default because it is not idempotent: running the
	// program over its own output would add a second marker, and a site build
	// that runs twice would accumulate them.
	quiet    bool
	annotate bool

	Rewritten  int
	AlreadyAbs int
	Unresolved int
	// Comments holding something that looks like a URL. A comment is not
	// parsed, so its contents cannot be rewritten; conditional comments are
	// the usual way this bites.
	CommentURLs int
	// BaseAfter counts elements already rewritten when a <base href> arrived.
	// Streaming means those cannot be revisited.
	BaseAfter int
	Doctype   string
	// Bad holds every URL that could not be parsed, so the caller can report
	// them without the document having to carry the annotation.
	Bad []string

	byAttr map[string]int
}

func (r *report) count(attr string) {
	if r.byAttr == nil {
		r.byAttr = map[string]int{}
	}
	r.byAttr[attr]++
}

func (r *report) options() []lolhtml.Option {
	opts := make([]lolhtml.Option, 0, len(urlAttrs)+4)

	for _, ua := range urlAttrs {
		ua := ua
		opts = append(opts, lolhtml.OnElement(ua.selector, func(e *lolhtml.Element) error {
			raw, ok := e.Attribute(ua.attr)
			if !ok || strings.TrimSpace(raw) == "" {
				return nil
			}

			var out string
			var bad []string
			if ua.list {
				out, bad = r.resolveSrcset(raw)
			} else {
				var ok bool
				out, ok = r.resolveOne(raw)
				if !ok {
					bad = append(bad, raw)
				}
			}

			if len(bad) > 0 {
				r.Unresolved += len(bad)
				r.Bad = append(r.Bad, bad...)
				if r.annotate {
					// Deliberately Text, not HTML: this is an unresolvable URL
					// from the input document, so it is untrusted and must not
					// be able to become markup.
					if err := e.After(" [unresolved: "+strings.Join(bad, " ")+"]", lolhtml.Text); err != nil {
						return err
					}
				}
			}
			if out == raw {
				return nil
			}
			r.count(ua.attr)
			return e.SetAttribute(ua.attr, out)
		}))
	}

	// A <base href> replaces the base for everything after it. Elements before
	// it are already in the output, which is the price of streaming, so say so
	// rather than pretend.
	opts = append(opts, lolhtml.OnElement("base[href]", func(e *lolhtml.Element) error {
		href, ok := e.Attribute("href")
		if !ok {
			return nil
		}
		// The value has already been absolutised by the handler above, since
		// base[href] is in the href group.
		nb, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return nil
		}
		// Minus one: the base element's own href was counted by the handler
		// above, and it is not something this base could have applied to.
		r.BaseAfter = r.Rewritten + r.AlreadyAbs - 1
		r.base = r.base.ResolveReference(nb)
		return nil
	}))

	opts = append(opts,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			r.Doctype, _ = d.Name()
			return nil
		}),

		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if looksLikeURL(c.Text()) {
				r.CommentURLs++
			}
			return nil
		}),

		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			if r.quiet {
				return nil
			}
			// HTML, because this is a comment we are constructing ourselves
			// and the delimiters have to survive as markup - which is exactly
			// why what goes between them cannot. r.base is not the -base flag
			// by the time this runs: a <base href> in the document replaces
			// it, so a crafted query carries "-->" into this line, ends the
			// comment early, and turns the rest of it into live markup. A
			// payload that was inert inside a quoted attribute in the input
			// becomes a <script> element in the output, which is the whole
			// failure this program's own -annotate path avoids by using Text.
			text, err := commentData(" absolutise: " + r.oneLine() + " ")
			if err != nil {
				return err
			}
			return d.Append("\n<!--"+text+"-->\n", lolhtml.HTML)
		}),
	)

	return opts
}

// resolveOne absolutises a single URL, reporting whether it could be parsed at
// all. An already-absolute URL is returned unchanged rather than round-tripped
// through url.String, which would normalise it.
func (r *report) resolveOne(raw string) (string, bool) {
	ref := strings.TrimSpace(raw)
	u, err := url.Parse(ref)
	if err != nil {
		return raw, false
	}
	if u.IsAbs() {
		r.AlreadyAbs++
		return raw, true
	}
	r.Rewritten++
	return r.base.ResolveReference(u).String(), true
}

// resolveSrcset absolutises each candidate of a srcset, keeping its descriptor.
//
// Splitting on commas is not the specification's algorithm, which allows a
// comma inside a URL when it is not followed by a descriptor. Data URLs are
// where that shows up. Candidates that fail to parse are reported rather than
// dropped.
func (r *report) resolveSrcset(raw string) (string, []string) {
	var bad []string
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		lead := part[:len(part)-len(strings.TrimLeft(part, " \t\n\r\f"))]
		body := strings.TrimLeft(part, " \t\n\r\f")
		if body == "" {
			continue
		}

		urlPart, descriptor := body, ""
		if j := strings.IndexAny(body, " \t\n\r\f"); j >= 0 {
			urlPart, descriptor = body[:j], body[j:]
		}

		got, ok := r.resolveOne(urlPart)
		if !ok {
			bad = append(bad, urlPart)
		}
		parts[i] = lead + got + descriptor
	}
	return strings.Join(parts, ","), bad
}

// looksLikeURL is deliberately crude: it exists to notice that a comment holds
// something a reader would expect to be rewritten, not to validate anything.
func looksLikeURL(s string) bool {
	return strings.Contains(s, "href=") || strings.Contains(s, "src=") ||
		strings.Contains(s, "http://") || strings.Contains(s, "https://")
}

func (r *report) oneLine() string {
	attrs := make([]string, 0, len(r.byAttr))
	for a, n := range r.byAttr {
		attrs = append(attrs, fmt.Sprintf("%s=%d", a, n))
	}
	sort.Strings(attrs)

	var sb strings.Builder
	fmt.Fprintf(&sb, "base=%s rewritten=%d absolute=%d unresolved=%d",
		r.base, r.Rewritten, r.AlreadyAbs, r.Unresolved)
	if len(attrs) > 0 {
		fmt.Fprintf(&sb, " [%s]", strings.Join(attrs, " "))
	}
	return sb.String()
}

func (r *report) summary() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", r.oneLine())
	if r.CommentURLs > 0 {
		fmt.Fprintf(&sb, "note: %d comment(s) contain URLs, which a rewriter cannot reach\n", r.CommentURLs)
	}
	for _, b := range r.Bad {
		fmt.Fprintf(&sb, "unresolved: %s\n", b)
	}
	if r.BaseAfter > 0 {
		fmt.Fprintf(&sb, "note: <base href> arrived after %d URL(s) were already emitted\n", r.BaseAfter)
	}
	return sb.String()
}

// rewriteString is a convenience for callers holding a whole document, used by
// the tests and by nothing else.
func rewriteString(in, base string, quiet, annotate bool) (string, *report, error) {
	var out bytes.Buffer
	rep, err := run(strings.NewReader(in), &out, base, quiet, annotate)
	return out.String(), rep, err
}

// commentData makes text safe to sit between comment delimiters the caller
// wrote itself.
//
// [lolhtml.CheckComment] is the library's guard for exactly this position, and
// it reports rather than repairs - deliberately, because the repair is a choice
// about meaning. Nothing inside a comment is a character reference, so there is
// no escaping available: text a comment cannot hold has to be changed instead.
// "- -" for "--" is the replacement its own message suggests, and it keeps a
// report readable, which is the point of a report.
//
// The check runs after the replacement rather than instead of it, as an
// assertion: if a later edit adds a field this does not neutralise, it comes
// back as an error here instead of quietly reopening the hole.
func commentData(text string) (string, error) {
	safe := strings.ReplaceAll(text, "--", "- -")
	if err := lolhtml.CheckComment(safe); err != nil {
		return "", err
	}
	return safe, nil
}

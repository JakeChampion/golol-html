// Command include expands Edge Side Include markers into fetched content,
// streaming, without buffering either the page or what it pulls in.
//
//	<esi:include src="/header"/>
//
// Three things make this more than a substitution.
//
// The first is that an esi: element is not a void element to an HTML parser. It
// has a colon in its name and it is conventionally written unclosed, so its
// content runs to the next matching end tag - which there never is - and
// replacing it takes the rest of the enclosing element with it.
// [lolhtml.WithESITags] is what says otherwise, and it is not optional here: the
// package documentation has the malformed output it produces without it. The
// selector needs the colon escaped too, as esi\:include, or the error blames a
// pseudo-class.
//
// The second is where the fetch goes. The obvious place is inside the
// [lolhtml.StreamFunc] - that is where the content is wanted - and it is the wrong
// place, because a sink write reaches the destination as it is made. A fetch that
// fails after the first write has already committed a page with half an include
// in it, and no error path leads anywhere useful: the response has left. So the
// fetch happens in the handler, where returning an error still costs nothing, and
// the sink is given only a body that is already open. That buys the ESI spec's
// own error handling: onerror="continue" drops a failed include, an alt="..."
// tries a second source, and a caller who asked for neither gets an error before
// anything was sent.
//
// The commitment is only moved, not removed: a body that fails after its first
// byte still truncates the page, and this program reports that as
// [Result.Truncated] rather than pretending otherwise. A stream cannot promise
// atomicity. What it can do is not spend the promise before it has to.
//
// The third is that includes nest. A fetched fragment can contain includes of its
// own, and expanding them by buffering the fragment would give back everything
// the streaming was for. Instead the fragment is run through its own rewriter
// whose destination is the sink:
//
//	fragment -> rewriter(depth+1) -> sink -> destination
//
// so nothing is held anywhere. Each nested rewriter gets its own option set,
// built with its own depth and its own path, because the function inside an
// Option is shared with every Writer it is given to - see [lolhtml.Option]. The
// path is also the cycle check: a fragment that includes an ancestor of itself is
// refused rather than followed until the depth limit stops it.
//
// Two other pieces of ESI, both small and both worth having for the shape of
// them:
//
// <esi:remove> holds content for clients that do not process ESI, so a processor
// removes it. That is [lolhtml.Element.Remove], and it is the one operation on an
// unclosed container that is not made worse by WithESITags being absent - though
// it is enabled anyway.
//
// <!--esi ... --> is the reverse: content hidden from those same clients inside a
// comment, which a processor unwraps and processes. Unwrapping means writing the
// comment's text out as markup, so the check is whether the document really
// spelled it as a comment: the delimiter arithmetic on
// [lolhtml.Comment.SourceLocation] says whether this is <!--esi ...--> or a
// processing instruction that happens to start with the same three letters.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Fetcher opens the content an include names. Returning an error is free until
// the body's first byte reaches the sink, which is why this is called from the
// handler.
type Fetcher interface {
	Fetch(src string) (io.ReadCloser, error)
}

// MapFetcher serves fragments from memory, which is what the tests use and what a
// caller should reach for before pointing this at a network.
type MapFetcher map[string]string

// Fetch implements Fetcher.
func (m MapFetcher) Fetch(src string) (io.ReadCloser, error) {
	body, ok := m[src]
	if !ok {
		return nil, fmt.Errorf("include: no fragment %q", src)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// Options are the limits. Both have to have a number: an include is a document
// telling this program what to fetch and how often.
type Options struct {
	// MaxDepth is how many levels of nested include to expand. Zero means the
	// page's own includes are expanded and theirs are not.
	MaxDepth int
	// ChunkSize is how much of a body is copied between checks of the sink's
	// error, so a fetch that is still arriving after the destination has failed
	// stops rather than being copied to nowhere.
	ChunkSize int
}

// DefaultOptions are what main uses.
var DefaultOptions = Options{MaxDepth: 3, ChunkSize: 8 << 10}

// A Result counts what happened.
type Result struct {
	// Includes expanded, and Bytes copied out of their bodies.
	Includes int
	Bytes    int
	// Continued is the includes whose fetch failed and which said
	// onerror="continue", or whose alt worked.
	Continued int
	// Cycles refused and TooDeep refused.
	Cycles, TooDeep int
	// Removes is the <esi:remove> elements dropped, Comments the <!--esi --> ones
	// unwrapped, and NotComments the tokens that looked like one and were not.
	Removes, Comments, NotComments int
	// Truncated says a body failed after its first byte had been committed, so
	// the output is not a document.
	Truncated bool
}

func (r Result) String() string {
	s := fmt.Sprintf("include: %d expanded (%d bytes), %d continued after a failure, "+
		"%d cycles refused, %d too deep, %d removes, %d esi comments (%d not comments)",
		r.Includes, r.Bytes, r.Continued, r.Cycles, r.TooDeep, r.Removes, r.Comments, r.NotComments)
	if r.Truncated {
		s += "\ninclude: TRUNCATED: a body failed after it had been committed"
	}
	return s
}

// Expand copies src to dst with every include expanded.
func Expand(dst io.Writer, src io.Reader, f Fetcher, opts Options) (Result, error) {
	e := &expander{fetch: f, opts: opts, res: &Result{}}
	w, err := lolhtml.NewWriter(dst, e.options()...)
	if err != nil {
		return *e.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return *e.res, e.failed(err)
	}
	if err := w.Close(); err != nil {
		return *e.res, e.failed(err)
	}
	return *e.res, nil
}

// failed records that the run ended badly, and whether that leaves the page
// truncated: any error after a fragment's first byte does, because that byte has
// already gone to the destination and cannot be recalled. See the note on
// lolhtml.StreamFunc.
func (e *expander) failed(err error) error {
	if e.res.Bytes > 0 {
		e.res.Truncated = true
	}
	return err
}

type expander struct {
	fetch Fetcher
	opts  Options
	// res is shared with every nested expander, because the counts are the
	// document's and not one fragment's.
	res *Result
	// depth is how many fragments deep this expander is, and path is how it got
	// here - the cycle check.
	depth int
	path  []string
}

// options builds the handlers. Built per rewriter, not once: the functions close
// over this expander's depth and path, and an Option shares its function with
// every Writer it is given to.
func (e *expander) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.WithESITags(),
		lolhtml.OnElement(`esi\:include`, e.include),
		lolhtml.OnElement(`esi\:remove`, e.remove),
		lolhtml.OnDocumentComment(e.comment),
	}
}

func (e *expander) include(el *lolhtml.Element) error {
	src, ok := el.Attribute("src")
	if !ok || src == "" {
		return fmt.Errorf("include: an <esi:include> with no src")
	}

	if e.depth > e.opts.MaxDepth {
		e.res.TooDeep++
		el.Remove()
		return nil
	}
	for _, seen := range e.path {
		if seen == src {
			e.res.Cycles++
			el.Remove()
			return nil
		}
	}

	// The fetch is here, in the handler, and not in the sink: an error is free
	// until the first byte is written, and worthless afterwards.
	body, err := e.fetch.Fetch(src)
	if err != nil {
		if alt, ok := el.Attribute("alt"); ok && alt != "" {
			if body, err = e.fetch.Fetch(alt); err == nil {
				e.res.Continued++
				return e.stream(el, alt, body)
			}
		}
		if _, ok := el.Attribute("onerror"); ok {
			// onerror="continue": the include is dropped and the page is served.
			e.res.Continued++
			el.Remove()
			return nil
		}
		return fmt.Errorf("include: %q: %w", src, err)
	}
	return e.stream(el, src, body)
}

// stream replaces the element with the body, expanding any includes inside it on
// the way through. Nothing is buffered: the fragment goes fragment -> nested
// rewriter -> sink -> destination.
func (e *expander) stream(el *lolhtml.Element, src string, body io.ReadCloser) error {
	e.res.Includes++
	return el.StreamReplace(func(s *lolhtml.Sink) error {
		defer body.Close()

		// Every fragment goes through a rewriter of its own, even at the depth
		// limit: the limit is a rule about following an include, not about
		// noticing one, and a marker copied out raw would put this program's
		// internals on the page.
		w, err := lolhtml.NewWriter(s.AsWriter(lolhtml.HTML), e.nested(src).options()...)
		if err != nil {
			return err
		}
		if err := e.copy(s, w, body); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	})
}

// nested is the expander for a fragment: one level deeper, with the fragment on
// the path so that a fragment including an ancestor of itself is a cycle rather
// than a long descent.
func (e *expander) nested(src string) *expander {
	return &expander{
		fetch: e.fetch,
		opts:  e.opts,
		res:   e.res,
		depth: e.depth + 1,
		path:  append(append([]string{}, e.path...), src),
	}
}

// copy moves the body across in chunks, checking between them whether the rewrite
// has already failed. Without that check a body that is still arriving after the
// destination has gone away is copied in full to nowhere: the sink accepts
// everything, because its writes go into lol-html's buffer rather than out.
func (e *expander) copy(s *lolhtml.Sink, dst io.Writer, body io.Reader) error {
	buf := make([]byte, max(e.opts.ChunkSize, 512))
	committed := false
	for {
		if err := s.Err(); err != nil {
			return err
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			e.res.Bytes += n
			committed = true
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			if committed {
				// The page has already been sent part of this fragment. Nothing
				// here can put that back; saying so is all that is left.
				e.res.Truncated = true
			}
			return fmt.Errorf("include: reading a fragment: %w", readErr)
		}
	}
}

func (e *expander) remove(el *lolhtml.Element) error {
	e.res.Removes++
	el.Remove()
	return nil
}

// comment unwraps an <!--esi ... --> block, which is markup hidden from clients
// that do not process ESI.
//
// Unwrapping writes the comment's text out as markup, so the question of whether
// the document really spelled this as a comment is not academic: a processing
// instruction and a CDATA section arrive here as comment tokens too, and
// "<?esi ... ?>" is not an ESI comment. The delimiter arithmetic answers it
// without needing a copy of the input.
func (e *expander) comment(c *lolhtml.Comment) error {
	text := c.Text()
	if !strings.HasPrefix(text, "esi") && !strings.HasPrefix(text, "?esi") {
		return nil
	}
	loc := c.SourceLocation()
	if (loc.End-loc.Start)-len(text) != 7 || !strings.HasPrefix(text, "esi") {
		// Spelled some other way: a bogus comment, or a processing instruction
		// that starts with the same three letters. Unwrapping it would write
		// somebody else's syntax out as markup.
		e.res.NotComments++
		return nil
	}
	e.res.Comments++

	// The content has to be expanded before it goes in, because inserted content
	// is not re-parsed: an include written inside the comment would otherwise
	// reach the page as an include. There is nothing to stream here - the text is
	// already a string in memory, and its size is the comment's - so this is the
	// one place the program buffers on purpose.
	var out strings.Builder
	inner := strings.TrimPrefix(text, "esi")
	// The same depth, not one deeper: the comment is part of this document rather
	// than a fragment fetched from somewhere.
	nested := &expander{fetch: e.fetch, opts: e.opts, res: e.res, depth: e.depth, path: e.path}
	w, err := lolhtml.NewWriter(&out, nested.options()...)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, inner); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Replace(out.String(), lolhtml.HTML)
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	res, err := Expand(os.Stdout, os.Stdin, DirFetcher(root), DefaultOptions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "include:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

// DirFetcher serves fragments from a directory, which is the only fetcher this
// command offers on purpose: an include is a document naming something to fetch,
// and a program that fetches URLs a document names is a request forgery waiting
// for a page it did not write.
type DirFetcher string

// Fetch implements Fetcher. A src is a path under the directory, and anything
// that climbs out of it is refused rather than cleaned - a rejected include is a
// visible failure and a cleaned one is not.
func (d DirFetcher) Fetch(src string) (io.ReadCloser, error) {
	if strings.Contains(src, "..") || strings.HasPrefix(src, "/") || src == "" {
		return nil, errors.New("include: a src has to be a relative path inside the directory")
	}
	return os.Open(string(d) + "/" + src)
}

// Package lolhtml provides Go bindings for lol-html, Cloudflare's streaming
// HTML rewriter.
//
// The rewriter walks HTML in a single pass, without building a document tree,
// and invokes your handlers as it encounters content matching a CSS selector.
// Memory use is bounded by the largest element it has to buffer rather than by
// document size, which makes it suitable for rewriting responses of unknown
// length on the fly.
//
// # Streaming
//
// [NewWriter] returns an [io.WriteCloser], so a rewrite composes with the rest
// of the io package:
//
//	w, err := lolhtml.NewWriter(os.Stdout,
//		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
//			href, _ := e.Attribute("href")
//			return e.SetAttribute("href", absolutise(href))
//		}),
//	)
//	if err != nil {
//		return err
//	}
//	if _, err := io.Copy(w, resp.Body); err != nil {
//		return err
//	}
//	return w.Close()
//
// Close finishes the document and flushes the tail of the output; skipping it
// truncates the result. Chunk boundaries never affect handler behaviour, so
// input may arrive however the network delivers it.
//
// For a document already in memory, [Rewrite] and [RewriteString] wrap the same
// machinery.
//
// # Handler lifetime
//
// The value passed to a handler is valid only until that handler returns.
// lol-html reuses the underlying storage, so golol-html detaches the wrapper on
// the way out and every later method call returns [ErrDetached]. Copy out what
// you need rather than retaining the unit:
//
//	lolhtml.OnElement("img", func(e *lolhtml.Element) error {
//		src, _ := e.Attribute("src")   // fine: a Go string
//		seen = append(seen, e)         // useless: detached once this returns
//		return nil
//	})
//
// # Handler order
//
// More than one handler can see the same unit, and the order they run in
// follows two rules.
//
// Within one kind of registration, handlers run in the order they were
// registered: two [OnElement] handlers whose selectors both match, three
// [OnDocumentEnd] handlers, several [Element.OnEndTag] handlers on one element.
// Each sees what the previous one did, so a handler reading an attribute gets
// the value an earlier handler wrote to it.
//
// Between kinds, every selector-associated handler runs before every
// document-level handler for the same unit, whatever order the options were
// written in. [OnComment] runs before [OnDocumentComment] and [OnText] before
// [OnDocumentText] even when the document-level one was registered first,
// because lol-html keeps the two in separate lists. A rewrite that needs to see
// a unit before anything else does has to register a selector-associated
// handler, not a document-level one.
//
// # Removal suppresses output, not handler calls
//
// [Element.Remove] takes the element and its content out of the output. It does
// not stop handlers running for that content: a text handler still sees the text
// of a removed element, and an element handler still runs for its descendants.
// Their edits are discarded along with everything else, but a handler that
// accumulates - collecting a document's visible text, counting what it rewrote -
// has to notice for itself that the content it is looking at is on its way to
// being dropped. [Element.IsRemoved] is how an element handler checks.
//
// One corner does not behave the way Remove's description suggests. Removal
// decides the fate of the element's inner content at the moment it is called, so
// content inserted inside the element *after* that still reaches the output,
// with the element's tags no longer around it:
//
//	e.Remove()
//	e.Append("x", lolhtml.HTML)   // "x" is emitted, as a child of the parent
//	e.Append("x", lolhtml.HTML)
//	e.Remove()                    // "x" is discarded
//
// The two orders disagree, and only the second does what Remove promises. It
// matters most when two handlers share a selector, because then the order is
// decided by which option was written first rather than by either handler: one
// removing a <script> and one appending inside it will, in one of the two
// orders, emit the appended content as document markup. Insert first and remove
// last, or check [Element.IsRemoved] before inserting inside an element.
//
// [Element.Before], [Element.After] and [Element.Replace] position content
// outside the element, and surviving its removal is what they are for.
//
// # Inserting into a script or a style
//
// Neither [ContentType] is right for the inside of a <script> or a <style>, and
// the failures are quiet in opposite directions.
//
// Those two are *raw text* elements: an HTML parser does not decode character
// references in them. So [Text], which escapes <, > and &, produces content
// that is inert but no longer says what it said:
//
//	e.SetInnerContent(`if (a < b && c > d) {}`, lolhtml.Text)
//	// <script>if (a &lt; b &amp;&amp; c &gt; d) {}</script>
//
// The document is valid, nothing returns an error, and the script throws a
// syntax error in the browser. [Element.Attribute] and the HTML around it look
// exactly as intended, which is why this is easy to ship.
//
// [HTML] inserts the text as written, and then the element ends wherever the
// content says it does:
//
//	e.SetInnerContent(`var s = "</script><img src=1 onerror=alert(1)>";`, lolhtml.HTML)
//	// <script>var s = "</script><img src=1 onerror=alert(1)>";</script>
//
// That is a working injection out of a string literal, and it is the caller's
// responsibility rather than a defect: HTML means raw markup.
//
// There is no combination of the two that makes arbitrary text safe here, and
// escaping it correctly needs to know where in the JavaScript it lands - inside
// a string literal, "</script" has to become "<\/script", which is a JavaScript
// transformation rather than an HTML one. So: build script and style bodies from
// values you control, and if untrusted data has to reach a script, put it in a
// data attribute or a JSON <script type="application/json"> block and read it
// from there.
//
// [Comment.SetText] is the one context where this is checked for you: it refuses
// text containing a comment-closing sequence rather than emitting markup that
// escapes the comment.
//
// A textarea and a title are *escapable* raw text, where references are
// decoded, so Text behaves normally in them.
//
// # Character references are not decoded
//
// Text, comment text and attribute values are reported as raw source: the href
// of <a href="?a=1&amp;b=2"> is "?a=1&amp;b=2". lol-html has to be able to
// re-emit what it read, so it does not decode on the way in, and correspondingly
// escapes what you write. Reading a value and writing it back unchanged is
// therefore correct; comparing one against a decoded Go string is not. Use
// html.UnescapeString when you need the decoded form.
//
// # Errors
//
// A handler returning a non-nil error stops the rewrite; the error surfaces
// from the [Writer.Write] or [Writer.Close] that was running at the time,
// wrapped in a [HandlerError] you can unwrap. A handler that panics does not
// unwind through Rust: the panic is caught at the boundary and re-raised on the
// goroutine that called Write or Close.
//
// lol-html cannot resume after an error, so a Writer that has failed is
// poisoned and every later Write returns [ErrPoisoned]. A Writer that panics
// releases its native resources on the way out, so a caller who recovers does
// not leak them, but Close should still be deferred as a matter of course.
package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"bytes"
	"strconv"
	"unsafe"
)

// ContentType says how inserted content should be interpreted.
//
// The choice is context-insensitive: Text escapes the same three characters
// wherever the content lands. That is correct in element content, in escapable
// raw text (textarea and title) and inside a comment, and it is wrong
// inside <script> and <style>. See the package documentation on inserting into
// a script or a style.
type ContentType int

const (
	// Text inserts content as text, escaping <, > and & so that none of it can
	// be read as markup. This is the safe choice for untrusted values.
	//
	// It escapes nothing else. A quote, an apostrophe and a backtick pass
	// through, which is correct for element content. So does a NUL, as a literal
	// zero byte: any parser reading the result replaces it with U+FFFD, so a
	// value containing one does not survive a round trip.
	Text ContentType = iota

	// HTML inserts content as raw markup, parsed as part of the document. The
	// caller is responsible for everything about it, including that it does not
	// end the element it is being inserted into.
	HTML
)

func (ct ContentType) isHTML() bool { return ct == HTML }

func (ct ContentType) String() string {
	if ct == HTML {
		return "html"
	}
	return "text"
}

// SourceLocation is the half-open byte range a unit occupied in the input
// document, counted from the first byte fed to the rewriter.
type SourceLocation struct {
	Start int
	End   int
}

// Len reports the length of the range in bytes.
func (s SourceLocation) Len() int { return s.End - s.Start }

func (s SourceLocation) String() string {
	return strconv.Itoa(s.Start) + ".." + strconv.Itoa(s.End)
}

func sourceLocation(c C.lol_html_source_location_bytes_t) SourceLocation {
	return SourceLocation{Start: int(c.start), End: int(c.end)}
}

// Rewrite applies the handlers to a complete HTML document and returns the
// result.
//
// It is a convenience wrapper over [NewWriter] for input already in memory; for
// anything streamed, use NewWriter directly and avoid buffering the whole
// document.
func Rewrite(html []byte, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(html) + len(html)/8)

	w, err := NewWriter(&buf, opts...)
	if err != nil {
		return nil, err
	}
	// Deferred rather than only called on the error paths: a handler that
	// panics is re-raised by Write, and without this the native resources
	// would never be released. Close is idempotent, so the explicit call below
	// still reports the error from the final flush.
	defer w.Close()

	if _, err := w.Write(html); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RewriteString is [Rewrite] for strings.
func RewriteString(html string, opts ...Option) (string, error) {
	out, err := Rewrite(unsafe.Slice(unsafe.StringData(html), len(html)), opts...)
	return string(out), err
}

// Helpers --------------------------------------------------------------------

// emptyBytePtr backs the zero-length case where lol-html would panic-abort on a
// NULL pointer.
var emptyBytePtr = &[1]byte{}

func bytePtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return unsafe.Pointer(emptyBytePtr)
	}
	return unsafe.Pointer(&b[0])
}

// borrowBytes exposes a C buffer as a Go slice without copying.
//
// Valid only for the duration of the call it is handed to. This is used for the
// output sink, where the io.Writer contract already forbids the destination
// from retaining the slice, and where copying every chunk was measurably the
// dominant allocation cost. Anything that needs to outlive the call must copy.
func borrowBytes(p *C.char, n C.size_t) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
}

func quote(s string) string { return strconv.Quote(s) }

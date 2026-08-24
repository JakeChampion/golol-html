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
type ContentType int

const (
	// Text inserts content as text, escaping anything that would otherwise be
	// read as markup. This is the safe choice for untrusted values.
	Text ContentType = iota

	// HTML inserts content as raw markup, parsed as part of the document.
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

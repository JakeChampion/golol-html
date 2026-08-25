package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"fmt"
	"io"
	"runtime"
)

// A StreamFunc produces inserted content on demand, writing it into the sink
// instead of returning it.
//
// Use it when the content is large or generated incrementally: nothing has to be
// assembled in memory first - each write reaches the destination as it is made,
// measured in streamcommit_test.go.
//
// Returning an error aborts the rewrite, and the error surfaces from Write or
// Close - but what the function has already written stays written, which is the
// one place where failing costs something. A handler that fails discards its
// insertion:
//
//	e.Before("<div>partial", lolhtml.HTML)
//	return err
//	// the destination gets nothing: the insertion goes with the rewrite
//
// A StreamFunc that fails does not, because the point of it is that the content
// was already on its way:
//
//	e.StreamBefore(func(s *lolhtml.Sink) error {
//		if err := s.WriteString("<div>partial", lolhtml.HTML); err != nil {
//			return err
//		}
//		return err // a fetch that failed halfway, say
//	})
//	// the destination already has "<div>partial", unclosed <div> and all
//
// So the first byte written to the sink is a commitment: after it there is no
// error path that leaves a usable document, only a truncated one. Whatever has to
// be true before committing - the file opened, the request returned 200, the
// template parsed - has to be established in the handler, where returning an
// error still costs nothing, and the sink used only for content that is already
// known to exist. examples/gip/include is built that way round.
//
// "On demand" means when the content is emitted, which is neither immediately
// nor at the end, and two things follow from that.
//
// It cannot see anything the rewriter has not parsed yet. A function registered
// on an element runs while that element is being written out, so state gathered
// from later in the document is not there yet, and the failure is silent - you
// get the empty result your closure computed, not an error. Building a table of
// contents at a marker near the top of a page is the usual way to meet this: one
// streaming pass cannot do it. Read the document twice, buffer it, or put the
// content at the document end with [OnDocumentEnd], which is the one position
// that has seen everything.
//
// It may never run at all. If the content is discarded - the element was removed
// by a later handler, or it is inside something that was removed - the function
// is not called. So a StreamFunc is the wrong place for a side effect you need:
// count and log in the handler, and write only content in the sink.
//
// It must not finish mid-character. Writes may split a rune however they like -
// lol-html joins the pieces, which is what makes io.Copy from an arbitrary
// reader safe - but a sequence still open when the function returns is dropped,
// so returning then is [ErrIncompleteRune] rather than a shorter insertion
// nobody mentioned.
type StreamFunc func(*Sink) error

// A Sink receives the output of a StreamFunc.
//
// Writes may fall anywhere: a rune split across two of them is joined, so
// copying from a reader that knows nothing about UTF-8 boundaries is safe. What
// is not safe is stopping in the middle of one, and that is checked when the
// StreamFunc returns; see [ErrIncompleteRune].
//
// It is valid only for the duration of that call. The rewriter may run it on a
// different goroutine than the one that called Write, though never on more than
// one at a time, so a StreamFunc must not depend on goroutine-local state.
type Sink struct {
	unit[*C.lol_html_streaming_sink_t]

	// tail is the trailing bytes of an incomplete UTF-8 sequence, if the last
	// write ended in one. lol-html holds those bytes waiting for the rest and
	// discards them if the stream ends first, silently; see checkComplete.
	tail    [4]byte
	tailLen int
}

// Err reports the error that has already stopped this rewrite, if any.
//
// It exists because the sink's own methods cannot tell you. They write into
// lol-html's buffer, not to the destination, so a nil from WriteString,
// WriteChunk or a writer from AsWriter means the content was accepted - not that
// it arrived. A destination that fails is recorded and reported from the Write or
// Close that was running, and until then the sink goes on accepting everything:
// measured, fifty writes after a failing destination were all accepted and none
// reported anything.
//
// For short content that costs nothing. For the case a StreamFunc is for - large
// or incrementally produced content, the io.Copy of a big template the
// documentation recommends - it means copying the whole thing after there is
// nowhere for it to go. Err is how to stop:
//
//	e.StreamAppend(func(s *lolhtml.Sink) error {
//		for _, chunk := range chunks {
//			if err := s.Err(); err != nil {
//				return err
//			}
//			if err := s.WriteString(chunk, lolhtml.HTML); err != nil {
//				return err
//			}
//		}
//		return nil
//	})
//
// Returning it is optional: the rewrite is already failing and the error will
// surface from Write or Close either way. Returning it costs nothing and makes
// the abandoned work visible in a stack trace rather than silent.
//
// Nil means nothing has failed yet, not that anything has succeeded. There is no
// point at which the destination is known to have taken the content, because the
// rewriter may still be holding it.
//
// This is the destination failing under a StreamFunc that is still going. The
// other direction - the StreamFunc failing after the destination has taken
// something - is not recoverable at all; see [StreamFunc].
func (s *Sink) Err() error {
	if s.c == nil {
		return ErrDetached
	}
	if _, err := s.live(); err != nil {
		return err
	}
	if err := s.c.st.sinkErr; err != nil {
		return err
	}
	if err := s.c.st.handlerErr; err != nil {
		return err
	}
	return nil
}

// WriteString writes s to the sink, escaping it when ct is Text.
//
// s must be complete, valid UTF-8. Use WriteChunk for content split at
// arbitrary byte boundaries.
func (s *Sink) WriteString(str string, ct ContentType) error {
	p, err := s.live()
	if err != nil {
		return err
	}
	if s.tailLen > 0 {
		// lol-html joins a partial sequence to the next WriteChunk, and does
		// not join it to a WriteString: the held bytes become U+FFFD and the
		// string is written after them. Measured, WriteChunk("caf\xc3") then
		// WriteString("x") produced "caf\ufffdx" with no error.
		return fmt.Errorf("%w: WriteString while %d byte(s) % x from an earlier "+
			"WriteChunk are still waiting to be completed; finish the sequence "+
			"with WriteChunk first", ErrIncompleteRune, s.tailLen, s.tail[:s.tailLen])
	}
	sp, sl := strPtr(str)
	var cerr C.lol_html_str_t
	rc := C.golol_sink_write_str(p, sp, sl, C.bool(ct.isHTML()), &cerr)
	runtime.KeepAlive(str)
	if rc != 0 {
		return nativeErr("streaming_sink_write_str", cerr)
	}
	s.trackTail(str)
	return nil
}

// WriteChunk writes a fragment of UTF-8 to the sink, escaping it when ct is
// Text.
//
// Unlike WriteString, b need not be complete UTF-8: a trailing partial sequence
// is buffered and flushed once a later WriteChunk completes it, so content can
// be forwarded straight from a network read.
//
// Two ways of never completing it, both of which used to be silent and are now
// [ErrIncompleteRune]. A [StreamFunc] that returns with a sequence still open
// loses those bytes - lol-html drops them - and a WriteString while one is open
// does not join it: the held bytes become U+FFFD and the string follows them.
func (s *Sink) WriteChunk(b []byte, ct ContentType) error {
	p, err := s.live()
	if err != nil {
		return err
	}
	var cerr C.lol_html_str_t
	rc := C.golol_sink_write_utf8_chunk(p, (*C.char)(bytePtr(b)), C.size_t(len(b)),
		C.bool(ct.isHTML()), &cerr)
	runtime.KeepAlive(b)
	if rc != 0 {
		return nativeErr("streaming_sink_write_utf8_chunk", cerr)
	}
	s.trackTail(string(b))
	return nil
}

// trackTail records whether everything written so far ends in the middle of a
// UTF-8 sequence.
//
// It keeps at most four bytes: a rune is at most four bytes long, so the last
// four are enough to say whether the final one is complete. Splitting a rune
// across writes is fine - lol-html holds the prefix and joins it to the next
// chunk - so this is not about rejecting a split, only about noticing one that
// never gets finished.
func (s *Sink) trackTail(w string) {
	if w == "" {
		return
	}
	// The last four bytes of tail+w, which is all that can matter. A write of
	// four bytes or more supersedes the tail entirely.
	var buf [4]byte
	var n int
	if len(w) >= 4 {
		n = copy(buf[:], w[len(w)-4:])
	} else {
		var joined [8]byte
		m := copy(joined[:], s.tail[:s.tailLen])
		m += copy(joined[m:], w)
		if m > 4 {
			m = copy(buf[:], joined[m-4:m])
		} else {
			m = copy(buf[:], joined[:m])
		}
		n = m
	}

	// Walk back to the byte that starts the last sequence. A continuation byte
	// is 10xxxxxx; anything else begins one.
	start := -1
	for i := n - 1; i >= 0 && i >= n-4; i-- {
		if buf[i]&0xC0 != 0x80 {
			start = i
			break
		}
	}
	if start < 0 {
		// Four continuation bytes with no lead: not valid UTF-8 at all, which
		// lol-html would have refused. Nothing to hold.
		s.tailLen = 0
		return
	}
	if have, want := n-start, runeLen(buf[start]); have < want {
		s.tailLen = copy(s.tail[:], buf[start:n])
		return
	}
	s.tailLen = 0
}

// runeLen is the length in bytes of the sequence a lead byte begins, or 1 for
// anything that does not begin one.
func runeLen(b byte) int {
	switch {
	case b&0x80 == 0:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return 1
}

// checkComplete reports whether the content written to this sink ended in the
// middle of a UTF-8 sequence.
//
// It matters because those bytes are dropped rather than reported. lol-html
// holds an incomplete sequence waiting for the rest of it, which is what makes
// io.Copy into AsWriter safe across arbitrary chunk boundaries - but if the
// StreamFunc returns while a sequence is still open, the bytes go nowhere and
// nothing says so. Measured before this check existed:
//
//	s.WriteChunk([]byte("ab\xc3"), lolhtml.Text)  ->  <div>ab</div>, nil
//
// The document path does not lose them - a truncated sequence in the input
// becomes U+FFFD - so this was the one place in the library where content
// vanished silently.
func (s *Sink) checkComplete() error {
	if s.tailLen == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d trailing byte(s) % x begin a UTF-8 sequence that "+
		"was never completed, and lol-html discards them", ErrIncompleteRune,
		s.tailLen, s.tail[:s.tailLen])
}

// AsWriter adapts the sink to io.Writer, so content can be produced with
// io.Copy or fmt.Fprintf. Writes go through WriteChunk, so chunk boundaries may
// fall anywhere in a UTF-8 sequence.
//
// A nil error from the returned writer means the content was accepted, not that
// it was delivered: the sink writes into lol-html's buffer and a destination
// failure surfaces from Write or Close instead. So io.Copy will happily copy a
// whole template into a rewrite that has already failed. Check [Sink.Err]
// between chunks if that matters, which for anything large it does.
func (s *Sink) AsWriter(ct ContentType) io.Writer {
	return sinkWriter{sink: s, ct: ct}
}

type sinkWriter struct {
	sink *Sink
	ct   ContentType
}

func (w sinkWriter) Write(p []byte) (int, error) {
	if err := w.sink.WriteChunk(p, w.ct); err != nil {
		return 0, err
	}
	return len(p), nil
}

// streamingCB carries a StreamFunc across the boundary. Unlike handler handles,
// these are released by lol-html's drop callback rather than with the rewriter,
// because each insertion creates its own.
type streamingCB struct {
	c        *core
	selector string
	fn       StreamFunc
}

func (cb *streamingCB) call(s *Sink) error { return cb.fn(s) }

// streamOp is the shape shared by every streaming insertion shim.
type streamOp[P comparable] func(P, C.uintptr_t, *C.lol_html_str_t) C.int

func withStream[P comparable](u *unit[P], selector string, fn StreamFunc, op string, call streamOp[P]) error {
	p, err := u.live()
	if err != nil {
		return err
	}
	if fn == nil {
		return errNilStreamFunc
	}

	// Released by golol_streaming_drop_cb, which lol-html calls exactly once
	// after the last use of the handler.
	h := newHandle(&streamingCB{c: u.c, selector: selector, fn: fn})

	var cerr C.lol_html_str_t
	if call(p, C.uintptr_t(h), &cerr) != 0 {
		// lol-html rejected the handler, so it will never call drop.
		deleteHandle(h)
		return nativeErr(op, cerr)
	}
	return nil
}

// Element streaming insertions ------------------------------------------------

// StreamBefore inserts content before the element's start tag, produced on
// demand by fn.
func (e *Element) StreamBefore(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_before", cfElementStreamBefore)
}

// StreamAfter inserts content after the element's end tag, produced on demand
// by fn.
func (e *Element) StreamAfter(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_after", cfElementStreamAfter)
}

// StreamPrepend inserts content as the element's first child, produced on
// demand by fn.
func (e *Element) StreamPrepend(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_prepend", cfElementStreamPrepend)
}

// StreamAppend inserts content as the element's last child, produced on demand
// by fn.
func (e *Element) StreamAppend(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_append", cfElementStreamAppend)
}

// StreamSetInnerContent replaces the element's content with output produced on
// demand by fn.
func (e *Element) StreamSetInnerContent(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_set_inner_content",
		cfElementStreamSetInnerContent)
}

// StreamReplace replaces the element, tags included, with output produced on
// demand by fn.
func (e *Element) StreamReplace(fn StreamFunc) error {
	return withStream(&e.unit, e.selector, fn, "element_streaming_replace", cfElementStreamReplace)
}

// EndTag streaming insertions -------------------------------------------------

// StreamBefore inserts content just inside the end tag, produced on demand by fn.
func (t *EndTag) StreamBefore(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "end_tag_streaming_before", cfEndTagStreamBefore)
}

// StreamAfter inserts content just after the end tag, produced on demand by fn.
func (t *EndTag) StreamAfter(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "end_tag_streaming_after", cfEndTagStreamAfter)
}

// StreamReplace replaces the end tag with output produced on demand by fn.
func (t *EndTag) StreamReplace(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "end_tag_streaming_replace", cfEndTagStreamReplace)
}

// TextChunk streaming insertions ----------------------------------------------

// StreamBefore inserts content before the chunk, produced on demand by fn.
func (t *TextChunk) StreamBefore(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "text_chunk_streaming_before", cfTextChunkStreamBefore)
}

// StreamAfter inserts content after the chunk, produced on demand by fn.
func (t *TextChunk) StreamAfter(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "text_chunk_streaming_after", cfTextChunkStreamAfter)
}

// StreamReplace replaces the chunk with output produced on demand by fn.
func (t *TextChunk) StreamReplace(fn StreamFunc) error {
	return withStream(&t.unit, t.selector, fn, "text_chunk_streaming_replace", cfTextChunkStreamReplace)
}

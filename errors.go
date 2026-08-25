package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDetached is returned by any method on a rewritable unit (Element, Comment,
// TextChunk, Doctype, DocumentEnd, EndTag) that is called after its handler has
// returned.
//
// lol-html only guarantees these values are alive for the duration of the
// handler invocation, so golol-html detaches the Go wrapper on return. Copy out
// whatever you need inside the handler instead of retaining the unit.
var ErrDetached = errors.New("lolhtml: rewritable unit used outside its handler")

// errNilStreamFunc reports a streaming insertion with no function to run.
var errNilStreamFunc = errors.New("lolhtml: StreamFunc is nil")

// ErrClosed is returned by Write on a Writer that has already been closed.
var ErrClosed = errors.New("lolhtml: writer is closed")

// ErrPoisoned is returned by Write and Close on a Writer whose earlier Write or
// Close failed. lol-html leaves the rewriter unusable after an error, and calling
// into it again would abort the process, so golol-html refuses instead.
//
// The refusal wraps the error that caused it, so errors.Is and errors.As reach
// past the sentinel to the handler error or destination-writer error underneath.
// That matters because the first failure is reported once, from the call that
// was running, and the ordinary Go shape - write, then check Close - asks
// afterwards. A handler panic is the exception: it poisons the Writer on its way
// to the caller without leaving an error, and the sentinel then stands alone.
var ErrPoisoned = errors.New("lolhtml: writer is poisoned by an earlier error")

// ErrIncompleteRune reports a UTF-8 sequence written into a [Sink] that never
// gets completed.
//
// Splitting a rune across writes is fine: lol-html holds the prefix and joins it
// to the next [Sink.WriteChunk], which is what makes copying from an arbitrary
// reader into [Sink.AsWriter] safe. Two things do not finish it, and both were
// silent:
//
//	the StreamFunc returns    the held bytes are dropped, so the insertion is
//	                          shorter than the content and nothing says so
//	a WriteString arrives     the held bytes become U+FFFD and the string is
//	                          written after them
//
// The first happens whenever the source is truncated mid-character. The second
// is a mistake in the calling code, and [Sink.WriteChunk] had documented it as
// something not to do without there being any way to notice having done it.
var ErrIncompleteRune = errors.New("lolhtml: streamed content has an unfinished UTF-8 sequence")

// A NativeError is an error reported by the underlying lol-html library.
type NativeError struct {
	Op      string // the operation that failed, e.g. "set_attribute"
	Message string // lol-html's own message
}

func (e *NativeError) Error() string {
	if e.Message == "" {
		return "lolhtml: " + e.Op + " failed"
	}
	return "lolhtml: " + e.Op + ": " + e.Message
}

// ErrMemoryLimitExceeded matches the error lol-html reports when a rewrite
// exceeds [MemorySettings.MaxMemory]:
//
//	if errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
//		// the response is truncated; discard it
//	}
//
// It is a match rather than a value: the error a caller receives is a
// [NativeError] carrying lol-html's own message, and this is what
// [errors.Is] compares it against.
//
// Worth distinguishing because it is one of the two failures a streaming caller
// has to act on rather than merely report, and because [WithGracefulBailOut]
// changes what has already reached the sink when it happens.
var ErrMemoryLimitExceeded = errors.New("lolhtml: the memory limit has been exceeded")

// ErrAmbiguousTag matches the error [WithStrict] produces when the parser
// reaches a tag whose meaning depends on markup it cannot see:
//
//	if errors.Is(err, lolhtml.ErrAmbiguousTag) {
//		// the response is truncated; discard it
//	}
//
// It is the other failure a streaming caller has to act on, and the alternative
// was matching lol-html's prose - which this package's own tests were doing,
// with strings.Contains(ne.Message, "ambiguous").
var ErrAmbiguousTag = errors.New("lolhtml: strict mode refused an ambiguous tag")

// Is lets errors.Is reach the two conditions a caller branches on. Any other
// target is not something this error can claim to be, so the answer is no and
// errors.Is falls back to its own comparison.
func (e *NativeError) Is(target error) bool {
	switch target {
	case ErrMemoryLimitExceeded:
		return e.Message == memoryLimitExceededMessage
	case ErrAmbiguousTag:
		return strings.HasPrefix(e.Message, ambiguousTagPrefix)
	}
	return false
}

// MemoryLimitExceeded reports whether this error is lol-html's memory-limit
// error.
//
// [ErrMemoryLimitExceeded] with [errors.Is] says the same thing and reads the
// way Go callers expect, including through the wrapping that [ErrPoisoned] adds
// to a later Close. This remains because it is exported.
func (e *NativeError) MemoryLimitExceeded() bool {
	return e.Message == memoryLimitExceededMessage
}

// lol-html's wording for the two errors that are classified rather than merely
// reported. Both are gated by tests that provoke the real thing, so a reword
// upstream fails the build rather than silently turning a caller's guard off.
//
// From c-api/src/rewriter.rs -> MemoryLimitExceededError.
const memoryLimitExceededMessage = "The memory limit has been exceeded."

// The ambiguity message names the offending tag - "a text content tag
// (`<xmp>`)" - so only the part before it is fixed. Measured identical for
// every shape that triggers it: xmp, style, title and iframe, in <select> and
// in <frameset>.
const ambiguousTagPrefix = "The parser has encountered a text content tag ("

// nativeErr converts a shim out-parameter into a Go error, freeing the
// library-allocated message. It must be called only when the shim reported
// failure; err.data is NULL when lol-html had nothing to say.
func nativeErr(op string, cerr C.lol_html_str_t) error {
	return &NativeError{Op: op, Message: takeStr(cerr)}
}

// A HandlerError wraps an error returned by one of your own handlers, so that
// the error surfacing from Write or Close is traceable back to the handler that
// produced it. Unwrap returns the original error.
type HandlerError struct {
	// Selector is the selector the handler was registered for, empty for a
	// document-level one. An end-tag or streaming handler inherits it from the
	// handler that registered it, so a failure in one says which selector it
	// belongs to rather than only which kind it was.
	Selector string
	// Kind is one of "element", "comment", "text", "doctype", "document-end",
	// "end-tag" and "streaming". The last is the output of a [StreamFunc], which
	// runs later than the handler that registered it and is reported separately
	// for that reason.
	Kind string
	Err  error
}

func (e *HandlerError) Error() string {
	if e.Selector == "" {
		return fmt.Sprintf("lolhtml: %s handler: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("lolhtml: %s handler for %q: %v", e.Kind, e.Selector, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

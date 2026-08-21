package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
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

// ErrPoisoned is returned by Write on a Writer whose earlier Write or Close
// failed. lol-html leaves the rewriter unusable after an error, and calling into
// it again would abort the process, so golol-html refuses instead.
var ErrPoisoned = errors.New("lolhtml: writer is poisoned by an earlier error")

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

// MemoryLimitExceeded reports whether this error is lol-html's memory-limit
// error, which is worth distinguishing because [WithGracefulBailOut] makes it
// recoverable at the response level.
func (e *NativeError) MemoryLimitExceeded() bool {
	return e.Message == memoryLimitExceededMessage
}

// lol-html's wording for the memory-limit error, from
// c-api/src/rewriter.rs -> MemoryLimitExceededError.
const memoryLimitExceededMessage = "The memory limit has been exceeded."

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
	Selector string // the selector the handler was registered for, if any
	Kind     string // "element", "comment", "text", "doctype", "document-end", "end-tag"
	Err      error
}

func (e *HandlerError) Error() string {
	if e.Selector == "" {
		return fmt.Sprintf("lolhtml: %s handler: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("lolhtml: %s handler for %q: %v", e.Kind, e.Selector, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

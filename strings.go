package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// takeStr copies a library-allocated string into Go memory and frees the
// original with lol_html_str_free, the only legal way to release it. A NULL
// data pointer means "absent" and yields "".
func takeStr(s C.lol_html_str_t) string {
	if s.data == nil {
		return ""
	}
	out := C.GoStringN(s.data, C.int(s.len))
	C.lol_html_str_free(s)
	return out
}

// takeOptStr is takeStr for getters where NULL and "" are different answers,
// such as a doctype with no name versus a doctype named "".
func takeOptStr(s C.lol_html_str_t) (string, bool) {
	if s.data == nil {
		return "", false
	}
	out := C.GoStringN(s.data, C.int(s.len))
	C.lol_html_str_free(s)
	return out, true
}

// emptyByte backs the zero-length case in strPtr, because lol-html panic-aborts
// on a NULL pointer even when the accompanying length is zero.
var emptyByte = [1]byte{}

// strPtr lends a Go string's backing array to C for the duration of one call.
//
// This is legal under the cgo pointer rules because lol-html never retains a
// caller-supplied buffer past the call that supplied it (in Rust these are
// borrowed slices, so the compiler enforces it), and it saves a malloc plus
// copy on every mutation. Every API here is length-delimited, so interior NUL
// bytes are fine. Callers must keep the string alive across the call; the
// with* helpers below do that.
func strPtr(s string) (*C.char, C.size_t) {
	if len(s) == 0 {
		return (*C.char)(unsafe.Pointer(&emptyByte[0])), 0
	}
	return (*C.char)(unsafe.Pointer(unsafe.StringData(s))), C.size_t(len(s))
}

// contentOp is the shape shared by every "insert content" shim: before, after,
// replace, append, prepend, set_inner_content and doc-end append.
type contentOp[P comparable] func(P, *C.char, C.size_t, C.bool, *C.lol_html_str_t) C.int

// withContent runs one content mutation, translating a shim failure into a
// *NativeError carrying lol-html's own message.
func withContent[P comparable](unit P, content string, isHTML bool, op string, fn contentOp[P]) error {
	p, n := strPtr(content)
	var cerr C.lol_html_str_t
	rc := fn(unit, p, n, C.bool(isHTML), &cerr)
	runtime.KeepAlive(content)
	if rc != 0 {
		return nativeErrFor(op, cerr, content)
	}
	return nil
}

// nameOp is the shape shared by the shims that set a single name or text value.
type nameOp[P comparable] func(P, *C.char, C.size_t, *C.lol_html_str_t) C.int

// withName runs one name/text mutation.
func withName[P comparable](unit P, value string, op string, fn nameOp[P]) error {
	p, n := strPtr(value)
	var cerr C.lol_html_str_t
	rc := fn(unit, p, n, &cerr)
	runtime.KeepAlive(value)
	if rc != 0 {
		return nativeErrFor(op, cerr, value)
	}
	return nil
}

package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// goStrN copies n bytes of a C buffer into a Go string.
//
// Not C.GoStringN, whose length is a C.int - 32 bits on every platform here.
// lol-html puts no ceiling on a token, so a comment or text node past 2 GiB
// arrives with a size_t length that does not fit: the conversion keeps the low
// 32 bits, which silently halves a 4 GiB token and panics outright whenever bit
// 31 is set. unsafe.Slice takes the length the library actually reported.
func goStrN(p *C.char, n C.size_t) string {
	if p == nil || n == 0 {
		return ""
	}
	return unsafe.String(&copyOut(p, n)[0], int(n))
}

// goBytesN is goStrN for callers wanting bytes.
func goBytesN(p *C.char, n C.size_t) []byte {
	if p == nil || n == 0 {
		return nil
	}
	return copyOut(p, n)
}

// copyOut copies n bytes out of C memory into one fresh Go allocation.
//
// Written out rather than left to string(unsafe.Slice(...)), which is shorter
// and sometimes free: the compiler keeps a short result on the stack when it
// does not escape, so reading a one-byte tag name would cost nothing and
// reading a forty-byte one would cost an allocation. This package documents the
// cost of reading a name or a value as a fixed number per call, and alloc_test.go
// pins it; a number that depends on the length of the document's identifiers and
// on what the caller does with the answer is not one worth documenting. One
// allocation, always, is the same bargain C.GoStringN made.
func copyOut(p *C.char, n C.size_t) []byte {
	b := make([]byte, n)
	copy(b, unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
	return b
}

// takeStr copies a library-allocated string into Go memory and frees the
// original with lol_html_str_free, the only legal way to release it. A NULL
// data pointer means "absent" and yields "".
func takeStr(s C.lol_html_str_t) string {
	if s.data == nil {
		return ""
	}
	out := goStrN(s.data, s.len)
	C.lol_html_str_free(s)
	return out
}

// takeOptStr is takeStr for getters where NULL and "" are different answers,
// such as a doctype with no name versus a doctype named "".
func takeOptStr(s C.lol_html_str_t) (string, bool) {
	if s.data == nil {
		return "", false
	}
	out := goStrN(s.data, s.len)
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

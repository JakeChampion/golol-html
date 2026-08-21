package lolhtml

/*
#include "shim.h"
*/
import "C"

// Adapter table for the C shims.
//
// cgo functions are not first-class values in Go - referring to C.f without
// calling it yields an unsafe.Pointer, not a func - so the generic helpers in
// strings.go and streaming.go cannot take them directly. Wrapping each shim in
// a Go closure once, here, keeps every method on the unit types a single line
// and confines the boilerplate to one file.
//
// Names follow the shim they wrap: cfElementBefore wraps golol_element_before.

// Content insertion: (unit, content, len, is_html, err) -> int.
var (
	cfCommentBefore contentOp[*C.lol_html_comment_t] = func(u *C.lol_html_comment_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_comment_before(u, s, n, h, e)
	}
	cfCommentAfter contentOp[*C.lol_html_comment_t] = func(u *C.lol_html_comment_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_comment_after(u, s, n, h, e)
	}
	cfCommentReplace contentOp[*C.lol_html_comment_t] = func(u *C.lol_html_comment_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_comment_replace(u, s, n, h, e)
	}
	cfElementBefore contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_before(u, s, n, h, e)
	}
	cfElementPrepend contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_prepend(u, s, n, h, e)
	}
	cfElementAppend contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_append(u, s, n, h, e)
	}
	cfElementAfter contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_after(u, s, n, h, e)
	}
	cfElementSetInnerContent contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_set_inner_content(u, s, n, h, e)
	}
	cfElementReplace contentOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_element_replace(u, s, n, h, e)
	}
	cfEndTagBefore contentOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_before(u, s, n, h, e)
	}
	cfEndTagAfter contentOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_after(u, s, n, h, e)
	}
	cfTextChunkBefore contentOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_before(u, s, n, h, e)
	}
	cfTextChunkAfter contentOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_after(u, s, n, h, e)
	}
	cfTextChunkReplace contentOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_replace(u, s, n, h, e)
	}
	cfDocEndAppend contentOp[*C.lol_html_doc_end_t] = func(u *C.lol_html_doc_end_t, s *C.char, n C.size_t, h C.bool, e *C.lol_html_str_t) C.int {
		return C.golol_doc_end_append(u, s, n, h, e)
	}
)

// Name and text assignment: (unit, value, len, err) -> int.
var (
	cfCommentTextSet nameOp[*C.lol_html_comment_t] = func(u *C.lol_html_comment_t, s *C.char, n C.size_t, e *C.lol_html_str_t) C.int {
		return C.golol_comment_text_set(u, s, n, e)
	}
	cfElementTagNameSet nameOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, s *C.char, n C.size_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_tag_name_set(u, s, n, e)
	}
	cfEndTagNameSet nameOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, s *C.char, n C.size_t, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_name_set(u, s, n, e)
	}
)

// Streaming insertion: (unit, handle, err) -> int.
var (
	cfElementStreamPrepend streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_prepend(u, h, e)
	}
	cfElementStreamAppend streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_append(u, h, e)
	}
	cfElementStreamBefore streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_before(u, h, e)
	}
	cfElementStreamAfter streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_after(u, h, e)
	}
	cfElementStreamSetInnerContent streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_set_inner_content(u, h, e)
	}
	cfElementStreamReplace streamOp[*C.lol_html_element_t] = func(u *C.lol_html_element_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_element_streaming_replace(u, h, e)
	}
	cfEndTagStreamBefore streamOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_streaming_before(u, h, e)
	}
	cfEndTagStreamAfter streamOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_streaming_after(u, h, e)
	}
	cfEndTagStreamReplace streamOp[*C.lol_html_end_tag_t] = func(u *C.lol_html_end_tag_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_end_tag_streaming_replace(u, h, e)
	}
	cfTextChunkStreamBefore streamOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_streaming_before(u, h, e)
	}
	cfTextChunkStreamAfter streamOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_streaming_after(u, h, e)
	}
	cfTextChunkStreamReplace streamOp[*C.lol_html_text_chunk_t] = func(u *C.lol_html_text_chunk_t, h C.uintptr_t, e *C.lol_html_str_t) C.int {
		return C.golol_text_chunk_streaming_replace(u, h, e)
	}
)

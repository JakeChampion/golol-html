// C shim between cgo and lol-html.
//
// Two jobs:
//
//  1. Error retrieval. lol-html keeps its last error in a Rust thread_local
//     (c-api/src/errors.rs). A cgo call is pinned to one OS thread only for its
//     own duration, so retrieving the error in a *separate* cgo call can read
//     the wrong thread and silently find nothing. Every fallible wrapper here
//     therefore performs the call and takes the error in a single cgo call,
//     handing the message back through an out-parameter.
//
//  2. Signature adaptation. Go closures cross the boundary as runtime/cgo
//     handles typed uintptr_t, which the shim casts to the void* user_data
//     lol-html expects. This avoids unsafe.Pointer(uintptr(...)) round-trips
//     that break the cgo pointer-passing rules. A handle of 0 means "no
//     handler", and the shim passes NULL so lol-html can skip parsing content
//     nobody asked for.
#ifndef GOLOL_SHIM_H
#define GOLOL_SHIM_H

#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>
#include "lol_html.h"

// Declares an "insert content" wrapper: (unit, content, len, is_html) -> int.
#define GOLOL_DECL_CONTENT(name, unit_t) \
    int name(unit_t *unit, const char *content, size_t content_len, \
             bool is_html, lol_html_str_t *err)

// Declares a streaming-handler wrapper: (unit, go_handle) -> int.
#define GOLOL_DECL_STREAMING(name, unit_t) \
    int name(unit_t *unit, uintptr_t handle, lol_html_str_t *err)

// Selector and rewriter construction -----------------------------------------

lol_html_selector_t *golol_selector_parse(const char *sel, size_t sel_len,
                                          lol_html_str_t *err);

int golol_builder_add_element_handlers(lol_html_rewriter_builder_t *b,
                                       const lol_html_selector_t *sel,
                                       uintptr_t element_h, uintptr_t comment_h,
                                       uintptr_t text_h, lol_html_str_t *err);

void golol_builder_add_document_handlers(lol_html_rewriter_builder_t *b,
                                        uintptr_t doctype_h, uintptr_t comment_h,
                                        uintptr_t text_h, uintptr_t doc_end_h);

lol_html_rewriter_t *golol_rewriter_build(lol_html_rewriter_builder_t *b,
                                          const char *encoding, size_t encoding_len,
                                          lol_html_memory_settings_t mem,
                                          uintptr_t sink_h, bool strict,
                                          bool esi_tags, lol_html_str_t *err);

int golol_rewriter_write(lol_html_rewriter_t *rw, const char *chunk, size_t len,
                         lol_html_str_t *err);
int golol_rewriter_end(lol_html_rewriter_t *rw, lol_html_str_t *err);

// Content insertion ----------------------------------------------------------

GOLOL_DECL_CONTENT(golol_comment_before, lol_html_comment_t);
GOLOL_DECL_CONTENT(golol_comment_after, lol_html_comment_t);
GOLOL_DECL_CONTENT(golol_comment_replace, lol_html_comment_t);

GOLOL_DECL_CONTENT(golol_element_before, lol_html_element_t);
GOLOL_DECL_CONTENT(golol_element_prepend, lol_html_element_t);
GOLOL_DECL_CONTENT(golol_element_append, lol_html_element_t);
GOLOL_DECL_CONTENT(golol_element_after, lol_html_element_t);
GOLOL_DECL_CONTENT(golol_element_set_inner_content, lol_html_element_t);
GOLOL_DECL_CONTENT(golol_element_replace, lol_html_element_t);

GOLOL_DECL_CONTENT(golol_end_tag_before, lol_html_end_tag_t);
GOLOL_DECL_CONTENT(golol_end_tag_after, lol_html_end_tag_t);

GOLOL_DECL_CONTENT(golol_text_chunk_before, lol_html_text_chunk_t);
GOLOL_DECL_CONTENT(golol_text_chunk_after, lol_html_text_chunk_t);
GOLOL_DECL_CONTENT(golol_text_chunk_replace, lol_html_text_chunk_t);

GOLOL_DECL_CONTENT(golol_doc_end_append, lol_html_doc_end_t);

// Streaming content insertion ------------------------------------------------

GOLOL_DECL_STREAMING(golol_element_streaming_prepend, lol_html_element_t);
GOLOL_DECL_STREAMING(golol_element_streaming_append, lol_html_element_t);
GOLOL_DECL_STREAMING(golol_element_streaming_before, lol_html_element_t);
GOLOL_DECL_STREAMING(golol_element_streaming_after, lol_html_element_t);
GOLOL_DECL_STREAMING(golol_element_streaming_set_inner_content, lol_html_element_t);
GOLOL_DECL_STREAMING(golol_element_streaming_replace, lol_html_element_t);

GOLOL_DECL_STREAMING(golol_end_tag_streaming_before, lol_html_end_tag_t);
GOLOL_DECL_STREAMING(golol_end_tag_streaming_after, lol_html_end_tag_t);
GOLOL_DECL_STREAMING(golol_end_tag_streaming_replace, lol_html_end_tag_t);

GOLOL_DECL_STREAMING(golol_text_chunk_streaming_before, lol_html_text_chunk_t);
GOLOL_DECL_STREAMING(golol_text_chunk_streaming_after, lol_html_text_chunk_t);
GOLOL_DECL_STREAMING(golol_text_chunk_streaming_replace, lol_html_text_chunk_t);

int golol_sink_write_str(lol_html_streaming_sink_t *sink, const char *s,
                         size_t len, bool is_html, lol_html_str_t *err);
int golol_sink_write_utf8_chunk(lol_html_streaming_sink_t *sink, const char *s,
                                size_t len, bool is_html, lol_html_str_t *err);

// Named mutations ------------------------------------------------------------

int golol_comment_text_set(lol_html_comment_t *c, const char *text, size_t len,
                           lol_html_str_t *err);
int golol_element_tag_name_set(lol_html_element_t *el, const char *name,
                               size_t len, lol_html_str_t *err);
int golol_end_tag_name_set(lol_html_end_tag_t *et, const char *name, size_t len,
                           lol_html_str_t *err);
int golol_element_has_attribute(const lol_html_element_t *el, const char *name,
                                size_t len, lol_html_str_t *err);
int golol_element_set_attribute(lol_html_element_t *el, const char *name, size_t nl,
                                const char *value, size_t vl, lol_html_str_t *err);
int golol_element_remove_attribute(lol_html_element_t *el, const char *name,
                                   size_t len, lol_html_str_t *err);
int golol_element_add_end_tag_handler(lol_html_element_t *el, uintptr_t handle,
                                      lol_html_str_t *err);

// User data ------------------------------------------------------------------
//
// lol-html stores an opaque void* per unit so C callers can carry state between
// handlers that see the same unit. We store a cgo handle there, which is why
// these take and return uintptr_t rather than void*.

#define GOLOL_DECL_USER_DATA(unit_name, unit_t)                               \
    void golol_##unit_name##_user_data_set(unit_t *unit, uintptr_t handle);   \
    uintptr_t golol_##unit_name##_user_data_get(const unit_t *unit)

GOLOL_DECL_USER_DATA(element, lol_html_element_t);
GOLOL_DECL_USER_DATA(comment, lol_html_comment_t);
GOLOL_DECL_USER_DATA(text_chunk, lol_html_text_chunk_t);
GOLOL_DECL_USER_DATA(doctype, lol_html_doctype_t);

#endif  // GOLOL_SHIM_H

#include "shim.h"
// cgo generates prototypes for the //export'ed Go callbacks and owns their exact
// types (it emits `char *` where lol-html declares `const char *`), so we
// include its header rather than redeclaring them.
#include "_cgo_export.h"

// Trampolines -----------------------------------------------------------------
//
// These adapt cgo's generated callback signatures to the function-pointer types
// lol-html expects, and are where the const cast lives.

static void sink_trampoline(const char *chunk, size_t len, void *ud) {
    golol_sink_cb((char *)chunk, len, (uintptr_t)ud);
}

// Each unit trampoline hands the cgo.Handle back to Go as an integer, so the Go
// side never routes a non-pointer value through unsafe.Pointer.
#define GOLOL_DEF_TRAMPOLINE(name, unit_t, go_cb)                              \
    static lol_html_rewriter_directive_t name(unit_t *unit, void *ud) {        \
        return go_cb(unit, (uintptr_t)ud);                                     \
    }

GOLOL_DEF_TRAMPOLINE(element_trampoline, lol_html_element_t, golol_element_cb)
GOLOL_DEF_TRAMPOLINE(comment_trampoline, lol_html_comment_t, golol_comment_cb)
GOLOL_DEF_TRAMPOLINE(text_trampoline, lol_html_text_chunk_t, golol_text_cb)
GOLOL_DEF_TRAMPOLINE(doctype_trampoline, lol_html_doctype_t, golol_doctype_cb)
GOLOL_DEF_TRAMPOLINE(doc_end_trampoline, lol_html_doc_end_t, golol_doc_end_cb)
GOLOL_DEF_TRAMPOLINE(end_tag_trampoline, lol_html_end_tag_t, golol_end_tag_cb)

static int streaming_write_trampoline(lol_html_streaming_sink_t *sink, void *ud) {
    return golol_streaming_write_cb(sink, (uintptr_t)ud);
}

static void streaming_drop_trampoline(void *ud) {
    golol_streaming_drop_cb((uintptr_t)ud);
}

// Selector and rewriter construction -----------------------------------------

lol_html_selector_t *golol_selector_parse(const char *sel, size_t sel_len,
                                          lol_html_str_t *err) {
    lol_html_selector_t *s = lol_html_selector_parse(sel, sel_len);
    if (s == NULL) *err = lol_html_take_last_error();
    return s;
}

int golol_builder_add_element_handlers(lol_html_rewriter_builder_t *b,
                                       const lol_html_selector_t *sel,
                                       uintptr_t element_h, uintptr_t comment_h,
                                       uintptr_t text_h, lol_html_str_t *err) {
    int rc = lol_html_rewriter_builder_add_element_content_handlers(
        b, sel,
        element_h ? element_trampoline : NULL, (void *)element_h,
        comment_h ? comment_trampoline : NULL, (void *)comment_h,
        text_h ? text_trampoline : NULL, (void *)text_h);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

void golol_builder_add_document_handlers(lol_html_rewriter_builder_t *b,
                                        uintptr_t doctype_h, uintptr_t comment_h,
                                        uintptr_t text_h, uintptr_t doc_end_h) {
    lol_html_rewriter_builder_add_document_content_handlers(
        b,
        doctype_h ? doctype_trampoline : NULL, (void *)doctype_h,
        comment_h ? comment_trampoline : NULL, (void *)comment_h,
        text_h ? text_trampoline : NULL, (void *)text_h,
        doc_end_h ? doc_end_trampoline : NULL, (void *)doc_end_h);
}

lol_html_rewriter_t *golol_rewriter_build(lol_html_rewriter_builder_t *b,
                                          const char *encoding, size_t encoding_len,
                                          lol_html_memory_settings_t mem,
                                          uintptr_t sink_h, bool strict,
                                          bool esi_tags, lol_html_str_t *err) {
    lol_html_rewriter_t *rw =
        esi_tags ? unstable_lol_html_rewriter_build_with_esi_tags(
                       b, encoding, encoding_len, mem, sink_trampoline,
                       (void *)sink_h, strict)
                 : lol_html_rewriter_build(b, encoding, encoding_len, mem,
                                           sink_trampoline, (void *)sink_h, strict);
    if (rw == NULL) *err = lol_html_take_last_error();
    return rw;
}

int golol_rewriter_write(lol_html_rewriter_t *rw, const char *chunk, size_t len,
                         lol_html_str_t *err) {
    int rc = lol_html_rewriter_write(rw, chunk, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_rewriter_end(lol_html_rewriter_t *rw, lol_html_str_t *err) {
    int rc = lol_html_rewriter_end(rw);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

// Content insertion ----------------------------------------------------------

#define GOLOL_DEF_CONTENT(name, unit_t, cfn)                                  \
    int name(unit_t *unit, const char *content, size_t content_len,           \
             bool is_html, lol_html_str_t *err) {                             \
        int rc = cfn(unit, content, content_len, is_html);                     \
        if (rc != 0) *err = lol_html_take_last_error();                        \
        return rc;                                                             \
    }

GOLOL_DEF_CONTENT(golol_comment_before, lol_html_comment_t, lol_html_comment_before)
GOLOL_DEF_CONTENT(golol_comment_after, lol_html_comment_t, lol_html_comment_after)
GOLOL_DEF_CONTENT(golol_comment_replace, lol_html_comment_t, lol_html_comment_replace)

GOLOL_DEF_CONTENT(golol_element_before, lol_html_element_t, lol_html_element_before)
GOLOL_DEF_CONTENT(golol_element_prepend, lol_html_element_t, lol_html_element_prepend)
GOLOL_DEF_CONTENT(golol_element_append, lol_html_element_t, lol_html_element_append)
GOLOL_DEF_CONTENT(golol_element_after, lol_html_element_t, lol_html_element_after)
GOLOL_DEF_CONTENT(golol_element_set_inner_content, lol_html_element_t,
                  lol_html_element_set_inner_content)
GOLOL_DEF_CONTENT(golol_element_replace, lol_html_element_t, lol_html_element_replace)

GOLOL_DEF_CONTENT(golol_end_tag_before, lol_html_end_tag_t, lol_html_end_tag_before)
GOLOL_DEF_CONTENT(golol_end_tag_after, lol_html_end_tag_t, lol_html_end_tag_after)

GOLOL_DEF_CONTENT(golol_text_chunk_before, lol_html_text_chunk_t, lol_html_text_chunk_before)
GOLOL_DEF_CONTENT(golol_text_chunk_after, lol_html_text_chunk_t, lol_html_text_chunk_after)
GOLOL_DEF_CONTENT(golol_text_chunk_replace, lol_html_text_chunk_t, lol_html_text_chunk_replace)

GOLOL_DEF_CONTENT(golol_doc_end_append, lol_html_doc_end_t, lol_html_doc_end_append)

// Streaming content insertion ------------------------------------------------

// The handler struct is copied by lol-html, so a stack temporary is correct
// (and is what the header recommends).
#define GOLOL_DEF_STREAMING(name, unit_t, cfn)                                \
    int name(unit_t *unit, uintptr_t handle, lol_html_str_t *err) {            \
        lol_html_streaming_handler_t h;                                        \
        h.user_data = (void *)handle;                                          \
        h.write_all_callback = streaming_write_trampoline;                     \
        h.drop_callback = streaming_drop_trampoline;                           \
        h.reserved = NULL;                                                     \
        int rc = cfn(unit, &h);                                                \
        if (rc != 0) *err = lol_html_take_last_error();                        \
        return rc;                                                             \
    }

GOLOL_DEF_STREAMING(golol_element_streaming_prepend, lol_html_element_t,
                    lol_html_element_streaming_prepend)
GOLOL_DEF_STREAMING(golol_element_streaming_append, lol_html_element_t,
                    lol_html_element_streaming_append)
GOLOL_DEF_STREAMING(golol_element_streaming_before, lol_html_element_t,
                    lol_html_element_streaming_before)
GOLOL_DEF_STREAMING(golol_element_streaming_after, lol_html_element_t,
                    lol_html_element_streaming_after)
GOLOL_DEF_STREAMING(golol_element_streaming_set_inner_content, lol_html_element_t,
                    lol_html_element_streaming_set_inner_content)
GOLOL_DEF_STREAMING(golol_element_streaming_replace, lol_html_element_t,
                    lol_html_element_streaming_replace)

GOLOL_DEF_STREAMING(golol_end_tag_streaming_before, lol_html_end_tag_t,
                    lol_html_end_tag_streaming_before)
GOLOL_DEF_STREAMING(golol_end_tag_streaming_after, lol_html_end_tag_t,
                    lol_html_end_tag_streaming_after)
GOLOL_DEF_STREAMING(golol_end_tag_streaming_replace, lol_html_end_tag_t,
                    lol_html_end_tag_streaming_replace)

GOLOL_DEF_STREAMING(golol_text_chunk_streaming_before, lol_html_text_chunk_t,
                    lol_html_text_chunk_streaming_before)
GOLOL_DEF_STREAMING(golol_text_chunk_streaming_after, lol_html_text_chunk_t,
                    lol_html_text_chunk_streaming_after)
GOLOL_DEF_STREAMING(golol_text_chunk_streaming_replace, lol_html_text_chunk_t,
                    lol_html_text_chunk_streaming_replace)

int golol_sink_write_str(lol_html_streaming_sink_t *sink, const char *s,
                         size_t len, bool is_html, lol_html_str_t *err) {
    int rc = lol_html_streaming_sink_write_str(sink, s, len, is_html);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_sink_write_utf8_chunk(lol_html_streaming_sink_t *sink, const char *s,
                                size_t len, bool is_html, lol_html_str_t *err) {
    int rc = lol_html_streaming_sink_write_utf8_chunk(sink, s, len, is_html);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

// Named mutations ------------------------------------------------------------

int golol_comment_text_set(lol_html_comment_t *c, const char *text, size_t len,
                           lol_html_str_t *err) {
    int rc = lol_html_comment_text_set(c, text, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_element_tag_name_set(lol_html_element_t *el, const char *name,
                               size_t len, lol_html_str_t *err) {
    int rc = lol_html_element_tag_name_set(el, name, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_end_tag_name_set(lol_html_end_tag_t *et, const char *name, size_t len,
                           lol_html_str_t *err) {
    int rc = lol_html_end_tag_name_set(et, name, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

// Returns 1 (has), 0 (has not) or -1 (error); only -1 sets *err.
int golol_element_has_attribute(const lol_html_element_t *el, const char *name,
                                size_t len, lol_html_str_t *err) {
    int rc = lol_html_element_has_attribute(el, name, len);
    if (rc < 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_element_set_attribute(lol_html_element_t *el, const char *name, size_t nl,
                                const char *value, size_t vl, lol_html_str_t *err) {
    int rc = lol_html_element_set_attribute(el, name, nl, value, vl);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_element_remove_attribute(lol_html_element_t *el, const char *name,
                                   size_t len, lol_html_str_t *err) {
    int rc = lol_html_element_remove_attribute(el, name, len);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

int golol_element_add_end_tag_handler(lol_html_element_t *el, uintptr_t handle,
                                      lol_html_str_t *err) {
    int rc = lol_html_element_add_end_tag_handler(el, end_tag_trampoline, (void *)handle);
    if (rc != 0) *err = lol_html_take_last_error();
    return rc;
}

// User data ------------------------------------------------------------------

#define GOLOL_DEF_USER_DATA(unit_name, unit_t, setter, getter)                \
    void golol_##unit_name##_user_data_set(unit_t *unit, uintptr_t handle) {  \
        setter(unit, (void *)handle);                                          \
    }                                                                          \
    uintptr_t golol_##unit_name##_user_data_get(const unit_t *unit) {          \
        return (uintptr_t)getter(unit);                                        \
    }

GOLOL_DEF_USER_DATA(element, lol_html_element_t,
                    lol_html_element_user_data_set, lol_html_element_user_data_get)
GOLOL_DEF_USER_DATA(comment, lol_html_comment_t,
                    lol_html_comment_user_data_set, lol_html_comment_user_data_get)
GOLOL_DEF_USER_DATA(text_chunk, lol_html_text_chunk_t,
                    lol_html_text_chunk_user_data_set, lol_html_text_chunk_user_data_get)
GOLOL_DEF_USER_DATA(doctype, lol_html_doctype_t,
                    lol_html_doctype_user_data_set, lol_html_doctype_user_data_get)

package lolhtml

/*
#include "shim.h"
*/
import "C"

// A DocumentEnd marks the end of the document, and exists so a handler can
// append trailing content after everything else - an injected script, a
// closing comment, a summary built up while rewriting.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type DocumentEnd struct {
	unit[*C.lol_html_doc_end_t]
}

// Append adds content at the very end of the document.
func (d *DocumentEnd) Append(content string, ct ContentType) error {
	p, err := d.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), "doc_end_append", cfDocEndAppend)
}

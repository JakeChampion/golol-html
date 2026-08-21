package lolhtml

/*
#include "shim.h"
*/
import "C"

// A Doctype is the document type declaration, as in <!DOCTYPE html>.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type Doctype struct {
	unit[*C.lol_html_doctype_t]
}

// Name returns the doctype name, such as "html". The second result is false
// when the declaration has no name.
func (d *Doctype) Name() (string, bool) {
	p, err := d.live()
	if err != nil {
		return "", false
	}
	return takeOptStr(C.lol_html_doctype_name_get(p))
}

// PublicID returns the PUBLIC identifier. The second result is false when there
// is none, which is the case for the modern <!DOCTYPE html>.
func (d *Doctype) PublicID() (string, bool) {
	p, err := d.live()
	if err != nil {
		return "", false
	}
	return takeOptStr(C.lol_html_doctype_public_id_get(p))
}

// SystemID returns the SYSTEM identifier. The second result is false when there
// is none.
func (d *Doctype) SystemID() (string, bool) {
	p, err := d.live()
	if err != nil {
		return "", false
	}
	return takeOptStr(C.lol_html_doctype_system_id_get(p))
}

// SourceLocation returns the byte range the declaration occupied in the input.
func (d *Doctype) SourceLocation() SourceLocation {
	p, err := d.live()
	if err != nil {
		return SourceLocation{}
	}
	return sourceLocation(C.lol_html_doctype_source_location_bytes(p))
}

// Remove removes the declaration from the output.
func (d *Doctype) Remove() {
	if p, err := d.live(); err == nil {
		C.lol_html_doctype_remove(p)
	}
}

// IsRemoved reports whether the declaration has been removed by a handler.
func (d *Doctype) IsRemoved() bool {
	p, err := d.live()
	if err != nil {
		return false
	}
	return bool(C.lol_html_doctype_is_removed(p))
}

var doctypeUserData = userDataAccessor[*C.lol_html_doctype_t]{
	get: func(p *C.lol_html_doctype_t) C.uintptr_t { return C.golol_doctype_user_data_get(p) },
	set: func(p *C.lol_html_doctype_t, h C.uintptr_t) { C.golol_doctype_user_data_set(p, h) },
}

// UserData returns the value most recently attached by SetUserData, or nil.
func (d *Doctype) UserData() any { return getUserData(&d.unit, doctypeUserData) }

// SetUserData attaches a value to the declaration. Go handlers can usually
// close over the value instead.
func (d *Doctype) SetUserData(v any) error { return setUserData(&d.unit, doctypeUserData, v) }

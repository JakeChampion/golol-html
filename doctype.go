package lolhtml

/*
#include "shim.h"
*/
import "C"

// A Doctype is the document type declaration, as in <!DOCTYPE html>.
//
// It can be read and it can be removed. There is no way to write one: this type
// has no Before, After or Replace, because lol-html offers none, so a rewrite that
// wants to change a legacy declaration into <!DOCTYPE html> has to remove the old
// one and insert the new one somewhere else - and the only place available is
// before the first element:
//
//	pending := false
//	lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
//		d.Remove()
//		pending = true
//		return nil
//	}),
//	lolhtml.OnElement("*", func(e *lolhtml.Element) error {
//		if !pending {
//			return nil
//		}
//		pending = false
//		return e.Before("<!DOCTYPE html>", lolhtml.HTML)
//	})
//
// That works on an ordinary document and fails silently on three shapes, measured
// against golang.org/x/net/html in differential/doctype_test.go:
//
//	<!DOCTYPE …><html>…            upgraded
//	<!DOCTYPE …><!--c--><html>…    upgraded: a comment before a doctype is allowed
//	<!DOCTYPE …>   <html>…         upgraded: so is whitespace
//	<!DOCTYPE …>text<html>…        the new one lands after text, and a parser
//	                               ignores a doctype there: quirks mode
//	<!DOCTYPE …>                   nothing to insert before: quirks mode
//	<!DOCTYPE …>just text          the same
//
// Adding one without removing the old is not an alternative: a second DOCTYPE is a
// parse error and dropped, so the legacy declaration still applies.
//
// So the decision to remove has to be made before the place to put the replacement
// is known, and nothing in the doctype handler can know it. A rewrite that must be
// right about this has to read the document twice - the first pass answering "is
// there an element before any text" - or leave the declaration alone.
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
//
// Which is the largest rendering change any single token removal can make: a
// document with no doctype is in quirks mode, where the box model, table cell
// heights, line heights and a dozen other things differ. Nothing warns, and the
// diff is one deleted token.
//
// See [Doctype] for the other half - that there is no way to write one back, and
// what that costs a rewrite that wants to upgrade a legacy declaration rather than
// delete it.
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

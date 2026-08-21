package lolhtml

/*
#include "shim.h"
*/
import "C"

import "runtime/cgo"

// unit is the shared base of every rewritable unit wrapper.
//
// ptr is cleared when the owning handler returns, because lol-html only
// guarantees the pointer for that window. Every method must therefore go
// through live() rather than touching ptr directly.
//
// The parameter is the pointer type, not the pointee: lol-html's structs are
// opaque, and Go generics reject an incomplete type as a type argument, but a
// pointer to one is complete and comparable.
type unit[P comparable] struct {
	ptr P
	c   *core
}

func (u *unit[P]) detach() {
	var zero P
	u.ptr = zero
}

// live returns the C pointer, or ErrDetached if the handler has returned.
func (u *unit[P]) live() (P, error) {
	var zero P
	if u.ptr == zero {
		return zero, ErrDetached
	}
	return u.ptr, nil
}

// Detached reports whether this value has outlived its handler. Every other
// method returns ErrDetached, or a zero value, once this is true.
func (u *unit[P]) Detached() bool {
	var zero P
	return u.ptr == zero
}

// User data ------------------------------------------------------------------
//
// lol-html carries an opaque pointer per unit so that handlers seeing the same
// unit can share state. In Go this is rarely needed - a closure captures
// whatever the handler wants, and an end-tag handler registered from inside an
// element handler simply closes over it - but it is part of the API surface, so
// it is exposed here.

type userDataAccessor[P comparable] struct {
	get func(P) C.uintptr_t
	set func(P, C.uintptr_t)
}

func getUserData[P comparable](u *unit[P], a userDataAccessor[P]) any {
	p, err := u.live()
	if err != nil {
		return nil
	}
	h := a.get(p)
	if h == 0 {
		return nil
	}
	return cgo.Handle(uintptr(h)).Value()
}

func setUserData[P comparable](u *unit[P], a userDataAccessor[P], v any) error {
	p, err := u.live()
	if err != nil {
		return err
	}

	if old := a.get(p); old != 0 {
		u.c.nt.dropUserData(cgo.Handle(uintptr(old)))
	}
	if v == nil {
		a.set(p, 0)
		return nil
	}
	a.set(p, C.uintptr_t(u.c.nt.newUserData(v)))
	return nil
}

// newUserData registers a user-data payload. These are tracked separately from
// handler handles because they can be replaced during a rewrite, and a handle
// must be deleted exactly once.
func (n *native) newUserData(v any) cgo.Handle {
	h := newHandle(v)
	if n.userData == nil {
		n.userData = make(map[cgo.Handle]struct{})
	}
	n.userData[h] = struct{}{}
	return h
}

func (n *native) dropUserData(h cgo.Handle) {
	if _, ok := n.userData[h]; !ok {
		// Not ours to free: another rewriter, or already replaced.
		return
	}
	delete(n.userData, h)
	deleteHandle(h)
}

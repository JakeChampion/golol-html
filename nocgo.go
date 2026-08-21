//go:build !cgo

package lolhtml

// lol-html is a Rust library reached through cgo, so there is nothing to fall
// back to when cgo is disabled. Without this file the package would have no
// buildable Go files at all and the error would be an unhelpful "build
// constraints exclude all Go files".
//
// A pure-Go backend would mean hosting lol-html as WebAssembly; see the README.
var _ = golol_html_requires_cgo_so_CGO_ENABLED_must_not_be_0

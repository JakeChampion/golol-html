//go:build !asan

package lolhtml_test

// asanEnabled reports whether this binary was built with -asan.
//
// Go defines the asan build constraint for a sanitized build, which is the only
// reliable way to ask: there is no runtime flag, and a test cannot see the
// command line it was compiled with.
const asanEnabled = false

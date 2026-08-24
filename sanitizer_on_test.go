//go:build asan

package lolhtml_test

// asanEnabled reports whether this binary was built with -asan. See the !asan
// file of the same name for why this is a build constraint and not a check.
const asanEnabled = true

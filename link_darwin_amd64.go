//go:build darwin && amd64

package lolhtml

// rustc --print native-static-libs also lists -lSystem -lc -lm, but macOS links
// libSystem implicitly and naming it again makes ld warn about a duplicate
// library on every consumer build. Measured: with just -liconv the build is
// clean, and so is a build with no flags at all. -liconv is kept as the explicit
// form in case std starts referencing it.
//
// The blank line below matters: cgo treats the comment block immediately
// preceding import "C" as the C preamble, so prose must be separated from it.

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/darwin_amd64/liblolhtml.a -liconv
import "C"

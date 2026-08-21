//go:build cgo && !(darwin && arm64) && !(linux && amd64) && !(linux && arm64)

package lolhtml

// No liblolhtml.a is vendored for this platform, so fail during type-checking
// with a name that explains itself, rather than letting the build reach an
// opaque "library not found" from the linker.
//
// Supported: darwin/arm64, linux/amd64, linux/arm64.
// To add a platform, add a target to scripts/build-native.sh and a matching
// link_<goos>_<goarch>.go.
var _ = golol_html_has_no_prebuilt_library_for_this_GOOS_GOARCH

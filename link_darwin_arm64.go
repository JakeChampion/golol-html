//go:build darwin && arm64

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/darwin_arm64/liblolhtml.a
import "C"

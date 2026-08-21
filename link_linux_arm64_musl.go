//go:build linux && arm64 && musl

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_arm64_musl/liblolhtml.a -lc
import "C"

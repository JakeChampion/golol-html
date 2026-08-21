//go:build linux && amd64 && musl

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_amd64_musl/liblolhtml.a -lc
import "C"

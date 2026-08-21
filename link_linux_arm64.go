//go:build linux && arm64

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_arm64/liblolhtml.a -lm -ldl -lpthread
import "C"

//go:build linux && arm64 && !musl

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_arm64/liblolhtml.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
import "C"

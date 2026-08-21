//go:build linux && amd64 && !musl

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_amd64/liblolhtml.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
import "C"

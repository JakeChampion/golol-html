//go:build linux && amd64

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/linux_amd64/liblolhtml.a -lm -ldl -lpthread
import "C"

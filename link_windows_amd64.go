//go:build windows && amd64

package lolhtml

// #cgo LDFLAGS: ${SRCDIR}/internal/lib/windows_amd64/liblolhtml.a -lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp
import "C"

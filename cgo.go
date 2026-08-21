package lolhtml

/*
#cgo CFLAGS: -I${SRCDIR}/internal/include
#include "shim.h"
#include <stdlib.h>
*/
import "C"

// This file carries the C include path shared by the whole package. Per-platform
// linker flags live in the link_<goos>_<goarch>.go files, each of which selects
// the prebuilt liblolhtml.a vendored under internal/lib.

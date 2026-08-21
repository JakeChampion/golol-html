package lolhtml

// Go build constraints cannot tell glibc from musl: both are linux/amd64. The
// two C libraries need different archives and different linker flags, so which
// one to use has to be stated explicitly, with the `musl` build tag:
//
//	go build -tags musl ./...
//
// Without it a build on Alpine picks the glibc archive and fails at link time
// with missing glibc symbols, which is a confusing way to find out. Passing the
// tag on a glibc system is the mirror image, and equally loud.
//
// The musl archives need no linker flags beyond -lc. rustc --print
// native-static-libs also lists -lunwind, but the c-api crate is built with
// panic = "abort", so nothing references the unwinder; Alpine does not ship
// libunwind by default, and requiring it would be a burden for no benefit. The
// Alpine job in CI is what holds this to account.

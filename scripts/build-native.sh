#!/usr/bin/env bash
# Build the vendored liblolhtml.a archives from a pinned lol-html release.
#
# The archives under internal/lib are what let consumers `go get` this module
# without a Rust toolchain. They are rebuilt only when LOL_HTML_REF changes, by
# CI, and the resulting SHA256SUMS is committed alongside them so anyone can
# reproduce and diff.
#
# Usage:
#   scripts/build-native.sh                  # host platform only
#   scripts/build-native.sh linux_amd64      # one named target
#   scripts/build-native.sh --all            # every supported target
#   scripts/build-native.sh --verify         # rebuild host target, diff, restore
#
# Builds are `cargo rustc --crate-type staticlib`, not `cargo build`. The c-api
# crate declares staticlib, cdylib and rlib; restricting it to the one we need
# lets LTO prune far harder and removes the need for a linker for the target,
# so cross-building works with nothing but `rustup target add`. Measured:
#
#   darwin/arm64   cargo build + strip   15.57 MB
#                  cargo rustc + strip    2.73 MB
#   linux/amd64    cargo build + strip   18.31 MB
#                  cargo rustc, unstripped 8.98 MB
#
# The smaller archive also links a smaller binary (8.85 MB against 10.04 MB for
# the example), because the pruning happens before the Go linker sees it.
#
# Verify a result with Apple's /usr/bin/nm, not a GNU nm: binutils cannot read
# the LTO objects and reports zero symbols instead of an error you would
# notice. Expect 96 `T lol_html_*` entry points plus the `unstable_` one.
set -euo pipefail

# Pinned upstream: lol-html v3.0.1 (c-api crate 1.4.0).
LOL_HTML_REPO="${LOL_HTML_REPO:-https://github.com/cloudflare/lol-html.git}"
LOL_HTML_REF="${LOL_HTML_REF:-608cc4a66b7ab4fcbe1bbdeb25df8f265572b11c}"

# The c-api crate declares rust-version = 1.89 and edition 2024.
RUST_TOOLCHAIN="${RUST_TOOLCHAIN:-1.95.0}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="${repo_root}/.native-build"

# go target -> rust target triple
rust_target_for() {
    case "$1" in
        darwin_arm64) echo aarch64-apple-darwin ;;
        darwin_amd64) echo x86_64-apple-darwin ;;
        linux_amd64)  echo x86_64-unknown-linux-gnu ;;
        linux_arm64)  echo aarch64-unknown-linux-gnu ;;
        *) echo "unsupported target: $1" >&2; return 1 ;;
    esac
}

ALL_TARGETS=(darwin_arm64 linux_amd64 linux_arm64)

host_target() {
    local os arch
    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux)  os=linux ;;
        *) echo "unsupported host OS: $(uname -s)" >&2; return 1 ;;
    esac
    case "$(uname -m)" in
        arm64|aarch64) arch=arm64 ;;
        x86_64|amd64)  arch=amd64 ;;
        *) echo "unsupported host arch: $(uname -m)" >&2; return 1 ;;
    esac
    echo "${os}_${arch}"
}

fetch_source() {
    if [[ ! -d "${work}/lol-html/.git" ]]; then
        mkdir -p "${work}"
        git clone --no-checkout "${LOL_HTML_REPO}" "${work}/lol-html"
    fi
    git -C "${work}/lol-html" fetch --depth 1 origin "${LOL_HTML_REF}" 2>/dev/null \
        || git -C "${work}/lol-html" fetch origin
    git -C "${work}/lol-html" checkout --force "${LOL_HTML_REF}"
}

build_target() {
    local go_target="$1"
    local rust_target
    rust_target="$(rust_target_for "${go_target}")"

    echo "==> building ${go_target} (${rust_target})"
    rustup target add --toolchain "${RUST_TOOLCHAIN}" "${rust_target}"

    ( cd "${work}/lol-html/c-api" \
      && cargo "+${RUST_TOOLCHAIN}" rustc --release --target "${rust_target}" \
             --crate-type staticlib )

    local built="${work}/lol-html/c-api/target/${rust_target}/release/liblolhtml.a"
    local dest="${repo_root}/internal/lib/${go_target}"
    mkdir -p "${dest}"
    cp "${built}" "${dest}/liblolhtml.a"

    # Strip debug and local symbols; still worth it after the crate-type
    # change. Measured on darwin/arm64: 6.51 MB -> 2.73 MB, no effect on
    # linking.
    case "${go_target}" in
        darwin_*) /usr/bin/strip -S -x "${dest}/liblolhtml.a" ;;
        linux_*)  "${STRIP:-strip}" --strip-debug "${dest}/liblolhtml.a" ;;
    esac

    echo "    $(du -h "${dest}/liblolhtml.a" | cut -f1)  ${go_target}/liblolhtml.a"
}

sync_header() {
    cp "${work}/lol-html/c-api/include/lol_html.h" "${repo_root}/internal/include/lol_html.h"
    cp "${work}/lol-html/LICENSE" "${repo_root}/LICENSE-lol-html"
}

# sha256sum on Linux, shasum on macOS. The output format is identical, so
# either tool can check a file the other produced.
sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

write_sums() {
    local tool=(shasum -a 256)
    command -v sha256sum >/dev/null && tool=(sha256sum)
    ( cd "${repo_root}/internal/lib" \
      && find . -name 'liblolhtml.a' | sort | xargs "${tool[@]}" > SHA256SUMS )
    echo "==> internal/lib/SHA256SUMS updated"
}

case "${1:-}" in
    --all)
        fetch_source; sync_header
        for t in "${ALL_TARGETS[@]}"; do build_target "$t"; done
        write_sums
        ;;
    --verify)
        target="$(host_target)"
        before="$(sha256 "${repo_root}/internal/lib/${target}/liblolhtml.a" | cut -d' ' -f1)"
        fetch_source; sync_header; build_target "${target}"
        after="$(sha256 "${repo_root}/internal/lib/${target}/liblolhtml.a" | cut -d' ' -f1)"
        if [[ "${before}" == "${after}" ]]; then
            echo "==> ${target} reproduced exactly: ${after}"
        else
            echo "==> ${target} DIFFERS" >&2
            echo "    committed: ${before}" >&2
            echo "    rebuilt:   ${after}" >&2
            echo "    Rust builds are not bit-reproducible across toolchain patch" >&2
            echo "    versions; compare with RUST_TOOLCHAIN=${RUST_TOOLCHAIN} before" >&2
            echo "    concluding the archive was tampered with." >&2
            exit 1
        fi
        ;;
    "")
        fetch_source; sync_header; build_target "$(host_target)"; write_sums
        ;;
    *)
        fetch_source; sync_header; build_target "$1"; write_sums
        ;;
esac

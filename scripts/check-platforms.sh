#!/usr/bin/env bash
# Check that every supported platform selects a link file, and that no supported
# platform falls through to the unsupported-platform guard.
#
# This exists because the guard's build constraint and the set of link files are
# two lists that must agree, and nothing else notices when they drift. They did
# drift once: unsupported.go still excluded only the original three platforms
# while its own comment claimed seven, so a Windows build failed with
# "undefined: golol_html_has_no_prebuilt_library_for_this_GOOS_GOARCH" instead
# of linking. go list resolves build constraints without compiling, so this
# works for every platform from any host.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# goos goarch tags expected-link-file
PLATFORMS=(
    "darwin  arm64 ''     link_darwin_arm64.go"
    "darwin  amd64 ''     link_darwin_amd64.go"
    "linux   amd64 ''     link_linux_amd64.go"
    "linux   arm64 ''     link_linux_arm64.go"
    "linux   amd64 musl   link_linux_amd64_musl.go"
    "linux   arm64 musl   link_linux_arm64_musl.go"
    "windows amd64 ''     link_windows_amd64.go"
)

fail=0

for row in "${PLATFORMS[@]}"; do
    read -r goos goarch tags want <<<"${row}"
    tags="${tags//\'/}"

    # Link files import "C", so they land in CgoFiles, not GoFiles. The guards
    # do not, so both lists are needed.
    files="$(GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=1 \
        go list ${tags:+-tags "${tags}"} -f '{{join .CgoFiles " "}} {{join .GoFiles " "}}' . 2>/dev/null || true)"

    label="${goos}/${goarch}${tags:+ -tags ${tags}}"

    if [[ -z "${files}" ]]; then
        echo "FAIL ${label}: go list produced nothing"
        fail=1
        continue
    fi
    if [[ " ${files} " != *" ${want} "* ]]; then
        echo "FAIL ${label}: expected ${want} to be selected; got: ${files}"
        fail=1
        continue
    fi
    if [[ " ${files} " == *" unsupported.go "* ]]; then
        echo "FAIL ${label}: falls through to the unsupported-platform guard"
        fail=1
        continue
    fi
    if [[ " ${files} " == *" nocgo.go "* ]]; then
        echo "FAIL ${label}: selected the cgo-disabled guard"
        fail=1
        continue
    fi
    echo "ok   ${label} -> ${want}"
done

# And the guards must still fire where they should.
unsup="$(GOOS=linux GOARCH=riscv64 CGO_ENABLED=1 go list -f '{{join .CgoFiles " "}} {{join .GoFiles " "}}' . 2>/dev/null || true)"
if [[ " ${unsup} " != *" unsupported.go "* ]]; then
    echo "FAIL linux/riscv64: expected the unsupported-platform guard, got: ${unsup}"
    fail=1
else
    echo "ok   linux/riscv64 -> unsupported.go (as intended)"
fi

nocgo="$(CGO_ENABLED=0 go list -f '{{join .CgoFiles " "}} {{join .GoFiles " "}}' . 2>/dev/null || true)"
if [[ " ${nocgo} " != *" nocgo.go "* ]]; then
    echo "FAIL CGO_ENABLED=0: expected the cgo-disabled guard, got: ${nocgo}"
    fail=1
else
    echo "ok   CGO_ENABLED=0 -> nocgo.go (as intended)"
fi

exit "${fail}"

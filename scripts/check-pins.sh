#!/usr/bin/env bash
# The pinned lol-html revision has one home, and everything else agrees with it.
#
# scripts/build-native.sh holds the pin and the Rust toolchain; native.yml reads
# both from it with --print-pins rather than keeping its own copy, because the
# two copies could disagree and nothing said so: the workflow would rebuild the
# old revision while `make native` built the new one, and `make verify` would
# then report a hash mismatch that reads exactly like tampering.
#
# What is left to check is the prose. SPEC.md states the pin for a reader, which
# is worth keeping and cannot be generated, so it is checked instead - and no
# workflow may quietly grow a hard-coded copy again. docs/gip/wontfix.md (W7)
# states the pin as a ruling and docs/provenance.md as the revision its whole
# argument is about, so a bump that left either behind would have the settled
# record and the provenance story both quoting a revision nothing ships.
#
# Deliberately dependency-free, like check-platforms.sh, check-workflows.sh and
# check-modules.sh: it greps, it does not parse YAML.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0

pins=$(scripts/build-native.sh --print-pins)
ref=${pins#*lol_html_ref=}
ref=${ref%%$'\n'*}
toolchain=${pins##*rust_toolchain=}

if [[ -z "${ref}" || -z "${toolchain}" ]]; then
    echo "FAIL scripts/build-native.sh --print-pins printed no pin"
    exit 1
fi

for doc in SPEC.md docs/gip/wontfix.md docs/provenance.md; do
    if ! grep -qF "${ref}" "${doc}"; then
        echo "FAIL ${doc} does not name the pinned lol-html revision ${ref}"
        echo "     scripts/build-native.sh is the source of truth; update ${doc} to match"
        fail=1
    fi
done

# provenance.md also quotes --print-pins' toolchain line, and its rustc
# fingerprints are only evidence for the toolchain that is actually pinned.
if ! grep -qF "rust_toolchain=${toolchain}" docs/provenance.md; then
    echo "FAIL docs/provenance.md does not name the pinned Rust toolchain ${toolchain}"
    echo "     scripts/build-native.sh is the source of truth; update docs/provenance.md to match"
    fail=1
fi

shopt -s nullglob
for f in .github/workflows/*.yml .github/workflows/*.yaml; do
    if grep -qF "${ref}" "$f"; then
        echo "FAIL ${f} hard-codes the lol-html revision"
        echo "     read it from scripts/build-native.sh --print-pins instead"
        fail=1
    fi
    if grep -qE "(^|[^0-9.])${toolchain//./\\.}([^0-9.]|$)" "$f"; then
        echo "FAIL ${f} hard-codes the Rust toolchain ${toolchain}"
        echo "     read it from scripts/build-native.sh --print-pins instead"
        fail=1
    fi
done

if [[ ${fail} -eq 0 ]]; then
    echo "the lol-html pin (${ref}) and Rust toolchain (${toolchain}) have one home"
fi
exit "${fail}"
